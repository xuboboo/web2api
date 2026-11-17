# 5 分钟跑通 Web2API

> 本文所有请求与响应样例均来自真实调用（2026-11-16），密钥、ID、时间已脱敏。
> 跟着做一遍，你就能拿到第一个「这个 URL 是否被百度/必应收录」的判定结果。

**约定**：下文用 `{BASE}` 代表你的服务根地址，例如 `http://127.0.0.1:7000/web-tool`
或部署后的 `https://your-domain.com/web-tool`。**所有端点都挂在 `/web-tool` 固定前缀下**。

---

## 第 0 步：确认服务活着

```bash
curl {BASE}/engines/status
```

这个端点**无需鉴权**，返回四引擎的实时状态。看到 `state:"ok"` 就说明服务正常：

```json
{
  "engines": [
    { "engine": "baidu", "state": "ok", "enabled": true, "breaker": { "state": "closed" } },
    { "engine": "bing",  "state": "ok", "enabled": true, "breaker": { "state": "closed" } }
  ],
  "generated_at": "2026-11-16 22:46:31"
}
```

> `generated_at` 为服务器本地时间（格式 `YYYY-MM-DD HH:MM:SS`）；样例引擎清单为截断示意，实际返回全部四引擎。

> 若某引擎 `state:"cooldown"`，说明它刚被搜索引擎风控、正在冷却自愈，换个引擎或稍后再试。

---

## 第 1 步：登录管理控制台，拿到管理员 JWT

调用 `/web-tool/v1/*` 需要一个 **API Key**；而创建 API Key 要先登录拿管理员 JWT。

```bash
curl -X POST {BASE}/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"你的管理员密码"}'
```

真实响应（200）：

```json
{
  "token": "<JWT字符串，约 200 字符>",
  "expires_in": 86400,
  "user": { "username": "admin" }
}
```

把 token 存进环境变量（后面要用）：

```bash
export ADMIN_TOKEN='<上面返回的 token>'
```

> 响应同时会下发一个 HttpOnly 的会话 Cookie，浏览器控制台靠它保持登录；程序化调用用 body 里的 `token` 即可。
> 连续 5 次密码错误会锁定该「用户名 + IP」15 分钟，期间正确密码也会被拒（429）。

---

## 第 2 步：创建一个 API Key

```bash
curl -X POST {BASE}/api/keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-first-key","daily_quota":100}'
```

真实响应（200）：

```json
{
  "id": "35b1b68658b1dda8",
  "name": "my-first-key",
  "prefix": "sk-w2a-f3a0c0",
  "status": "active",
  "daily_quota": 100,
  "used_today": 0,
  "used_total": 0,
  "created_at": "2026-11-16T14:46:31.889117348Z",
  "last_used_at": null,
  "expires_at": null,
  "allowed_ips": [],
  "key": "<YOUR_API_KEY>：完整密钥只在这里出现一次，请立即保存"
}
```

立刻把完整密钥存好（**只返回这一次**，列表接口只显示 `prefix`）：

```bash
export W2A_KEY='<YOUR_API_KEY>'
```

字段说明：

| 字段 | 含义 |
| --- | --- |
| `key` | 完整密钥，**仅创建时返回**；之后要用它调 `/v1/*` |
| `prefix` | 密钥前缀，列表/日志里用来识别是哪把钥匙 |
| `daily_quota` | 每日调用次数上限；`0` = 不限 |
| `allowed_ips` | IP 白名单，空数组 = 不限制 |
| `expires_at` | 过期时间，`null` = 永不过期 |

---

## 第 3 步：第一次收录判定（核心调用）

查两个 URL 是否被百度和必应收录：

```bash
curl -X POST {BASE}/v1/index/check \
  -H "X-API-Key: $W2A_KEY" \
  -H "Content-Type: application/json" \
  -d '{"urls":["https://github.com","https://www.python.org"],"engines":["baidu","bing"]}'
```

真实响应（200，为可读性做了换行）：

