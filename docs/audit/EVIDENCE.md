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
go test -race ./...                          当时本机无 gcc/clang；后续已通过 Docker Linux 镜像完成全量竞态检测（见 docs/TESTING.md 与 qa/features-audit/race-final.log）
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

### E28b 把"仍然未知"里的两个直接验了（下载镜像层，不跑容器）

网络恢复后，用 Docker Registry API 直接拉 `chromedp/headless-shell@sha256:f7e7ac72…` 的 linux/amd64 manifest（143.5 MiB，2 层），逐个流式解 tar 索引并核对关键路径：

```
FOUND   /headless-shell/headless-shell   mode=0o755  size=231238880
FOUND   /headless-shell                  mode=0o755
FOUND   /usr/bin/apt-get                 mode=0o755
FOUND   /usr/bin/dpkg                    mode=0o755
FOUND   /usr/bin/getent                  mode=0o755   ← Dockerfile 的 getent 探测可用
FOUND   /usr/sbin/useradd                mode=0o755   ← 回退分支可用
FOUND   /usr/bin/bash                    (useradd --shell /bin/bash 可用)
ABSENT  /usr/bin/tini
ABSENT  /usr/bin/wget                    ← 所以 apt-get 装这两个是必需的, 不是冗余
```

再从 image config 读 ENV/ENTRYPOINT：

```
ENTRYPOINT : ["/headless-shell/run.sh"]        ← 被本仓库 Dockerfile 的 tini 覆盖
PATH       : /headless-shell:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
PATH 含 /usr/sbin: True (useradd 可解析)
PATH 含 /usr/bin : True (getent / apt-get 可解析)
```

**结论**：`WEB2API_APP_BROWSER_PATH=/headless-shell/headless-shell` 这个固定路径**真实存在且可执行（0755）**——之前"镜像里可能没有这个路径，Rod 会在运行时下载 Chromium 然后失败"的担心被排除。基础镜像是 Debian 系（有 apt-get/dpkg），`apt-get install wget tini` 与 `getent passwd chrome || useradd` 两条命令的依赖都在。

**剩下真正未知的只有两件**：(1) 构建时能否连到 Debian 镜像源；(2) `docker run` 后健康检查是否真的转 healthy。这两件必须有 daemon。

## E29 登录后全页面 UX 走查（真实 Chrome，1440×900，8 个页面）

此前只走查过登录页（桌面+移动）与总览。本轮把登录后的全部路由过了一遍：真实 Chrome 158（REA/Playwright 驱动，本机 localhost，无外网依赖），逐页截图并收集 console 与 page-error。

路由：`#/overview` `#/engines` `#/keys` `#/usage` `#/playground` `#/agents` `#/docs` `#/settings`。

**结论：8 页全部正常渲染，0 个 page-error**；控制台只有两类非缺陷输出：登录前的 `GET /api/me 401`（既有的会话探测，浏览器会为 4xx 打红字）与设置页的 `[DOM] Password field is not contained in a form`（Chrome 提示，见 F-37）。

**走查中发现并修复的 3 个问题**：

1. **总览卡片自相矛盾（F-36）**：卡片大字是「调用次数」，下面小字是「今日 N 次」——两个不同指标共用"次"这个量词，实测出现「1 / 今日 0 次」这种看起来像 bug 的显示。改为「计费 0 次」，让"调用次数 vs 计费次数"分开。
2. **使用日志看不出业务失败（F-36）**：一条 `HTTP 200 / 次数 0` 的记录，实际是引擎侧 `engine_internal`、0 计费。界面只有 HTTP 状态列，用户/运维无法判断"成功了但没记数"还是"引擎失败"。现在 `次数 0` 会显示为弱化的 `0 · 未计费` 并在 title 里说明原因。
3. **设置页/API 文档仍在描述旧的按 IP 锁定（F-37）**：F-25 已把锁定改成按用户名跨 IP 聚合，但三处用户可见文案还写着"同一用户名在同一 IP 下连续 5 次"。已全部改为"同一用户名连续 5 次（换 IP 不重置）"，并全仓复查 `同一 IP` 已无残留。

**过程中自我纠错一次（记录在案）**：我最初给使用日志加了一个「结果」列，把 `outcome=billed` 显示成"成功"。核对 `platform/model.go` 后确认 `billed` 的语义是"到达引擎且 HTTP 200"，而我们那条 200+引擎错误的记录正是 `billed`——那样显示会撒谎。于是撤回该列，改为只陈述事实（未计费）。**这类"看起来很合理但语义错误"的改动必须在提交前用代码语义校验一遍。**

## E30 `-race` + 严格集成：先跑出 1 个真问题，修完全绿

E26 只把 `-race` 跑在单元测试上。本轮把**集成测试也放在竞态检测器下**：

```
CGO_ENABLED=1 WEB2API_INTEGRATION_TESTS=1 WEB2API_INTEGRATION_STRICT=1 \
  go test -race -count=1 -p 1 -tags=integration ./...
```

**第一次运行（暴露问题）**：

```
--- FAIL: TestIndexJobReleasesMemoryWhenDone (0.02s)
    index_job_test.go:326: finished job still holds 1 URLs in memory
FAIL github.com/xuboboo/web2api/core 38.856s
```

**根因不是测试写错，代码顺序有问题**：`Cancel` 与 `finish` 两条终态路径都是「持锁把 `Status` 置为终态 → 取快照 → 解锁 → 落盘 → 再调用 `release()` 释放 URL 列表」。中间存在一个真实窗口：**外部已经能读到"已完成"，而内存里那份 URL 列表还在**。普通运行时窗口只有微秒级，测试几乎不会踩到；`-race` 让调度变慢，把它放大了。

**修复**：拆出 `releaseLocked()`，在**持有锁的同一临界区内**、取完快照后立刻清空 `URLs`/`pending`/`attempts`（快照已持有列表，磁盘记录不受影响）。这样"终态"与"内存已释放"成为同一瞬间成立的原子事实，`release()` 也随之删除（已无调用者）。

**复测**：

```
ok  baidu 6.0s  ok bing 9.2s  ok cmd 16.1s  ok core 39.7s  ok core/browser 4.9s
ok  core/fpcheck/detectors 2.1s  ok core/region 1.6s  ok mcp 3.2s
ok  platform 85.9s  ok so360 3.4s  ok sogou 3.6s
EXIT=0      ← 11 包全绿，无 DATA RACE
```

**为什么这条值得单独记**：它证明竞态检测不只是"确认没并发 bug"，还能把**测试里被掩盖的时序假设**翻出来；而这次的根因落在产品代码（内存释放时机）而不是测试上。若只在 CI 上跑非 race 的集成测试，这个窗口会一直存在。

## E31 30 分钟长跑（此前只有 4 分钟）

