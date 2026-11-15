package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

var errCooldown = errors.New("engine_cooldown simulated failure")

// fakeAuth mimics the contract's AuthAny wrapper: X-API-Key → api_key caller
// (locals auth_type/key_id), Authorization Bearer → admin caller (locals
// auth_type/username); anything else → 401 platform envelope.
func fakeAuth(c *fiber.Ctx) error {
	if key := strings.TrimSpace(c.Get("X-API-Key")); key == "sk-test-123" {
		c.Locals(localAuthType, "api_key")
		c.Locals(localKeyID, "key_123")
		return c.Next()
	}
	if bearer := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)); strings.HasPrefix(bearer, "Bearer jwt-admin") {
		c.Locals(localAuthType, "jwt_admin")
		c.Locals(localUsername, "admin")
		return c.Next()
	}
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
}

// newTestApp builds a fiber app with Register mounted at /web-tool (mirrors
// the cmd wiring: root := app.Group(core.BasePath); mcp.Register(root, ...)).
func newTestApp(t *testing.T, auth fiber.Handler, deps Deps) *fiber.App {
	t.Helper()
	app := fiber.New()
	if err := Register(app.Group("/web-tool"), auth, deps); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return app
}

// rpc performs one POST /web-tool/mcp round trip and decodes the JSON-RPC
// response.
func rpc(t *testing.T, app *fiber.App, body string, headers map[string]string) (int, *http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, "/web-tool/mcp", strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body = io.NopCloser(strings.NewReader(string(raw)))

	status := resp.StatusCode
	if status == fiber.StatusNoContent {
		return status, resp, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("response is not JSON (HTTP %d): %s", status, string(raw))
	}
	return status, resp, out
}

// rpcMsg builds a JSON-RPC request body. id nil = notification (no id field).
func rpcMsg(id any, method string, params map[string]any) string {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// mustResult asserts a successful JSON-RPC response and decodes the tool text
// payload (contract: tools/call 结果统一 {content:[{type:"text",text:<JSON>}]}).
func mustResult(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	if out["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", out["error"])
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %v", out)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("result.content must hold one entry: %v", result)
	}
	entry, ok := content[0].(map[string]any)
	if !ok || entry["type"] != "text" {
		t.Fatalf("content[0] must be {type:\"text\"}: %v", content)
	}
	text, _ := entry["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool text is not a JSON string: %q", text)
	}
	return payload
}

func mustRPCError(t *testing.T, out map[string]any, wantCode float64) map[string]any {
	t.Helper()
	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON-RPC error, got: %v", out)
	}
	code, _ := errObj["code"].(float64)
	if code != wantCode {
		t.Fatalf("error code = %v, want %v", errObj["code"], wantCode)
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		t.Fatalf("error message must not be empty: %v", errObj)
	}
	return errObj
}

func authorized() map[string]string {
	return map[string]string{"X-API-Key": "sk-test-123"}
}

func withSession(sid string) map[string]string {
	return map[string]string{"X-API-Key": "sk-test-123", sessionHeader: sid}
}

// ---------------------------------------------------------------------------
// Handshake: initialize / notifications / ping
// ---------------------------------------------------------------------------

func TestInitializeHandshake(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})

	status, resp, out := rpc(t, app, rpcMsg(1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]any{"name": "claude-desktop", "version": "1.0"},
	}), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("initialize status = %d", status)
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result missing: %v", out)
	}
	if got := result["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want 2025-06-18", got)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != ServerName {
		t.Fatalf("serverInfo.name = %v, want %s", info["name"], ServerName)
	}
	if v, _ := info["version"].(string); v == "" {
		t.Fatalf("serverInfo.version must not be empty: %v", info)
	}
	if _, ok := result["capabilities"]; !ok {
		t.Fatalf("capabilities missing: %v", result)
	}
	if sid := resp.Header.Get(sessionHeader); sid == "" {
		t.Fatalf("initialize must set %s response header", sessionHeader)
	}
}

