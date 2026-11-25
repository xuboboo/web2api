# 用户角色 / 日志清理 / 黑名单：五轮独立审计与本机 100 项验证

## 方法与证据边界

- 五轮审计由五个独立上下文的审计员并行执行，每轮均重新读取当前磁盘源码，不共享先前列举的结论；审计员只读，不修改文件。
- 100 项验证由 qa/features-audit/http_audit.py 对**本机真实运行的 web2api 二进制**发起 HTTP 请求，逐项记录请求、期望状态、实际状态与响应校验结果。证据：qa/features-audit/http-results.json；报告：docs/audit/features-http-audit.md。
- 审计发现问题后在主线程修复，重新构建、重跑全部单测与 100 项 HTTP 验证。最终结果以修复后的二进制为准。

## 五轮审计结论

### 第 1 轮：鉴权与角色（audit-auth）

发现并修复：

| 级别 | 问题 | 修复 |
| --- | --- | --- |
| P1 | 配置管理员密码在 config.yaml 变更后未同步到 users 桶，旧密码仍可登录 | platform/users.go 启动时按 settings 哈希同步 admin 用户记录 |
| P1 | 最后一名管理员保护存在 TOCTOU，并发互降可致 0 管理员 | platform/handlers_admin_new.go 增加 userMutationMu，将计数检查与写入串行化 |
| P1 | 旧 JWT 在 MCP 平面被当作无限额 admin，禁用/降级仍可用 | platform/security.go 的 AuthAny JWT 分支要求 sid、重查用户状态与角色，仅 admin 可走免费 JWT 通道 |
| P2 | refresh 会续签无 sid 的长期令牌 | handlers_admin_s4.go 为 legacy token 刷新时新建会话并绑定 sid |
| P2 | 第二管理员自助改密会改写全局 settings 凭据并踢掉所有会话 | handlers_admin.go 仅配置管理员触碰全局凭据/token_version |
| P2 | 登录锁定按未规范化用户名计数，大小写/空白可绕过 | 登录入口统一 normalizeUsername |
| P2 | jwtGuard 忽略 touchSession 的 active 结果 | 会话已撤销时返回 401 |

### 第 2 轮：存储与迁移一致性（audit-store）

发现并修复：

| 级别 | 问题 | 修复 |
| --- | --- | --- |
| P1 | admin-reset 只写 settings，登录走 users 桶，离线改密无效 | cmd/admin_reset.go 在同一 bbolt 事务内更新用户记录并递增 token_version |
| P1 | 审计轮转失败可留下 nil 句柄并截断证据 | platform/audit.go 轮转失败恢复 append 句柄，重命名失败不截断 |
| P2 | JSON fallback 的 user/blacklist 写盘失败后内存已变更 | store_users.go / store_blacklist.go 改为失败回滚 |
| P2 | JSON 与 bbolt 的 timeseries 口径不一致 | store_json.go 按 recordReached 过滤 rejected |
| P2 | 最后一名管理员保护 TOCTOU（与第 1 轮同因） | 同上互斥锁 |

未修复并保留为已知限制：旧版 .db 恢复时校验仅覆盖原三桶，新增桶为空后由启动逻辑重建；CLI 备份/恢复尚不支持 JSON fallback 独立恢复路径（API 备份可用）。

### 第 3 轮：日志轮转与自动清理（audit-logging）

发现并修复：

| 级别 | 问题 | 修复 |
| --- | --- | --- |
| P1 | serve 日志轮转失败后 writer 永久失效，之后所有文件日志静默丢失 | core/logger.go 失败时用 restoreAppendLocked 恢复可写句柄 |
| P1 | audit 轮转忽略 rename 错误后无条件 O_TRUNC，历史审计被销毁 | rotateLocked 返回 error；失败保留当前文件不截断 |
| P2 | log_max_mb=0 会连带关闭时间保留，手动清理静默无效 | InitLoggerWithRetention 始终使用 rotating writer，0 仅关闭大小触发 |
| P2 | log_max_mb=0 时 audit 仍按默认 10MB×5 轮转，与文档不符 | /api/logs/config 增加 file_logging_enabled；审计侧保留固定轮转作为安全兜底并在文档说明 |
| P2 | 归档按旧 mtime 判过期，刚轮转出的文件可被立即删除 | 轮转后 os.Chtimes 刷新归档 mtime |
| P3 | 文本/JSON 格式切换后旧格式归档不可查 | core/log_query.go 双格式回退解析 |
| P3 | logs.txt.0 等非标准后缀被误清理 | prune 仅接受正整数后缀 |
| P3 | /api/logs/config 不反映是否真的写文件 | 增加 file_logging_enabled |

