# Web2API API 参考

本文档是 **Web2API 网站收录查询开放平台** 的全量接口文档。整个应用固定部署在 **`/web-tool`** 子路径下（代码内常量 `core.BasePath`，根路径无任何路由），本文所有路径均已带前缀，可逐字拷贝使用。

**第一次接入？先看 [QUICKSTART.md](QUICKSTART.md)** —— 5 分钟从零到第一个收录判定结果，含真实响应样例、Python/JS/curl 可复制代码与排错表。本文是逐端点的完整参考。

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
| `{BASE}` | 你的服务根地址，如 `http://127.0.0.1:7000/web-tool` 或部署后的 `https://your-domain.com/web-tool`；下文示例均用它 |
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
| 429 | `quota_exceeded` | `retry_after_seconds`（响应亦带 `Retry-After` 头） |
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

鉴权：请求头 `X-API-Key: sk-w2a-…`（兼容 `Authorization: Bearer sk-w2a-…`）。key 缺失、未知或被禁用一律 `401 {"error":"unauthorized"}`。配额预检：`当日已用 + 本次计费 unit 数 > daily_quota`（`daily_quota=0` 为不限）时拒绝，`429 {"error":"quota_exceeded"}`，**预检拒绝不写用量记录、不扣费**。

计费口径：**1 URL × 1 引擎 = 1 unit**。

### 2.1 POST /web-tool/v1/index/check

批量收录判定：每个 (URL × 引擎) 单元在引擎内检索前 10 条结果并做归一化匹配。

> **判定口径（baidu）**：百度开启 `baidu.index_title_probe`（默认开启）后，检索词是目标页面的 `<title>`（同域 HTTP 抓取，不经过百度），结果按标题相似度匹配（阈值 0.72，已归一化标点/空格/来源后缀/截断省略号），并校验命中结果的主机与目标一致（同标题转载不算本页）。之所以不用 `wd=<URL>`：实测已收录文章用 URL 当检索词也常返回空结果，会把"已收录"判成"未收录"。标题抓取失败时自动回退到 URL 短语检索。
>
> 百度明确返回"没有相关结果"（`div.content_none`）时本单元判 `indexed:false`，不再按超时重试；收录单元默认只重试 1 次（`stability.index_check.max_retries`）。

**请求体**

```json
{
  "urls": ["https://example.com/post-1", "https://example.com/post-2"],
  "engines": ["baidu", "sogou", "so360", "bing"]
}
```

- `urls`：1 条以上，上限为 `index_jobs.max_urls`（默认 5000）。
  - **≤ 20 条**：同步返回 200（本节形状）。
  - **> 20 条**：请求被接收入队，返回 **202** + `job_id`（异步任务，见 §2.2b）；调用方随后查进度/取结果。
  - 未开启任务队列（`index_jobs.enabled=false`）时，> 20 条仍返回 400 `TOO_MANY_URLS`。
  - 为空 → 400 `EMPTY_QUERY`；非法 URL → 400 `INVALID_PARAM`。
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
  - 百度按标题判定命中时，`matched_result_url` 可能是同一站点的移动域（如 `m.example.com` 命中 `www.example.com` 的文章）；判定已按可注册域名校验同站，不会把转载站算成本页。
