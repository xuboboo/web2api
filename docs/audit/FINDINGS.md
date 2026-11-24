# 上线审计：问题清单（滚动更新）

分级：**P0** 致命（必须立即修）· **P1** 高危（上线前必修）· **P2** 中危（建议上线前）· **P3** 低危（可迭代）

状态：`已修复` / `已确认待修` / `待复测` / `环境受限` / `已接受`

| ID | 级别 | 标题 | 状态 |
| --- | --- | --- | --- |
| F-01 | P1 | 收录查询的标题探测是 SSRF：`GuardPrivateNetworks` 在生产路径从未开启 | 已修复 |
| F-02 | P1 | Docker 构建上下文泄露运行态密钥（`data/secret.key`、`web2api.db`）且体积 772MB | 已修复 |
| F-09 | P1 | 异步收录任务越权：任意 API Key 可读/列/取消其他 Key 的任务 | 已修复 |
| F-03 | P2 | `docker-compose.yaml` 注释宣称「API 无鉴权层」，与实际（API Key + JWT）相反 | 已修复 |
| F-04 | P2 | CI 的 golangci-lint 版本无法 lint go1.26 模块（v1.64.8 由 go1.24 构建） | 已确认待修 |
| F-05 | P3 | 工作区残留 531MB 可执行文件 + 146MB QA 目录 | 已确认待修 |
| F-06 | P2 | 登录锁定仅按 (用户名, IP) 且纯内存，换 IP 可绕过、多实例失效 | 已确认待评估 |
| F-07 | P3 | 本机无 C 工具链，`go test -race` 无法执行（CI 有覆盖） | 环境受限 |
| F-08 | P3 | `go.mod` go1.26.0 / 开发工具链 1.27.4 / 镜像 1.26.11 三者不一致 | 待验证 |
| F-10 | P3 | 管理员（JWT）没有查询异步任务的接口，任务只有持 key 方可见 | 已确认 |
| F-11 | P2 | 完成任务永不释放内存：URL 列表常驻 + 常驻任务表无上限；队列满时还会并发跑第二个任务 | 已修复 |
| F-12 | P3 | `GET /v1/index/check/jobs` 每次轮询都遍历磁盘读取全部 job.json | 已确认 |
| F-13 | P1 | `security.data_plane_auth` 代码默认 `false`，与文档/配置注释相反：裸启即匿名开放检索 | 已修复 |
| F-14 | P2 | `admin-backup` 只含 `web2api.db`，不含 `secret.key` 与 `index_jobs/` | 已确认（文档已写明） |
| F-15 | P2 | 收录结论随时间变化，接口/文档未说明时效性，用户易误读为"永久状态" | 已确认（需文档口径） |
| F-16 | — | 核心业务准确性实测：正/负样本与时间变化复核 | 已验证（见 §F-16） |
| F-17 | P2 | 登录页文案与后端能力不符（"20 条上限"、"10/30/120 冷却档"） | 已修复 |
| F-18 | P2 | `index_jobs.dir` 无启动校验：部署健康，首个大批次才 500 | 已修复 |
| F-19 | P3 | 登录表单 `autocomplete=off`，密码管理器无法填充 | 已修复 |
| F-20 | P3 | 登录页无条件探测 `/api/me`，未登录时留下 401 控制台错误并占用未鉴权额度 | 已复核：**设计取舍，不修** |
| F-21 | P2 | 批量任务进度按 chunk(10) 跳：21 条任务前 2 分钟一直显示 0/21，像卡死 | 已修复（+实测） |
| F-22 | P2 | 数据库损坏时静默 JSON fallback：服务健康但 Key/账本全丢，监控需按 storage 告警 | 已确认（契约行为，文档加固） |
| F-23 | P2 | CI lint v1.64.8 + v1 schema 不兼容 go1.26；集成/发布 workflow 仍用 Go 1.24 | 已修复 |
| F-24 | P2 | `ENVIRONMENT.md` 漏掉多行 map 默认键 `stability.engine_cooldown.per_engine` | 已修复 |
| F-25 | P2 | 登录锁定按 (用户名,IP)，换 IP 可无限绕过失败阈值 | 已修复（+实机/回归） |
| F-26 | P1 | 非 loopback 绑定 + 公开默认管理员密码仅告警，仍启动 | 已修复（拒绝启动） |
| F-27 | P1 | 依赖 fasthttp v1.65.0 存在 GO-2026-4950（双编码路径鉴权绕过），已升至 v1.70.0 | 已修复（扫描复核 0 包漏洞） |
| F-28 | P2 | `golang.org/x/crypto/openpgp` 上游已标记 unmaintained（GO-2026-5932），模块级告警 | 已确认（不可达，不修） |
| F-29 | P3 | `..` 类前缀打到受保护 API 路径时落到静态兜底页返回 200 HTML（非 404），无数据泄露但会误导扫描器/监控 | 已记录（+回归固化） |
| F-30 | P2 | staticcheck 报 14 条：含 `sessionCache.mu` 未使用（锁陷阱）、探针错误死赋值、未使用常量/字段 | 已修复（staticcheck 0 条） |
| F-31 | — | 运行期任务目录不可写：500 + 0.01s 响亮失败，health 仍 200，恢复权限即自愈 | 已实测通过（非缺陷） |
| F-32 | P2 | 并发下业务延迟（p50 37.8s / p95 48.2s）超出清单阈值（15s/25s）：单引擎 12s/条串行导致的排队，非服务端缺陷 | 已量化（需写入容量规划/SLA） |
| F-33 | — | `-race` 竞态检测在本机打通（mingw + ASCII 路径/GOTMPDIR），11 包全绿无 DATA RACE | 已实测通过 |
| F-34 | **P1** | `docker-compose.yaml` 固定 `0.0.0.0` 却不设管理员密码 → 与本会话新增的 F-26 守卫冲突，容器退出 1 并重启循环 | 已修复（必需变量 + `.env` 忽略 + 文档） |
| F-35 | P3 | 容器 ENV 契约（`APP_BROWSER_PATH` 等）此前无任何测试兜底，键丢失会静默失效并在运行期才炸 | 已修复（新增 `cmd/dockerfile_env_test.go`） |
| F-36 | P2 | 控制台指标口径歧义：总览「1 / 今日 0 次」、使用日志「HTTP 200 / 次数 0」看不出业务失败 | 已修复（文案 + `0 · 未计费` + title）+ P3 遗留（engine_stats 未展示） |
| F-37 | P2 | F-25 改锁定语义后，设置页/API 文档三处仍写「同一 IP」，用户看到的是错的 | 已修复（全仓无 `同一 IP` 残留） |

