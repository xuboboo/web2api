# 上线审计：实机验证证据

记录方式：命令 + 实际输出摘录 + 时间。所有针对接口的验证都在**独立数据目录**（`data-audit-r1`）与**独立端口 7077** 上完成，未触碰生产数据；临时配置、二进制、数据目录在验证后已删除。

---

## E1 鉴权基线（未携带凭据时的行为）

```
health            : 200      # 公开探活
v1 no key         : 401      # POST /v1/index/check
v1 bad key        : 401      # 伪造 X-API-Key
jobs no key       : 401      # GET /v1/index/check/jobs
quota no key      : 401
admin keys no jwt : 401      # GET /api/keys
admin stats no jwt: 401
mcp no key        : 401      # POST /mcp tools/list
metrics (loopback): 200      # 空 metrics_token 时仅本机
stats (no key)    : 401      # data_plane_auth=true 生效
```

结论：数据面、管理面、MCP 面三处入口都强制鉴权；`/stats/*` 的默认安全姿态（需 key）实际生效。

## E2 静态资源目录穿越

```
/web-tool/console/../config.yaml        : 404
/web-tool/console/%2e%2e%2fconfig.yaml  : 404
/web-tool/config.yaml                   : 404
/web-tool/console/../../server.go       : 404
```

结论：`config.yaml`（含管理员密码）与源码均无法通过静态路由取出；Fiber 的路径规范化 + 目录限定有效。

## E3 配额与请求体上限

| 场景 | 结果 |
| --- | --- |
| key `daily_quota=1`，第 1 次单 URL 查询 | 200 |
| 同 key 第 2 次 | **429**（`quota_exceeded`） |
| `urls` 5001 条（0.14MB） | **400** `TOO_MANY_URLS: urls accepts at most 5000 entries per job, got 5001` |
| `urls` 200000 条（5.81MB） | **400** 同上（`got 200000`） |
| 上述请求后 `/health` | 200（进程未受影响） |

配置 `app.max_concurrent_requests: 2`、`BodyLimit=10MB`、`ReadTimeout=60s`、`WriteTimeout` 由批量预算推导、`IdleTimeout=120s`。

## E4 异步任务越权（F-09 修复前 → 修复后）

创建 key A 提交 21 条 → `202 job_20261124T071313_d5f7dca9c1`；用 key B 访问：

| 操作 | 修复前 | 修复后 |
| --- | --- | --- |
| B `GET /v1/index/check/jobs/{A}` | 200（读到进度） | **404** |
| B `GET …/{A}/results` | 200（读到结果） | **404** |
| B `DELETE …/{A}` | 200 `cancelled` | **404** |
| B `GET /v1/index/check/jobs` | `count=1` | **0** |
| A 读自己 / 取消自己 | 200 / 200 | 200 / 200 |

## E5 构建上下文（F-02）

静态规则重放（同一套 `.dockerignore` pattern 作用于工作区）：

```
before: 772 MB   after: 2 MB   saved: 770 MB
```

`data/` 内含 `secret.key`(64B)、`web2api.db`(128KB)、`audit.log`，均不再进入构建上下文。

## E6 测试与静态检查基线

```
go vet ./...                                 无输出（通过）
go test -p 1 -count=1 ./...                  全部 ok（baidu/bing/cmd/core/core/*/mcp/platform/so360/sogou）
go build -trimpath -ldflags="-s -w" (linux/amd64, CGO_ENABLED=0)  → 22.3 MB 静态二进制
go test -race ./...                          本机不可执行：无 gcc/clang（CI 的 ubuntu job 覆盖）
```

并行跑全量测试时出现过一次 `VirtualAlloc failed errno=1455`（Windows 提交内存耗尽：27.2/30.3 GB，本机另有 67 个 Chrome 进程），改为 `-p 1` 后连续两次全绿 —— 属本机资源限制，非代码缺陷。

## E7 控制面压测（不触发引擎）

工具：`D:\web2api-audit\loadprobe`（Go 标准库，keep-alive 复用连接，统计 P50/P95/P99 与状态码分布）。

```
/web-tool/health   conc=20  n=500   wall=0.82s  qps=611.6
                   p50=3.3ms p95=132.2ms p99=138.9ms max=141.2ms   status 200×500  errors=0
/web-tool/console/ conc=10  n=200   wall=0.12s  qps=1653.2
                   p50=1.1ms p95=19.6ms p99=69.5ms max=70.7ms      status 200×200  errors=0
```

鉴权路径（单 Key 限速 120/分钟、突发 30）：