- 失败单元：`error.code` / `error.message`，错误码与搜索端点同表（见 §7.2），例如 `blocked`、`captcha_detected`、`circuit_open`、`not_implemented`。
- `meta.units = len(urls) × len(engines)`，即本次请求的 unit 总数（计费口径为**实扣**：带 `error` 的单元不扣费）。
- **批量超时预算**：引擎限流（防验证码，经实测标定）决定了批量的串行吞吐——百度默认 `5 次/分钟`（约每 12 秒放行一个查询），20 条 URL 的百度批量需要约 4 分钟的令牌排队。请求预算按批量规模动态放宽：`基础重试预算 + (单元数 × 最大尝试次数 − 1) × 最慢所选引擎的令牌间隔`，**上限 12 分钟**。正常批量在此预算内真实跑完；若仍超出（极端配置），排队单元返回 `engine_queue_exceeded`（§7.2），未发起真实查询、不计费。
- 超过 20 条的批量不要靠提高预算硬撑，走 §2.2b 的异步任务：同样的节奏，但不受单次 HTTP 连接寿命限制。
- `meta.engines_cooldown`：请求发出时已处于冷却期的引擎列表，元素为 `{"engine","until","reason"}`；`reason` 为 `COOLDOWN`（自然冷却）或 `FORCED`（运维强制冷却）。冷却中的引擎不会对真实引擎发请求——若**部分**引擎冷却，其余单元照常执行（200 信封内冷却单元的 `error.code` 为 `engine_cooldown`，附 `retry_after_seconds`）；若**所选引擎全部**冷却，整个请求 503（§1.3）。

**错误响应**

| HTTP | 信封 | 场景 |
| --- | --- | --- |
| 200 | 见上 | ≤ 20 条，同步结果 |
| 202 | `{"job_id",…}`（§2.2b） | > 20 条且已开启任务队列，已入队 |
| 400 | `{"error":"bad_request",…,"reason":"EMPTY_QUERY"}` | `urls` 为空 |
| 400 | `{"error":"bad_request",…,"reason":"TOO_MANY_URLS"}` | 超过 20 条但**未开启任务队列**，或超过 `index_jobs.max_urls` |
| 400 | `{"error":"bad_request",…,"reason":"INVALID_PARAM"}` | 含未知引擎、非法 URL 等 |
| 401 | `{"error":"unauthorized"}` | key 缺失 / 未知 / 禁用 |
| 429 | `{"error":"quota_exceeded"}`（`Retry-After` 头） | 当日配额用尽 |
| 503 | `{"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":600}`（`Retry-After` 头） | 所选引擎全部处于冷却期 |

> **本端点 400 的信封现状（实测）**：契约目标形状是上面的三字段平台信封（`platform/errors.go` 注释与 §1.2），管理端 `/api/*` 的 400 已是该形状；但 `/web-tool/v1/index/check` 的参数校验当前由核心 index 处理器应答，实测返回的是 §1.1 核心信封：
>
> ```json
> {"error":"bad_request","code":400,"request_id":"01a1db49-…","message":"TOO_MANY_URLS: urls accepts at most 5000 entries per job, got 6000","reason":"TOO_MANY_URLS"}
> ```
>
> 两种形状的 `error` 恒为 `bad_request`、`reason` 同值——**客户端按 `reason` 字段分支即可两代兼容**（仓库自带 e2e 也是按 `reason` 断言的）。信封统一列入集成收尾清单。

```bash
# curl 示例
curl -X POST {BASE}/v1/index/check \
  -H "X-API-Key: sk-w2a-3f9c…" -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}'
```

```js
// fetch 示例
const res = await fetch('{BASE}/v1/index/check', {
  method: 'POST',
  headers: { 'X-API-Key': 'sk-w2a-3f9c…', 'Content-Type': 'application/json' },
  body: JSON.stringify({ urls: ['https://example.com/post-1'], engines: ['baidu', 'bing'] }),
});
const data = await res.json();
```

### 2.2 GET /web-tool/v1/index/check

单 URL 便捷版，查询参数传值，响应结构同上：

```bash
curl "{BASE}/v1/index/check?url=https%3A%2F%2Fexample.com%2F&engines=360" \
  -H "X-API-Key: sk-w2a-3f9c…"
```

- `url`：必填（URL 编码）；`engines`：可选，逗号分隔，别名 `360` 可用。
- 该请求计费 unit 数恒为 `1 × len(engines)`（缺省引擎时为 4）。

### 2.2b 批量提交（异步任务）

