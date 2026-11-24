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

## E10 受环境限制未执行的验证（禁止当作已通过）

| 项 | 原因 | 影响 |
| --- | --- | --- |
| `go test -race` | 本机无 gcc/clang（cgo 关闭） | 竞态结论只有 CI 覆盖，本地未跑 |
| `docker build` / 容器冒烟 | Docker 服务需管理员权限，`Start-Service com.docker.service` 被拒 | 镜像构建未实机验证；已用同参数交叉编译替代 |
| 业务端点压测（含引擎） | 会真实打百度，直接污染当前出口 IP | QPS/P95 仅有控制面数据 |
| 浏览器崩溃恢复 | 本机有 67 个用户自己的 Chrome 进程，杀进程可能影响用户工作 | 只做代码审查，未做破坏性注入 |
| 磁盘写满 | 无独立测试盘，填盘有风险 | 未做 |