func TestInitializeNegotiatesProtocolVersion(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})

	cases := []struct {
		requested string
		want      string
	}{
		{"2024-11-05", "2024-11-05"}, // supported → echo
		{"2025-03-26", "2025-03-26"}, // supported → echo
		{"2025-06-18", "2025-06-18"}, // supported → echo
		{"2025-11-25", "2025-11-25"}, // supported → echo
		{"2026-07-28", "2025-06-18"}, // modern era → fallback to contract default
		{"", "2025-06-18"},           // absent → contract default
		{"nonsense", "2025-06-18"},   // unsupported → contract default
	}
	for _, tc := range cases {
		params := map[string]any{}
		if tc.requested != "" {
			params["protocolVersion"] = tc.requested
		}
		_, _, out := rpc(t, app, rpcMsg(1, "initialize", params), authorized())
		result, ok := out["result"].(map[string]any)
		if !ok {
			t.Fatalf("initialize(%q) failed: %v", tc.requested, out)
		}
		if got := result["protocolVersion"]; got != tc.want {
			t.Fatalf("requested %q → protocolVersion %v, want %q", tc.requested, got, tc.want)
		}
	}
}

func TestNotificationsInitializedReturns204(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})

	status, resp, _ := rpc(t, app, rpcMsg(nil, "notifications/initialized", nil), authorized())
	if status != fiber.StatusNoContent {
		t.Fatalf("notifications/initialized status = %d, want 204", status)
	}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) != 0 {
		t.Fatalf("notification must not answer a body, got %q", string(raw))
	}
}

func TestUnknownNotificationIsSwallowed(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, _ := rpc(t, app, rpcMsg(nil, "notifications/progress", nil), authorized())
	if status != fiber.StatusNoContent {
		t.Fatalf("unknown notification status = %d, want 204 (MCP: never reply to notifications)", status)
	}
}

func TestPing(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(7, "ping", nil), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("ping status = %d", status)
	}
	if out["result"] == nil || out["error"] != nil {
		t.Fatalf("ping must answer an empty result without error: %v", out)
	}
}

// ---------------------------------------------------------------------------
// tools/list
// ---------------------------------------------------------------------------