```
/v1/quota conc=4 n=100  -> 200×30  429×70   （突发桶 30 用尽后立即 429，p50 0.5ms）
/v1/quota conc=8 n=200  -> 200×2   429×198
```

结论：控制面本身不是瓶颈（数百 QPS 无错误）；限速在**突发维度**很硬（30 个令牌），对"一次 20 条同步批量"的正常用法无影响，但客户端若并发轮询会很快拿到 429，需要按 `Retry-After` 退避。

## E8 异常注入：任务目录不可写

把 `index_jobs.dir` 指向一个"父路径是文件"的目录后：

```
修复前：启动成功(health 200) → 提交 21 条 → HTTP 500
        {"error":"internal_error","message":"INTERNAL: cannot persist the job"}
        服务仍存活，jobs 列表为空（无脏任务）
修复后：启动即失败，退出码 1
        Error: index_jobs.dir 不可写(D:\...\blocked\sub\jobs)，请检查目录权限或 index_jobs.dir 配置:
               mkdir D:\...\blocked\sub: The system cannot find the path specified.
        正常目录：启动成功，health 200
```

结论：修复前是"部署看起来健康、第一个大批次才炸"，修复后是"启动即失败并指出具体路径"。这正是"部署成功 ≠ 具备运行能力"的一类问题。

## E9 前端 UX 实机走查（Playwright + 真实 Chrome 158）

| 视口 | 结果 |
| --- | --- |
| 1440×900 桌面 | 登录页布局正常：左侧品牌区（4 引擎 / 批量上限 / 冷却说明）+ 右侧表单，无错位 |
| 390×844 移动（@2x） | 表单优先、品牌区下沉，可读、无横向滚动，按钮可点 |

发现并修复的问题（截图对比：`D:\web2api-audit\ux-login-{desktop,mobile}.png`）：

1. 登录页品牌区写着"**20 条** 单次批量收录判定上限"——批量早已支持 5000 条（>20 转异步），属前后端能力不一致。
2. 同一区域写着"三档冷却 **10 / 30 / 120** 分钟"——百度实际档位已是 `[2, 10, 30]`（配置实测），文案过期。
3. 登录表单 `autocomplete="off"` 且输入框无 autocomplete 属性 → 浏览器控制台告警 `Input elements should have autocomplete attributes (suggested: "current-password")`，密码管理器无法填充。

三处均已修复；移动端截图确认新文案已正确渲染。

## E10 登录后的控制台走查（Playwright 实机，1440×900）

流程：打开 `/web-tool/console/` → 填 `admin` + 密码 → 点击"登录" → 落到 `#/overview`。全程 5 步、5883 ms，无 page-error。

结果（截图 `D:\web2api-audit\ux-dashboard.png`）：

| 检查点 | 结果 |
| --- | --- |
| 登录成功反馈 | 右下角 toast「登录成功,欢迎回来」，同时跳转总览 |
| 侧栏导航 | 监控（总览/引擎状态）、数据（API 密钥/使用日志）、开发（在线调试/Agent 集成/API 文档）、系统（系统设置）分组清晰 |
| 空数据展示 | 统计带全 0 并附「参数错误、配额与限速拒绝的请求不计入调用与成功率」；最近日志显示「暂无调用记录 / 完成一次收录查询后,最近 5 条日志会显示在这里」——空态有解释，不是白屏 |
| 引擎状态 | 四引擎均显示「运行中」+ 绿色分段条，可点进冷却看板 |
| 图表空态 | 14 日图表零数据仍渲染坐标轴与逐日 tooltip，不误导 |
| 手动刷新 | 页头右侧有「手动刷新」按钮 |

遗留问题（记为 F-20，未修）：登录页加载时会请求 `GET /api/me` 探测既有会话，未登录时浏览器控制台留下一条红色 `401 (Unauthorized)` 资源错误。功能无影响（页面随后显示登录框），但属于噪声，且这次探测会占用未鉴权限流额度。修法建议：仅当本地存在 token 时才发起该探测。

## E12 长时间运行与批量任务实测（含内存采样）

任务：21 条 onlinedown 文章，`engines=[baidu]`，真实打引擎。

```
job job_20261124T075919_102fc31f8f
07:59:19 queued -> 08:03:20 done   耗时 241s（11.5s/条，配置 12s/条）
completed 21/21  indexed 4  not_indexed 17  failed 0  retrying 0
```

进程内存（仅 web2api 进程；Chrome 子进程另计）：