> 一次提交整张表，服务端排队跑完。**≤20 条仍然是同步返回**（2.1），所以现有调用一行都不用改；只有超过 20 条时才会拿到一个任务号。

`POST /v1/index/check` 的请求体不变，只是 `urls` 允许超过 20 条（默认上限 5000，见 `index_jobs.max_urls`）：

```json
{ "urls": ["https://www.onlinedown.net/article/10048973.html", "…共 800 条"], "engines": ["baidu"] }
```

**202 Accepted**

```json
{
  "job_id": "job_20261124T054613_379221272c",
  "status": "queued",
  "total": 30,
  "engines": ["baidu"],
  "created_at": "2026-11-24T05:46:13Z",
  "poll_url": "/web-tool/v1/index/check/jobs/job_20261124T054613_379221272c",
  "results_url": "/web-tool/v1/index/check/jobs/job_20261124T054613_379221272c/results",
  "csv_url": "/web-tool/v1/index/check/jobs/job_20261124T054613_379221272c/results?format=csv",
  "note": "queued; the server drains this batch at the engine's safe pace. Poll poll_url for progress."
}
```

服务端按引擎实测节奏串行执行（百度 12s/条），遇到验证码冷却会**等冷却结束再继续**（`status:"waiting"` + `wait_seconds`），不会把剩余 URL 全判成失败；被冷却/验证码挡住的单条会重排（`max_unit_retries`）。

**GET `/v1/index/check/jobs/{id}`** — 进度

```json
{
  "job_id": "job_…", "status": "running",
  "progress": 0.66, "total": 30, "completed": 20,
  "indexed": 12, "not_indexed": 8, "failed": 0, "retrying": 0,
  "eta_seconds": 120, "interval_ms": 12000,
  "engines": ["baidu"], "wait_seconds": 0,
  "created_at": "…", "updated_at": "…", "finished_at": null,
  "poll_url": "…", "results_url": "…", "csv_url": "…"
}
```

`status`：`queued` → `running` →（冷却时 `waiting`）→ `done` | `cancelled` | `failed`。

**GET `/v1/index/check/jobs/{id}/results`** — 结果

- 默认 JSON：`{"job_id","status","total","offset","count","results":[…]}`，`results` 与 2.1 的行结构完全一致，支持 `?offset=&limit=` 分页。
- `?format=csv`：直接下载 Excel 可打开的 CSV（UTF-8 BOM + CRLF），列形如 `url,baidu_indexed,baidu_match,baidu_matched_result_url,baidu_total_results,baidu_error`。

**GET `/v1/index/check/jobs`** — 最近任务（`?limit=` 默认 50，最大 500）

```json
{ "count": 2, "jobs": [ { "job_id": "job_…", "status": "running", "progress": 0.66, … } ] }
```

**DELETE `/v1/index/check/jobs/{id}`** — 取消；成功返回该任务的进度对象（`status:"cancelled"`），已结束的任务返回 409 `conflict`，未知 id 返回 404 `not_found`。

其它约定：

- 计费：202 时按提交的 `urls × engines` 计 units（服务端确实会消耗这么多引擎调用）；查询任务进度/结果、取消均不计费。
- 断点续跑：任务状态与逐条结果落在 `index_jobs.dir`（默认 `<platform.data_dir>/index_jobs`），进程重启后自动续跑，已完成的 URL 不会重复查询。
- 同一时刻只跑一个任务（引擎按出口 IP 限速，并发只会互相抢令牌）；其余任务 FIFO 排队。
- 单个 URL 连续被冷却/验证码挡住时，最多重排 `index_jobs.max_unit_retries` 次（默认 6）；用尽后该条以 `error` 计入 `failed`，任务继续。
- 控制台在线调试（`POST /api/playground/check`）仍是 **1–20 条同步**，不返回任务号——它的界面只渲染同步信封。

### 2.3 GET /web-tool/v1/quota

