package cmd

// 契约 A/B integration tests through the real assembly (mountPlatformAndPanel):
// cooldown management endpoints, the cooldown interception on both data paths,
// the playground mount, the optional data-plane auth switch and the raw-mode
// CheckIndexed implementation. All offline (mock engines, no network).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/platform"
	"golang.org/x/time/rate"
)

// countingEngineMock implements core.SearchEngine + core.IndexChecker with hit
// counters so "zero engine contact during cooldown" is observable.
type countingEngineMock struct {
	name string
	// checkFn controls CheckIndexed; nil answers "self hit".
	checkFn func(ctx context.Context, targetURL string) ([]core.SearchResult, error)

	mu         sync.Mutex
	searchHits int
	checkHits  int
}

func (m *countingEngineMock) Name() string                  { return m.name }
func (m *countingEngineMock) IsInitialized() bool           { return true }
func (m *countingEngineMock) GetRateLimiter() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }

func (m *countingEngineMock) Search(_ context.Context, _ core.Query) ([]core.SearchResult, error) {
	m.mu.Lock()
	m.searchHits++
	m.mu.Unlock()
	return []core.SearchResult{{Rank: 1, URL: "https://example.com/serp", Title: "serp"}}, nil
}

func (m *countingEngineMock) CheckIndexed(_ context.Context, targetURL string, _ core.Query) ([]core.SearchResult, error) {
	m.mu.Lock()
	m.checkHits++
	m.mu.Unlock()
	if m.checkFn != nil {
		return m.checkFn(context.Background(), targetURL)
	}
	return []core.SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
}

func (m *countingEngineMock) hits() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.searchHits, m.checkHits
}

// assembleCooldownTest builds the platform assembly over two counting engines
// with the security node configured the way serve() does it.
func assembleCooldownTest(t *testing.T) (*core.Server, *countingEngineMock, *countingEngineMock) {
	t.Helper()

	previous := config
	t.Cleanup(func() { config = previous })

	config.Platform.Enabled = true
	config.Platform.DataDir = t.TempDir()
	config.Platform.AdminUsername = "admin"
	config.Platform.AdminPassword = "admin123"
	config.Platform.JWTExpireHours = 24
	config.Platform.UsageRetentionDays = 30
	config.Server.Host = "127.0.0.1"
	config.Server.Port = 7000
	config.Security = platform.SecurityConfig{
		AdminRatePerMinute: 60,
		LoginLockout:       platform.LoginLockoutConfig{Fails: 5, Minutes: 15},
	}
	config.Stability = core.DefaultStabilityConfig()

	baidu := &countingEngineMock{name: "baidu"}
	sogou := &countingEngineMock{name: "sogou"}
	serv := core.NewServerWithOptions("127.0.0.1", 7000, core.DefaultServerOptions(), baidu, sogou)

	closePlat, err := mountPlatformAndPanel(serv)
	if err != nil {
		t.Fatalf("mount platform: %v", err)
	}
	t.Cleanup(func() {
		if closePlat != nil {
			_ = closePlat()
		}
	})
	return serv, baidu, sogou
}

