package platform

// 契约 B: platform security hardening.
//
//   - Login brute-force lockout: 5 consecutive failures for the same
//     username+IP lock the pair for 15 minutes; during the window every login
//     attempt answers 429 {"error":"too_many_attempts"} + Retry-After — even
//     with correct credentials. Configured by security.login_lockout.
//   - Admin API global rate limit: security.admin_rate_per_minute (default 60)
//     requests per minute across all /api/* endpoints; over the limit answers
//     429 {"error":"rate_limited"} + Retry-After.
//   - AuthAny: the unified authentication seam for the MCP plane — X-API-Key
//     (validated + quota pre-checked + billed) or admin Bearer JWT (not
//     billed). MCP tool calls are sized from the JSON-RPC body.
//   - ChargeUsage: usage-journal entry + quota consumption for one billed
//     call.

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// SecurityConfig maps the `security:` node of config.yaml. Field names are
// fixed by the product contract:
//
//	security:
//	  admin_rate_per_minute: 60
//	  data_plane_auth: false
//	  login_lockout:
//	    fails: 5
//	    minutes: 15
type SecurityConfig struct {
	// AdminRatePerMinute is the global /api/* rate limit (0 → default 60).
	AdminRatePerMinute int `mapstructure:"admin_rate_per_minute"`
	// DataPlaneAuth, when true, requires X-API-Key on the data plane
	// (dedicated /{engine}/search and /stats/*). Default false keeps the
	// historical behavior.
	DataPlaneAuth bool `mapstructure:"data_plane_auth"`
	// LoginLockout configures the admin login brute-force lockout.
	LoginLockout LoginLockoutConfig `mapstructure:"login_lockout"`
}

// LoginLockoutConfig maps security.login_lockout.
type LoginLockoutConfig struct {
	// Fails is the consecutive-failure count that locks the username+IP pair.
	Fails int `mapstructure:"fails"`
	// Minutes is the lock window in minutes.
	Minutes int `mapstructure:"minutes"`
}

// DefaultSecurityConfig returns the contract defaults.
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		AdminRatePerMinute: 60,
		DataPlaneAuth:      false,
		LoginLockout:       LoginLockoutConfig{Fails: 5, Minutes: 15},
	}
}

// normalized fills zero/invalid fields with the contract defaults so a
// partially written security node still yields working protections.
func (c SecurityConfig) normalized() SecurityConfig {
	def := DefaultSecurityConfig()
	if c.AdminRatePerMinute <= 0 {
		c.AdminRatePerMinute = def.AdminRatePerMinute
	}
	if c.LoginLockout.Fails <= 0 {
		c.LoginLockout.Fails = def.LoginLockout.Fails
	}
	if c.LoginLockout.Minutes <= 0 {
		c.LoginLockout.Minutes = def.LoginLockout.Minutes
	}
	return c
}

// SetSecurity installs the `security:` node (rate limiter + lockout knobs).
// Call before RegisterAdmin; omitted/zero fields keep the contract defaults.
func (p *Platform) SetSecurity(cfg SecurityConfig) {
	p.secMu.Lock()
	defer p.secMu.Unlock()
	p.sec = cfg.normalized()
	p.adminLimiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(p.sec.AdminRatePerMinute)), p.sec.AdminRatePerMinute)
}

// securityValue returns the effective (normalized) security config.
func (p *Platform) securityValue() SecurityConfig {
	p.secMu.Lock()
	defer p.secMu.Unlock()
	if p.sec.AdminRatePerMinute == 0 && p.sec.LoginLockout.Fails == 0 {
		return DefaultSecurityConfig()
	}
	return p.sec
}

// ---------------------------------------------------------------------------
// Login brute-force lockout
// ---------------------------------------------------------------------------

type loginAttemptKey struct {
	username string
	ip       string
}

type loginFailState struct {
	fails       int
	lockedUntil time.Time
	lastFail    time.Time
}

// lockoutWindow returns (fails, window) from the effective config.
func (p *Platform) lockoutWindow() (int, time.Duration) {
	sec := p.securityValue()
	return sec.LoginLockout.Fails, time.Duration(sec.LoginLockout.Minutes) * time.Minute
}

