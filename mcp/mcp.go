// Package mcp implements the Agent plane (二期契约 C): an MCP (Model Context
// Protocol) streamable-HTTP server that exposes the platform's search and
// index-verification capabilities as MCP tools, plus the agent-ecosystem
// static assets openapi.yaml and llms.txt.
//
// Transport: POST-only JSON endpoint answering JSON-RPC 2.0 responses as a
// single JSON object (the "responsive JSON" mode of the MCP streamable HTTP
// transport). The legacy-era lifecycle is implemented (initialize →
// notifications/initialized → tools/*), which is what Claude Desktop, Claude
// Code, Cursor and other mainstream clients speak today; the negotiated
// protocolVersion defaults to "2025-06-18" per the product contract.
//
// Mounting: Register is called by the serve command with a fiber router
// rooted at core.BasePath, which yields
//
//	POST  /web-tool/mcp          (JSON-RPC 2.0; auth middleware applied)
//	GET   /web-tool/openapi.yaml (OpenAPI 3.1 spec, go:embed, public)
//	GET   /web-tool/llms.txt     (agent-facing platform brief, go:embed, public)
package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// Version is reported as serverInfo.version by initialize. The serve command
// overwrites it with the CLI version before mounting.
var Version = "0.8.19"

// ServerName is the MCP serverInfo.name (product contract fixes "web2api").
const ServerName = "web2api"

// defaultProtocolVersion is answered when the client requests a version this
// server does not support (contract C: 默认 "2025-06-18").
const defaultProtocolVersion = "2025-06-18"

// supportedProtocolVersions lists the legacy-era protocol versions whose
// initialize/tools semantics this server implements. All four share the same
// streamable-HTTP JSON mode and tools/call shape, so one implementation serves
// every entry; initialize echoes the client's requested version when it is in
// this set, otherwise defaultProtocolVersion.
var supportedProtocolVersions = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
	"2025-11-25",
}

// JSON-RPC 2.0 reserved error codes plus the MCP session error.
const (
	codeParseError      = -32700
	codeInvalidRequest  = -32600
	codeMethodNotFound  = -32601
	codeInvalidParams   = -32602
	codeInternalError   = -32603
	codeSessionNotFound = -32001 // MCP streamable HTTP: unknown/terminated session
)

// sessionHeader is the MCP streamable HTTP session header (legacy era).
const sessionHeader = "Mcp-Session-Id"

// sessionTTL bounds how long an idle MCP session stays resident in memory.
const sessionTTL = 30 * time.Minute

// Locals keys consumed from the auth middleware. The contract names the
// credential locals auth_type/key_id (X-API-Key) and auth_type/username (JWT
// admin); platform.AuthAny writes the same values under its own
// platform_-prefixed keys, so both spellings are accepted defensively.
// "quota" is optional: when the auth layer stores a quota object under that
// key, the get_quota tool returns it verbatim.
const (
	localAuthType = "auth_type"
	localKeyID    = "key_id"
	localUsername = "username"
	localQuota    = "quota"

	// platform.AuthAny's spellings of the same credentials.
	platformAuthType = "platform_auth_type"
	platformKeyID    = "platform_key_id"
	platformUsername = "platform_username"
)

// Deps carries the platform capabilities served over MCP.
//
// 接缝逐字(二期契约 C,字段名与签名不得偏离):
//
//	type Deps struct {
//	    Search        func(ctx context.Context, engine, query string, limit int) ([]map[string]any, error)
//	    CheckIndexed  func(ctx context.Context, urls []string, engines []string) (any, error)
//	    EnginesStatus func() any
//	}
//
// The mcp package itself never charges quota nor pre-checks it — billing and
// quota precheck belong entirely to the AuthAny wrapper (P0-9). A nil dep
// yields a per-call tool error result instead of a panic.
//
// S4-B-10: OriginAllowlist extends the Origin check beyond the request's own
// origin (security.mcp_allowed_origins). Entries may be full origins
// ("https://panel.example.com") or bare hosts ("panel.example.com").
type Deps struct {
	Search          func(ctx context.Context, engine, query string, limit int) ([]map[string]any, error)
	CheckIndexed    func(ctx context.Context, urls []string, engines []string) (any, error)
	EnginesStatus   func() any
	OriginAllowlist []string
}

