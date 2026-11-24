package platform

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
)

// apiKeyHeader is the public-API credential header (fiber v2 ships no constant
// for it).
const apiKeyHeader = "X-API-Key"

// Locals keys injected by APIKeyGuard. The index/check handler (integration
// stage) can read platform_units / platform_urls / platform_engines to build
// its response meta without re-deriving them.
const (
	localKeyName    = "platform_key"     // APIKey (value copy) of the authenticated key
	localKeyUnits   = "platform_units"   // int: urls × engines
	localKeyURLs    = "platform_urls"    // []string target URLs
	localKeyEngines = "platform_engines" // []string resolved engine names
)

// bearerToken extracts a Bearer token from an Authorization header value.
func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if len(header) >= 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// authenticateAPIKey resolves the caller's API key from X-API-Key (or
// Authorization: Bearer) and enforces the status contract: missing/unknown or
// disabled keys are rejected (禁用 key 一律 401). S4-GAP 密钥增强: a key past
// its expires_at is rejected with 401; a key whose allowed_ips list does not
// match the caller IP is rejected with 403 (denyStatus).
func (p *Platform) authenticateAPIKey(c *fiber.Ctx) (key *APIKey, denyStatus int, ok bool) {
	raw := strings.TrimSpace(c.Get(apiKeyHeader))
	if raw == "" {
		raw = bearerToken(c.Get(fiber.HeaderAuthorization))
	}
	if raw == "" {
		return nil, 0, false
	}
	key, err := p.store.GetKeyByHash(sha256Hex(raw))
	if err != nil || key == nil {
		return nil, 0, false
	}
	if key.Status != KeyStatusActive {
		return nil, 0, false
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, 0, false
	}
	if !ipAllowedFor(key.AllowedIPs, c.IP()) {
		return nil, fiber.StatusForbidden, false
	}
	return key, 0, true
}

// ipAllowedFor matches the caller IP against the key's CIDR allowlist
// (S4-GAP 密钥增强). An empty list allows every IP; bare IPs are treated as
// /32 (IPv4) or /128 (IPv6).
func ipAllowedFor(allowedIPs []string, ip string) bool {
	if len(allowedIPs) == 0 {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, entry := range allowedIPs {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			if parsed.Equal(net.ParseIP(entry)) {
				return true
			}
			continue
		}
		_, cidr, err := net.ParseCIDR(entry)
		if err != nil {
			continue
		}
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

// parseIndexRequest pre-parses an index/check request to size the quota check.
// It returns the target URLs, the resolved engine list and ok=false when the
// request is malformed (missing urls, unknown engine …) — the endpoint handler
// then owns the 400 answer and no units are counted.
func (p *Platform) parseIndexRequest(c *fiber.Ctx) (urls []string, engines []string, ok bool) {
	switch c.Method() {
	case fiber.MethodPost, fiber.MethodPut:
		var body struct {
			URLs    []string `json:"urls"`
			Engines []string `json:"engines"`
		}
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return nil, nil, false
		}
		for _, u := range body.URLs {
			if u = strings.TrimSpace(u); u != "" {
				urls = append(urls, u)
			}
		}
		if len(urls) == 0 {
			return nil, nil, false
		}
		engines, valid := ResolveEngines(body.Engines)
		if !valid {
			return nil, nil, false
		}
		return urls, engines, true

	case fiber.MethodGet, fiber.MethodHead:
		u := strings.TrimSpace(c.Query("url"))
		if u == "" {
			return nil, nil, false
		}
		var raw []string
		if param := strings.TrimSpace(c.Query("engines")); param != "" {
			raw = strings.Split(param, ",")
		}
		engines, valid := ResolveEngines(raw)
		if !valid {
			return nil, nil, false
		}
		return []string{u}, engines, true
	}
	return nil, nil, false
}

// APIKeyGuard guards the public /v1/* routes. Flow:
//  1. X-API-Key (or Authorization: Bearer) → unknown/disabled key ⇒ 401
//     {"error":"unauthorized"}.
//  2. Pre-parse the request to compute units (1 URL × 1 engine = 1 unit);
//     when the key has a daily quota (0 = unlimited) and used_today+units
//     would exceed it ⇒ 429 {"error":"quota_exceeded"}.
//  3. Inject key info into c.Locals and run the wrapped handler.
//  4. After c.Next() record a usage entry for /v1/index/* requests (endpoint,
//     engines, urls, units, HTTP status, took_ms, per-engine ok/fail counts)
//     and persist the key counters.
//
// Quota-blocked requests are rejected before c.Next() and therefore produce no
// usage record and consume no units.
func (p *Platform) APIKeyGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		started := time.Now()

		key, denyStatus, ok := p.authenticateAPIKey(c)
		if !ok {
			if denyStatus == fiber.StatusForbidden {
				return errForbidden(c)
			}
			if !p.allowUnauthRate(c.IP()) {
				core.Metrics().IncRateLimited()
				return errRateLimited(c, retryAfterSecondsForRate(p.securityValue().UnauthRatePerMinute))
			}
			return errUnauthorized(c)
		}

		// S4-B-02: per-key token bucket (security.api_rate_per_minute, 0=off;
		// burst = max(10, rate/4)). The daily-quota pre-check below is kept
		// unchanged and stays conservative at full units.
		if !p.allowKeyRate(key.ID) {
			core.Metrics().IncRateLimited()
			return errRateLimited(c, retryAfterSecondsForRate(p.securityValue().APIRatePerMinute))
		}

		urls, engines, parsed := p.parseIndexRequest(c)
		units := 0
		if parsed {
			units = len(urls) * len(engines)
		}

		if units > 0 && key.DailyQuota > 0 && usedTodayUnits(key, time.Now())+int64(units) > key.DailyQuota {
			core.Metrics().IncQuotaBlocked()
			return errQuotaExceeded(c)
		}

		c.Locals(localKeyName, *key)
		c.Locals(localKeyUnits, units)
		c.Locals(localKeyURLs, urls)
		c.Locals(localKeyEngines, engines)

		handlerErr := c.Next()

		endpoint := c.Route().Path
		if endpoint == "" || endpoint == "/" {
			endpoint = c.Path()
		}
		// Usage logging is scoped to the index/check endpoints per contract;
		// /v1/quota and /v1/engines stay out of the usage journal, and job
		// polling (/v1/index/check/jobs/...) is a progress read, not a lookup.
		isLookupPath := (strings.Contains(endpoint, "/v1/index") || strings.Contains(c.Path(), "/v1/index")) &&
			!strings.Contains(c.Path(), "/jobs")
		if isLookupPath {
			p.writeUsage(c, key, endpoint, urls, engines, units, started, handlerErr)
		}
		return handlerErr
	}
}

