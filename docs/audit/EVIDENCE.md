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

## E11 受环境限制未执行的验证（禁止当作已通过）

| 项 | 原因 | 影响 |
| --- | --- | --- |
| `go test -race` | 本机无 gcc/clang（cgo 关闭） | 竞态结论只有 CI 覆盖，本地未跑 |
| `docker build` / 容器冒烟 | Docker 服务需管理员权限，`Start-Service com.docker.service` 被拒 | 镜像构建未实机验证；已用同参数交叉编译替代 |
| 业务端点压测（含引擎） | 会真实打百度，直接污染当前出口 IP | QPS/P95 仅有控制面数据 |
| 浏览器崩溃恢复 | 本机有 67 个用户自己的 Chrome 进程，杀进程可能影响用户工作 | 只做代码审查，未做破坏性注入 |
| 磁盘写满 | 无独立测试盘，填盘有风险 | 未做 |

