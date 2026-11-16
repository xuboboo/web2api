# AGENT-README —— 给 AI Agent 开发者的 5 分钟接入指南

本平台（Web2API）是**自托管的网站收录查询开放平台**：你的 Agent 可以用它做两件事——

1. **收录判定**（`check_indexed`）：一批 URL 是否被百度 / 搜狗 / 360 / 必应收录，每 URL×引擎一个结论；
2. **国内搜索**（`web_search`）：四引擎 organic 结果（rank/title/url/snippet）。

两种接法任选：**MCP**（Claude 系客户端一次配置即得 4 个工具）或 **OpenAPI spec**（Dify / n8n / LangChain 通用导入）。两者都已在服务端内置，无需装任何 SDK。

```
能力自发现（给 LLM 看的）：
  {BASE}/llms.txt        平台简介 + 端点清单
  {BASE}/openapi.yaml    OpenAPI 3.1 全端点规范
```

下文统一用 `{BASE}` 代表你的服务根地址（本机默认 `http://127.0.0.1:7000/web-tool`，部署后如 `https://your-domain.com/web-tool`），所有路径固定带 `/web-tool` 前缀；示例中的 `http://127.0.0.1:7000/...` 请整体替换为 `{BASE}/...`。

---

## 0. 拿到鉴权头（1 分钟）

1. 管理员登录控制台 `{BASE}/console/`（默认 `admin/admin123`，部署后请改密）；
2. 「API 密钥」页 → 新建 key → 复制 `sk-w2a-<32位hex>`（**仅显示这一次**）；
3. 之后所有调用带请求头 `X-API-Key: sk-w2a-…`。

鉴权规则：

| 身份 | 头 | 计费 |
| --- | --- | --- |
| API key（Agent 常规身份） | `X-API-Key: sk-w2a-…` | 计费（配额内按次扣费） |
| 管理员 JWT | `Authorization: Bearer <token>`（仅 MCP 端点接受） | 不计费（仅调试/管理用；REST `/v1/*` 不接受 JWT，其 Bearer 等效密钥本体） |

计费口径：`web_search` 每次调用计 1 次；`check_indexed` = `URL 数 × 引擎数` 次；`get_quota` / `list_engines` 免费。**按成功单元计费**：HTTP 200 完成后逐引擎核对响应信封，仅成功单元按次扣费；失败单元（`error` 字段，含 raw 模式 `not_implemented`）不计费；响应信封无法解析时按请求单元全额计费（兜底）；4xx / 429 / 503 不扣费。详见 [API.md §9](API.md)。

---

## 1. Claude / Claude Desktop（MCP，1 分钟）

Claude Desktop 经 Settings → Connectors 界面添加远程 MCP（URL 填 `{BASE}/mcp`，鉴权选自定义 Header `X-API-Key`）；Claude Code 等客户端用通用 JSON 配置（`.mcp.json`）：

```json
{
  "mcpServers": {
    "web2api": {
      "url": "{BASE}/mcp",
      "headers": { "X-API-Key": "sk-w2a-…" }
    }
  }
}
```

配置完成后即可看到 4 个工具：`web_search`、`check_indexed`、`get_quota`、`list_engines`。试着对它说：

> 用 check_indexed 查一下 https://gov.cn 和 https://example.com 在百度和必应的收录情况

Claude Code 用户一行接入：

```bash
claude mcp add --transport http web2api {BASE}/mcp \
  --header "X-API-Key: sk-w2a-…"
```

协议细节（供自研客户端直连）：MCP streamable HTTP，JSON-RPC 2.0，`protocolVersion: "2025-06-18"`；`initialize` → `notifications/initialized`（HTTP 204）→ `tools/list` / `tools/call`；支持 `Mcp-Session-Id` 内存会话。报文示例见 [API.md §5.1](API.md)。

---

## 2. Dify（OpenAPI 导入，2 分钟）

1. 工具页 →「自定义」→「创建自定义工具」；
2. 导入方式选 **OpenAPI-Swagger**，粘贴或用 URL 导入：`{BASE}/openapi.yaml`；
   URL 导入时 `servers` 必须是域名格式、不能写 IP（OpenAPI 规范要求）；本地调试可改用本地文件导入。
3. 鉴权选 **API Key**，类型 **Header**，Header 名称填 **`X-API-Key`**，值为 `sk-w2a-…`；
4. 导入后为每个接口补一句工具描述（Dify 要求），例如 check_indexed：「判断 URL 是否被百度/搜狗/360/必应收录」；
5. 在 Agent / Workflow 里即可作为工具调用。

## 3. n8n（HTTP Request 节点）

HTTP Request 节点 → Import cURL，直接粘：

