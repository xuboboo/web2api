package platform

// IP/CIDR blacklist. The guard is mounted as the first middleware on the whole
// /web-tool group so every plane (site, console, /api/*, /v1/*, /mcp) sees the
// same deny decision. Client IP honors the trusted-proxy X-Real-IP setting
// already configured on the Fiber app (core.Server).

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

const (
	BlacklistKindIP   = "ip"
	BlacklistKindCIDR = "cidr"
)

// BlacklistEntry is one persisted deny rule. ExpiresAt == nil means permanent.
type BlacklistEntry struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Value     string     `json:"value"`
	Reason    string     `json:"reason,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Hits      int64      `json:"hits"`
	LastHitAt *time.Time `json:"last_hit_at,omitempty"`
}

// blacklistView adds a derived expired flag without persisting it.
type blacklistView struct {
	BlacklistEntry
	Expired bool `json:"expired"`
}

func newBlacklistView(e BlacklistEntry, now time.Time) blacklistView {
	return blacklistView{BlacklistEntry: e, Expired: e.ExpiredAt(now)}
}

func (e *BlacklistEntry) ExpiredAt(now time.Time) bool {
	return e.ExpiresAt != nil && !now.Before(*e.ExpiresAt)
}

// parseBlacklistValue validates and canonicalizes one rule. An invalid rule
// returns a natural-language reason suitable for the admin 400 envelope.
func parseBlacklistValue(kind, value string) (canonical string, normalizedKind string, reason string) {
	value = strings.TrimSpace(value)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		// Infer the kind from the value for operator convenience.
		if strings.Contains(value, "/") {
			kind = BlacklistKindCIDR
		} else {
			kind = BlacklistKindIP
		}
	}
	switch kind {
	case BlacklistKindIP:
		ip := net.ParseIP(value)
		if ip == nil {
			return "", "", "IP 地址格式无效"
		}
		return ip.String(), BlacklistKindIP, ""
	case BlacklistKindCIDR:
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return "", "", "CIDR 格式无效(如 203.0.113.0/24)"
		}
		return network.String(), BlacklistKindCIDR, ""
	default:
		return "", "", "黑名单类型无效(仅支持 ip 或 cidr)"
	}
}

// blacklistMatch reports the matching rule for one client IP, ignoring rules
// whose expiry has passed. Entries are few (tens), so a linear scan is enough
// and needs no in-memory cache to invalidate on edits.
func (p *Platform) blacklistMatch(ip string, now time.Time) (*BlacklistEntry, bool) {
	if p.store == nil {
		return nil, false
	}
	entries, err := p.store.ListBlacklist()
	if err != nil {
		logrus.WithError(err).Warn("platform: 读取黑名单失败,本次放行")
		return nil, false
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return nil, false
	}
	for i := range entries {
		e := &entries[i]
		if e.ExpiredAt(now) {
			continue
		}
		switch e.Kind {
		case BlacklistKindIP:
			if net.ParseIP(e.Value) != nil && parsed.Equal(net.ParseIP(e.Value)) {
				return e, true
			}
		case BlacklistKindCIDR:
			if _, network, err := net.ParseCIDR(e.Value); err == nil && network.Contains(parsed) {
				return e, true
			}
		}
	}
	return nil, false
}

// blacklistHitSampleWindow caps how often one entry writes an audit row plus a
// bbolt hit-counter transaction. The 403 itself is answered on every request —
// only the diagnostic bookkeeping is sampled.
const blacklistHitSampleWindow = time.Minute

// blacklistSamplerCap bounds the sampler map; on overflow it resets wholesale
// (under-counting diagnostics, never affecting the deny decision).
const blacklistSamplerCap = 1024

// BlacklistGuard rejects blacklisted clients before any other platform
// middleware runs. Fail-open on storage errors is deliberate: a broken store
// must not take the whole service offline; the error is logged and audited.
//
// The guard runs before every rate limiter (it is the first middleware), so a
// blacklisted client can issue unlimited cheap requests. Writing an audit row
// and a bolt transaction per hit let such a client flood audit.log's rotation
// and evict real evidence, so hit bookkeeping is sampled to one write per
// entry per window while the accumulated count is still recorded.
func (p *Platform) BlacklistGuard() fiber.Handler {
	type hitStamp struct {
		writtenAt time.Time
		counted   int64
	}
	var (
		samplerMu sync.Mutex
		sampler   = map[string]*hitStamp{}
	)
	recordHit := func(entryID string) (shouldWrite bool, hitsSince int64) {
		now := time.Now()
		samplerMu.Lock()
		defer samplerMu.Unlock()
		if len(sampler) >= blacklistSamplerCap {
			sampler = map[string]*hitStamp{}
		}
		st := sampler[entryID]
		if st == nil {
			st = &hitStamp{}
			sampler[entryID] = st
		}
		st.counted++
		if st.writtenAt.IsZero() || now.Sub(st.writtenAt) >= blacklistHitSampleWindow {
			st.writtenAt = now
			hits := st.counted
			st.counted = 0
			return true, hits
		}
		return false, 0
	}

	return func(c *fiber.Ctx) error {
		now := time.Now()
		entry, blocked := p.blacklistMatch(c.IP(), now)
		if !blocked {
			return c.Next()
		}
		if shouldWrite, hitsSince := recordHit(entry.ID); shouldWrite {
			p.auditFrom(c, "", AuditActionBlacklistHit, entry.Value, "403")
			// Hit counters are diagnostic; a failed update never blocks the deny.
			if fresh, err := p.store.GetBlacklist(entry.ID); err == nil && fresh != nil {
				fresh.Hits += hitsSince
				hitAt := now
				fresh.LastHitAt = &hitAt
				_ = p.store.PutBlacklist(fresh)
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "forbidden", "reason": "blacklisted",
		})
	}
}

// SeedBlacklist installs optional static rules on first start only. Rules the
// operator later deletes through the console are never re-added, so the panel
// stays the single source of truth after bootstrap.
func (p *Platform) SeedBlacklist(entries []string, actor string) error {
	if p.store == nil {
		return nil
	}
	// One-shot marker: rules deleted through the console must stay deleted.
	if seeded, err := p.store.GetSetting(settingBlacklistSeeded); err != nil {
		return err
	} else if len(seeded) > 0 {
		return nil
	}
	if len(entries) == 0 {
		return p.store.PutSetting(settingBlacklistSeeded, []byte("1"))
	}
	existing, err := p.store.ListBlacklist()
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(existing))
	for _, e := range existing {
		known[e.Kind+":"+e.Value] = true
	}
	for _, raw := range entries {
		value, kind, reason := parseBlacklistValue("", raw)
		if reason != "" {
			logrus.WithField("entry", raw).WithField("reason", reason).Warn("platform: 忽略无效静态黑名单条目")
			continue
		}
		if known[kind+":"+value] {
			continue
		}
		e := &BlacklistEntry{
			ID: newKeyID(), Kind: kind, Value: value,
			Reason: "config 初始条目", CreatedBy: actor, CreatedAt: time.Now(),
		}
		if err := p.store.PutBlacklist(e); err != nil {
			return err
		}
		known[kind+":"+value] = true
	}
	return p.store.PutSetting(settingBlacklistSeeded, []byte("1"))
}

// revokeUserSessions marks every session of one console user revoked.
func (p *Platform) revokeUserSessions(userID string) (int, error) {
	if userID == "" {
		return 0, nil
	}
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	sessions, err := p.store.ListSessions()
	if err != nil {
		return 0, err
	}
	revoked := 0
	for i := range sessions {
		s := &sessions[i]
		if s.UserID != userID || s.Revoked {
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
