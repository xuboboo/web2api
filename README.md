<p align="center"><img src="logo.svg" alt="Web2API — 网站收录查询开放平台" width="640"></p>

# Web2API —— 自托管的网站收录查询开放平台

**提交一批 URL，判定它们是否被百度 / 搜狗 / 360 / 必应收录**；附带四引擎原始搜索、AI Agent 一等公民接入（MCP + OpenAPI）与一个管理控制台（API 密钥、配额、用量、引擎风控看板），并自带产品官网。官网、管理控制台与全部 API 固定部署在 **`/web-tool`** 子路径下：官网即 `/web-tool/` 根，控制台在 `/web-tool/console/`。

> 它**不是**通用 API 网关，也不是又一个 SerpAPI 平替。壁垒在两处：**国内四引擎的收录判定语义**（以 URL 为查询词检索 + 跳转链接还原 + 归一化匹配，这是 SerpAPI/Serper/博查都不提供的细分）与**引擎反爬对抗**（桌面指纹、双通道兜底、验证码门控、限速、熔断、IP 冷却状态机）。转发与编排只是外壳。

三个平面：

| 平面 | 端点 | 鉴权 | 一句话 |
| --- | --- | --- | --- |
| 数据平面 | `/web-tool/v1/*`、`/web-tool/{engine}/search` | `X-API-Key`（search 仅当 `data_plane_auth:false` 时免鉴权，模板默认 true 需密钥） | 收录判定 + 四引擎搜索，配额/限流/冷却内建 |
| Agent 平面 | `/web-tool/mcp`、`/web-tool/openapi.yaml`、`/web-tool/llms.txt` | `X-API-Key` 或 JWT | AI Agent 一等公民：Claude/Dify/Coze/n8n/LangChain 双入口接入 |
| 控制平面 | `/web-tool/api/*` + `/web-tool/console/` 控制台 | JWT | 风控与用量治理台：密钥、配额、用量日志、引擎冷却看板、在线调试 |

四引擎搜索管道内建：桌面浏览器指纹、浏览器/HTTP 双通道兜底、CAPTCHA 门控、按引擎限速、熔断保护与代理策略。

---

## 快速开始

需要本机装有 Chrome/Chromium/Edge（浏览器模式默认）。

```bash
go build -o web2api .
./web2api serve          # 默认读 ./config.yaml，监听 127.0.0.1:7000
# 浏览器打开官网 http://127.0.0.1:7000/web-tool/
# 管理控制台入口 http://127.0.0.1:7000/web-tool/console/
```

或 Docker Compose（详见 [deploy/README.md](deploy/README.md)）：

```bash
docker compose -f deploy/docker-compose.yml up -d --build
curl http://127.0.0.1:7000/web-tool/health
```

默认管理员 **`admin` / `admin123`** —— 首次部署后立即在控制台（`/web-tool/console/`）「系统设置」页改密码，或修改 `config.yaml` 的 `platform.admin_password`。启动日志检测到默认弱密码时会给出告警；控制台登录无验证码，切勿将管理端点暴露到公网。

**第一个调用**（控制台 `#/keys` 页创建一个 API key 后）：

```bash
curl -X POST http://127.0.0.1:7000/web-tool/v1/index/check \
  -H "X-API-Key: sk-w2a-…" -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com/post-1"],"engines":["baidu","bing"]}'
```

**开发者第一次接入,先看 [docs/QUICKSTART.md](docs/QUICKSTART.md)**:5 分钟跑通「登录 → 建钥 → 第一次收录判定 → 查配额」,含真实响应样例、Python/JS/curl 可复制代码与常见错误排查表。

全量端点、请求/响应示例、错误码表、限流与计费口径见 **[docs/API.md](docs/API.md)**；Agent 接入见 **[docs/AGENT-README.md](docs/AGENT-README.md)**。

---

## 与 SaaS 竞品的对照

我们先说清楚**不做什么**：不做全球引擎覆盖、不做 Google 垂直矩阵、不做位置/语言参数、不做异步任务归档、没有 reranker 增值层。也如实说明两点：计费口径是 **HTTP 200 即计费**（200 信封内引擎级失败单元不退费；4xx/429/503 不计费），当前没有"仅成功计费"承诺；批量判定为同步模式，单请求上限 20 URL。下面是功能级矩阵（✓=有，✗=无，部分=有但有明显限制）：