```json
{
  "id": "01a1e4d2-f0da-7175-a42f-bfa781e071d1",
  "created_at": "2026-11-16T14:46:31Z",
  "results": [
    {
      "url": "https://github.com",
      "engines": {
        "baidu": {
          "indexed": false,
          "match": "none",
          "matched_result_url": "",
          "total_results": 10,
          "took_ms": 3451
        },
        "bing": {
          "indexed": true,
          "match": "exact",
          "matched_result_url": "https://github.com/",
          "total_results": 9,
          "took_ms": 4827
        }
      }
    },
    {
      "url": "https://www.python.org",
      "engines": {
        "baidu": {
          "indexed": true,
          "match": "exact",
          "matched_result_url": "https://www.python.org/",
          "total_results": 8,
          "took_ms": 3739
        },
        "bing": {
          "indexed": true,
          "match": "exact",
          "matched_result_url": "https://www.python.org/",
          "total_results": 10,
          "took_ms": 4845
        }
      }
    }
  ],
  "meta": {
    "request_id": "01a1e4d2-f0da-7175-a42f-bfa781e071d1",
    "took_ms": 4851,
    "units": 4
  }
}
```

**怎么读这个结果**（这是全文档最重要的一段）：

| 字段 | 含义 | 怎么看 |
| --- | --- | --- |
| `indexed` | 是否收录 | `true` = 在前 10 条搜索结果里找到了这个 URL |
| `match` | 匹配精度 | 只有 `exact`（精确命中）或 `none`（没找到）两种值 |
| `matched_result_url` | 命中的真实地址 | 命中时给出，`none` 时是空串 `""` |
| `total_results` | 该引擎返回的 organic 结果数 | 参考值 |
| `took_ms` | 该引擎耗时（毫秒） | 排查慢查询 |
| `meta.units` | 本次计费次数(字段 units)| = URL 数 × 引擎数，这里是 2×2=4 |
| `meta.request_id` | 请求 ID | 报错时拿着它找运维 |

> ⚠️ **`indexed:false` 不等于「没收录」**：它严格表示"前 10 条结果里没有"。
> 一个页面可能已被收录但排名靠后，或被搜索引擎区域化结果挡住。
> **建议对同一 URL 多引擎交叉验证**——任一引擎 `indexed:true` 即可确认已收录。
> 带深层路径的 URL 比首页更容易漏报。

**单 URL 快捷版**（GET，查询参数）：

```bash
curl -G {BASE}/v1/index/check \
  -H "X-API-Key: $W2A_KEY" \
  --data-urlencode 'url=https://github.com' \
  --data-urlencode 'engines=bing'
```

---

## 第 4 步：查配额与用量

```bash
# 当前 key 的配额
curl {BASE}/v1/quota -H "X-API-Key: $W2A_KEY"
```

```json
{
  "key_id": "35b1b68658b1dda8",
  "name": "my-first-key",
  "daily_quota": 100,
  "used_today": 5,
  "unlimited": false,
  "used_total": 5
}
```

```bash
# 最近的用量流水（管理面，用 JWT）
curl "{BASE}/api/usage?limit=3" -H "Authorization: Bearer $ADMIN_TOKEN"
```

```json
{
  "items": [
    {
      "time": "2026-11-16T14:46:38.400926338Z",
      "key_id": "35b1b68658b1dda8",
      "key_name": "my-first-key",
      "endpoint": "/web-tool/v1/index/check",
      "engines": ["bing"],
      "urls": 1,
      "units": 1,
      "status": 200,
      "took_ms": 1633,
      "outcome": "billed"
    }
  ],
  "total": 1
}
```

**计费口径**：`outcome:"billed"` 表示成功计费；引擎级失败（如 `blocked`、`captcha_detected`）对应的计费次数记 0，不扣费。

---

## 第 5 步：用你熟悉的语言调用

把 `{BASE}` 和密钥替换成你自己的。

### Python（标准库，无需安装依赖）