```
运行期采样：25 → 32 → 31 → 33 → 33 → 34 → 35 → 32 → 34 MB   （无单调上涨）
任务结束后：27 MB（浏览器池空闲回收生效）
健康检查：全程 200
```

同批次的同步路径（5 条，≤20 不走队列）也验证过：直接返回 200 结果信封，耗时约 60s（5×12s）。

## E13 任务进度粒度：修复前后对比

同一命令（21 条百度任务，12 秒/条）轮询 `GET /v1/index/check/jobs/{id}`：

| 修复前（`chunk=10`） | 修复后（`chunk=1`） |
| --- | --- |
| `00:00 done=0/21` | `00:00 done=0/21` |
| `00:13 done=0/21` | `00:13 done=1/21` |
| `01:50 done=0/21` | `00:26 done=3/21` |
| `02:03 done=0/21`（整整两分钟不动） | `00:39 done=4/21 idx=1` |
| `02:15 done=10/21`（一次跳 10 条） | `00:52 done=5/21 idx=1` |
| … | 之后每 ~12 秒 +1，`idx` 同步增长 |

结论：修复前用户看到的是"进度条卡在 0、ETA 不动"两分钟；修复后每完成一条就前进一步，与引擎节奏一致。落盘开销为每条一次 ~1KB 原子重命名，可忽略。

## E14 异常恢复：硬杀进程 + 任务续跑（已实测通过）

环境：独立 `data-audit-r1`、7077 端口、真实百度任务 21 条。

1. `08:17:28` 提交 → `queued`，随后 `running`。
2. `08:18:13`（运行 45 秒）硬杀服务进程（`Stop-Process -Force`，模拟断电/被 OOM killer 杀）：任务 `completed=4/21`。
3. 用同一个二进制、同一个数据目录重启 → 08:18:36 起任务仍为 `running`，`completed` 从 4 继续增长（7 → 8 → 9 → 10），没有从 0 重跑。
4. `08:21:40` 完成：`21/21, indexed=2, not_indexed=19, failed=0`。

**结论**：任务队列的持久化 + 恢复链路真实可用，不是仅靠单测。已完成 URL 没有重复计费/重复查询；硬杀后恢复也没有脏任务。

## E15 数据库异常：bbolt 截断 → JSON fallback（已实测，P2 发现 F-22）

把 `web2api.db` 截成 512 字节后启动：

```
/health              200  storage=json
登录(admin/配置密码)  200
/api/keys(无 JWT)    401
data-corrupt/web2api.db   仍是 512 字节（原文件没被"修好"或覆盖）
data-corrupt/web2api.json 生成，包含新管理员 hash / 会话 / fallback 标记
```

**结论**：进程不崩、安全边界仍在（无 JWT 仍 401）、业务继续可用；**但原有 API Key 与用量账本全部消失**（JSON fallback 从空状态开始）。这是既有产品契约（`platform/store_json.go`、`TestNewFallsBackToJSONWhenBoltBlocked`），不是新引入的故障。

**上线含义**：数据库损坏时系统会"看似健康地降级"，监控 `/health.storage` 必须告警 `json`，否则会静默丢账本。已在 `OPERATIONS.md` 增加此告警项与处置流程。

## E16 安全绕过测试：X-Forwarded-For 伪造（已实测通过）

`security.trust_proxy_headers=false`（直连场景默认）时，40 个未鉴权请求每次换一个伪造 `X-Forwarded-For` / `X-Real-IP`：

```
401×6 + 429×34
```

与不换头的结果一致 → **伪造 XFF 不能绕过未鉴权 IP 限速**。
带合法 Key 的 30 个请求全部 200（burst=30）→ Key 维度限速独立生效。

附带验证：
- Key 名称 `<img src=x onerror=alert(1)>` + 负配额 → 400 `INVALID_PARAM`（不是 500，也不是接受后反射）
- Key `daily_quota=-1` 更新 → 400 `INVALID_PARAM`
- 任务 ID `../../config.yaml` / `..%2f..%2fconfig.yaml` / `job_../../x` → 全 404

## E17 CI 与文档契约检查

- 路由契约脚本：控制台 JS 的 **15 个 API 路径字面量**全部能在 Go 中找到挂载（含 `/api/engines/:name/cooldown/{reset,force}` 参数路由），**0 个悬空接口**。
- 配置契约脚本：`cmd/root.go` 的 **76 个 SetDefault 键**（75 个单行 + 1 个多行 per-engine map）全部出现在 `ENVIRONMENT.md`；`index_jobs.chunk` 的 config/code 均为 1。
- CI 版本：`ci.yml` / `integration.yml` / `release.yml` 均与 `go.mod` 的 Go 1.26 对齐。
- `golangci-lint` v2.13.2 已成功安装；v2 schema 配置已通过加载。完整 lint 在本机因分页文件不足中止（Windows 资源限制），交由 CI 验证。

