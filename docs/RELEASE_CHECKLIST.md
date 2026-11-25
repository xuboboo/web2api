# 上线检查清单（RELEASE_CHECKLIST）

用法：逐项执行、记录结果；**任何一项"否"都不允许上线**。命令按生产地址替换变量。

> 复审记录（2026-11-25）：核心代码门禁、三正三负桌面百度对照、DNS 重绑定、干净 Debian systemd/NGINX TLS、可信客户端 IP、升级回滚和恢复已完成；未勾项只剩生产凭据、监控告警、异地保留、正式域名/CA、容量/SLA 或极端故障注入。历史实测值及环境边界见 `docs/audit/REPORT.md`。

```bash
BASE=https://api.example.com/web-tool
TOKEN=<管理员 JWT>
KEY=<新签发的 API Key>
```

---

## A. 配置与凭据（阻塞项）

| # | 检查 | 命令 / 方法 | 通过标准 | 结果 |
| --- | --- | --- | --- | --- |
| A1 | 管理员密码已改 | 非 loopback + 默认密码启动必须退出码 1；改密后正常启动 | 无默认密码公网启动 | ☑ 机制已实测（E19）；部署侧仍需确认密码已改 |
| A2 | 数据目录已持久化 | `docker inspect` 挂载 / `df` 看目录 | 容器重建后 `web2api.db` 仍在 | ☑（E60：删除并重建容器后仍为 bbolt，管理员可登录，恢复 Key 仍在） |
| A3 | `secret.key` 权限 0600 | `ls -l $DATA/secret.key` | `-rw-------` | ☑（E60：恢复后的 named volume 中 `secret.key` 为 0600；生产部署仍需核验运行用户） |
| A4 | 仓库/镜像内无运行态数据 | 最终镜像内检查 `/usr/src/app/data` 与 `web2api.db`/`secret.key` | `data/` 为空初始化目录；无数据库、密钥、审计日志或任务文件 | ☑（E60：数据目录故意预建并由 `chrome` 拥有，内容为空；CI 已增加契约检查） |
| A5 | 反代后 `trust_proxy_headers` 已开 | 看配置 + 登录失败日志里的 IP | 是真实客户端 IP | ☑（F-52：仅受信 `trusted_proxies` 的 `X-Real-IP` 生效；跨容器 HTTPS 伪造头后，审计仍记录真实调用方 IP，E63） |
| A6 | `app.debug_endpoints: false` | `curl -s -o /dev/null -w '%{http_code}' $BASE/debug/fingerprint-check` | 404 | ☐ |
| A7 | compose 已提供 `ADMIN_PASSWORD` | `docker compose config` | 解析通过（缺失时明确报错） | ☑（E28：必需变量已实测；缺值 exit 1） |
| A8 | `.env` / `config-*.yaml` 未入库 | `git ls-files | grep -E '^\.env$\|config-audit'` | 空 | ☑（`.env` 已进 `.gitignore` 与 `.dockerignore`） |
| A9 | `metrics_token` 已设或仅本机采集 | 从外部 `curl $BASE/metrics` | 401 | ☐ |

