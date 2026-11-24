package platform

// 契约 B / 契约 A 管理面 tests: login lockout, password change (token_version),
// admin rate limiting, Retry-After on every 429, AuthAny/ChargeUsage (MCP
// billing), the EngineControl endpoints and the playground mount.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// doJSONErr performs a request and returns status, decoded body and the
// response headers (Retry-After assertions need them).
func doJSONErr(t *testing.T, app *fiber.App, method, target string, body any, headers map[string]string) (int, map[string]any, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, target, err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded, resp.Header
}

// ---------------------------------------------------------------------------
// Login brute-force lockout (契约 B)
// ---------------------------------------------------------------------------

func TestLoginLockoutAfterConsecutiveFailures(t *testing.T) {
	env := newTestEnv(t, 200, false)

	const target = "/api/auth/login"
	// 5 consecutive wrong-password attempts from one IP are allowed to 401…
	for i := 0; i < 5; i++ {
		status, body, _ := doJSONErr(t, env.app, http.MethodPost, target,
			map[string]any{"username": "admin", "password": "wrong"}, nil)
		if status != http.StatusUnauthorized || body["error"] != "unauthorized" {
			t.Fatalf("attempt %d: status=%d body=%v, want 401 unauthorized", i+1, status, body)
		}
	}

	// …the 6th attempt (even with the CORRECT password) must be 429
	// too_many_attempts with Retry-After.
	status, body, headers := doJSONErr(t, env.app, http.MethodPost, target,
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != http.StatusTooManyRequests || body["error"] != "too_many_attempts" {
		t.Fatalf("locked login: status=%d body=%v, want 429 too_many_attempts", status, body)
	}
	if headers.Get("Retry-After") == "" {
		t.Fatal("locked login missing Retry-After header")
	}

	// A different username is not affected by admin's lock.
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, target,
		map[string]any{"username": "root", "password": "nope"}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("different username during lockout: status=%d, want 401 (lock is per username)", status)
	}

	// White-box: expire the lock window → correct credentials work again.
	env.p.loginMu.Lock()
	for key, st := range env.p.loginFails {
		if key.username == "admin" {
			st.lockedUntil = time.Now().Add(-time.Minute)
		}
	}
	env.p.loginMu.Unlock()

	status, body, _ = doJSONErr(t, env.app, http.MethodPost, target,
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != http.StatusOK || body["token"] == nil {
		t.Fatalf("login after lock expiry: status=%d body=%v", status, body)
	}
}

func TestLoginLockoutCannotBeBypassedByRotatingIPs(t *testing.T) {
	env := newTestEnv(t, 200, false)
	const target = "/api/auth/login"

	// Five failures deliberately come from five different source addresses.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, target,
			strings.NewReader(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fmt.Sprintf("203.0.113.%d:4321", i+1)
		resp, err := env.app.Test(req, -1)
		if err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d, want 401", i+1, resp.StatusCode)
		}
	}

	// The correct password from yet another IP must still be locked. Before
	// the fix this returned 200 because each (username,IP) had its own budget.
	req := httptest.NewRequest(http.MethodPost, target,
		strings.NewReader(`{"username":"admin","password":"pw-123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.250:4321"
	resp, err := env.app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rotating-IP login: status=%d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("rotating-IP lock missing Retry-After")
	}
}

func TestUsesDefaultAdminPasswordHonorsCustomMarker(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.cfg.AdminPassword = DefaultPlatformConfig().AdminPassword
	if !env.p.UsesDefaultAdminPassword() {
		t.Fatal("new test platform should report the configured default password")
	}
	if err := env.p.store.PutSetting(settingAdminPasswordCustom, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if env.p.UsesDefaultAdminPassword() {
		t.Fatal("console-changed password must stay custom even if config still has the default")
	}
}

// TestProtectedEndpointsNeverAnswerJSONWithoutAuth pins the invariant behind
// the fasthttp path-normalization class of bugs (GO-2026-4950): whatever the
// encoding, an unauthenticated request must never reach a protected handler
// and come back with a JSON 200. Encoded traversal prefixes may legitimately
// 404 or fall through to a static page, but they must not execute the API.
func TestProtectedEndpointsNeverAnswerJSONWithoutAuth(t *testing.T) {
	env := newTestEnv(t, 200, false)
	protected := []string{
		"/api/keys", "/api/me", "/api/runtime", "/api/usage",
		"/api/engines", "/api/admin/backup", "/api/settings",
	}
	prefixes := []string{
		"", "/%2e%2e", "/%252e%252e", "/..%2f", "/%2e%2e%2f", "/./", "//", "/%09",
	}
	for _, path := range protected {
		for _, prefix := range prefixes {
			target := prefix + path
			req := httptest.NewRequest(http.MethodGet, target, nil)
			resp, err := env.app.Test(req, -1)
			if err != nil {
				t.Fatalf("GET %s: %v", target, err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			ctype := resp.Header.Get("Content-Type")
			isJSON := strings.Contains(strings.ToLower(ctype), "json")
			if isJSON && resp.StatusCode == http.StatusOK {
				t.Fatalf("GET %s reached a protected handler unauthenticated: status=%d ctype=%s body=%.120s",
					target, resp.StatusCode, ctype, body)
			}
		}
	}
}

func TestLoginLockoutConfigFromSecurity(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.SetSecurity(SecurityConfig{
		AdminRatePerMinute: 60,
		LoginLockout:       LoginLockoutConfig{Fails: 2, Minutes: 15},
	})

	for i := 0; i < 2; i++ {
		status, _, _ := doJSONErr(t, env.app, http.MethodPost, "/api/auth/login",
			map[string]any{"username": "admin", "password": "wrong"}, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d", i+1, status)
		}
	}
	status, body, headers := doJSONErr(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != http.StatusTooManyRequests || body["error"] != "too_many_attempts" {
		t.Fatalf("custom fails=2 lockout: status=%d body=%v", status, body)
	}
	if got := headers.Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("Retry-After=%q, want the remaining lock seconds", got)
	}
}

// ---------------------------------------------------------------------------
// Password change + token_version invalidation (契约 B)
// ---------------------------------------------------------------------------

func TestPasswordChangeInvalidatesOldTokens(t *testing.T) {
	env := newTestEnv(t, 200, false)
	oldToken := login(t, env.app)

	// Wrong old password → 401, nothing changes.
	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/api/auth/password",
		map[string]any{"old_password": "nope", "new_password": "brand-new-pass"}, adminHeaders(oldToken))
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong old password: status=%d body=%v", status, body)
	}

	// Short new password → 400.
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/auth/password",
		map[string]any{"old_password": "pw-123", "new_password": "short"}, adminHeaders(oldToken))
	if status != http.StatusBadRequest {
		t.Fatalf("short password: status=%d body=%v, want 400", status, body)
	}

	// Successful change returns a fresh token at the bumped version.
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/auth/password",
		map[string]any{"old_password": "pw-123", "new_password": "brand-new-pass"}, adminHeaders(oldToken))
	if status != http.StatusOK || body["ok"] != true || body["token"] == nil {
		t.Fatalf("password change: status=%d body=%v", status, body)
	}
	newToken, _ := body["token"].(string)

	// The old token is invalid everywhere (token_version bumped).
	status, _, _ = doJSONErr(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(oldToken))
	if status != http.StatusUnauthorized {
		t.Fatalf("old token after change: status=%d, want 401", status)
	}
	// The fresh token works.
	status, _, _ = doJSONErr(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(newToken))
	if status != http.StatusOK {
		t.Fatalf("fresh token after change: status=%d", status)
	}

	// The new password logs in; the old one does not.
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("old password login after change: status=%d body=%v", status, body)
	}
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "brand-new-pass"}, nil)
	if status != http.StatusOK {
		t.Fatalf("new password login: status=%d", status)
	}

	// Restart: the API-changed password OVERRIDES the config default (契约 B),
	// so a fresh Platform on the same data dir accepts the new password only.
	// Close the old platform first: bbolt locks the file, and the restart must
	// open the SAME store to observe the persisted hash + custom marker.
	if err := env.p.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted, err := New(env.p.cfg, nil)
	if err != nil {
		t.Fatalf("restart platform: %v", err)
	}
	defer restarted.Close()
	app := fiber.New()
	restarted.RegisterAdmin(app, nil, nil)
	status, _, _ = doJSONErr(t, app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "admin123"}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("config default password after API change: status=%d, want 401", status)
	}
	status, _, _ = doJSONErr(t, app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "brand-new-pass"}, nil)
	if status != http.StatusOK {
		t.Fatalf("API-changed password after restart: status=%d", status)
	}
}

// ---------------------------------------------------------------------------
// Admin API global rate limit (契约 B)
// ---------------------------------------------------------------------------

func TestAdminRateLimit429WithRetryAfter(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.SetSecurity(SecurityConfig{AdminRatePerMinute: 3})

	token := login(t, env.app) // 1 token consumed by the login itself.
	// Burst allows 2 more, then the limiter answers 429 rate_limited.
	saw429 := false
	for i := 0; i < 4; i++ {
		status, body, headers := doJSONErr(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(token))
		if status == http.StatusTooManyRequests {
			saw429 = true
			if body["error"] != "rate_limited" {
				t.Fatalf("rate limited body: %v", body)
			}
			if headers.Get("Retry-After") == "" {
				t.Fatal("admin rate limit missing Retry-After")
			}
			break
		}
		if status != http.StatusOK {
			t.Fatalf("/api/me under limiter: status=%d", status)
		}
	}
	if !saw429 {
		t.Fatal("admin rate limiter never triggered")
	}
}

// ---------------------------------------------------------------------------
// 429 quota envelope carries Retry-After (契约 B 全量落实)
// ---------------------------------------------------------------------------

func TestQuotaExceededCarriesRetryAfter(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "tiny", 1)

	status, body, headers := doJSONErr(t, env.app, http.MethodPost, "/v1/index/check",
		map[string]any{"urls": []string{"https://example.com/a"}, "engines": []string{"baidu", "bing"}}, keyHeaders(key))
	if status != http.StatusTooManyRequests || body["error"] != "quota_exceeded" {
		t.Fatalf("quota exceeded: status=%d body=%v", status, body)
	}
	if headers.Get("Retry-After") == "" {
		t.Fatal("quota_exceeded missing Retry-After header (S2 断言)")
	}
	if _, ok := body["retry_after_seconds"]; !ok {
		t.Fatalf("quota_exceeded missing retry_after_seconds: %v", body)
	}
}

// ---------------------------------------------------------------------------
// AuthAny + ChargeUsage (契约 C seam)
// ---------------------------------------------------------------------------

type engineControlFake struct {
	mu       sync.Mutex
	status   any
	resets   []string
	forces   []string
	forceErr error
	resetErr error
}

func (f *engineControlFake) EnginesStatusWithCooldown() any {
	return f.status
}

func (f *engineControlFake) ResetCooldown(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets = append(f.resets, name)
	return f.resetErr
}

func (f *engineControlFake) ForceCooldown(name string, minutes int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forces = append(f.forces, fmt.Sprintf("%s:%d", name, minutes))
	if minutes <= 0 {
		return errors.New("minutes must be > 0")
	}
	return f.forceErr
}

const mcpCheckBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_indexed","arguments":{"urls":["https://a.example.com/","https://b.example.com/"]}}}`
const mcpSearchBody = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"golang"}}}`

func TestAuthAnyMCPBillingUnits(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "mcp", 0) // unlimited

	app := fiber.New()
	app.Post("/web-tool/mcp", env.p.AuthAny(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}})
	})

	// web_search = 1 unit.
	status, _, _ := doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(mcpSearchBody), keyHeaders(key))
	if status != http.StatusOK {
		t.Fatalf("mcp web_search: status=%d", status)
	}
	if used := quotaUsedToday(t, env.app, key); used != 1 {
		t.Fatalf("web_search charged %v units, want 1", used)
	}

	// check_indexed 2 URLs × default engines (4) = 8 units.
	status, _, _ = doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(mcpCheckBody), keyHeaders(key))
	if status != http.StatusOK {
		t.Fatalf("mcp check_indexed: status=%d", status)
	}
	if used := quotaUsedToday(t, env.app, key); used != 9 {
		t.Fatalf("after check_indexed used_today=%v, want 9 (1 + 8)", used)
	}

	// Non-tool methods are free.
	freeBody := `{"jsonrpc":"2.0","id":3,"method":"ping"}`
	status, _, _ = doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(freeBody), keyHeaders(key))
	if status != http.StatusOK {
		t.Fatalf("mcp ping: status=%d", status)
	}
	if used := quotaUsedToday(t, env.app, key); used != 9 {
		t.Fatalf("ping must be free: used_today=%v", used)
	}

	// The usage journal holds the two billed calls with charged units.
	status, body, _ := doJSONErr(t, env.app, http.MethodGet, "/api/usage?limit=10", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("usage: status=%d", status)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("usage items after MCP calls: %v", body)
	}
	latest, _ := items[0].(map[string]any)
	if latest["units"] != float64(8) || latest["endpoint"] != "/web-tool/mcp" {
		t.Fatalf("latest MCP usage item: %v", latest)
	}
}

func TestAuthAnyMCPQuotaPrecheckAndJWT(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "mcp-quota", 5)

	app := fiber.New()
	app.Post("/web-tool/mcp", env.p.AuthAny(), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})

	// 2 URLs × 4 default engines = 8 units > quota 5 → 429 quota_exceeded with
	// Retry-After, and nothing is consumed.
	status, body, headers := doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(mcpCheckBody), keyHeaders(key))
	if status != http.StatusTooManyRequests || body["error"] != "quota_exceeded" {
		t.Fatalf("mcp quota pre-check: status=%d body=%v", status, body)
	}
	if headers.Get("Retry-After") == "" {
		t.Fatal("mcp quota pre-check missing Retry-After")
	}
	if used := quotaUsedToday(t, env.app, key); used != 0 {
		t.Fatalf("rejected mcp call consumed quota: %v", used)
	}

	// Bearer JWT (admin) → no billing at all, even for tool calls.
	jwtHeaders := map[string]string{"Authorization": "Bearer " + token}
	status, _, _ = doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(mcpCheckBody), jwtHeaders)
	if status != http.StatusOK {
		t.Fatalf("mcp with JWT: status=%d", status)
	}
	_, body, _ = doJSONErr(t, env.app, http.MethodGet, "/api/usage?limit=10", nil, adminHeaders(token))
	items, _ := body["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("JWT calls must not be billed: %v", body)
	}

	// No credential at all → 401 unauthorized.
	status, body, _ = doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(mcpCheckBody), nil)
	if status != http.StatusUnauthorized || body["error"] != "unauthorized" {
		t.Fatalf("mcp without credential: status=%d body=%v", status, body)
	}
}

func TestChargeUsageConsumesOnlyOn200(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	keyID, key := createKey(t, env, token, "charge", 10)

	if err := env.p.ChargeUsage(keyID, 3, "/web-tool/mcp", http.StatusOK, 12); err != nil {
		t.Fatalf("ChargeUsage: %v", err)
	}
	if used := quotaUsedToday(t, env.app, key); used != 3 {
		t.Fatalf("charged units: %v, want 3", used)
	}

	// A failed call records usage but consumes nothing.
	if err := env.p.ChargeUsage(keyID, 3, "/web-tool/mcp", http.StatusBadGateway, 30); err != nil {
		t.Fatalf("ChargeUsage failed call: %v", err)
	}
	if used := quotaUsedToday(t, env.app, key); used != 3 {
		t.Fatalf("failed call consumed quota: %v, want 3", used)
	}
	_, body, _ := doJSONErr(t, env.app, http.MethodGet, "/api/usage?limit=10", nil, adminHeaders(token))
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("usage rows after ChargeUsage: %v", body)
	}
	latest, _ := items[0].(map[string]any)
	if latest["status"] != float64(http.StatusBadGateway) || latest["units"] != float64(3) {
		t.Fatalf("failed-call usage row: %v", latest)
	}
}

// ---------------------------------------------------------------------------
// EngineControl endpoints + playground mount (契约 A 管理面)
// ---------------------------------------------------------------------------

func TestEngineControlEndpoints(t *testing.T) {
	env := newTestEnv(t, 200, false)
	control := &engineControlFake{
		status: []map[string]any{
			{"engine": "baidu", "state": "cooldown", "until": "2026-01-01T00:10:00Z", "reason": "COOLDOWN", "consecutive_blocks": 1, "last_error_at": "2026-01-01T00:00:00Z"},
			{"engine": "sogou", "state": "ok", "until": nil, "reason": "", "consecutive_blocks": 0, "last_error_at": nil},
		},
	}
	env.p.RegisterAdmin(env.app, nil, control)
	token := login(t, env.app)

	// GET /api/engines carries the extended fields verbatim.
	status, body, _ := doJSONErr(t, env.app, http.MethodGet, "/api/engines", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("engines: status=%d", status)
	}
	engines, _ := body["engines"].([]any)
	if len(engines) != 2 {
		t.Fatalf("engines list: %v", body)
	}
	baidu, _ := engines[0].(map[string]any)
	for _, field := range []string{"engine", "state", "until", "reason", "consecutive_blocks", "last_error_at"} {
		if _, ok := baidu[field]; !ok {
			t.Fatalf("/api/engines entry missing %q: %v", field, baidu)
		}
	}
	if baidu["state"] != "cooldown" || baidu["reason"] != "COOLDOWN" || baidu["consecutive_blocks"] != float64(1) {
		t.Fatalf("baidu entry: %v", baidu)
	}

	// POST reset → ok:true; force → ok:true with minutes echoed.
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/reset", nil, adminHeaders(token))
	if status != http.StatusOK || body["ok"] != true || body["engine"] != "baidu" {
		t.Fatalf("reset: status=%d body=%v", status, body)
	}
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/force",
		map[string]any{"minutes": 30}, adminHeaders(token))
	if status != http.StatusOK || body["ok"] != true || body["minutes"] != float64(30) {
		t.Fatalf("force: status=%d body=%v", status, body)
	}
	control.mu.Lock()
	if len(control.resets) != 1 || control.resets[0] != "baidu" || len(control.forces) != 1 || control.forces[0] != "baidu:30" {
		t.Fatalf("control calls: resets=%v forces=%v", control.resets, control.forces)
	}
	control.mu.Unlock()

	// Without a token both endpoints are 401.
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/reset", nil, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("reset without token: status=%d", status)
	}
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/force",
		map[string]any{"minutes": 30}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("force without token: status=%d", status)
	}

	// minutes<=0 and unknown engines are 400.
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/force",
		map[string]any{"minutes": 0}, adminHeaders(token))
	if status != http.StatusBadRequest {
		t.Fatalf("force minutes=0: status=%d", status)
	}
	control.resetErr = errors.New("unknown engine: nosuch")
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/nosuch/cooldown/reset", nil, adminHeaders(token))
	if status != http.StatusBadRequest {
		t.Fatalf("reset unknown engine: status=%d", status)
	}
}

func TestEngineControlNilDegradesGracefully(t *testing.T) {
	env := newTestEnv(t, 200, false) // RegisterAdmin(app, nil, nil)
	token := login(t, env.app)

	// Cooldown endpoints answer 503 engine_control_unavailable.
	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/reset", nil, adminHeaders(token))
	if status != http.StatusServiceUnavailable || body["error"] != "engine_control_unavailable" {
		t.Fatalf("reset with nil control: status=%d body=%v", status, body)
	}
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/engines/baidu/cooldown/force",
		map[string]any{"minutes": 10}, adminHeaders(token))
	if status != http.StatusServiceUnavailable || body["error"] != "engine_control_unavailable" {
		t.Fatalf("force with nil control: status=%d body=%v", status, body)
	}

	// GET /api/engines degrades to the legacy shape (engineStatus provider).
	status, body, _ = doJSONErr(t, env.app, http.MethodGet, "/api/engines", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("engines with nil control: status=%d", status)
	}
	if _, isMap := body["engines"].(map[string]any); !isMap {
		t.Fatalf("legacy engines shape expected: %v", body)
	}
}

func TestPlaygroundCheckMount(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.RegisterAdmin(env.app, stubIndexCheck(200, false), nil)
	token := login(t, env.app)

	// Without token → 401 (JWT guarded, not billed).
	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/api/playground/check",
		map[string]any{"urls": []string{"https://example.com/p"}, "engines": []string{"baidu"}}, nil)
	if status != http.StatusUnauthorized || body["error"] != "unauthorized" {
		t.Fatalf("playground without token: status=%d body=%v", status, body)
	}

	// With the JWT the stub envelope passes through verbatim (same structure
	// as /v1/index/check).
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/playground/check",
		map[string]any{"urls": []string{"https://example.com/p"}, "engines": []string{"baidu"}}, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("playground with token: status=%d body=%v", status, body)
	}
	for _, field := range []string{"id", "created_at", "results", "meta"} {
		if _, ok := body[field]; !ok {
			t.Fatalf("playground response missing %q: %v", field, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Retention (用量日志滚动清理上限)
// ---------------------------------------------------------------------------

func TestUsageRetentionPrunesOldRecords(t *testing.T) {
	dir := t.TempDir()
	p, err := New(PlatformConfig{
		Enabled: true, DataDir: dir, AdminUsername: "admin", AdminPassword: "pw-123",
		JWTExpireHours: 24, UsageRetentionDays: 1,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	now := time.Now()
	for i := 0; i < 5; i++ {
		rec := &UsageRecord{Time: now.Add(-48 * time.Hour), KeyID: "k", Endpoint: "/v1/index/check", Units: 1, Status: 200}
		if err := p.recordUsage(rec, "k", 0, false, now.Add(-48*time.Hour)); err != nil {
			t.Fatalf("record old: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		rec := &UsageRecord{Time: now, KeyID: "k", Endpoint: "/v1/index/check", Units: 1, Status: 200}
		if err := p.recordUsage(rec, "k", 0, false, now); err != nil {
			t.Fatalf("record fresh: %v", err)
		}
	}

	p.pruneUsage()
	all, err := p.store.AllUsage()
	if err != nil || len(all) != 3 {
		t.Fatalf("after prune: len=%d err=%v, want 3 (old records pruned)", len(all), err)
	}
}

func TestUsageRetentionUnlimitedKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	p, err := New(PlatformConfig{
		Enabled: true, DataDir: dir, AdminUsername: "admin", AdminPassword: "pw-123",
		JWTExpireHours: 24, UsageRetentionDays: 0,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	now := time.Now()
	rec := &UsageRecord{Time: now.Add(-240 * time.Hour), KeyID: "k", Endpoint: "/v1/index/check", Units: 1, Status: 200}
	if err := p.recordUsage(rec, "k", 0, false, now.Add(-240*time.Hour)); err != nil {
		t.Fatalf("record: %v", err)
	}
	p.pruneUsage()
	if all, err := p.store.AllUsage(); err != nil || len(all) != 1 {
		t.Fatalf("retention disabled must keep records: len=%d err=%v", len(all), err)
	}
}

// ---------------------------------------------------------------------------
// Lockout / rate-limit helpers guard rails
// ---------------------------------------------------------------------------

func TestSecurityConfigNormalization(t *testing.T) {
	got := SecurityConfig{}.normalized()
	if got.AdminRatePerMinute != 60 || got.LoginLockout.Fails != 5 || got.LoginLockout.Minutes != 15 {
		t.Fatalf("defaults: %+v", got)
	}
	if got.DataPlaneAuth {
		t.Fatal("data_plane_auth must default to false")
	}
	got = SecurityConfig{AdminRatePerMinute: -1, LoginLockout: LoginLockoutConfig{Fails: -2, Minutes: -3}}.normalized()
	if got.AdminRatePerMinute != 60 || got.LoginLockout.Fails != 5 || got.LoginLockout.Minutes != 15 {
		t.Fatalf("invalid values normalized: %+v", got)
	}
}

func TestMCPToolUnitsParsing(t *testing.T) {
	if got := mcpToolUnits([]byte(mcpSearchBody)); got != 1 {
		t.Fatalf("web_search units=%d, want 1", got)
	}
	if got := mcpToolUnits([]byte(mcpCheckBody)); got != 8 {
		t.Fatalf("check_indexed 2 URLs × default = %d, want 8", got)
	}
	custom := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_indexed","arguments":{"urls":["https://a.example.com/"],"engines":["baidu","bing"]}}}`
	if got := mcpToolUnits([]byte(custom)); got != 2 {
		t.Fatalf("check_indexed 1 URL × 2 engines = %d, want 2", got)
	}
	alias := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_indexed","arguments":{"urls":["https://a.example.com/"],"engines":["360"]}}}`
	if got := mcpToolUnits([]byte(alias)); got != 1 {
		t.Fatalf("alias 360 engines sizing = %d, want 1", got)
	}
	if got := mcpToolUnits([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_quota"}}`)); got != 0 {
		t.Fatalf("get_quota must be free, got %d", got)
	}
	if got := mcpToolUnits([]byte("{not json")); got != 0 {
		t.Fatalf("malformed body must be free, got %d", got)
	}
	if got := mcpToolUnits(nil); got != 0 {
		t.Fatalf("empty body must be free, got %d", got)
	}
	if !strings.Contains(mcpCheckBody, "check_indexed") {
		t.Fatal("test fixture drift")
	}
}