## E18 登录锁定绕过：换 IP 爆破（已修复并实测）

**修复前逻辑**：锁定键为 `(username, IP)`。攻击者每换一个源 IP，失败计数就从 0 开始；5 次错误密码的阈值可被无限绕过。

**修复后**：锁定键改为**用户名全局**（跨 IP 聚合失败次数）；IP 仍写入日志/审计用于溯源，诊断用 IP 集合最多保留 16 个，避免无限增长。

实机（独立 7077，5 个不同 `X-Forwarded-For`，均错误密码）：

```
203.0.113.1 → 401
203.0.113.2 → 401
203.0.113.3 → 401
203.0.113.4 → 401
203.0.113.5 → 401
203.0.113.250 + 正确密码 → 429 too_many_attempts
```

自动化回归：`TestLoginLockoutCannotBeBypassedByRotatingIPs`（5 个 RemoteAddr 各失败一次，第 6 个 IP 的正确密码必须 429）。

**取舍**：单管理员平台按用户名锁定可阻止分布式爆破，但攻击者可借此短暂锁死管理员（15 分钟）；这是"防接管"优先于"防骚扰"的有意选择，配合 IP 审计可定位来源。

## E19 默认管理员密码：非 loopback 拒绝启动（已修复并实测）

**证据**：仓库自带 `config.yaml` 是 `server.host: 0.0.0.0` + 公开默认管理员密码；原实现只在启动日志 Warn，服务照常监听。

**修复**：平台初始化后、监听前检查：
- loopback（127.0.0.1 / ::1 / localhost）保留开发便利；
- 非 loopback + 有效密码仍是公开默认 → **拒绝启动，退出码 1**；
- 通过 `WEB2API_PLATFORM_ADMIN_PASSWORD` / `config.platform.admin_password` 改过，或在控制台改过密码（custom marker），正常启动。

实测：

```
127.0.0.1 + 默认密码                 → 启动成功（开发可用）
0.0.0.0   + 默认密码                 → 退出码 1，安全启动被拒绝
0.0.0.0   + 环境变量强密码           → 启动成功，/health 200
```

回归：`TestUsesDefaultAdminPasswordHonorsCustomMarker`（控制台改过密码后，即使 config 仍写默认值也不误判）。

## E20 浏览器崩溃恢复：严格集成测试通过

不再用"杀用户 Chrome"冒险，改为测试**自己启动的独立 Chrome 进程 + 独立临时 profile**：

```bash
WEB2API_INTEGRATION_TESTS=1 \
WEB2API_INTEGRATION_STRICT=1 \
WEB2API_APP_BROWSER_PATH="C:\Program Files\Google\Chrome\Application\chrome.exe" \
go test -p 1 -count=1 -timeout=900s -tags=integration ./...
```

结果：全部 12 个包 ok。其中 `TestBrowserRecoversAfterProcessDeath` 覆盖：

- Chrome 在首次连接前死亡；
- Chrome 在近期成功连接后死亡；
- Chrome 健康 ping 失败后死亡；
- 恢复时 3 路并发导航，确认只换 launcher、不重复拉起已关闭浏览器；
- `insecure=true` 的自签名 TLS 页面在恢复后仍可访问。

结论：浏览器崩溃恢复从"代码审查认为有机制"升级为"严格集成实测通过"。

## E21 依赖漏洞扫描：govulncheck（实机通过，发现并修复 1 个 P1）

**工具**：`govulncheck`（`golang.org/x/vuln/cmd/govulncheck`），source 模式，扫描 49 个模块 + 标准库。

**修复前**（`./...`）：

```
=== Package Results ===
Vulnerability #1: GO-2026-4950
    Authorization bypass via double-encoded paths in github.com/valyala/fasthttp
  Module: github.com/valyala/fasthttp
    Found in: github.com/valyala/fasthttp@v1.65.0
    Fixed in: github.com/valyala/fasthttp@v1.70.0

=== Module Results ===
Vulnerability #1: GO-2026-5932
    The golang.org/x/crypto/openpgp package is unmaintained...
  Module: golang.org/x/crypto
    Found in: golang.org/x/crypto@v0.58.0
```