| 能力 | **Web2API** | SerpAPI | Serper.dev | 博查 BochaAI | 5118 | 爱站 | Link113 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 定位 | **收录判定** + 国内搜索 | 全球搜索结果 API | Google 搜索结果 API | AI 原生搜索 | SEO 大数据 | 站点级 SEO 数据 | 收录查询 |
| 国内四引擎（baidu/sogou/so360/bing）搜索结果 | ✓ | 部分（含 Baidu/Yandex 等 100+ 引擎） | ✗（仅 Google 系） | 部分（Web Search 覆盖中文 web，非引擎对等） | ✗ | ✗ | ✗ |
| **URL 收录判定 API（本文核心语义）** | **✓** | ✗ | ✗ | ✗ | ✗（仅网页工具，一次 200 条，无 API） | ✗ | ✓ |
| 自托管（密钥/日志本地存储） | **✓**（单二进制） | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ |
| MCP server（Claude 系客户端直连） | ✓（本仓库内置） | ✓（官方） | ✗ | ✓ | ✗ | ✗ | ✗ |
| 公开 OpenAPI spec（Dify/Coze/n8n/LangChain 导入） | ✓（`/web-tool/openapi.yaml` + `llms.txt`） | ✗ | ✗ | ✗（文档在需登录的飞书 wiki） | ✗ | ✗ | ✗ |
| 异步批量 / 归档 / 回调 | ✗（同步，20 URL/请求） | ✓（async + Searches Archive） | ✗ | ✗ | ✓（taskid 轮询） | ✗ | ✓（Webhook 回调） |
| 位置/语言/时效参数 | ✗ | ✓（location/uule/gl/hl…） | 部分 | ✓（freshness） | ✗ | ✗ | ✗ |
| 结果格式选择 | 部分（search 端点 `format=json\|markdown\|text\|ndjson`；收录端点固定 JSON） | ✓（json/html/md） | ✗ | ✗ | ✗ | ✗ | ✗ |
| Reranker / AI 增值层 | ✗ | ✗ | ✗ | ✓（Semantic Reranker / AI Search） | ✗ | ✗ | ✗ |
| 结构化数字错误码 | ✗（字符串 `code` + `reason`，见错误码表） | ✗ | ✗ | ✗ | ✓（100xxx/200xxx 分段） | — | ✗ |
| 免费额度 / 开源 | ✓（MIT，自托管零费用） | 试用 | 2500 次 | — | 0 元 100 次 | — | — |
| 计费承诺 | 200 即计费（失败请求不计费） | — | 仅成功计费 | — | 按次 | 按次 | — |

**差异化结论**：SerpAPI/Serper/博查做"搜索结果"，我们做"收录判定"；5118/爱站在收录上只有网页工具或站点级间接指标；Link113 有收录 API 但无自托管、无 Agent 入口、无搜索平面。**"中文引擎 URL 收录判定 API + 自托管可控 + Agent 双入口（MCP + OpenAPI）"是当前无人同时占据的生态位。**

---

## 收录判定是怎么做的

1. **检索**：以目标 URL 为查询词在引擎内检索前 10 条结果——baidu/sogou/so360 用完整 URL；bing 去掉 scheme 后用 host+path 检索（实测 10 站对照：完整 URL 命中 4/10、去 scheme 命中 7/10，`https://` 前缀会被 Bing 当成关键词）。bing 早期的 `url:<url>` 语法已于 2026-11 停用（返回无关结果）。全部走各引擎现有搜索管道，桌面指纹、双通道、CAPTCHA 门控、限速、熔断照常生效。
2. **跳转解析**：候选结果里的跳转链接（`baidu.com/link?url=`、`sogou.com/link`、`so.com/link`、bing `/ck/a`）先还原成真实地址，再参与匹配。
3. **归一化匹配**：协议与 host 小写、去默认端口、去 fragment、去尾部 `/`（根路径除外）、百分号解码；目标 URL 无 query 时忽略结果的 query，有 query 时参数排序后须相等。host 与 path 相等 → `indexed=true` 且 `match="exact"`，否则 `indexed=false` 且 `match="none"`。
4. **准确率说明**：所有引擎在**已收录但排名靠后（未进前 10）时会漏报**；`match="none"` 的严格含义是"检索前 10 条结果中没有该页面"，不等于"未收录"。根路径 URL（如 `https://example.com`）命中率最高；带深层路径的 URL 若未进前 10 会漏报。建议对同一 URL **多引擎交叉验证**：任一引擎 `indexed=true` 即可确认已收录。

