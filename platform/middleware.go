package platform

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
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
// disabled keys are rejected (禁用 key 一律 401).
func (p *Platform) authenticateAPIKey(c *fiber.Ctx) (*APIKey, bool) {
	raw := strings.TrimSpace(c.Get(apiKeyHeader))
	if raw == "" {
		raw = bearerToken(c.Get(fiber.HeaderAuthorization))
	}
	if raw == "" {
		return nil, false
	}
	key, err := p.store.GetKeyByHash(sha256Hex(raw))
	if err != nil || key == nil {
		return nil, false
	}
	if key.Status != KeyStatusActive {
		return nil, false
	}
	return key, true
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

		key, ok := p.authenticateAPIKey(c)
		if !ok {
			return errUnauthorized(c)
		}

		urls, engines, parsed := p.parseIndexRequest(c)
		units := 0
		if parsed {
			units = len(urls) * len(engines)
		}

		if units > 0 && key.DailyQuota > 0 && usedTodayUnits(key, time.Now())+int64(units) > key.DailyQuota {
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
		// /v1/quota and /v1/engines stay out of the usage journal. The route may
		// be mounted under a base path (the serve command registers everything
		// under a fixed prefix), so match the /v1/index segment anywhere in the
		// path instead of assuming a root mount.
		if strings.Contains(endpoint, "/v1/index") || strings.Contains(c.Path(), "/v1/index") {
			p.writeUsage(c, key, endpoint, urls, engines, units, started, handlerErr)
		}
		return handlerErr
	}
}

// writeUsage persists the usage record and key counters after an index/check
// request. 计费口径(按成功单元扣费): the charged units are the number of OK
// units derived from the response envelope (units whose engine field carries
// an error object are not charged — including raw-mode not_implemented
// pseudo-units). Quota pre-checks stay conservative at full units. If the
// envelope cannot be parsed (defensive), the full requested units are charged,
// matching the pre-2026 behavior.
func (p *Platform) writeUsage(c *fiber.Ctx, key *APIKey, endpoint string, urls, engines []string, units int, started time.Time, handlerErr error) {
	at := time.Now()
	status := c.Response().StatusCode()
	if status == fiber.StatusOK && handlerErr != nil {
		status = fiber.StatusInternalServerError
	}
	engineStats := parseEngineStatsFromBody(c.Response().Body())
	charged := units
	if engineStats != nil {
		charged = 0
		for _, s := range engineStats {
			charged += int(s.OK)
		}
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
	}
	consume := charged > 0 && status == fiber.StatusOK && handlerErr == nil
	if err := p.recordUsage(rec, key.ID, int64(charged), consume, at); err != nil {
		logrus.WithError(err).Warn("platform: 写入 usage 记录失败")
	}
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

// jwtGuard protects every admin endpoint except /api/auth/login: Authorization
// Bearer <JWT>, signature, expiry and token_version verified (契约 B: 改密码后
// 旧 token 全部失效).
func (p *Platform) jwtGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := bearerToken(c.Get(fiber.HeaderAuthorization))
		if token == "" {
			return errUnauthorized(c)
		}
		claims, err := verifyJWT(p.secret, token)
		if err != nil {
			return errUnauthorized(c)
		}
		if claims.TV != p.tokenVersion() {
			return errUnauthorized(c)
		}
		return c.Next()
	}
}

// ValidateDataPlaneKey reports whether the request carries a valid, active
// X-API-Key (or Bearer key material). It backs the optional data-plane auth
// switch (security.data_plane_auth) without exposing store internals.
func (p *Platform) ValidateDataPlaneKey(c *fiber.Ctx) bool {
	_, ok := p.authenticateAPIKey(c)
	return ok
}