**修复**：`go get github.com/valyala/fasthttp@v1.70.0`（fiber v2.52.16 要求 ≥v1.51.0，v2 线已是最新，故直接在 MVS 上抬 fasthttp）。

**修复后**（`./...`）：

```
=== Symbol Results ===
No vulnerabilities found.
Your code is affected by 0 vulnerabilities.
This scan also found 0 vulnerabilities in packages you import and 1
vulnerability in modules you require, but your code doesn't appear to call these.
```

**回归**：`go build ./...`、`go vet ./...`、`go test -p 1 ./...` 12 包全绿；严格集成测试全绿。

**遗留（不修，需说明）**：`golang.org/x/crypto/openpgp` 已被官方标记为 unmaintained/unsafe by design（GO-2026-5932）。本项目只用到 `x/crypto` 的 bcrypt，未 import openpgp，govulncheck 判定不可达。这是模块级告警，不构成可达风险；若后续要"零模块告警"，只能等上游移除该包。

**同时确认的工具链事实**：`govulncheck` 若 PATH 里没有 `go`，会报 `no go.mod file`（误导性错误）；网络需要 HTTP(S)_PROXY 且关闭 http2 才能稳定拉取 `vuln.go.dev`。这两点已写入 `docs/TESTING.md`，避免下一个人重复踩。

## E22 外部引擎集成测试的抖动（如实记录）

严格集成模式下 `TestSearchBing/web` 曾失败一次：

```
--- FAIL: TestSearchBing/web (17.70s)
    bing web search failed (strict mode): timeout. Cannot find element on page
```

该用例会真实打开 bing.com 搜索页并等待结果元素，属**外部站点依赖**，与本次依赖升级无关（`bing` 包不经过 fasthttp 的请求解析路径）。

复核证据：
- 单独重跑 `-run '^TestSearchBing$'` → **ok 7.8s**；
- 全量严格集成再跑一遍 → **11 个包全 ok，exit 0**。

**结论**：这是外部站点的偶发抖动（元素渲染超时），不是代码回归；但**必须承认**：严格模式下这类用例会因外部原因让 CI 变红。已在 `docs/TESTING.md` 标注：`TestSearch*` 为 live 用例，网络/风控波动时按 flake 处理并重跑，非严格模式自动 skip。

## E23 路径编码鉴权绕过复测（fasthttp 升级后，无绕过 + 1 个 P3 瑕疵）

CVE 类（双编码路径鉴权绕过）必须用真实请求验证结论，而不是只看 govulncheck 的符号分析。

**方法**：对 5 个受保护端点 × 8 种前缀变体（`/%2e%2e`、`/%252e%252e`、`/..%2f`、`/%2e%2e%2f`、`/./`、`//`、`/%09`、无前缀）逐一发原始请求，并按 Content-Type 判定是"API JSON"还是"静态 HTML"。

**结果**：

```
(plain)      /api/keys   401  JSON {"error":"unauthorized"}
/%2e%2e      /api/keys   200  HTML <!DOCTYPE html> ...     ← 静态兜底页，非 API
/%252e%252e  /api/keys   404  JSON {"error":"not_found"...}
/..%2f       /api/keys   200  HTML <!DOCTYPE html> ...
/%2e%2e%2f   /api/keys   200  HTML <!DOCTYPE html> ...
/./          /api/keys   404  JSON {"error":"not_found"...}
//           /api/keys   404  JSON {"error":"not_found"...}
/%09         /api/keys   404  JSON {"error":"not_found"...}
（/api/me、/api/runtime、/api/usage、/api/admin/backup 全部只有 401 或 404）
```

**判定**：
- **无鉴权绕过**：所有变体都**没有**出现"未鉴权 JSON 200"，受保护 handler 一次都没被执行。`/%2e%2e` 那三条 200 是静态站点的 SPA 兜底 HTML（643 字节、无任何 key 字段），不是 API 响应。
- **P3 瑕疵（F-29）**：`/api/keys` 前面加 `/..` 类前缀会落到静态兜底页并返回 **200 text/html**，而不是 404。没有数据泄露，但会让：
  - 漏洞扫描器把"200 on /api/keys"当成可疑项反复告警；
  - 对 `/api/*` 做可用性探测的监控出现**假通过**（拿到 200 却不是 API 响应）。

**已固化回归**：`TestProtectedEndpointsNeverAnswerJSONWithoutAuth`（7 端点 × 8 前缀），断言"任何编码下未鉴权都不可能返回 JSON 200"。这样将来路由/中间件调整若把静态兜底换成 JSON 兜底，测试会立刻失败，而不是等上线后被扫出来。