清单 D5 一直是"4 分钟采样"，不足以看出慢泄漏。本轮跑满 **30 分钟**：每 30 秒采一次 RSS / 私有内存 / 句柄 / goroutine / heap，同时保持轻负载（每 30 秒 `/health`，每 3 次采样一次 `/api/stats/overview` 走 bbolt 读路径，每 5 分钟一次单 URL 真实百度查询）。

```
samples=59 minutes=30
rss        首次 17MB  末次 27MB  min 17  max 31
            前半均值 27.0MB  后半均值 27.6MB      ← +0.6MB / 15min，平台期不是上升趋势
goroutines 首次 10  末次 10  max 19（只在查询瞬间）  末次 10
handles    首次 162 末次 407  min 162 max 415
health     {200}       ← 59 次采样全部 200
engine checks 6 次：各 1.9–2.2s，indexed=false 且 error=false（合成 URL 未收录，符合预期）
```

句柄曲线按 2.5 分钟取样：

```
t=1s     162     ← 启动
t=156s   371     ← 首次查询拉起 Chrome + 连接池预热
t=465s   391
t=775s   401
t=1054s  405
t=1363s  407
t=1672s  409     ← 最后 10 分钟在 407–415 之间摆动，无单调增长
```

**判读（如实）**：

- **RSS 无泄漏迹象**：17 MB 冷启动 → 27 MB 稳定，前后半程均值只差 0.6 MB（2%），峰值 31 MB；
- **goroutine 无泄漏**：常驻 10，查询期间短暂到 19，每次回落到 10；
- **句柄需要继续观察**：预热后停在 407–415，30 分钟窗口内饱和而非增长，但**30 分钟不足以排除极慢泄漏**。判读方法已写进 `docs/OPERATIONS.md`（生产上比 RSS/句柄 24 小时斜率，而不是看绝对值）。

**顺带得到的稳定性结论**：30 分钟内 6 次真实引擎查询全部成功、无冷却、无验证码、无 5xx，健康检查 59/59 通过——这条比"跑通一次查询"更有说服力。

## E32 本地真实部署验证（release 二进制 + 独立目录，22/22 通过）

按"运维拿到的就是一个二进制"的方式验证，不带仓库、不带测试框架：

```
D:\web2api-audit\deploy\
├── web2api.exe                 21.7 MiB（go build -trimpath -ldflags="-s -w"）
├── config.yaml
├── web\site\  web\admin\
└── data\                       ← 运行后生成 web2api.db / secret.key / audit.log / index_jobs\
```

```
PASS  startup guard: 0.0.0.0 + default admin password refuses to start  (exit=1)
PASS  GET /health 200                      PASS  GET /ready 200
PASS  GET /metrics OpenMetrics (1580B)     PASS  GET /console/ 200
PASS  GET / 官网 200
PASS  data plane without key -> 401        PASS  admin API without JWT -> 401
PASS  admin login 200 + token              PASS  create API key (quota 500)
PASS  real baidu check (sync, 1 URL)  (200 2.4s indexed=True match=exact)
PASS  21 URLs -> 202 + job_id              PASS  job status readable (running)
PASS  job cancel accepted (200)            PASS  quota reflects consumption (used_today=22)
PASS  over-quota submission refused with retry_after (429 quota_exceeded, retry_after=75288)
PASS  usage log has records (2 rows)       PASS  usage row terminal status (202 units=21 billed)
PASS  runtime metrics readable             PASS  graceful stop
PASS  admin-backup produces a snapshot (24576B)
PASS  data dir has db + secret.key
22/22 passed
```

**过程中暴露并修掉的部署缺陷（F-40）**：第一次跑 `/console/` 是 **404**——因为 `web/site`、`web/admin` 不在二进制里，而 release workflow 的打包命令是 `tar ... "$BIN"`，**只打二进制**。已修 CI 增加 `web` 目录，并在 INSTALL 新增 §4、DEPLOYMENT §4 补部署要求与排错行。

**一次自我纠错的完整记录（重要）**：中途我连续两次看到"真实查询 240 s 超时、随后 `/api/runtime` 也超时"，一度怀疑是浏览器通道或自动下载 Chromium 卡死。实际原因是**我的验证脚本用 `subprocess.PIPE` 接服务端 stdout 却从不读取**，管道写满后服务端自己阻塞在日志写上——是 harness 缺陷，不是产品缺陷。改成写文件后同一场景 **1.7–2.4 s** 正常返回；再用 `probe_lane.py` 单独验证"不配 browser_path"的情况，结果是**快速降级到 HTTP 通道、2.3 s 命中**，与 INSTALL §1 的说法一致。因此没有写成产品缺陷，而是把自己写错的文档行改掉。

## E33 真实用户流程打真接口（百度收录为主线，31→39 步）

不走单元测试，按用户路径打真接口、真引擎（`probe_user_flow.py`）：

| 步骤 | 验证点 | 结果 |
| --- | --- | --- |
| 1 新用户 | 官网 200 / 引用自有静态资源 | ✅ 39837B |
| 2 登录 | 控制台 200 / 登录拿到 token / `/api/me` 可读 | ✅ |
| 3 建密钥 | 创建只回显一次完整 key；列表只显示前缀 | ✅ |
| 4 在线调试 | `/api/playground/check` 真实查询返回百度结果 | ✅ 5.0s indexed=true |
| 5 单条查询 | 已收录页面 `indexed=true`、命中 URL 一致、`meta` 带 request_id/units | ✅ 8.2s match=exact |
| 6 负样本 | 伪造路径 `indexed=false`（无假阳性） | ✅ |
| 7 异常输入 | 3 条相同 URL 去重为 1 条 / 1 单元 | ❌→已修（F-42） |
| 8 异常输入 | 空数组 400；非域名、`ftp://` 曾返回 200 | ⚠️→已修（F-42） |
| 9 批量任务 | 21 条转 202 + job_id；轮询 `2/21→3/21→4/21` 递增；ETA=192s | ✅ |
| 10 刷新页面 | 重复 GET 同一 job 状态一致（状态在服务端） | ✅ running/5 |
| 11 取消任务 | DELETE 被接受 | ✅ 200 |
| 12 配额 | 额度用尽后 429 + `retry_after_seconds` | ✅ |
| 13 鉴权 | 非法 key 401；伪造 JWT 401；数据面 key 不能当管理 JWT | ✅ |
| 14 巡检 | health/ready/metrics/stats×4/openapi.yaml/llms.txt 全 200；未知 API → JSON 404；未知页面 → 品牌 404 | ✅ |
| 15 用量 | 用量有记录；总览与用量一致；**对账接口 0 差异**（`charged_units == journaled_units`） | ✅ |

**修复后复测（`probe_refix.py`，冷却期间只用 1 次真实查询）**：

```
PASS  同步路径去重：3 个相同 URL → results=1 units=1
PASS  非域名输入被拒：400 INVALID_PARAM … has no public domain
PASS  不支持的 scheme 被拒：400 INVALID_PARAM … uses unsupported scheme "ftp"
```