---

## F-17 登录页文案与后端能力不符（P2，已修复）

**证据**（Playwright 实机截图 `ux-login-desktop.png`）
- 品牌区："**20 条** 单次批量收录判定上限" —— 实际 `index_jobs.max_urls=5000`，>20 条转异步任务。
- 同一区域："三档冷却 · **10 / 30 / 120** 分钟" —— 实际百度档位是 `[2, 10, 30]`（`stability.engine_cooldown.per_engine`）。

这是"接口能力已改、前端文案仍旧逻辑"的典型：用户按 20 条上限规划批量，会白白多提交几十次。

**修复**：改为"5000 条 · 单次批量上限，>20 条自动转异步任务"与"2 分钟起 · 按引擎分档，百度首档 2 分钟"。

**验证**：390×844 移动视口重新截图，新文案渲染正确、无溢出。

---

## F-18 任务目录不可写只在首个大批次暴露（P2，已修复）

**证据（实测）**
- 修复前：把 `index_jobs.dir` 指到不可创建路径，服务照常启动、`/health` 200；提交 21 条才返回
  `500 INTERNAL: cannot persist the job`，且无脏任务残留（这点是对的）。
- 修复后：启动阶段即失败，退出码 1，报错指名路径
  `index_jobs.dir 不可写(...)：mkdir ...: The system cannot find the path specified`。

**影响**：修复前属于"部署看起来健康、客户第一次大批量才炸"的隐雷，排障成本极高（要等业务触发才发现）。

**修复**：新增 `core.ProbeIndexJobDir`（MkdirAll + 临时文件写入）并在 `serve` 启动时调用。

**验证**：上述两条实测；正常目录启动后 `/health` 200，任务提交路径不受影响。

---

## F-19 登录表单阻止密码管理器（P3，已修复）

Playwright 控制台告警：`[DOM] Input elements should have autocomplete attributes (suggested: "current-password")`。
`<form autocomplete="off">` + 输入框无属性 → 密码管理器不能填充，管理员每次手输。

**修复**：form 改 `autocomplete="on"`，用户名 `autocomplete="username"`，密码 `autocomplete="current-password"`。
**验证**：移动端截图页面正常；控制台告警消失（下次抓取可复核）。

---

## F-20 登录页 401 探测：查证后判定"不修"（P3，设计取舍）

**初始判断**：登录页加载即请求 `GET /api/me`，未登录时控制台留一条红色 `401 (Unauthorized)`，并消耗一个"未鉴权 IP 限额"令牌。看起来应该"有 token 才探测"。

**复核后推翻**（关键证据）：
- 管理员会话用的是 **HttpOnly + SameSite=Strict** 的 cookie（`platform/handlers_admin.go:191-200`）。
- HttpOnly 意味着前端 JS **读不到**该 cookie，因此"本地有没有 token"无法反映"服务端有没有会话"。
- 若按原方案改成"仅当内存里有 token 才探测"，**F5 刷新后会丢失登录态**（内存 token 天然不持久）——这是比一行控制台错误严重得多的回归。

**结论**：该探测是 httpOnly 会话恢复的**唯一**手段，保留。残余成本仅为：每次冷加载一条 401 日志行 + 一个未鉴权限流令牌（默认 30/分钟，正常使用无感）。

**可选改进（未实施，避免为观感加接口）**：新增 `GET /api/auth/session` 返回 `200 {"authenticated":false}` 而不是 401，可消除控制台红字；但会引入新的公开端点与契约，收益仅是"日志好看"，不建议在上线前做。

---

## F-21 批量任务进度像卡死（P2，已修复）

**证据（实测，21 条百度任务，12 秒/条）**
```
16:05:19 status=running done=0/21
16:06:01 status=running done=0/21
16:07:01 status=running done=0/21     <- 整整 102 秒没有任何前进
16:07:14 status=running done=10/21    <- 一次跳 10 条
```

**根因**：`RunIndexCheckUnits` 是**按块**处理的（块大小 = `index_jobs.chunk`，默认 10），函数返回后 runner 才逐行累加 `Completed/Indexed`。于是"进度可见"的粒度 = 10 条 × 12 秒 = **2 分钟一跳**。对 800 条的大批量只是"跳得粗"，但对 21 条这种小批量，用户前两分钟看到的是"0/21、ETA 不动"，第一反应是任务卡死了。

**影响**：纯 UX/可观测性问题，不影响数据正确性（`completed` 最终准确）。但它直接影响"批量操作是否有进度反馈"这条上线检查项。

**修复**：`Chunk` 默认 10 → **1**（`core/index_job_manager.go` + `cmd/root.go` + `config.yaml` + 文档）。落盘是 ~1KB 的原子重命名，5000 条任务总共 5000 次极小写入，相对于 12 秒/条的引擎节奏完全可忽略。

**验证（同一命令对比）**
```
修复后：done=1/21 -> 3/21 -> 4/21(idx=1) -> 5/21 -> 6/21(idx=2) -> ...  每 ~12 秒 +1
```
`docs/audit/EVIDENCE.md` E13 有完整对照表。

**兼容性**：显式配置了 `index_jobs.chunk: 10` 的部署行为不变（配置优先）；本仓库 config.yaml 已同步为 1。
**回滚**：把默认值改回 10 即可。

---

## F-22 数据库损坏时静默降级（P2，契约行为，文档加固）

**证据（实测）**：把 `web2api.db` 截成 512 字节再启动：
- `/health` 200 且 `storage=json`；
- 登录成功、无 JWT 访问管理面仍 401；
- 原 `web2api.db` 保持 512 字节，新建 `web2api.json` 从空 keys/usage 开始。

**根因**：`platform.New` 在 bbolt 打开失败且**不是文件锁**时，按既有产品契约回退到 JSON store（`platform/platform.go:123`，`TestNewFallsBackToJSONWhenBoltBlocked` 覆盖）。设计意图是"数据库不可用时业务继续"，但它只保证**接口可用**，不保证**数据不丢**。

**影响**：如果只看 `/health` 的 HTTP 200，会误以为系统正常，实际上 API Key、配额、用量账本已经全部从当前 store 消失；客户端拿旧 Key 调用会连续 401，而管理员看到的是一个空平台。