// ---------------------------------------------------------------------------
// T-02 / T-03: MCP 计费与 /v1 同口径(成功单元计费 + EngineStats + outcome)
// ---------------------------------------------------------------------------

// mcpToolTextEnvelope wraps a tool payload as the MCP tools/call response shape
// ({result:{content:[{type:"text",text:"<JSON字符串>"}]}}).
func mcpToolTextEnvelope(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	text, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
			"isError": false,
		},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

// TestAuthAnyMCPChargesEngineOKUnits (T-02/T-03): a check_indexed MCP call whose
// tool text carries the per-engine envelope is charged per OK unit and journals
// engine_stats + outcome exactly like the /v1 plane.
func TestAuthAnyMCPChargesEngineOKUnits(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "mcp-ok", 0)

	app := fiber.New()
	app.Post("/web-tool/mcp", env.p.AuthAny(), func(c *fiber.Ctx) error {
		payload := map[string]any{
			"results": []map[string]any{
				{"url": "https://a.example.com/", "engines": map[string]any{
					"baidu": map[string]any{"indexed": true, "match": "none", "matched_result_url": "", "total_results": 3, "took_ms": 12},
					"sogou": map[string]any{"error": map[string]any{"code": 503, "message": "engine_cooldown"}},
				}},
			},
		}
		return c.Send(mcpToolTextEnvelope(t, payload))
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check_indexed","arguments":{"urls":["https://a.example.com/"],"engines":["baidu","sogou"]}}}`
	status, _, _ := doJSONErr(t, app, http.MethodPost, "/web-tool/mcp", json.RawMessage(body), keyHeaders(key))
	if status != http.StatusOK {
		t.Fatalf("mcp check_indexed: status=%d", status)
	}
	// 成功单元计费: baidu 1,sogou error 单元不计费(请求全额应为 2)。
	if used := quotaUsedToday(t, env.app, key); used != 1 {
		t.Fatalf("charged %v units, want 1 (engine OK units)", used)
	}

	// outcome 与 units 走 /api/usage 列表视图(engine_stats 不在该视图契约里)。
	status, bodyOut, _ := doJSONErr(t, env.app, http.MethodGet, "/api/usage?limit=5", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("usage: status=%d", status)
	}
	items := bodyOut["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("usage items: %v", bodyOut)
	}
	rec := items[0].(map[string]any)
	if rec["outcome"] != "billed" {
		t.Fatalf("MCP usage record outcome = %v, want billed (T-03)", rec["outcome"])
	}
	if rec["units"] != float64(1) {
		t.Fatalf("MCP usage record units = %v, want 1", rec["units"])
	}

	// engine_stats 落账与否经 reconcile 验证: journaled=1(charged=1, 无 mismatch)。
	status, rc, _ := doJSONErr(t, env.app, http.MethodGet, "/api/reconcile", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("reconcile: %d %v", status, rc)
	}
	if rc["charged_units"] != float64(1) || rc["journaled_units"] != float64(1) || rc["mismatch_count"] != float64(0) {
		t.Fatalf("reconcile must see MCP engine_stats: %v", rc)
	}
	if rc["unverified_count"] != float64(0) {
		t.Fatalf("reconcile unverified: %v", rc)
	}
}

// TestReconcileIgnoresRecordsWithoutEngineStats (T-02): 无引擎明细的记录
// (MCP web_search 等)按落账口径计入、不判 mismatch;有明细的记录正常对账。
func TestReconcileIgnoresRecordsWithoutEngineStats(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	keyID, _ := createKey(t, env, token, "reconcile-mcp", 0)

	// 无引擎明细(MCP web_search 类): charged 1, engine_stats nil。
	if err := env.p.ChargeUsage(keyID, 1, "/web-tool/mcp", http.StatusOK, 10); err != nil {
		t.Fatalf("ChargeUsage: %v", err)
	}
	// 有引擎明细: charged 2 == engine OK 2。
	stats := map[string]EngineStat{
		"baidu": {Calls: 1, OK: 1},
		"bing":  {Calls: 1, OK: 1},
	}
	if err := env.p.chargeUsage(keyID, 2, "/web-tool/mcp", http.StatusOK, 10, stats); err != nil {
		t.Fatalf("chargeUsage: %v", err)
	}

	status, body, _ := doJSONErr(t, env.app, http.MethodGet, "/api/reconcile", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("reconcile: %d %v", status, body)
	}
	if body["mismatch_count"] != float64(0) {
		t.Fatalf("reconcile mismatch: %v", body)
	}
	if body["charged_units"] != float64(3) || body["journaled_units"] != float64(3) {
		t.Fatalf("reconcile totals: %v", body)
	}
	if body["unverified_count"] != float64(1) {
		t.Fatalf("reconcile unverified: %v", body)
	}
}