func TestCooldownManagementThroughAssembly(t *testing.T) {
	serv, baidu, sogou := assembleCooldownTest(t)
	app := serv.FiberApp()
	token := adminLoginToken(t, app)

	// ---- force cooldown (维护窗口) ------------------------------------------
	status, body := platformRequest(t, app, http.MethodPost, core.BasePath+"/api/engines/baidu/cooldown/force",
		`{"minutes":30}`, map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"engine":"baidu"`) {
		t.Fatalf("force: status=%d body=%s", status, body)
	}

	// ---- /api/engines carries the extended fields ---------------------------
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/api/engines", "",
		map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK {
		t.Fatalf("engines: status=%d body=%s", status, body)
	}
	var enginesPayload struct {
		Engines []struct {
			Engine            string  `json:"engine"`
			State             string  `json:"state"`
			Until             *string `json:"until"`
			Reason            string  `json:"reason"`
			ConsecutiveBlocks int     `json:"consecutive_blocks"`
			LastErrorAt       *string `json:"last_error_at"`
		} `json:"engines"`
	}
	if err := json.Unmarshal([]byte(body), &enginesPayload); err != nil {
		t.Fatalf("decode engines: %v\n%s", err, body)
	}
	var baiduEntry *struct {
		Engine            string  `json:"engine"`
		State             string  `json:"state"`
		Until             *string `json:"until"`
		Reason            string  `json:"reason"`
		ConsecutiveBlocks int     `json:"consecutive_blocks"`
		LastErrorAt       *string `json:"last_error_at"`
	}
	for i := range enginesPayload.Engines {
		if enginesPayload.Engines[i].Engine == "baidu" {
			baiduEntry = &enginesPayload.Engines[i]
		}
	}
	if baiduEntry == nil {
		t.Fatalf("baidu entry missing: %s", body)
	}
	if baiduEntry.State != "cooldown" || baiduEntry.Reason != "FORCED" || baiduEntry.Until == nil {
		t.Fatalf("baidu entry wrong: %+v", baiduEntry)
	}

	// ---- dedicated search fails fast, zero engine contact -------------------
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/baidu/search?text=golang", "", nil)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, `"engine_cooldown"`) {
		t.Fatalf("cooled search: status=%d body=%s", status, body)
	}
	searchHits, _ := baidu.hits()
	if searchHits != 0 {
		t.Fatalf("engine touched while cooling: %d hits", searchHits)
	}

	// ---- batch paths --------------------------------------------------------
	// engines=[baidu] alone → whole-request 503.
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=baidu", "",
		map[string]string{"X-API-Key": "unused-without-auth"}) // will 401 — see below
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing key first: status=%d body=%s", status, body)
	}

	// Create a real key to exercise the data paths.
	key, _ := createKey(t, app, token, "cooldown-it", 0)

	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=baidu", "",
		map[string]string{"X-API-Key": key})
	if status != http.StatusServiceUnavailable || !strings.Contains(body, `"engine_cooldown"`) {
		t.Fatalf("all-cooldown batch: status=%d body=%s", status, body)
	}

	// engines=[baidu,sogou] → 200; baidu unit answers engine_cooldown, sogou judges.
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=baidu,sogou", "",
		map[string]string{"X-API-Key": key})
	if status != http.StatusOK {
		t.Fatalf("mixed batch: status=%d body=%s", status, body)
	}
	var mixed struct {
		Meta struct {
			EnginesCooldown []struct {
				Engine string `json:"engine"`
				Until  string `json:"until"`
				Reason string `json:"reason"`
			} `json:"engines_cooldown"`
		} `json:"meta"`
		Results []struct {
			Engines map[string]struct {
				Indexed bool `json:"indexed"`
				Error   *struct {
					Code              string `json:"code"`
					RetryAfterSeconds int    `json:"retry_after_seconds"`
				} `json:"error"`
			} `json:"engines"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &mixed); err != nil {
		t.Fatalf("decode mixed: %v\n%s", err, body)
	}
	if len(mixed.Meta.EnginesCooldown) != 1 || mixed.Meta.EnginesCooldown[0].Engine != "baidu" {
		t.Fatalf("meta.engines_cooldown: %+v", mixed.Meta.EnginesCooldown)
	}
	if mixed.Results[0].Engines["baidu"].Error == nil || mixed.Results[0].Engines["baidu"].Error.Code != "engine_cooldown" {
		t.Fatalf("baidu unit: %+v", mixed.Results[0].Engines["baidu"])
	}
	if !mixed.Results[0].Engines["sogou"].Indexed {
		t.Fatalf("sogou unit: %+v", mixed.Results[0].Engines["sogou"])
	}
	_, checkHits := baidu.hits()
	if checkHits != 0 {
		t.Fatalf("cooling engine touched in mixed batch: %d check hits", checkHits)
	}

	// ---- reset recovers -----------------------------------------------------
	status, body = platformRequest(t, app, http.MethodPost, core.BasePath+"/api/engines/baidu/cooldown/reset",
		"", map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("reset: status=%d body=%s", status, body)
	}
	status, body = platformRequest(t, app, http.MethodGet,
		core.BasePath+"/v1/index/check?url=https://a.example.com/one&engines=baidu", "",
		map[string]string{"X-API-Key": key})
	if status != http.StatusOK || !strings.Contains(body, `"indexed":true`) {
		t.Fatalf("post-reset check: status=%d body=%s", status, body)
	}
	if _, checkHits := baidu.hits(); checkHits != 1 {
		t.Fatalf("post-reset check hits=%d, want 1", checkHits)
	}
	_ = sogou
}

