package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ---------------------------------------------------------------------------
// Test scaffolding
// ---------------------------------------------------------------------------

// stubIndexCheck mimics the integration-stage index handler: it echoes the
// locals injected by APIKeyGuard into a contract-shaped envelope, with an
// optional per-engine error (bing blocked) so usage parsing can be tested.
func stubIndexCheck(status int, failBing bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if status != fiber.StatusOK {
			return c.Status(status).JSON(fiber.Map{
				"error":  "bad_request",
				"code":   "EMPTY_QUERY",
				"reason": "EMPTY_QUERY",
			})
		}
		urls, _ := c.Locals(localKeyURLs).([]string)
		engines, _ := c.Locals(localKeyEngines).([]string)
		units, _ := c.Locals(localKeyUnits).(int)
		results := make([]fiber.Map, 0, len(urls))
		for _, u := range urls {
			enginesOut := fiber.Map{}
			for _, e := range engines {
				if e == "bing" && failBing {
					enginesOut[e] = fiber.Map{"error": fiber.Map{"code": "blocked", "message": "bing blocked"}}
					continue
				}
				enginesOut[e] = fiber.Map{
					"indexed":            true,
					"match":              "exact",
					"matched_result_url": u,
					"total_results":      3,
					"took_ms":            5,
				}
			}
			results = append(results, fiber.Map{"url": u, "engines": enginesOut})
		}
		return c.JSON(fiber.Map{
			"id":         "stub-1",
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"results":    results,
			"meta":       fiber.Map{"request_id": "stub-req", "took_ms": 6, "units": units},
		})
	}
}

type testEnv struct {
	p   *Platform
	app *fiber.App
	dir string
}

type retentionPreviewFake struct{ engineControlFake }

func (f *retentionPreviewFake) IndexJobRetentionPreview(days int) (int, int64, error) {
	return days, 1234, nil
}

func TestConsoleJobsRequireAuthenticatedUser(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.engineControl = &retentionPreviewFake{}
	called := 0
	env.p.RegisterConsoleJobs(env.app, IndexJobHandlers{List: func(c *fiber.Ctx) error {
		called++
		return c.JSON(fiber.Map{"ok": true})
	}})
	status, _ := doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs", nil, nil)
	if status != http.StatusUnauthorized || called != 0 {
		t.Fatalf("anonymous request reached handler: status=%d calls=%d", status, called)
	}
	token := login(t, env.app)
	status, _ = doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs", nil, adminHeaders(token))
	if status != http.StatusOK || called != 1 {
		t.Fatalf("authenticated request failed: status=%d calls=%d", status, called)
	}
	status, preview := doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs-retention-preview?days=30", nil, adminHeaders(token))
	if status != http.StatusOK || preview["jobs"] != float64(30) || preview["bytes"] != float64(1234) {
		t.Fatalf("preview: status=%d body=%v", status, preview)
	}
	status, _ = doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs-retention-preview?days=0", nil, adminHeaders(token))
	if status != http.StatusBadRequest {
		t.Fatalf("invalid retention: status=%d", status)
	}
	createConsoleUser(t, env.app, token, "job_viewer", RoleViewer)
	viewer := loginAs(t, env.app, "job_viewer", "pw-123456")
	status, _ = doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs-retention-preview?days=30", nil, adminHeaders(viewer))
	if status != http.StatusForbidden {
		t.Fatalf("viewer accessed retention preview: status=%d", status)
	}
}

func newTestEnv(t *testing.T, indexStatus int, failBing bool) *testEnv {
	t.Helper()
	env := &testEnv{dir: t.TempDir()}
	env.p = mustNewPlatform(t, env.dir, func() any {
		return fiber.Map{"baidu": fiber.Map{"state": "closed"}, "sogou": fiber.Map{"state": "closed"}}
	})
	env.app = fiber.New()
	env.p.RegisterAdmin(env.app, nil, nil)
	env.p.RegisterPublic(env.app, stubIndexCheck(indexStatus, failBing))
	return env
}

