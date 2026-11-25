package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
	"golang.org/x/time/rate"
)

// platformEngineMock implements core.SearchEngine + core.IndexChecker with a
// fixed in-memory SERP: the target URL itself. Every engine "finds" the page,
// so index/check outcomes are deterministic offline.
type platformEngineMock struct {
	name string
}

func (m *platformEngineMock) Name() string                  { return m.name }
func (m *platformEngineMock) IsInitialized() bool           { return true }
func (m *platformEngineMock) GetRateLimiter() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }
func (m *platformEngineMock) Search(_ context.Context, _ core.Query) ([]core.SearchResult, error) {
	return []core.SearchResult{{Rank: 1, URL: "https://example.com/serp", Title: "serp"}}, nil
}

func (m *platformEngineMock) CheckIndexed(_ context.Context, targetURL string, _ core.Query) ([]core.SearchResult, error) {
	return []core.SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
}

// assemblePlatformForTest mirrors serve(): builds a core server over four mock
// engines and wires the platform groups + admin panel exactly like production.
func assemblePlatformForTest(t *testing.T, enabled bool) (*core.Server, func()) {
	t.Helper()

	previous := config
	t.Cleanup(func() { config = previous })

	config.Platform.Enabled = enabled
	config.Platform.DataDir = t.TempDir() // absolute: independent of cwd
	config.Platform.AdminUsername = "admin"
	config.Platform.AdminPassword = "admin123"
	config.Platform.JWTExpireHours = 24
	config.Server.Host = "127.0.0.1"
	config.Server.Port = 7000

	engines := []core.SearchEngine{
		&platformEngineMock{name: "baidu"},
		&platformEngineMock{name: "sogou"},
		&platformEngineMock{name: "so360"},
		&platformEngineMock{name: "bing"},
	}
	serv := core.NewServerWithOptions("127.0.0.1", 7000, core.DefaultServerOptions(), engines...)

	closePlat, err := mountPlatformAndPanel(serv)
	if err != nil {
		t.Fatalf("mount platform: %v", err)
	}
	return serv, func() {
		if closePlat != nil {
			_ = closePlat()
		}
	}
}

func platformRequest(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) (int, string) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer closeTestResource(t, resp.Body)
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, buf.String()
}

func adminLoginToken(t *testing.T, app *fiber.App) string {
	t.Helper()
	status, body := platformRequest(t, app, http.MethodPost, core.BasePath+"/api/auth/login",
		`{"username":"admin","password":"admin123"}`, nil)
	if status != http.StatusOK {
		t.Fatalf("login status=%d body=%s", status, body)
	}
	var payload struct {
		Token     string `json:"token"`
		ExpiresIn int64  `json:"expires_in"`
		User      struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode login: %v\n%s", err, body)
	}
	if payload.Token == "" || payload.ExpiresIn != 86400 || payload.User.Username != "admin" {
		t.Fatalf("login payload=%s", body)
	}
	return payload.Token
}

