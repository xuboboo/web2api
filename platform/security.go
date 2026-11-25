package platform

// 契约 B: platform security hardening.
//
//   - Login brute-force lockout: 5 consecutive failures for the same
//     username across source IPs lock the account for 15 minutes; during the window every login
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
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
	"golang.org/x/time/rate"
)

// SecurityConfig maps the `security:` node of config.yaml. Field names are
// fixed by the product contract:
//
//	security:
//	  admin_rate_per_minute: 60
//	  admin_rate_per_ip: 30
//	  api_rate_per_minute: 120
//	  unauth_rate_per_minute: 30
//	  data_plane_auth: false
//	  trust_proxy_headers: false
//	  mcp_allowed_origins: []
//	  metrics_token: ""
//	  login_lockout:
//	    fails: 5
//	    minutes: 15
//	  password_policy:
//	    min_length: 8
//	    min_classes: 0
type SecurityConfig struct {
	// AdminRatePerMinute is the global /api/* rate limit (0 → default 60).
	AdminRatePerMinute int `mapstructure:"admin_rate_per_minute"`
	// AdminRatePerIP is the per-IP /api/* rate limit (S4-B-05; 0 = off).
	// Negative → contract default (30).
	AdminRatePerIP int `mapstructure:"admin_rate_per_ip"`
	// APIRatePerMinute is the per-key rate limit for the billed /v1 and MCP
	// planes (S4-B-02; 0 = off, negative → default 120).
	APIRatePerMinute int `mapstructure:"api_rate_per_minute"`
	// UnauthRatePerMinute limits /v1 and MCP requests that carry no valid
	// credential, per client IP (S4-B-02 防爆破/反抓放大; 0 = off, negative →
	// default 30).
	UnauthRatePerMinute int `mapstructure:"unauth_rate_per_minute"`
	// DataPlaneAuth, when true, requires X-API-Key on the data plane
	// (dedicated /{engine}/search and /stats/*). The Platform helper default is
	// false for compatibility; serve's Viper production default is true.
	DataPlaneAuth bool `mapstructure:"data_plane_auth"`
	// TrustProxyHeaders enables trusted-proxy client-IP extraction and, for a
	// trusted HTTPS proxy, Strict-Transport-Security. Only for deployments
	// behind a reverse proxy.
	TrustProxyHeaders bool `mapstructure:"trust_proxy_headers"`
	// TrustedProxies lists the proxy peer IPs/CIDRs allowed to supply
	// X-Real-IP. Empty means no peer is trusted.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// MCPAllowedOrigins (S4-B-10) extends the MCP Origin allowlist beyond the
	// request's own origin. Empty = same-origin only (empty Origin / curl 放行).
	MCPAllowedOrigins []string `mapstructure:"mcp_allowed_origins"`
	// MetricsToken guards GET /web-tool/metrics (S4-GAP 运维指标). Empty =
	// loopback-only scraping.
	MetricsToken string `mapstructure:"metrics_token"`
	// LoginLockout configures the admin login brute-force lockout.
	LoginLockout LoginLockoutConfig `mapstructure:"login_lockout"`
	// PasswordPolicy (S4-B-11) governs the admin password change.
	PasswordPolicy PasswordPolicyConfig `mapstructure:"password_policy"`
}

// LoginLockoutConfig maps security.login_lockout.
type LoginLockoutConfig struct {
	// Fails is the consecutive-failure count that locks the username across IPs.
	Fails int `mapstructure:"fails"`
	// Minutes is the lock window in minutes.
	Minutes int `mapstructure:"minutes"`
}

// PasswordPolicyConfig maps security.password_policy (S4-B-11). Defaults keep
// the existing contract: min_length 8, no complexity requirement.
type PasswordPolicyConfig struct {
	// MinLength is the minimum admin password length (0 → 8).
	MinLength int `mapstructure:"min_length"`
	// MinClasses is how many character classes (lower/upper/digit/symbol)
	// the password must span (0 = no requirement).
	MinClasses int `mapstructure:"min_classes"`
}

