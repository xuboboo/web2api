# Web2API API 参考

Web2API 查询指定网页能否在百度、搜狗、360 搜索和必应中检索到。REST API 支持单条查询、同步批量查询和异步批量任务。

## 第一次调用

在管理控制台的「API 密钥」页创建密钥并保存完整值。以下命令适用于 Bash，需安装 curl。示例使用本机地址，远程部署时把地址换成你的服务地址并保留 `/web-tool`。

```bash
export W2A_BASE='http://127.0.0.1:7000/web-tool'
read -rsp 'Web2API API Key: ' W2A_KEY; printf '\n'
export W2A_KEY

# 验证密钥和配额。这一步不会访问百度。
curl -sS -i "$W2A_BASE/v1/quota" -H "X-API-Key: $W2A_KEY"

# 查询一条 URL，只选百度。
curl -sS -i --max-time 60 "$W2A_BASE/v1/index/check" \
  -H "X-API-Key: $W2A_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"urls":["https://www.onlinedown.net/article/10049071.htm"],"engines":["baidu"]}'
```

收到 `200` 后，查看 `results[0].engines.baidu`。有 `error` 表示查询失败。有 `indexed` 表示查询成功，`true` 为命中，`false` 为本次未命中。

Windows 上也可以用 PowerShell 7.2 或更新版本执行。

```powershell
$env:W2A_BASE = 'http://127.0.0.1:7000/web-tool'
$env:W2A_KEY = Read-Host -Prompt 'Web2API API Key' -MaskInput
$headers = @{ 'X-API-Key' = $env:W2A_KEY }
Invoke-RestMethod "$env:W2A_BASE/v1/quota" -Headers $headers

$body = @{
    urls = @('https://www.onlinedown.net/article/10049071.htm')
    engines = @('baidu')
} | ConvertTo-Json -Compress
$result = Invoke-RestMethod "$env:W2A_BASE/v1/index/check" -Method Post `
    -Headers $headers -ContentType 'application/json' -Body $body -TimeoutSec 60