**过程中学到的两件事（都记进 FINDINGS）**：

1. 密集打真实引擎会触发**冷却**（`503 engine_cooldown retry_after=48`）——这是设计正确，但也说明**验证脚本必须控制调用密度**，否则会把"保护生效"误读成"功能失败"。
2. 我的第一版脚本把 `/api/auth/me`、`/version`、`/assets/` 当成真实端点/路径，实际分别是 `/api/me`、不存在、`assets/`（相对路径）。**断言写错会造成假失败**，每次红灯都要先确认期望本身是否正确。

### E33b 单 IP 的真实调用上限（现场观测，非缺陷）

当晚在这台机器上累计跑了 20+ 次真实百度查询后，百度开始对本机出口 IP 弹验证码，**两条通道都一样**：

```
browser lane : [error] Captcha detected: https://bioc-static.cdn.bcebos.com/pages/ds_svcp.html?…
               [warning] CAPTCHA detected, skipping retries
               [warning] code=captcha_detected error="captcha detected" Index check unit failed
http lane    : {"engines":{"baidu":{"error":{"code":"captcha_detected","message":"captcha detected"}}}}
```

**系统的处理完全符合设计**（这条本身是"第三方不可用/反爬"场景的正向证据）：

| 观察点 | 实测 |
| --- | --- |
| 是否重试浪费额度 | ❌ 不重试，日志明确写 `skipping retries` |
| 单元结果是否诚实 | ✅ 单元内带 `error.code=captcha_detected`，不是伪装成"未收录" |
| 是否计费 | ✅ **0 unit**（`units=0`、`used_today=0`、对账 `charged_units=0`） |
| 整体 HTTP 状态 | 200（单单元失败不污染整批），全部引擎冷却时才 503 |
| 恢复手段 | `POST /api/engines/baidu/cooldown/reset` 实测可用；配 `proxies.entries` 才是根治 |

**结论**：无代理池时单 IP 约 20+ 次/小时即进入验证码状态——**这是业务规模的硬约束，不是 bug**。生产必须配代理池（`docs/DEPLOYMENT.md` 已写），单机自用则接受这个频率上限。

## E11 初始环境受限验证（2026-11-25；后续进展见 E35–E38）

| 项 | 原因 | 影响 |
| --- | --- | --- |
| `docker build` / 容器冒烟 | Docker CLI 存在，但 Linux engine named pipe `//./pipe/dockerDesktopLinuxEngine` 不存在 | 镜像层、容器健康检查和 TLS 实网访问未实机验证；Linux 交叉构建不能替代镜像运行 |
| 当前树 race detector | 初次验证时本机缺少可用 CGO 编译器 | 初始未重跑；后续安装/配置 MinGW 工具链后的当前树结果见 E37 |
| TLS 收紧后的严格集成/百度 live query | 初始阶段尚未执行；后续在当前树以严格集成 race 对固定公开查询做低频验证（E37） | Baidu/Bing 请求通过；未覆盖 Docker 容器网络路径，容器 HTTPS 仍由 I5 阻塞 |
| 真正写满磁盘 | 无独立测试盘，填盘有风险 | 未做（但"运行期目录不可写"已实测，见 E25） |

> E26/E27 的 race 与业务并发结果是 2026-11-24 的先前证据；race 已由当前树 E37 更新。E27 业务并发与历史容量数据未在当前树重跑。

## E34 初始构建与回归（2026-11-25；race 结论已由 E37 更新）

