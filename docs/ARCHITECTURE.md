# Architecture

web2api 是 Go 实现的搜索 API 服务，基于 [OpenSERP](https://github.com/karust/openserp) 的框架改造，重心在国内搜索引擎。

## 执行模式

- **浏览器模式（默认）**：`go-rod` + headless Chromium（容器内为 chromedp/headless-shell）。所有引擎都支持，能拿到 JS 水合后的结果。
- **纯 HTTP 模式**：直接 HTTP + goquery 解析。由 `/{engine}/parse`、CLI 的 `--raw`、以及国内引擎的**兜底通道**共用。
- **双通道（国内引擎）**：浏览器通道被反爬拦截（captcha/timeout/block/parser）时，引擎自动改用 HTTP 通道拉取同一 URL；`core.CaptchaGate` 记录拦截状态，10 分钟内优先走 HTTP。

## 目录结构

```text
.
├── main.go
├── config.yaml                 # 本仓库国内部署配置
├── baidu/ bing/ google/ yandex/ duckduckgo/ ecosia/ sogou/ so360/
│   ├── url.go                  # URL 构造（拒绝空查询、翻页换算）
│   ├── selectors.go            # 选择器集中处（改版时优先改这里）
│   ├── parse_html.go           # goquery 解析（纯函数，无网络）
│   ├── search.go               # 浏览器通道 + 双通道编排
│   ├── search_raw.go           # 纯 HTTP 通道（国内引擎均有）
│   ├── features.go             # 可选：SERP 特性模块
│   └── testdata/               # 真实 SERP 快照
├── core/
│   ├── server.go               # 路由、handler、错误映射
│   ├── common.go               # Query、引擎选项、结果工具
│   ├── browser.go              # 浏览器池、profile/lane、页面工具
│   ├── http_client.go          # raw HTTP 客户端（tls-client）、GuardPrivateNetworks
│   ├── resilient.go retry.go circuit_breaker.go   # 重试/熔断
│   ├── captcha_gate.go         # 浏览器通道拦截门控
│   ├── cache.go proxy*.go      # 响应缓存、代理策略与 lane
│   ├── server_extract.go extract/                 # 正文提取
│   ├── response*.go format_*.go clusters.go       # 响应构建与序列化
│   └── enrichment_domain.go enrichment_domains.yaml
├── cmd/                        # CLI：search / serve，引擎注册表
├── deploy/                     # Docker 编排、E2E/稳态脚本、运维文档
└── docs/                       # 架构、配置、测试、OpenAPI
```

## 核心接口

`core.SearchEngine`：

- `Search(ctx, Query) ([]SearchResult, error)`
- `SearchImage(ctx, Query) ([]SearchResult, error)`
- `IsInitialized() bool`
- `Name() string`
- `GetRateLimiter() *rate.Limiter`

国内引擎额外导出包级 `SearchRaw(ctx, Query)`，供双通道兜底、CLI raw 模式与测试复用。

`core.SearchEngineOptions`（每个引擎的 `config.yaml` 段）：

`rate_requests` / `rate_seconds` / `rate_burst` / `selector_timeout` / `captcha` / `profile`；引擎包装层另有 `proxy`（tag）与 `disabled`。

## Query

来自 HTTP 查询参数与请求头（`core/common.go` 的 `InitFromContext`）：

- 文本与过滤：`text`、`site`、`file`、`date`（`YYYYMMDD..YYYYMMDD`）、`lang`、`region`
- 分页与开关：`limit`（1–100，默认 10）、`start`（≥0）、`filter`、`features`
- 提取：`extract`（bool 或 N≤5）、`extract_mode`（auto/fast/rendered）、`min_runes`
- 跳转解析：`resolve_urls`（true/false/N）
- 代理：`X-Use-Proxy`（direct/tag）、`X-Proxy-URL`、`X-Proxy-Country/Class/Provider/Session-ID`
- 其它头：`X-Request-ID`、`X-Tenant`、`X-Use-Profile`

至少需要 `text`、`site`、`file` 之一，否则返回 400 `EMPTY_QUERY`。

## 请求流

```text
HTTP
 ├─ Fiber 中间件：RequestContext → CORS → RequestLogger
 ├─ Query.InitFromContext / resolveFormat
 ├─ 缓存查找（仅专用端点的 JSON 响应）
 ├─ ResilientSearcher
 │    ├─ 熔断器 → 限速器 → 代理策略
 │    ├─ 重试循环（captcha/blocked/parser/engine_internal 不重试）
 │    └─ engine.Search
 │         ├─ CaptchaGate 已拦截？→ SearchRaw
 │         ├─ 浏览器：Navigate → HTML → ParseHTML
 │         └─ 浏览器失败 → SearchRaw（HTTP 通道）
 ├─ 响应构建：稳定 ID、URL 规范化、pagination、domain_info/classification、图片元数据
 ├─ mega：URL 归一化去重 + clusters
 └─ 序列化：JSON / Markdown / text / NDJSON
```

## 响应

搜索信封（`/{engine}/search`、`/mega/search`）：

- `query`：请求回显，含 `engines_requested`
- `meta`：`request_id`、`requested_at`、`took_ms`、`engines_failed`、`version`
- `results`：结构化结果（`rank`、`type`、`engine`、`title`、`url`、`domain`、`snippet` …）；跨页时 `rank` 为绝对排名
- `pagination`：`page`、`has_more`、`next_start`
- `serp_features`：非 organic 模块（相关搜索、answer box 等）
- `clusters`：仅 `/mega/search`

稳定 ID 前缀：`s_`（网页结果）、`i_`（图片）、`c_`（聚合簇）。

响应头：`X-Request-ID`、`X-Cache`、`X-Fallback-Engine`、`X-Proxy-Mode/Tag/Used`、`X-Network-Bytes`、`X-Browser-Profile-Id`。

错误体：`{"error","code","request_id","message","reason"?,"meta"?}`。引擎级错误把机器码放 `error`；参数校验错误 `error=bad_request`，具体原因放 `reason`（`INVALID_LIMIT` 等）。完整状态码表见根 README。

## Mega 聚合

`/mega/search`：

- `engines` 指定引擎（空则用全部已启用引擎），先按别名归一化（`360`→`so360`、`duck`/`ddg`→`duckduckgo`）
- `mode`：`balanced`（并行全部）、`any`（按序回退，首个成功即止）、`fast`（按熔断统计选最快引擎）
- 允许部分成功，失败引擎写入 `meta.engines_failed`
- 按归一化 URL 去重，构建 `clusters`（含逐引擎出现次数与排名），分数 = sum(1/rank)/引擎数，封顶 1.0

## 弹性与反爬

1. 引擎限速（token bucket，按 `rate_requests/rate_seconds/rate_burst`）
2. 重试（指数退避；`resilience.max_retries`）
3. 熔断（`circuit_breaker.*`，阈值/恢复时间可配；captcha、blocked、rate_limited、parser_failure、engine_internal、not_implemented 属于不重试错误，直接计入熔断统计）
4. 代理策略与健康检查（失败只由网络/代理错误触发，解析错误与验证码不影响代理健康）
5. `CaptchaGate`：浏览器通道被拦后优先 HTTP 通道
6. 响应缓存（仅专用端点 JSON，TTL 可配）

浏览器指纹：

- 引擎可配置 `profile` 固定指纹（国内引擎默认 `chrome-win-uhd620`）
- 未配置时按引擎/地区/会话哈希从 `core/browser/profiles.json` 选择
- 代理 lane 复用“引擎 + 代理会话”维度的 cookie，遇验证码可按配置清理

## 配置

优先级：CLI > `OPENSERP_*` 环境变量 > `config.yaml` > 默认值。逐项说明见 [CONFIGURATION.md](CONFIGURATION.md)。

## 测试

单测、E2E、稳态脚本的分工与用法见 [TESTING.md](TESTING.md)。