```python
import json, os
from urllib.request import Request, urlopen

BASE = os.environ["W2A_BASE"]   # 例如 http://127.0.0.1:7000/web-tool
KEY  = os.environ["W2A_KEY"]    # 你的 sk-w2a- 开头完整密钥

req = Request(
    f"{BASE}/v1/index/check",
    data=json.dumps({
        "urls": ["https://github.com"],
        "engines": ["baidu", "bing"],
    }).encode(),
    headers={"X-API-Key": KEY, "Content-Type": "application/json"},
    method="POST",
)
with urlopen(req, timeout=60) as resp:
    data = json.load(resp)

for item in data["results"]:
    for eng, v in item["engines"].items():
        if "error" in v:
            print(f"{eng}: 错误 {v['error']['code']}")
        else:
            print(f"{eng}: {'已收录' if v['indexed'] else '未命中'} ({v['match']})")
print("计费 units =", data["meta"]["units"])
```

### JavaScript（Node 18+ 内置 fetch）

```js
const BASE = process.env.W2A_BASE; // http://127.0.0.1:7000/web-tool
const KEY = process.env.W2A_KEY;   // 你的 sk-w2a- 开头完整密钥

const res = await fetch(`${BASE}/v1/index/check`, {
  method: "POST",
  headers: { "X-API-Key": KEY, "Content-Type": "application/json" },
  body: JSON.stringify({ urls: ["https://github.com"], engines: ["baidu", "bing"] }),
});
const data = await res.json();
for (const item of data.results) {
  for (const [eng, v] of Object.entries(item.engines)) {
    console.log(eng, v.error ? `错误 ${v.error.code}` : `${v.indexed ? "已收录" : "未命中"} (${v.match})`);
  }
}
console.log("计费 units =", data.meta.units);
```

### Agent / MCP 客户端

MCP 入口是 `{BASE}/mcp`，**必须携带 API Key**，否则 401。以 Claude Code 为例：

```bash
claude mcp add --transport http web2api {BASE}/mcp --header "X-API-Key: $W2A_KEY"
```

其它客户端的远程 MCP 配置字段名各异（Gemini CLI 用 httpUrl、Codex 用 config.toml），以各自文档为准；下方 JSON 适用于 Claude Code（.mcp.json）、Cursor、VS Code：

```json
{
  "mcpServers": {
    "web2api": {
      "type": "http",
      "url": "{BASE}/mcp",
      "headers": { "X-API-Key": "<YOUR_API_KEY>" }
    }
  }
}
```

内置 4 个工具：`web_search`（四引擎搜索）、`check_indexed`（收录判定）、`get_quota`（配额）、`list_engines`（引擎与冷却状态）。

---

## 第 6 步：常见错误排查

| 现象 | HTTP | 原因 | 怎么办 |
| --- | --- | --- | --- |
| `{"error":"unauthorized"}` | 401 | 没带 key / key 错误 / 已禁用 | 检查 `X-API-Key` 头；确认密钥状态 active |
| `{"error":"quota_exceeded"}` + `Retry-After` | 429 | 当日配额用完 | 换 key、提高 `daily_quota`，或等次日零点自动恢复 |
| `{"error":"engine_cooldown","retry_after_seconds":600}` | 503 | 所选引擎全在冷却 | 等 `retry_after_seconds` 秒后重试，或换引擎 |
| `reason:"EMPTY_QUERY"` | 400 | `urls` 为空 | 传 1–20 条 URL |
| `reason:"TOO_MANY_URLS"` | 400 | 超过 20 条 | 分片请求 |
| `reason:"INVALID_PARAM"` | 400 | 引擎名不认识 | 用 `baidu`/`sogou`/`so360`/`bing`（`360` 是 so360 的别名） |
| 某引擎返回 `error` 但 HTTP 200 | 200 | 该引擎被风控/超时 | 属正常：其它引擎结果照常返回，失败单元不计费；看 `error.code` |

> 客户端按 `reason` 字段分支即可兼容新旧两种 400 信封形状。
> 所有响应头都带 `X-Request-Id`，与 body 里的 `request_id` 一致，报错时提供给运维。

---

## 下一步

- 完整端点与字段定义：[docs/API.md](API.md) / 网页浏览 `{BASE}/openapi.yaml`
- Agent 能力清单：`{BASE}/llms.txt`
- 配置项说明：[docs/CONFIGURATION.md](CONFIGURATION.md)