---

## API 一览

全部路径以 `/web-tool` 为前缀；请求/响应/错误码真实示例见 **[docs/API.md](docs/API.md)**。

```
数据平面（X-API-Key）
  POST /web-tool/v1/index/check    批量收录判定（1..20 URL × 引擎，1 URL×1引擎=1 unit）
  GET  /web-tool/v1/index/check    单 URL 便捷版
  GET  /web-tool/v1/quota          配额查询
  GET  /web-tool/v1/engines        引擎与熔断状态
  GET  /web-tool/{baidu|sogou|so360|bing}/search?text=…   四引擎原始搜索（data_plane_auth=false 时免鉴权）

Agent 平面
  POST /web-tool/mcp               MCP streamable HTTP（initialize/tools，4 个工具）
  GET  /web-tool/openapi.yaml      OpenAPI 3.1 全端点规范
  GET  /web-tool/llms.txt          Agent 能力清单

控制平面（JWT）
  /web-tool/api/auth/login|password、/web-tool/api/keys、/web-tool/api/stats/*、
  /web-tool/api/usage、/web-tool/api/engines（冷却状态机）、
  /web-tool/api/engines/{name}/cooldown/reset|force、
  /web-tool/api/playground/check（控制台在线调试，不计费）

健康观测（免鉴权）
  /web-tool/health、/ready、/stats、/stats/cache|proxy|cb
```

---

## Agent 接入（MCP + OpenAPI 双入口）

### Claude / Claude Desktop（MCP）

`claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "web2api": {
      "url": "http://127.0.0.1:7000/web-tool/mcp",
      "headers": { "X-API-Key": "sk-w2a-…" }
    }
  }
}
```

接入后 Claude 可直接调用 4 个工具：`web_search`（四引擎搜索，1 unit/次）、`check_indexed`（收录判定，urls×engines units）、`get_quota`（配额）、`list_engines`（引擎与冷却状态）。协议为 MCP streamable HTTP，JSON-RPC 2.0，protocolVersion `2025-06-18`，支持 `Mcp-Session-Id` 内存会话。

### Dify / Coze / n8n / LangChain（OpenAPI 导入）

四个平台都以 OpenAPI spec 为通用入口：Dify「自定义工具 → 导入 OpenAPI」、Coze「资源库 → 插件 → 导入」、n8n「HTTP Request 节点 → 导入 cURL/OpenAPI」、LangChain「OpenAPI toolkit」。鉴权统一配 **Header 型 API Key**，Header 名 `X-API-Key`（n8n 选 Header Auth）。spec 地址：`http://127.0.0.1:7000/web-tool/openapi.yaml`。

五分钟跑通的逐平台截图级步骤、JSON-RPC 报文与常见坑见 **[docs/AGENT-README.md](docs/AGENT-README.md)**。

---

## IP 风控与冷却机制

国内引擎对单出口 IP 的高频访问极为敏感。平台内置四层递进的保护：