// loginLockedAt reports the remaining lock deadline for username+IP, if any.
func (p *Platform) loginLockedAt(username, ip string, now time.Time) (time.Time, bool) {
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	st, ok := p.loginFails[loginAttemptKey{username, ip}]
	if !ok {
		return time.Time{}, false
	}
	if !st.lockedUntil.IsZero() {
		if now.Before(st.lockedUntil) {
			return st.lockedUntil, true
		}
		// Window elapsed: fresh start.
		delete(p.loginFails, loginAttemptKey{username, ip})
		return time.Time{}, false
	}
	return time.Time{}, false
}

// recordLoginFailure counts one failed attempt and locks the pair once the
// configured consecutive-failure count is reached.
func (p *Platform) recordLoginFailure(username, ip string, now time.Time) {
	fails, window := p.lockoutWindow()
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	p.pruneLoginFailsLocked(now)
	key := loginAttemptKey{username, ip}
	st, ok := p.loginFails[key]
	if !ok {
		st = &loginFailState{}
		p.loginFails[key] = st
	}
	if !st.lockedUntil.IsZero() && now.After(st.lockedUntil) {
		st.fails = 0
		st.lockedUntil = time.Time{}
	}
	st.fails++
	st.lastFail = now
	if st.fails >= fails {
		st.lockedUntil = now.Add(window)
	}
}

// clearLoginFailures resets the pair after a successful login.
func (p *Platform) clearLoginFailures(username, ip string) {
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	delete(p.loginFails, loginAttemptKey{username, ip})
}

// pruneLoginFailsLocked drops expired locks and stale (unlocked, >1h idle)
// entries so the map stays bounded for the single-admin platform. Caller
// holds the mutex.
func (p *Platform) pruneLoginFailsLocked(now time.Time) {
	for key, st := range p.loginFails {
		expired := !st.lockedUntil.IsZero() && now.After(st.lockedUntil)
		stale := st.lockedUntil.IsZero() && now.Sub(st.lastFail) > time.Hour
		if expired || stale {
			delete(p.loginFails, key)
		}
	}
}

// ---------------------------------------------------------------------------
// Admin API global rate limit
// ---------------------------------------------------------------------------

// adminRateGuard returns the /api/* rate-limit middleware (契约 B: 默认
// 60 req/min,超限 429 + Retry-After). Disabled when the limit is <= 0.
func (p *Platform) adminRateGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		sec := p.securityValue()
		if sec.AdminRatePerMinute <= 0 || p.adminLimiter == nil {
			return c.Next()
		}
		reservation := p.adminLimiter.Reserve()
		if !reservation.OK() {
			return errRateLimited(c, sec.AdminRatePerMinute*60)
		}
		if delay := reservation.DelayFrom(time.Now()); delay > 0 {
			reservation.Cancel()
			return errRateLimited(c, int((delay+time.Second-time.Nanosecond)/time.Second))
		}
		return c.Next()
	}
}

// ---------------------------------------------------------------------------
// AuthAny: unified auth seam (X-API-Key billed / Bearer JWT admin)
// ---------------------------------------------------------------------------

// Locals keys injected by AuthAny. auth_type is "api_key" or "jwt"; key_id is
// the API key id (api_key only), username the admin username (jwt only). The
// full APIKey value is additionally exposed under localKeyName for the MCP
// get_quota tool.
const (
	localAuthType = "platform_auth_type"
	localKeyID    = "platform_key_id"
	localUsername = "platform_username"
)