## E24 静态分析：staticcheck 实机跑通（14 → 0）

`golangci-lint` 在本机因 Windows 分页/导出数据问题跑不完整，改用**同源分析器** staticcheck 2026.2.2 直连 `go test` 的 type info：

```bash
export PATH="$HOME/go/bin:$PATH"   # 否则报 go command not found
staticcheck ./...
```

**修复前 14 条**（不是纯风格）：

| 位置 | 检查 | 性质 |
| --- | --- | --- |
| `core/index_check.go:780` | SA4006 | 死赋值：探针错误变量先赋默认值、随即被覆盖，读不到 |
| `core/index_check.go:695` | U1000 | 未使用常量（注释声称"供调用方兼容"，但它是未导出的，外部根本引用不到） |
| `platform/sessions.go:59` | U1000 | `sessionCache.mu` 从未使用——缓存其实靠 `Platform.sessionMu` 保护 |
| `platform/security_test.go:362` | U1000 | 测试桩里未使用字段 `forceErrFor`（会让人误以为支持按引擎错误） |
| `baidu/url.go:83` | ST1005 | 错误串大写 |
| `platform/platform.go:160` | SA1019 | `bolt.ErrTimeout` 已弃用 |
| 5 + 2 处测试 | SA4006 | `status`/`out` 赋值后从未使用 |

**修复后**：`staticcheck ./...` **exit 0**，`go vet ./...` 通过，11 包测试全绿。

**最值得记的一条**：`sessions.go` 的 `sessionCache.mu` 是"看起来自保护、实际不保护"的结构陷阱。我逐一核对了 `known`/`revoked` 的 **11 处访问全部在 `p.sessionMu` 之下**，因此删掉冗余字段并写明"由 Platform.sessionMu 保护"，避免下一个人误以为缓存自带锁而漏加锁——这类问题在 `-race` 跑不了的机器上最难发现。

## E25 运行期任务目录不可写（实测：响亮失败 + 自愈）

此前只验证了**启动期**探针（F-18）。本次验证更难的运行期场景：先正常启动、提交一个 21 条异步任务，然后**在服务运行中**把 `index_jobs` 目录 ACL 改成拒绝写入。

```
baseline submit : 202 {"job_id":"job_...","status":"queued","total":21,...}
locking job dir ... (icacls /deny USER:(OI)(CI)(W))
locked submit   : 500 in 0.01s
                  {"error":"internal_error","code":500,
                   "message":"INTERNAL: cannot persist the job"}
health after    : 200
unlocking ...   (icacls /remove:d)
after unlock    : 202 {"job_id":"job_...","status":"queued"}
```

**结论**：
- 0.01 秒内**响亮失败**，不是挂住、不是崩溃、不是"接受一个永远存不下来的任务"；
- 失败后 `/health` 仍 200，进程不受影响；
- 权限恢复后立即可用（无残留坏状态）。

这条把"磁盘满/不可写"从"文档里写了有处理"升级为"运行期实测有处理"，只剩**真正写满磁盘**（无独立测试盘）未做。

## E26 竞态检测 `-race` 实机跑通（最后一个"只能 CI"的检查）

**此前结论**：本机无 gcc/clang，`-race` 报 `requires cgo`，只能交 CI。本轮把它打通了。

**打通方式**（三步，都是环境问题，不是代码问题）：

1. `winget install -e --id BrechtSanders.WinLibs.POSIX.UCRT` —— Go 的 cgo 在 Windows 需要 POSIX 线程 + UCRT 的 mingw，这个组合正确。
2. **路径不能有非 ASCII 字符**：WinGet 默认装到 `C:\Users\豆豆\AppData\Local\Microsoft\WinGet\Packages\...`，gcc/ld 把 `豆豆` 变成 `????`，链接期报 `cannot find .../crt2.o`。把 `mingw64` 复制到 **`D:\mingw64`** 解决。
3. **临时目录也要 ASCII**：默认 `%TEMP%` 同样含中文，汇编期报 `can't create ...b136/_x001.o`。设 `GOTMPDIR=TMP=TEMP=D:\gotmp` 解决。

**结果**：