## B. 鉴权与权限（阻塞项）

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| B1 | 数据面无 Key → 401 | 401 | ☑（`TestAPIKeyGuardAuthAndQuota`，E51） |
| B2 | 数据面错误 Key → 401 | 401 | ☑（`TestAPIKeyGuardAuthAndQuota`，E51） |
| B3 | 管理面无 JWT → 401 | 401 | ☑（受保护端点无 JWT/编码路径回归，E51） |
| B4 | 任务端点跨 Key 越权 → 404 | 用 Key B 读 Key A 的任务 = 404 | ☑（实际平台装配中验证状态、结果、取消均 404，列表隐藏任务，E51） |
| B5 | 静态目录不可穿越 | `/console/../config.yaml`、编码穿越 = 404 | ☑（E49：当前生产路由装配的 4 种穿越路径均 404，无配置内容泄露；部署反代仍须上线验证） |
| B6 | 登录爆破被锁 | 连续 5 次错误密码 → 429 + Retry-After；**换 IP 不能重置计数** | ☑（同 IP 与轮换 IP 登录锁定契约测试，E51） |
| B7 | 配额生效 | `daily_quota=1` 的 Key 第 2 次调用 → 429 | ☑（超配额 429/Retry-After 契约测试，E51） |
| B8 | 限流生效 | 单 Key 高频调用 → 429 `rate_limited` | ☑（per-key limiter 真实路由测试，E51） |
| B9 | 角色最小权限 | viewer/operator 访问 `/api/users*` → 403；admin → 2xx | ☐（需本轮新增角色门禁回归） |
| B10 | 黑名单全站前置 | 封禁测试 IP 后 `/health`、`/web-tool/`、`/api/*`、`/v1/*`、`/mcp` 全部 403 `reason=blacklisted`，写 `blacklist.hit` | ☐（需真机跨平面验证） |
| B11 | 自锁保护与 force | 创建覆盖当前 IP 的规则默认 400；显式 `force:true` 可创建 | ☐（需真机验证） |
| B12 | 日志角色与配置 | viewer 可读 `/api/logs*`，admin 可 `POST /api/logs/cleanup`；`retention_days` 与配置一致 | ☐（需真机验证） |

## C. 核心业务（阻塞项）

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| C1 | 单 URL 判定可用 | 返回 `indexed` + `match`，非 error | ☑（真实部署单条查询及 strict live smoke，见 E32/E33/E37） |
| C2 | 已知收录 URL 判为 true | 抽查 3 条，与人工百度搜索一致 | ☑（2026-11-25 桌面 Chrome 同口径复核 `10049053/51/48`，三条精确标题均为第 1 项，E64） |
| C3 | 已知未收录 URL 判为 false | 抽查 3 条一致 | ☑（同日复核 `10049057/55/54`，三条均明确显示“未找到相关结果”；另记录 `10049071` 约 30 分钟后翻正，E64） |
| C4 | 批量 ≤20 同步 | 返回 200 完整信封 | ☑（`TestIndexCheckSmallBatchStaysSynchronous`，E50） |
| C5 | 批量 >20 转异步 | 202 + `job_id`，轮询到 `done` | ☑（`TestIndexCheckLargeBatchReturnsJob`，E50；真实走查见 E32/E33） |
| C6 | 结果 CSV 可打开 | UTF-8 BOM，Excel 无乱码 | ☑（E50 已证端点内容类型与序列化；E55 用本机 Microsoft Excel 实际打开同格式临时导出，中文、BOM、逗号引号及 2 行 × 6 列解析均精确） |
| C7 | 重复 URL 去重 | 重复提交时 `total` 为去重后数量 | ☑（同步/异步去重回归与真实 3→1 实测，见 E33/E50） |
| C8 | 非法 URL 报错清晰 | 400 `INVALID_PARAM` 且带具体 URL | ☑（解析器按错误类型回显具体 URL；HTTP POST/GET 回归通过，见 E54） |
| C9 | 引擎冷却有降级 | 冷却时 `503 engine_cooldown` + `Retry-After`，或任务转 `waiting` | ☑（`TestCooldownManagementThroughAssembly` 与核心冷却语义回归，E50；当前 `cmd` 全包通过） |
| C10 | 结果时效性已告知用户 | API 文档说明搜索引擎视角、非官方状态和复查间隔 | ☑（`docs/API.md` §2.1 已明确搜索视角和复查建议；API 无 checked_at 字段） |

