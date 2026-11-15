package platform

// S4-GAP 会话系统 (单管理员强化): server-side admin sessions with per-session
// revocation, sliding renewal and logout.
//
//   - JWT claims carry sid (session id) + jti (per-token id). jwtGuard /
//     AuthAny verify the sid is still active (memory cache + store).
//   - Tokens issued before S4 carry no sid and stay valid until their own
//     TTL (兼容), still subject to the token_version global invalidation.
//   - POST /api/auth/logout revokes the current session and clears the
//     httpOnly cookie; POST /api/auth/refresh slides the expiry forward.
//   - Password change bumps token_version (全局失效) AND revokes every stored
//     session.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// AdminSession is one stored admin login session. IsCurrent is derived per
// listing request and never persisted with a true value.
type AdminSession struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"user_agent"`
	Revoked      bool      `json:"revoked"`
	// Current marks the session of the requesting admin (listing only).
	Current bool `json:"current,omitempty"`
}

// sessionTouchInterval throttles LastActiveAt store writes: a burst of admin
// API calls updates the session record at most once per interval.
const sessionTouchInterval = time.Minute

// newSessionID returns a 256-bit random hex session id (also used as JWT jti).
func newSessionID() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is fatal-grade in practice; fall back to a
		// time-derived id rather than crashing the login path.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf)
}

// sessionCache is the in-memory view of the sessions bucket: active IDs with
// their expiry, plus a revoked set so a revocation answers instantly even
// when the store lookup would race.
type sessionCache struct {
	mu      sync.Mutex
	known   map[string]time.Time // sid -> expiry (active sessions seen recently)
	revoked map[string]bool
}

func newSessionCache() *sessionCache {
	return &sessionCache{known: map[string]time.Time{}, revoked: map[string]bool{}}
}

// ---------------------------------------------------------------------------
// Platform session operations
// ---------------------------------------------------------------------------

// createSession stores a fresh session for the admin login and returns it.
func (p *Platform) createSession(ip, userAgent string, ttl time.Duration) (*AdminSession, error) {
	now := time.Now()
	s := &AdminSession{
		ID:           newSessionID(),
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(ttl),
		IP:           ip,
		UserAgent:    userAgent,
	}
	if err := p.store.PutSession(s); err != nil {
		return nil, err
	}
	p.sessionMu.Lock()
	p.sessions.known[s.ID] = s.ExpiresAt
	delete(p.sessions.revoked, s.ID)
	p.sessionMu.Unlock()
	return s, nil
}

// sessionActive reports whether sid is an active (non-revoked, non-expired)
// session. Memory cache first, store as the source of truth.
func (p *Platform) sessionActive(sid string) bool {
	if sid == "" {
		return false
	}
	now := time.Now()
	p.sessionMu.Lock()
	if p.sessions.revoked[sid] {
		p.sessionMu.Unlock()
		return false
	}
	if exp, ok := p.sessions.known[sid]; ok && now.Before(exp) {
		p.sessionMu.Unlock()
		return true
	}
	p.sessionMu.Unlock()

	s, err := p.store.GetSession(sid)
	if err != nil || s == nil || s.Revoked || now.After(s.ExpiresAt) {
		p.sessionMu.Lock()
		p.sessions.revoked[sid] = true
		p.sessionMu.Unlock()
		return false
	}
	p.sessionMu.Lock()
	p.sessions.known[sid] = s.ExpiresAt
	delete(p.sessions.revoked, sid)
	p.sessionMu.Unlock()
	return true
}

// touchSession slides LastActiveAt forward (throttled to sessionTouchInterval)
// and, when slide is true, extends ExpiresAt (POST /api/auth/refresh).
func (p *Platform) touchSession(sid string, slide bool) {
	if sid == "" {
		return
	}
	s, err := p.store.GetSession(sid)
	if err != nil || s == nil || s.Revoked {
		return
	}
	now := time.Now()
	if !slide && now.Sub(s.LastActiveAt) < sessionTouchInterval {
		return
	}
	s.LastActiveAt = now
	if slide {
		s.ExpiresAt = now.Add(p.jwtTTL())
	}
	if err := p.store.PutSession(s); err != nil {
		logrus.WithError(err).Warn("platform: 更新会话活跃时间失败")
		return
	}
	if slide {
		p.sessionMu.Lock()
		p.sessions.known[s.ID] = s.ExpiresAt
		p.sessionMu.Unlock()
	}
}

// revokeSession marks one session revoked (single-session logout/撤销).
func (p *Platform) revokeSession(sid string) (bool, error) {
	if sid == "" {
		return false, nil
	}
	p.sessionMu.Lock()
	p.sessions.revoked[sid] = true
	delete(p.sessions.known, sid)
	p.sessionMu.Unlock()
	s, err := p.store.GetSession(sid)
	if err != nil || s == nil {
		return false, err
	}
	if s.Revoked {
		return true, nil
	}
	s.Revoked = true
	return true, p.store.PutSession(s)
}

// revokeOtherSessions revokes every session except keepID (撤销其他会话;
// keepID "" = all) and returns how many were revoked.
func (p *Platform) revokeOtherSessions(keepID string) (int, error) {
	sessions, err := p.store.ListSessions()
	if err != nil {
		return 0, err
	}
	now := time.Now()
	revoked := 0
	for i := range sessions {
		s := &sessions[i]
		if s.ID == keepID || s.Revoked || now.After(s.ExpiresAt) {
			continue
		}
		p.sessionMu.Lock()
		p.sessions.revoked[s.ID] = true
		delete(p.sessions.known, s.ID)
		p.sessionMu.Unlock()
		s.Revoked = true
		if err := p.store.PutSession(s); err != nil {
			return revoked, err
		}
		revoked++
	}
	return revoked, nil
}

// revokeAllSessions revokes every stored session (password change).
func (p *Platform) revokeAllSessions() (int, error) {
	return p.revokeOtherSessions("")
}

// listSessions returns active (non-revoked, non-expired) sessions newest
// active first, flagging the requester's own session (constant-time compare).
func (p *Platform) listSessions(currentID string) []AdminSession {
	sessions, err := p.store.ListSessions()
	if err != nil {
		return []AdminSession{}
	}
	now := time.Now()
	out := make([]AdminSession, 0, len(sessions))
	for _, s := range sessions {
		if s.Revoked || now.After(s.ExpiresAt) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	if currentID != "" {
		for i := range out {
			if subtle.ConstantTimeCompare([]byte(out[i].ID), []byte(currentID)) == 1 {
				out[i].Current = true
			}
		}
	}
	return out
}
