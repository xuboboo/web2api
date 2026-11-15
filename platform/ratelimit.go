package platform

// S4-B-02 公网面限速与防抓: per-key token buckets for the /v1/* and MCP
// planes, plus a per-IP limiter for requests without valid credentials (防
// key 爆破与反抓放大), and S4-B-05 per-IP buckets for the admin /api/* plane
// (单 IP 失控脚本不再吃光全局预算饿死真管理员).
//
// Tables are bounded: entries idle for limiterIdleTTL are swept lazily on the
// same access path, so no background goroutine is needed. golang.org/x/time/rate
// is already a dependency (engine limiters + admin limiter).

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// limiterIdleTTL bounds how long an idle bucket stays resident.
const limiterIdleTTL = time.Hour

// limiterTable is a keyed set of token buckets with lazy expiry sweeping.
type limiterTable struct {
	mu    sync.Mutex
	m     map[string]*tableEntry
	swept time.Time
}

type tableEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newLimiterTable() *limiterTable {
	return &limiterTable{m: map[string]*tableEntry{}, swept: time.Now()}
}

// allow reports whether one event is permitted for key at the configured
// events/minute rate (burst = max(minBurst, rate/4)). ttl entries idle longer
// than limiterIdleTTL are dropped on access.
func (t *limiterTable) allow(key string, perMinute int, minBurst int) bool {
	if perMinute <= 0 {
		return true // 0 = off
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.swept) > time.Minute {
		for k, e := range t.m {
			if now.Sub(e.lastSeen) > limiterIdleTTL {
				delete(t.m, k)
			}
		}
		t.swept = now
	}
	e, ok := t.m[key]
	if !ok {
		burst := perMinute / 4
		if burst < minBurst {
			burst = minBurst
		}
		if burst > perMinute {
			burst = perMinute
		}
		e = &tableEntry{limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMinute)), burst)}
		t.m[key] = e
	}
	e.lastSeen = now
	return e.limiter.Allow()
}

// retryAfterSecondsForRate renders a sane Retry-After for a per-minute limit.
func retryAfterSecondsForRate(perMinute int) int {
	if perMinute <= 0 {
		return 60
	}
	secs := 60 / perMinute
	if secs < 1 {
		secs = 1
	}
	return secs
}

// ---------------------------------------------------------------------------
// Platform wiring
// ---------------------------------------------------------------------------

// platformRateLimiters bundles the three S4 tables (nil-safe: created with the
// Platform, never nil).
type platformRateLimiters struct {
	keyLimiters    *limiterTable // per API key (billed planes)
	unauthLimiters *limiterTable // per IP without valid credentials (/v1, MCP)
	adminIPLimiters *limiterTable // per IP for /api/* (B-05)
}

func newPlatformRateLimiters() platformRateLimiters {
	return platformRateLimiters{
		keyLimiters:     newLimiterTable(),
		unauthLimiters:  newLimiterTable(),
		adminIPLimiters: newLimiterTable(),
	}
}

// allowKeyRate applies security.api_rate_per_minute (0 = off) to the key.
func (p *Platform) allowKeyRate(keyID string) bool {
	sec := p.securityValue()
	if sec.APIRatePerMinute <= 0 {
		return true
	}
	return p.ratelimits.keyLimiters.allow(keyID, sec.APIRatePerMinute, 10)
}

// allowUnauthRate applies security.unauth_rate_per_minute (0 = off) per client
// IP to requests with no valid credential on the /v1 and MCP planes.
func (p *Platform) allowUnauthRate(ip string) bool {
	sec := p.securityValue()
	if sec.UnauthRatePerMinute <= 0 {
		return true
	}
	return p.ratelimits.unauthLimiters.allow(ip, sec.UnauthRatePerMinute, 5)
}

// allowAdminIPRate applies security.admin_rate_per_ip (0 = off) per client IP
// to /api/* (B-05); the global adminLimiter stays as the backstop. Burst floor
// 15: a single console page fires ~8 API calls on load, and the sustained
// limit (default 30/min) still binds over time.
func (p *Platform) allowAdminIPRate(ip string) bool {
	sec := p.securityValue()
	if sec.AdminRatePerIP <= 0 {
		return true
	}
	return p.ratelimits.adminIPLimiters.allow(ip, sec.AdminRatePerIP, 15)
}