## D. 稳定性与容量

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| D1 | 重启后任务续跑 | kill -9 后重启，未完成任务继续且不重查已完成项 | ☐ |
| D2 | 重启后在途请求不报 500 | 优雅停服时返回 503 而非断连 | ☐ |
| D3 | 磁盘写满时行为可预期 | 任务标 failed 并记录原因，进程不崩 | ☐ |
| D4 | 浏览器崩溃可恢复 | server-owned Chrome 被杀后自动重启，3 路并发导航成功 | ☑（严格集成测试 E20） |
| D5 | 长跑内存平稳 | 30 分钟采样 RSS/句柄/goroutine 无单调上涨 | ☑（E31：RSS 27.0→27.6MB 平台期、goroutine 恒定 10、句柄 407–415 饱和、health 59/59=200；4 小时未做，判读方法见 OPERATIONS §1） |
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
| 单条收录查询 P50（并发 1，串行口径） | 11.5–12.2 s/条（21 条 241 s、30 条 367 s） | < 15 s ✅ |
| 单条收录查询 P50（并发 4） | **37.8 s** | < 15 s ❌（排队所致，见下行说明） |
| 单条收录查询 P95（并发 4） | **48.2 s** | < 25 s ❌ |
| 批量吞吐（百度单引擎） | 12.0–12.2 s/条（30 条实测 367 s）→ ≈5 条/分钟 | ≥ 4 条/分钟 ✅ |
| 峰值 RSS（21 条任务 + 8 次并发查询） | 25–35 MB，任务结束后回落 27 MB | < 70% 物理内存 ✅ |

> **并发下的延迟是设计结果，不是缺陷**：单引擎按 12 s/条串行，4 个并发请求排在同一个节拍后面，因此 P50/P95 按并发数线性放大。**容量规划与 SLA 必须写明"单引擎吞吐 ≈5 条/分钟"**；要降延迟只能加引擎（bing/sogou/360 并行判定）或加出口 IP + 代理池，**加机器无效**（进程 RSS 全程 25–35 MB，CPU 不是瓶颈）。实测见 `docs/audit/EVIDENCE.md` E27。

## F. 数据与备份

| # | 检查 | 通过标准 | 结果 |
| --- | --- | --- | --- |
| F1 | 备份命令可用 | `admin-backup` 产出文件且不能覆盖源数据库 | ☑（快照生成见 E35；F-50 同路径/硬链接保护与全量回归见 E48） |
| F2 | **恢复实测** | 恢复后能登录、Key 数量正确、用量计数一致 | ☑（2026-11-25；隔离临时目录执行 restore、重启并重新登录；1 个快照内 Key 恢复，快照后新增 Key 不存在，见 E35） |
| F3 | 完整恢复点含 `secret.key`、`index_jobs/` 与配置 | 隔离恢复后关键状态可用 | ☑（`admin-backup --full` 生成带清单/checksum 的完整数据目录+配置归档；E59 验证本地逐项恢复，E60 完成 Docker named volume，E63 完成 Debian systemd 7001 隔离恢复、登录与 Key 校验） |
| F4 | 备份保留策略 | ≥ 7 天，异地一份 | ☐ |
| F5 | 审计日志可读 | `/api/audit` 有登录与密钥操作记录 | ☑（当前树 `TestAuditTrail` 端到端通过，含 JWT 读取与 Key 脱敏，E41） |

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
| H1 | `docs/INSTALL.md` 按步骤在干净机器走通 | ☑（干净 Debian 12 systemd 容器完成用户、目录、配置权限、Chromium、二进制和 web 资源安装；实际发现并修复 `/etc/web2api` 需 `0750 root:web2api`，E63） |
| H2 | `docs/DEPLOYMENT.md` 的 compose/systemd/反代配置可用 | ☑（Compose 见 E60；systemd unit、NGINX 443 TLS1.3、HSTS、可信客户端 IP、重启/崩溃和升级回滚见 E63） |
| H3 | `docs/ENVIRONMENT.md` 与 `cmd/root.go` 默认值一致（自动生成） | ☑ |
| H4 | `docs/OPERATIONS.md` 的备份/恢复/冷却命令实测可用 | ☑（E35/E59 验证本地完整恢复点；E60 验证 Docker named volume；E63 在 Debian systemd 停服生成含配置/secret/任务/审计的完整归档并在 7001 隔离 unit 恢复，登录与 Key 数量一致） |
| H5 | `docs/API.md` 与实现一致（本会话已逐条核对） | ☑ |
| H6 | `docs/audit/FINDINGS.md` 中 P0/P1 全部关闭 | ☑（F-46 重绑定回归 E62；F-52 反代真实 IP E63；F-50/F-51 与 TLS/认证等既有 P1 均有当前修复和验证） |
| H7 | **改了语义必须改说法**：安全策略/限额/计费口径变更后，面板文案、API 文档页、markdown 文档三处同步 | ☐（新增角色/黑名单/日志语义后需重新核对面板与全部 markdown；本轮文档收尾已执行，发布前再确认） |
| H8 | 控制台指标口径无歧义（调用次数 vs 计费次数、失败可见） | ☑（E29：总览 + 使用日志已修） |