```
gcc.exe (MinGW-W64 x86_64-ucrt-posix-seh, built by Brecht Sanders, r3) 16.2.0
go test -race -count=1 -p 1 ./...
ok  baidu 3.7s   ok  bing 3.8s   ok  cmd 15.9s   ok  core 7.1s
ok  core/browser 1.9s   ok  core/fpcheck/detectors 2.4s   ok  core/region 1.9s
ok  mcp 3.8s   ok  platform 87.2s   ok  so360 3.6s   ok  sogou 3.5s
RACE_ALL_EXIT=0        ← 无 DATA RACE，11 包全绿
```

**意义**：并发/竞态是任务书里的重点项，之前"平台包有没有数据竞争"只能靠代码审查；现在有竞态检测器的实测结论。配合 staticcheck 抓到的 `sessionCache.mu` 锁陷阱，并发面从"看起来没问题"变成"两个独立手段都没发现问题"。

**环境注记**：`D:\mingw64`（~924 MB）与 `D:\gotmp` 是为本次审计安装/创建的，`winget uninstall BrechtSanders.WinLibs.POSIX.UCRT` 并删除这两个目录即可完全还原。

## E27 业务端点真实并发（8 次真实百度查询，受控）

之前 §C 只有控制面（`/health`、静态资源）的 QPS。本轮补上真正花钱的那条路径：`POST /v1/index/check`（单 URL，引擎=baidu），并发 4。

```
requests=8  concurrency=4  rounds=2  wall=85.7s
status distribution: {200: 8}          ← 无 5xx、无超时、无限速误伤
latency p50=37.82s  p95=48.16s  max=48.16s  mean=33.88s stdev=16.63s
indexed values: [False]                ← 8 条合成 URL 全部判定未收录，无假阳性
```

**怎么读这组数**：慢**不是**服务端能力问题。单引擎按 12 秒/条的节奏串行（`baidu.rate_requests: 5`、`interval 12s`），再叠加收录查询的标题探测（多一次页面抓取）与重试预算，所以 4 并发会排队在同一引擎的节拍后面。8 个单元 / 85.7 秒 ≈ **0.09 单元/秒**，与"12 秒一条"的设计完全吻合。

**并发结论**：
- 并发 4 时全部 200，说明队列、限速、配额三者协同正常，没有把并发压力转成 5xx；
- 真正决定吞吐的是**引擎节拍**，不是 CPU/内存（进程 RSS 全程 25–35 MB）；
- 想提高吞吐只能加引擎（bing/sogou/360 并行）或加出口 IP + 代理池，不是加机器。

## E28 镜像构建：无 daemon 条件下能静态验证的全部验证完（含一个真 bug）

**环境事实**：本机没有可用的容器引擎——`com.docker.service` 拒绝启动（权限），Docker Desktop 未安装在 `C:\Program Files\Docker\Docker\Docker Desktop.exe`，`docker-desktop` WSL 发行版里没有 `dockerd`。因此 `docker build` 仍然无法实机执行。

**但 `docker build` 会失败的多数原因可以机械排除**，我写了脚本逐项验证（`D:\web2api-audit\tools\verify_dockerfile.py`）：

```
== COPY sources ==            go.mod / go.sum / . / config.yaml / web/site / web/admin 全部存在且未被 .dockerignore 排除
== base images ==             golang:1.26.11-bookworm@sha256:dc40a13b…  → 200, 6531-byte manifest list
                              chromedp/headless-shell:stable@sha256:f7e7ac72… → 200, 506 bytes
== runtime wiring ==          wget / tini 已安装；HEALTHCHECK 用 wget（镜像内存在）；
                              ENTRYPOINT /usr/bin/tini 与安装路径一致；HEALTHCHECK 打 /web-tool/health；USER chrome
== build flags ==             Dockerfile 与 release.yml 的 -trimpath / -ldflags="-s -w" / CGO_ENABLED=0 一致
== builder stage 等价执行 ==   CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" → 22 MiB ELF（magic 7f454c46）
ALL STATIC DOCKER CHECKS PASS
```

**`docker compose` 可以完整验证（不需要 daemon）**：

```
$ docker compose config          # 未提供 ADMIN_PASSWORD
error while interpolating services.web2api.environment.WEB2API_PLATFORM_ADMIN_PASSWORD:
required variable ADMIN_PASSWORD is missing a value: set ADMIN_PASSWORD to a strong
admin password before 'docker compose up'          → exit 1

$ ADMIN_PASSWORD=*** docker compose config
WEB2API_PLATFORM_ADMIN_PASSWORD: ***   WEB2API_SERVER_HOST: 0.0.0.0   WEB2API_SERVER_PORT: "7000"
image: web2api:local   init: true   shm_size: "536870912"
ports 127.0.0.1:7000 → 7000   volumes: config.yaml(ro) + web2api-data→/usr/src/app/data
→ exit 0
```

