# 2026-11-26 Web2API 全维度复审

## 范围与结论

初审基线为 `89d0e9cff752bad91341bc589684f807c3459d2c`。本次修复候选在分支 `codex/fix-full-audit-risk-20261126`；完整验证包括本机 Go 测试、vet、build、前端语法、Compose 配置和 diff 检查。推送后的 GitHub CI/Docker 工作流仍需以候选提交 SHA 为准。

**结论：本轮列出的代码侧安全、队列资源、限流、计划执行/元数据保留、用量保留、容器资源上限和 Actions 引用风险均已实现修复，且本机验证通过；公网正式上线准入仍需按生产环境清单验收。** 正式域名路由曾实测返回资讯雷达 HTML，而非 Web2API；用户明确要求暂不处理该项，因此该项保留为部署侧待办，不阻挡本轮代码合并，但也不能据此宣布正式域名上线成功。

## 发现

### P1 — 标题探测 SSRF 允许部分特殊地址和 NAT64

`isPublicIP` 只排除 unspecified、loopback、RFC1918/4193 private、link-local、multicast 和 CGNAT；Go `IsPrivate` 并不覆盖所有 IANA 特殊用途网段。对照当前谓词，`0.1.2.3`、`192.0.2.1`、`198.18.0.1`、`240.0.0.1`、`2001:db8::1`、`2001:2::1` 和 `64:ff9b::a9fe:a9fe` 会通过。URL 标题探测实际经过该公网地址检查，因此在特殊路由或 NAT64 网络上可请求内部/保留目标；暴露面取决于服务网络路由。OWASP 明确记录 IPv4-mapped IPv6 与 NAT64 可映射到 IPv4 私有/metadata 目标；IANA 登记表包含 `Globally Reachable` 字段。[实现](../../core/network_guard.go#L159) [调用链](../../core/title_fetch.go#L141) [OWASP SSRF 指南](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html) [IANA IPv4 特殊用途登记表](https://www.iana.org/assignments/iana-ipv4-special-registry)

**建议：**按 IANA IPv4/IPv6 特殊用途登记表建立明确的全局可达地址策略，拒绝所有 Globally Reachable=false 的网段；识别并阻断标准 NAT64 前缀；覆盖映射地址、特殊网段和 NAT64 的回归测试。保留现有 DNS 解析 pinning 与拨号时再次校验。

### P1 — 异步任务有任务数上限，没有总 URL 字节/任务工作集预算

异步队列可容纳 1024 个任务；单任务默认最多 5000 个 URL，HTTP body 上限为 10 MiB，而每个排队任务都会把 URL 集合保存在 `jobState.pending` 中。队列限制只数任务，不限制累计 URL 数或字节数；请求体上限按请求独立生效，因此高额度或多个有效 API key 可累计大量排队数据，内存占用在处理速度落后于提交速度时持续增长。按请求上限估算，最坏输入量级可接近 10 GiB（另有 Go 字符串、slice、job state 开销），不是常态值，但足以构成资源耗尽路径。[队列与容量](../../core/index_job_manager.go#L166) [URL 上限与 pending 保存](../../core/index_job_manager.go#L422) [HTTP body 上限](../../core/server.go#L196)

**建议：**增加全局排队 URL 数/字节预算和每 key 的待处理任务预算；拒绝时返回稳定的 429/503 与 Retry-After。也应对提交体和单 URL 长度设明确契约。Docker 的 CPU、memory、PID 限制作为纵深防护另行设置。

### P2 — 限流器按空闲过期，但没有硬容量上限

注释称 limiter table bounded；实现只在访问时每分钟扫掉空闲超过一小时的键，没有最大条目数。未认证 IP 表、管理 IP 表和 key 表都会存条目。分布式来源或大量不同 IP/key 在一小时内持续请求时，条目数随唯一键数增长；每分钟的全表清理又在全局 mutex 下执行，形成内存和请求延迟压力。[限流表](../../platform/ratelimit.go#L23) [惰性扫描](../../platform/ratelimit.go#L48)

**建议：**设置硬容量和确定的满载策略，或改成有界分片结构；对可信反向代理的客户端 IP 解析边界一并纳入测试，并保留上游边缘限流。

### P2 — 定时任务使用带 IP 限制的密钥时会反复失败

创建计划只检查密钥有效状态，没有检查当前 IP 是否在 `allowed_ips` 中；后台 runner 随后用空字符串作为请求 IP 调用共享计费入口。对任何设置了 IP 白名单的 key，`ipAllowedFor` 会拒绝这个空 IP，计划创建成功但不能执行，并按每小时重试策略不断失败。[创建与启用校验](../../platform/schedule.go#L120) [后台提交传空 IP](../../platform/schedule.go#L271) [白名单校验](../../platform/job_submit.go#L113)

**建议：**明确计划使用 key 白名单的产品语义，在计划创建/启用时阻止不支持的组合并给出可理解错误，或为受授权的后台计划定义可审计、范围有限的身份校验方式；不要静默跳过 key 限制。

### P2 — 计划运行历史元数据没有随计划/任务保留期清理

每个定时任务完成后需要 `scheduled_job:<jobID>` 设置记录供通知回调读取；基线另存为 `schedule_baseline:<scheduleID>`。计划删除时没有删除对应 baseline，完成通知后也没有删除 job 元数据。任务保留策略只清理任务目录，不清理这些平台设置；`index_jobs.retention_days` 默认 0（永不自动删）。长期运行的周期计划会不断增加设置数据，即使任务文件已按期限清理也会继续累积。[周期元数据写入](../../platform/schedule.go#L284) [回调读取及 baseline 写入](../../platform/notices.go#L67) [默认长期保留](../../config.yaml#L68)

**建议：**完成回调后清理 job 元数据；删除计划时清理 baseline；为异常中断保留有界的 TTL 清理路径，并确保通知比较完成前元数据仍可恢复。

### P2 — 配置的 30 天用量保留并非硬期限

清理在服务启动时执行，运行期只在累计用量序号恰好达到 256 的倍数时触发，没有定时器。低流量实例可让超过 30 天的数据长期留存，直到下一次启动或累计到清理点；这会偏离 `usage_retention_days: 30` 的直观含义。[清理触发条件](../../platform/usage_retention.go#L18) [清理实现](../../platform/usage_retention.go#L47)

**建议：**改为定时清理并保留启动清理；将阈值触发仅作为高流量补充，或在文档中准确标为 best-effort retention。

### P2 — 容器部署没有 CPU、内存和 PID 硬限制

两份 Compose 有 512 MiB `/dev/shm`、丢弃 capability、禁止提权与日志轮转，但没有容器 memory/CPU/PID 上限。应用请求并发限制为 2 有助于控制常规浏览器负载，但不能替代容器硬限制；Docker 默认不给容器 CPU 周期设置上限，Chrome 子进程故障或突发积压仍可能消耗主机资源。[本机 Compose](../../deploy/docker-compose.yml#L31) [上游 Compose](../../docker-compose.yaml#L6) [Docker 资源限制文档](https://docs.docker.com/engine/containers/resource_constraints/)

**建议：**结合目标主机内存和 Chrome 峰值实测配置 memory、CPU、PID 上限与告警阈值。当前 3 分钟低负载样本约 30 MiB 工作集/54 MiB 私有内存，不可作为生产限额依据；需压测后确定值。

### P1（部署待办，按用户要求暂缓）— 正式域名路由尚未指向 Web2API

本次对 `https://radar.ydsdoa.cn/web-tool/health`、`/web-tool/openapi.yaml`、`/web-tool/v1/engines` 分别发起只读 GET。三者均返回 HTTP 200 和资讯雷达 HTML（`/radar.svg` 页面），其中健康路径不是 Web2API JSON，OpenAPI 不是 YAML，engine 列表不是 API JSON。正式流量不能通过只检查 HTTP 状态码判定健康。

**部署验收门槛：**后续处理时需将反向代理正确路由 `/web-tool` 至 Web2API，并从外部验证健康 JSON、OpenAPI YAML、未授权 401、静态页面、证书链和真实引擎路径；健康检查须验证内容类型/响应结构，不仅检查 200。此项不在本轮代码修复范围。

### P2 — Actions 依赖使用可变 tag

CI/Docker workflow 使用 `actions/checkout@v4`、`actions/setup-go@v5` 等可变 tag；tag 被重定向或上游发布替换时，工作流执行的代码可能不同于审阅过的代码。GitHub 官方将完整 commit SHA 描述为当前唯一不可变的 action 引用方式。[工作流示例](../../.github/workflows/ci.yml#L18) [GitHub Actions 安全指南](https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions)

**建议：**把第三方 Actions 固定到完整 SHA，并由 Dependabot/Renovate 定期更新；评估生成 SBOM、镜像漏洞扫描和 provenance/signing。

## 修复状态与验证（2026-11-26）

| 初审项 | 处理结果 | 验证证据 |
| --- | --- | --- |
| 特殊地址/NAT64 SSRF | 已拒绝非全局可达特殊地址、NAT64 与 IPv4-mapped 绕过，并保留解析及拨号阶段校验 | `core/network_guard_test.go`、标题探测回归；全量 `go test ./...` 通过 |
| 异步队列 URL 工作集 | 增加累计 URL 数/字节预算、单 URL 2048 字节限制、超限 503/Retry-After、恢复任务惰性水合及资源指标 | `core/index_job_test.go`；管理总览增加队列占用卡片 |
| 限流器无硬容量 | 每张表设置 8192 条目上限，饱和时走共享溢出桶并清除过期项 | `platform/ratelimit_test.go`、全量平台测试 |
| 定时任务 IP 白名单 | 仅带私有 typed context 标记的内部 scheduler 调用绕过入站 IP 检查；普通 API/MCP 路径仍执行白名单 | `platform/job_submit_test.go` |
| 定时计划元数据增长/恢复 | 计划删除时清理 baseline；任务通知元数据先于执行持久化，完成后消费；启动阶段可恢复终态通知 | bbolt/JSON store、通知及计划回归测试 |
| 用量保留不是硬期限 | 保留启动清理，新增受 Close 管理的 6 小时定时清理并串行化执行 | `TestUsageRetentionPrunesOnTimerAtLowVolume` |
| Compose 无资源上限 | 两份 Compose 增加可覆盖的内存、CPU、PID 默认上限 | `cmd/dockerfile_env_test.go`、两份 `docker compose config --quiet` 通过 |
| Actions 可变引用 | workflow 中第三方 Action 固定完整 commit SHA；镜像构建开启 SBOM 与 provenance | GitHub workflow 内容复核；推送后的 CI/Docker 结果待确认 |
| 正式域名路由 | 用户要求暂缓 | 不纳入本轮代码合并；公网发布前仍须独立验收 |

本机验证环境使用 Go 1.26.11、`GOMAXPROCS=1`、`GOMEMLIMIT=768MiB`：`go test -count=1 ./...`、`go vet ./...`、`go build .`、`node --check web/admin/assets/app.js`、`git diff --check` 和两份 Compose 配置检查均通过。Linux Docker 中 `go test -race -count=1 -p 1 ./...` 全包通过（平台包 187.678 秒），未报告竞态；golangci-lint v2.13.2 本机容器扫描 0 issues。Go 测试中的公网 DNS 夹具从文档保留段更换为公网地址，未放宽生产地址策略。远端 Docker build/HTTPS smoke 已通过；CI lint 首次发现通知错误未检查，已显式记录失败并加入日志处理，修复后的远端 CI（含 govulncheck）仍待通过后合并。

## 功能完整度、架构与性能

- 新增批量异步任务、幂等计费与恢复对账、定时计划、变化通知、结果分页/CSV 和资源卡片均有实现。本轮修复了计划使用 IP 白名单 key 的执行语义和通知元数据清理/恢复路径；远端工作流仍需在新提交上验证。
- 系统采用 Go/Fiber、单进程 bbolt、本地任务文件与单个 FIFO 执行器；限流、计划互斥与队列状态是进程内状态。当前架构适合单实例，不能直接横向多副本扩展；bbolt 文件与本地队列需要单写者/恢复语义。若未来需要 HA，需先迁移共享队列、分布式锁、账本和浏览器 worker 的所有权。
- 引擎受站点节奏限制，历史验收测得百度单引擎约 12 秒/URL、约 5 URL/分钟；这不是本轮重测值。队列仍为单 worker；新增全局 URL 数/字节预算与可观测指标，但上线前需按真实 key 配额、站点限速和 SLA 配置容量。
- 当前工作树 `git diff --check` 通过；浏览器功能与本机低负载测量已在上一轮交付报告记录。本轮没有向真实搜索引擎发批量流量，也没有重新做长时间压力/故障注入。

## 上线与合并判定

PR #14 是本轮基线上的既有合并。当前审计修复候选需经远端 Actions/Docker 门禁后再合并。代码侧列出的风险已修复；公网正式上线仍需按 [上线清单](../RELEASE_CHECKLIST.md) 完成管理员凭据/密钥管理、生产 TLS、监控告警、异地备份保留、容量/SLA 和极端故障验收。正式域名路由明确按用户要求暂缓，不纳入本轮合并条件；发布状态必须据此保持为“代码修复完成，生产发布验收未完成”。