func mustNewPlatform(t *testing.T, dir string, engineStatus func() any) *Platform {
	t.Helper()
	p, err := New(PlatformConfig{
		Enabled:        true,
		DataDir:        dir,
		AdminUsername:  "admin",
		AdminPassword:  "pw-123",
		JWTExpireHours: 24,
	}, engineStatus)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// S4 限速默认值(per-IP admin / per-key API / unauth)会让高频测试在
	// 突发上限处收到 429,与被测行为无关——默认关闭,专项测试自己打开
	// (TestAdminPerIPRateLimit / TestPerKeyRateLimit / TestUnauthRateLimit)。
	p.SetSecurity(SecurityConfig{
		AdminRatePerIP:      0,
		APIRatePerMinute:    0,
		UnauthRatePerMinute: 0,
	})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// doRaw performs an in-process request and returns status + raw body bytes.
func doRaw(t *testing.T, app *fiber.App, method, target string, body any, headers map[string]string) (int, []byte) {
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
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, raw
}

// doJSON performs an in-process request and returns status + decoded object body.
func doJSON(t *testing.T, app *fiber.App, method, target string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	status, raw := doRaw(t, app, method, target, body, headers)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return status, out
}

func doJSONRaw(t *testing.T, app *fiber.App, method, target string, rawBody string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if rawBody != "" {
		reader = strings.NewReader(rawBody)
	}
	req := httptest.NewRequest(method, target, reader)
	if rawBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// login obtains an admin token.
func login(t *testing.T, app *fiber.App) string {
	t.Helper()
	status, body := doJSON(t, app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != 200 {
		t.Fatalf("login: status=%d body=%v", status, body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("login: missing token: %v", body)
	}
	return token
}

func adminHeaders(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func keyHeaders(key string) map[string]string {
	return map[string]string{"X-API-Key": key}
}

// createKey creates a key via the admin API and returns (id, fullKey).
func createKey(t *testing.T, env *testEnv, token, name string, quota int64) (string, string) {
	t.Helper()
	req := map[string]any{"name": name}
	if quota >= 0 {
		req["daily_quota"] = quota
	}
	status, body := doJSON(t, env.app, http.MethodPost, "/api/keys", req, adminHeaders(token))
	if status != 200 {
		t.Fatalf("create key: status=%d body=%v", status, body)
	}
	id, _ := body["id"].(string)
	key, _ := body["key"].(string)
	if id == "" || key == "" {
		t.Fatalf("create key: missing id/key: %v", body)
	}
	return id, key
}

var fullKeyPattern = regexp.MustCompile(`^sk-w2a-[0-9a-f]{32}$`)

// ---------------------------------------------------------------------------
// Admin: login, me, key lifecycle
// ---------------------------------------------------------------------------

func TestLoginAndAdminAuth(t *testing.T) {
	env := newTestEnv(t, 200, false)

	// Wrong password → 401 {"error":"unauthorized"}.
	status, body := doJSON(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "nope"}, nil)
	if status != 401 || body["error"] != "unauthorized" {
		t.Fatalf("bad login: status=%d body=%v", status, body)
	}
	// Unknown user → 401.
	status, _ = doJSON(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "root", "password": "pw-123"}, nil)
	if status != 401 {
		t.Fatalf("unknown user: status=%d", status)
	}

	// Success → token, expires_in=86400, user.username.
	status, body = doJSON(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != 200 || body["expires_in"] != float64(86400) {
		t.Fatalf("login: status=%d body=%v", status, body)
	}
	user, _ := body["user"].(map[string]any)
	if user == nil || user["username"] != "admin" {
		t.Fatalf("login user: %v", body)
	}
	token := body["token"].(string)

	// /api/me requires JWT.
	status, body = doJSON(t, env.app, http.MethodGet, "/api/me", nil, nil)
	if status != 401 || body["error"] != "unauthorized" {
		t.Fatalf("me without token: status=%d body=%v", status, body)
	}
	status, body = doJSON(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(token))
	if status != 200 || body["username"] != "admin" ||
		body["keys_count"] != float64(0) || body["today_calls"] != float64(0) || body["total_calls"] != float64(0) {
		t.Fatalf("me: status=%d body=%v", status, body)
	}

	// Tampered JWT rejected.
	status, _ = doJSON(t, env.app, http.MethodGet, "/api/me", nil,
		map[string]string{"Authorization": "Bearer " + token + "x"})
	if status != 401 {
		t.Fatalf("tampered admin token: status=%d", status)
	}
}

func TestAdminKeyLifecycle(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)

	// Missing name → 400.
	status, body := doJSON(t, env.app, http.MethodPost, "/api/keys", map[string]any{"name": "  "}, adminHeaders(token))
	if status != 400 || body["error"] != "bad_request" {
		t.Fatalf("create key empty name: status=%d body=%v", status, body)
	}
	// Malformed body → 400.
	status, _ = doJSONRaw(t, env.app, http.MethodPost, "/api/keys", "{not json", adminHeaders(token))
	if status != 400 {
		t.Fatalf("create key malformed body: status=%d", status)
	}
	// Negative quota → 400.
	status, _ = doJSON(t, env.app, http.MethodPost, "/api/keys", map[string]any{"name": "x", "daily_quota": -1}, adminHeaders(token))
	if status != 400 {
		t.Fatalf("create key negative quota: status=%d", status)
	}

	id, fullKey := createKey(t, env, token, "ci", 10)
	if !fullKeyPattern.MatchString(fullKey) {
		t.Fatalf("key format: %s", fullKey)
	}

	// List: one item, full key not included, prefix shown.
	status, listBytes := doRaw(t, env.app, http.MethodGet, "/api/keys", nil, adminHeaders(token))
	if status != 200 {
		t.Fatalf("list keys: status=%d", status)
	}
	var items []map[string]any
	if err := json.Unmarshal(listBytes, &items); err != nil || len(items) != 1 {
		t.Fatalf("list keys: len=%d err=%v", len(items), err)
	}
	item := items[0]
	if item["id"] != id || item["name"] != "ci" || item["status"] != "active" ||
		item["daily_quota"] != float64(10) || item["prefix"] == "" {
		t.Fatalf("list keys item: %v", item)
	}
	if _, ok := item["used_today"]; !ok {
		t.Fatalf("list keys missing used_today: %v", item)
	}
	if strings.Contains(string(listBytes), fullKey) {
		t.Fatal("full key must never be listed")
	}

	// Disable via PATCH.
	status, body = doJSON(t, env.app, http.MethodPatch, "/api/keys/"+id,
		map[string]any{"status": "disabled"}, adminHeaders(token))
	if status != 200 || body["status"] != "disabled" {
		t.Fatalf("patch disable: status=%d body=%v", status, body)
	}
	// Disabled key rejected publicly.
	status, body = doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, keyHeaders(fullKey))
	if status != 401 || body["error"] != "unauthorized" {
		t.Fatalf("disabled key: status=%d body=%v", status, body)
	}
	// Invalid patch values.
	status, _ = doJSON(t, env.app, http.MethodPatch, "/api/keys/"+id, map[string]any{"status": "paused"}, adminHeaders(token))
	if status != 400 {
		t.Fatalf("patch invalid status: status=%d", status)
	}
	status, _ = doJSON(t, env.app, http.MethodPatch, "/api/keys/"+id, map[string]any{"daily_quota": -5}, adminHeaders(token))
	if status != 400 {
		t.Fatalf("patch negative quota: status=%d", status)
	}
	// Re-enable + rename.
	status, body = doJSON(t, env.app, http.MethodPatch, "/api/keys/"+id,
		map[string]any{"status": "active", "name": "ci-2", "daily_quota": 20}, adminHeaders(token))
	if status != 200 || body["name"] != "ci-2" || body["daily_quota"] != float64(20) {
		t.Fatalf("patch enable: status=%d body=%v", status, body)
	}

	// Delete → {"ok":true}; second delete → 404; patch deleted → 404.
	status, body = doJSON(t, env.app, http.MethodDelete, "/api/keys/"+id, nil, adminHeaders(token))
	if status != 200 || body["ok"] != true {
		t.Fatalf("delete key: status=%d body=%v", status, body)
	}
	status, _ = doJSON(t, env.app, http.MethodDelete, "/api/keys/"+id, nil, adminHeaders(token))
	if status != 404 {
		t.Fatalf("delete twice: status=%d", status)
	}
	status, _ = doJSON(t, env.app, http.MethodPatch, "/api/keys/"+id, map[string]any{"name": "z"}, adminHeaders(token))
	if status != 404 {
		t.Fatalf("patch deleted key: status=%d", status)
	}
}

// ---------------------------------------------------------------------------
// Public guard: auth, quota, alias, GET variant
// ---------------------------------------------------------------------------

func TestAPIKeyGuardAuthAndQuota(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "ci", 5)

	checkBody := func(engines ...string) map[string]any {
		return map[string]any{"urls": []string{"https://example.com/a"}, "engines": engines}
	}

	// No key → 401; unknown key → 401.
	status, body := doJSON(t, env.app, http.MethodPost, "/v1/index/check", checkBody("baidu", "bing"), nil)
	if status != 401 || body["error"] != "unauthorized" {
		t.Fatalf("no key: status=%d body=%v", status, body)
	}
	status, _ = doJSON(t, env.app, http.MethodPost, "/v1/index/check", checkBody("baidu"), keyHeaders("sk-w2a-00000000000000000000000000000000"))
	if status != 401 {
		t.Fatalf("unknown key: status=%d", status)
	}

	// 1 url × 2 engines = 2 units.
	status, body = doJSON(t, env.app, http.MethodPost, "/v1/index/check", checkBody("baidu", "bing"), keyHeaders(key))
	if status != 200 || body["id"] != "stub-1" {
		t.Fatalf("check: status=%d body=%v", status, body)
	}
	meta, _ := body["meta"].(map[string]any)
	if meta == nil || meta["units"] != float64(2) {
		t.Fatalf("check meta units: %v", body)
	}

	// Quota endpoint reflects usage.
	_, body = doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, keyHeaders(key))
	if status != 200 || body["used_today"] != float64(2) || body["daily_quota"] != float64(5) ||
		body["unlimited"] != false || body["used_total"] != float64(2) || body["key_id"] == "" || body["name"] != "ci" {
		t.Fatalf("quota: status=%d body=%v", status, body)
	}

	// Bearer via Authorization header also works; +2 units → 4.
	status, _ = doJSON(t, env.app, http.MethodPost, "/v1/index/check", checkBody("baidu", "bing"),
		map[string]string{"Authorization": "Bearer " + key})
	if status != 200 {
		t.Fatalf("bearer check: status=%d", status)
	}
	_, body = doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, keyHeaders(key))
	if body["used_today"] != float64(4) {
		t.Fatalf("quota after bearer: %v", body)
	}

	// Exceeding quota: 2 more units would make 6 > 5 → 429, envelope fixed.
	status, body = doJSON(t, env.app, http.MethodPost, "/v1/index/check", checkBody("baidu", "bing"), keyHeaders(key))
	if status != 429 || body["error"] != "quota_exceeded" {
		t.Fatalf("quota exceeded: status=%d body=%v", status, body)
	}
	// Rejected request consumed nothing.
	_, body = doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, keyHeaders(key))
	if body["used_today"] != float64(4) {
		t.Fatalf("quota after 429: %v", body)
	}

	// 1 unit still fits exactly (4+1=5).
	status, _ = doJSON(t, env.app, http.MethodPost, "/v1/index/check", checkBody("baidu"), keyHeaders(key))
	if status != 200 {
		t.Fatalf("exact-fit unit: status=%d", status)
	}
}