// writeUsage persists the usage record and key counters after an index/check
// request. B-01 用量记账如实化: the HTTP status is derived from the real
// handler error (*APIError.HTTPStatus / *fiber.Error.Code / other → 500)
// instead of blindly rewriting a not-yet-rendered 200 into 500. 计费口径: only
// a completed HTTP 200 bills — charged units are the OK units derived from the
// response envelope (units whose engine field carries an error object are not
// charged — including raw-mode not_implemented pseudo-units; when the envelope
// cannot be parsed the full requested units are charged, matching the pre-2026
// behavior). Any 4xx/5xx final state records zero units, whether the handler
// returned an error value or wrote the status itself. Every record carries the
// outcome verdict (billed / engine_failed / rejected) so the P-01 有效调用 KPI
// can exclude rejects.
func (p *Platform) writeUsage(c *fiber.Ctx, key *APIKey, endpoint string, urls, engines []string, units int, started time.Time, handlerErr error) {
	at := time.Now()
	status := realHandlerStatus(c.Response().StatusCode(), handlerErr)
	outcome := outcomeForStatus(status)
	engineStats := parseEngineStatsFromBody(c.Response().Body())
	charged := 0
	switch {
	case status == fiber.StatusOK && handlerErr == nil:
		charged = units
		if engineStats != nil {
			charged = 0
			for _, s := range engineStats {
				charged += int(s.OK)
			}
		}
	case status == fiber.StatusAccepted && handlerErr == nil:
		// 202 = the batch was accepted into the job queue. Bill the requested
		// units up front: the server will spend exactly that many engine calls,
		// and per-result charging would let a caller queue work for free.
		charged = units
		outcome = OutcomeBilled
	}
	rec := &UsageRecord{
		Time:        at,
		KeyID:       key.ID,
		KeyName:     key.Name,
		Endpoint:    endpoint,
		Engines:     engines,
		URLs:        len(urls),
		Units:       charged,
		Status:      status,
		TookMS:      at.Sub(started).Milliseconds(),
		EngineStats: engineStats,
		Outcome:     outcome,
	}
	consume := charged > 0 && (status == fiber.StatusOK || status == fiber.StatusAccepted) && handlerErr == nil
	if err := p.recordUsage(rec, key.ID, int64(charged), consume, at); err != nil {
		logrus.WithError(err).Warn("platform: 写入 usage 记录失败")
	}
	core.Metrics().AddUsageUnits(int64(charged))
}

// realHandlerStatus derives the true final HTTP status of a guarded request.
// fiber renders the error response AFTER the middleware chain unwinds, so
// c.Response().StatusCode() still carries the not-yet-overwritten 200 default
// when the handler failed — the error value is the source of truth.
func realHandlerStatus(responseStatus int, handlerErr error) int {
	if handlerErr == nil {
		return responseStatus
	}
	switch e := handlerErr.(type) {
	case *core.APIError:
		if e.HTTPStatus > 0 {
			return e.HTTPStatus
		}
	case *fiber.Error:
		if e.Code > 0 {
			return e.Code
		}
	}
	return fiber.StatusInternalServerError
}