func TestPlaygroundCheckMatchesIndexCheckShape(t *testing.T) {
	serv, _, _ := assembleCooldownTest(t)
	app := serv.FiberApp()
	token := adminLoginToken(t, app)
	key, _ := createKey(t, app, token, "pg", 0)

	// Same request through both mounts must produce the same structure.
	statusPublic, bodyPublic := platformRequest(t, app, http.MethodPost, core.BasePath+"/v1/index/check",
		`{"urls":["https://a.example.com/one"],"engines":["baidu"]}`, map[string]string{"X-API-Key": key})
	statusPanel, bodyPanel := platformRequest(t, app, http.MethodPost, core.BasePath+"/api/playground/check",
		`{"urls":["https://a.example.com/one"],"engines":["baidu"]}`, map[string]string{"Authorization": "Bearer " + token})

	if statusPublic != http.StatusOK || statusPanel != http.StatusOK {
		t.Fatalf("statuses: public=%d playground=%d\n%s\n%s", statusPublic, statusPanel, bodyPublic, bodyPanel)
	}
	var pub, panel struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
		Results   []struct {
			URL     string `json:"url"`
			Engines map[string]struct {
				Indexed bool `json:"indexed"`
			} `json:"engines"`
		} `json:"results"`
		Meta struct {
			RequestID string `json:"request_id"`
			Units     int    `json:"units"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(bodyPublic), &pub); err != nil {
		t.Fatalf("decode public: %v\n%s", err, bodyPublic)
	}
	if err := json.Unmarshal([]byte(bodyPanel), &panel); err != nil {
		t.Fatalf("decode playground: %v\n%s", err, bodyPanel)
	}
	if panel.Meta.Units != pub.Meta.Units || len(panel.Results) != len(pub.Results) {
		t.Fatalf("playground shape diverged: public=%+v playground=%+v", pub, panel)
	}
	if panel.Results[0].Engines["baidu"].Indexed != pub.Results[0].Engines["baidu"].Indexed {
		t.Fatalf("playground verdict diverged: %s vs %s", bodyPublic, bodyPanel)
	}

	// Playground is JWT-guarded: no token → 401.
	status, body := platformRequest(t, app, http.MethodPost, core.BasePath+"/api/playground/check",
		`{"urls":["https://a.example.com/one"]}`, nil)
	if status != http.StatusUnauthorized || !strings.Contains(body, `"unauthorized"`) {
		t.Fatalf("playground without token: status=%d body=%s", status, body)
	}
}

func TestDataPlaneAuthSwitch(t *testing.T) {
	previous := config
	t.Cleanup(func() { config = previous })

	config.Platform.Enabled = true
	config.Platform.DataDir = t.TempDir()
	config.Platform.AdminUsername = "admin"
	config.Platform.AdminPassword = "admin123"
	config.Platform.JWTExpireHours = 24
	config.Server.Host = "127.0.0.1"
	config.Server.Port = 7000
	config.Security = platform.SecurityConfig{
		AdminRatePerMinute: 60,
		DataPlaneAuth:      true, // ON
		LoginLockout:       platform.LoginLockoutConfig{Fails: 5, Minutes: 15},
	}

	corsCfg := core.DefaultCORSConfig()
	serverOpts := buildServerOptions(corsCfg, core.DefaultProxyConfig(), core.BrowserOpts{})
	serv := core.NewServerWithOptions("127.0.0.1", 7000, serverOpts, &countingEngineMock{name: "baidu"})
	closePlat, err := mountPlatformAndPanel(serv)
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer func() {
		if closePlat != nil {
			_ = closePlat()
		}
	}()
	app := serv.FiberApp()
	token := adminLoginToken(t, app)
	key, _ := createKey(t, app, token, "dp", 0)

	// Without a key the data plane answers 401.
	status, body := platformRequest(t, app, http.MethodGet, core.BasePath+"/baidu/search?text=golang", "", nil)
	if status != http.StatusUnauthorized || !strings.Contains(body, `"unauthorized"`) {
		t.Fatalf("data-plane auth ON without key: status=%d body=%s", status, body)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/stats", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("stats without key while ON: status=%d", status)
	}

	// With a key the data plane works.
	status, body = platformRequest(t, app, http.MethodGet, core.BasePath+"/baidu/search?text=golang", "",
		map[string]string{"X-API-Key": key})
	if status != http.StatusOK {
		t.Fatalf("data-plane auth ON with key: status=%d body=%s", status, body)
	}

	// Health/ready stay open for orchestrator probes.
	for _, path := range []string{core.BasePath + "/health", core.BasePath + "/ready"} {
		status, _ = platformRequest(t, app, http.MethodGet, path, "", nil)
		if status != http.StatusOK {
			t.Fatalf("%s must stay public: status=%d", path, status)
		}
	}
}

func TestDataPlaneAuthOffByDefault(t *testing.T) {
	serv, _, _ := assembleCooldownTest(t)
	app := serv.FiberApp()

	// The switch defaults to false: identical to the historical behavior.
	status, _ := platformRequest(t, app, http.MethodGet, core.BasePath+"/baidu/search?text=golang", "", nil)
	if status != http.StatusOK {
		t.Fatalf("data-plane auth OFF: status=%d, want 200 (default unchanged)", status)
	}
	status, _ = platformRequest(t, app, http.MethodGet, core.BasePath+"/stats", "", nil)
	if status != http.StatusOK {
		t.Fatalf("stats with auth OFF: status=%d", status)
	}
}

func TestRawEngineCheckIndexedShapes(t *testing.T) {
	// Shape 1: a raw channel exists — the SERP query must be exactly
	// IndexCheckQuery(targetURL, runtime): URL as text, limit 10, features off.
	var captured core.Query
	engine := &rawEngine{
		name: "baidu",
		searchFn: func(ctx context.Context, q core.Query) ([]core.SearchResult, error) {
			captured = q
			return []core.SearchResult{{Rank: 1, URL: "https://a.example.com/one", Title: "self"}}, nil
		},
	}
	results, err := engine.CheckIndexed(context.Background(), "https://a.example.com/one", core.Query{})
	if err != nil {
		t.Fatalf("CheckIndexed: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://a.example.com/one" {
		t.Fatalf("results: %+v", results)
	}
	if captured.Text != "https://a.example.com/one" || captured.Limit != core.IndexCheckLimit ||
		captured.Features || captured.Filter || captured.Start != 0 {
		t.Fatalf("raw index query wrong: %+v", captured)
	}

	// Shape 2: no raw channel → not_implemented (never a fake verdict).
	engine = &rawEngine{name: "bing"} // resolveEngineSpec("bing") has no rawSearchFn
	if _, err := engine.CheckIndexed(context.Background(), "https://a.example.com/one", core.Query{}); !errors.Is(err, core.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}
