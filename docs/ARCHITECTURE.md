# Architecture

web2api 是 Go 实现的自托管网站收录查询开放平台，重心在国内搜索引擎的收录判定与反爬对抗；由一套通用 SERP 框架改造而来，保留多引擎管道骨架。

## 执行模式

- **浏览器模式（默认）**：`go-rod` + headless Chromium（容器内为 chromedp/headless-shell）。所有引擎都支持，能拿到 JS 水合后的结果。
- **纯 HTTP 模式**：直接 HTTP + goquery 解析。由 CLI 的 `--raw`、以及国内引擎的**兜底通道**共用。
- **双通道（国内引擎）**：浏览器通道被反爬拦截（captcha/timeout/block/parser）时，引擎自动改用 HTTP 通道拉取同一 URL；`core.CaptchaGate` 记录拦截状态，10 分钟内优先走 HTTP。

## 目录结构

```text
.
├── main.go
├── config.yaml                 # 本仓库国内部署配置
├── baidu/ bing/ sogou/ so360/
│   ├── url.go                  # URL 构造（拒绝空查询、翻页换算）
│   ├── selectors.go            # 选择器集中处（改版时优先改这里）
│   ├── parse_html.go           # goquery 解析（纯函数，无网络）
│   ├── search.go               # 浏览器通道 + 双通道编排
│   ├── search_raw.go           # 纯 HTTP 通道（国内引擎均有）
│   ├── index_check.go          # 收录判定入口（四引擎均以 URL 为查询词）
│   ├── features.go             # 可选：SERP 特性模块
│   └── testdata/               # 真实 SERP 快照
├── core/
│   ├── server.go               # 路由（全部挂在 core.BasePath=/web-tool 下）、错误映射
│   ├── common.go               # Query、引擎选项、结果工具
│   ├── index_check.go          # 收录判定核心：归一化匹配、跳转解析、批量编排
│   ├── browser.go              # 浏览器池、profile/lane、页面工具
│   ├── http_client.go          # raw HTTP 客户端（tls-client）、GuardPrivateNetworks
│   ├── resilient.go retry.go circuit_breaker.go   # 重试/熔断
│   ├── captcha_gate.go         # 浏览器通道拦截门控
│   ├── cache.go proxy*.go      # 响应缓存、代理策略与 lane
│   ├── response*.go format_*.go                   # 响应构建与序列化
│   └── enrichment_domain.go enrichment_domains.yaml
├── platform/                   # 收录查询平台：API key/配额/用量(bbolt)、JWT 管理端、/v1 /api 路由
├── web/site/                   # 产品官网（纯静态零依赖，由 serve 挂在 /web-tool/ 根）
├── web/admin/                  # 管理控制台（纯静态零依赖，由 serve 挂在 /web-tool/console/）
├── cmd/                        # CLI：search / serve，引擎注册表与装配
├── deploy/                     # Docker 编排、E2E/稳态脚本、运维文档
└── docs/                       # 架构、配置、测试
```

## 核心接口

`core.SearchEngine`：

- `Search(ctx, Query) ([]SearchResult, error)`
- `IsInitialized() bool`
- `Name() string`
- `GetRateLimiter() *rate.Limiter`

收录判定另需实现 `core.IndexChecker`（`CheckIndexed(ctx, targetURL string, runtime Query)`），
四个引擎均已实现；serve 模式下 `pooledBrowserEngine` 会把它转发给池内引擎实例。

国内引擎额外导出包级 `SearchRaw(ctx, Query)`，供双通道兜底、CLI raw 模式与测试复用。

`core.SearchEngineOptions`（每个引擎的 `config.yaml` 段）：

`rate_requests` / `rate_seconds` / `rate_burst` / `selector_timeout` / `captcha` / `profile`；引擎包装层另有 `proxy`（tag）与 `disabled`。

## Query

来自 HTTP 查询参数与请求头（`core/common.go` 的 `InitFromContext`）：