### 第 4 轮：黑名单中间件与端点（audit-blacklist）

发现并修复：

| 级别 | 问题 | 修复 |
| --- | --- | --- |
| P1 | 守卫晚于 core 路由注册，/health、/stats/*、/{engine}/search 完全绕过黑名单 | cmd/serve.go 通过 ServerOptions.ExtraMiddleware 在所有路由注册前安装 earlyBlacklistGuard |
| P1 | MCP 平面不解析用户，viewer/禁用账号可免费使用引擎 | 与第 1 轮 AuthAny 修复合并 |
| P2 | 配置种子规则删除后每次重启复活 | settings 桶写入一次性 blacklist_seeded 标记 |
| P2 | 管理员可把自己 IP 加黑导致全站自锁 | 创建时拒绝覆盖当前 IP，确需自锁须显式 force:true |
| P2 | 最后一名管理员 TOCTOU（同因） | 已修复 |
| P3 | 命中计数读改写竞态、store 故障 fail-open | fail-open 为已文档化的降级策略；计数竞态仅影响诊断，保留为已知限制 |

### 第 5 轮：前后端 / 配置 / 文档一致性（audit-integration）

发现并修复：

| 级别 | 问题 | 修复 |
| --- | --- | --- |
| P2 | 并发创建同名用户可绕过唯一性校验 | handleCreateUser 纳入 userMutationMu |
| P2 | 前端允许非 admin 查看用户列表，服务端要求 admin | 前端改为明确提示无权查看；角色解析默认 viewer（fail-closed） |
| P2 | 编辑黑名单清空过期时间发送 null，后端 nil 无法区分“未传” | 前端改发空串表示永久 |
| P2 | 配置用户名改为既有非管理员账号时会重置其密码 | 启动同步仅限 RoleAdmin 用户 |
| P3 | OpenAPI 429 错误名 too_many_requests 与代码 rate_limited 不符 | 文档统一为 rate_limited |
| P3 | force 参数与全站守卫范围未写入契约 | docs/API.md / docs/internal/openapi-admin.yaml 已补充 |

## 本机 100 项真机验证

- 运行脚本：qa/features-audit/http_audit.py（100 个独立检查，覆盖可达性、登录身份、用户角色、会话密码、密钥与公网 API、黑名单、日志、稳定性与最终拦截）。
- 最终结果：**100/100 通过**。原始逐项结果：qa/features-audit/http-results.json；Markdown 报告：docs/audit/features-http-audit.md。
- 首次基线为 92/100，其中 8 项失败经复核为测试期望与既有 API 契约不符（如 /api/keys 返回数组、未知路径由静态站返回 HTML 404、operator 访问用户管理被角色守卫拒绝），脚本期望修正后重跑；另发现的产品缺陷已按上表修复。
- 修复后复验：go build ./...、go vet ./...、go test -count=1 -p 1 ./...、node --check web/admin/assets/app.js、git diff --check 全部退出码 0。

## 保留的已知限制（非阻塞）

1. GET /api/audit 只读当前 audit.log，不读轮转归档；/api/logs 的 total 受 20 万行扫描上限约束。
2. 黑名单 store 读取失败时 fail-open（已在 API 文档明示）。
3. CLI admin-backup / admin-restore 对 JSON fallback 与新增桶的恢复覆盖不完整；API 备份路径不受影响。
4. Windows 本机无本地 C 编译器；后续已通过 Docker Linux 镜像完成全量 `go test -race`（见 docs/TESTING.md 与 qa/features-audit/race-final.log），无 DATA RACE。