1. **按引擎限速**（`rate_requests/rate_burst`，默认保守）——第一道闸。
2. **CAPTCHA 门控 + 双通道**——被拦后 10 分钟内该引擎优先走纯 HTTP 通道（`core/captcha_gate.go`）；可选接入 2captcha 自动解题（见 config.yaml 注释）。
3. **熔断器**——连续请求失败 8 次熔断、20 秒后半开（`circuit_breaker` 节）。针对**技术性失败**。
4. **引擎冷却状态机**（二期新增，`stability.engine_cooldown` 节）——针对**反爬拦截**，每引擎独立：
   - **触发**：引擎返回 blocked（403）/ 429 / captcha。**行为级提示：浏览器验证码页一旦出现即触发冷却**——哪怕那次请求最终解题成功，最短也会进入 10 分钟冷却。这是刻意设计：出现验证码意味着 IP 已被标记，继续打只会加深封禁。
   - **连续升级**：第 1 次冷却 10 分钟、第 2 次 30 分钟、第 3 次及以上 120 分钟（`steps_minutes: [10,30,120]`）。
   - **计数归零**：连续无拦截达 240 分钟（`reset_after_minutes`）后计数清零，重新从第 1 档起算。
   - **冷却期内**：该引擎所有 search/check **快速失败（HTTP 503 `engine_cooldown` 信封 + `Retry-After`），不发真实请求**——不消耗引擎配额、代理与浏览器资源。
   - **到期探测**：冷却到期放行一个探测请求，成功恢复，失败进下一档。
   - **运维干预**：`POST /web-tool/api/engines/{name}/cooldown/reset` 强制恢复、`POST /web-tool/api/engines/{name}/cooldown/force {"minutes":N}` 维护窗口强制冷却；控制台「引擎状态」页（10 秒轮询）提供冷却倒计时与同款按钮。

冷却与熔断是两套独立机制，冷却检查在前。`/v1` 与 search 响应的 `meta.engines_cooldown` 会列出当前冷却中的引擎，客户端应据此跳过并在 `Retry-After` 后重试。

**单出口 IP 无法支撑长时间高并发**——长期使用请配置代理池（`proxies` 节，支持 tagged pool 与 sticky lanes）或降低频率。

---

## 安全模型

- **凭据存储**：管理员密码 bcrypt 哈希（settings 桶，修改密码后覆盖 config 默认值）；API key 只存 SHA-256 哈希，完整 key 仅创建响应返回一次；JWT 签名密钥为随机 32 字节，存 `<data_dir>/secret.key`（0600）。
- **登录防爆破**：同一 用户名+IP 连续 5 次失败锁定 15 分钟（`security.login_lockout.{fails:5,minutes:15}`），期间一律 `429 {"error":"too_many_attempts"}` + `Retry-After`。
- **管理 API 限速**：默认 60 req/min（`security.admin_rate_per_minute`），超限 429 + `Retry-After`。
- **token 版本化**：修改密码（`POST /web-tool/api/auth/password`，新密码 ≥ 8 位）后 `token_version` +1，**全部旧 JWT（控制台与 MCP 的 Bearer 会话）立即失效**。
- **传输**：CORS 默认放开但非回环绑定 + 无鉴权组合会在启动日志给出告警；生产环境建议绑回环、收紧 `cors.allow_origins` 或前置 nginx + TLS。
- **存储**：bbolt（纯 Go 无 CGO）单文件 `<data_dir>/web2api.db`，打开失败自动回退等价 JSON 文件存储（接口不变）。

---

## raw 模式（如实说明）

`server.raw_requests: true`（或 `--raw`）切换纯 HTTP 模式：不起浏览器、资源占用低，但**上游已明确结果不稳定或可能完全不可用**（启动日志会告警），且仅覆盖 baidu/sogou/so360（无 bing）。更重要的是：raw 引擎**不支持收录判定**——`/web-tool/v1/index/check` 在 raw 模式下每个单元返回 `{"error":{"code":"not_implemented",…}}`（HTTP 仍 200）。**计费口径不变：HTTP 200 完成即全额扣 unit**，因此 raw 模式下跑收录判定会"花钱买 not_implemented"。raw 收录能力在修复清单中（p1）；在那之前，收录判定请使用默认浏览器模式。

---

## 遗留资产声明

`examples/` 与 `skills/web2api-search/` 是**上一代改造遗留**：其中的脚本指向 `/mega/search`、`/extract`、Google/DuckDuckGo 引擎等**当前 API 面已不存在的端点**，未随本期契约更新，不可直接运行。一切以本 README 与 [docs/API.md](docs/API.md) 为准（示例修复已列入后续清单）。阅读时请把它们当历史参考，不要复制其中的 URL 或字段。

