# 上线检查清单（RELEASE_CHECKLIST）

用法：逐项执行、记录结果；**任何一项"否"都不允许上线**。命令按生产地址替换变量。

```bash
BASE=https://api.example.com/web-tool
TOKEN=<管理员 JWT>
KEY=<新签发的 API Key>
```

---

## A. 配置与凭据（阻塞项）

| # | 检查 | 命令 / 方法 | 通过标准 | 结果 |
| --- | --- | --- | --- | --- |
| A1 | 管理员密码已改 | 非 loopback + 默认密码启动必须退出码 1；改密后正常启动 | 无默认密码公网启动 | ☐ |
| A2 | 数据目录已持久化 | `docker inspect` 挂载 / `df` 看目录 | 容器重建后 `web2api.db` 仍在 | ☐ |
| A3 | `secret.key` 权限 0600 | `ls -l $DATA/secret.key` | `-rw-------` | ☐ |
| A4 | 仓库/镜像内无运行态数据 | `docker run --rm web2api:local ls /usr/src/app` | 无 `data/` | ☐ |
| A5 | 反代后 `trust_proxy_headers` 已开 | 看配置 + 登录失败日志里的 IP | 是真实客户端 IP | ☐ |
| A6 | `app.debug_endpoints: false` | `curl -s -o /dev/null -w '%{http_code}' $BASE/debug/fingerprint-check` | 404 | ☐ |
| A7 | `metrics_token` 已设或仅本机采集 | 从外部 `curl $BASE/metrics` | 401 | ☐ |

## B. 鉴权与权限（阻塞项）

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| B1 | 数据面无 Key → 401 | 401 | ☐ |
| B2 | 数据面错误 Key → 401 | 401 | ☐ |
| B3 | 管理面无 JWT → 401 | 401 | ☐ |
| B4 | 任务端点跨 Key 越权 → 404 | 用 Key B 读 Key A 的任务 = 404 | ☐ |
| B5 | 静态目录不可穿越 | `/console/../config.yaml`、`%2e%2e%2f` = 404 | ☐ |
| B6 | 登录爆破被锁 | 连续 5 次错误密码 → 429 + Retry-After；**换 IP 不能重置计数** | ☐ |
| B7 | 配额生效 | `daily_quota=1` 的 Key 第 2 次调用 → 429 | ☐ |
| B8 | 限流生效 | 单 Key 高频调用 → 429 `rate_limited` | ☐ |

## C. 核心业务（阻塞项）

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| C1 | 单 URL 判定可用 | 返回 `indexed` + `match`，非 error | ☐ |
| C2 | 已知收录 URL 判为 true | 抽查 3 条，与人工百度搜索一致 | ☐ |
| C3 | 已知未收录 URL 判为 false | 抽查 3 条一致 | ☐ |
| C4 | 批量 ≤20 同步 | 返回 200 完整信封 | ☐ |
| C5 | 批量 >20 转异步 | 202 + `job_id`，轮询到 `done` | ☐ |
| C6 | 结果 CSV 可打开 | UTF-8 BOM，Excel 无乱码 | ☐ |
| C7 | 重复 URL 去重 | 重复提交时 `total` 为去重后数量 | ☐ |
| C8 | 非法 URL 报错清晰 | 400 `INVALID_PARAM` 且带具体 URL | ☐ |
| C9 | 引擎冷却有降级 | 冷却时 `503 engine_cooldown` + `Retry-After`，或任务转 `waiting` | ☐ |
| C10 | 结果时效性已告知用户 | 文档/控制台明确"结果为搜索引擎视角快照" | ☐ |

## D. 稳定性与容量

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| D1 | 重启后任务续跑 | kill -9 后重启，未完成任务继续且不重查已完成项 | ☐ |
| D2 | 重启后在途请求不报 500 | 优雅停服时返回 503 而非断连 | ☐ |
| D3 | 磁盘写满时行为可预期 | 任务标 failed 并记录原因，进程不崩 | ☐ |
| D4 | 浏览器崩溃可恢复 | server-owned Chrome 被杀后自动重启，3 路并发导航成功 | ☑（严格集成测试 E20） |
| D5 | 长跑内存平稳 | 4 小时压测 RSS 无单调上涨 | 🟡 21 条/4 分钟实测 25–35MB 平稳；4 小时未做 |
| D6 | 并发上限生效 | `max_concurrent_requests` 打满时新请求排队/503，不 OOM | ☐ |
| D7 | 队列积压可见 | 任务列表能看到排队数量与 ETA | ☐ |
| D8 | **硬杀后续跑** | kill -9 后重启，未完成任务继续且不重查已完成项 | ☑（本次审计 E14：4/21 → 7 → … → 21/21） |
| D9 | **存储降级告警** | `/health.storage=json` 触发 P1 | ☐（代码契约已实测 E15，告警需生产监控配置） |
| D10 | **运行期写入失败** | 运行中把 `index_jobs` 置为不可写 → 提交任务应 500 且不挂起，`/health` 仍 200，恢复权限即自愈 | ☑（E25：500/0.01s + 自愈） |