**处置（不擅改数据契约）**：
- 保留 fallback（这是明确的产品契约，突然改成拒绝启动会影响既有部署）；
- `docs/OPERATIONS.md` 增加：`/health.storage=json` 必须按 P1 告警，先停服、隔离数据目录、从最近备份恢复；
- 本机无独立测试盘，未做磁盘写满注入；数据库损坏这条异常已实测。

**回滚/恢复**：无需回滚代码；停止服务，把损坏目录移走，用 `admin-restore` 恢复 `web2api.db`，并补回 `secret.key`。

---

## F-23 CI lint/Go 版本与 go.mod 不一致（P2，已修复）

**证据**
- `go.mod`: `go 1.26.0`
- `.github/workflows/ci.yml`: `golangci-lint-action@v6` + `v1.64.8` + `.golangci.yml` v1 schema（`disable-all` / `gosimple`）
- `.github/workflows/integration.yml` / `release.yml`: `go-version: "1.24"`

**影响**：v1.64.8 无法解析 Go 1.26 的导出数据；集成测试与发布构建在低于模块声明的工具链上跑，结果不能代表真实 CI 主链路。

**修复**（依据官方 [迁移指南](https://golangci-lint.run/docs/product/migration-guide/)）：
- `.golangci.yml` → `version: "2"`、`go: "1.26"`、`linters.default: none`；移除已并入 staticcheck 的 `gosimple`。
- CI action → `golangci-lint-action@v8`，lint 版本固定 `v2.13.2`。
- 增加 `govulncheck ./...` 步骤。
- integration/release workflow 的 Go → 1.26。

**验证**：v2.13.2 已安装并成功读取 v2 配置（不是"未运行就宣称修好"）。完整 lint 在本机被 Windows 分页文件限制中止，错误为 Go 1.27 导出数据版本不兼容 + `fork/exec compile.exe: The paging file is too small`；CI 有 Linux + 完整内存，作为最终验证关卡。

**回滚**：恢复三个 workflow 和 `.golangci.yml` 的上一版本。

---

## F-24 环境文档漏项（P2，已修复）

自动契约检查发现：`cmd/root.go` 有 76 个 `SetDefault` 键，`docs/ENVIRONMENT.md` 只有 75 个。漏的是**多行 map**：
`stability.engine_cooldown.per_engine`（百度覆盖为 `[2,10,30]`）。

**根因**：文档生成脚本的正则只抓单行 `v.SetDefault(...)`，遇到跨行 map 就漏掉；而运行时 Viper 是正常读到的。

**修复**：手工补上该键，并在文档头部注明"75 个单行 + 1 个多行 map = 76 项"。契约检查脚本改为直接扫描所有 `v.SetDefault("key"` 并逐项断言文档包含。

**验证**：`SetDefault keys: 76, absent from ENVIRONMENT.md: 0`。

**兼容性**：纯文档修复，无运行时影响。

---

## F-25 登录锁定可换 IP 绕过（P2，已修复）

**证据**：原实现 `loginFails map[loginAttemptKey]*...`，键为 `{username, ip}`。攻击者用 5 个不同 IP 各错一次，每个桶都只有 1 次失败，永远到不了阈值；正确密码可从第 6 个 IP 登录。

**根因**：把"防单 IP 骚扰"的限速键误用成"防账号接管"的锁定键。对象是**单个管理员账号**，不是每个 IP 一个账号。

**修复**：锁定键改为 `username`，跨 IP 聚合失败次数；IP 保留在日志/审计与有界诊断集合（≤16）中。成功登录清空该用户计数，过期窗口自动清空。

**实测**：5 个不同 XFF 各 401，第 6 个 IP 的正确密码 → 429。回归 `TestLoginLockoutCannotBeBypassedByRotatingIPs`。

**取舍**：攻击者可短暂锁死管理员（默认 15 分钟），但无法通过分布式爆破接管账号；对单管理员平台，"防接管"优先于"防骚扰"。

---

## F-26 公网默认密码只告警不拦截（P1，已修复）

**证据**：仓库 `config.yaml` 默认 `host: 0.0.0.0` + 公开默认管理员密码；原实现 `logSecurityPosture` 只 Warn，服务仍监听。任何拿到仓库的人都能登录公网管理面。

**根因**：安全默认值依赖运维自觉，没有 fail-closed 边界；而"部署成功"会掩盖凭据未改。

**修复**：`mountPlatformAndPanel` 在监听前调用 `enforceAdminPasswordSafety`：
- loopback 允许默认密码（本地开发）；
- 非 loopback + 默认密码 → **拒绝启动，退出码 1**，提示用环境变量或配置修改；
- 环境变量/配置已改，或控制台曾改密（custom marker）→ 正常启动。

**实测**：
```
127.0.0.1 + 默认密码       → 启动成功
0.0.0.0   + 默认密码       → 退出码 1，安全启动被拒绝
0.0.0.0   + 强密码环境变量 → 启动成功，/health 200
```
回归：`TestUsesDefaultAdminPasswordHonorsCustomMarker`。

**兼容性**：依赖公网默认密码的部署会被拒绝——这是有意的安全收紧；loopback 开发与已改密部署不受影响。临时回退：绑 127.0.0.1，或设置强密码。

---

## F-27 依赖存在鉴权绕过漏洞 fasthttp GO-2026-4950（P1，已修复）

**证据**：`govulncheck ./...` 在修复前报告：

```
Vulnerability #1: GO-2026-4950
    Authorization bypass via double-encoded paths in github.com/valyala/fasthttp
    Found in: github.com/valyala/fasthttp@v1.65.0
    Fixed in: github.com/valyala/fasthttp@v1.70.0
```

**为什么对本项目重要**：本项目所有路由、鉴权中间件（`jwtGuard`、`X-API-Key`、限速、`platform` 前缀挂载）都建立在 fiber→fasthttp 的 URI 路径解析之上。"双编码路径"是典型的**路由/中间件绕过**手法：`/%252e%252e/` 之类在不同解码层得到不同结论，可能让请求匹配到不该匹配的路由。govulncheck 的符号可达性分析判定当前代码不直接触发，但 HTTP 层的路径规范化语义由 fasthttp 统一裁决，这类漏洞不应以"没直接调用"放过。

**修复**：`go get github.com/valyala/fasthttp@v1.70.0`。fiber v2.52.16（v2 线最新）要求 `fasthttp >= v1.51.0`，MVS 直接抬到 v1.70.0 即可，无需改 fiber。

**验证**：
- 修复后 `govulncheck ./...`：`0 vulnerabilities` in imported packages；
- `go build ./...` / `go vet ./...` 通过；
- `go test -p 1 ./...` 12 包全绿；
- 严格集成测试 11 包全绿。

**回滚**：`go get github.com/valyala/fasthttp@v1.65.0`。回滚会把该 P1 一起带回来，不建议。

---

## F-28 x/crypto 模块级告警（P2，已确认不修）

`govulncheck` 的 Module Results 仍列 GO-2026-5932：`golang.org/x/crypto/openpgp` 上游已标记 unmaintained / unsafe by design。

**判断**：本项目只用 `x/crypto/bcrypt`，从未 import `openpgp`；govulncheck 明确 `0 vulnerabilities in packages you import`，该告警不可达。要彻底消除只能等上游把 openpgp 拆出模块或升级 `x/crypto` 到移除版本，**不应为此改动依赖**。

**记录价值**：下一个人看到 Module Results 有一条时，不必重新排查一遍。

---

## F-29 `..` 前缀路径落到静态兜底返回 200（P3，已记录 + 回归固化）

**证据**（E23，fasthttp 升级后实测）：

| 请求 | 状态 | 类型 |
| --- | --- | --- |
| `GET /web-tool/api/keys` | 401 | JSON `unauthorized` |
| `GET /web-tool/%2e%2e/api/keys` | **200** | **text/html**（静态 SPA 兜底，643 字节） |
| `GET /web-tool/..%2f/api/keys` | **200** | **text/html** |
| `GET /web-tool/%252e%252e/api/keys` | 404 | JSON `not_found` |

**影响**：**没有数据泄露**——受保护 handler 未被调用，响应体是静态页面且不含任何 Key 字段。真正的代价是运维噪音：
- 漏洞扫描器会把"200 on /api/keys"列为可疑，浪费排查时间；
- 以 `/api/*` 做探活的监控会**假通过**（200 但不是 API 响应）。

**为什么不改路由**：静态兜底改成"含 `..` 一律 404"会动到站点路由匹配，属于低收益高回归风险的改动（现有 UX 与 `nonexist → 404` 的行为已经不一致，说明兜底逻辑本身有特例）。按"稳定性优先"原则，本轮只固化不变量：

`TestProtectedEndpointsNeverAnswerJSONWithoutAuth`（7 端点 × 8 前缀）断言**任何编码下未鉴权都不可能拿到 JSON 200**。若将来有人把静态兜底改成 JSON 兜底（或错误地给兜底挂上 API），测试立即失败。

**后续建议（不阻塞上线）**：给 `/api`、`/v1` 前缀加一条前置中间件，命中即返回 JSON 404，避免落到静态兜底；同时把监控探活改为校验响应体结构而不是只看状态码。

---

## F-30 静态分析 14 条（P2，已修复）

**背景**：`golangci-lint` 在本机因 Windows 分页/导出数据版本问题无法完整运行，改用同源分析器 `staticcheck 2026.2.2` 直连类型信息，实机跑通并修到 **0 条**。

**修掉的实质问题（非风格）**：

1. **`platform/sessions.go` `sessionCache.mu` 未使用**——最危险的一条。结构体里声明了互斥锁却从不加锁，实际保护来自 `Platform.sessionMu`。这种"看起来自保护"的结构是**漏加锁的温床**：下一个人新增一个缓存访问路径时很可能不想加锁。已核对 `known`/`revoked` 的 11 处访问**全部**在 `p.sessionMu` 之下，删掉冗余字段并写明"由 Platform.sessionMu 保护"。
   > 在本机无法跑 `-race` 的前提下，这类问题只能靠静态分析发现。
2. **`core/index_check.go` 探针错误死赋值**：`probeErr` 先赋 `errProbeIncomplete`，随后无条件被真实 `err` 覆盖。行为正确（`FinishProbe(nil)` 表示引擎有应答），但死代码会让人误以为存在"提前返回时保留 incomplete"的保护。已简化为 `gate.FinishProbe(err)` 并注明 `server.go` 那份才需要防御性默认值（那里能提前返回）。
3. **未使用常量 `indexCheckWorkerLimit`**：注释称"为按它计算批大小的调用方保留兼容"，但它是**未导出**常量，包外无法引用——兼容性说法不成立，删除。
4. **测试桩未使用字段 `forceErrFor`**：会误导读者以为支持按引擎注入错误，删除。
5. 其余：错误串大写（ST1005）、`bolt.ErrTimeout` 弃用（改 `bbolt/errors.ErrTimeout`）、测试里 7 处赋值未使用。

**验证**：`staticcheck ./...` exit 0；`go vet ./...` 通过；11 包测试全绿。

**回滚**：改动均为删除死代码/等价替换，逐条可独立回滚，无兼容性影响。

---

## F-32 并发下业务延迟超出清单阈值（P2，已量化，非缺陷）

**证据**（E27，8 次真实百度查询、并发 4）：

```
status: {200: 8}     latency p50=37.82s  p95=48.16s  mean=33.88s
```

而 `RELEASE_CHECKLIST` §E 写的是「P50 < 15 s、P95 < 25 s」。

**根因**：不是服务端算力问题。单引擎按 `interval 12s` 串行发查询，再加收录判定的标题探测与重试预算，所以 4 个并发请求排队在同一引擎的节拍后面，延迟按并发数近似线性放大。串行口径下单条就是 11.5–12.2 s（21 条 241 s、30 条 367 s），**符合 <15 s 目标**。

**为什么必须单列**：如果只把 37.8 s 写进报告而不解释，读者会误判为性能缺陷并去"加机器"——而进程 RSS 全程 25–35 MB、CPU 不是瓶颈，加机器不会改善任何一个数字。

**处置（不改代码）**：
1. `RELEASE_CHECKLIST` §E 拆成「串行口径」与「并发 4 口径」两行，并把并发口径标 ❌ + 原因；
2. 明确容量口径：**单引擎吞吐 ≈5 条/分钟**，并发只影响排队深度，不提高吞吐；
3. 想真正提高吞吐只有两条路：多引擎并行判定（bing/sogou/360），或加出口 IP + 代理池；
4. 对外 SLA 若承诺"单条 <15 s"，必须限定"低并发/独占引擎"。

**待产品决策**：是否给并发请求返回 `Retry-After`/排队位置，让用户预期可视化（当前是静默排队）。属体验优化，不阻塞上线。

---

## F-33 `-race` 竞态检测在本机打通（非缺陷，能力补齐）

见 E26。三步环境修正：安装 mingw（正版 POSIX/UCRT 组合）、把工具链放到**纯 ASCII 路径**、把 `GOTMPDIR/TMP/TEMP` 指向 **ASCII 目录**。

结果：`go test -race -count=1 -p 1 ./...` 11 包全绿、无 DATA RACE。

**意义**：并发正确性从"代码审查 + staticcheck"升级为"三个独立手段都通过"；`sessionCache.mu`（F-30）这类锁陷阱今后可以被竞态检测器兜住。

---

## F-34 compose 与默认密码守卫冲突，容器会进重启循环（P1，已修复）

**这是本会话自己引入的回归**，也正是审计该抓的东西：F-26 让服务在"非 loopback + 默认管理员密码"时拒绝启动，而 `docker-compose.yaml` 恰好把 `WEB2API_SERVER_HOST` 固定成 `0.0.0.0`、又**没有**设置管理员密码。

**影响链**：

```
docker compose up -d
  → 容器内 host=0.0.0.0 + config.yaml 默认密码
  → enforceAdminPasswordSafety 拒绝启动，进程 exit 1
  → restart: unless-stopped 反复重启 → 无限重启循环，日志被刷屏
```

**为什么容器里躲不开**：端口映射必然要求进程绑 `0.0.0.0`，所以"loopback 可用默认密码"的豁免在容器里永远不成立。修复不是放宽守卫（那会退回 F-26 的漏洞），而是**让部署侧必须显式提供密码**。

**修复**：

1. `docker-compose.yaml` 增加必需变量（缺值即解析期报错，不会进重启循环）：
   ```yaml
   WEB2API_PLATFORM_ADMIN_PASSWORD: ${ADMIN_PASSWORD:?set ADMIN_PASSWORD to a strong admin password before 'docker compose up'}
   ```
2. `docs/DEPLOYMENT.md` 说明该变量对**每条** compose 命令都必需，并给出 `.env` 方案（含 `install -m 600` 与 `chmod` 提示）。
3. `.env` 加入 `.gitignore`（否则运维照文档创建 `.env` 时可能把管理员密码提交进仓库）；同时加入 `.dockerignore`（避免进镜像层）。

**实测（无需 daemon）**：

```
docker compose config                        → 明确报错 + exit 1（不再是重启循环）
ADMIN_PASSWORD=*** docker compose config      → 正常解析，env/ports/volumes 全部符合预期
```

**兼容性/回滚**：这是**有意的破坏性变更**——已有部署升级后必须导出 `ADMIN_PASSWORD`（或改用挂载的 config.yaml 里的强密码），否则 compose 直接报错。回滚方式是删掉那一行环境变量，但会把 F-26 的漏洞带回来，不建议。

---

## F-35 容器 ENV 契约缺少测试兜底（P3，已修复）

**问题**：镜像完全依赖 Viper 的 `BindEnv` 把 `WEB2API_<KEY>` 绑到配置键（`cmd/root.go` 的 `v.AllKeys()` 循环）。但 `AllKeys()` 只包含**已知键**，所以任何键一旦丢掉 `SetDefault`，对应环境变量就**静默失效**。

具体后果：`WEB2API_APP_BROWSER_PATH` 失效 → `app.browser_path` 为空 → Rod 在运行时去下载 Chromium → 在无外网、非 root 的镜像里必然失败，而且只有**真正跑查询时**才暴露。

**修复**：新增 `cmd/dockerfile_env_test.go`：

- 断言 `WEB2API_APP_BROWSER_PATH`、`WEB2API_SERVER_HOST`、`WEB2API_SERVER_PORT`、`WEB2API_PLATFORM_ADMIN_PASSWORD` 都能绑定到预期配置值；
- 断言 `config.yaml` 仍可读且保留 `server:` / `platform:` / `app:` 三段（镜像 `COPY` 它并依赖它）。

**验证**：`go test -run 'TestDockerfileEnvContract|TestContainerConfigShipsDefaults' ./cmd/` 通过；全量回归 11 包全绿。

---

## F-36 控制台指标口径歧义：看不出「业务失败」（P2，已修文案 + 1 条 P3 遗留）

**证据**（E29，真实 Chrome 登录后走查）：

- 总览：卡片大字 `1`（调用次数），下方小字 `今日 0 次`（计费次数）——实测出现过「1 … 今日 0 次」，读起来像 bug；
- 使用日志：一行 `HTTP 200 / URL 数 1 / 次数 0 / 12.0s`。实际那次请求引擎返回 `engine_internal`、0 计费。**界面没有任何位置能看出来它失败了**。

**根因**：两处文案都用「次」表示两个不同量（调用次数 vs 计费次数），且使用日志只有 HTTP 状态列——而 `outcome`（billed/engine_failed/rejected）早在 API 里返回了，前端没用。

**修复（只陈述事实，不美化）**：

1. 总览小字改「计费 N 次」，与大字「调用次数」区分；
2. 使用日志 `次数 0` 渲染为弱化的 `0 · 未计费`，`title` 说明「所有单元都返回 error（或引擎侧失败）」；
3. 详情面板 JSON 里带出 `outcome` 字段。

**自我纠错**：第一版我给使用日志加了「结果」列并把 `billed` 显示成"成功"。核对 `platform/model.go` 后确认 `billed` = 到达引擎且 HTTP 200，**那条 200+引擎错误的记录正是 billed**，显示"成功"等于撒谎，已撤回。

**遗留（P3，需后端配合，未做）**：`UsageRecord.EngineStats`（每引擎 calls/ok）虽在结构体里，但本用例 `/api/usage` 响应里缺失（`omitempty` 未落库或未回传），所以界面无法直接显示"百度 1 次调用 / 0 次成功"。建议后续把 engine_stats 落库并在日志详情里展示——那才是"失败原因"的最终答案。

---

## F-37 锁定语义改了，用户可见文案没改（P2，已修复）

**又一次「改了机制忘了改说法」**：F-25 把登录锁定从「用户名+IP」改成**按用户名跨 IP 聚合**，但控制台里三处用户可见文案仍写着"同一用户名在**同一 IP** 下连续 5 次密码失败"：

- `web/admin/assets/app.js` API 文档页（`POST /api/auth/login` 说明）
- `web/admin/assets/app.js` 安全机制卡片（登录防爆破）
- `web/admin/assets/app.js` 系统设置页「安全机制」列表

对运维的实际影响：安全说明告诉管理员"换 IP 会重置计数"，而实际不会——出事时会按错误的模型排查（以为攻击者换 IP 就能继续试，或以为自己被误锁是 bug）。

**修复**：三处统一为「同一用户名连续 5 次密码失败即锁定 15 分钟（按用户名跨 IP 聚合，换 IP 不会重置）」；全仓 `grep '同一 IP'` 已无残留（`docs/OPERATIONS.md`、`docs/RELEASE_CHECKLIST.md` 此前已正确）。

**教训（与 F-34 同类）**：凡是改动**用户可见语义**（安全策略、限额、计费口径），必须同时搜索面板文案、API 文档页与 markdown 文档。这类不一致不会让测试变红，只会让人在故障时按错的模型处理。已在 `docs/RELEASE_CHECKLIST.md` 增加一条复查项。

---

## F-13 数据面鉴权默认值不安全（P1，已修复）

**证据**
- `cmd/root.go:664` 原文 `v.SetDefault("security.data_plane_auth", false)`。
- 同一份代码 `cmd/root.go:500` 把它列为受支持配置键；`config.yaml:139` 的注释写着"出厂安全姿态(默认翻转): true 时 … 既有部署如依赖匿名访问需显式改回 false"。
- 也就是说：**用仓库自带 config.yaml 部署是安全的（true），但用最小配置/环境变量裸启 `web2api serve` 时默认是 false**——`/{engine}/search`、`/stats/*` 全部匿名可用。

**影响**：任何"自己写个 config 只配端口"的部署、以及未来可能的 systemd 直接起二进制场景，都会把检索能力公开给互联网。它同时是**放大器**：匿名可用的检索端点最容易被批量抓取，直接消耗出口 IP 的百度风控额度，把付费用户拖下水。

**根因**：安全开关的默认值与产品宣称的安全姿态不一致，且没有任何测试锁定这个默认值。

**修复**：默认改为 `true`（与 config.yaml、与文档、与"数据面必须带 Key"的产品定位一致），并在代码注释里写明回退方式。

**验证**：`go test -p 1 -count=1 ./...` 全绿（没有任何测试依赖匿名默认）；实机验证无 Key 请求 `/v1/index/check`、`/v1/index/check/jobs`、`/v1/quota`、`/v1/engines`、`/stats` 均为 401（见 EVIDENCE E1）。

**兼容性**：依赖匿名访问的既有部署需显式 `security.data_plane_auth: false`，这是**有意的安全姿态收紧**；CLI 的 `search` 子命令不走 HTTP，不受影响。

**回滚**：把该行改回 `false`。

---

## F-14 备份不含密钥与任务目录（P2，已确认）

**实测**：`admin-backup` 产出 28 KB 文件；`admin-restore` 恢复出 `web2api.db`；启动后登录成功、`acc-sample` 这个 Key 仍在且 `used_today=4` 计数完好 —— 备份恢复链路**可用**。

**缺口**：
- `secret.key` 不在备份内 → 恢复到新机器后所有管理员 JWT 失效（需重新登录）。API Key 不受影响（哈希在 DB 里）。
- `index_jobs/` 不在备份内 → 任务历史与"运行中任务"丢失，无法续跑。

**处置**：不擅自改备份格式（会动数据契约）；已在 `docs/OPERATIONS.md` §3 明确"备份必须覆盖 web2api.db + secret.key + index_jobs/ + config.yaml"，并在 `RELEASE_CHECKLIST.md` F3 设为检查项。

---

## F-15 / F-16 核心业务准确性：实测与边界（P2 文档口径）

### 实测证据（本次）

| 样本 | 结果 |
| --- | --- |
| 早前 30 条 11-24 文章任务 | 全部 `indexed=false`，其中 4 条 `total_results=1`（可疑） |
| 复核这 4 条（间隔约 1 小时） | **`indexed=true, match=exact, total_results=8~10`** |
| 另外 2 条当时未收录的老文章 | 仍 `indexed=false`（`total=9/10`，无匹配） |
| 767 条历史样本 | `indexed` 320 (41.7%) / `not_indexed` 447；判为收录的标题相似度最小 0.724、中位数 1.000（阈值 0.72 起效，且没有"低分混过"的堆积） |
| 近 132 条（11-24） | `indexed` 29 (22.0%)，与"新发文章尚未收录"的预期一致 |

### 结论（必须写进用户文档的口径）

1. **判定是"百度搜索结果视角"的快照，不是站长平台官方状态**：`indexed=true` 的含义是"用标题检索、前 10 条里出现同站且标题相似的页面"。它足够可靠地反映"能不能被搜到"，但**不能**等同于"百度已收录该 URL"。
2. **结论随时间变化**：同一 URL 从 `false` 变 `true` 只需几小时（本次实测）。所以：
   - 接口不缓存收录结论（`cache.index_check_ttl_seconds=0`），每次都真实打引擎；
   - 用户看到 `not_indexed` 应理解为"此刻搜不到"，建议 24–72 小时后复查，而不是"永久未收录"。
3. **4 条"可疑"样本复核后全部翻正** → 说明此前的否定不是匹配器失效，而是文章当时确实还没被索引。这是正向证据：**判定对真实索引状态是敏感的**。
4. **仍未量化的部分**：标题被百度大幅改写、外站转载压过原站、结果超过前 10 条这三种情况会产生漏判（false negative）。当前没有任何自动发现机制，只能靠抽样人工复核。已在报告中列为"业务准确性已知边界"，未伪称已解决。

**待办（未做）**：把上述口径写进 `docs/API.md` 的响应说明与官网文案；给用户侧加一句"结果时间戳 + 复查建议"。

---

## F-09 异步收录任务越权（P1，已修复）

**证据（修复前，实机复现）**
1. 用 key A 提交 21 条 URL → `202 job_id=job_...d5f7dca9c1`。
2. 用**另一个** key B 直接请求：
   - `GET /v1/index/check/jobs/{id}` → **200**，返回 A 的任务进度
   - `GET /v1/index/check/jobs/{id}/results` → **200**，读到 A 的结果集
   - `GET /v1/index/check/jobs` → `count=1`（A 的任务出现在 B 的列表里）
   - `DELETE /v1/index/check/jobs/{id}` → **200**，`status=cancelled`（B 取消了 A 正在跑的任务）

**影响**：这是收费多租户平台里的水平越权（IDOR）：
- 数据泄露：租户提交的 URL 列表与收录结论属于其业务数据；
- 拒绝服务：任意 key 可取消他人批量任务，且列表接口暴露全局任务量；
- 计费纠纷：被取消的任务已按提交量扣费。

**根因**：任务模型只存了"任务"，没有存"属主"。`IndexJob` 没有 owner 字段，`Get/List/Cancel` 也没有调用方身份参数——鉴权只验证了"是不是合法 key"，没有验证"是不是你的任务"。

**修复**
1. `IndexJob` 新增持久化字段 `OwnerKeyID`（写盘，重启后仍生效）。
2. 新增 `core/index_job_owner.go`：`WithJobOwner(ctx, keyID)` / `JobOwner(ctx)` / `jobVisibleTo()`；约定"空 owner = 管理员（JWT），可见全部"。
3. 平台两处鉴权成功后把 key 绑定进请求上下文：
   - `platform/middleware.go` `APIKeyGuard`（/v1 数据面）
   - `platform/security.go` `AuthAny`（MCP 平面，`check_indexed` 提交任务走这里）
4. 所有访问口改为按属主过滤：`Get(id, owner)`、`List(owner, limit)`、`Cancel(id, owner)`；`Submit(ctx, …)` 从 ctx 取属主。外部访问他人任务统一返回 **404 not_found**（而不是 403），避免成为"任务是否存在"的探测口。

**验证**
- 单测 `TestIndexJobIsScopedToOwner`：属主可见、他人 not-found、他人无法取消、他人列表为 0、管理员可见全部。
- 实机复测（同一套 curl）：

| 操作 | 修复前 | 修复后 |
| --- | --- | --- |
| B 读 A 的任务状态 | 200 | **404** |
| B 读 A 的结果 | 200 | **404** |
| B 取消 A 的任务 | 200 cancelled | **404** |
| B 的任务列表 | 1（含 A 的） | **0** |
| A 读自己的 | 200 | 200 |
| A 取消自己的 | 200 | 200 |

**回归**：`go test -count=1 ./...` 全绿（见本轮末尾输出）。

**兼容性**：≤20 条同步接口、计费口径、任务 API 形状都没变；只是把"不属于你的任务"从 200 变成 404。对单一 key 的自用部署零影响。

**回滚**：去掉 `OwnerKeyID` 过滤即可，数据字段向后兼容（老任务 owner 为空 = 管理员任务）。

---

## F-11 任务内存不回收 + 队列满可并发执行（P2，已修复）

**证据（代码审查）**
1. `IndexJobManager.jobs` 只在 `Submit`/`resume` 时写入，**从不删除**；任务进入终态后 `st.job.URLs`（整批 URL）与 `st.pending` 仍然常驻。一个 5000 条的批次约占用 0.5–1MB（字符串头 + 内容），跑满 5000 条任务 ≈ 数 GB。
2. `st.pending` 在任务结束后已清空（切片被逐步取走），但 `st.job.URLs` 保留全量 —— 这是主要占用。
3. `enqueue` 的兜底分支在队列（容量 1024）满时 **另起 goroutine 直接 runJob** —— 会打破"同一时刻只跑一个任务"的节奏保证，两个任务同时抢同一引擎令牌，等于把 12s/条的限速变成 6s/条，直接提高验证码概率。

**影响**：长跑实例内存单调增长（OOM 风险），且高并发提交时可能把安全节奏打穿。

**修复**
- `finish()` / `Cancel()` 落盘后调用 `jobState.release()`：终态任务清空 `URLs`、`pending`、`attempts`；记录本身与磁盘文件保留，`Get/List` 仍可读。
- 新增 `maxResidentJobs = 500` 与 `evictTerminal()`：常驻表超过上限时按创建时间淘汰最老的**终态**任务，之后由磁盘兜底（`Get`/`List` 已有 disk fallback）。
- `enqueue(ctx, id)` 改为**阻塞式**发送（受 ctx 约束），不再开并发 runner：节奏契约不可协商。

**验证**：新增 `TestIndexJobReleasesMemoryWhenDone` —— 任务完成后断言 `st.job.URLs == nil`、`st.pending == nil`，且 `Get`/`Results` 仍可读。`go test ./core -run IndexJob` 全绿。

**残留**：见 F-12。

---

## F-12 任务列表每次轮询扫磁盘（P3，已确认）

`List(owner, limit)` 每次都调用 `store.ListMeta()`，后者 `os.ReadDir` + 逐个 `ReadFile(job.json)` 并 `json.Unmarshal`。任务数 N 时，每个轮询请求产生 N 次文件读取。当前任务量（几十个）无感；若长期运行积累到数千个，轮询会变成磁盘压力。
建议：为磁盘任务元数据加 5–10s 的进程内缓存（或维护一份轻量 meta 索引），仅 `Submit`/`finish` 时失效。未实施（需配合 F-10 的管理面一起设计）。

---

## F-10 管理员看不到异步任务（P3，已确认）

`/v1/index/check/jobs*` 只接受 API Key；管理面 22 个端点里没有任务列表。运维/客服排查"某个任务为什么卡住"目前只能拿用户 key 或看日志/磁盘目录。
建议（未实现）：在管理面加只读 `GET /api/index/jobs`（JWT），复用 `m.jobs.List("", limit)`（空 owner = 管理员，已支持）。

---

## F-01 收录查询标题探测的 SSRF（P1，已修复）

**证据**
- `core/title_fetch.go:125` `fetchPageTitle` → `RawSearchRequest(ctx, targetURL, q)`，`targetURL` 直接来自 API 调用方（`/v1/index/check` 的 `urls[]`、MCP `check_indexed`、CLI）。
- 代码里**存在**完整的私有网段防护：`core/network_guard.go` 的 `GuardPrivateNetworks` / `GuardedDialContext` / `ValidatePublicHTTPURL`（拒绝 loopback、RFC1918、link-local、CGNAT、DNS rebinding，并对每次重定向复核）。
- 但 `grep GuardPrivateNetworks` 显示：**除定义与测试外，生产代码从未把它置 true**（`core/common.go:316` 只是字段声明）。防护是"休眠"的。

**影响**
持有任意有效 API Key 的调用方可以让服务器发起任意 GET：`http://127.0.0.1:7000/web-tool/api/*`（自身管理面）、`http://169.254.169.254/latest/meta-data/`（云元数据）、内网主机与端口。虽然响应体只用于提取 `<title>` 并最终只回吐"是否收录"的判定，但仍是服务器侧请求伪造（内网探测/盲打带副作用的 GET 接口/日志污染），且属于越权访问基础设施。等级定为 P1 而非 P0：不是直接数据读取原语，但违反"服务端不得替用户访问内网"的底线。

**根因**：安全能力做成了"默认关闭的开关"，而唯一需要它的调用点没有打开。默认值方向与信任边界相反。

**修复**
```go
// core/title_fetch.go
q.GuardPrivateNetworks = true   // 调用方给的 URL 一律走私有网段防护
```
放在 `fetchPageTitle` 内（唯一出网口），而不是放在 baidu 引擎里——这样任何现在或将来接入标题探测的引擎都自动受保护。

**验证**
- 新增 `TestFetchPageTitleRejectsPrivateTargets`：起一个 loopback `httptest` 服务器，断言 `FetchPageTitle` 对 `127.0.0.1`、`169.254.169.254`、`10.0.0.1`、`[::1]` 全部返回 `ok=false`，且**服务器一次都没有被访问**（`hits == 0`），证明是在 dial 阶段被拦，不是靠超时。
- `go test ./core/ -run Title` 全绿。

**回滚**：单行删除即可回滚；影响面仅"私有地址不再被抓标题"，公开站点判定不受影响。

**残留**：私有地址被拦后回退到 URL 短语检索，仍会给出判定（通常为未收录）。需在 API.md 说明该行为，避免用户把"内网 URL 查不到"当 bug。

---

## F-02 构建上下文泄露运行态密钥（P1，已修复）

**证据**
- 工作区 `.dockerignore` 原内容只排除了 `.git`、`*.md`、`web2api`、`web2api.exe`、缓存目录。
- 工作区实际存在：`data/secret.key`（64 字节 = JWT 签名密钥）、`data/web2api.db`（128KB，含 API key 哈希、用量账本、管理员 bcrypt 哈希）、`data/audit.log`、`logs.txt`（1.8MB）。
- `Dockerfile` 构建阶段执行 `COPY . .` → 上述文件进入构建层；配合 `docker build --target builder` 或 registry 缓存导出（`--cache-to`）即可被取出。
- 上下文体积：实测工作区（除 `.git`）**772MB**，其中 `*.exe` 18 个共 **531MB**、QA/数据目录 812 个文件 **146MB**。

**影响**
1. 安全：JWT 签名密钥与 API key 哈希进入镜像构建缓存；任何能拉取 builder 层或缓存的人可伪造管理员 JWT。
2. 可用性：700MB+ 的构建上下文让每次 `docker build` 传输/扫描变慢，CI 与低配服务器上容易超时。

**修复**：重写 `.dockerignore`，加入 `data/`、`data-*/`、`qa/`、`deploy/e2e-results/`、`logs*.txt`、`server-*.log`、`config-qa*.yaml`、`config-audit*.yaml`、`*.exe`、`*.exe~`、`docs/`、`.zcode/`。

**验证**：用同一套 pattern 规则对工作区做静态计算 → **772MB → 2MB**（见审计命令输出）。

**残留**：Docker daemon 未运行，镜像构建未实机验证（见 F-07 同类限制）；建议 CI 里加一步 `docker build` 并断言镜像内不含 `data/`。

---

## F-03 compose 注释与鉴权现实相反（P2，已修复）

`docker-compose.yaml` 原文："Loopback only: the API has no auth layer, never expose it beyond localhost."
实际：数据面强制 `X-API-Key`（`security.data_plane_auth` 默认 true），管理面强制 JWT。
这类注释会误导运维：要么以为有鉴权可以随便暴露，要么以为无鉴权而放弃部署。已改为准确表述（明文 HTTP + 凭据风险 + 需前置 TLS 反代）。

---

## F-04 CI lint 无法运行（P2，已确认待修）

**证据**：`.github/workflows/ci.yml` 固定 `golangci/golangci-lint-action@v6` + `version: v1.64.8`；`go.mod` 声明 `go 1.26.0`；`.golangci.yml` 使用 v1 配置格式（`linters.disable-all`、`run.go`）。
**依据**：golangci-lint 拒绝 lint 目标 Go 版本高于自身构建版本的模块，Go 1.26 支持从 v2.9.0 起（[issue #6272](https://github.com/golangci/golangci-lint/issues/6272)、[FAQ](https://golangci-lint.run/docs/welcome/faq/)）。
**影响**：lint gate 实际失效（要么报版本错误、要么整步失败），代码质量门禁形同虚设。不影响运行中的服务。
**修复方案**（未执行，需联网安装 v2 二进制验证）：CI 升 action 到 v8 + `version: v2.13.2`；`.golangci.yml` 迁移为 v2 格式（`version: "2"`、`linters.default: none`）。
**本次尝试**：`go install ...@v2.13.2` 因 sumdb/网络中断失败两次，未完成本地验证 → 不写入未验证的配置。

---

## F-06 登录锁定可被换 IP 绕过（P2，**已修复**，详见 F-25）

**证据**：`platform/security.go`，锁定键原为 `loginAttemptKey{username, ip}`，状态存在进程内 map。
**影响**：单管理员 + 单实例部署下，攻击者用代理池对同一用户名可继续尝试（每 IP 5 次/15 分钟）。

**结案**：已改为**按用户名跨 IP 聚合**（F-25 记录了完整修复、取舍与实测）。此条保留作为原始发现编号，结论以 F-25 为准。

**仍存在（记录在案，非本项范围）**：多实例部署时锁定状态是进程内的，跨实例不共享；单机部署不受影响，多实例需要粘性会话或在反代层做登录限速。

---

## F-08 三处 Go 版本不一致（P3，待验证）

`go.mod` = `go 1.26.0`；本机工具链 = **go1.27.4**；`Dockerfile` = `golang:1.26.11`；CI = `go-version: "1.26"`。
风险：开发机上用 1.27 特性写代码 → CI/镜像 1.26 编译失败。当前 `go vet`/`go test` 通过只证明 1.27 能编译。
**验证方案**：CI 已用 1.26，等价覆盖；本地可通过 `GOTOOLCHAIN=go1.26.11 go build` 复现（需下载工具链，网络受限未执行）。
