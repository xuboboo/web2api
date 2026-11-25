package platform

// S4-GAP 会话系统 (单管理员强化): server-side admin sessions with per-session
// revocation, sliding renewal and logout.
//
//   - JWT claims carry sid (session id) + jti (per-token id). jwtGuard /
//     AuthAny verify the sid is still active (persistent store).
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
	"time"
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
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	if err := p.store.PutSession(s); err != nil {
		return nil, err
	}
	return s, nil
}

// sessionActive validates the persisted session on every authorization check.
func (p *Platform) sessionActive(sid string) bool {
	if sid == "" {
		return false
	}
	s, err := p.store.GetSession(sid)
	return err == nil && s != nil && !s.Revoked && time.Now().Before(s.ExpiresAt)
}

// touchSession serializes read-modify-write with revocation. The boolean
// reports whether the session was active when it was touched.
func (p *Platform) touchSession(sid string, slide bool) (bool, error) {
	if sid == "" {
		return false, nil
	}
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	s, err := p.store.GetSession(sid)
	now := time.Now()
	if err != nil || s == nil || s.Revoked || !now.Before(s.ExpiresAt) {
		return false, err
	}
	if !slide && now.Sub(s.LastActiveAt) < sessionTouchInterval {
		return true, nil
	}
	s.LastActiveAt = now
	if slide {
		s.ExpiresAt = now.Add(p.jwtTTL())
	}
	if err := p.store.PutSession(s); err != nil {
		return false, err
	}
	return true, nil
}

// revokeSession marks one session revoked (single-session logout or revoke).
func (p *Platform) revokeSession(sid string) (bool, error) {
	if sid == "" {
		return false, nil
	}
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	s, err := p.store.GetSession(sid)
	if err != nil || s == nil {
		return false, err
	}
	if !s.Revoked {
		s.Revoked = true
		if err := p.store.PutSession(s); err != nil {
			return false, err
		}
	}
	return true, nil
}

// revokeOtherSessions revokes every active session except keepID.
func (p *Platform) revokeOtherSessions(keepID string) (int, error) {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	sessions, err := p.store.ListSessions()
	if err != nil {
		return 0, err
	}
	now := time.Now()
	revoked := 0
	for i := range sessions {
		s := &sessions[i]
		if s.ID == keepID || s.Revoked || !now.Before(s.ExpiresAt) {
			continue
		}
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