func TestToolsListFourTools(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(2, "tools/list", nil), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("tools/list status = %d", status)
	}
	result := out["result"].(map[string]any)
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools must be an array: %v", result)
	}
	got := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name, _ := tool["name"].(string)
		got[name] = true
		desc, _ := tool["description"].(string)
		if desc == "" {
			t.Fatalf("tool %s must carry a description (Coze/Dify LLM digest)", name)
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Fatalf("tool %s inputSchema must be an object schema: %v", name, tool["inputSchema"])
		}
	}
	for _, want := range []string{toolWebSearch, toolCheckIndexed, toolGetQuota, toolListEngines} {
		if !got[want] {
			t.Fatalf("tools/list missing %q; got %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("expected exactly 4 tools, got %d: %v", len(got), got)
	}
}

// ---------------------------------------------------------------------------
// tools/call with fake deps
// ---------------------------------------------------------------------------

func TestToolsCallWebSearchDefaultsAndShape(t *testing.T) {
	var gotEngine, gotQuery string
	var gotLimit int
	deps := Deps{
		Search: func(_ context.Context, engine, query string, limit int) ([]map[string]any, error) {
			gotEngine, gotQuery, gotLimit = engine, query, limit
			return []map[string]any{
				{"rank": 1, "title": "示例标题", "url": "https://example.com/a", "snippet": "示例摘要", "extra": "dropped"},
			}, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	_, _, out := rpc(t, app, rpcMsg(3, "tools/call", map[string]any{
		"name":      toolWebSearch,
		"arguments": map[string]any{"query": "golang 教程"},
	}), authorized())

	payload := mustResult(t, out)
	results, ok := payload["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results missing: %v", payload)
	}
	row := results[0].(map[string]any)
	for _, key := range []string{"rank", "title", "url", "snippet"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("result row missing %q (contract shape): %v", key, row)
		}
	}
	if _, leaked := row["extra"]; leaked {
		t.Fatalf("contract shape must be exactly {rank,title,url,snippet}: %v", row)
	}
	if gotEngine != "baidu" {
		t.Fatalf("engine default = %q, want baidu", gotEngine)
	}
	if gotLimit != defaultSearchLimit {
		t.Fatalf("limit default = %d, want %d", gotLimit, defaultSearchLimit)
	}
	if gotQuery != "golang 教程" {
		t.Fatalf("query passthrough failed: %q", gotQuery)
	}
}

func TestToolsCallWebSearchExplicitArgs(t *testing.T) {
	var gotEngine string
	var gotLimit int
	deps := Deps{
		Search: func(_ context.Context, engine, _ string, limit int) ([]map[string]any, error) {
			gotEngine, gotLimit = engine, limit
			return []map[string]any{}, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	_, _, out := rpc(t, app, rpcMsg(4, "tools/call", map[string]any{
		"name":      toolWebSearch,
		"arguments": map[string]any{"query": "测试", "engine": "bing", "limit": 5},
	}), authorized())
	mustResult(t, out)
	if gotEngine != "bing" || gotLimit != 5 {
		t.Fatalf("engine/limit passthrough failed: %q %d", gotEngine, gotLimit)
	}
}

func TestToolsCallCheckIndexed(t *testing.T) {
	var gotURLs []string
	var gotEngines []string
	deps := Deps{
		CheckIndexed: func(_ context.Context, urls []string, engines []string) (any, error) {
			gotURLs, gotEngines = urls, engines
			// Mirrors the /v1/index/check response structure (IndexCheckResponse).
			return map[string]any{
				"id":         "req_1",
				"created_at": "2026-11-15T00:00:00Z",
				"results": []any{
					map[string]any{
						"url": urls[0],
						"engines": map[string]any{
							"baidu": map[string]any{"indexed": true, "match": "exact"},
						},
					},
				},
				"meta": map[string]any{"request_id": "req_1", "took_ms": 12, "units": len(urls) * 4},
			}, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	_, _, out := rpc(t, app, rpcMsg(5, "tools/call", map[string]any{
		"name": toolCheckIndexed,
		"arguments": map[string]any{
			"urls": []string{"https://example.com/a", "https://example.com/b"},
		},
	}), authorized())

	payload := mustResult(t, out)
	if payload["id"] != "req_1" {
		t.Fatalf("check_indexed must return the /v1/index/check structure verbatim: %v", payload)
	}
	results := payload["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results structure mismatch: %v", payload)
	}
	if _, ok := payload["meta"].(map[string]any); !ok {
		t.Fatalf("meta missing: %v", payload)
	}
	if len(gotURLs) != 2 {
		t.Fatalf("urls passthrough failed: %v", gotURLs)
	}
	if gotEngines != nil {
		t.Fatalf("engines should stay nil when omitted (default = all four engines), got %v", gotEngines)
	}
}

func TestToolsCallGetQuotaFallbackAndAdmin(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})

	// API-key caller: no quota local → identity fallback pointing at /v1/quota.
	_, _, out := rpc(t, app, rpcMsg(6, "tools/call", map[string]any{"name": toolGetQuota, "arguments": map[string]any{}}), authorized())
	payload := mustResult(t, out)
	if payload["auth_type"] != "api_key" || payload["key_id"] != "key_123" {
		t.Fatalf("api_key quota fallback mismatch: %v", payload)
	}

	// Admin caller: unlimited per contract (管理员不计费).
	_, _, out = rpc(t, app, rpcMsg(7, "tools/call", map[string]any{"name": toolGetQuota, "arguments": map[string]any{}}),
		map[string]string{fiber.HeaderAuthorization: "Bearer jwt-admin-token"})
	payload = mustResult(t, out)
	if payload["unlimited"] != true {
		t.Fatalf("admin quota must report unlimited: %v", payload)
	}

	// When AuthAny stores a quota object under the "quota" local it is returned verbatim.
	app2 := fiber.New()
	authWithQuota := func(c *fiber.Ctx) error {
		c.Locals(localAuthType, "api_key")
		c.Locals(localKeyID, "key_123")
		c.Locals(localQuota, map[string]any{"key_id": "key_123", "daily_quota": 1000, "used_today": 8})
		return c.Next()
	}
	if err := Register(app2.Group("/web-tool"), authWithQuota, Deps{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, _, out = rpc(t, app2, rpcMsg(8, "tools/call", map[string]any{"name": toolGetQuota, "arguments": map[string]any{}}), nil)
	payload = mustResult(t, out)
	if payload["daily_quota"] != float64(1000) || payload["used_today"] != float64(8) {
		t.Fatalf("quota local passthrough failed: %v", payload)
	}

	// platform.AuthAny writes the same credentials under platform_-prefixed
	// keys; get_quota must honor both spellings.
	app3 := fiber.New()
	authPlatformSpelling := func(c *fiber.Ctx) error {
		c.Locals(platformAuthType, "api_key")
		c.Locals(platformKeyID, "key_platform")
		return c.Next()
	}
	if err := Register(app3.Group("/web-tool"), authPlatformSpelling, Deps{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, _, out = rpc(t, app3, rpcMsg(9, "tools/call", map[string]any{"name": toolGetQuota, "arguments": map[string]any{}}), nil)
	payload = mustResult(t, out)
	if payload["key_id"] != "key_platform" {
		t.Fatalf("platform_-prefixed locals must be honored: %v", payload)
	}
}

func TestToolsCallListEngines(t *testing.T) {
	deps := Deps{
		EnginesStatus: func() any {
			return []map[string]any{
				{"engine": "baidu", "state": "cooldown", "until": "2026-11-15T02:00:00Z", "reason": "COOLDOWN"},
				{"engine": "bing", "state": "ok"},
			}
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	_, _, out := rpc(t, app, rpcMsg(9, "tools/call", map[string]any{"name": toolListEngines, "arguments": map[string]any{}}), authorized())
	payload := mustResult(t, out)
	engines, ok := payload["engines"].([]any)
	if !ok || len(engines) != 2 {
		t.Fatalf("list_engines shape mismatch: %v", payload)
	}
}

func TestToolsCallNilDepIsToolErrorNotPanic(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(10, "tools/call", map[string]any{
		"name": toolWebSearch, "arguments": map[string]any{"query": "x"},
	}), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("status = %d", status)
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("nil dep must produce isError:true, got %v", result)
	}
	content := result["content"].([]any)[0].(map[string]any)
	var payload map[string]any
	if err := json.Unmarshal([]byte(content["text"].(string)), &payload); err != nil {
		t.Fatalf("tool error text must be JSON: %v", err)
	}
	if payload["error"] == nil {
		t.Fatalf("tool error text must carry an error object: %v", payload)
	}
}

func TestToolsCallDepFailureIsErrorResult(t *testing.T) {
	deps := Deps{
		Search: func(_ context.Context, _, _ string, _ int) ([]map[string]any, error) {
			return nil, errCooldown
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	status, _, out := rpc(t, app, rpcMsg(11, "tools/call", map[string]any{
		"name": toolWebSearch, "arguments": map[string]any{"query": "x"},
	}), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("dep failures must stay HTTP 200 tool results, got %d", status)
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("dep failure must set isError:true: %v", result)
	}
}

// ---------------------------------------------------------------------------
// Invalid params → JSON-RPC -32602 (contract: not 500)
// ---------------------------------------------------------------------------

func TestInvalidParamsReturnRPCError32602(t *testing.T) {
	deps := Deps{
		Search: func(context.Context, string, string, int) ([]map[string]any, error) {
			t.Fatal("dep must not be called on invalid params")
			return nil, nil
		},
		CheckIndexed: func(context.Context, []string, []string) (any, error) {
			t.Fatal("dep must not be called on invalid params")
			return nil, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)

	cases := []struct {
		name string
		body string
	}{
		{"web_search missing query", rpcMsg(1, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{}})},
		{"web_search empty query", rpcMsg(2, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{"query": "   "}})},
		{"web_search non-string query", rpcMsg(3, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{"query": 42}})},
		{"web_search unknown engine", rpcMsg(4, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{"query": "x", "engine": "google"}})},
		{"web_search limit zero", rpcMsg(5, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{"query": "x", "limit": 0}})},
		{"web_search limit too big", rpcMsg(6, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{"query": "x", "limit": 101}})},
		{"web_search limit fractional", rpcMsg(7, "tools/call", map[string]any{"name": toolWebSearch, "arguments": map[string]any{"query": "x", "limit": 2.5}})},
		{"check_indexed missing urls", rpcMsg(8, "tools/call", map[string]any{"name": toolCheckIndexed, "arguments": map[string]any{}})},
		{"check_indexed empty urls", rpcMsg(9, "tools/call", map[string]any{"name": toolCheckIndexed, "arguments": map[string]any{"urls": []string{}}})},
		{"check_indexed 21 urls", rpcMsg(10, "tools/call", map[string]any{"name": toolCheckIndexed, "arguments": map[string]any{"urls": twentyOneURLs()}})},
		{"check_indexed empty url entry", rpcMsg(11, "tools/call", map[string]any{"name": toolCheckIndexed, "arguments": map[string]any{"urls": []string{"https://a.com", ""}}})},
		{"check_indexed unknown engine", rpcMsg(12, "tools/call", map[string]any{"name": toolCheckIndexed, "arguments": map[string]any{"urls": []string{"https://a.com"}, "engines": []string{"yahoo"}}})},
		{"unknown tool name", rpcMsg(13, "tools/call", map[string]any{"name": "shell_exec", "arguments": map[string]any{}})},
		{"arguments not object", rpcMsg(14, "tools/call", map[string]any{"name": toolWebSearch, "arguments": []any{"not", "an", "object"}})},
		{"missing tool name", rpcMsg(15, "tools/call", map[string]any{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _, out := rpc(t, app, tc.body, authorized())
			if status != fiber.StatusOK {
				t.Fatalf("invalid params must be HTTP 200 JSON-RPC errors, got %d", status)
			}
			mustRPCError(t, out, -32602)
		})
	}
}

func TestUnknownMethodIs32601(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(22, "resources/read", map[string]any{}), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("status = %d", status)
	}
	mustRPCError(t, out, -32601)
}

func TestMalformedBodies(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})

	// Invalid JSON → -32700 (HTTP 400).
	status, _, out := rpc(t, app, "{not json", authorized())
	if status != fiber.StatusBadRequest {
		t.Fatalf("parse error status = %d, want 400", status)
	}
	mustRPCError(t, out, -32700)

	// Batch array → -32600 (HTTP 400, batching removed from streamable HTTP).
	status, _, out = rpc(t, app, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, authorized())
	if status != fiber.StatusBadRequest {
		t.Fatalf("batch status = %d, want 400", status)
	}
	mustRPCError(t, out, -32600)

	// Missing method → -32600.
	status, _, out = rpc(t, app, `{"jsonrpc":"2.0","id":1}`, authorized())
	mustRPCError(t, out, -32600)
}

// ---------------------------------------------------------------------------
// Auth / origin / session
// ---------------------------------------------------------------------------

func TestUnauthorizedIsRejectedByAuthLayer(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(1, "tools/list", nil), nil)
	if status != fiber.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", status)
	}
	if out["error"] != "unauthorized" {
		t.Fatalf("platform envelope expected: %v", out)
	}
}