$result.results[0].engines.baidu
```

| 要做什么 | 调用方式 | 拿到什么 |
| --- | --- | --- |
| 查询 1–20 条 URL | [POST /v1/index/check](#21-post-web-toolv1indexcheck) | `200`，整批完成后一次返回 JSON |
| 查询 21 条及以上 | [POST /v1/index/check](#22b-批量提交异步任务)，需开启任务队列 | `202`，用回执中的链接查进度、下载结果 |
| 查看引擎是否冷却 | `GET /web-tool/engines/status`，无需鉴权 | 当前状态和冷却结束时间，不发起搜索 |
| 查看密钥剩余配额 | [GET /v1/quota](#23-get-web-toolv1quota) | 当日上限和已用量，不扣查询额度 |

**只查百度时请显式传 `"engines":["baidu"]`。** 省略引擎会使用全部可用引擎。同步查询 20 条百度 URL 通常需约 4 分钟，客户端和反向代理都要允许连接持续到整批结束。

也可以通过 API 创建密钥、按其他语言接入或导入机器可读规范。 [接入教程](QUICKSTART.md) · [OpenAPI 3.1](../mcp/openapi.yaml) · [MCP 接入指南](AGENT-README.md) · [配置](CONFIGURATION.md) · [部署](DEPLOYMENT.md)

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
| 时间戳 | 任务与查询时间使用 RFC3339。公开状态页的 `generated_at` 使用服务器本地时间，格式为 `YYYY-MM-DD HH:MM:SS` |
| 请求 ID | 每请求生成（可由 `X-Request-ID` 请求头带入），回显在响应头与多数信封的 `request_id` 字段 |
| 鉴权 | 公网 API：`X-API-Key`（兼容 `Authorization: Bearer <key>`）；管理 API：`Authorization: Bearer <JWT>`；MCP：两者皆可（见 §5） |
| 引擎名 | `baidu` / `sogou` / `so360` / `bing`；`360` 是 `so360` 的别名，响应中一律归一化为 `so360` |
| 机器可读文档 | `GET /web-tool/openapi.yaml` 提供公开 REST 和 MCP 规范，`GET /web-tool/llms.txt` 提供 Agent 接入说明 |

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
| 403 | `forbidden` | 无（来源 IP 不符合密钥访问限制） |
| 403 | `forbidden` + `"code":"insufficient_role"` | 角色不足：`{"error":"forbidden","code":"insufficient_role"}`（§6） |
| 403 | `forbidden` + `"reason":"blacklisted"` | IP/CIDR 黑名单命中：`{"error":"forbidden","reason":"blacklisted"}`（§6.12） |
| 404 | `not_found` | 无 |
| 429 | `quota_exceeded` | `retry_after_seconds`（响应亦带 `Retry-After` 头） |
| 429 | `too_many_attempts` | `retry_after_seconds`（登录防爆破锁定；响应亦带 `Retry-After` 头） |
| 429 | `rate_limited` | 接口访问超限，含 `retry_after_seconds` 与 `Retry-After` |
| 500 | `server_error` | 仅含 `request_id`（取自 X-Request-ID），不含 `message`；原因只写服务端日志 |

### 1.3 冷却信封（stability，契约 503）

请求单个搜索引擎时，如果该引擎已冷却，服务直接返回 503。同步收录批次在开始时发现所选引擎全部冷却，也会直接返回 503。以下为关键字段：

```json
{"error": "engine_cooldown", "reason": "COOLDOWN", "retry_after_seconds": 600}
```

HTTP 503，响应带 `Retry-After` 头（与 `retry_after_seconds` 同值）。实际响应体还会携带核心信封惯例的 `request_id` 与 `meta`（含 `engine`、`until`、以及完整 `engines_cooldown` 快照，见 §2.1）。信封顶层 `reason` 恒为 `COOLDOWN`；自然冷却与运维强制冷却（cooldown/force）的区分体现在 `meta.engines_cooldown[].reason`（`COOLDOWN` / `FORCED`）与管理端点状态里。`retry_after_seconds` 为距冷却解除的秒数。

> 客户端先判断 HTTP 状态，再用顶层 `error` 和可选的 `reason` 分支。一个端点可返回不同的错误形状，例如收录请求的鉴权错误来自平台，参数校验则来自核心。同步返回 200 时，再检查每个引擎结果内的 `error.code`。

---

## 2. 数据平面 `/v1/*`（公网 API，X-API-Key）

使用 `X-API-Key: sk-w2a-…`，也可使用 `Authorization: Bearer sk-w2a-…`。密钥缺失、无效或已禁用时返回 `401 unauthorized`。来源 IP 不在该密钥允许范围内时返回 `403 forbidden`。日配额检查会加上在途预留和本次请求单元数。超限返回 `429 quota_exceeded`，本次不扣费。将日配额设为 `0` 表示不限量。

计费口径：**1 URL × 1 引擎 = 1 unit**。

### 2.1 POST /web-tool/v1/index/check

批量收录判定：每个 (URL × 引擎) 单元在引擎内检索前 10 条结果并做归一化匹配。

**请求体**

```json
{
  "urls": ["https://example.com/post-1"],
  "engines": ["baidu", "sogou"]
}
```

- `urls`：1 条以上，上限为 `index_jobs.max_urls`（默认 5000）。
  - **≤ 20 条**：同步执行。服务端等整批完成后一次性返回 JSON，连接期间没有逐条响应或进度事件。同一引擎按 URL 顺序逐条查询，每批在该引擎上同时只跑一条。选择多个引擎时，引擎之间可以并行。
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
    "units": 2,
    "engines_cooldown": [
      { "engine": "bing", "until": "2026-11-15T08:10:00Z", "reason": "COOLDOWN" }
    ]
  }
}
```

字段说明：

- 成功单元：`indexed`（是否收录）、`match`（仅 `exact` / `none` 两种取值）、`matched_result_url`（命中的候选地址，`match=none` 时字段存在但为空串 `""`，解析方按空串判断即可）、`total_results`（SERP 内 organic 条数）、`took_ms`（该单元耗时）。
- 百度默认用页面标题检索，并在返回的候选结果里按标题相似度和可注册域名匹配。标题抓取失败时回退到 URL 短语检索。命中地址可能使用同站的移动子域。百度改写标题、命中结果排名靠后时，本次可能返回 `false`。`indexed` 表示本次检索的匹配结果，官方收录状态以搜索引擎站长平台为准。
- 失败单元：`error.code` / `error.message`，错误码与搜索端点同表（见 §7.2），例如 `blocked`、`captcha_detected`、`circuit_open`、`not_implemented`。
- `meta.units = len(urls) × len(engines)`，即本次请求的 unit 总数（计费口径为**实扣**：带 `error` 的单元不扣费）。
- **批量超时预算**：百度默认 `5 次/分钟`、突发 `1`。首个令牌就绪时，首条可立即开始。后续查询通常约每 12 秒放行一条，20 条大约需要 4 分钟，实际还取决于引擎响应和重试。服务端按去重后的 URL 数量放宽基础预算：`基础预算 + (URL 数 × 最大尝试次数 − 1) × 最慢所选引擎的令牌间隔`，批量预算扩展上限 12 分钟。因预算不足而无法获准发送的单元可能返回 `engine_queue_exceeded`（§7.2），该单元不扣费。同步调用的客户端和反向代理需要允许连接等待整批结束。
- 超过 20 条的批量不要靠提高预算硬撑，走 §2.2b 的异步任务：同样的节奏，但不受单次 HTTP 连接寿命限制。
- `meta.engines_cooldown`：响应生成时观测到的冷却状态，元素为 `{"engine","until","reason"}`，原因包括 `COOLDOWN`（自然冷却）或 `FORCED`（运维强制冷却）。若同步批次开始时所选引擎全部在冷却，请求立即返回 503（§1.3）。若只有部分引擎在冷却，或执行过程中触发冷却，批次仍返回 200。冷却期间跳过的单元包含 `error.code=engine_cooldown` 和 `error.retry_after_seconds`，其他单元保留各自结果。异步批次的等待规则见 §2.2b。

**本机实测**（2026-11-25）：一个请求提交 20 条 URL，仅选百度。230 秒后一次收到 20 条结果，5 条命中、15 条未命中、0 条报错，实扣 20 个单元。批次结束后引擎仍为 `ok`，无额外冷却。

**错误响应**

| HTTP | 信封 | 场景 |
| --- | --- | --- |
| 200 | 见上 | ≤ 20 条，同步结果 |
| 202 | `{"job_id",…}`（§2.2b） | > 20 条且已开启任务队列，已入队 |
| 400 | `{"error":"bad_request",…,"reason":"EMPTY_QUERY"}` | `urls` 为空 |
| 400 | `{"error":"bad_request",…,"reason":"TOO_MANY_URLS"}` | 超过 20 条但**未开启任务队列**，或超过 `index_jobs.max_urls` |
| 400 | `{"error":"bad_request",…,"reason":"INVALID_PARAM"}` | 含未知引擎、非法 URL 等 |
| 401 | `{"error":"unauthorized"}` | key 缺失 / 未知 / 禁用 |
| 403 | `{"error":"forbidden"}` | 来源 IP 不符合密钥访问限制 |
| 429 | `{"error":"quota_exceeded"}`（`Retry-After` 头） | 当日配额用尽 |
| 429 | `{"error":"rate_limited"}`（`Retry-After` 头） | 接口访问频率超过限额 |
| 503 | `{"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":600}`（`Retry-After` 头） | 所选引擎全部处于冷却期 |
| 503 | `job_queue_full`（`reason=JOB_QUEUE_FULL`，带 `Retry-After`） | 异步任务队列已满，本次未入队、未计费。MCP 以 HTTP 503 返回 JSON-RPC 错误，同样可退避后重试 |
| 503 | `server_draining`（`reason=SERVER_DRAINING`，带 `Retry-After`） | 服务进入停机排水，暂停接收新异步任务。REST 和 MCP 本次均未入队、未计费 |

**`urls` 的去重与校验规则**

1. 同步按归一化后的 URL 去重，并保留首次出现顺序。例如 `https://a.com/x` 与 `HTTPS://A.COM/x/` 在同步结果中合为一行。异步按去掉首尾空白后的 URL 字符串去重。
2. 只接受 HTTP(S)。`ftp:`、`file:`、`javascript:`、`data:`、`mailto:` 返回 `400 INVALID_PARAM`。
3. URL 必须含有效的公网域名。单标签主机（如 `localhost`、`intranet`）返回 `400 INVALID_PARAM`，裸域名（如 `example.com`）按 HTTP 处理。
4. 同步与异步的 20 条分界按提交的原始条数判断，异步任务上限按去除完全重复项后的条数判断。配额预检仍按提交条数估算，建议调用方在提交前去重。

参数校验错误遵循 §1.1 的核心格式。客户端可用 `reason` 判断具体参数问题。

```json
{"error":"bad_request","code":400,"request_id":"01a1db49-…","message":"TOO_MANY_URLS: urls accepts at most 5000 entries per job, got 6000","reason":"TOO_MANY_URLS"}
```

以下服务端 JavaScript 示例使用「第一次调用」中设置的环境变量。保存为 `check-index.mjs`，用支持 `fetch` 的 Node.js 运行。

```js
const { W2A_BASE, W2A_KEY } = process.env;
const res = await fetch(`${W2A_BASE}/v1/index/check`, {
  method: 'POST',
  headers: { 'X-API-Key': W2A_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({
    urls: ['https://www.onlinedown.net/article/10049071.htm'],
    engines: ['baidu'],
  }),
});
const data = await res.json();
if (res.status !== 200) {
  const retryAfter = res.headers.get('Retry-After');
  throw new Error(`HTTP ${res.status} ${data.error} ${data.reason ?? ''} (Retry-After: ${retryAfter ?? '无'})`);
}
for (const row of data.results) {
  const baidu = row.engines.baidu;
  console.log(row.url, baidu.error ? `查询失败 ${baidu.error.code}` : baidu.indexed ? '命中' : '未命中');
}
```

### 2.2 GET /web-tool/v1/index/check

单 URL 便捷版，查询参数传值，响应结构同上：

```bash
curl "{BASE}/v1/index/check?url=https%3A%2F%2Fexample.com%2F&engines=360" \
  -H "X-API-Key: sk-w2a-3f9c…"
```

- `url`：必填（URL 编码）；`engines`：可选，逗号分隔，别名 `360` 可用。
- 请求单元数为 `1 × len(engines)`（省略时使用所有可用引擎），实际仅扣成功单元。

### 2.2b 批量提交（异步任务）

向同一个 `POST /web-tool/v1/index/check` 提交超过 20 条 URL。任务队列开启时，接口返回 `202`，查询在后台继续执行。默认上限为 5000 条，由 `index_jobs.max_urls` 控制。保存回执中的 `job_id` 和链接，后续查询使用提交任务时的 API Key。返回的链接已含 `/web-tool`，拼接时只加服务的协议和主机名。

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

后台每次处理一条 URL。同一条的多个引擎可并行。所选引擎全部冷却时，任务进入 `waiting`，用 `wait_seconds` 查看当前等待时间。某条 URL 的所有引擎均以可重试错误结束时，整条会重新排队。仅部分引擎失败时，保留该行的成功和失败结果。

**GET `/v1/index/check/jobs/{id}`** — 进度

```json
{
  "job_id": "job_…", "status": "running",
  "progress": 0.6666666666666666, "total": 30, "completed": 20,
  "indexed": 12, "not_indexed": 8, "failed": 0, "retrying": 0,
  "eta_seconds": 120, "interval_ms": 12000, "engines": ["baidu"],
  "created_at": "2026-11-24T05:46:13Z", "updated_at": "2026-11-24T05:50:13Z",
  "poll_url": "…", "results_url": "…", "csv_url": "…"
}
```

| 字段 | 含义 |
| --- | --- |
| `status` | `queued` 排队、`running` 执行、`waiting` 等待。终态为 `done`、`cancelled`、`failed` |
| `total` / `completed` | 去重后的 URL 总数和已完成行数（包含失败行）。显示精确比例时用 `completed / total` |
| `indexed` | 至少一个引擎命中的行数 |
| `not_indexed` | 至少一个引擎完成查询，且没有命中的行数 |
| `failed` | 所有引擎都失败的行数 |
| `retrying` | 等待重试的行数 |
| `progress` / `wait_seconds` / `eta_seconds` | 服务端报告的进度、当前等待秒数、预计剩余秒数。等待时间和 ETA 字段可能省略，是否结束以 `status` 为准 |

即使任务状态是 `done`，也应检查每行的引擎结果。存在部分引擎失败时，`failed` 行数可以为 0。

**GET `/v1/index/check/jobs/{id}/results`** — 结果

- 默认 JSON 含 `job_id`、`status`、`total`、`offset`、`count`、`results`。目前只返回已落盘的结果，可在任务运行中读取。用 `offset`（从 0 开始）与 `limit` 分页，向下翻页时按实际 `count` 增加偏移。按 `url` 关联提交记录，重试后的写入顺序可能变化。
- `?format=csv`：直接下载 Excel 可打开的 CSV（UTF-8 BOM + CRLF），列形如 `url,baidu_indexed,baidu_match,baidu_matched_result_url,baidu_total_results,baidu_error`。

**GET `/v1/index/check/jobs`** — 最近任务（`?limit=` 默认 50，最大 500）

响应中的 `jobs` 数组由进度对象组成，按创建时间倒序排列。`count` 是本次返回的任务数。没有任务时返回 `{"count":0,"jobs":[]}`。

**DELETE `/v1/index/check/jobs/{id}`** — 取消；成功返回该任务的进度对象（`status:"cancelled"`），已结束的任务返回 409 `conflict`，未知 id 返回 404 `not_found`。

其它约定：

- 计费在返回 202 时完成，按提交的 URL 条数 × 计费引擎数扣费，重复提交项也计入。回执中的 `total` 是去重后的任务行数。取消或单元失败不会退回额度。查询进度、读取结果和取消本身均不扣费。
- 断点续跑：任务状态与逐条结果落在 `index_jobs.dir`（默认 `<platform.data_dir>/index_jobs`），进程重启后自动续跑，已完成的 URL 不会重复查询。
- 同一时刻只跑一个任务（引擎按出口 IP 限速，并发只会互相抢令牌）；其余任务 FIFO 排队。
- 每条 URL 最多重排 `index_jobs.max_unit_retries` 次（默认 6）。重试用尽后记录该行最终的引擎结果，任务继续执行其他 URL。
- 超时或连接中断时，原提交可能已入队。可先列出同一密钥的最近任务，结合时间、引擎和条数确认，再决定是否重提。直接重复提交可能创建另一个任务并再次扣费。
- 控制台在线调试（`POST /api/playground/check`）仍是 **1–20 条同步**，不返回任务号——它的界面只渲染同步信封。

**Python 示例 · 从 URL 清单提交并下载 CSV**

把 URL 保存到 `urls.txt`，每行一条，至少 21 条。将下例保存为 `batch-index.py`，使用「第一次调用」中的环境变量，运行 `python batch-index.py`。脚本提交一次，打印任务 ID，随后查询状态。若脚本中断，可用已打印的任务 ID 继续查询。

```python
import json
import os
import time
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import urljoin
from urllib.request import Request, urlopen

base = os.environ["W2A_BASE"].rstrip("/")
key = os.environ["W2A_KEY"]
urls = [
    line.strip()
    for line in Path("urls.txt").read_text(encoding="utf-8-sig").splitlines()
    if line.strip()
]
if len(urls) <= 20:
    raise SystemExit("异步批次需要超过 20 条 URL")

def call(url, payload=None):
    headers = {"X-API-Key": key}
    body = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload).encode("utf-8")
    try:
        with urlopen(Request(url, data=body, headers=headers), timeout=30) as response:
            return response.status, response.read()
    except HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        retry_after = exc.headers.get("Retry-After", "无")
        raise SystemExit(f"HTTP {exc.code} · Retry-After: {retry_after} · {detail}") from exc

status, body = call(f"{base}/v1/index/check", {"urls": urls, "engines": ["baidu"]})
if status != 202:
    raise SystemExit(f"预期 HTTP 202，收到 {status}")
accepted = json.loads(body)
job_id = accepted["job_id"]
print(f"已入队 {job_id}，共 {accepted['total']} 条", flush=True)

poll_url = urljoin(f"{base}/", accepted["poll_url"])
while True:
    _, body = call(poll_url)
    job = json.loads(body)
    state = job["status"]
    print(f"{state} · {job['completed']}/{job['total']}", flush=True)
    if state == "done":
        break
    if state in {"cancelled", "failed"}:
        raise SystemExit(f"{job_id} 已停止：{job.get('last_error') or state}")
    time.sleep(min(60, max(15, job.get("wait_seconds", 0))))

_, csv_data = call(urljoin(f"{base}/", accepted["csv_url"]))
destination = Path(f"{job_id}.csv")
destination.write_bytes(csv_data)
print(f"已下载 {destination}")
```

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
- `Authorization: Bearer <管理员JWT>` —— 管理员身份，**不计费**；仅接受登录签发、携带 `sid` 且持久化角色为 `admin` 的会话 JWT，无 `sid` 或 operator/viewer JWT 返回 `403 {"error":"forbidden","code":"insufficient_role"}`。

鉴权失败返回平台信封 `401 {"error":"unauthorized"}`；JWT 身份/角色不符返回 `403 {"error":"forbidden","code":"insufficient_role"}`；API key 配额用尽返回 `429 {"error":"quota_exceeded"}`。

**会话**：`initialize` 响应头返回 `Mcp-Session-Id`。后续请求在使用同一 API Key 身份或管理员账号时可以复用会话，也可以无状态调用。关闭会话时用相同身份和 `Mcp-Session-Id` 发送 `DELETE /web-tool/mcp`。成功返回 204，未鉴权返回 401，已过期、未知或归属其他身份的会话返回 404。

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

程序调用使用 `Authorization: Bearer <JWT>`，浏览器管理台使用登录时设置的 HttpOnly 会话 Cookie。凭据缺失、过期或无效时返回 `401 unauthorized`。修改密码会使旧 token 失效（见 §6.9）。管理 API 默认全局限速每分钟 60 个请求，超限返回 `429 rate_limited` 和 `Retry-After`。

**角色与最小权限**：固定三种角色 `admin` / `operator` / `viewer`（权限 `admin > operator > viewer`），按持久化用户记录逐请求校验，角色/状态变更立即生效；未知或缺失角色一律拒绝。配置 `platform.admin_username` / `admin_password` 仅在首次启动迁移进用户库，之后用户库是唯一事实来源；不含 `uid`/`role` 声明的旧 JWT 按 `sub` 查找持久化用户，按其当前角色与状态逐请求校验；仅当用户库为空才回退 bootstrap admin。MCP 的 JWT 必须携带 `sid` 且持久化角色为 `admin`，否则 403 `insufficient_role`。

| 最小角色 | 端点范围 |
| --- | --- |
| `viewer` | 所有 GET 读取端点（含 `/api/me`、`/api/keys` 列表、`/api/stats/*`、`/api/usage*`、`/api/reconcile`、`/api/engines`、`/api/sessions`、`/api/audit`、`/api/logs`、`/api/logs/config`）、`POST /api/playground/check`、`POST /api/auth/password`、`POST /api/auth/logout`、`POST /api/auth/refresh` |
| `operator` | API key 创建/更新/删除/轮换、引擎冷却 `reset` / `force`、黑名单全部端点 |
| `admin` | 用户管理 `/api/users*`、`DELETE /api/sessions/:id`、`GET /api/admin/backup`、`POST /api/logs/cleanup` |

角色不足返回 `403 {"error":"forbidden","code":"insufficient_role"}`；黑名单命中在其它中间件之前返回 `403 {"error":"forbidden","reason":"blacklisted"}`（§6.12）。

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
  "user": { "username": "admin", "role": "admin", "user_id": "<用户 ID>" }
}
```

**401**：`{"error":"unauthorized"}`（凭据错误）。
**429**：`{"error":"too_many_attempts"}`（同一用户名跨 IP 连续 5 次失败后锁定 15 分钟，期间登录一律 429，带 `Retry-After`；`security.login_lockout.{fails,minutes}` 可调）。

`user` 中 `role`（`admin`/`operator`/`viewer`）与 `user_id` 为 additive 字段。

### 6.2 GET /web-tool/api/me

```json
{ "username": "admin", "role": "admin", "user_id": "<用户 ID>",
  "keys_count": 2, "today_calls": 17, "total_calls": 1337 }
```

`role` 与 `user_id` 均为字符串 additive 字段。

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

修改当前登录账号的密码（管理员、运维员、只读用户均可自助修改）：

```bash
curl -X POST {BASE}/api/auth/password \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"old_password":"admin123","new_password":"s3cret-Passw0rd!"}'
```

**200**：

```json
{ "ok": true, "token": "eyJhbGciOi…", "expires_in": 86400, "token_version": 2 }
```

（响应携带**新签发的 token**，当前会话无需重新登录。）

- `new_password` 长度 < 8 → 400 `{"error":"bad_request","code":"INVALID_PARAM","reason":"INVALID_PARAM"}`。
- `old_password` 错误 → 401 `{"error":"unauthorized"}`。
- 成功后：新密码以 bcrypt 哈希写入**用户库**并撤销该用户的会话。仅当操作者是配置管理员（`platform.admin_username` 对应的 bootstrap admin）时，还会写入 settings 桶覆盖 config 默认值、`token_version` +1，并让**全局旧 JWT 立即失效**（含控制台与 MCP Bearer）；其他账号改密只影响本人会话。

### 6.10 POST /web-tool/api/playground/check

控制台「在线调试」页后端：JWT 鉴权，**不计费、不写用量**。请求/响应结构与 `/web-tool/v1/index/check` 完全一致（§2.1）：

```bash
curl -X POST {BASE}/api/playground/check \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}'
```

错误：401（无/坏 token，平台信封）、400（参数，同 §2.1 表）、503（冷却信封）。

### 6.11 用户与角色（admin）

用户对象（`/api/users` 的 `items[]` 或创建的 `user`）：`{"id","username","role","status","created_at","last_login_at"?}`；`role ∈ admin|operator|viewer`，`status ∈ active|disabled`（不含密码哈希）。

| 端点 | 请求 | 响应 |
| --- | --- | --- |
| `GET /web-tool/api/users` | — | `{"total":N,"items":[用户对象…]}` |
| `POST /web-tool/api/users` | `{"username","password","role"}` | `{"ok":true,"user":用户对象}` |
| `PATCH /web-tool/api/users/:id` | `{"role"?,"status"?}` | `{"ok":true,"user":用户对象}` |
| `DELETE /web-tool/api/users/:id` | — | `{"ok":true}` |
| `POST /web-tool/api/users/:id/password` | `{"new_password"}` | `{"ok":true}` |

- 参数非法（用户名长度、未知角色/状态、用户名重复、密码过短）或违反保护规则 → 400 `{"error":"bad_request","code":"INVALID_PARAM","reason":"INVALID_PARAM"}`（消息为中文）；`:id` 不存在 → 404 `{"error":"not_found"}`。
- 保护规则：不能删除/降级/禁用**最后一名 active admin**（存储读取失败时按 fail-closed 处理）；不能通过用户管理修改或删除**当前登录账号**。
- 状态改为 `disabled`、降级、删除以及管理员重置该用户密码都会撤销其会话。管理员自助改密走 §6.9，仍沿用 `token_version` 全局失效契约；非管理员改自己的密码只撤销本人会话。

### 6.12 IP 黑名单（operator 及以上）

黑名单守卫作为全站前置中间件挂在所有路由注册之前（先于其它中间件），命中即返回 `403 {"error":"forbidden","reason":"blacklisted"}`，并写一条 `blacklist.hit` 审计、累加命中计数。客户端 IP 取自 `security.trust_proxy_headers` / `security.trusted_proxies`。

| 端点 | 请求 | 响应 |
| --- | --- | --- |
| `GET /web-tool/api/blacklist` | — | `{"total":N,"items":[条目…]}` |
| `POST /web-tool/api/blacklist` | `{"value","kind"?,"reason"?,"expires_at"?,"force"?}` | `{"ok":true,"entry":条目}` |
| `PATCH /web-tool/api/blacklist/:id` | `{"reason"?,"expires_at"?}` | `{"ok":true,"entry":条目}` |
| `DELETE /web-tool/api/blacklist/:id` | — | `{"ok":true}` |

条目：`{"id","kind":"ip|cidr","value","reason","created_by","created_at","expires_at"?,"hits","last_hit_at"?,"expired":bool}`，其中 `expired` 为派生字段。

- `kind` 省略时按 `value` 推断 IP / CIDR；非法 IP/CIDR 或重复规则 → 400 `INVALID_PARAM`（中文消息）。`:id` 不存在 → 404。
- `expires_at` 须为 RFC3339 且晚于当前时间；`PATCH` 传空串表示永久。**过期规则只被忽略，不会被日志清理自动删除**，需要时用 `DELETE` 移除。
- 初始规则来自 `security.blacklist`，仅首次启动写入（一次性标记落 settings 桶），之后以控制台/API 为准；存储读取失败时守卫 fail-open（不拦截）。
- 自锁保护：创建覆盖当前客户端 IP 的规则时默认 400；确需封禁自身须显式传 `force:true`，此操作会使该 IP 立即失去包括解除规则在内的所有 Web 访问。

### 6.13 服务日志（查询 viewer+，清理 admin）

```bash
curl -s "$BASE/api/logs?level=info&q=captcha&since=2026-11-25T00:00:00Z&limit=50&offset=0" \
  -H "Authorization: Bearer $TOKEN"
curl -s "$BASE/api/logs/config" -H "Authorization: Bearer $TOKEN"
curl -s -X POST "$BASE/api/logs/cleanup" -H "Authorization: Bearer $TOKEN"
```

- `GET /api/logs` → `{"total":N,"items":[{"time":"RFC3339","level":"…","message":"…","source":"logs.txt|logs.txt.1|…"}]}`。`level` 精确过滤、`q` 子串过滤、`since` 接受 RFC3339；`limit` 默认 50、最大 200，`offset` 默认 0。同时解析方括号文本与 JSON 两种日志格式；日志行按**不可信诊断数据**处理，只展示不执行。
- `GET /api/logs/config` → `{"max_mb":10,"backups":5,"retention_days":30,"format":"text|json","file_logging_enabled":true,"audit_retention_days":30}`。`file_logging_enabled=false` 表示当前进程未写 `logs.txt`（quiet/debug/关闭落盘），此时查询到的历史文件不代表本次运行。
- `POST /api/logs/cleanup`（admin）→ `{"ok":true,"removed_serve":N,"removed_audit":N}`；按 `app.log_retention_days` 删除过期的 `logs.txt.N` / `audit.log.N`。注意：`GET /api/audit` 只读取当前 `<data_dir>/audit.log`，不读轮转归档 `audit.log.N`；离线 `admin-backup` / `admin-restore` 只处理 bbolt，不覆盖 JSON fallback 存储（`web2api.json`）。

---

## 7. 错误码总表

### 7.1 平台信封错误（`/v1/*` 与 `/api/*`；`error` 字符串，无整型 `code`）

| HTTP | `error` | 场景 | Retry-After |
| --- | --- | --- | --- |
| 400 | `bad_request` | `reason` ∈ `EMPTY_QUERY`（urls 为空）/ `TOO_MANY_URLS`（>20 条）/ `INVALID_PARAM`（未知引擎、空名、负配额、短密码等） | 无 |
| 401 | `unauthorized` | key 缺失/未知/禁用；JWT 缺失/过期/密码修改后失效 | 无 |
| 403 | `forbidden` | 来源 IP 不符合密钥访问限制 | 无 |
| 403 | `forbidden` + `"code":"insufficient_role"` | 角色低于端点最小角色（§6） | 无 |
| 403 | `forbidden` + `"reason":"blacklisted"` | 客户端 IP 命中 IP/CIDR 黑名单（§6.12） | 无 |
| 404 | `not_found` | 资源（key、引擎名）不存在 | 无 |
| 429 | `quota_exceeded` | 当日配额用尽（预检拒绝，不扣费） | 有 |
| 429 | `too_many_attempts` | 登录连续失败锁定（默认 5 次失败锁 15 分钟，体内含 `retry_after_seconds`） | 有 |
| 429 | `rate_limited` | API Key、匿名鉴权失败或管理端点触发访问限速 | 有 |
| 500 | `server_error` | 存储等内部错误，`{"error":"server_error","request_id":"<X-Request-ID>"}`；原始错误只写服务端日志 | 无 |

### 7.2 核心错误和引擎单元错误

表中 HTTP 列适用于整请求错误。批量查询的单元失败放在 `results[].engines.<engine>.error`，其中 `code` 是字符串错误码，整批仍可返回 HTTP 200。

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
| 接口访问限速 `rate_limited` | 429 | 服务建议的等待秒数 |
| 引擎冷却 `engine_cooldown` | 503 | 距冷却解除的秒数（与 `retry_after_seconds` 字段同值） |
| `job_queue_full` / `server_draining` | 503 | 再次提交异步任务前的建议等待秒数 |

### 8.1 引擎冷却状态机（每引擎独立）

- **触发**：引擎返回 blocked（403）/ 429 / captcha。
- **连续升级**：连续第 1 次触发冷却 **10 分钟**，第 2 次 **30 分钟**，第 3 次及以上 **120 分钟**（`stability.engine_cooldown.steps_minutes: [10,30,120]`）。
- **按引擎覆盖**：`stability.engine_cooldown.per_engine.<engine>.steps_minutes` 可单独给某个引擎换档位。本仓库给百度配的是 **[2, 10, 30]**（实测单次验证码停 ~2 分钟即可恢复，10 分钟首档会明显拖低吞吐）。
- **计数归零**：连续无拦截达 **240 分钟**（`stability.engine_cooldown.reset_after_minutes`）后，连续计数归零重新从第 1 档起算。
- **正常批次结束后**：无额外批间冷却，可提交下一批，百度仍按同一个引擎限流器排队。最后一条如果已经用完令牌，下一条须等限流器的下一次放行。
- **冷却期内**：单引擎搜索和所选引擎全部已冷却的同步收录请求返回 503。同步批次中的冷却单元按 §2.1 返回，异步任务可等待恢复（§2.2b）。被冷却门控拦下的单元不会访问引擎。
- **到期探测**：冷却到期后放行一个探测请求——成功即恢复 `ok`；失败则进入下一档冷却。
- **行为级提示**：浏览器验证码页（无论人工还是自动触发）都会被判定为拦截并启动首档冷却（默认 10 分钟；百度 2 分钟，见 §8.1）。这是以牺牲短时可用性换取单出口 IP 存活率的刻意设计；高频使用方应配置代理池（[CONFIGURATION.md](CONFIGURATION.md) `proxies` 节）。
- 运维干预：`POST /web-tool/api/engines/{name}/cooldown/reset`（强制恢复）、`POST /web-tool/api/engines/{name}/cooldown/force {"minutes":N}`（强制冷却），见 §6.8；控制台「引擎状态」页（`#/engines`）提供同能力。

熔断器（`circuit_breaker.{failures,recovery_seconds,successes}`）与冷却状态机是**两套独立机制**：熔断针对连续请求失败（技术性失败），冷却针对反爬拦截（blocked/captcha）。两者同时生效时，冷却检查在前（不发真实请求）。

---

## 9. 计费口径

- **提交时预检**：按提交的 URL 条数 × 计费引擎数计算，重复 URL 也计入。省略引擎时，配额预检按四个默认引擎估算。超过可用额度时返回 `429 quota_exceeded`。
- **同步返回 200**：按响应中成功完成的 URL × 引擎单元计费。`indexed: false` 也属于成功完成，带 `error` 的单元不计费。`meta.units` 是实际参与查询的单元总数。服务端若无法解析单元明细，按请求单元数记账。
- **异步返回 202**：按提交的请求单元数一次性扣费，任务中的去重、失败或取消不调整该笔用量。提交返回 4xx 或 503 时，REST 不扣费。
- API Key 调用 MCP `web_search` 计 1 个单元。`check_indexed` 有逐引擎明细时按成功单元扣费，其他成功 HTTP 响应（包括入队回执）按估算单元记账。工具级错误可能封装在 HTTP 200 中，应检查工具结果。
- 查询配额、引擎状态、任务进度或结果、取消任务、管理 API 和在线调试不扣查询额度。
- **用量日志**：REST 收录请求通过密钥、限速及配额预检后，处理器返回的参数错误和冷却拒绝会写入真实 HTTP 状态，扣费单元为 `0`。密钥鉴权、接口限速或配额预检直接拒绝的 REST 请求不进入该用量日志。记录的 `outcome` 区分 `billed`、`engine_failed` 和 `rejected`。S4 之前的存量记录没有 `outcome` 字段。
- **管理统计口径（P-01）**：`GET /api/stats/overview` 的 `today_calls`/`total_calls`/`success_rate_24h` 统计**有效调用**（到达业务逻辑的 billed+engine_failed），参数错误、配额与限速拒绝、冷却拒绝不计入；`today_calls_all`（今日全部请求）与 `rejected_today_24h`（24h 内拒绝数）为 additive 字段。口径变化在发布说明中说明,历史记录无法回填。
- 对账：`GET /api/reconcile` 按日核对「落账次数」与「逐引擎成功单元数」；携带逐引擎明细的记录逐条核对，无明细的记录（MCP `web_search`、信封解析失败的存量数据）按落账口径计入、不判 mismatch。
- 对计费敏感的批量任务建议：先小批量探测引擎健康度（`GET /web-tool/v1/engines` / `list_engines`），避开冷却与熔断中的引擎，再放大批量——冷却/熔断单元虽不计费，但整请求会因此缩水。
- 配额维度：`daily_quota`（本地日重置，0 = 不限）与 `used_total`（累计）；用量日志（`/web-tool/api/usage`）逐笔记录计费 unit 数（units）、HTTP 状态与各引擎成功/失败数。
- **并发配额（B-15）**：REST 和 MCP 在同一进程内按 API Key 共用在途配额。接收前检查已用量和在途预留，完成后按实际计费口径落账并释放预留。预留只存在于进程内，已入账的扣费和流水在同一存储事务提交。可用 `GET /api/reconcile` 核对用量。
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
  GET    /web-tool/api/users                                          用户列表（admin）
  POST   /web-tool/api/users                                          新建用户 {"username","password","role"}（admin）
  PATCH  /web-tool/api/users/:id                                     改角色/状态（admin）
  DELETE /web-tool/api/users/:id                                     删除用户（admin）
  POST   /web-tool/api/users/:id/password                            重置用户密码（admin）
  GET    /web-tool/api/blacklist                                     黑名单列表（operator+）
  POST   /web-tool/api/blacklist                                     新增 IP/CIDR 规则（operator+）
  PATCH  /web-tool/api/blacklist/:id                                 改备注/过期时间（operator+）
  DELETE /web-tool/api/blacklist/:id                                 删除规则（operator+）
  GET    /web-tool/api/logs?level=&q=&since=&limit=&offset=           服务日志查询（viewer+，limit≤200）
  GET    /web-tool/api/logs/config                                    日志轮转/保留配置（viewer+）
  POST   /web-tool/api/logs/cleanup                                   手动清理过期滚动日志（admin）

健康与观测
  GET  /web-tool/health  /web-tool/ready  /web-tool/stats
  GET  /web-tool/stats/cache  /web-tool/stats/proxy  /web-tool/stats/cb
  GET  /web-tool/metrics                        OpenMetrics 指标（security.metrics_token Bearer 或仅 loopback, S4）

CLI（停服态, S4）
  web2api admin-backup   --full --out <bundle.tar.gz>   备份完整恢复点
  web2api admin-restore  --in <bundle.tar.gz> [--preserve-in <外部目录>]
                                                         校验并恢复；挂载卷根目录使用原地模式
  web2api admin-reset  --data-dir <dir> --username admin --password <新密码>
                                                          停服重置；同时更新用户库与 token_version
```