func createKey(t *testing.T, app *fiber.App, token, name string, quota int64) (fullKey, id string) {
	t.Helper()
	createBody := fmt.Sprintf(`{"name":%q,"daily_quota":%d}`, name, quota)
	status, body := platformRequest(t, app, http.MethodPost, core.BasePath+"/api/keys", createBody,
		map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK {
		t.Fatalf("create key status=%d body=%s", status, body)
	}
	var payload struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Prefix string `json:"prefix"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode key: %v\n%s", err, body)
	}
	if !strings.HasPrefix(payload.Key, "sk-w2a-") || len(payload.Key) != len("sk-w2a-")+32 {
		t.Fatalf("key format unexpected: %q", payload.Key)
	}
	if payload.Status != "active" || payload.ID == "" || payload.Prefix == "" {
		t.Fatalf("key view unexpected: %s", body)
	}
	return payload.Key, payload.ID
}

// TestPlatformAssemblyContract walks every product-contract endpoint through
// the real assembly under the fixed /web-tool prefix: static panel, health
// endpoints, admin JWT API, public API key flow, quota accounting and the
// 400/401/429 error envelopes.
func TestPlatformAssemblyContract(t *testing.T) {
	serv, closePlat := assemblePlatformForTest(t, true)
	defer closePlat()
	app := serv.FiberApp()

	// ---- static mounts: 官网 @ BasePath root, 管理控制台 @ BasePath/console/ ----
	// GET <BasePath>/ serves the marketing site (web/site) — the console CTA
	// keyword proves it is the site, not the admin panel.
	status, body := platformRequest(t, app, http.MethodGet, core.BasePath+"/", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "进入控制台") {
		t.Fatalf("GET %s/ status=%d (want site index.html), body head=%.80s", core.BasePath, status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/assets/app.js", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/assets/app.js status=%d, want 200 (site assets)", core.BasePath, status)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/assets/style.css", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/assets/style.css status=%d, want 200 (site assets)", core.BasePath, status)
	}
	// GET <BasePath>/console/ serves the admin console (web/admin).
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/console/", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "管理控制台") {
		t.Fatalf("GET %s/console/ status=%d (want console index.html), body head=%.80s", core.BasePath, status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/console/assets/app.js", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/console/assets/app.js status=%d, want 200", core.BasePath, status)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/console/assets/style.css", "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s/console/assets/style.css status=%d, want 200", core.BasePath, status)
	}
	for _, rootPath := range []string{"/", "/health", "/ready", "/stats", "/v1/quota", "/api/auth/login"} {
		status, _ = platformRequest(t, app, http.MethodGet, rootPath, "", nil)
		if status != http.StatusNotFound {
			t.Fatalf("root path %s status=%d, want 404 (nothing at the root)", rootPath, status)
		}
	}

	// ---- health/stats live only under the prefix --------------------------
	for _, path := range []string{core.BasePath + "/health", core.BasePath + "/ready", core.BasePath + "/stats"} {
		status, _ = platformRequest(t, app, http.MethodGet, path, "", nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s status=%d, want 200", path, status)
		}
	}

	// ---- admin API: login failures ----------------------------------------
	status, body = platformRequest(t, app, http.MethodPost, core.BasePath+"/api/auth/login",
		`{"username":"admin","password":"wrong"}`, nil)
	if status != http.StatusUnauthorized || !strings.Contains(body, `"unauthorized"`) {
		t.Fatalf("bad login status=%d body=%s, want 401 unauthorized", status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/api/me", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /api/me without token status=%d, want 401", status)
	}

	// ---- admin API: login + me --------------------------------------------
	token := adminLoginToken(t, app)
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/api/me", "",
		map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK {
		t.Fatalf("GET /api/me status=%d body=%s", status, body)
	}
	for _, field := range []string{"username", "keys_count", "today_calls", "total_calls"} {
		if !strings.Contains(body, `"`+field+`"`) {
			t.Fatalf("/api/me missing field %q: %s", field, body)
		}
	}

	// ---- admin API: engines + stats endpoints exist -----------------------
	for _, path := range []string{
		core.BasePath + "/api/engines",
		core.BasePath + "/api/stats/overview",
		core.BasePath + "/api/stats/timeseries?days=14",
		core.BasePath + "/api/usage?limit=50&offset=0",
	} {
		status, body = platformRequest(t, app, http.MethodGet, path, "",
			map[string]string{"Authorization": "Bearer " + token})
		if status != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, status, body)
		}
	}

	// ---- public API without key → 401 -------------------------------------
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one", "", nil)
	if status != http.StatusUnauthorized || !strings.Contains(body, `"unauthorized"`) {
		t.Fatalf("no-key check status=%d body=%s, want 401 unauthorized", status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/v1/quota", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("GET /v1/quota without key status=%d, want 401", status)
	}

	// ---- create keys (unlimited + quota=1) ---------------------------------
	unlimitedKey, unlimitedID := createKey(t, app, token, "unlimited", 0)
	quotaKey, quotaKeyID := createKey(t, app, token, "quota-1", 1)

	// GET /v1/index/check with the key: hits via the mock engine pipeline.
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=baidu,bing",
		"", map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusOK {
		t.Fatalf("GET index/check status=%d body=%s", status, body)
	}
	var check struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
		Results   []struct {
			URL     string `json:"url"`
			Engines map[string]struct {
				Indexed      bool   `json:"indexed"`
				Match        string `json:"match"`
				MatchedURL   string `json:"matched_result_url"`
				TotalResults int    `json:"total_results"`
				TookMS       int    `json:"took_ms"`
				Error        *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"engines"`
		} `json:"results"`
		Meta struct {
			RequestID string `json:"request_id"`
			TookMs    int64  `json:"took_ms"`
			Units     int    `json:"units"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(body), &check); err != nil {
		t.Fatalf("decode index/check: %v\n%s", err, body)
	}
	if len(check.Results) != 1 || check.Results[0].URL != "https://a.example.com/one" {
		t.Fatalf("results wrong: %s", body)
	}
	if check.Meta.Units != 2 || check.Meta.RequestID == "" || check.ID != check.Meta.RequestID {
		t.Fatalf("meta wrong: %+v", check.Meta)
	}
	if _, ok := check.Results[0].Engines["baidu"]; !ok {
		t.Fatalf("baidu outcome missing: %s", body)
	}
	for _, name := range []string{"baidu", "bing"} {
		out := check.Results[0].Engines[name]
		if out.Error != nil || !out.Indexed || out.Match != "exact" || out.MatchedURL == "" {
			t.Fatalf("%s outcome wrong: %s", name, body)
		}
	}

	// Engine alias 360 → so360.
	status, body = platformRequest(t, app, http.MethodPost, core.BasePath+"/v1/index/check",
		`{"urls":["https://b.example.com/two"],"engines":["360"]}`,
		map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusOK {
		t.Fatalf("alias check status=%d body=%s", status, body)
	}
	check = struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
		Results   []struct {
			URL     string `json:"url"`
			Engines map[string]struct {
				Indexed      bool   `json:"indexed"`
				Match        string `json:"match"`
				MatchedURL   string `json:"matched_result_url"`
				TotalResults int    `json:"total_results"`
				TookMS       int    `json:"took_ms"`
				Error        *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"engines"`
		} `json:"results"`
		Meta struct {
			RequestID string `json:"request_id"`
			TookMs    int64  `json:"took_ms"`
			Units     int    `json:"units"`
		} `json:"meta"`
	}{}
	if err := json.Unmarshal([]byte(body), &check); err != nil {
		t.Fatalf("decode alias check: %v\n%s", err, body)
	}
	if check.Meta.Units != 1 {
		t.Fatalf("alias units=%d, want 1 (so360)", check.Meta.Units)
	}
	if _, ok := check.Results[0].Engines["so360"]; !ok {
		t.Fatalf("alias 360 did not resolve to so360: %s", body)
	}

	// ---- 400 envelopes from the index handler ------------------------------
	status, body = platformRequest(t, app, http.MethodPost, core.BasePath+"/v1/index/check",
		`{"urls":[]}`, map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusBadRequest || !strings.Contains(body, `"bad_request"`) ||
		!strings.Contains(body, `"EMPTY_QUERY"`) {
		t.Fatalf("empty urls status=%d body=%s, want 400 bad_request EMPTY_QUERY", status, body)
	}
	tooMany := `{"urls":[` + strings.TrimSuffix(strings.Repeat(`"https://x.example.com/",`, 21), ",") + `]}`
	status, body = platformRequest(t, app, http.MethodPost, core.BasePath+"/v1/index/check",
		tooMany, map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusBadRequest || !strings.Contains(body, `"TOO_MANY_URLS"`) {
		t.Fatalf("21 urls status=%d body=%s, want 400 TOO_MANY_URLS", status, body)
	}
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=nosuch", "",
		map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusBadRequest || !strings.Contains(body, `"INVALID_PARAM"`) {
		t.Fatalf("unknown engine status=%d body=%s, want 400 INVALID_PARAM", status, body)
	}

	// ---- quota + usage journal --------------------------------------------
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://c.example.com/three&engines=baidu", "",
		map[string]string{"X-API-Key": quotaKey})
	if status != http.StatusOK {
		t.Fatalf("quota key first check status=%d body=%s", status, body)
	}
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://c.example.com/three&engines=baidu", "",
		map[string]string{"X-API-Key": quotaKey})
	if status != http.StatusTooManyRequests || !strings.Contains(body, `"quota_exceeded"`) {
		t.Fatalf("quota key second check status=%d body=%s, want 429 quota_exceeded", status, body)
	}
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/v1/quota", "",
		map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusOK {
		t.Fatalf("GET /v1/quota status=%d body=%s", status, body)
	}
	for _, field := range []string{"key_id", "name", "daily_quota", "used_today", "unlimited", "used_total"} {
		if !strings.Contains(body, `"`+field+`"`) {
			t.Fatalf("/v1/quota missing field %q: %s", field, body)
		}
	}
	if !strings.Contains(body, `"unlimited":true`) {
		t.Fatalf("unlimited key reported non-unlimited: %s", body)
	}

	// Usage journal saw the /v1/index/check calls.
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/api/usage?limit=50&offset=0", "",
		map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK || !strings.Contains(body, `"total"`) {
		t.Fatalf("GET /api/usage status=%d body=%s", status, body)
	}
	if !strings.Contains(body, "v1/index/check") {
		t.Fatalf("usage journal missing index/check endpoint: %s", body)
	}

	// Overview stats must expose the contract fields.
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/api/stats/overview", "",
		map[string]string{"Authorization": "Bearer " + token})
	for _, field := range []string{"today_calls", "today_units", "total_calls", "total_units", "active_keys", "success_rate_24h", "by_engine"} {
		if !strings.Contains(body, `"`+field+`"`) {
			t.Fatalf("/api/stats/overview missing field %q: %s", field, body)
		}
	}
	if status != http.StatusOK {
		t.Fatalf("GET /api/stats/overview status=%d body=%s", status, body)
	}

	// ---- engine status snapshot feeds /v1/engines + /api/engines -----------
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/v1/engines", "",
		map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusOK {
		t.Fatalf("GET /v1/engines status=%d body=%s", status, body)
	}
	for _, name := range []string{"baidu", "sogou", "so360", "bing"} {
		if !strings.Contains(body, `"`+name+`"`) {
			t.Fatalf("/v1/engines missing engine %q: %s", name, body)
		}
	}
	if !strings.Contains(body, `"breaker"`) || !strings.Contains(body, `"state"`) {
		t.Fatalf("/v1/engines missing breaker state: %s", body)
	}

	// ---- key lifecycle: list → disable (401) → delete -----------------------
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/api/keys", "",
		map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK || !strings.Contains(body, `"quota-1"`) {
		t.Fatalf("GET /api/keys status=%d body=%s", status, body)
	}
	var keys []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		DailyQuota int64  `json:"daily_quota"`
	}
	if err := json.Unmarshal([]byte(body), &keys); err != nil || len(keys) < 2 {
		t.Fatalf("decode keys: %v\n%s", err, body)
	}
	sawUnlimited, sawQuota := false, false
	for _, k := range keys {
		switch k.Name {
		case "unlimited":
			sawUnlimited = true
			if k.ID != unlimitedID || k.Status != "active" {
				t.Fatalf("unlimited key row wrong: %+v", k)
			}
		case "quota-1":
			sawQuota = true
			if k.ID != quotaKeyID || k.DailyQuota != 1 {
				t.Fatalf("quota-1 key row wrong: %+v", k)
			}
		}
	}
	if !sawUnlimited || !sawQuota {
		t.Fatalf("key list incomplete: %s", body)
	}

	// Disable the unlimited key → every request 401s (禁用 key 一律 401).
	status, body = platformRequest(t, app, http.MethodPatch, core.BasePath+"/api/keys/"+unlimitedID,
		`{"status":"disabled"}`, map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK || !strings.Contains(body, `"disabled"`) {
		t.Fatalf("PATCH disable status=%d body=%s", status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=baidu", "",
		map[string]string{"X-API-Key": unlimitedKey})
	if status != http.StatusUnauthorized {
		t.Fatalf("disabled key status=%d, want 401", status)
	}

	status, body = platformRequest(t, app, http.MethodDelete, core.BasePath+"/api/keys/"+quotaKeyID, "",
		map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("DELETE key status=%d body=%s", status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/v1/quota", "",
		map[string]string{"X-API-Key": quotaKey})
	if status != http.StatusUnauthorized {
		t.Fatalf("deleted key status=%d, want 401", status)
	}
}

// TestPlatformDisabledKeepsPanelAndHealth: platform.enabled=false unmounts
// /api/* and /v1/* but the static panels (site + console) and health endpoints
// stay reachable — only /api/* and /v1/* are gated by the switch.
func TestPlatformDisabledKeepsPanelAndHealth(t *testing.T) {
	serv, closeFn := assemblePlatformForTest(t, false)
	defer closeFn()
	app := serv.FiberApp()

	for _, path := range []string{core.BasePath + "/api/auth/login", core.BasePath + "/v1/quota"} {
		status, _ := platformRequest(t, app, http.MethodGet, path, "", nil)
		if status != http.StatusNotFound {
			t.Fatalf("disabled platform %s status=%d, want 404", path, status)
		}
	}
	status, _ := platformRequest(t, app, http.MethodPost, core.BasePath+"/api/auth/login",
		`{"username":"admin","password":"admin123"}`, nil)
	if status != http.StatusNotFound {
		t.Fatalf("disabled platform POST login status=%d, want 404", status)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/", "", nil)
	if status != http.StatusOK {
		t.Fatalf("site with disabled platform status=%d, want 200", status)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/console/", "", nil)
	if status != http.StatusOK {
		t.Fatalf("console with disabled platform status=%d, want 200", status)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/health", "", nil)
	if status != http.StatusOK {
		t.Fatalf("health with disabled platform status=%d, want 200", status)
	}
}

// TestPanelAddressLogUnwrapsWildcardHost locks the startup-log address shape.
func TestPanelAddressLogUnwrapsWildcardHost(t *testing.T) {
	previous := config
	t.Cleanup(func() { config = previous })
	cases := map[string]string{
		"0.0.0.0":  "127.0.0.1",
		"":         "127.0.0.1",
		"::":       "127.0.0.1",
		"10.0.0.5": "10.0.0.5",
	}
	for host, wantHost := range cases {
		config.Server.Host = host
		config.Server.Port = 7123
		want := fmt.Sprintf("http://%s:7123%s/", wantHost, core.BasePath)
		if got := panelURL(); got != want {
			t.Fatalf("panelURL() with host=%q = %q, want %q", host, got, want)
		}
		wantConsole := fmt.Sprintf("http://%s:7123%s/console/", wantHost, core.BasePath)
		if got := consoleURL(); got != wantConsole {
			t.Fatalf("consoleURL() with host=%q = %q, want %q", host, got, wantConsole)
		}
	}
}

// TestPooledBrowserEngineForwardsCheckIndexed: the serve-mode engine wrapper
// must forward core.IndexChecker to the inner engine, otherwise /v1/index/check
// answers not_implemented for every browser-mode engine (regression guard for
// the assembly bug found in live smoke testing).
func TestPooledBrowserEngineForwardsCheckIndexed(t *testing.T) {
	// This is a capability-forwarding test; it has no UI assertions and must
	// also run on Linux CI runners without X11/$DISPLAY.
	pool := newBrowserPool(core.BrowserOpts{IsHeadless: true}, "", nil, 1, 0)
	wrapper := &pooledBrowserEngine{
		name: "baidu",
		factory: func(b core.Browser, opts core.SearchEngineOptions) core.SearchEngine {
			return &platformEngineMock{name: "baidu"}
		},
		pool: pool,
	}
	opts := core.SearchEngineOptions{}
	opts.Init()
	wrapper.opts = opts

	results, err := wrapper.CheckIndexed(context.Background(), "https://a.example.com/one", core.Query{})
	if err != nil {
		t.Fatalf("CheckIndexed forwarded call failed: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://a.example.com/one" {
		t.Fatalf("unexpected results: %+v", results)
	}

	// An inner engine without the capability surfaces not_implemented.
	wrapper.factory = func(b core.Browser, opts core.SearchEngineOptions) core.SearchEngine {
		return &bareEngineMock{name: "baidu"}
	}
	if _, err := wrapper.CheckIndexed(context.Background(), "https://a.example.com/one", core.Query{}); !errors.Is(err, core.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented for inner engine without CheckIndexed, got %v", err)
	}
}

// bareEngineMock implements core.SearchEngine only.
type bareEngineMock struct {
	name string
}

func (m *bareEngineMock) Name() string                  { return m.name }
func (m *bareEngineMock) IsInitialized() bool           { return true }
func (m *bareEngineMock) GetRateLimiter() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }
func (m *bareEngineMock) Search(_ context.Context, _ core.Query) ([]core.SearchResult, error) {
	return nil, nil
}