func TestInvalidOriginIs403(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(1, "tools/list", nil),
		map[string]string{"X-API-Key": "sk-test-123", fiber.HeaderOrigin: "not-a-url"})
	if status != fiber.StatusForbidden {
		t.Fatalf("invalid origin status = %d, want 403", status)
	}
	if out["error"] != "invalid_origin" {
		t.Fatalf("origin envelope mismatch: %v", out)
	}

	// Valid origins pass.
	for _, origin := range []string{"https://claude.ai", "http://localhost:3000"} {
		status, _, _ = rpc(t, app, rpcMsg(1, "tools/list", nil),
			map[string]string{"X-API-Key": "sk-test-123", fiber.HeaderOrigin: origin})
		if status != fiber.StatusOK {
			t.Fatalf("origin %q status = %d, want 200", origin, status)
		}
	}
	// No Origin (curl / agents) passes.
	status, _, _ = rpc(t, app, rpcMsg(1, "tools/list", nil), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("absent origin status = %d, want 200", status)
	}
}

func TestSessionLifecycle(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{EnginesStatus: func() any { return []any{} }})

	_, resp, _ := rpc(t, app, rpcMsg(1, "initialize", map[string]any{"protocolVersion": "2025-06-18"}), authorized())
	sid := resp.Header.Get(sessionHeader)
	if sid == "" {
		t.Fatalf("session id missing")
	}

	// Known session accepted.
	status, _, _ := rpc(t, app, rpcMsg(2, "tools/list", nil), withSession(sid))
	if status != fiber.StatusOK {
		t.Fatalf("known session status = %d", status)
	}

	// Unknown session → 404 + JSON-RPC error.
	status, _, out := rpc(t, app, rpcMsg(3, "tools/list", nil), withSession("bogus-session"))
	if status != fiber.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", status)
	}
	mustRPCError(t, out, -32001)

	// DELETE terminates the session.
	req := httptest.NewRequest(fiber.MethodDelete, "/web-tool/mcp", nil)
	req.Header.Set(sessionHeader, sid)
	delResp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if delResp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}
	status, _, _ = rpc(t, app, rpcMsg(4, "tools/list", nil), withSession(sid))
	if status != fiber.StatusNotFound {
		t.Fatalf("terminated session status = %d, want 404", status)
	}
}