// rpcRequest is one decoded JSON-RPC 2.0 request. ID is raw so the response
// echoes number/string ids byte-exact; an absent (or null) id marks a
// notification, which never gets a response body (contract: HTTP 204).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcError is the JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// rpcResponse is the JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// server owns the dependency surface and the in-memory MCP session table.
type server struct {
	deps            Deps
	sessions        sessionStore
	originAllowlist []string // normalized extra allowed origins (S4-B-10)
}

// sessionStore tracks MCP sessions issued at initialize. Sessions live in
// memory only (single-process deployment); expired entries are pruned lazily
// on every access, so no background goroutine is required.
type sessionStore struct {
	mu  sync.Mutex
	m   map[string]time.Time
	ttl time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{m: make(map[string]time.Time), ttl: ttl}
}

// newSession mints a fresh session id (256-bit random hex).
func (s *sessionStore) newSession() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is fatal-grade; fall back to a time-derived id
		// rather than crashing the request path.
		id := hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
		s.mu.Lock()
		s.m[id] = time.Now()
		s.mu.Unlock()
		return id
	}
	id := hex.EncodeToString(buf)
	s.mu.Lock()
	s.m[id] = time.Now()
	s.mu.Unlock()
	return id
}

// touch reports whether id is live, refreshing its last-seen timestamp and
// pruning expired sessions.
//
// T-01 根因修复: id 通常来自 fiber c.Get → fasthttp Peek 的零拷贝字符串
// (unsafeString),其底层 kv 缓冲区在 Ctx 池化下会被后续请求原地复用改写;
// Go 1.24+ 的 Swiss map 在命中赋值时也会重写键槽。若把该别名直接写进
// s.m,键内容会随后续请求(典型:伪造 Mcp-Session-Id 的请求把缓冲区前缀
// 覆盖掉)就地变化,有效会话随即间歇性 404 -32001 "session not found",
// 且之后同 sid 又恢复 200。入库前必须深拷贝为自有字符串。
func (s *sessionStore) touch(id string) bool {
	id = strings.Clone(id)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	_, ok := s.m[id]
	if ok {
		s.m[id] = now
	}
	return ok
}

// drop removes a session (MCP DELETE termination) and reports whether it existed.
// id 深拷贝的必要性同 touch(T-01):不把 fasthttp 缓冲区别名留在任何
// 与 map 交互的路径上。
func (s *sessionStore) drop(id string) bool {
	id = strings.Clone(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[id]
	delete(s.m, id)
	return ok
}

// size reports the number of live sessions (diagnostics only).
func (s *sessionStore) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

func (s *sessionStore) pruneLocked(now time.Time) {
	for id, seen := range s.m {
		if now.Sub(seen) > s.ttl {
			delete(s.m, id)
		}
	}
}

// Register mounts the Agent plane on the given router. Paths are registered
// relative to the router — pass a router rooted at core.BasePath to serve
// POST /web-tool/mcp, GET /web-tool/openapi.yaml, GET /web-tool/llms.txt
// (the same convention as platform.RegisterAdmin/RegisterPublic).
//
// auth wraps POST /mcp only: it performs credential validation, quota
// precheck and billing (contract: 全部由 AuthAny 包装层负责). The two static
// asset routes stay public so Dify/Coze/n8n can import the spec by URL
// without credentials. auth may be nil (bare test/loopback deployments).
func Register(app fiber.Router, auth fiber.Handler, deps Deps) error {
	srv := &server{deps: deps, sessions: *newSessionStore(sessionTTL)}
	srv.originAllowlist = normalizeOriginAllowlist(deps.OriginAllowlist)

	post := []fiber.Handler{srv.handleMCP}
	if auth != nil {
		post = append([]fiber.Handler{auth}, post...)
	}
	app.Post("/mcp", post...)
	app.Delete("/mcp", srv.handleTerminate)
	app.Get("/mcp", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusMethodNotAllowed).JSON(fiber.Map{
			"error": "method_not_allowed",
		})
	})

	app.Get("/openapi.yaml", serveAsset(openapiSpec, "application/yaml; charset=utf-8", "OpenAPI 3.1 规范", "Web2API 数据面与 Agent 面契约,Dify / Coze / n8n 可直接导入;管理面 /api/* 不在公开规范中"))
	app.Get("/llms.txt", serveAsset(llmsText, "text/plain; charset=utf-8", "llms.txt · Agent 平台简介", "面向 AI Agent 的平台说明与端点清单"))
	return nil
}