## E. 性能（按实测填写，不允许编数字）

| 指标 | 实测值 | 目标 |
| --- | --- | --- |
| `/health` 吞吐（并发 20） | 611 QPS，p50 3.3ms / p95 132ms / p99 139ms，0 错误 | > 200 QPS |
| 静态控制台（并发 10） | 1653 QPS，p50 1.1ms / p95 19.6ms | > 500 QPS |
| 单条收录查询 P50 | **待压测**（需隔离出口，否则污染百度 IP） | < 15 s |
| 单条收录查询 P95 | **待压测** | < 25 s |
| 批量吞吐（百度单引擎） | 12.0–12.2 s/条（30 条实测 367 s） | ≥ 4 条/分钟 |
| 峰值 RSS | **待压测** | < 70% 物理内存 |

## F. 数据与备份

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| F1 | 备份命令可用 | `admin-backup` 产出文件 | ☐ |
| F2 | **恢复实测** | 恢复后能登录、Key 数量正确、用量计数一致 | ☐（本次审计已实测通过） |
| F3 | 备份含 `secret.key` 与 `index_jobs/` | 归档里存在 | ☐ |
| F4 | 备份保留策略 | ≥ 7 天，异地一份 | ☐ |
| F5 | 审计日志可读 | `/api/audit` 有登录与密钥操作记录 | ☐ |

## G. 监控与告警

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| G1 | 存活探针接监控 | `/health` 5xx 时告警 | ☐ |
| G2 | 就绪探针接监控 | `/ready` 持续失败告警 | ☐ |
| G2a | **存储后端告警** | `/health.storage` 从 `bbolt` 变为 `json` 时立即 P1（防静默丢账本） | ☐ |
| G3 | 引擎冷却告警 | 单引擎冷却 > 30 分钟 | ☐ |
| G4 | 错误率告警 | 5xx > 5% 持续 5 分钟 | ☐ |
| G5 | 磁盘/内存告警 | > 85% | ☐ |
| G6 | 登录失败告警 | 5 分钟 > 10 次 | ☐ |

## H. 文档与交付

| # | 检查 | 结果 |
| --- | --- | --- |
| H1 | `docs/INSTALL.md` 按步骤在干净机器走通 | ☐ |
| H2 | `docs/DEPLOYMENT.md` 的 compose/systemd/反代配置可用 | ☐ |
| H3 | `docs/ENVIRONMENT.md` 与 `cmd/root.go` 默认值一致（自动生成） | ☑ |
| H4 | `docs/OPERATIONS.md` 的备份/恢复/冷却命令实测可用 | ☑（备份恢复已实测） |
| H5 | `docs/API.md` 与实现一致（本会话已逐条核对） | ☑ |
| H6 | `docs/audit/FINDINGS.md` 中 P0/P1 全部关闭 | ☐ |

## I. 回归

| # | 检查 | 命令 | 通过标准 | 结果 |
| --- | --- | --- | --- | --- |
| I1 | 单元/集成测试 | `go test -count=1 ./...` | 全绿 | ☑ |
| I2 | 静态检查 | `go vet ./...` | 无输出 | ☑ |
| I3 | 竞态检测 | CI: `go test -race ./...` | 全绿（本机无 cgo，需 CI） | ☐ |
| I4 | 交叉编译 | `CGO_ENABLED=0 GOOS=linux go build` | 成功 | ☑ |
| I5 | 镜像构建 | `docker build .` + `docker run` 冒烟 | 健康检查通过 | ☐（本机 Docker daemon 未运行） |
| I6 | golangci-lint v2 | CI `v2.13.2` + v2 schema | 0 issues | ☐（本机已装并读配置；完整运行受 Windows 分页/Go1.27 导出数据限制，交由 CI） |
| I6a | **静态分析（本机替代）** | `staticcheck ./...` | 0 条 | ☑（E24：14 → 0） |
| I9 | 依赖版本复核 | `go list -m all` 中 HTTP/S 解析层无已知漏洞 | 与 govulncheck 结论一致 | ☑（fasthttp v1.70.0） |
| I7 | 依赖漏洞扫描 | `govulncheck ./...` | 0 个可达漏洞 | ☑（本机实机跑通；据此修复 fasthttp GO-2026-4950 → v1.70.0） |
| I8 | 严格集成测试 | `WEB2API_INTEGRATION_STRICT=1 go test -tags=integration ./...` | 全绿 | ☑（11 包全过；`TestSearch*` 属 live 用例，外部站点抖动时会偶发失败，重跑判定） |

---

## 上线判定

**只有在 A–H 全部 ☑、I1–I5 全部 ☑ 时才能上线。**
当前状态见 `docs/audit/REPORT.md` 的"准入结论"一节。