func TestSessionlessRequestsStayAccepted(t *testing.T) {
	// curl-style flows without Mcp-Session-Id must work end to end.
	app := newTestApp(t, fakeAuth, Deps{})
	status, _, out := rpc(t, app, rpcMsg(1, "initialize", map[string]any{}), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("initialize status = %d", status)
	}
	status, _, out = rpc(t, app, rpcMsg(2, "tools/list", nil), authorized())
	if status != fiber.StatusOK {
		t.Fatalf("tools/list status = %d", status)
	}
	if out["result"] == nil {
		t.Fatalf("tools/list must answer a result")
	}
}

func TestMethodNotAllowedOnGetMCP(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	req := httptest.NewRequest(fiber.MethodGet, "/web-tool/mcp", nil)
	req.Header.Set("X-API-Key", "sk-test-123")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("get /mcp: %v", err)
	}
	if resp.StatusCode != fiber.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status = %d, want 405", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Static assets
// ---------------------------------------------------------------------------

func TestStaticAssets(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})

	req := httptest.NewRequest(fiber.MethodGet, "/web-tool/openapi.yaml", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("openapi.yaml status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get(fiber.HeaderContentType); !strings.HasPrefix(ct, "application/yaml") {
		t.Fatalf("openapi.yaml content-type = %q", ct)
	}
	if !strings.Contains(body, "openapi: 3.1.0") {
		t.Fatalf("embedded spec must be the 3.1 document, got prefix %q", firstLine(body))
	}
	for _, want := range []string{"/api/engines/{name}/cooldown/force", "/api/playground/check", "/api/auth/password", "/mcp", "/v1/index/check"} {
		if !strings.Contains(body, want) {
			t.Fatalf("spec must document %s", want)
		}
	}

	req = httptest.NewRequest(fiber.MethodGet, "/web-tool/llms.txt", nil)
	resp, err = app.Test(req, -1)
	if err != nil {
		t.Fatalf("llms.txt: %v", err)
	}
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("llms.txt status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get(fiber.HeaderContentType); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("llms.txt content-type = %q", ct)
	}
	if !strings.Contains(string(raw), "/web-tool/mcp") || !strings.Contains(string(raw), "web_search") {
		t.Fatalf("llms.txt must reference the MCP endpoint and tools")
	}
}