查询当前 key 的配额：

```bash
curl {BASE}/v1/quota -H "X-API-Key: sk-w2a-3f9c…"
```

```json
{
  "key_id": "35b1b68658b1dda8",
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
curl "{BASE}/baidu/search?text=golang%20%E5%B9%B6%E5%8F%91%E6%A8%A1%E5%9E%8B&limit=5" \
  -H "X-API-Key: <YOUR_API_KEY>"
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
| `site` / `file` / 日期参数等 | 引擎语法透传，详见 [ARCHITECTURE.md](ARCHITECTURE.md)（HTTP 查询参数名为 `file`，`filetype` 是内部字段名） |

**鉴权与计费**：该组端点由核心管道直接挂载，**不计费**。鉴权随 `security.data_plane_auth` 开关：本仓库 config.yaml 模板默认 `true`，`/{engine}/search` 与 `/stats/*` 需要 `X-API-Key`（兼容 `Authorization: Bearer <密钥本体>`），缺失或无效返回 401；置 `false` 时匿名可用（启动日志会给出安全告警）。无论开关如何，都受引擎限速、熔断、冷却（见 §8）与 CORS 保护，请不要将其当作公网匿名接口暴露。

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

三个入口，全部挂在 `/web-tool` 之下，是本平台供 AI Agent 直接调用的接口：

| 入口 | 用途 |
| --- | --- |
| `POST /web-tool/mcp` | MCP (Model Context Protocol) streamable HTTP 端点：Claude / Claude Desktop / Claude Code / 自研 Agent 直接把本平台当工具集调用 |
| `GET /web-tool/openapi.yaml` | OpenAPI 3.1 全端点规范（静态文件，`go:embed` 内嵌）：Dify / n8n / LangChain 导入即用 |
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
curl -X POST {BASE}/mcp \
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
curl -X POST {BASE}/mcp \
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
curl -X POST {BASE}/mcp \
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
        "description": "批量判定 URL 是否被国内引擎收录（每个 URL × 引擎一个结论）；超过 20 条自动转异步任务",
        "inputSchema": {
          "type": "object",
          "properties": {
            "urls": { "type": "array", "items": { "type": "string" }, "minItems": 1, "maxItems": 5000, "description": "目标 URL 列表（>20 条转异步任务，返回 job_id）" },
            "engines": { "type": "array", "items": { "type": "string", "enum": ["baidu", "sogou", "so360", "bing"] }, "description": "引擎列表，缺省为全部" }
          },
          "required": ["urls"]
        }
      },
      {
        "name": "get_index_job",
        "description": "查询异步收录任务的进度；include_results=true 时附带结果行",
        "inputSchema": {
          "type": "object",
          "properties": {
            "job_id": { "type": "string", "description": "check_indexed 返回的 job_id" },
            "include_results": { "type": "boolean", "description": "true = 一并返回结果行" },
            "offset": { "type": "integer", "description": "结果行起点" },
            "limit": { "type": "integer", "description": "返回多少行，缺省全部" }
          },
          "required": ["job_id"]
        }
      },
      {
        "name": "cancel_index_job",
        "description": "取消排队中或运行中的收录任务",
        "inputSchema": {
          "type": "object",
          "properties": {
            "job_id": { "type": "string", "description": "check_indexed 返回的 job_id" }
          },
          "required": ["job_id"]
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
curl -X POST {BASE}/mcp \
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

计费：**每次调用计 1 unit**。

#### tools/call —— check_indexed

`{"name":"check_indexed","arguments":{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}}` → 输出结构与 `/web-tool/v1/index/check` 的 `results` 完全一致（§2.1）。计费：**len(urls) × len(engines) units**。

`urls` 超过 20 条且部署开启了 `index_jobs` 时，工具不再报“最多 20 条”，而是把整批放进服务端队列并立即返回：

```json
{ "job_id": "job_20261124T054613_379221272c", "status": "queued", "total": 800,
  "engines": ["baidu"], "created_at": "…", "note": "queued; … Call get_index_job …" }
```

计费仍按提交条数 `len(urls) × len(engines)`（服务端确实会执行这么多引擎调用）。

#### tools/call —— get_index_job / cancel_index_job

- `get_index_job{"job_id":"…","include_results":true,"offset":0,"limit":200}` → 默认返回进度（`status`/`progress`/`completed`/`indexed`/`not_indexed`/`failed`/`wait_seconds`/`eta_seconds`）；`include_results=true` 时附带 `{job_id,offset,count,results:[…]}`，行结构与 §2.1 一致。
- `cancel_index_job{"job_id":"…"}` → 取消排队中/运行中的任务；已结束的任务返回工具错误 `isError:true`。
- 二者均**不计费**；任务本身按 §2.2b 的节奏在服务端推进（遇到验证码冷却会等待，不会把剩余 URL 判失败）。

#### tools/call —— get_quota 与 list_engines

`get_quota` → §2.3 的配额对象；`list_engines` → 引擎清单与冷却/熔断状态（§2.4 数据源 + §6.7 冷却字段）。均不计费。

#### 其他方法与错误

- `ping` → `{"jsonrpc":"2.0","id":…,"result":{}}`。
- 未知方法 → JSON-RPC `-32601`；JSON 解析失败 → `-32700`；参数非法 → `-32602`。
- 引擎冷却期调用 `web_search` / `check_indexed`：请求本身成功（HTTP 200），工具以 `isError: true` 返回，`content[0].text` 携带 §1.3 冷却信封 JSON（含 `retry_after_seconds`）。
- 密码修改后旧 JWT 立即失效（见 §6.9），MCP 会话随之失效，需重新握手。

### 5.2 GET /web-tool/openapi.yaml

OpenAPI 3.1 规范，覆盖 `/v1/*`、`/{engine}/search`、`/web-tool/mcp` 与健康观测端点；控制平面 `/api/*` 按安全约定不在公开规范中枚举，端点形状见 §6。Dify（自定义工具 → 导入 OpenAPI）、n8n（HTTP Request 节点 → 导入 cURL/OpenAPI）、LangChain（OpenAPI toolkit）均以它为通用入口；鉴权统一配 **Header 型 API Key**，Header 名 `X-API-Key`（n8n 选 "Header Auth"，Dify 选 API Key + 自定义 Header 名）。导入后逐接口的调用示例见 [AGENT-README.md](AGENT-README.md)。

### 5.3 GET /web-tool/llms.txt

Agent 友好的能力清单（平台简介、端点、鉴权约定、四引擎语义）。LLM/Agent 可先抓取该文件决定如何调用。

---

## 6. 控制平面 `/api/*`（管理 API，JWT）

鉴权：除登录外一律 `Authorization: Bearer <JWT>`；缺失/过期/无效 → `401 {"error":"unauthorized"}`。修改密码会使全部旧 token 立即失效（见 §6.9）。管理 API 默认全局限速 **60 req/min**（`security.admin_rate_per_minute`），超限 `429 {"error":"rate_limited"}` + `Retry-After`。

### 6.1 POST /web-tool/api/auth/login

```bash
curl -X POST {BASE}/api/auth/login \
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
  "id": "35b1b68658b1dda8",
  "name": "ci-robot",
  "prefix": "sk-w2a-3f9c",
  "status": "active",
  "daily_quota": 500,
  "used_today": 0,
  "used_total": 0,
  "created_at": "2026-11-15T08:00:00Z",
  "last_used_at": null,
  "key": "<YOUR_API_KEY>"
}
```

完整密钥形如 `sk-w2a-<32位hex>`，仅创建响应返回一次，请立即保存；服务端只存 SHA-256 哈希，**列表中不再出现**。禁用（`status:"disabled"`）后的 key 调用公网 API 一律 401。

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
      "key_id": "35b1b68658b1dda8",
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
curl -X POST {BASE}/api/engines/baidu/cooldown/reset \
  -H "Authorization: Bearer $TOKEN"
# → {"ok":true,"engine":"baidu"}

# 强制冷却 N 分钟（维护窗口内阻止某引擎接客）
curl -X POST {BASE}/api/engines/baidu/cooldown/force \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"minutes":30}'
# → {"ok":true,"engine":"baidu","minutes":30}
```

未知引擎名或非法 `minutes` → 400 `{"error":"bad_request","code":"INVALID_PARAM","reason":"INVALID_PARAM"}`；装配未注入 EngineControl（降级态）→ 503 `{"error":"engine_control_unavailable"}`。

### 6.9 POST /web-tool/api/auth/password

修改管理员密码：

```bash
curl -X POST {BASE}/api/auth/password \
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
curl -X POST {BASE}/api/playground/check \
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
| 429 | `quota_exceeded` | 当日配额用尽（预检拒绝，不扣费） | 有 |
| 429 | `too_many_attempts` | 登录连续失败锁定（默认 5 次失败锁 15 分钟，体内含 `retry_after_seconds`） | 有 |
| 429 | `rate_limited` | 管理端点超过 60 req/min（默认值） | 有 |
| 500 | `server_error` | 存储等内部错误，`message` 附原因 | 无 |

### 7.2 核心信封错误（`/{engine}/search` 与 `/web-tool/v1/index/check` 的引擎级单元；`code` 为整型 HTTP 状态码）

| `error` | HTTP | 场景 |
| --- | --- | --- |
| `blocked` | 403 | 引擎反爬拦截（浏览器验证/风控页） |
| `blocked` | 429 | 引擎返回 HTTP 429（`meta.upstream_status=429`） |
| `captcha_detected` | 429 | 引擎弹出图形验证码（同时触发该引擎首档冷却，默认 10 分钟，百度 2 分钟，见 §8.1） |
| `circuit_open` | 503 | 该引擎熔断器处于 open（连续失败达阈值） |
| `engine_cooldown` | 503 | 该引擎处于冷却期。两种出现方式：整请求 503（所选引擎全部冷却，§1.3 信封带 `meta.engines_cooldown`）；或批量判定 200 信封内的**单元级**错误 `{"error":{"code":"engine_cooldown","message":…,"retry_after_seconds":N}}`（部分引擎冷却时其余单元照常执行） |
| `search_timeout` | 504 | 搜索超时 |
| `proxy_auth` / `proxy_connect` / `proxy_timeout` / `proxy_unavailable` | 503 | 代理链路失败 |
| `parser_failure` | 502 | SERP 解析失败 |
| `engine_internal` | 502 | 引擎内部错误（未分类兜底） |
| `all_engines_failed` | 502 | 多引擎全失败（endpoint fallback 场景） |
| `not_implemented` | 501 | 该引擎不支持该能力（例：`raw_requests=true` 时所有引擎不支持收录判定——每个单元返回此错误） |
| `request_timeout` | 504 | 请求级超时（context deadline） |
| `engine_queue_exceeded` | 504 | 该单元在引擎限流队列中等待超出请求预算，**未发起真实查询**即失败（批量限流排队时间随单元数线性增长，如百度 5 次/分钟 → 每令牌 12 秒；服务端会按批量规模动态放宽请求预算，上限 12 分钟，见 §2.1） |
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
| `TOO_MANY_URLS` | 收录判定 urls 超过 20 条（未开启任务队列时）或超过 `index_jobs.max_urls` |

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
- **按引擎覆盖**：`stability.engine_cooldown.per_engine.<engine>.steps_minutes` 可单独给某个引擎换档位。本仓库给百度配的是 **[2, 10, 30]**（实测单次验证码停 ~2 分钟即可恢复，10 分钟首档会明显拖低吞吐）。
- **计数归零**：连续无拦截达 **240 分钟**（`stability.engine_cooldown.reset_after_minutes`）后，连续计数归零重新从第 1 档起算。
- **冷却期内**：该引擎所有 search/check 快速失败（503 冷却信封），**不消耗真实引擎请求、不消耗代理与浏览器资源**。
- **到期探测**：冷却到期后放行一个探测请求——成功即恢复 `ok`；失败则进入下一档冷却。
- **行为级提示**：浏览器验证码页（无论人工还是自动触发）都会被判定为拦截并启动首档冷却（默认 10 分钟；百度 2 分钟，见 §8.1）。这是以牺牲短时可用性换取单出口 IP 存活率的刻意设计；高频使用方应配置代理池（[CONFIGURATION.md](CONFIGURATION.md) `proxies` 节）。
- 运维干预：`POST /web-tool/api/engines/{name}/cooldown/reset`（强制恢复）、`POST /web-tool/api/engines/{name}/cooldown/force {"minutes":N}`（强制冷却），见 §6.8；控制台「引擎状态」页（`#/engines`）提供同能力。

熔断器（`circuit_breaker.{failures,recovery_seconds,successes}`）与冷却状态机是**两套独立机制**：熔断针对连续请求失败（技术性失败），冷却针对反爬拦截（blocked/captcha）。两者同时生效时，冷却检查在前（不发真实请求）。

---

## 9. 计费口径

- 口径：**1 URL × 1 引擎 = 1 unit**。`/web-tool/v1/index/check` 计 `len(urls) × len(engines)` units（>20 条转异步任务时，按提交条数在 202 当次计费）；MCP `web_search` 每次调用计 1 unit；MCP `check_indexed` 计 `len(urls) × len(engines)` units。`/web-tool/v1/quota`、`/web-tool/v1/engines`、任务进度/结果下载（`GET /v1/index/check/jobs…`）、MCP `get_index_job`/`cancel_index_job`/`get_quota`/`list_engines`、管理 API、`/web-tool/api/playground/check` 不计费。
- **扣费时点**：请求以 **HTTP 200 完成**即扣费，但按响应信封**逐引擎核对，仅成功单元计费**——200 信封内引擎级失败的单元（`results[].engines.<engine>.error`，含 `engine_cooldown` 冷却、`circuit_open` 熔断等）**不计费**。响应信封无法解析时按请求单元全额计费（兜底口径）。**202 入队的批量按提交单元全额计费**（服务端确实会执行这么多次引擎调用；单条失败由任务重试机制消化，不退回）。4xx（401/400）与配额预检 429、冷却 503 **不扣费**。MCP 平面与 `/v1` 同口径：`check_indexed` 的工具结果（`result.content[].text`）携带逐引擎明细时按成功单元计费；明细不可解析（`web_search` 结果、工具级错误信封、**入队回执**）时按请求单元全额计费。
- **用量日志如实化（B-01）**：被拒绝的请求（400 参数错误、401/403、429 配额与限速、503 冷却/熔断快速失败）同样写入用量日志，但 `status` 记录真实终态码、`units=0`（计 0 unit），并携带 `outcome` 字段：`billed`（200 完成）/ `engine_failed`（5xx 引擎失败）/ `rejected`（未进入业务逻辑）。MCP 计费记录的 `outcome` 与 `/v1` 同规则落账。S4 之前的存量记录无 `outcome` 字段，按 reached 计。
- **管理统计口径（P-01）**：`GET /api/stats/overview` 的 `today_calls`/`total_calls`/`success_rate_24h` 统计**有效调用**（到达业务逻辑的 billed+engine_failed），参数错误、配额与限速拒绝、冷却拒绝不计入；`today_calls_all`（今日全部请求）与 `rejected_today_24h`（24h 内拒绝数）为 additive 字段。口径变化在发布说明中说明,历史记录无法回填。
- 对账：`GET /api/reconcile` 按日核对「落账次数」与「逐引擎成功单元数」；携带逐引擎明细的记录逐条核对，无明细的记录（MCP `web_search`、信封解析失败的存量数据）按落账口径计入、不判 mismatch。
- 对计费敏感的批量任务建议：先小批量探测引擎健康度（`GET /web-tool/v1/engines` / `list_engines`），避开冷却与熔断中的引擎，再放大批量——冷却/熔断单元虽不计费，但整请求会因此缩水。
- 配额维度：`daily_quota`（本地日重置，0 = 不限）与 `used_total`（累计）；用量日志（`/web-tool/api/usage`）逐笔记录计费 unit 数（units）、HTTP 状态与各引擎成功/失败数。
- **配额硬上限语义（B-15）**：配额为「预检-事后扣减」两段式——并发在途请求可轻微超出 `daily_quota`（每请求最多超额「在途次数 − 1」）；扣减在 bbolt 单事务内完成,**无丢账**。严格 Reservation 语义（先记账后执行、失败回滚）未实现;对账接口 `GET /api/reconcile` 可核对扣费与流水差异。
- 免费额度、QPS 分级等商用配额策略当前未实现；自托管场景配额即自我约束工具。

---

## 附：端点速查

```
数据平面（X-API-Key）
  POST /web-tool/v1/index/check         批量收录判定（≤20 条同步；>20 条转异步任务，202）
  GET  /web-tool/v1/index/check         单 URL 收录判定（?url=&engines=）
  GET  /web-tool/v1/index/check/jobs    异步任务列表（?limit=）
  GET  /web-tool/v1/index/check/jobs/:id          任务进度（status/progress/eta_seconds）
  GET  /web-tool/v1/index/check/jobs/:id/results  任务结果（?offset=&limit=；?format=csv 下载）
  DELETE /web-tool/v1/index/check/jobs/:id        取消任务
  GET  /web-tool/v1/quota               当前 key 配额
  GET  /web-tool/v1/engines             引擎与熔断状态

搜索（鉴权随 `security.data_plane_auth`：模板默认需 X-API-Key；不计费）
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
  GET    /web-tool/api/usage/export?format=csv|json&key_id=&from=&to=  用量导出下载（S4。CSV 表头固定为 seq,time,key_id,key_name,endpoint,engines,urls,units,status,took_ms，units = 计费 unit 数，1 URL × 1 引擎 = 1 unit）
  GET    /web-tool/api/reconcile?date=YYYY-MM-DD                       扣费-流水对账（S4）
  GET    /web-tool/api/engines                                         引擎状态机（含冷却）
  POST   /web-tool/api/engines/:name/cooldown/reset                    强制恢复
  POST   /web-tool/api/engines/:name/cooldown/force                    强制冷却 {"minutes":N}
  GET    /web-tool/api/sessions                                        管理员会话列表（S4）
  DELETE /web-tool/api/sessions/:id                                    撤销会话（撤销当前会话=退出, S4）
  GET    /web-tool/api/audit?limit=&offset=                            安全审计轨迹（S4）
  GET    /web-tool/api/admin/backup                                    下载数据一致性快照（S4）
  POST   /web-tool/api/playground/check                                控制台在线调试（不计费；仍是 1–20 条同步，不走任务队列）

健康与观测
  GET  /web-tool/health  /web-tool/ready  /web-tool/stats
  GET  /web-tool/stats/cache  /web-tool/stats/proxy  /web-tool/stats/cb
  GET  /web-tool/metrics                        OpenMetrics 指标（security.metrics_token Bearer 或仅 loopback, S4）

CLI（停服态, S4）
  web2api admin-backup   --out <file>     备份 bbolt 快照
  web2api admin-restore  --in <file>      校验并恢复快照
  web2api admin-reset                     停服重置管理员密码
```