func TestGuardAliasAndBadRequests(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "alias", 0) // unlimited

	// Alias "360" resolves to so360 (stub echoes resolved engines).
	status, body := doJSON(t, env.app, http.MethodPost, "/v1/index/check",
		map[string]any{"urls": []string{"https://example.com/x"}, "engines": []string{"360"}}, keyHeaders(key))
	if status != 200 {
		t.Fatalf("alias check: status=%d", status)
	}
	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("alias results: %v", body)
	}
	engines, _ := results[0].(map[string]any)["engines"].(map[string]any)
	if _, ok := engines["so360"]; !ok {
		t.Fatalf("alias engine not resolved: %v", engines)
	}

	// Unknown engine name → guard does not block; usage records status 200 but
	// with zero units (stub still answers since it echoes whatever arrived —
	// quota accounting must stay at 0).
	// (The real index handler answers 400 INVALID_PARAM; the guard's contract
	// is only that it does not consume quota for malformed input.)
	before := quotaUsedToday(t, env.app, key)
	doJSON(t, env.app, http.MethodPost, "/v1/index/check",
		map[string]any{"urls": []string{"https://example.com/x"}, "engines": []string{"google"}}, keyHeaders(key))
	if after := quotaUsedToday(t, env.app, key); after != before {
		t.Fatalf("invalid engines consumed quota: before=%v after=%v", before, after)
	}

	// Malformed JSON body: no quota consumed.
	before = quotaUsedToday(t, env.app, key)
	_, _ = doJSONRaw(t, env.app, http.MethodPost, "/v1/index/check", "{bad json", keyHeaders(key))
	if after := quotaUsedToday(t, env.app, key); after != before {
		t.Fatalf("malformed body consumed quota: before=%v after=%v", before, after)
	}

	// GET variant with query params: 1 url × 2 engines = 2 units.
	status, body = doJSON(t, env.app, http.MethodGet, "/v1/index/check?url=https://example.com/g&engines=baidu,bing", nil, keyHeaders(key))
	if status != 200 {
		t.Fatalf("GET check: status=%d", status)
	}
	meta, _ := body["meta"].(map[string]any)
	if meta["units"] != float64(2) {
		t.Fatalf("GET check units: %v", body)
	}
}