// AuthAny guards the MCP/agent plane (契约 C): Authorization Bearer JWT →
// admin identity, never billed; X-API-Key → validated key with quota
// pre-check, billed via ChargeUsage once the call completed with HTTP 200.
// Anything else answers 401 {"error":"unauthorized"}.
func (p *Platform) AuthAny() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// 1) Admin JWT (not billed). An invalid bearer falls through to the
		//    API-key branch so a client sending both headers keeps working.
		if token := bearerToken(c.Get(fiber.HeaderAuthorization)); token != "" {
			if claims, err := verifyJWT(p.secret, token); err == nil && claims.TV == p.tokenVersion() {
				c.Locals(localAuthType, "jwt")
				c.Locals(localUsername, claims.Sub)
				return c.Next()
			}
		}

		// 2) API key (billed). Mirrors authenticateAPIKey's credential sources:
		//    X-API-Key first, Bearer key material as fallback.
		raw := strings.TrimSpace(c.Get(apiKeyHeader))
		if raw == "" {
			raw = bearerToken(c.Get(fiber.HeaderAuthorization))
		}
		if raw != "" {
			key, err := p.store.GetKeyByHash(sha256Hex(raw))
			if err == nil && key != nil && key.Status == KeyStatusActive {
				units := p.mcpChargeUnits(c)
				if units > 0 && key.DailyQuota > 0 && usedTodayUnits(key, time.Now())+int64(units) > key.DailyQuota {
					return errQuotaExceeded(c)
				}
				c.Locals(localAuthType, "api_key")
				c.Locals(localKeyID, key.ID)
				c.Locals(localKeyName, *key)
				started := time.Now()
				handlerErr := c.Next()
				if status := c.Response().StatusCode(); status == fiber.StatusOK && handlerErr == nil && units > 0 {
					endpoint := c.Route().Path
					if endpoint == "" || endpoint == "/" {
						endpoint = c.Path()
					}
					if err := p.ChargeUsage(key.ID, units, endpoint, status, time.Since(started).Milliseconds()); err != nil {
						logCharged(c, key.ID, units, err)
					}
				}
				return handlerErr
			}
		}

		return errUnauthorized(c)
	}
}

// mcpChargeUnits sizes the billing units for an MCP JSON-RPC request
// (契约 C): web_search=1, check_indexed=len(urls)×len(engines) (engines
// omitted counts conservatively as the 4 default engines), every other
// method/tool costs nothing. Only POST paths ending in /mcp are parsed.
func (p *Platform) mcpChargeUnits(c *fiber.Ctx) int {
	if c.Method() != fiber.MethodPost {
		return 0
	}
	path := strings.TrimSuffix(c.Path(), "/")
	if !strings.HasSuffix(path, "/mcp") {
		return 0
	}
	return mcpToolUnits(c.Body())
}

// mcpToolUnits extracts the billed units from one JSON-RPC 2.0 request body.
func mcpToolUnits(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	var payload struct {
		Method string `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	if payload.Method != "tools/call" {
		return 0
	}
	switch payload.Params.Name {
	case "web_search":
		return 1
	case "check_indexed":
		var args struct {
			URLs    []string `json:"urls"`
			Engines []string `json:"engines"`
		}
		if len(payload.Params.Arguments) > 0 {
			if err := json.Unmarshal(payload.Params.Arguments, &args); err != nil {
				return 0
			}
		}
		units := len(args.URLs) * len(resolveChargeEngines(args.Engines))
		return units
	default:
		return 0
	}
}

// resolveChargeEngines mirrors ResolveEngines sizing (default engines = 4)
// without rejecting unknown names — quota pre-check stays conservative.
func resolveChargeEngines(raw []string) []string {
	if len(raw) == 0 {
		return DefaultEngines
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if mapped, ok := engineAliases[name]; ok {
			name = mapped
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return DefaultEngines
	}
	return out
}

// ChargeUsage records one billed call (落账) and consumes the units from the
// key's counters (扣配额) when status is HTTP 200. endpoint is the route
// pattern (or raw path) used in the usage journal.
func (p *Platform) ChargeUsage(keyID string, units int, endpoint string, status int, tookMs int64) error {
	now := time.Now()
	rec := &UsageRecord{
		Time:     now,
		KeyID:    keyID,
		Endpoint: endpoint,
		Units:    units,
		Status:   status,
		TookMS:   tookMs,
	}
	if key, err := p.store.GetKeyByID(keyID); err == nil && key != nil {
		rec.KeyName = key.Name
	}
	return p.recordUsage(rec, keyID, int64(units), units > 0 && status == fiber.StatusOK, now)
}

// logCharged keeps charge failures visible without failing the response.
func logCharged(c *fiber.Ctx, keyID string, units int, err error) {
	logrus.WithError(err).WithFields(logrus.Fields{
		"key_id": keyID, "units": units, "path": c.Path(),
	}).Warn("platform: MCP 计费落账失败")
}