// parseEngineStatsFromBody derives per-engine attempt/success counts from the
// index/check response envelope: results[].engines.<engine> is either a result
// object or an {"error":{...}} object.
func parseEngineStatsFromBody(body []byte) map[string]EngineStat {
	if len(body) == 0 {
		return nil
	}
	var payload struct {
		Results []struct {
			Engines map[string]struct {
				Error json.RawMessage `json:"error"`
			} `json:"engines"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	stats := make(map[string]EngineStat)
	for _, result := range payload.Results {
		for engine, item := range result.Engines {
			s := stats[engine]
			s.Calls++
			hasError := len(item.Error) > 0 && !strings.EqualFold(strings.TrimSpace(string(item.Error)), "null")
			if !hasError {
				s.OK++
			}
			stats[engine] = s
		}
	}
	if len(stats) == 0 {
		return nil
	}
	return stats
}

// parseEngineStatsFromMCPBody derives per-engine attempt/success counts from an
// MCP tools/call response body (T-02 与 writeUsage 同口径): the tool payload
// rides in result.content[].text as a JSON string carrying the same
// results[].engines envelope as /v1/index/check (check_indexed 走
// serv.CheckIndexBatch,响应结构一致). Returns nil when the body is not a
// text-bearing tool result or the payload has no per-engine section (web_search
// 结果、工具级错误信封) — callers then charge the full requested units,
// mirroring writeUsage's unparsed-envelope behavior.
func parseEngineStatsFromMCPBody(body []byte) map[string]EngineStat {
	if len(body) == 0 {
		return nil
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	for _, item := range resp.Result.Content {
		if item.Type != "text" || item.Text == "" {
			continue
		}
		if stats := parseEngineStatsFromBody([]byte(item.Text)); stats != nil {
			return stats
		}
	}
	return nil
}

// adminSessionCookie is the httpOnly session cookie name (S4-B-12). The
// console login sets it alongside the Bearer token; jwtGuard accepts either
// so existing API consumers (Bearer) stay untouched.
const adminSessionCookie = "w2a_admin_session"

// verifyAdminToken validates signature, expiry, token_version and — for S4
// tokens — that the bound session is still active. Legacy tokens without a
// sid stay valid until their own TTL (兼容).
func (p *Platform) verifyAdminToken(token string) (*jwtClaims, bool) {
	claims, err := verifyJWT(p.secret, token)
	if err != nil {
		return nil, false
	}
	if claims.TV != p.tokenVersion() {
		return nil, false
	}
	if claims.SID != "" && !p.sessionActive(claims.SID) {
		return nil, false
	}
	return claims, true
}

// adminTokenFromRequest prefers the httpOnly cookie (S4-B-12) and falls back
// to the Authorization: Bearer header (既有契约不变).
func adminTokenFromRequest(c *fiber.Ctx) string {
	if token := strings.TrimSpace(c.Cookies(adminSessionCookie)); token != "" {
		return token
	}
	return bearerToken(c.Get(fiber.HeaderAuthorization))
}

// jwtGuard protects every admin endpoint except /api/auth/login: JWT via
// httpOnly cookie (preferred) or Authorization Bearer <JWT>; signature,
// expiry, token_version and session revocation verified (契约 B: 改密码后旧
// token 全部失效; S4-GAP 会话: 撤销会话后旧 cookie/Bearer 同样失效).
func (p *Platform) jwtGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := adminTokenFromRequest(c)
		if token == "" {
			return errUnauthorized(c)
		}
		claims, ok := p.verifyAdminToken(token)
		if !ok {
			return errUnauthorized(c)
		}
		c.Locals(localAdminSessionID, claims.SID)
		c.Locals(localAdminUsername, claims.Sub)
		p.touchSession(claims.SID, false)
		return c.Next()
	}
}

// ValidateDataPlaneKey reports whether the request carries a valid, active
// X-API-Key (or Bearer key material). It backs the optional data-plane auth
// switch (security.data_plane_auth) without exposing store internals. S4-GAP
// 密钥增强: an expired key answers false; a non-allowlisted IP is treated the
// same here (the data-plane guard turns any rejection into 401/403 upstream).
func (p *Platform) ValidateDataPlaneKey(c *fiber.Ctx) bool {
	_, denyStatus, ok := p.authenticateAPIKey(c)
	if !ok && denyStatus == fiber.StatusForbidden {
		c.Set("X-Auth-Deny", "forbidden")
		return false
	}
	return ok
}