func quotaUsedToday(t *testing.T, app *fiber.App, key string) float64 {
	t.Helper()
	status, body := doJSON(t, app, http.MethodGet, "/v1/quota", nil, keyHeaders(key))
	if status != 200 {
		t.Fatalf("quota endpoint: status=%d", status)
	}
	used, _ := body["used_today"].(float64)
	return used
}

// ---------------------------------------------------------------------------
// Usage journal, stats, engines endpoints
// ---------------------------------------------------------------------------

func TestUsageAndStats(t *testing.T) {
	env := newTestEnv(t, 200, true) // bing answers with {"error":{...}}
	token := login(t, env.app)
	id, key := createKey(t, env, token, "ci", 0)

	// Two successful checks (1 url × baidu+bing = 2 units each), one that ends
	// in a 400 (env app has status 200 → use a separate failing env below).
	for i := 0; i < 2; i++ {
		doJSON(t, env.app, http.MethodPost, "/v1/index/check",
			map[string]any{"urls": []string{"https://example.com/a"}, "engines": []string{"baidu", "bing"}}, keyHeaders(key))
	}

	// Failing (400) request via a second env sharing the same storage.
	failEnv := &testEnv{p: env.p, dir: env.dir}
	failEnv.app = fiber.New()
	env.p.RegisterAdmin(failEnv.app, nil, nil)
	env.p.RegisterPublic(failEnv.app, stubIndexCheck(400, false))
	doJSON(t, failEnv.app, http.MethodPost, "/v1/index/check",
		map[string]any{"urls": []string{"https://example.com/b"}}, keyHeaders(key))

	// /api/usage
	status, body := doJSON(t, env.app, http.MethodGet, "/api/usage?limit=50&offset=0", nil, adminHeaders(token))
	if status != 200 || body["total"] != float64(3) {
		t.Fatalf("usage: status=%d body=%v", status, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("usage items: %v", body)
	}
	first, _ := items[0].(map[string]any)
	// Newest first: the 400 request is the most recent. B-01 如实记账: the
	// rejection records the REAL status (400, not the pre-S4 phantom 500) and
	// zero units — no ghost billing for requests that never reached engines.
	if first["status"] != float64(400) || first["units"] != float64(0) || first["key_id"] != id {
		t.Fatalf("usage newest item: %v", first)
	}
	for _, field := range []string{"time", "key_id", "key_name", "endpoint", "engines", "urls", "units", "status", "took_ms"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("usage item missing %s: %v", field, first)
		}
	}
	if first["endpoint"] != "/v1/index/check" {
		t.Fatalf("usage endpoint: %v", first)
	}

	// key_id filter.
	_, body = doJSON(t, env.app, http.MethodGet, "/api/usage?key_id="+id, nil, adminHeaders(token))
	if body["total"] != float64(3) {
		t.Fatalf("usage filter: %v", body)
	}
	_, body = doJSON(t, env.app, http.MethodGet, "/api/usage?key_id=absent", nil, adminHeaders(token))
	if body["total"] != float64(0) {
		t.Fatalf("usage filter absent: %v", body)
	}

	// Pagination: limit=1 offset=1.
	_, body = doJSON(t, env.app, http.MethodGet, "/api/usage?limit=1&offset=1", nil, adminHeaders(token))
	if body["total"] != float64(3) {
		t.Fatalf("usage pagination total: %v", body)
	}
	items, _ = body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("usage pagination items: %v", body)
	}

	// Overview. B-01/P-01 有效调用口径: the two 200 checks charge 1 unit each
	// (baidu OK; the errored bing unit is not charged); the 400 rejection
	// records 0 units and does NOT count into today/total calls or the
	// success rate → 2 reached calls, 2 units.
	status, body = doJSON(t, env.app, http.MethodGet, "/api/stats/overview", nil, adminHeaders(token))
	if status != 200 {
		t.Fatalf("overview: status=%d", status)
	}
	if body["today_calls"] != float64(2) || body["total_calls"] != float64(2) ||
		body["total_units"] != float64(2) || body["today_units"] != float64(2) ||
		body["active_keys"] != float64(1) {
		t.Fatalf("overview totals: %v", body)
	}
	// Additive fields (P-01): all journaled calls today + rejections in 24h.
	if body["today_calls_all"] != float64(3) || body["rejected_today_24h"] != float64(1) {
		t.Fatalf("overview additive fields: %v", body)
	}
	// Both REACHED calls (the two 200s) succeeded → 100.
	if body["success_rate_24h"] != 100.0 {
		t.Fatalf("overview success rate: %v", body)
	}
	byEngine, _ := body["by_engine"].(map[string]any)
	for _, name := range []string{"baidu", "sogou", "so360", "bing"} {
		if _, ok := byEngine[name]; !ok {
			t.Fatalf("overview by_engine missing %s: %v", name, byEngine)
		}
	}
	baidu, _ := byEngine["baidu"].(map[string]any)
	bing, _ := byEngine["bing"].(map[string]any)
	// baidu: 2 units, both ok; bing: 2 units, both errored (stub failBing).
	if baidu["calls"] != float64(2) || baidu["ok"] != float64(2) {
		t.Fatalf("by_engine baidu: %v", baidu)
	}
	if bing["calls"] != float64(2) || bing["ok"] != float64(0) {
		t.Fatalf("by_engine bing: %v", bing)
	}

	// Timeseries: 14 ascending dates ending today; today carries the calls.
	status, seriesBytes := doRaw(t, env.app, http.MethodGet, "/api/stats/timeseries?days=14", nil, adminHeaders(token))
	if status != 200 {
		t.Fatalf("timeseries: status=%d", status)
	}
	var series []map[string]any
	if err := json.Unmarshal(seriesBytes, &series); err != nil || len(series) != 14 {
		t.Fatalf("timeseries len: %d err=%v", len(series), err)
	}
	today := series[len(series)-1]
	// B-01/P-01 口径: timeseries calls count reached calls only (2), the
	// 400 rejection contributes zero units.
	if today["date"] != localDate(time.Now()) || today["calls"] != float64(2) || today["units"] != float64(2) || today["ok"] != float64(2) {
		t.Fatalf("timeseries today: %v", today)
	}
	if series[0]["calls"] != float64(0) {
		t.Fatalf("timeseries first day: %v", series[0])
	}

	// Engines endpoints (admin + public) share the engineStatus provider.
	status, body = doJSON(t, env.app, http.MethodGet, "/api/engines", nil, adminHeaders(token))
	if status != 200 {
		t.Fatalf("admin engines: status=%d", status)
	}
	engines, ok := body["engines"].(map[string]any)
	if !ok || engines["baidu"] == nil {
		t.Fatalf("admin engines body: %v", body)
	}
	status, body = doJSON(t, env.app, http.MethodGet, "/v1/engines", nil, keyHeaders(key))
	if status != 200 || body["engines"] == nil {
		t.Fatalf("public engines with api key: status=%d body=%v", status, body)
	}
}