---

## 子路径部署

整个应用**固定部署在 `/web-tool` 子路径下**（代码内常量 `core.BasePath`，不是配置项，不支持根挂载）。根路径 `/` 没有任何路由（404），`/health`、`/v1/*`、`/api/*`、`/mcp`、`/{engine}/search` 只存在于前缀之下——容器探针与反向代理一律使用 `/web-tool/health` 等带前缀路径。

**nginx 直接转发（不去前缀）**：

```nginx
location /web-tool/ {
    proxy_pass http://127.0.0.1:7000;   # 原样透传，不做路径重写
    proxy_set_header Host $host;
    proxy_read_timeout 120s;
}
```

`proxy_pass` 末尾**不带斜杠**即不重写路径；官网与控制台自身用相对路径引用资源、hash 路由，无需任何额外配置。更多部署细节（探针、日志、排障）见 [deploy/README.md](deploy/README.md)。

---

## 配置

配置优先级：**CLI 参数 > `WEB2API_*` 环境变量 > `config.yaml` > 内置默认值**（Viper）。逐键注释见仓库根 [config.yaml](config.yaml)，完整参考见 [docs/CONFIGURATION.md](docs/CONFIGURATION.md)。平台相关节（字段名固定）：

```yaml
platform:
  enabled: true           # false 时不挂载 /api/* 与 /v1/*，官网与控制台静态页与健康端点仍可用
  data_dir: "./data"      # bbolt 库 web2api.db + JWT 密钥 secret.key 的目录（.gitignore 已忽略 data/）
  admin_username: "admin"
  admin_password: "admin123"  # 部署后务必修改
  jwt_expire_hours: 24

stability:                # 二期：引擎冷却状态机
  engine_cooldown:
    steps_minutes: [10, 30, 120]   # 连续第 1/2/3+ 次被拦截的冷却时长
    reset_after_minutes: 240       # 连续无拦截达此时长后连续计数归零

security:                 # 二期：控制平面加固
  login_lockout:
    fails: 5              # 同一 用户名+IP 连续失败次数阈值
    minutes: 15           # 锁定时长
  admin_rate_per_minute: 60  # 管理 API 全局限速（超限 429 rate_limited）
  data_plane_auth: true      # true 时数据面 /{engine}/search 与 /stats/* 也要求 X-API-Key（本仓库模板默认;代码内置默认仍 false 以兼容既有部署）
```

引擎与反爬参数（限速、指纹、熔断、代理、2captcha）说明见 [docs/CONFIGURATION.md](docs/CONFIGURATION.md) 与 config.yaml 内注释。

---

## 测试

```bash
go test ./...                                  # 单元测试（离线）
node deploy/e2e.mjs http://127.0.0.1:7000      # 平台契约冒烟（含 MCP 握手、冷却恢复、openapi.yaml/llms.txt 断言）
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000   # 引擎稳态观测
```

测试设计见 [docs/TESTING.md](docs/TESTING.md)。

## 项目结构

```
baidu/ sogou/ so360/ bing/   四引擎（URL、选择器、解析、双通道、index_check 入口）
core/                        HTTP 服务（全部路由挂 /web-tool）、收录判定核心、浏览器池、
                             代理、缓存、熔断、响应构建、错误契约
mcp/                         MCP streamable HTTP 端点与 4 个工具（Agent 平面）
platform/                    平台后端：API key/配额/用量（bbolt）、JWT 管理端、
                             /v1 与 /api 路由注册、计费与限速
web/site/                    产品官网（纯静态零依赖，由 serve 挂在 /web-tool/ 根）
web/admin/                   管理控制台（纯静态零依赖，由 serve 挂在 /web-tool/console/：
                             总览/密钥/用量/文档/引擎状态/在线调试/Agent 集成/系统设置）
cmd/                         CLI（search / serve）与装配（platform 挂载、官网与控制台静态、启动日志）
deploy/                      Docker 编排、E2E 冒烟与稳态脚本、运维文档
docs/                        API 参考、Agent 接入、架构、配置、测试文档
```

## 许可证

MIT，见 [LICENSE](LICENSE)。