// handleTerminate implements MCP streamable HTTP session termination
// (DELETE with Mcp-Session-Id): known session → 204; otherwise 404.
func (s *server) handleTerminate(c *fiber.Ctx) error {
	id := strings.TrimSpace(c.Get(sessionHeader))
	if id == "" || !s.sessions.drop(id) {
		return c.Status(fiber.StatusNotFound).JSON(rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &rpcError{Code: codeSessionNotFound, Message: "session not found"},
		})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// handleMCP answers POST /mcp: origin validation, session validation, JSON-RPC
// dispatch. Notifications (no id) answer 204 per the product contract;
// everything else answers a single JSON object with HTTP 200 (or 400 for
// parse/invalid-request framing errors).
func (s *server) handleMCP(c *fiber.Ctx) error {
	if !s.validOrigin(c.Get(fiber.HeaderOrigin), c.Hostname()) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "invalid_origin"})
	}

	if id := strings.TrimSpace(c.Get(sessionHeader)); id != "" {
		if !s.sessions.touch(id) {
			// T-01 取证日志(debug 级,仅 --verbose/--debug 运行时可见):记录实际
			// 收到的 sid 全文、长度与库内活跃会话数,用于区分三类成因——伪造/过期
			// sid(正常)、其他进程铸造的 sid(多实例/端口复用)、本进程铸造却被
			// 误判丢失(touch 查表缺陷;根因见 sessionStore.touch 的深拷贝注释,
			// 保留本日志以便线上复核)。本机无 gcc 无法构建 -race,该日志是
			// 生产环境的精确取证通道。
			logrus.WithFields(logrus.Fields{
				"sid":           id,
				"sid_len":       len(id),
				"ip":            c.IP(),
				"live_sessions": s.sessions.size(),
			}).Debug("mcp: Mcp-Session-Id 校验失败(session not found)")
			return c.Status(fiber.StatusNotFound).JSON(rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error: &rpcError{
					Code:    codeSessionNotFound,
					Message: "session not found; send initialize again",
					Data:    map[string]any{"received_sid_len": len(id)},
				},
			})
		}
	}

	body := c.Body()
	if isJSONArray(body) {
		return respondRPCError(c, fiber.StatusBadRequest, nil, codeInvalidRequest,
			"batch requests are not supported; send one JSON-RPC message per POST")
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return respondRPCError(c, fiber.StatusBadRequest, nil, codeParseError,
			"parse error: body is not valid JSON")
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return respondRPCError(c, fiber.StatusBadRequest, req.ID, codeInvalidRequest,
			"jsonrpc must be \"2.0\"")
	}
	if req.Method == "" {
		return respondRPCError(c, fiber.StatusBadRequest, req.ID, codeInvalidRequest,
			"method is required")
	}

	// Notifications carry no id (JSON-RPC 2.0): no response body. The contract
	// pins notifications/initialized to HTTP 204; unknown notifications are
	// swallowed the same way (MCP: servers MUST NOT reply to notifications).
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return c.SendStatus(fiber.StatusNoContent)
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(c, req)
	case "notifications/initialized":
		// Reached only when the notification illegally carries an id; treat as
		// an empty success so strict clients do not hang.
		return respondResult(c, req.ID, map[string]any{})
	case "ping":
		return respondResult(c, req.ID, map[string]any{})
	case "tools/list":
		return respondResult(c, req.ID, map[string]any{"tools": toolSpecs()})
	case "tools/call":
		return s.handleToolsCall(c, req)
	default:
		return respondRPCError(c, fiber.StatusOK, req.ID, codeMethodNotFound,
			"method not found: "+req.Method)
	}
}