func TestDailyQuotaOneAllowsFirstCallAndRejectsSecond(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	token := loginToken(t, env.app)
	_, key := createKey(t, env, token, "single-unit", 1)
	body := map[string]any{
		"urls":    []string{"https://example.com/quota-check"},
		"engines": []string{"baidu"},
	}

	status, _ := doJSON(t, env.app, http.MethodPost, "/v1/index/check", body, keyHeaders(key))
	if status != http.StatusOK {
		t.Fatalf("first one-unit call: status=%d, want 200", status)
	}
	status, response := doJSON(t, env.app, http.MethodPost, "/v1/index/check", body, keyHeaders(key))
	if status != http.StatusTooManyRequests || response["error"] != "quota_exceeded" {
		t.Fatalf("second one-unit call: status=%d body=%v, want 429 quota_exceeded", status, response)
	}

	status, quota := doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, keyHeaders(key))
	if status != http.StatusOK || quota["used_today"] != float64(1) || quota["daily_quota"] != float64(1) {
		t.Fatalf("quota after rejected second call: status=%d body=%v", status, quota)
	}
}

func TestDailyQuotaSharesPendingBudgetBetweenRESTAndMCP(t *testing.T) {
	p := mustNewPlatform(t, t.TempDir(), nil)
	env := &testEnv{p: p, app: fiber.New()}
	p.RegisterAdmin(env.app, nil, nil)
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p.RegisterPublic(env.app, func(c *fiber.Ctx) error {
		close(entered)
		<-release
		return stubIndexCheck(http.StatusOK, false)(c)
	})
	env.app.Post("/mcp", p.AuthAny(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"result": "ok"})
	})
	token := loginToken(t, env.app)
	_, key := createKey(t, env, token, "shared-budget", 1)
	statusREST := 0
	go func() {
		defer close(completed)
		statusREST, _ = doJSON(t, env.app, http.MethodGet,
			"/v1/index/check?url=https%3A%2F%2Fexample.test%2Fa&engines=baidu", nil, keyHeaders(key))
	}()
	defer func() {
		close(release)
		<-completed
		if statusREST != http.StatusOK {
			t.Errorf("first REST request returned %d", statusREST)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("REST request did not enter the handler")
	}
	status, body := doJSONRaw(t, env.app, http.MethodPost, "/mcp",
		mcpSearchBody, keyHeaders(key))
	if status != http.StatusTooManyRequests || body["error"] != "quota_exceeded" {
		t.Fatalf("concurrent MCP request returned %d, body %v", status, body)
	}
}

