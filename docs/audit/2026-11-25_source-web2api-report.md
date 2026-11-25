# web2api 独立源码复审

复审日期为 2026-11-25。基线为 Git 提交 `7326057` 及当前工作区改动，结论来自当前源码、新的运行结果和工作区差异。授权范围为本地仓库与 Windows 本机实例。

## 结论

复审确认了管理员会话撤销、密码轮换、密钥账本、任务持久化、MCP 会话归属和异步任务引擎选择六组问题。对应修复已写入源码，并增加了并发、存储写入失败和路由边界的离线回归。运行验证范围以本机与模拟引擎为限。

## 证据和修复

| 编号 | 级别 | 现象与影响 | 修复和证据 |
| --- | --- | --- | --- |
| R1 | P1 | 管理员会话刷新与撤销都可能写回完整快照，撤销后仍可能留下有效的持久化状态。 | 刷新、撤销和活跃时间更新串行写入，会话校验以持久化状态为准。见 [sessions.go](../../platform/sessions.go) 和 `TestSessionRevocationSurvivesConcurrentRefresh`、`TestSessionsLifecycle`。 |
| R2 | P1 | 密码哈希、配置标记和 JWT 版本分开提交，写入半途失败后状态可能分裂。读取版本失败时曾按版本 0 处理。 | 三个设置在一次存储提交中更新。JWT 校验在版本不可读时拒绝，刷新前再次核对已授权的版本。见 [handlers_admin.go](../../platform/handlers_admin.go)、[platform.go](../../platform/platform.go) 和 `TestPasswordChangeInvalidatesOldTokens`、`TestAdminTokenRejectsInvalidVersionState`。 |
| R3 | P1 | 管理员编辑或轮换 API Key 可能覆盖并发提交的用量。跨日乱序写入可能使当日配额回退，JSON 写盘失败还可能留下只在内存生效的计数或设置。 | bbolt 与 JSON 在最新记录上原子更新密钥，旧日流水只增加累计量。JSON 提交失败回滚内存改动。见 [store_bolt.go](../../platform/store_bolt.go)、[store_json.go](../../platform/store_json.go) 和 `TestBoltStore`、`TestJSONStore`、`TestJSONStoreRollsBackFailedWrites`。 |
| R4 | P1 | 新任务曾接受无效引擎，恢复时也可能因引擎下线而改用其他引擎。 | 入队前校验并持久化实际引擎清单。重启缺少指定引擎时将任务标记失败。见 [index_job_manager.go](../../core/index_job_manager.go) 和 `TestIndexJobSubmitValidation`、`TestIndexJobRetainsEngineSelectionAfterRestart`。 |
| R5 | P2 | 取消任务时，元数据写入失败仍可能向客户端确认取消。 | 取消先提交磁盘状态，再停止执行。写入失败可重试。见 [index_job_manager.go](../../core/index_job_manager.go)、[index_job_http.go](../../core/index_job_http.go) 和 `TestIndexJobCancelRequiresPersistedTransition`。 |
| R6 | P2 | MCP 会话关闭缺少身份校验，POST 会话未绑定创建者。部分配额和路由判断与 Fiber 的匹配口径不一致。 | MCP POST 和 DELETE 都检查鉴权与会话归属。配额和计费读取已匹配路由，REST 查询使用一致的 JSON 解析方式。见 [mcp.go](../../mcp/mcp.go)、[security.go](../../platform/security.go)、[middleware.go](../../platform/middleware.go) 和 `TestSessionLifecycle`、`TestAuthAnyMCPBillingUnits`。 |

## 覆盖范围

- 读码范围覆盖 REST、管理 API、MCP、任务队列、bbolt 与 JSON、标题探测出站防护、代理和反代配置、控制台取数及发布配置。备份与恢复使用仓库内离线回归。
- 风险边界包括管理员凭据与会话、API Key 所有权和额度、任务文件及其状态转换、来自用户或上游站点的数据、服务端出站连接。
- 生产域名、公网部署、第三方搜索引擎实时结果和压力容量属于本轮实测范围之外。持久化错误可能限制服务处理请求的能力，回滚仅覆盖上述变更路径。

## 本轮验证结果

| 检查 | 结果 |
| --- | --- |
| `go test -p 2 -count=1 ./...` | 11 个测试包通过，含备份与恢复、出站防护和密钥并发回归 |
| `go test -race -tags=integration -p 2 -count=1 ./...` | Windows amd64，11 个测试包通过，未报告竞态。设置 `WEB2API_INTEGRATION_TESTS=0`，跳过实时引擎用例 |
| `go vet -p 2 ./...`、golangci-lint 2.13.2、`go mod verify` | 通过，linter 报告 0 个问题 |
| govulncheck 1.10.0 | 0 个可达漏洞，1 个未调用的模块公告 `GO-2026-5932` |
| `TestBoltUsageScale10k` | 通过，使用临时 bbolt 库写入 1 万条模拟流水。统计、分页和时间序列断言通过 |
| Compose 和公开接口描述 | 两套 Compose 静态配置校验通过，MCP OpenAPI 的 POST 和 DELETE 通过契约测试 |
| Windows 本机实例 | 更新后二进制与构建 SHA-256 一致，沿用原 bbolt 数据目录。17 项本机 HTTP 检查通过，覆盖健康、控制台、管理员登录和刷新、注销、API Key 生命周期及 MCP 会话关闭。监听地址为 `radar.ydsdoa.cn` |

本机二进制 SHA-256 为 `68ccf42bb64c6852c8061f87efcd98b3dc69d3b96602316c6971f4631648b5a7`。部署仅更新本机，改动仍在工作区。验证期间对百度的实网查询为 0 次。