```bash
curl -X POST '{BASE}/v1/index/check' \
  -H 'X-API-Key: sk-w2a-…' -H 'Content-Type: application/json' \
  -d '{"urls":["https://example.com/"],"engines":["baidu","bing"]}'
```

或用 Generic Credential Type → **Header Auth**（Name: `X-API-Key`）把密钥放进凭证而非节点。n8n 也支持导入 OpenAPI spec 自动生成参数面板。

## 4. LangChain（Python，30 秒）

```python
import json, urllib.request

BASE = "{BASE}"
KEY = "sk-w2a-…"

def check_indexed(urls: list[str], engines: list[str] | None = None) -> dict:
    body = json.dumps({"urls": urls, "engines": engines or ["baidu", "sogou", "so360", "bing"]}).encode()
    req = urllib.request.Request(
        f"{BASE}/v1/index/check", data=body, method="POST",
        headers={"X-API-Key": KEY, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        return json.load(resp)

# 接入 LangChain：函数签名 + docstring 即 schema
from langchain_core.tools import tool

@tool
def web2api_check_indexed(urls: list[str]) -> str:
    """判断 URL 列表是否被百度/搜狗/360/必应收录，返回每个 URL × 引擎的结论。"""
    return json.dumps(check_indexed(urls), ensure_ascii=False)
```

也可以用 OpenAPI toolkit 直接吃 `openapi.yaml`：`langchain_community.agent_toolkits.openapi`。

---

## 5. 直连 REST（不用任何平台）

```bash
# 收录判定（POST 批量）
curl -X POST {BASE}/v1/index/check \
  -H "X-API-Key: sk-w2a-…" -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com/post-1","https://example.com/post-2"],
       "engines":["baidu","so360"]}'

# 收录判定（GET 单条；engines 逗号分隔，360 是 so360 别名）
curl "{BASE}/v1/index/check?url=https%3A%2F%2Fexample.com%2F&engines=360" \
  -H "X-API-Key: sk-w2a-…"

# 配额
curl {BASE}/v1/quota -H "X-API-Key: sk-w2a-…"
```

成功单元形如 `{"indexed":true,"match":"exact","matched_result_url":"…","total_results":9,"took_ms":2393}`；失败单元形如 `{"error":{"code":"blocked","message":"…"}}`，HTTP 仍 200。

---

## 6. 错误处理：Agent 应该这样退避

所有 **429** 与 **503** 都带 `Retry-After` 响应头（秒）。Agent / 工作流的正确姿势是读它，而不是固定重试：

```text
收到 429 {"error":"quota_exceeded"}      → 当日配额用尽：停止重试，提示扩容或明日再来
收到 429 {"error":"too_many_attempts"}   → 登录连续失败被锁：等 Retry-After，别再试密码
收到 503 {"error":"engine_cooldown",…}   → 引擎冷却中：等 retry_after_seconds，或换引擎
收到 200 且单元 error.code=blocked 等    → 引擎级失败：跳过该引擎或稍后重查（失败单元不计费）
```

发起调用前可以先 `list_engines`（MCP）或 `GET /web-tool/v1/engines`（REST）看哪些引擎在冷却/熔断，把批量任务只发给健康引擎——**冷却中的引擎调用会在服务端被快速拒绝，不产生真实引擎请求，但同步等待与重试节奏仍会拖慢你的工作流**。

---

## 7. 限制与如实说明

- **同步上限**：收录判定单请求最多 **20 URL**；更大批量请分片，或关注后续异步模式（规划中）。
- **漏报语义**：四引擎均以 URL 为查询词检索前 10 条，已收录但排名靠后会漏报；`match="none"` 严格含义是"前 10 条里没有"，不等于"未收录"。建议多引擎交叉验证：任一引擎 `indexed=true` 即可确认已收录。（bing 早期 `url:` 操作符已因实测失效而停用。）
- **raw 模式**：部署方若以 `server.raw_requests: true` 运行，收录判定每个单元返回 `not_implemented`（HTTP 仍 200、**不计费**）——遇到整批 `not_implemented` 请让部署方切回浏览器模式，而不是重试。
- **协议版本**：MCP `protocolVersion` 为 `2025-06-18`（当前主流客户端的 legacy 握手）；对 2026-07-28 现代版客户端：服务器按 legacy 语义应答，现代请求头（`MCP-Protocol-Version`、`Mcp-Method` 等）会被忽略，不影响 tools/call。
- **计费**：按成功单元计费——200 信封内引擎级失败单元不计费，响应信封无法解析时按请求单元全额计费（兜底）；4xx / 429 / 503 不扣费。配额是自托管场景的自我约束工具，免费额度/QPS 分级等商用策略未实现。

完整字段与错误码表：[docs/API.md](API.md)。