func TestQuotaNotConsumedOnBadRequest(t *testing.T) {
	// Stub always answers 400; quota must stay untouched.
	env := newTestEnv(t, 400, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "ci", 5)

	doJSON(t, env.app, http.MethodPost, "/v1/index/check",
		map[string]any{"urls": []string{"https://example.com/a"}, "engines": []string{"baidu"}}, keyHeaders(key))
	if used := quotaUsedToday(t, env.app, key); used != 0 {
		t.Fatalf("400 consumed quota: %v", used)
	}

	// Usage journal holds the failed request with its HTTP status.
	status, body := doJSON(t, env.app, http.MethodGet, "/api/usage", nil, adminHeaders(token))
	if status != 200 || body["total"] != float64(1) {
		t.Fatalf("usage after 400: %v", body)
	}
	items, _ := body["items"].([]any)
	first, _ := items[0].(map[string]any)
	if first["status"] != float64(400) {
		t.Fatalf("usage status after 400: %v", first)
	}
}

func TestUnlimitedQuota(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := login(t, env.app)
	_, key := createKey(t, env, token, "free", 0)

	for i := 0; i < 25; i++ { // 25 × 1 unit, quota unlimited → never 429.
		status, _ := doJSON(t, env.app, http.MethodPost, "/v1/index/check",
			map[string]any{"urls": []string{fmt.Sprintf("https://example.com/%d", i)}, "engines": []string{"baidu"}}, keyHeaders(key))
		if status != 200 {
			t.Fatalf("unlimited call %d: status=%d", i, status)
		}
	}
	_, body := doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, keyHeaders(key))
	if body["unlimited"] != true || body["daily_quota"] != float64(0) || body["used_today"] != float64(25) {
		t.Fatalf("unlimited quota state: %v", body)
	}
}

func TestPublicAuthOnEnginesAndQuota(t *testing.T) {
	env := newTestEnv(t, 200, false)
	// Without any credential the public engines/quota endpoints are protected.
	status, body := doJSON(t, env.app, http.MethodGet, "/v1/engines", nil, nil)
	if status != 401 || body["error"] != "unauthorized" {
		t.Fatalf("engines without key: status=%d body=%v", status, body)
	}
	status, _ = doJSON(t, env.app, http.MethodGet, "/v1/quota", nil, nil)
	if status != 401 {
		t.Fatalf("quota without key: status=%d", status)
	}
}