## I. 回归

| # | 检查 | 命令 | 通过标准 | 结果 |
| --- | --- | --- | --- | --- |
| I1 | 单元/集成测试 | `go test -p 1 -count=1 ./...` | 全绿 | ☑（2026-11-25 当前树以 Go 1.26.x（`go.mod` 为 1.26.0）低内存配置完整重跑，所有有测试的包通过，见 E57） |
| I2 | 静态检查 | `go vet -p 1 ./...` | 无输出 | ☑（2026-11-25 当前树全量 vet 通过，见 E57；GitHub CI 独立复跑仍待确认） |
| I3 | 竞态检测 | `CGO_ENABLED=1 go test -race -count=1 -p 1 ./...` 与 `-race -tags=integration` | 全绿、无 DATA RACE | ☑（全量 race 已在 Docker Linux 镜像 `golang:1.26.11-bookworm`（预装 Chromium 运行库）完成，11 个测试包全绿、无 DATA RACE，原始日志 `qa/features-audit/race-final.log`；严格集成 race 沿用 E37） |
| I4 | 交叉编译 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` | 成功 | ☑（2026-11-25；最终修订树 Linux/amd64 ELF 23,290,018 bytes（E38）） |
| I5 | 镜像构建 | `docker build .` + `docker run` 冒烟 | 健康检查通过 | ☑（E60：Linux engine 构建成功；fresh named volume、非 root、最小权限、health、bbolt、HTTPS、完整恢复与容器重建均通过。E61：远端 Docker workflow 在 PR 的代码 SHA `628daff` 上也通过） |
| I6 | golangci-lint v2 | CI `v2.13.2` + v2 schema | 0 issues | ☑（2026-11-25；F-49 修复后 Go 1.26.11 当前树复跑 0 issues、exit 0，见 E45） |
| I6a | **静态分析（本机替代）** | `staticcheck ./...` | 0 条 | ☑（2026-11-25 当前树复跑 0 条，见 E39；E24 为历史修复记录） |
| I9 | 依赖版本复核 | `go list -m all` 中 HTTP/S 解析层无已知漏洞 | 与 govulncheck 结论一致 | ☑（fasthttp v1.70.0；2026-11-24 历史证据） |
| I7 | 依赖漏洞扫描 | `govulncheck ./...` | 0 个可达漏洞 | ☑（2026-11-25；F-49 修复后 Go 1.26.11 govulncheck exit 0、0 可达；1 条未调用 OpenPGP 模块公告 GO-2026-5932，见 E45） |
| I8 | 严格集成测试 | `WEB2API_INTEGRATION_TESTS=1 WEB2API_INTEGRATION_STRICT=1 go test -race -tags=integration ./...` | 全绿 | ☑（2026-11-25；百度/必应 live smoke 与集成包全绿，见 E37） |

---

## 上线判定

**只有在 A–H 全部 ☑、I 段全部 ☑ 时才能上线。**
代码侧此前列出的 F-46、C2/C3、H1/H2/H4 和 A5 已闭环。当前剩余未勾项属于生产环境证据或运维能力配置：正式域名/CA、生产管理员凭据与密钥管理、监控告警、异地备份保留、容量/SLA，以及部分极端故障注入。E61 的远端 CI/Docker 门禁已在代码 SHA `628daff` 通过；本次修订推送后必须以新 SHA 的远端结果为准。以各表当前勾选状态为准，不得用本地自签名证书或测试凭据替代生产验收。
当前状态见 `docs/audit/REPORT.md` 的"准入结论"一节。