// handleInitialize answers the MCP handshake. protocolVersion echoes the
// client's requested version when supported, otherwise the contract default;
// serverInfo is fixed by contract (name "web2api"). A new in-memory session is
// minted and returned in the Mcp-Session-Id response header.
func (s *server) handleInitialize(c *fiber.Ctx, req rpcRequest) error {
	requested := ""
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	requested = strings.TrimSpace(params.ProtocolVersion)

	version := defaultProtocolVersion
	for _, v := range supportedProtocolVersions {
		if v == requested {
			version = v
			break
		}
	}

	sessionID := s.sessions.newSession()
	c.Set(sessionHeader, sessionID)

	return respondResult(c, req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    ServerName,
			"title":   "Web2API 网站收录查询开放平台",
			"version": Version,
		},
		"instructions": "国内四引擎(百度/搜狗/360/必应)搜索与收录判定。鉴权:请求头 X-API-Key(计费)或 Authorization: Bearer <JWT>(管理员,不计费)。完整端点见 /openapi.yaml 与 /llms.txt。",
	})
}

// handleToolsCall validates params and dispatches one tool. Validation
// failures answer JSON-RPC -32602 (contract: 入参校验失败不回 500); dependency
// failures answer a normal tool result with isError:true (MCP tools semantics).
func (s *server) handleToolsCall(c *fiber.Ctx, req rpcRequest) error {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return respondRPCError(c, fiber.StatusOK, req.ID, codeInvalidParams,
				"invalid params: params must be an object with name and arguments")
		}
	}
	if strings.TrimSpace(params.Name) == "" {
		return respondRPCError(c, fiber.StatusOK, req.ID, codeInvalidParams,
			"invalid params: name is required")
	}

	args := map[string]json.RawMessage{}
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return respondRPCError(c, fiber.StatusOK, req.ID, codeInvalidParams,
				"invalid params: arguments must be an object")
		}
	}

	ctx := c.UserContext()
	switch params.Name {
	case toolWebSearch:
		engine, query, limit, err := parseWebSearchArgs(args)
		if err != nil {
			return respondRPCError(c, fiber.StatusOK, req.ID, codeInvalidParams, "invalid params: "+err.Error())
		}
		if s.deps.Search == nil {
			return respondToolError(c, req.ID, "not_available", "web_search is not wired in this deployment (Deps.Search is nil)")
		}
		rows, err := s.deps.Search(ctx, engine, query, limit)
		if err != nil {
			return respondToolError(c, req.ID, "search_failed", err.Error())
		}
		results := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			results = append(results, map[string]any{
				"rank":    row["rank"],
				"title":   row["title"],
				"url":     row["url"],
				"snippet": row["snippet"],
			})
		}
		return respondToolResult(c, req.ID, map[string]any{"results": results})

	case toolCheckIndexed:
		urls, engines, err := parseCheckIndexedArgs(args)
		if err != nil {
			return respondRPCError(c, fiber.StatusOK, req.ID, codeInvalidParams, "invalid params: "+err.Error())
		}
		if s.deps.CheckIndexed == nil {
			return respondToolError(c, req.ID, "not_available", "check_indexed is not wired in this deployment (Deps.CheckIndexed is nil)")
		}
		payload, err := s.deps.CheckIndexed(ctx, urls, engines)
		if err != nil {
			return respondToolError(c, req.ID, "check_failed", err.Error())
		}
		if payload == nil {
			payload = map[string]any{"results": []any{}}
		}
		return respondToolResult(c, req.ID, payload)

	case toolGetQuota:
		return respondToolResult(c, req.ID, quotaView(c))

	case toolListEngines:
		var status any = map[string]any{}
		if s.deps.EnginesStatus != nil {
			if v := s.deps.EnginesStatus(); v != nil {
				status = v
			}
		}
		return respondToolResult(c, req.ID, map[string]any{"engines": status})

	default:
		return respondRPCError(c, fiber.StatusOK, req.ID, codeInvalidParams,
			"invalid params: unknown tool "+params.Name)
	}
}