**顺带抓到并修掉一个真 bug（F-34）**：compose 原本把 `WEB2API_SERVER_HOST` 固定为 `0.0.0.0` 且**不设**管理员密码——本会话新增的 F-26 守卫会让容器**直接退出 1**，`restart: unless-stopped` 会把它变成重启循环。已改为必需变量并在 `DEPLOYMENT.md` 说明 `.env` 用法；同时把 `.env` 加进 `.gitignore` 与 `.dockerignore`（否则运维按文档创建 `.env` 时可能把管理员密码提交进仓库或打进镜像）。

**容器 ENV 契约现在有单元测试兜住**：`cmd/dockerfile_env_test.go` 断言 `WEB2API_APP_BROWSER_PATH` / `WEB2API_SERVER_HOST` / `WEB2API_SERVER_PORT` / `WEB2API_PLATFORM_ADMIN_PASSWORD` 真的能绑定到对应配置键。没有这条测试，某个键丢掉 `SetDefault` 就会静默变得不可绑定——镜像里 `browser_path` 变空后 Rod 会在运行时下载 Chromium，在无网/非 root 镜像里必然失败，而且是**上线后才炸**。

**仍然未知**：daemon 实际执行每一层（`apt-get` 能不能连通、`useradd` 分支是否按预期走到、`chromedp/headless-shell` 里是否真有 `/headless-shell/headless-shell`）。这三点只能由 CI 或你那边有 Docker 权限的机器确认，**我不把它写成"已验证"**。

## E29 登录后全页面 UX 走查（真实 Chrome，1440×900，8 个页面）

此前只走查过登录页（桌面+移动）与总览。本轮把登录后的全部路由过了一遍：真实 Chrome 158（REA/Playwright 驱动，本机 localhost，无外网依赖），逐页截图并收集 console 与 page-error。

路由：`#/overview` `#/engines` `#/keys` `#/usage` `#/playground` `#/agents` `#/docs` `#/settings`。

**结论：8 页全部正常渲染，0 个 page-error**；控制台只有两类非缺陷输出：登录前的 `GET /api/me 401`（既有的会话探测，浏览器会为 4xx 打红字）与设置页的 `[DOM] Password field is not contained in a form`（Chrome 提示，见 F-37）。

**走查中发现并修复的 3 个问题**：

1. **总览卡片自相矛盾（F-36）**：卡片大字是「调用次数」，下面小字是「今日 N 次」——两个不同指标共用"次"这个量词，实测出现「1 / 今日 0 次」这种看起来像 bug 的显示。改为「计费 0 次」，让"调用次数 vs 计费次数"分开。
2. **使用日志看不出业务失败（F-36）**：一条 `HTTP 200 / 次数 0` 的记录，实际是引擎侧 `engine_internal`、0 计费。界面只有 HTTP 状态列，用户/运维无法判断"成功了但没记数"还是"引擎失败"。现在 `次数 0` 会显示为弱化的 `0 · 未计费` 并在 title 里说明原因。
3. **设置页/API 文档仍在描述旧的按 IP 锁定（F-37）**：F-25 已把锁定改成按用户名跨 IP 聚合，但三处用户可见文案还写着"同一用户名在同一 IP 下连续 5 次"。已全部改为"同一用户名连续 5 次（换 IP 不重置）"，并全仓复查 `同一 IP` 已无残留。

**过程中自我纠错一次（记录在案）**：我最初给使用日志加了一个「结果」列，把 `outcome=billed` 显示成"成功"。核对 `platform/model.go` 后确认 `billed` 的语义是"到达引擎且 HTTP 200"，而我们那条 200+引擎错误的记录正是 `billed`——那样显示会撒谎。于是撤回该列，改为只陈述事实（未计费）。**这类"看起来很合理但语义错误"的改动必须在提交前用代码语义校验一遍。**

## E11 受环境限制未执行的验证（禁止当作已通过）

| 项 | 原因 | 影响 |
| --- | --- | --- |
| `docker build` / 容器冒烟 | Docker 服务需管理员权限，`Start-Service com.docker.service` 被拒 | 镜像构建未实机验证；已用同参数交叉编译替代 |
| 真正写满磁盘 | 无独立测试盘，填盘有风险 | 未做（但"运行期目录不可写"已实测，见 E25） |

> `-race` 与"业务端点并发"已于本轮补齐，见 E26 / E27，不再属于"未验证"。