// DefaultSecurityConfig returns the contract defaults.
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		AdminRatePerMinute:  60,
		AdminRatePerIP:      30,
		APIRatePerMinute:    120,
		UnauthRatePerMinute: 30,
		DataPlaneAuth:       false,
		LoginLockout:        LoginLockoutConfig{Fails: 5, Minutes: 15},
		PasswordPolicy:      PasswordPolicyConfig{MinLength: 8, MinClasses: 0},
	}
}

// normalized fills zero/invalid fields with the contract defaults so a
// partially written security node still yields working protections. The new
// S4 limits treat an explicit 0 as "off" and a negative value as "unset"
// (→ contract default) — viper supplies the defaults for genuinely missing
// keys, so 0 stays a deliberate opt-out.
func (c SecurityConfig) normalized() SecurityConfig {
	def := DefaultSecurityConfig()
	if c.AdminRatePerMinute <= 0 {
		c.AdminRatePerMinute = def.AdminRatePerMinute
	}
	if c.AdminRatePerIP < 0 {
		c.AdminRatePerIP = def.AdminRatePerIP
	}
	if c.APIRatePerMinute < 0 {
		c.APIRatePerMinute = def.APIRatePerMinute
	}
	if c.UnauthRatePerMinute < 0 {
		c.UnauthRatePerMinute = def.UnauthRatePerMinute
	}
	if c.LoginLockout.Fails <= 0 {
		c.LoginLockout.Fails = def.LoginLockout.Fails
	}
	if c.LoginLockout.Minutes <= 0 {
		c.LoginLockout.Minutes = def.LoginLockout.Minutes
	}
	if c.PasswordPolicy.MinLength <= 0 {
		c.PasswordPolicy.MinLength = def.PasswordPolicy.MinLength
	}
	if c.PasswordPolicy.MinClasses < 0 {
		c.PasswordPolicy.MinClasses = def.PasswordPolicy.MinClasses
	}
	if c.PasswordPolicy.MinClasses > 4 {
		c.PasswordPolicy.MinClasses = 4
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
// Password policy (S4-B-11)
// ---------------------------------------------------------------------------

// commonAdminPasswords is the built-in deny list for password changes (S4-B-11
// 常用密码黑名单; natural-language rejection on match).
var commonAdminPasswords = map[string]bool{
	"password": true, "password1": true, "password123": true,
	"123456": true, "12345678": true, "123456789": true, "1234567890": true,
	"admin123": true, "admin@123": true, "admin1234": true, "admin": true,
	"qwerty": true, "qwerty123": true, "abc123": true, "abc123456": true,
	"111111": true, "000000": true, "88888888": true, "666666": true,
	"letmein": true, "iloveyou": true, "welcome": true, "welcome1": true,
	"monkey": true, "dragon": true, "sunshine": true, "master": true,
	"root": true, "toor": true, "passw0rd": true, "p@ssw0rd": true,
	"web2api": true, "web2api123": true, "a123456": true, "a12345678": true,
}

// validatePassword applies security.password_policy and returns a
// natural-language rejection (Chinese, no config keys — 面板文案红线) or "".
func (p *Platform) validatePassword(newPassword string) string {
	sec := p.securityValue()
	if n := len([]rune(newPassword)); n < sec.PasswordPolicy.MinLength {
		return fmt.Sprintf("密码长度至少需要 %d 个字符", sec.PasswordPolicy.MinLength)
	}
	if sec.PasswordPolicy.MinClasses > 0 {
		classes := 0
		hasLower, hasUpper, hasDigit, hasSymbol := false, false, false, false
		for _, ch := range newPassword {
			switch {
			case ch >= 'a' && ch <= 'z':
				hasLower = true
			case ch >= 'A' && ch <= 'Z':
				hasUpper = true
			case ch >= '0' && ch <= '9':
				hasDigit = true
			default:
				hasSymbol = true
			}
		}
		for _, b := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
			if b {
				classes++
			}
		}
		if classes < sec.PasswordPolicy.MinClasses {
			return "密码需要混合大小写字母、数字与符号中的更多种类"
		}
	}
	if commonAdminPasswords[strings.ToLower(newPassword)] {
		return "密码过于常见,请更换为不易猜测的密码"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Login brute-force lockout
// ---------------------------------------------------------------------------

type loginAttemptKey struct {
	username string
}

type loginFailState struct {
	fails       int
	lockedUntil time.Time
	lastFail    time.Time
	// ips records the sources that contributed failures during this lock
	// window. A lock is deliberately per username: otherwise rotating source
	// IPs resets the attacker's budget and the real admin can be locked out by
	// a distributed attempt. IPs are diagnostic only and bounded below.
	ips map[string]struct{}
}

// lockoutWindow returns (fails, window) from the effective config.
func (p *Platform) lockoutWindow() (int, time.Duration) {
	sec := p.securityValue()
	return sec.LoginLockout.Fails, time.Duration(sec.LoginLockout.Minutes) * time.Minute
}

// loginLockedAt reports the remaining lock deadline for username, if any.
// The lock is intentionally not keyed by IP: the admin username is a single
// account, so per-IP counters let a botnet rotate addresses and bypass the
// threshold. IP remains part of the audit trail, not the lock identity.
func (p *Platform) loginLockedAt(username string, now time.Time) (time.Time, bool) {
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	key := loginAttemptKey{username}
	st, ok := p.loginFails[key]
	if !ok {
		return time.Time{}, false
	}
	if !st.lockedUntil.IsZero() {
		if now.Before(st.lockedUntil) {
			return st.lockedUntil, true
		}
		// Window elapsed: fresh start.
		delete(p.loginFails, key)
		return time.Time{}, false
	}
	return time.Time{}, false
}

// recordLoginFailure counts one failed attempt for the username across all
// source IPs and locks the account once the configured threshold is reached.
func (p *Platform) recordLoginFailure(username, ip string, now time.Time) {
	fails, window := p.lockoutWindow()
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	p.pruneLoginFailsLocked(now)
	key := loginAttemptKey{username}
	st, ok := p.loginFails[key]
	if !ok {
		st = &loginFailState{ips: make(map[string]struct{})}
		p.loginFails[key] = st
	}
	if !st.lockedUntil.IsZero() && now.After(st.lockedUntil) {
		st.fails = 0
		st.lockedUntil = time.Time{}
		st.ips = make(map[string]struct{})
	}
	st.fails++
	st.lastFail = now
	if len(st.ips) < 16 {
		st.ips[ip] = struct{}{}
	}
	if st.fails >= fails {
		st.lockedUntil = now.Add(window)
	}
}

// clearLoginFailures resets the username after a successful login.
func (p *Platform) clearLoginFailures(username string) {
	p.loginMu.Lock()
	defer p.loginMu.Unlock()
	delete(p.loginFails, loginAttemptKey{username})
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

// adminRateGuard returns the /api/* rate-limit middleware. S4-B-05: per-IP
// buckets (security.admin_rate_per_ip, 默认 30/min) so a single runaway
// client cannot starve the real admin out of the global budget
// (security.admin_rate_per_minute stays as the backstop). 超限 429 +
// Retry-After; both tiers disabled when configured 0.
func (p *Platform) adminRateGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		sec := p.securityValue()
		if sec.AdminRatePerIP > 0 && !p.allowAdminIPRate(c.IP()) {
			core.Metrics().IncRateLimited()
			return errRateLimited(c, retryAfterSecondsForRate(sec.AdminRatePerIP))
		}
		if sec.AdminRatePerMinute <= 0 || p.adminLimiter == nil {
			return c.Next()
		}
		reservation := p.adminLimiter.Reserve()
		if !reservation.OK() {
			core.Metrics().IncRateLimited()
			return errRateLimited(c, sec.AdminRatePerMinute*60)
		}
		if delay := reservation.DelayFrom(time.Now()); delay > 0 {
			reservation.Cancel()
			core.Metrics().IncRateLimited()
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
// Anything else answers 401 {"error":"unauthorized"} — with the S4-B-02
// unauth per-IP limiter in front of that rejection (防 key 爆破与反抓放大).
func (p *Platform) AuthAny() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// 1) Admin JWT (not billed). An invalid bearer falls through to the
		//    API-key branch so a client sending both headers keeps working.
		if token := bearerToken(c.Get(fiber.HeaderAuthorization)); token != "" {
			if claims, ok := p.verifyAdminToken(token); ok {
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
			if key, denyStatus, ok := p.authenticateAPIKey(c); ok {
				if !p.allowKeyRate(key.ID) {
					core.Metrics().IncRateLimited()
					return errRateLimited(c, retryAfterSecondsForRate(p.securityValue().APIRatePerMinute))
				}
				units := p.mcpChargeUnits(c)
				if units > 0 && key.DailyQuota > 0 && usedTodayUnits(key, time.Now())+int64(units) > key.DailyQuota {
					core.Metrics().IncQuotaBlocked()
					return errQuotaExceeded(c)
				}
				c.Locals(localAuthType, "api_key")
				c.Locals(localKeyID, key.ID)
				c.Locals(localKeyName, *key)
				// Scope async jobs to the paying key (core.JobOwner): the MCP
				// check_indexed tool submits through this context.
				c.SetUserContext(core.WithJobOwner(c.UserContext(), key.ID))
				started := time.Now()
				handlerErr := c.Next()
				if status := c.Response().StatusCode(); status == fiber.StatusOK && handlerErr == nil && units > 0 {
					endpoint := c.Route().Path
					if endpoint == "" || endpoint == "/" {
						endpoint = c.Path()
					}
					// T-02 与 writeUsage 同口径: 响应体(result.content[].text)携带逐
					// 引擎明细时按成功单元计费并落 EngineStats(/api/reconcile 可逐
					// 单元对账);明细不可解析(web_search、工具级错误信封)时保持
					// 既有行为——按请求单元全额计费。
					stats := parseEngineStatsFromMCPBody(c.Response().Body())
					charged := units
					if stats != nil {
						charged = 0
						for _, s := range stats {
							charged += int(s.OK)
						}
					}
					if err := p.chargeUsage(key.ID, charged, endpoint, status, time.Since(started).Milliseconds(), stats); err != nil {
						logCharged(c, key.ID, charged, err)
					}
				}
				return handlerErr
			} else if denyStatus == fiber.StatusForbidden {
				return errForbidden(c)
			}
		}

		if !p.allowUnauthRate(c.IP()) {
			core.Metrics().IncRateLimited()
			return errRateLimited(c, retryAfterSecondsForRate(p.securityValue().UnauthRatePerMinute))
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
// pattern (or raw path) used in the usage journal. T-03: outcome 由真实状态
// 码推导(与 /v1 路径 writeUsage 一致);T-02: engineStats 非空时随记录落账,
// 供 /api/reconcile 逐单元对账(不传时按无引擎明细口径落账)。
func (p *Platform) ChargeUsage(keyID string, units int, endpoint string, status int, tookMs int64) error {
	return p.chargeUsage(keyID, units, endpoint, status, tookMs, nil)
}

// chargeUsage is ChargeUsage with the per-engine stats the caller derived from
// the response body (MCP plane, T-02 同口径).
func (p *Platform) chargeUsage(keyID string, units int, endpoint string, status int, tookMs int64, engineStats map[string]EngineStat) error {
	now := time.Now()
	rec := &UsageRecord{
		Time:        now,
		KeyID:       keyID,
		Endpoint:    endpoint,
		Units:       units,
		Status:      status,
		TookMS:      tookMs,
		EngineStats: engineStats,
		Outcome:     outcomeForStatus(status),
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