**环境与工具链**：Go 1.26.11 Windows/amd64 官方 archive 下载至系统临时目录，SHA-256 校验为 `71ae086f0137e665f855a811cc119152b6a783e0bbc1b906e709f46d80141eb0`（[Go 官方下载页](https://go.dev/dl/)）。设置 `GOENV=off` 避免继承本机旧配置，使用 `GOMAXPROCS=2`、`-p 1` 控制内存。

| 检查 | 结果 |
| --- | --- |
| `go test -p 1 -count=1 ./...` | **通过**；所有有测试的包全绿，包含 F-43/F-44/F-46 新增回归 |
| `go vet -p 1 ./...` | **通过**，无输出 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w'` | **通过**；临时 Linux ELF 23,277,730 bytes |
| 根/部署 Compose `config --quiet` | **均通过** |
| `git diff --check` | **通过**；未发现 whitespace error |
| Docker daemon | **不可用**；named pipe 缺失 |
| `go test -race` | **初始未执行**；当时缺少可用 gcc/clang，后续当前树 race 结果见 E37 |

## E35 本地控制台走查与备份恢复（2026-11-25）

**隔离方式**：按当前源码构建 Windows/amd64 临时二进制；复制配置到 `%TEMP%\web2api-ux-audit`，监听 `127.0.0.1:17891`，数据库、密钥、日志、任务目录和备份均处于同一临时目录。临时管理员口令仅用于该隔离实例；未使用现有 `data/`，未触碰 `baidu_check/`。

**控制台走查**：本地健康端点返回 200 / `storage=bbolt`；通过登录页进入总览并检查引擎状态、API 密钥、使用日志、在线调试说明、Agent 集成、API 文档、系统设置。在线调试页提示会真实访问引擎，因此没有提交查询。窄视图截图为浏览器工具提供的 608×480；导航和宽表可横向滚动。当前 in-app browser 不提供 viewport override，因此这次不能声称覆盖 390×844 或桌面完整视口；页面可读性/交互覆盖限于上述实测视图和路由。

**备份与恢复步骤/结果**：

1. 在隔离服务内创建 `audit-restore-check`，服务停止后执行 `admin-backup --data-dir <temp>/data --out <temp>/restore-check.db`，成功生成 24,576 字节快照。
2. 重启服务，在快照之后再创建 `restore-after-snapshot`；停止服务并执行 `admin-restore --data-dir <temp>/data --in <temp>/restore-check.db`。退出码 0，工具生成 `web2api.db.pre-restore-20261125-041220` 回滚副本。
3. 再次启动后退出并重新登录，登录成功；总览显示 1 个活跃 Key，密钥列表仅保留快照中的 `audit-restore-check`，快照后创建的 `restore-after-snapshot` 不存在。健康检查重新返回 healthy / bbolt。

**结论**：临时环境里的 CLI 快照、恢复、自动留档、服务重启和登录/Key 状态一致性均通过。该结果验证功能行为，不覆盖生产目录权限、异地备份保留或生产恢复窗口流程。

**清理状态**：本轮临时服务进程已停止。Windows 执行环境拒绝删除该临时目录，因此测试配置、快照和隔离数据库仍留在 `%TEMP%\web2api-ux-audit`；其中不含工作区生产数据，用户可在确认后手动删除。

## E36 依赖漏洞扫描（2026-11-25）

**工具/范围**：govulncheck v1.10.0；Go 1.26.11；漏洞数据库更新至 2026-11-24；扫描当前树 15 个根包、49 个模块及标准库。`govulncheck ./...` 退出码 0。

**结果**：Symbol Results 为 0 个可达漏洞。模块结果包含一条 `GO-2026-5932`：`golang.org/x/crypto/openpgp` 被 Go 漏洞库标记为不维护且不应使用；项目使用同一模块的 bcrypt，但当前代码没有导入/调用 OpenPGP 包。官方条目：[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)。按任务书的可达性口径 I7 通过；该模块级发现已在 F-28 保留，不把它误报成 0 条数据库记录。

## E37 当前树回归、race、集成与 lint（2026-11-25）

| 检查 | 命令/配置 | 结果 |
| --- | --- | --- |
| 全量测试 | Go 1.26.11；`go test -count=1 -p 1 ./...` | **通过**，全部测试包绿 |
| vet | `go vet ./...`（`GOFLAGS=-p=1`） | **通过** |
| race 单测 | `CGO_ENABLED=1 go test -race -count=1 -p 1 ./...`；MinGW UCRT POSIX；`GOTMPDIR=TMP=TEMP=D:\gotmp` | **通过**，无 DATA RACE |
| 严格集成 race | `WEB2API_INTEGRATION_TESTS=1 WEB2API_INTEGRATION_STRICT=1 go test -race -p 1 -count=1 -timeout=900s -tags=integration ./...`；系统 Chrome；固定公开查询 | **通过**；Baidu、Bing live smoke 与全包均绿，退出码 0 |
| lint | golangci-lint v2.13.2，`run --concurrency=1 --config .golangci.yml ./...` | 修复前 71 项（55 errcheck、2 govet、14 staticcheck）；修复后 **0 issues** |

lint 修复不仅是降噪：补上 CLI 备份/恢复与管理员重置的文件同步、关闭和输出错误处理；任务结果 append/sync、审计日志读取也检查关闭/读取错误。测试夹具与 HTTP 测试资源清理现在显式校验 Close；两处 `ForceCooldown` 错误不再被忽略。其余静态提示按 linter quick-fix 简化嵌入字段访问、布尔条件及 `reflect.Pointer` 常量。


## E39 当前树 staticcheck 复核（2026-11-25）

使用 Go 1.26.11、`GOENV=off`、`GOFLAGS=-p=1` 执行 `staticcheck ./...`，退出码 0、无诊断。E24 保留为历史修复记录；当前树结果以 E39 为准。

## E40 Docker Compose 静态安全基线（2026-11-25）

- 根 `docker-compose.yaml` 与 `deploy/docker-compose.yml` 均解析通过：设置隔离占位 `ADMIN_PASSWORD` 后，分别执行 `docker compose config --quiet` 与 `docker compose -f deploy/docker-compose.yml config --quiet`，退出码均为 0。
- 新增 `TestComposeManifestsShareContainerSecurityBaseline` 并通过（`go test ./cmd -run 'Test(ComposeManifestsShareContainerSecurityBaseline|DockerfileEnvContract|ContainerConfigShipsDefaults|DockerfilePackageInstallFailureCannotBeMasked)'`）。该测试锁定 healthcheck 路径、`cap_drop: ALL`、`no-new-privileges` 与 json-file 日志轮转（20MB × 3）。
- 静态审查确认两种 Compose 均继续将 7000 仅发布到 loopback、只读挂载配置并保留持久数据卷；未启用只读根文件系统，因为服务会在工作目录创建轮转日志。
- 当时 Docker engine 不可用，故本证据只覆盖静态基线；后续真实镜像/容器结果见 E60。

## E41 审计日志端到端读取（2026-11-25）

使用 Go 1.26.11 在当前树运行 `go test -count=1 ./platform -run '^TestAuditTrail$'`，通过。测试隔离创建平台存储，执行管理员登录和 API Key 创建，验证审计 JSONL 至少写入登录与密钥操作、JWT 保护的 `GET /api/audit` 返回完整字段，并断言 target/result 不泄露 `sk-w2a-` 密钥材料。

## E42 当前树全量回归（2026-11-25）

在 F-48 Compose 和 F5 审计日志文档更新后，使用 Go 1.26.11、`GOENV=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=1` 执行 `go test -count=1 ./...` 与 `go vet ./...`，两条命令均退出码 0。全量测试覆盖新增 Compose 契约测试和 `TestAuditTrail`。

## E43 Docker CI 运行时冒烟门禁已配置（2026-11-25）

- `.github/workflows/docker.yml` 的 PR/main Docker job 现通过 Buildx 构建并 `load: true` 加载 linux/amd64 镜像，随后启动容器、等待 Docker healthcheck healthy、请求宿主映射的 `/web-tool/health`，检查 `CapDrop` 包含 `ALL`、`SecurityOpt` 包含 `no-new-privileges:true`，并在容器内用 wget 验证 `https://example.com/` 的证书链。
- 冒烟管理员密码由 runner 的 `openssl rand -hex 32` 临时生成，不使用 secrets、不写入仓库；容器由 shell trap 清理。
- 使用 PyYAML BaseLoader 解析 workflow，确认 Buildx load 和 smoke step 存在；提取内嵌脚本用 Git Bash `bash -n` 校验通过；`git diff --check` 通过。Docker 官方 action 文档将 `load` 定义为 Buildx `--output=type=docker` 的简写（[build-push-action README](https://github.com/docker/build-push-action/blob/master/README.md)）。
- 该条记录只证明 workflow 配置/脚本可解析；本机真实 Docker runtime 已由 E60 补充。GitHub runner 仍须在推送后执行最终提交，才能确认远端门禁本身生效。

## E44 GitHub Actions 远端运行核验（2026-11-25）

只读检查 `gh run view`：当前 HEAD/远端 main SHA `5ceb33f9e2d5f579c4efdbc63dd8b0135ac268fa` 的 Docker workflow run [45470748893](https://github.com/xuboboo/web2api/actions/runs/45470748893) 成功，步骤只有旧版 `Build (no push)`，没有容器启动/HTTPS smoke。相同 SHA 的 CI run [45470748718](https://github.com/xuboboo/web2api/actions/runs/45470748718) 失败于 `TestPooledBrowserEngineForwardsCheckIndexed`，Chrome 报 `Missing X server or $DISPLAY`；失败后 vet、build、lint、govulncheck 均跳过。检查日志后未发现其他 `--- FAIL` 项。

这两次远端运行对应未修改的 Git HEAD，不含本地工作树改动；不能证明当前 F-49 修复或 E43 Docker smoke workflow 已通过。

## E45 F-49 修复后的 Go 1.26.11 全量验证（2026-11-25）

将 `TestPooledBrowserEngineForwardsCheckIndexed` fixture 显式设为 `IsHeadless:true` 后，先单独运行该测试通过，再用 Go 1.26.11、`GOENV=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=1` 执行 `go test -count=1 ./...` 和 `go vet ./...`；全包测试与 vet 均退出码 0。此为本地证据，GitHub CI 仍须在改动进入远端后复跑。

E45 补充：按远端 CI 后续步骤顺序在同一 Go 1.26.11 环境执行 `go build .`（exit 0）、`golangci-lint run --concurrency=1 --config .golangci.yml ./...`（exit 0，0 issues）、`govulncheck ./...`（exit 0，0 reachable、1 条未调用模块公告）。这仍是本机验证；GitHub CI 同 SHA 需要在修订进入远端后重新运行。

- F-49 的 headless wrapper 测试随后以 `CGO_ENABLED=1 go test -race -count=1 ./cmd -run '^TestPooledBrowserEngineForwardsCheckIndexed$' -v` 再通过（exit 0，无 race 报告；MinGW UCRT POSIX、ASCII GOTMPDIR）。

## E46 F-46 定向回归复核（2026-11-25）

使用本机 `D:\go-toolchain\go\bin\go.exe`（Go 1.27.4 windows/amd64，`GOENV=off`）执行：

`go test -count=1 ./core -run 'TestGuardedTitleProbeDoesNotUseRemoteProxy|TestFetchPageTitleRejectsPrivateTargets' -v`

两个测试均通过：标题探测查询会清除远端代理并启用私网防护；loopback、link-local、RFC1918 和 IPv6 loopback 目标被拒绝。本次仅为定向回归复核，不含 DNS 记录切换模拟或隔离代理端到端演练；F-46 的 DNS 重绑定实机演练仍未完成。Go 1.27.4 的本次结果不替代 E45 中 Go 1.26.11 全量测试证据。

## E47 当前工作树 Go 1.27.4 全量回归（2026-11-25）

使用 `D:\go-toolchain\go\bin\go.exe`、`GOENV=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=1` 执行 `go test -count=1 ./...`，全部有测试的包通过；随后执行 `go vet ./...`，退出码 0、无诊断。该复核是当前工作树的 Windows/amd64 Go 1.27.4 证据；不覆盖 Linux/Docker runtime，也不替代 CI 目标 Go 1.26.11 和远端 CI 状态确认。

## E48 admin-backup 输出别名保护（2026-11-25）

新增 `TestAdminBackupRejectsOutputThatAliasesSourceDatabase`，在 Windows/amd64 覆盖输出路径等于源 `web2api.db` 和输出为源文件硬链接两种情况。两种情况下备份均在打开源 bbolt/创建输出文件前拒绝；测试随后确认源文件大小与修改时间不变，且 bbolt 数据库仍可打开并读到原值。使用 Go 1.27.4 执行该定向测试通过；随后当前工作树 `go test -count=1 ./...` 全包通过，`go vet ./...` 退出码 0。该测试验证本地文件别名防护，不覆盖完整数据目录备份/恢复方案（F-14/F3 仍待完成）。

## E49 静态资源路径穿越回归（2026-11-25）

新增 `TestStaticMountRejectsConfigPathTraversal`，通过当前生产 Fiber 路由装配和 `app.Test` HTTP 请求覆盖普通 `..`、`%2e%2e`、编码斜杠以及固定前缀目录穿越到根目录的 4 种路径。四种请求均返回 404，响应体不包含 `admin_password:` 或 `platform:` 配置段。Go 1.27.4 下定向测试通过。此为当前代码的 HTTP 路由集成测试，不替代上线后对部署反代和真实监听端口的验证。

## E50 静态路径穿越修复后的完整回归（2026-11-25）

加入 E49 回归后，使用 Go 1.27.4 Windows/amd64、`GOENV=off`、`GOMAXPROCS=2`、`GOFLAGS=-p=1` 执行当前树 `go test -count=1 ./...`，所有有测试的包通过；`go vet ./...` 退出码 0、无诊断。

## E51 数据面认证、租户隔离与限额安全回归（2026-11-25）

定向命令 `go test -count=1 ./cmd ./platform ./core -run '^(TestIndexJobEndpointsAreScopedAcrossAPIKeysThroughAssembly|TestStaticMountRejectsConfigPathTraversal|TestAPIKeyGuardAuthAndQuota|TestDailyQuotaOneAllowsFirstCallAndRejectsSecond|TestProtectedEndpointsNeverAnswerJSONWithoutAuth|TestLoginAndAdminAuth|TestLoginLockoutAfterConsecutiveFailures|TestLoginLockoutCannotBeBypassedByRotatingIPs|TestAdminRateLimit429WithRetryAfter|TestQuotaExceededCarriesRetryAfter|TestPerKeyRateLimit|TestIndexJobIsScopedToOwner)$'` 在 Go 1.27.4 下通过。

新增的 `TestIndexJobEndpointsAreScopedAcrossAPIKeysThroughAssembly` 经过与服务一致的 `mountPlatformAndPanel` 装配，Key A 提交并完成 21 URL 异步任务后，Key B 读取状态、读取结果和取消均得到 404，任务列表不包含 A 的任务；Key A 自身读取成功。另新增 `TestDailyQuotaOneAllowsFirstCallAndRejectsSecond`，quota=1 的 Key 首次单单位调用返回 200，第二次返回 `429 quota_exceeded`，且账本仍为 1。定向回归同时覆盖无/错误 API Key、无 JWT、锁定与跨 IP 聚合、Retry-After、per-key rate limit 和静态目录 traversal。`go vet ./cmd ./platform ./core` 同样退出码 0。以上均为本地 HTTP 应用/组装层测试，不代替真实部署反向代理与生产配置验收。

## E52 当前树全量回归的 Windows 提交内存限制（2026-11-25）

E51 加入测试后，重新执行 `go test -count=1 ./...` 两次（第二次设置 `GOMAXPROCS=1`、`GOGC=20`、`GOMEMLIMIT=512MiB`、`GOFLAGS=-p=1`）均在 Go 测试二进制链接阶段因 `VirtualAlloc ... errno=1455` 失败；未出现测试断言失败。同期系统物理可用内存约 5.8 GiB，但 Windows 可用提交内存约 0.37–0.49 GiB，未发现残留 Go/link 进程。独立的 `go test -count=1 ./cmd` 全包通过；平台/核心安全定向用例通过，相关三包 vet 通过。当前整仓全量回归需在 Windows 提交内存恢复后重跑，因此当前树 I1 暂不视为已验证；E50 是新增 E51 测试前最近一次全仓通过证据。

## E53 核心业务验收项证据映射（2026-11-25）

- C1：E32/E33 有真实百度单条查询，返回 `indexed=true` 与 `match=exact`；E37 有较新 strict live smoke。
- C4/C5：E50 的 Go 全量测试包含 `TestIndexCheckSmallBatchStaysSynchronous`、`TestIndexCheckLargeBatchReturnsJob`；E32/E33 还记录真实 21 URL → 202/轮询。
- C7：E50 含同步 URL 校验与异步任务去重测试；E33 的真实流程记录 3 个重复 URL 去重为 1 个计费单元。
- C9：E50 含核心冷却语义测试；本轮单独的 `go test -count=1 ./cmd` 全包通过，包含 `TestCooldownManagementThroughAssembly`。

C2/C3 仍缺“各 3 条”的完整人工百度对照，现有报告样本是 2 条正样本与 2 条负样本；C6 已用本机 Microsoft Excel 实际打开同格式导出并精确读取中文与引号字段（E55）；C8 的 POST/GET 错误消息回显断言已补入，但编译阶段受 Windows 提交内存不足阻断（E54）。C2/C3/C8 继续保持未通过。

## E54 C8 非法 URL 错误消息回显回归（2026-11-25）

- 代码检查发现 `parseIndexCheckRequest` 的直接 HTTP 校验早于 `validateIndexCheckURLs`，原先只检查绝对 URL 形状；因此某些不支持 scheme 或单标签主机请求可能先走另一条消息路径。现已在解析阶段加入明确 scheme、绝对 URL 和公开域名校验，错误 `APIError.Message` 包含对应的 `urls[i]` 输入值。既有 core `JSONErrorMiddleware` 将该消息序列化为 `message` 字段，保留 `INVALID_PARAM` reason。
- `TestIndexCheckHandlerValidation` 已增加 POST 和 GET 非法 URL 的 HTTP 响应断言，要求 `message` 含输入 `not a url`，并要求 `INVALID_PARAM` 均带非空消息。
- 早先两次验证在编译阶段因 Windows `VirtualAlloc errno=1455`/内存分配失败，测试没有启动。2026-11-25 08:49 在提交内存恢复到约 880 MiB 余量后，以 Go 1.27.4、`GOMAXPROCS=1`、`GOGC=20`、`GOMEMLIMIT=512MiB`、`GOFLAGS=-p=1` 重跑：`TestIndexCheckHandlerValidation` 通过（`ok .../core 2.267s`），POST/GET 的具体 URL 回显与 `INVALID_PARAM` 断言均通过。因此 C8 通过。

## E55 C6 结果 CSV 经 Microsoft Excel 实际打开（2026-11-25）

- 本机 Office16 Excel 已通过 COM 实际打开临时 CSV。文件按 `WriteCSV` 的格式生成：UTF-8 BOM、CRLF、表头 `url,baidu_indexed,baidu_match,baidu_matched_result_url,baidu_total_results,baidu_error`，并包含中文 URL 与带逗号的匹配 URL。
- Excel 读取结果为 2 行 × 6 列；`Url` 精确等于 `https://example.test/路径?x=中文`，`MatchedResultURL` 精确等于 `https://example.test/a,b`，`Indexed=True`、`Match=exact`、`TotalResults=9`。中文无乱码，BOM 未进入首列表头，逗号字段未被拆列。
- E50 已覆盖真实结果端点返回 `text/csv`、BOM、CRLF 和序列化结构；E55 补足“Excel 实际打开”这一人工验收要求。因此 C6 通过。

## E56 C2/C3 当前百度排名复核尝试（2026-11-25）

- 用当前 `web2api.exe search baidu ... --raw` 对历史样本重新查询。`10048979`（`http://www.onlinedown.net/article/10048979.html`，微软 Win11 驱动路线）在该 URL 对应的标题查询中当前为百度第 1 名，结果域名为 `onlinedown.net`，标题与目标高度一致。
- 昨日保存的第 1 条负样本 `10048980`（`http://www.onlinedown.net/article/10048980.html`，KB5132057 安装失败）当前已翻转：同一百度查询返回目标标题 `...含 28 项改动_华军软件园` 于第 4 名。该现象再次证明收录结论是时间敏感快照，不能沿用昨日状态做上线验收。
- 随后尝试补充更多负样本时，百度对本机出口返回 `captcha detected`；验证立即停止，没有重试或把验证码失败伪造成“未收录”。移动版 `m.baidu.com` 虽有结果页且无验证码，但排序与产品桌面查询不同，例如同一正样本未在前三条出现，因此没有用它替代同一口径的人工对照。
- 移动页现场截图：[10048979](c23-live-20261125-1.png)、[10048980](c23-live-20261125-2.png)。截图仅用于证明查询词与页面状态，不据此宣称桌面排名。
- 结论：本轮新增了当前正样本与“负样本翻正”证据，但 C2/C3 要求的各 3 条同一口径人工百度对照仍未完成，继续保持未通过。

## E57 当前树完整 Go 回归恢复通过（2026-11-25）

- Windows 提交内存恢复到约 880 MiB 余量后，使用 Go 1.27.4 执行 `GOENV=off GOMAXPROCS=1 GOGC=20 GOMEMLIMIT=768MiB GOFLAGS=-p=1 go test -p 1 -count=1 ./...`，退出码 0。
- 所有有测试的包均通过：`baidu`、`bing`、`cmd`、`core`、`core/browser`、`core/fpcheck/detectors`、`core/region`、`mcp`、`platform`、`so360`、`sogou`；其余包无测试文件。
- 同一环境随后执行 `go vet -p 1 ./...`，退出码 0 且无输出。
- 此前 E52 记录了提交内存不足导致的链接失败；本次同树完整运行成功，I1 由未验证更新为通过。E54 的 C8 定向回归也在同轮通过。

## E58 当前树单元 race 复核（2026-11-25）

- 使用 Go 1.27.4、MinGW-W64、`CGO_ENABLED=1`、ASCII `GOTMPDIR=D:\gotmp` 重跑当前树 `go test -race -count=1 -p 1 ./...`。
- 一次全仓进程在 Windows 提交内存不足时于 `baidu`/`bing`/`cmd`/`core` 的链接阶段失败；其他 7 个包（`core/browser`、`core/fpcheck/detectors`、`core/region`、`mcp`、`platform`、`so360`、`sogou`）已通过。
- 为释放链接器内存，将失败包拆成独立 Go 进程重跑：`baidu`、`bing`、`cmd` 均通过；`core` 最终以 `GOGC=5 GOMEMLIMIT=256MiB` 单独运行通过（`ok .../core 8.065s`）。
- 汇总结果：当前树 11 个有测试的单元包均完成 race 检测且无 `DATA RACE`。这是同一树、同一检测器、不同进程的等价覆盖，不是把一次失败运行记成全绿。
- 严格集成 race 的完整结果仍沿用 E37；本轮因百度出口已触发验证码，没有再次发送 live 引擎请求。

## E59 内置完整恢复点往返与防篡改验证（2026-11-25）

新增 `admin-backup --full` 和完整归档自动识别的 `admin-restore`。完整恢复点包含 `platform.data_dir` 的全部普通文件/目录（数据库、`secret.key`、`index_jobs/`、审计日志等）和当前解析出的配置文件；清单记录每个条目的类型、大小、权限与 SHA-256。输出拒绝位于数据目录或已存在的路径，自定义 `index_jobs.dir` 位于数据目录外时拒绝生成以免静默漏掉任务状态。恢复先在目标父目录的隔离 staging 目录解包并验证路径、条目集合、类型、大小、checksum 以及 bbolt 的 `keys`/`usage`/`settings` contract bucket，全部通过后才替换目标目录与原配置，默认保留 `*.pre-restore-<时间戳>`。

在 Windows/amd64 Go 1.27.4 下执行：

`go test ./cmd -run 'TestAdminFullBackupAndRestoreRoundTrip|TestAdminFullRestoreRejectsPathTraversal|TestAdminFullRestoreRejectsManifestChecksumMismatch' -count=1 -v`

三个测试均通过：

- 往返测试从隔离源目录生成 1,827 字节完整恢复点，清单含 7 个文件；恢复后 `web2api.db` contract bucket、`secret.key`、任务 `job.json`/`results.jsonl`、`audit.log`/轮转日志与 `config.yaml` 内容均逐项一致。
- 目标目录原有的不同数据库、secret、独有文件与配置均完整留档为 `*.pre-restore-*`；恢复后的独有文件不再存在，证明不是简单覆盖合并。
- `../escaped.txt` tar 条目在写入 staging 前返回“路径越界”，隔离目录外未产生文件。
- 把 `audit.log` 清单 SHA-256 改为全零后，恢复在 staging 校验阶段返回“校验和不一致”。
- 另用当前源码构建的真实 `web2api.exe` 走通完整流程：启动隔离源实例创建真实 bbolt 数据库和 1 个 API Key，停服后加入任务文件并生成 2,510 字节完整归档（6 个普通文件，另含目录与 `manifest.json`）；归档列表包含 `data/web2api.db`、`data/secret.key`、任务 JSON/JSONL、`data/audit.log` 和 `config/config.yaml`。恢复后 secret、config、任务文件字节一致，目标实例重新启动返回 `healthy`/`bbolt`，管理员重新登录成功且 `keys_count=1`、Key 名称仍为 `bundle-audit-key`。

以 `go vet ./cmd` 同轮复测，退出码 0。该证据覆盖本机文件系统上的完整恢复点逻辑；Docker named volume 的实际限制和修复结果由 E60 补充，但干净 Linux 主机安装与 systemd 隔离 unit（H1/H2/H4）仍需独立验收。

## E60 Docker 镜像、最小权限与 named volume 完整恢复（2026-11-25）

**环境**：Docker Desktop 4.99.0，Linux engine 29.9.3，linux/amd64；镜像从当前工作树执行 `docker build` 成功，最终 manifest list SHA-256 为 `a37351b6fa56dc3e4cf3df7dab9ed946b61da0c67d22d0c530bf0cf573e1154c`。

**镜像与运行基线**：

- 最终镜像以 UID/GID 1001 `chrome` 运行；`/usr/src/app/data` 是空的初始化目录，镜像内没有 `web2api.db`、`secret.key`、审计日志或任务文件。
- 新建 named volume 挂载到 `/usr/src/app/data` 后，容器健康检查转为 healthy；`/web-tool/health` 返回 `storage=bbolt` 和四个引擎 `ready`。
- 运行容器 inspect 得到 `Config.User=chrome`、`HostConfig.CapDrop=["ALL"]`、`HostConfig.SecurityOpt=["no-new-privileges:true"]`。
- 在最终镜像内执行 `wget --spider https://example.com/` 成功，证明镜像的 CA bundle 与出站 TLS 证书链可用。
- fresh named volume 从启动到创建 API Key 均成功，证明镜像构建阶段的 `/usr/src/app/data` 所有权修复有效。

**named volume 完整恢复**：

- 首次按普通 path swap 恢复时，挂载点根目录返回 `device or resource busy`，因为 Linux 不允许 `rename` 一个 named-volume mount root。为此新增 `admin-restore --preserve-in <外部目录>`：先把旧卷内容复制到独立 preserve volume，再清空挂载点内子项、写回已校验 staging，写入失败时尝试从 preserve 回滚。
- 新增 `TestApplyStagedAdminBundleInPlacePreservesMountedVolumeContents`：在目标数据目录内先放旧 bbolt 和旧独有文件，恢复后新数据库、secret、任务文件与配置均替换成功；旧数据库和 `old-volume-only.txt` 完整保存在 preserve 目录。
- 定向执行 `go test ./cmd -run 'TestAdmin|TestApplyStaged' -count=1` 与 `go vet ./cmd`，均通过。
- 使用真实 9,052 字节完整归档恢复到全新 `web2api-audit-inplace-*` named volumes，退出码 0，旧卷标记 `old-volume-only` 出现在 `/preserve/restored-data.pre-restore-20261125-031332`。
- 恢复实例启动后返回 `healthy`/`bbolt`；管理员登录成功，`keys_count=1`，恢复出的 Key 名称是 `docker-volume-audit-key`；`secret.key` 与 `web2api.db` 的真实卷权限均为 0600。
- 删除并重建运行容器，继续挂载同一恢复数据卷和配置卷；health 仍为 healthy，登录仍成功，`keys_count=1` 且 Key 名不变，验证了容器重建持久性。
- 最终修订树复跑 `go test -p 1 -count=1 ./...`、`go vet -p 1 ./...`、Linux/amd64 构建、定向 `-race`、golangci-lint v2.13.2、staticcheck 与 govulncheck，均退出码 0；lint/staticcheck 为 0 条，govulncheck 为 0 个可达漏洞。

**边界**：该证据关闭本机 Docker 镜像构建、容器 HTTPS、最小权限、fresh volume、named-volume 完整恢复和重建持久性检查（I5/A2，Docker 部分的 F3）。远端 workflow 的独立结果见 E61；干净 Linux 主机、systemd unit 与 TLS 反代仍由 H1/H2/H4 和生产部署验收，不能用本证据替代。

## E61 最终代码 SHA 的远端 CI 与 Docker 门禁（2026-11-25）

- 分支 `codex/release-readiness-20261125` 已推送，PR #1 使用最终代码 SHA `628daff` 重跑门禁。
- CI run [45540860716](https://github.com/xuboboo/web2api/actions/runs/45540860716) 的 `test-build-lint` 通过（2m46s），覆盖 race 单测、vet、build、golangci-lint 与 govulncheck。
- 第一次远端 CI 在新增 commit 上暴露了真实流水线缺陷：runner 未安装 `govulncheck`，步骤以 127 失败。修复为固定安装 `golang.org/x/vuln/cmd/govulncheck@v1.10.0`，再从 `$(go env GOPATH)/bin` 执行；修复后的同 SHA 运行通过。
- Docker run [45540860782](https://github.com/xuboboo/web2api/actions/runs/45540860782) 的 `build` 通过（1m30s），覆盖最终镜像构建、非 root/无烘焙运行态断言、fresh named volume、health、CapDrop/ALL、no-new-privileges 与容器内 HTTPS。
- 这说明自动构建与容器门禁已经在最终代码上闭环；C2/C3 人工百度对照、F-46 隔离代理演练、H1/H2/H4 干净 Linux/systemd/反代和生产监控凭据仍是独立上线门槛。

## E62 F-46 DNS 重绑定端到端回归（2026-11-25）

标题探测现在通过 context 注入测试 resolver 和 dialer，生产默认仍是 `net.DefaultResolver` 与 `net.Dialer`。Windows/amd64 Go 1.26.11 下执行：

```text
go test -count=1 ./core -run 'TestFetchPageTitleDNSRebindingCannotReachPrivateTargetOrProxy|TestGuardedTitleProbePublicPathAndFallbackRemainUsable|TestGuardedTitleProbeDoesNotUseRemoteProxy|TestFetchPageTitleRejectsPrivateTargets' -v
```

退出码 0。重绑定用例第一次解析返回公网 `203.0.113.10`，第二次返回 loopback `127.0.0.1`；guarded transport 在 dial 前拒绝，dials、公网 fixture、私网 fixture 和远端代理命中数均为 0。另一个用例证明 DNS 持续公网时标题探测成功取到 `public article title`，代理仍未收到标题探测，调用方原 Query 的 `ProxyURL` 保持不变，后续 URL 短语回退仍可走原代理。由此 F-46 的“验证后重绑定到私网”端到端路径已闭环，不再只依赖清除代理字段的静态断言。

## E63 干净 Linux systemd、NGINX TLS、可信 IP 与升级恢复（2026-11-25）

**环境**：全新 `jrei/systemd-debian:12`、privileged systemd 容器、Debian 12、NGINX 1.22.1、Chromium 156、OpenSSL 3、linux/amd64 静态二进制；服务以 `web2api:web2api` 非 root 用户运行，监听 `127.0.0.1:7000`。

**干净安装与权限**：

- 首次按 `/etc/web2api` 默认 `0700 root:root` 放置配置时，systemd 以 `cannot read config ... permission denied` 反复重启。这是真实安装文档缺口，不是测试夹具；已把 INSTALL/DEPLOYMENT 改为 `0750 root:web2api` 目录、`0640 root:web2api` 配置、`0600 root:root` EnvironmentFile。
- 修正后 `/health` 与 `/ready` 均 200，官网 44,360 bytes、控制台 13,684 bytes 均 200；进程以 `web2api` 用户运行；NGINX 在本机 443 终结 TLS，上游仅监听 loopback 7000。
- 自签名临时证书 `CN=web2api-audit.invalid` 有效，实际协商 `TLSv1.3 / TLS_AES_256_GCM_SHA384`；`/health`、`/ready`、官网、控制台经 HTTPS 均 200，响应带 `Strict-Transport-Security: max-age=31536000; includeSubDomains`。

**可信客户端 IP**：从另一个 bridge 容器（真实地址 `172.17.0.2`）经 HTTPS 登录，同时伪造 `X-Real-IP: 203.0.113.250` 和 `X-Forwarded-Proto: http`。NGINX 覆盖转发头后，`/api/audit` 记录的 `login.success.ip` 是 `172.17.0.2`，既不是伪造值，也不是上游看到的代理 `127.0.0.1`。同轮 `TestTrustedProxyClientIP` 与 `TestUntrustedPeerCannotSpoofClientIP` 通过，覆盖受信 peer 接受合法头和不受信 peer 忽略伪造头。

**重启、崩溃与数据**：

- `systemctl restart` 后 MainPID 6305 → 6450，服务 active、health 200。
- 对进程执行 `kill -9` 后 systemd 在 5 秒重启，MainPID 6450 → 6479，active 且 health 恢复 200。
- 停服生成 `admin-backup --full`：9,791 bytes、4 个普通文件，manifest 格式 `web2api-full-backup` v1，含 `data/web2api.db`、`data/secret.key`、`data/audit.log`、`data/index_jobs/` 与 `config/config.yaml`，归档权限 0600。
- `admin-restore` 恢复到隔离 `/var/lib/web2api-restore` 与 `/etc/web2api-restore/config.yaml`；隔离端口 7001 上 `/health`、`/ready`、官网、控制台均 200，管理员登录成功，Key 数量 1、`total_units=0`，与备份前一致。
- 用停服前的旧提交 `27ab1c4` 与当前树分别构建 Linux/amd64 二进制：旧 SHA-256 `73d32c...7515d`、当前 `aee13a...f23`。按旧版启动 → 升级当前版 → 回滚旧版 → 再部署当前版的顺序执行，每一步 health 均 200、管理员可登录、Key 始终为 1，验证了二进制升级/回滚不破坏 bbolt 与 `secret.key`。

**边界**：该证据在干净 Debian 12 systemd 容器中完成，覆盖真实目录权限、systemd unit、NGINX TLS、可信来源、服务重启/崩溃和离线恢复；自签名证书与容器内单机路径不能替代生产域名、正式 CA、云主机重启或异地备份 provider 的验收。

## E64 C2/C3 同口径桌面百度三组正负样本（2026-11-25）

批量低频采样共完成 69 条当前页面，0 次 captcha；随后使用用户本机 Chrome、已登录的桌面百度、相同 `site:www.onlinedown.net` 范围逐条人工复核。查询词取目标页实际 `<title>`，而不是移动页或后台分类器结果。

| 结果 | 样本 | 人工桌面百度现场 |
| --- | --- | --- |
| true | `10049053` CloudFormation 新增 206 个资源类型，涉 88 个服务 | 精确标题结果第 1 项，域名华军软件园 |
| true | `10049051` qBittorrent 5.3.1 修 14 处缺陷，锁文件与磁力链同改 | 精确标题结果第 1 项 |
| true | `10049048` Humanity's First Exam 544 题，测 18 个模型一致性 | 精确标题结果第 1 项 |
| false | `10049057` Win11 内存占用降了 900MB，自带应用与 Office 减负 | 百度明确显示“抱歉，未找到相关结果” |
| false | `10049055` 业余爱好者用 Codex 筛 10 万颗恒星，200 多个信号待核验 | 百度明确显示无结果 |
| false | `10049054` Claude Artifacts 装成桌面应用，三档权限逐项可选 | 百度明确显示无结果 |

采样时判为 false 的 `10049071`（LibreOffice 收紧 AI 写代码）在约 30 分钟后的人工复核中已返回精确标题结果，说明本功能是时间敏感搜索引擎视角，不能把单次 false 当成永久官方状态。C2/C3 在当前同一时点的三正三负对照均通过；证据截图见 [c23-positive-20261125.png](c23-positive-20261125.png) 与 [c23-negative-20261125.png](c23-negative-20261125.png)。这仍不代表百度官方收录接口，结论时效性与已知漏判边界继续按 API 文档对外说明。