// TestOpenAPISpecContractInvariants validates the spec structurally: unique
// operationIds (Coze 硬性要求), a summary/description on every operation, and
// the contract's 14 /api/* paths.
func TestOpenAPISpecContractInvariants(t *testing.T) {
	var spec struct {
		OpenAPI string `yaml:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
			Summary     string `yaml:"summary"`
			Description string `yaml:"description"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openapiSpec, &spec); err != nil {
		t.Fatalf("spec must parse as YAML: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf("spec openapi version = %q, want 3.1.x", spec.OpenAPI)
	}

	seen := map[string]string{}
	apiOps := 0
	ops := 0
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if op.OperationID == "" {
				continue // non-operation keys like "parameters"
			}
			ops++
			if strings.HasPrefix(path, "/api/") {
				apiOps++
			}
			if prev, dup := seen[op.OperationID]; dup {
				t.Fatalf("duplicate operationId %q at %s and %s %s", op.OperationID, prev, method, path)
			}
			seen[op.OperationID] = method + " " + path
			if op.Summary == "" || op.Description == "" {
				t.Fatalf("operation %s (%s %s) needs summary and description (Coze 导入要求)", op.OperationID, method, path)
			}
		}
	}
	if ops == 0 {
		t.Fatalf("spec paths parsed empty — YAML structure mismatch")
	}
	if apiOps != 14 {
		t.Fatalf("spec must cover the contract's 14 /api/* endpoint registrations, got %d", apiOps)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func twentyOneURLs() []string {
	urls := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		urls = append(urls, fmt.Sprintf("https://example.com/p%d", i))
	}
	return urls
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