- 文本与过滤：`text`、`site`、`file`、`date`（`YYYYMMDD..YYYYMMDD`）、`lang`、`region`
- 分页与开关：`limit`（1–100，默认 10）、`start`（≥0）、`filter`、`features`
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
 ├─ 响应构建：稳定 ID、URL 规范化、pagination、domain_info/classification
 └─ 序列化：JSON / Markdown / text / NDJSON
```

收录判定流（`/web-tool/v1/index/check`，`core/index_check.go`）：

```text
APIKeyGuard（key 校验/配额预检） → IndexCheckHandler
 ├─ ≤20 条：同步执行，200 返回结果信封
 └─ >20 条：IndexJobManager 入队 → 202 {job_id}（状态/结果/CSV 走 /v1/index/check/jobs/*）

RunIndexCheckUnits：每个引擎一个协程串行推进（同引擎同时只有一个在途请求，
  避免多 worker 抢令牌形成突发）；跨引擎并行，经 ResilientSearcher 的限流/熔断/冷却/代理
 ├─ engine.CheckIndexed
 │    ├─ baidu：直连抓目标页 <title>（私网/DNS 重绑定防护，1h 缓存）再以标题为检索词；
 │    │           取不到标题则回退 URL 短语
 │    └─ 其余引擎：仍以 URL 为查询词
 └─ EvaluateIndexSERP
      ├─ 直连候选：归一化 host+path 相等 → indexed=true/exact
      ├─ 标题探测（baidu）：结果标题相似度 ≥0.72 且同站（可注册域名）→ exact
      │                     （手机站/电脑站视为同站；同标题转载站不算）
      └─ 兜底：解析 /link、bing /ck/a 跳转后再匹配 URL
```

## 响应

搜索信封（`/web-tool/{engine}/search`）：

- `query`：请求回显，含 `engines_requested`
- `meta`：`request_id`、`requested_at`、`took_ms`、`engines_failed`、`version`
- `results`：结构化结果（`rank`、`type`、`engine`、`title`、`url`、`domain`、`snippet` …）；跨页时 `rank` 为绝对排名
- `pagination`：`page`、`has_more`、`next_start`
- `serp_features`：非 organic 模块（相关搜索、answer box 等）

稳定 ID 前缀：`s_`（网页结果）。

响应头：`X-Request-ID`、`X-Cache`、`X-Fallback-Engine`、`X-Proxy-Mode/Tag/Used`、`X-Network-Bytes`、`X-Browser-Profile-Id`。

错误体：`{"error","code","request_id","message","reason"?,"meta"?}`。引擎级错误把机器码放 `error`；参数校验错误 `error=bad_request`，具体原因放 `reason`（`INVALID_LIMIT` 等）。完整状态码表见 [API.md §7](API.md)。

## 收录判定（index check）

- 查询词：baidu/sogou/so360 用完整 URL；bing 依次尝试多种查询形式并命中早停（优先带引号的完整 URL，其次完整 URL、去 scheme 的 host+path），任一 SERP 含精确匹配即返回。bing 早期使用的 `"url:<url>"` 语法于 2026-11 停用——实测 cn.bing.com 对该操作符返回无关结果。
- 候选跳转解析：sogou/so360 的 `/link` 在引擎内解析；漏网的 `baidu.com/link?url=`、`sogou.com/link`、`so.com/link` 由 core 复用 rawClickRequest 读 Location；bing 的 `/ck/a` 离线解码。
- 归一化：协议与 host 小写、去默认端口、去 fragment、去尾部 `/`（根路径除外）、百分号解码；目标 URL 无 query 时忽略结果的 query，有 query 时参数排序后须相等。
- 判定：host 与 path 相等 → `indexed=true, match="exact"`；否则 `indexed=false, match="none"`。
- 准确率：所有引擎在“已收录但排名靠后”时会漏报（SERP 前 10 没有目标页）；根路径 URL 命中率最高，深链接易漏。建议多引擎交叉验证，任一 `indexed=true` 即确认收录。

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

优先级：CLI > `WEB2API_*` 环境变量 > `config.yaml` > 默认值。逐项说明见 [CONFIGURATION.md](CONFIGURATION.md)。新增键：`app.log_retention_days=30`（按 mtime 清理滚动日志；`app.log_max_mb=0` 只关闭大小轮转，按天清理仍生效）、`security.blacklist=[]`（首启种子，一次性标记落 settings 桶）。

安全模型：三级角色按用户库逐请求复核；旧 JWT（无 uid）按 sub 查库校验，仅用户库为空才回退 bootstrap admin；离线 `admin-reset` 同时更新 settings/users 桶并递增 token_version；全站黑名单守卫命中返回 `403 reason=blacklisted`。

## 平台与装配（cmd/serve.go）

- 所有路由（官网、控制台、`/v1/*`、`/api/*`、`/health /ready /stats`、`/{engine}/search`）统一挂载在
  固定前缀 `core.BasePath = "/web-tool"` 之下，根路径 404；前缀不是配置项。
- `cmd.mountPlatformAndPanel`：`platform.New(config.Platform, serv.EngineStatusSnapshot)`
  → `RegisterAdmin`/`RegisterPublic`（根在 BasePath 的路由组）→ MCP Agent 平面 →
  静态挂载（`cmd.mountStaticPanels`）：官网 `web/site` 以 fiber Static 挂在 BasePath 根，
  管理控制台 `web/admin` 挂在 BasePath+`/console`（`GET <BasePath>/console` 301 到 `/console/`；
  API 路由先注册，优先于静态通配）。
- `platform.enabled=false` 只卸下 `/api/*` 与 `/v1/*`，官网与控制台静态页与健康端点保留。
- 控制平面在 JWT 校验后按持久化用户记录解析角色（`admin > operator > viewer`），每个 `/api/*` 路由带最小角色守卫；用户表在 `users` 桶，黑名单在 `blacklist` 桶，两种 Store 后端（bbolt / JSON fallback）等价实现。
- IP 黑名单守卫作为 `ServerOptions.ExtraMiddleware` 在 core 注册健康/统计/搜索路由之前安装，覆盖全站；过期规则匹配时忽略，命中写审计并累加计数。
- 日志写入器同时支持按大小轮转与按 mtime 保留天数清理；`logs.txt.N` / `audit.log.N` 在启动、轮转后和每 12 小时被剪枝，`/api/logs*` 提供查询与手动清理。
- 控制平面端点（括号内为最小角色）：`auth/login|logout|refresh|password`(viewer)、`me`(viewer，含 role/user_id)、`runtime`(viewer)、`keys`(GET viewer / 写 operator，含 rotate)、`stats/*` 与 `usage|usage/export|reconcile`(viewer)、`engines`(viewer)、`engines/{name}/cooldown/reset|force`(operator)、`sessions`(GET viewer / DELETE admin)、`audit`(viewer)、`admin/backup`(admin)、`playground/check`(viewer)、`users*`(admin)、`blacklist*`(operator)、`logs|logs/config`(viewer)、`logs/cleanup`(admin)。
- 权限不足统一返回 `403 {"error":"forbidden","code":"insufficient_role"}`；MCP 的 JWT 通道要求 `sid` 且持久化角色为 admin，operator/viewer JWT 同样返回 403。
- 引擎清单/熔断状态的可复用数据源是 `Server.EngineStatusSnapshot()`，同时供给
  `/web-tool/v1/engines` 与 `/web-tool/api/engines`。
- 优雅关闭：`listenWithGracefulShutdown` 的 onClose 依次关闭浏览器池与 platform 存储。

## 测试

单测、E2E、稳态脚本的分工与用法见 [TESTING.md](TESTING.md)。