// quotaView builds the get_quota tool payload from the auth layer's locals.
// Live quota numbers are returned verbatim when the auth layer stores them
// under the "quota" local; otherwise the fallback reports the caller identity
// (accepting both the contract spelling and platform.AuthAny's
// platform_-prefixed keys) and points at GET /v1/quota. Admin callers (JWT,
// 不计费) report unlimited.
func quotaView(c *fiber.Ctx) any {
	if q := c.Locals(localQuota); q != nil {
		return q
	}
	authType := localString(c, localAuthType, platformAuthType)
	keyID := localString(c, localKeyID, platformKeyID)
	username := localString(c, localUsername, platformUsername)
	if username != "" || authType == "jwt" || authType == "jwt_admin" || authType == "admin" {
		return map[string]any{
			"auth_type": authType,
			"username":  username,
			"unlimited": true,
		}
	}
	return map[string]any{
		"auth_type": authType,
		"key_id":    keyID,
		"detail":    "实时配额与用量见 GET /v1/quota(同一 X-API-Key)",
	}
}

// localString returns the first non-empty string local among the keys.
func localString(c *fiber.Ctx, keys ...string) string {
	for _, k := range keys {
		if v, ok := c.Locals(k).(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// normalizeOriginAllowlist lower-cases entries and reduces them to their host
// (port included), accepting both full origins and bare hosts (S4-B-10).
func normalizeOriginAllowlist(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host := raw
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			host = u.Host
		}
		out = append(out, strings.ToLower(host))
	}
	return out
}

// validOrigin implements the MCP Origin check (S4-B10 白名单化): non-browser
// clients (curl, agents, SDKs) send no Origin and pass; an Origin that fails
// to parse as an absolute http(s) URL is rejected with 403 (MCP 规范: 无效
// Origin → 403); otherwise the Origin must match the request's own host
// (same-origin, DNS-rebinding 防线) or an entry of security.mcp_allowed_origins.
func (s *server) validOrigin(raw string, requestHost string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if requestHost != "" && strings.EqualFold(u.Host, requestHost) {
		return true
	}
	host := strings.ToLower(u.Host)
	for _, allowed := range s.originAllowlist {
		if allowed == host {
			return true
		}
	}
	return false
}

// isJSONArray reports whether the body is a JSON array (batching was removed
// from MCP streamable HTTP; a single object per POST is required).
func isJSONArray(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

func respondResult(c *fiber.Ctx, id json.RawMessage, result any) error {
	return c.JSON(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func respondRPCError(c *fiber.Ctx, status int, id json.RawMessage, code int, message string) error {
	return c.Status(status).JSON(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

// toolText is the MCP tool result envelope required by the contract:
// {content:[{type:"text",text:"<JSON字符串>"}], isError:…}. The payload is
// always serialized JSON, so LLM consumers can json.loads it.
func toolText(payload any, isError bool) fiber.Map {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw, _ = json.Marshal(map[string]any{"error": map[string]any{
			"code": "internal", "message": "result serialization failed",
		}})
	}
	return fiber.Map{
		"content": []fiber.Map{{"type": "text", "text": string(raw)}},
		"isError": isError,
	}
}

func respondToolResult(c *fiber.Ctx, id json.RawMessage, payload any) error {
	return c.JSON(rpcResponse{JSONRPC: "2.0", ID: id, Result: toolText(payload, false)})
}

func respondToolError(c *fiber.Ctx, id json.RawMessage, code, message string) error {
	return c.JSON(rpcResponse{JSONRPC: "2.0", ID: id, Result: toolText(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	}, true)})
}
