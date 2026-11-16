# Web2API API 参考

本文档是 **Web2API 网站收录查询开放平台** 的全量接口文档。整个应用固定部署在 **`/web-tool`** 子路径下（代码内常量 `core.BasePath`，根路径无任何路由），本文所有路径均已带前缀，可逐字拷贝使用。

配置、部署与架构分别见 [CONFIGURATION.md](CONFIGURATION.md)、[deploy/README.md](../deploy/README.md)、[ARCHITECTURE.md](ARCHITECTURE.md)；面向 Agent 开发者的 5 分钟接入指南见 [AGENT-README.md](AGENT-README.md)。

---

## 目录

- [0. 通用约定](#0-通用约定)
- [1. 三套响应信封（务必区分）](#1-三套响应信封务必区分)
- [2. 数据平面 `/v1/*`（公网 API，X-API-Key）](#2-数据平面-v1-公网-apix-api-key)
- [3. 搜索端点 `/{engine}/search`](#3-搜索端点-enginesearch)
- [4. 健康与观测端点](#4-健康与观测端点)
- [5. Agent 平面（MCP / openapi.yaml / llms.txt）](#5-agent-平面)
- [6. 控制平面 `/api/*`（管理 API，JWT）](#6-控制平面-api管理-apijwt)
- [7. 错误码总表](#7-错误码总表)
- [8. 限流与 Retry-After](#8-限流与-retry-after)
- [9. 计费口径](#9-计费口径)

---

## 0. 通用约定

| 约定 | 值 |
| --- | --- |
| 前缀 | 所有端点固定挂在 `/web-tool` 之下；`GET /` 与 `GET /health`（无前缀）一律 404 |
| 请求体 | `Content-Type: application/json`（除静态资源与 MCP 通知外） |
| 时间戳 | RFC3339 UTC，如 `2026-11-15T08:00:00Z` |
| 请求 ID | 每请求生成（可由 `X-Request-ID` 请求头带入），回显在响应头与多数信封的 `request_id` 字段 |
| 鉴权 | 公网 API：`X-API-Key`（兼容 `Authorization: Bearer <key>`）；管理 API：`Authorization: Bearer <JWT>`；MCP：两者皆可（见 §5） |
| 引擎名 | `baidu` / `sogou` / `so360` / `bing`；`360` 是 `so360` 的别名，响应中一律归一化为 `so360` |
| 交叉引用 | 本仓库同时发布 `GET /web-tool/openapi.yaml`（OpenAPI 3.1 机器可读规范）与 `GET /web-tool/llms.txt`（Agent 友好端点清单），内容与本文一致 |

配置键优先级：**CLI 参数 > `WEB2API_*` 环境变量 > `config.yaml` > 内置默认值**（Viper），逐键说明见 [CONFIGURATION.md](CONFIGURATION.md)。

---

## 1. 三套响应信封（务必区分）

平台历史上存在三个错误信封来源，**形状不同、字段不同、适用端点不同**。写客户端解析时必须按端点对号入座：

### 1.1 核心信封（core，`/{engine}/search` 等核心管道端点）

来源：`core/middleware.go` 的 `JSONErrorResponse`。形状：

```json
{
  "error":   "<错误码字符串>",
  "code":    403,
  "request_id": "01936d3a-…",
  "message": "人类可读描述（5xx 内部错误会脱敏为 internal server error）",
  "reason":  "参数类错误才有的原因码（如 EMPTY_QUERY）",
  "meta":    {"engine": "baidu", "…": "…"}
}
```

注意此信封的 **`code` 是 HTTP 状态码（整数）**，`error` 是机器可读错误码；`request_id`、`message`、`reason`、`meta` 按需出现。引擎类错误还会带 `meta.engine`、`meta.proxy_used` 等观测字段。

### 1.2 平台信封（platform，`/api/*` 管理端与 `/v1/*` 鉴权/配额错误）

来源：`platform/errors.go`。形状固定、字段精简：

```json
{"error": "unauthorized"}
```

| HTTP | `error` 取值 | 附加字段 |
| --- | --- | --- |
| 400 | `bad_request` | `code` 与 `reason` 同值，为 `EMPTY_QUERY` / `TOO_MANY_URLS` / `INVALID_PARAM` 之一 |
| 401 | `unauthorized` | 无 |
| 404 | `not_found` | 无 |
| 429 | `quota_exceeded` | 无（响应带 `Retry-After` 头） |
| 429 | `too_many_attempts` | `retry_after_seconds`（登录防爆破锁定；响应亦带 `Retry-After` 头） |
| 429 | `rate_limited` | 管理 API 全局限速超限（响应带 `Retry-After` 头） |
| 500 | `server_error` | `message` |

### 1.3 冷却信封（stability，契约 503）

引擎处于冷却期时，该引擎的所有 search/check 快速失败，**不发真实请求**：

```json
{"error": "engine_cooldown", "reason": "COOLDOWN", "retry_after_seconds": 600}
```

HTTP 503，响应带 `Retry-After` 头（与 `retry_after_seconds` 同值）。实际响应体还会携带核心信封惯例的 `request_id` 与 `meta`（含 `engine`、`until`、以及完整 `engines_cooldown` 快照，见 §2.1）。信封顶层 `reason` 恒为 `COOLDOWN`；自然冷却与运维强制冷却（cooldown/force）的区分体现在 `meta.engines_cooldown[].reason`（`COOLDOWN` / `FORCED`）与管理端点状态里。`retry_after_seconds` 为距冷却解除的秒数。

> **解析建议**：`/v1/*`、`/api/*` 只按 §1.2/§1.3 解析（`error` 为字符串，无整型 `code`）；`/{engine}/search` 按 §1.1 解析（有整型 `code`）。两者不会混用。

---

## 2. 数据平面 `/v1/*`（公网 API，X-API-Key）

鉴权：请求头 `X-API-Key: sk-w2a-…`（兼容 `Authorization: Bearer sk-w2a-…`）。key 缺失、未知或被禁用一律 `401 {"error":"unauthorized"}`。配额预检：`当日已用 + 本次 units > daily_quota`（`daily_quota=0` 为不限）时拒绝，`429 {"error":"quota_exceeded"}`，**预检拒绝不写用量记录、不扣 unit**。

计费单位：**1 URL × 1 引擎 = 1 unit**。

### 2.1 POST /web-tool/v1/index/check

批量收录判定：每个 (URL × 引擎) 单元以目标 URL 为查询词在引擎内检索前 10 条结果并做归一化匹配。

**请求体**

```json
{
  "urls": ["https://example.com/post-1", "https://example.com/post-2"],
  "engines": ["baidu", "sogou", "so360", "bing"]
}
```

- `urls`：1–20 条（超出 → 400 `TOO_MANY_URLS`，为空 → 400 `EMPTY_QUERY`）。
- `engines`：可选，缺省 = 全部可用引擎；接受别名 `360`；含未知引擎 → 400 `INVALID_PARAM`。

**200 响应**（引擎级失败不拖垮整体——该单元给 `error` 字段，HTTP 仍 200）

```json
{
  "id": "01936d3a-8f2e-7d4a-9c1b-2e6f8a0b3c5d",
  "created_at": "2026-11-15T08:00:00Z",
  "results": [
    {
      "url": "https://example.com/post-1",
      "engines": {
        "baidu": {
          "indexed": true,
          "match": "exact",
          "matched_result_url": "https://example.com/post-1",
          "total_results": 9,
          "took_ms": 2393
        },
        "sogou": {
          "error": { "code": "blocked", "message": "search engine blocked the request" }
        }
      }
    }
  ],
  "meta": {
    "request_id": "01936d3a-8f2e-7d4a-9c1b-2e6f8a0b3c5d",
    "took_ms": 2501,
    "units": 8,
    "engines_cooldown": [
      { "engine": "bing", "until": "2026-11-15T08:10:00Z", "reason": "COOLDOWN" }
    ]
  }
}
```

字段说明：

- 成功单元：`indexed`（是否收录）、`match`（仅 `exact` / `none` 两种取值）、`matched_result_url`（命中的候选地址，`match=none` 时字段存在但为空串 `""`，解析方按空串判断即可）、`total_results`（SERP 内 organic 条数）、`took_ms`（该单元耗时）。
- 失败单元：`error.code` / `error.message`，错误码与搜索端点同表（见 §7.2），例如 `blocked`、`captcha_detected`、`circuit_open`、`not_implemented`。
- `meta.units = len(urls) × len(engines)`，即本次计费 unit 数。
- `meta.engines_cooldown`：请求发出时已处于冷却期的引擎列表，元素为 `{"engine","until","reason"}`；`reason` 为 `COOLDOWN`（自然冷却）或 `FORCED`（运维强制冷却）。冷却中的引擎不会对真实引擎发请求——若**部分**引擎冷却，其余单元照常执行（200 信封内冷却单元的 `error.code` 为 `engine_cooldown`，附 `retry_after_seconds`）；若**所选引擎全部**冷却，整个请求 503（§1.3）。

**错误响应**

| HTTP | 信封 | 场景 |
| --- | --- | --- |
| 400 | `{"error":"bad_request",…,"reason":"EMPTY_QUERY"}` | `urls` 为空 |
| 400 | `{"error":"bad_request",…,"reason":"TOO_MANY_URLS"}` | `urls` 超过 20 条 |
| 400 | `{"error":"bad_request",…,"reason":"INVALID_PARAM"}` | 含未知引擎等 |
| 401 | `{"error":"unauthorized"}` | key 缺失 / 未知 / 禁用 |
| 429 | `{"error":"quota_exceeded"}`（`Retry-After` 头） | 当日配额用尽 |
| 503 | `{"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":600}`（`Retry-After` 头） | 所选引擎全部处于冷却期 |

> **本端点 400 的信封现状（实测）**：契约目标形状是上面的三字段平台信封（`platform/errors.go` 注释与 §1.2），管理端 `/api/*` 的 400 已是该形状；但 `/web-tool/v1/index/check` 的参数校验当前由核心 index 处理器应答，实测返回的是 §1.1 核心信封：
>
> ```json
> {"error":"bad_request","code":400,"request_id":"01a1db49-…","message":"TOO_MANY_URLS: urls accepts at most 20 entries, got 21","reason":"TOO_MANY_URLS"}
> ```
>
> 两种形状的 `error` 恒为 `bad_request`、`reason` 同值——**客户端按 `reason` 字段分支即可两代兼容**（仓库自带 e2e 也是按 `reason` 断言的）。信封统一列入集成收尾清单。

```bash
# curl 示例
curl -X POST http://127.0.0.1:7000/web-tool/v1/index/check \
  -H "X-API-Key: sk-w2a-3f9c…" -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}'
```

```js
// fetch 示例
const res = await fetch('http://127.0.0.1:7000/web-tool/v1/index/check', {
  method: 'POST',
  headers: { 'X-API-Key': 'sk-w2a-3f9c…', 'Content-Type': 'application/json' },
  body: JSON.stringify({ urls: ['https://example.com/post-1'], engines: ['baidu', 'bing'] }),
});
const data = await res.json();
```

### 2.2 GET /web-tool/v1/index/check

单 URL 便捷版，查询参数传值，响应结构同上：

```bash
curl "http://127.0.0.1:7000/web-tool/v1/index/check?url=https%3A%2F%2Fexample.com%2F&engines=360" \
  -H "X-API-Key: sk-w2a-3f9c…"
```

- `url`：必填（URL 编码）；`engines`：可选，逗号分隔，别名 `360` 可用。
- 该请求 units 恒为 `1 × len(engines)`（缺省引擎时为 4）。

### 2.3 GET /web-tool/v1/quota

查询当前 key 的配额：

```bash
curl http://127.0.0.1:7000/web-tool/v1/quota -H "X-API-Key: sk-w2a-3f9c…"
```

```json
{
  "key_id": "key_01jdp2…",
  "name": "ci-robot",
  "daily_quota": 0,
  "used_today": 3,
  "unlimited": true,
  "used_total": 42
}
```

`daily_quota=0` 表示不限量（`unlimited=true`）。

### 2.4 GET /web-tool/v1/engines

引擎清单与熔断状态（key 鉴权即可查看）：

```json
{
  "engines": [
    {
      "engine": "baidu",
      "endpoint": "/web-tool/baidu/search",
      "enabled": true,
      "breaker": { "engine": "baidu", "state": "closed", "failure_count": 0 }
    }
  ]
}
```

管理员视角（含冷却状态机字段 `state/until/reason/consecutive_blocks/last_error_at`）见 §6.7 的 `GET /web-tool/api/engines`。

---

## 3. 搜索端点 `/{engine}/search`

四引擎原始搜索（收录判定的同一套管道）：

```
GET /web-tool/baidu/search?text=…&limit=…
GET /web-tool/sogou/search?text=…&limit=…
GET /web-tool/so360/search?text=…&limit=…      （别名 /web-tool/360/search）
GET /web-tool/bing/search?text=…&limit=…
```

```bash
curl "http://127.0.0.1:7000/web-tool/baidu/search?text=golang%20%E5%B9%B6%E5%8F%91%E6%A8%A1%E5%9E%8B&limit=5"
```

**200 响应**（核心信封，`Envelope` 结构）：

```json
{
  "query": { "text": "golang 并发模型", "engines_requested": ["baidu"] },
  "meta": {
    "request_id": "01936d3a-…",
    "requested_at": "2026-11-15T08:00:00Z",
    "took_ms": 1834,
    "engines_failed": [],
    "version": "2.1"
  },
  "results": [
    {
      "id": "01936d3a-…",
      "rank": 1,
      "type": "organic",
      "title": "Go 并发模型 - Go 语言圣经",
      "url": "https://example.com/golang-concurrency",
      "display_url": "example.com/golang-concurrency",
      "snippet": "…摘要…",
      "domain": "example.com",
      "favicon": "https://…/favicon.ico",
      "position": { "absolute": 1 },
      "engine": "baidu"
    }
  ],
  "serp_features": [],
  "pagination": { "page": 1, "has_more": false, "next_start": 10 }
}
```

结果对象字段（`Result` 结构，`position`/`domain_info`/`classification` 为空时省略；`meta.engines_responded` 仅在引擎回退等场景出现）：`id`、`rank`（引擎内名次）、`type`（`organic`/`ad`/`featured_snippet` 等模块类型）、`title`、`url`、`display_url`、`snippet`、`domain`、`favicon`、`engine`、`position.absolute`（全流 1-based 名次）。

常用查询参数：

| 参数 | 说明 |
| --- | --- |
| `text` | 查询词（必填，缺失 → 400 `EMPTY_QUERY`） |
| `limit` | 返回条数（非法值 → 400 `INVALID_LIMIT`） |
| `start` | 分页偏移（非法值 → 400 `INVALID_START`） |
| `format` | `json`（默认）/ `markdown` / `text` / `ndjson`；未知值 → 400 `UNKNOWN_FORMAT`；也可用 `Accept: text/markdown`、`text/plain`、`application/x-ndjson` 头选择 |
| `site` / `filetype` / 日期参数等 | 引擎语法透传，详见 [ARCHITECTURE.md](ARCHITECTURE.md) |

**鉴权与计费**：该组端点由核心管道直接挂载，**不走 API key 鉴权、不计费**；受引擎限速、熔断、冷却（见 §8）与 CORS 保护。请不要将其当作公网计费接口暴露。

**引擎级错误**（HTTP 状态随错误类型，核心信封 §1.1，完整表见 §7.2）：

```json
{
  "error": "blocked",
  "code": 403,
  "request_id": "01936d3a-…",
  "message": "search engine blocked the request",
  "meta": { "engine": "baidu" }
}
```

冷却期访问冷却引擎返回 §1.3 的 503 冷却信封（带 `Retry-After`）。

---

## 4. 健康与观测端点

全部免鉴权，用于探针与排障：

| 端点 | 说明 |
| --- | --- |
| `GET /web-tool/health` | 聚合健康：`{"status":"healthy\|degraded\|unhealthy","uptime":"2h13m","engines":[{"name":"baidu","initialized":true,"status":"ready\|not_initialized\|circuit_open"}],"storage":"bolt\|json"}`；`unhealthy` 时 HTTP 503。运行指标（goroutines/memory_mb/go_version）已移至鉴权后的 `/api/runtime`，不再在此匿名端点暴露 |
| `GET /web-tool/ready` | 就绪探针；停机排水期返回 503 + `status=draining` |
| `GET /web-tool/stats` | 缓存 / 代理 / 熔断 / 验证码求解器指标聚合 |
| `GET /web-tool/stats/cache` | 端点响应缓存命中统计 |
| `GET /web-tool/stats/proxy` | 代理池与健康度 |
| `GET /web-tool/stats/cb` | 各引擎熔断器状态 |

---

## 5. Agent 平面

三个入口，全部挂在 `/web-tool` 之下，是本平台对 AI Agent 的一等公民接口：

| 入口 | 用途 |
| --- | --- |
| `POST /web-tool/mcp` | MCP (Model Context Protocol) streamable HTTP 端点：Claude / Claude Desktop / Claude Code / 自研 Agent 直接把本平台当工具集调用 |
| `GET /web-tool/openapi.yaml` | OpenAPI 3.1 全端点规范（静态文件，`go:embed` 内嵌）：Dify / Coze / n8n / LangChain 导入即用 |
| `GET /web-tool/llms.txt` | 平台简介 + 端点清单（llms.txt 约定），供 LLM 自主发现能力 |

`openapi.yaml` 与 `llms.txt` 免鉴权、200，`Cache-Control` 允许缓存。

### 5.1 POST /web-tool/mcp

**传输**：MCP streamable HTTP，JSON-RPC 2.0，响应式 JSON（每条请求一个 JSON-RPC 响应，不使用 SSE 流）。协议版本 **`2025-06-18`**（legacy 三步握手，当前主流客户端均兼容）。

**鉴权**（二选一）：

- `X-API-Key: sk-w2a-…` —— API key 身份，**工具调用计费**（走配额，见 §9）；
- `Authorization: Bearer <管理员JWT>` —— 管理员身份，**不计费**。

鉴权失败返回平台信封 `401 {"error":"unauthorized"}`；API key 配额用尽返回 `429 {"error":"quota_exceeded"}`。

**会话**：`initialize` 响应头返回 `Mcp-Session-Id`，后续请求携带该头可复用内存会话；不携带则每请求独立（无状态调用同样合法）。显式关闭会话：`DELETE /web-tool/mcp`（带 `Mcp-Session-Id` 头）→ 204（已知会话）/ 404（未知会话）。

#### 握手三步

```bash
# ① initialize
curl -X POST http://127.0.0.1:7000/web-tool/mcp \
  -H "Content-Type: application/json" \
  -H "X-API-Key: sk-w2a-3f9c…" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"my-agent","version":"0.1.0"}}}'
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": { "tools": { "listChanged": false } },
    "serverInfo": { "name": "web2api", "version": "0.8.19" }
  }
}
```

（响应同时携带 `Mcp-Session-Id` 响应头。）

```bash
# ② initialized 通知 → HTTP 204，无响应体
curl -X POST http://127.0.0.1:7000/web-tool/mcp \
  -H "Content-Type: application/json" \
  -H "X-API-Key: sk-w2a-3f9c…" \
  -H "Mcp-Session-Id: <上一步返回的会话 ID>" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
```

```bash
# ③ 之后即可 tools/list 与 tools/call
```

#### tools/list

```bash
curl -X POST http://127.0.0.1:7000/web-tool/mcp \
  -H "Content-Type: application/json" -H "X-API-Key: sk-w2a-3f9c…" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      {
        "name": "web_search",
        "description": "用国内搜索引擎（baidu/sogou/so360/bing）搜索，返回 organic 结果列表",
        "inputSchema": {
          "type": "object",
          "properties": {
            "query": { "type": "string", "description": "查询词" },
            "engine": { "type": "string", "enum": ["baidu", "sogou", "so360", "bing"], "description": "引擎，默认 baidu" },
            "limit": { "type": "integer", "description": "返回条数，默认 10" }
          },
          "required": ["query"]
        }
      },
      {
        "name": "check_indexed",
        "description": "批量判定 URL 是否被国内引擎收录（每个 URL × 引擎一个结论）",
        "inputSchema": {
          "type": "object",
          "properties": {
            "urls": { "type": "array", "items": { "type": "string" }, "minItems": 1, "maxItems": 20, "description": "目标 URL 列表" },
            "engines": { "type": "array", "items": { "type": "string", "enum": ["baidu", "sogou", "so360", "bing"] }, "description": "引擎列表，缺省为全部" }
          },
          "required": ["urls"]
        }
      },
      {
        "name": "get_quota",
        "description": "查询当前身份（API key）的配额与用量",
        "inputSchema": { "type": "object", "properties": {} }
      },
      {
        "name": "list_engines",
        "description": "列出引擎及其冷却/熔断状态",
        "inputSchema": { "type": "object", "properties": {} }
      }
    ]
  }
}
```

#### tools/call —— web_search

```bash
curl -X POST http://127.0.0.1:7000/web-tool/mcp \
  -H "Content-Type: application/json" -H "X-API-Key: sk-w2a-3f9c…" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"golang 并发模型","engine":"baidu","limit":5}}}'
```

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"results\":[{\"rank\":1,\"title\":\"Go 并发模型\",\"url\":\"https://go.dev/blog/io2023\",\"snippet\":\"…\"}]}"
      }
    ],
    "isError": false
  }
}
```

工具输出 JSON（`content[0].text` 内为 JSON 字符串，解析后）：

```json
{
  "results": [
    { "rank": 1, "title": "…", "url": "https://…", "snippet": "…" }
  ]
}
```

计费：**1 unit/次**。

#### tools/call —— check_indexed

`{"name":"check_indexed","arguments":{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}}` → 输出结构与 `/web-tool/v1/index/check` 的 `results` 完全一致（§2.1）。计费：**len(urls) × len(engines) units**。

#### tools/call —— get_quota 与 list_engines

`get_quota` → §2.3 的配额对象；`list_engines` → 引擎清单与冷却/熔断状态（§2.4 数据源 + §6.7 冷却字段）。均不计费。

#### 其他方法与错误

- `ping` → `{"jsonrpc":"2.0","id":…,"result":{}}`。
- 未知方法 → JSON-RPC `-32601`；JSON 解析失败 → `-32700`；参数非法 → `-32602`。
- 引擎冷却期调用 `web_search` / `check_indexed`：请求本身成功（HTTP 200），工具以 `isError: true` 返回，`content[0].text` 携带 §1.3 冷却信封 JSON（含 `retry_after_seconds`）。
- 密码修改后旧 JWT 立即失效（见 §6.9），MCP 会话随之失效，需重新握手。

### 5.2 GET /web-tool/openapi.yaml

OpenAPI 3.1 规范，覆盖 `/v1/*`、`/api/*`、`/web-tool/mcp` 全部端点。Dify（自定义工具 → 导入 OpenAPI）、Coze（资源库 → 插件 → 导入 OpenAPI 3.0）、n8n（HTTP Request 节点 → 导入 cURL/OpenAPI）、LangChain（OpenAPI toolkit）均以它为通用入口；鉴权在上述平台里统一配 **Header 型 API Key**，Header 名 `X-API-Key`（n8n 选 "Header Auth"；Dify/Coze 选 API Key + 自定义 Header 名）。导入后逐接口的调用示例见 [AGENT-README.md](AGENT-README.md)。

### 5.3 GET /web-tool/llms.txt

Agent 友好的能力清单（平台简介、端点、鉴权约定、四引擎语义）。LLM/Agent 可先抓取该文件决定如何调用。

---

## 6. 控制平面 `/api/*`（管理 API，JWT）

鉴权：除登录外一律 `Authorization: Bearer <JWT>`；缺失/过期/无效 → `401 {"error":"unauthorized"}`。修改密码会使全部旧 token 立即失效（见 §6.9）。管理 API 默认全局限速 **60 req/min**（`security.admin_rate_per_minute`），超限 `429 {"error":"rate_limited"}` + `Retry-After`。

### 6.1 POST /web-tool/api/auth/login

```bash
curl -X POST http://127.0.0.1:7000/web-tool/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}'
```

**200**：

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9…",
  "expires_in": 86400,
  "user": { "username": "admin" }
}
```

**401**：`{"error":"unauthorized"}`（凭据错误）。
**429**：`{"error":"too_many_attempts"}`（同一 用户名+IP 连续 5 次失败后锁定 15 分钟，期间登录一律 429，带 `Retry-After`；`security.login_lockout.{fails,minutes}` 可调）。

### 6.2 GET /web-tool/api/me

```json
{ "username": "admin", "keys_count": 2, "today_calls": 17, "total_calls": 1337 }
```

### 6.3 API key 管理

| 端点 | 请求 | 响应 |
| --- | --- | --- |
| `POST /web-tool/api/keys` | `{"name":"ci-robot","daily_quota":500}`（`daily_quota` 缺省/0 = 不限） | key 对象 + **仅返回这一次**的完整 `key` |
| `GET /web-tool/api/keys` | — | key 对象数组（新→旧） |
| `PATCH /web-tool/api/keys/:id` | `{"name"?,"status":"active\|disabled"?,"daily_quota"?}` | 更新后的 key 对象 |
| `DELETE /web-tool/api/keys/:id` | — | `{"ok":true}` |

**POST 200 响应**：

```json
{
  "id": "key_01jdp2q9…",
  "name": "ci-robot",
  "prefix": "sk-w2a-3f9c",
  "status": "active",
  "daily_quota": 500,
  "used_today": 0,
  "used_total": 0,
  "created_at": "2026-11-15T08:00:00Z",
  "last_used_at": null,
  "key": "sk-w2a-3f9c8f2e7d4a9c1b2e6f8a0b3c5d17e"
}
```

`key` 形如 `sk-w2a-<32位hex>`，服务端只存 SHA-256 哈希，**列表中不再出现**。禁用（`status:"disabled"`）后的 key 调用公网 API 一律 401。

参数非法（空名、负配额、未知状态）→ 400 `{"error":"bad_request","code":"INVALID_PARAM","reason":"INVALID_PARAM"}`；`:id` 不存在 → 404 `{"error":"not_found"}`。

### 6.4 GET /web-tool/api/stats/overview

```json
{
  "today_calls": 17,
  "today_units": 34,
  "total_calls": 1337,
  "total_units": 2654,
  "active_keys": 2,
  "success_rate_24h": 98.1,
  "by_engine": {
    "baidu": { "calls": 40, "ok": 39 },
    "sogou": { "calls": 0, "ok": 0 },
    "so360": { "calls": 0, "ok": 0 },
    "bing": { "calls": 0, "ok": 0 }
  }
}
```

### 6.5 GET /web-tool/api/stats/timeseries?days=14

按本地日升序、零填充，最多 90 天：

```json
[ { "date": "2026-11-14", "calls": 0, "units": 0, "ok": 0 } ]
```

### 6.6 GET /web-tool/api/usage?limit=50&offset=0&key_id=

用量日志（新→旧），`limit` 最大 200：

```json
{
  "total": 1337,
  "items": [
    {
      "time": "2026-11-15T08:00:05Z",
      "key_id": "key_01jdp2q9…",
      "key_name": "ci-robot",
      "endpoint": "/web-tool/v1/index/check",
      "engines": ["baidu", "bing"],
      "urls": 2,
      "units": 4,
      "status": 200,
      "took_ms": 2501
    }
  ]
}
```

### 6.7 GET /web-tool/api/engines

管理员视角引擎状态机（冷却 + 熔断）：

```json
{
  "engines": [
    {
      "engine": "baidu",
      "endpoint": "/web-tool/baidu/search",
      "enabled": true,
      "breaker": { "engine": "baidu", "state": "closed", "failure_count": 0 },
      "state": "ok",
      "until": null,
      "reason": "",
      "consecutive_blocks": 0,
      "last_error_at": null
    }
  ]
}
```

- `state`：`ok` / `cooldown` / `circuit_open`。
- `state=cooldown` 时 `until` 为 RFC3339 解除时间、`reason` 为 `COOLDOWN`（自然冷却）或 `FORCED`（强制冷却）、`consecutive_blocks` 为连续拦截档位（1/2/3+）；`state=ok` 时 `until=null`、`reason=""`。
- `last_error_at`：该引擎最近一次被拦截的时间（RFC3339，从未拦截为 `null`）。

### 6.8 冷却管理

```bash
# 强制恢复（运维误判/维护窗口结束）
curl -X POST http://127.0.0.1:7000/web-tool/api/engines/baidu/cooldown/reset \
  -H "Authorization: Bearer $TOKEN"
# → {"ok":true,"engine":"baidu"}

# 强制冷却 N 分钟（维护窗口内阻止某引擎接客）
curl -X POST http://127.0.0.1:7000/web-tool/api/engines/baidu/cooldown/force \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"minutes":30}'
# → {"ok":true,"engine":"baidu","minutes":30}
```

未知引擎名或非法 `minutes` → 400 `{"error":"bad_request","code":"INVALID_PARAM","reason":"INVALID_PARAM"}`；装配未注入 EngineControl（降级态）→ 503 `{"error":"engine_control_unavailable"}`。

### 6.9 POST /web-tool/api/auth/password

修改管理员密码：

```bash
curl -X POST http://127.0.0.1:7000/web-tool/api/auth/password \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"old_password":"admin123","new_password":"s3cret-Passw0rd!"}'
```

**200**：

```json
{ "ok": true, "token": "eyJhbGciOi…", "expires_in": 86400, "token_version": 2 }
```

（响应携带**新签发的 token**，当前会话无需重新登录；其余旧 token 全部失效。）

- `new_password` 长度 < 8 → 400 `{"error":"bad_request","code":"INVALID_PARAM","reason":"INVALID_PARAM"}`。
- `old_password` 错误 → 401 `{"error":"unauthorized"}`。
- 成功后：新密码以 bcrypt 哈希存入 settings 桶并**覆盖 config.yaml 中的默认值**；`token_version` +1，**除本次返回的新 token 外，全部旧 JWT（含控制台与 MCP 的 Bearer 会话）立即失效**。

### 6.10 POST /web-tool/api/playground/check

控制台「在线调试」页后端：JWT 鉴权，**不计费、不写用量**。请求/响应结构与 `/web-tool/v1/index/check` 完全一致（§2.1）：

```bash
curl -X POST http://127.0.0.1:7000/web-tool/api/playground/check \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}'
```

错误：401（无/坏 token，平台信封）、400（参数，同 §2.1 表）、503（冷却信封）。

---

## 7. 错误码总表

### 7.1 平台信封错误（`/v1/*` 与 `/api/*`；`error` 字符串，无整型 `code`）

| HTTP | `error` | 场景 | Retry-After |
| --- | --- | --- | --- |
| 400 | `bad_request` | `reason` ∈ `EMPTY_QUERY`（urls 为空）/ `TOO_MANY_URLS`（>20 条）/ `INVALID_PARAM`（未知引擎、空名、负配额、短密码等） | 无 |
| 401 | `unauthorized` | key 缺失/未知/禁用；JWT 缺失/过期/密码修改后失效 | 无 |
| 404 | `not_found` | 资源（key、引擎名）不存在 | 无 |
| 429 | `quota_exceeded` | 当日配额用尽（预检拒绝，不扣 unit） | 有 |
| 429 | `too_many_attempts` | 登录连续失败锁定（默认 5 次失败锁 15 分钟，体内含 `retry_after_seconds`） | 有 |
| 429 | `rate_limited` | 管理端点超过 60 req/min（默认值） | 有 |
| 500 | `server_error` | 存储等内部错误，`message` 附原因 | 无 |

### 7.2 核心信封错误（`/{engine}/search` 与 `/web-tool/v1/index/check` 的引擎级单元；`code` 为整型 HTTP 状态码）

| `error` | HTTP | 场景 |
| --- | --- | --- |
| `blocked` | 403 | 引擎反爬拦截（浏览器验证/风控页） |
| `blocked` | 429 | 引擎返回 HTTP 429（`meta.upstream_status=429`） |
| `captcha_detected` | 429 | 引擎弹出图形验证码（同时触发该引擎 10 分钟冷却，见 §8.1） |
| `circuit_open` | 503 | 该引擎熔断器处于 open（连续失败达阈值） |
| `engine_cooldown` | 503 | 该引擎处于冷却期。两种出现方式：整请求 503（所选引擎全部冷却，§1.3 信封带 `meta.engines_cooldown`）；或批量判定 200 信封内的**单元级**错误 `{"error":{"code":"engine_cooldown","message":…,"retry_after_seconds":N}}`（部分引擎冷却时其余单元照常执行） |
| `search_timeout` | 504 | 搜索超时 |
| `proxy_auth` / `proxy_connect` / `proxy_timeout` / `proxy_unavailable` | 503 | 代理链路失败 |
| `parser_failure` | 502 | SERP 解析失败 |
| `engine_internal` | 502 | 引擎内部错误（未分类兜底） |
| `all_engines_failed` | 502 | 多引擎全失败（endpoint fallback 场景） |
| `not_implemented` | 501 | 该引擎不支持该能力（例：`raw_requests=true` 时所有引擎不支持收录判定——每个单元返回此错误） |
| `request_timeout` | 504 | 请求级超时（context deadline） |
| `request_canceled` | 503 | 客户端取消 |

参数校验类（核心信封，`error=bad_request`、`code=400`、`reason` 取下列值）：

| `reason` | 场景 |
| --- | --- |
| `EMPTY_QUERY` | 查询词为空 |
| `INVALID_LIMIT` | `limit` 非法 |
| `INVALID_START` | `start` 非法 |
| `INVALID_PARAM` | 其他参数非法 |
| `NO_ENGINES` | 无可用引擎 |
| `UNKNOWN_FORMAT` | `format` 取值不在 json/markdown/text/ndjson |
| `REQUEST_PROXY_URL_DISABLED` | 请求级代理被配置禁用 |
| `UNSUPPORTED_PROXY_SCHEME` | 代理 URL 协议不支持 |
| `TOO_MANY_URLS` | 收录判定 urls 超过 20 条 |

### 7.3 冷却信封（503）

```json
{"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":600}
```

触发、升级与恢复规则见 §8.1。

---

## 8. 限流与 Retry-After

所有 **429** 与 **503（engine_cooldown）** 响应都携带 `Retry-After` 头（秒数，HTTP 整数形式）。客户端（尤其 Agent/工作流）应读取该头做退避，而不是固定间隔重试。

| 场景 | HTTP | Retry-After 语义 |
| --- | --- | --- |
| `quota_exceeded` | 429 | 距当日配额重置（本地日界）的秒数 |
| 登录锁定 `too_many_attempts` | 429 | 距锁定解除的秒数（默认 ≤ 900） |
| 管理 API 限速 | 429 | 距限速窗口重置的秒数 |
| 引擎冷却 `engine_cooldown` | 503 | 距冷却解除的秒数（与 `retry_after_seconds` 字段同值） |

### 8.1 引擎冷却状态机（每引擎独立）

- **触发**：引擎返回 blocked（403）/ 429 / captcha。
- **连续升级**：连续第 1 次触发冷却 **10 分钟**，第 2 次 **30 分钟**，第 3 次及以上 **120 分钟**（`stability.engine_cooldown.steps_minutes: [10,30,120]`）。
- **计数归零**：连续无拦截达 **240 分钟**（`stability.engine_cooldown.reset_after_minutes`）后，连续计数归零重新从第 1 档起算。
- **冷却期内**：该引擎所有 search/check 快速失败（503 冷却信封），**不消耗真实引擎请求、不消耗代理与浏览器资源**。
- **到期探测**：冷却到期后放行一个探测请求——成功即恢复 `ok`；失败则进入下一档冷却。
- **行为级提示**：浏览器验证码页（无论人工还是自动触发）都会被判定为拦截并启动最短 10 分钟冷却。这是以牺牲短时可用性换取单出口 IP 存活率的刻意设计；高频使用方应配置代理池（[CONFIGURATION.md](CONFIGURATION.md) `proxies` 节）。
- 运维干预：`POST /web-tool/api/engines/{name}/cooldown/reset`（强制恢复）、`POST /web-tool/api/engines/{name}/cooldown/force {"minutes":N}`（强制冷却），见 §6.8；控制台「引擎状态」页（`#/engines`）提供同能力。

熔断器（`circuit_breaker.{failures,recovery_seconds,successes}`）与冷却状态机是**两套独立机制**：熔断针对连续请求失败（技术性失败），冷却针对反爬拦截（blocked/captcha）。两者同时生效时，冷却检查在前（不发真实请求）。

---

## 9. 计费口径

- 单位：**1 URL × 1 引擎 = 1 unit**。`/web-tool/v1/index/check` 计 `len(urls) × len(engines)`；MCP `web_search` 计 1；MCP `check_indexed` 计 `len(urls) × len(engines)`。`/web-tool/v1/quota`、`/web-tool/v1/engines`、MCP `get_quota`、`list_engines`、管理 API、`/web-tool/api/playground/check` 不计费。
- **扣费时点**：请求以 **HTTP 200 完成**即扣费，但按响应信封**逐引擎核对，仅成功单元计费**——200 信封内引擎级失败的单元（`results[].engines.<engine>.error`，含 `engine_cooldown` 冷却、`circuit_open` 熔断等）**不计费**。响应信封无法解析时按请求单元全额计费（兜底口径）。4xx（401/400）与配额预检 429、冷却 503 **不扣费**。MCP 平面与 `/v1` 同口径：`check_indexed` 的工具结果（`result.content[].text`）携带逐引擎明细时按成功单元计费；明细不可解析（`web_search` 结果、工具级错误信封）时按请求单元全额计费。
- **用量日志如实化（B-01）**：被拒绝的请求（400 参数错误、401/403、429 配额与限速、503 冷却/熔断快速失败）同样写入用量日志，但 `status` 记录真实终态码、`units=0`，并携带 `outcome` 字段：`billed`（200 完成）/ `engine_failed`（5xx 引擎失败）/ `rejected`（未进入业务逻辑）。MCP 计费记录的 `outcome` 与 `/v1` 同规则落账。S4 之前的存量记录无 `outcome` 字段，按 reached 计。
- **管理统计口径（P-01）**：`GET /api/stats/overview` 的 `today_calls`/`total_calls`/`success_rate_24h` 统计**有效调用**（到达业务逻辑的 billed+engine_failed），参数错误、配额与限速拒绝、冷却拒绝不计入；`today_calls_all`（今日全部请求）与 `rejected_today_24h`（24h 内拒绝数）为 additive 字段。口径变化在发布说明中说明,历史记录无法回填。
- 对账：`GET /api/reconcile` 按日核对「落账 units」与「逐引擎成功单元数」；携带逐引擎明细的记录逐条核对，无明细的记录（MCP `web_search`、信封解析失败的存量数据）按落账口径计入、不判 mismatch。
- 对计费敏感的批量任务建议：先小批量探测引擎健康度（`GET /web-tool/v1/engines` / `list_engines`），避开冷却与熔断中的引擎，再放大批量——冷却/熔断单元虽不计费，但整请求会因此缩水。
- 配额维度：`daily_quota`（本地日重置，0 = 不限）与 `used_total`（累计）；用量日志（`/web-tool/api/usage`）逐笔记录 units、HTTP 状态与各引擎成功/失败数。
- **配额硬上限语义（B-15）**：配额为「预检-事后扣减」两段式——并发在途请求可轻微超出 `daily_quota`（每请求最多超额「在途 units-1」）；扣减在 bbolt 单事务内完成,**无丢账**。严格 Reservation 语义（先记账后执行、失败回滚）未实现;对账接口 `GET /api/reconcile` 可核对扣费与流水差异。
- 免费额度、QPS 分级等商用配额策略当前未实现；自托管场景配额即自我约束工具。

---

## 附：端点速查

```
数据平面（X-API-Key）
  POST /web-tool/v1/index/check        批量收录判定（1..20 URL × 引擎）
  GET  /web-tool/v1/index/check        单 URL 收录判定（?url=&engines=）
  GET  /web-tool/v1/quota              当前 key 配额
  GET  /web-tool/v1/engines            引擎与熔断状态

搜索（无鉴权，核心管道）
  GET  /web-tool/{baidu|sogou|so360|bing|360}/search?text=&limit=&format=

Agent 平面
  POST /web-tool/mcp                   MCP streamable HTTP（X-API-Key 或 JWT）
  GET  /web-tool/openapi.yaml          OpenAPI 3.1 规范
  GET  /web-tool/llms.txt              Agent 能力清单

控制平面（JWT）
  POST   /web-tool/api/auth/login                                      登录（失败锁定；同时下发 httpOnly 会话 cookie）
  POST   /web-tool/api/auth/logout                                     退出登录（撤销当前会话 + 清 cookie, S4）
  POST   /web-tool/api/auth/refresh                                    会话滑动续期（新 token 同会话, S4）
  POST   /web-tool/api/auth/password                                   修改密码（旧 token 与全部会话失效）
  GET    /web-tool/api/me                                              当前管理员概览（含应用版本号）
  GET    /web-tool/api/runtime                                         运行时指标 goroutine/内存/Go 版本（S4,自 /health 收敛）
  POST   /web-tool/api/keys                                            创建 key（完整 key 仅此一次；支持 expires_at/allowed_ips）
  GET    /web-tool/api/keys                                            key 列表
  PATCH  /web-tool/api/keys/:id                                        改名/禁用/改配额/过期时间/IP 白名单
  POST   /web-tool/api/keys/:id/rotate                                 轮换 key（旧 key 即时失效,计数保留, S4）
  DELETE /web-tool/api/keys/:id                                        删除 key
  GET    /web-tool/api/stats/overview                                  总览（B-01 有效调用口径 + additive 字段）
  GET    /web-tool/api/stats/timeseries?days=14                        日粒度时序
  GET    /web-tool/api/usage?limit=&offset=&key_id=&from=&to=          用量日志（S4 增 from/to）
  GET    /web-tool/api/usage/export?format=csv|json&key_id=&from=&to=  用量导出下载（S4）
  GET    /web-tool/api/reconcile?date=YYYY-MM-DD                       扣费-流水对账（S4）
  GET    /web-tool/api/engines                                         引擎状态机（含冷却）
  POST   /web-tool/api/engines/:name/cooldown/reset                    强制恢复
  POST   /web-tool/api/engines/:name/cooldown/force                    强制冷却 {"minutes":N}
  GET    /web-tool/api/sessions                                        管理员会话列表（S4）
  DELETE /web-tool/api/sessions/:id                                    撤销会话（撤销当前会话=退出, S4）
  GET    /web-tool/api/audit?limit=&offset=                            安全审计轨迹（S4）
  GET    /web-tool/api/admin/backup                                    下载数据一致性快照（S4）
  POST   /web-tool/api/playground/check                                控制台在线调试（不计费）

健康与观测
  GET  /web-tool/health  /web-tool/ready  /web-tool/stats
  GET  /web-tool/stats/cache  /web-tool/stats/proxy  /web-tool/stats/cb
  GET  /web-tool/metrics                        OpenMetrics 指标（security.metrics_token Bearer 或仅 loopback, S4）

CLI（停服态, S4）
  web2api admin-backup   --out <file>     备份 bbolt 快照
  web2api admin-restore  --in <file>      校验并恢复快照
  web2api admin-reset                     停服重置管理员密码
```
