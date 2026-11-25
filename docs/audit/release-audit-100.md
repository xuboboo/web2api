# web2api 本机发布门禁 100 项验证

本次运行时间（执行主机）为 2026-11-25T14:27:32+08:00。结果为 100/100 项通过。

每项执行独立命名的 Go 测试。最终判定要求每项都有顶层通过事件、其子用例无跳过、Go 测试进程正常退出。
使用 Go 竞态检测器和临时测试目录。百度等实时查询及外部代理用例关闭，1 万条存储负载由本机模拟数据构造。

Go 测试退出码为 0。原始 JSON 事件保存在本机 `qa/audit100/go-test.jsonl`，SHA-256 为 `4718e201d3202f6444d2b41bfb41510aa1602c6f8f66ee08f40024d0fda48cbf`。

## 本轮修复及部署判定

| 修复 | 验证 |
| --- | --- |
| 配置管理员密码发生变化时，新密码哈希和 JWT 版本一起提交，旧令牌跨重启失效。 | 009 |
| 损坏的短签名密钥或读取错误阻止启动并保留现有文件，新建密钥使用排他创建。 | 010 |
| 用量和任务 CSV 将可能被电子表格视为公式的文本作为字面值输出。 | 029、030、050 |
| 停机先进入排水状态。REST 和 MCP 返回可重试的 503，并拒绝接收新增异步任务。 | 037、048 |

全量普通 Go 测试、`go vet`、golangci-lint、两套 Compose 静态校验、控制台脚本语法检查和 Windows/amd64、Linux/amd64 构建通过。依赖扫描报告 0 个可达漏洞和 1 个未调用的模块公告。

Windows 本机更新后二进制 SHA-256 为 `d45ac6cc0a6835082daf65d7dc92e12c4f44d26ad2c1e365cb9d7b9f9b1a783b`，与验收候选文件一致。实例沿用 bbolt，17 项本机 HTTP 检查通过，百度实网查询 0 次。实际 Docker 容器和生产环境验收未完成。应用仅监听 `127.0.0.1:7000`，改动尚未推送。

## 管理身份与会话

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 001 | 登录和受保护管理接口 | [`TestLoginAndAdminAuth`](../../platform/api_test.go#L215) | 通过（4.16s） |
| 002 | 管理接口无凭据时拒绝请求 | [`TestProtectedEndpointsNeverAnswerJSONWithoutAuth`](../../platform/security_test.go#L165) | 通过（0.80s） |
| 003 | 连续失败触发登录锁定 | [`TestLoginLockoutAfterConsecutiveFailures`](../../platform/security_test.go#L60) | 通过（6.58s） |
| 004 | 单账号的登录预算跨来源生效 | [`TestLoginLockoutCannotBeBypassedByRotatingIPs`](../../platform/security_test.go#L107) | 通过（4.47s） |
| 005 | 改密使旧令牌失效 | [`TestPasswordChangeInvalidatesOldTokens`](../../platform/security_test.go#L266) | 通过（7.73s） |
| 006 | JWT 版本状态异常时拒绝授权 | [`TestAdminTokenRejectsInvalidVersionState`](../../platform/security_test.go#L225) | 通过（1.44s） |
| 007 | 刷新与撤销并发时持久化撤销生效 | [`TestSessionRevocationSurvivesConcurrentRefresh`](../../platform/security_test.go#L236) | 通过（0.81s） |
| 008 | 会话续期更新过期时间 | [`TestSessionRefreshSlidesExpiry`](../../platform/s4_test.go#L272) | 通过（1.94s） |
| 009 | 配置密码跨重启变更后旧 JWT 失效 | [`TestConfigPasswordChangeInvalidatesPriorJWT`](../../platform/infra_test.go#L410) | 通过（4.60s） |
| 010 | 损坏签名密钥拒绝启动且保留原件 | [`TestNewRejectsCorruptSecretWithoutOverwritingIt`](../../platform/infra_test.go#L440) | 通过（0.01s） |

## API 鉴权、限流与配额

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 011 | 密钥鉴权与配额基础契约 | [`TestAPIKeyGuardAuthAndQuota`](../../platform/api_test.go#L354) | 通过（2.12s） |
| 012 | REST 和 MCP 并发共享配额 | [`TestDailyQuotaSharesPendingBudgetBetweenRESTAndMCP`](../../platform/api_test.go#L643) | 通过（2.28s） |
| 013 | 输入被拒绝时不扣费 | [`TestQuotaNotConsumedOnBadRequest`](../../platform/api_test.go#L683) | 通过（1.94s） |
| 014 | 配额拒绝提供重试时间 | [`TestQuotaExceededCarriesRetryAfter`](../../platform/security_test.go#L377) | 通过（1.57s） |
| 015 | 密钥独立限速 | [`TestPerKeyRateLimit`](../../platform/s4_test.go#L70) | 通过（1.92s） |
| 016 | 未鉴权请求限速 | [`TestUnauthRateLimit`](../../platform/s4_test.go#L89) | 通过（1.07s） |
| 017 | 密钥过期及允许来源规则 | [`TestKeyExpiresAtAndAllowedIPs`](../../platform/s4_test.go#L143) | 通过（2.15s） |
| 018 | 轮换后旧密钥失效且保留属主记录 | [`TestKeyRotate`](../../platform/s4_test.go#L103) | 通过（1.68s） |
| 019 | MCP 计费状态规则 | [`TestChargeUsageConsumesOnlyOn200`](../../platform/security_test.go#L533) | 通过（1.58s） |
| 020 | MCP 按成功引擎单元结算 | [`TestAuthAnyMCPChargesEngineOKUnits`](../../platform/security_test.go#L821) | 通过（1.49s） |

## 存储、账本与数据输出

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 021 | bbolt 数据、并发更新与跨日记账 | [`TestBoltStore`](../../platform/infra_test.go#L328) | 通过（0.06s） |
| 022 | JSON 兼容存储与重启保留 | [`TestJSONStore`](../../platform/infra_test.go#L339) | 通过（0.05s） |
| 023 | JSON 落盘失败后回滚用量和凭据状态 | [`TestJSONStoreRollsBackFailedWrites`](../../platform/infra_test.go#L282) | 通过（0.01s） |
| 024 | bbolt 不可用时走约定的 JSON 降级 | [`TestNewFallsBackToJSONWhenBoltBlocked`](../../platform/infra_test.go#L461) | 通过（1.06s） |
| 025 | 重启保留签名密钥与管理员哈希 | [`TestNewReusesSecretAndHash`](../../platform/infra_test.go#L366) | 通过（5.17s） |
| 026 | 一万条流水的统计和分页正确性 | [`TestBoltUsageScale10k`](../../platform/usage_scale_test.go#L157) | 通过（24.56s） |
| 027 | 过期流水清理 | [`TestUsageRetentionPrunesOldRecords`](../../platform/security_test.go#L692) | 通过（0.77s） |
| 028 | 流水索引与聚合一致性 | [`TestBoltUsageIndexAccuracy`](../../platform/s4_test.go#L437) | 通过（0.27s） |
| 029 | 导出和对账契约 | [`TestUsageExportAndReconcile`](../../platform/s4_test.go#L361) | 通过（2.33s） |
| 030 | CSV 文本字段保持字面含义且正确转义 | [`TestSpreadsheetCSVCellPreservesLiteralText`](../../core/index_job_test.go#L411) | 通过（0.00s） |

## 任务落盘与中断恢复

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 031 | 入队前验证输入和引擎 | [`TestIndexJobSubmitValidation`](../../core/index_job_test.go#L43) | 通过（0.02s） |
| 032 | 每条任务结果落盘并正确结算进度 | [`TestIndexJobRunsToDone`](../../core/index_job_test.go#L78) | 通过（0.04s） |
| 033 | 重启回放持久化结果及残缺尾行 | [`TestIndexJobResumesAfterRestart`](../../core/index_job_test.go#L118) | 通过（0.05s） |
| 034 | 重启缺少目标引擎时禁止改跑 | [`TestIndexJobRetainsEngineSelectionAfterRestart`](../../core/index_job_test.go#L173) | 通过（0.03s） |
| 035 | 恢复积压超过通道容量也能启动 | [`TestIndexJobRestoresBacklogBeyondQueueCapacity`](../../core/index_job_test.go#L189) | 通过（0.05s） |
| 036 | 满队列拒绝后无幽灵任务 | [`TestIndexJobFullQueueRejectsWithoutPersisting`](../../core/index_job_test.go#L218) | 通过（0.06s） |
| 037 | 正常停服保留未完成任务 | [`TestIndexJobShutdownLeavesInFlightJobResumable`](../../core/index_job_test.go#L234) | 通过（0.02s） |
| 038 | 写入结果失败时任务失败 | [`TestIndexJobFailsWhenResultCannotBePersisted`](../../core/index_job_test.go#L260) | 通过（0.01s） |
| 039 | 取消持久化失败后可重试 | [`TestIndexJobCancelRequiresPersistedTransition`](../../core/index_job_test.go#L285) | 通过（0.01s） |
| 040 | 任务完成释放常驻 URL 列表 | [`TestIndexJobReleasesMemoryWhenDone`](../../core/index_job_test.go#L475) | 通过（0.02s） |

## HTTP 装配与任务契约

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 041 | 实际装配中健康、管理与数据面契约 | [`TestPlatformAssemblyContract`](../../cmd/serve_platform_test.go#L150) | 通过（4.42s） |
| 042 | 实际装配中任务读取和取消受密钥边界保护 | [`TestIndexJobEndpointsAreScopedAcrossAPIKeysThroughAssembly`](../../cmd/wiring_test.go#L483) | 通过（2.73s） |
| 043 | 数据面鉴权开关行为 | [`TestDataPlaneAuthSwitch`](../../cmd/wiring_test.go#L294) | 通过（1.82s） |
| 044 | 静态站点、控制台及路由前缀 | [`TestStaticMountContract`](../../cmd/wiring_test.go#L421) | 通过（1.16s） |
| 045 | 静态挂载边界保护配置 | [`TestStaticMountRejectsConfigPathTraversal`](../../cmd/wiring_test.go#L463) | 通过（1.22s） |
| 046 | 大批量返回异步任务回执 | [`TestIndexCheckLargeBatchReturnsJob`](../../core/index_job_http_test.go#L69) | 通过（0.13s） |
| 047 | 小批量仍返回同步结果 | [`TestIndexCheckSmallBatchStaysSynchronous`](../../core/index_job_http_test.go#L161) | 通过（0.01s） |
| 048 | 停机排水时 REST 与 MCP 拒绝新增异步任务 | [`TestDrainingRejectsAsyncSubmissionsWithoutScheduling`](../../core/index_job_http_test.go#L137) | 通过（0.00s） |
| 049 | 控制台试用保持同步批量上限 | [`TestPlaygroundKeepsSynchronousCeiling`](../../core/index_job_http_test.go#L196) | 通过（0.00s） |
| 050 | 任务 CSV 含 BOM、引号和逐项错误 | [`TestIndexJobCSV`](../../core/index_job_test.go#L380) | 通过（0.00s） |

## MCP 协议与租户边界

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 051 | Agent 请求需要凭据 | [`TestUnauthorizedIsRejectedByAuthLayer`](../../mcp/mcp_test.go#L602) | 通过（0.00s） |
| 052 | 不允许的来源被拒绝 | [`TestInvalidOriginIs403`](../../mcp/mcp_test.go#L613) | 通过（0.00s） |
| 053 | 会话鉴权、归属、关闭和失效 | [`TestSessionLifecycle`](../../mcp/mcp_test.go#L648) | 通过（0.00s） |
| 054 | 凭据充分时兼容无会话调用 | [`TestSessionlessRequestsStayAccepted`](../../mcp/mcp_test.go#L700) | 通过（0.00s） |
| 055 | Agent 队列满载返回可重试 HTTP 错误 | [`TestCheckIndexedQueueFullReturnsRetryableHTTPError`](../../mcp/mcp_jobs_test.go#L53) | 通过（0.00s） |
| 056 | Agent 批量任务入队契约 | [`TestCheckIndexedLargeBatchBecomesJob`](../../mcp/mcp_jobs_test.go#L14) | 通过（0.00s） |
| 057 | Agent 任务状态与结果访问 | [`TestGetIndexJobStatusAndResults`](../../mcp/mcp_jobs_test.go#L89) | 通过（0.00s） |
| 058 | 公开规范操作完整性 | [`TestOpenAPISpecContractInvariants`](../../mcp/mcp_test.go#L858) | 通过（0.02s） |
| 059 | MCP 路由识别与计费 | [`TestAuthAnyMCPBillingUnits`](../../platform/security_test.go#L432) | 通过（1.45s） |
| 060 | Agent 预检和管理员口径 | [`TestAuthAnyMCPQuotaPrecheckAndJWT`](../../platform/security_test.go#L491) | 通过（1.43s） |

## 出站连接、缓存与代理信任

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 061 | 出站 URL 私有地址拦截 | [`TestValidatePublicHTTPURLRejectsPrivateTargets`](../../core/network_guard_test.go#L10) | 通过（0.01s） |
| 062 | 出站 URL 协议限制 | [`TestValidatePublicHTTPURLRejectsUnsupportedScheme`](../../core/network_guard_test.go#L34) | 通过（0.00s） |
| 063 | 标题探测 DNS 变化不触达受保护目的地 | [`TestFetchPageTitleDNSRebindingCannotReachPrivateTargetOrProxy`](../../core/title_fetch_test.go#L105) | 通过（0.00s） |
| 064 | 正常探测和查询回退行为 | [`TestGuardedTitleProbePublicPathAndFallbackRemainUsable`](../../core/title_fetch_test.go#L180) | 通过（0.00s） |
| 065 | 标题探测负缓存有效期 | [`TestFetchPageTitleReusesNegativeResultUntilExpiry`](../../core/title_fetch_test.go#L54) | 通过（0.00s） |
| 066 | 可信反代的客户端地址提取 | [`TestTrustedProxyClientIP`](../../core/server_proxy_test.go#L12) | 通过（0.00s） |
| 067 | 非可信反代来源头不受信任 | [`TestUntrustedPeerCannotSpoofClientIP`](../../core/server_proxy_test.go#L38) | 通过（0.00s） |
| 068 | 请求级代理地址默认关闭 | [`TestRequestProxyURLDisabledByDefault`](../../core/server_test.go#L1045) | 通过（0.00s） |
| 069 | 代理凭据日志脱敏 | [`TestMaskProxyURLRedactsCredentials`](../../core/proxy_test.go#L186) | 通过（0.00s） |
| 070 | 缓存按出口市场区分 | [`TestBuildCacheKeyUsesProxyMarketNotSessionOrURL`](../../core/cache_test.go#L226) | 通过（0.00s） |

## 并发预算、熔断和故障降级

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 071 | 连续错误触发断路 | [`TestCircuitBreaker_OpensAfterThreshold`](../../core/circuit_breaker_test.go#L16) | 通过（0.02s） |
| 072 | 半开探测成功恢复 | [`TestCircuitBreaker_HalfOpenSuccessClosesCircuit`](../../core/circuit_breaker_test.go#L66) | 通过（0.03s） |
| 073 | 半开探测失败重新断路 | [`TestCircuitBreaker_HalfOpenFailureReopens`](../../core/circuit_breaker_test.go#L100) | 通过（0.03s） |
| 074 | 冷却升级与时间档位 | [`TestCooldownTierProgression`](../../core/engine_cooldown_test.go#L62) | 通过（0.00s） |
| 075 | 手动重置使旧探测失效 | [`TestCooldownResetInvalidatesInFlightProbe`](../../core/engine_cooldown_test.go#L389) | 通过（0.00s） |
| 076 | 单引擎冷却不影响其他引擎 | [`TestCooldownDoesNotFreezeOtherEngines`](../../core/engine_cooldown_test.go#L941) | 通过（0.00s） |
| 077 | 冷却期间快速失败且零引擎接触 | [`TestDedicatedSearchCooldownFastFailAndZeroEngineContact`](../../core/engine_cooldown_test.go#L512) | 通过（0.01s） |
| 078 | 批处理超时预算 | [`TestIndexCheckBatchDeadline`](../../core/index_check_deadline_test.go#L19) | 通过（0.00s） |
| 079 | 不同端点单独设置请求期限 | [`TestRequestTimeoutMiddlewareScalesIndexCheckPaths`](../../core/index_check_deadline_test.go#L103) | 通过（0.00s） |
| 080 | 客户端上下文错误不计为引擎故障 | [`TestResilientSearchPrimary_ContextErrorsDoNotOpenCircuit`](../../core/resilient_context_test.go#L86) | 通过（0.00s） |

## 四引擎本地样本

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 081 | 百度 URL 构造与参数编码 | [`TestBuildURL`](../../baidu/all_test.go#L10) | 通过（0.02s） |
| 082 | 百度冻结样本解析 | [`TestBaiduParseHTMLFixtures`](../../baidu/search_raw_test.go#L16) | 通过（0.01s） |
| 083 | 百度验证码、无结果和正常页分类 | [`TestBaiduClassifyRawHTML`](../../baidu/search_raw_test.go#L49) | 通过（0.02s） |
| 084 | 必应 URL 构造与参数编码 | [`TestBuildURL`](../../bing/bing_test.go#L10) | 通过（0.02s） |
| 085 | 必应冻结样本结构化区块 | [`TestParseHTMLFixtureExtractsRealFeatures`](../../bing/serp_features_test.go#L13) | 通过（0.07s） |
| 086 | 搜狗 URL 构造与分页 | [`TestBuildURL`](../../sogou/url_test.go#L10) | 通过（0.00s） |
| 087 | 搜狗忽略隐藏结果 | [`TestParseHTMLSkipsHiddenCards`](../../sogou/parse_html_test.go#L73) | 通过（0.00s） |
| 088 | 搜狗拦截页分类 | [`TestParseHTMLCaptcha`](../../sogou/parse_html_test.go#L92) | 通过（0.00s） |
| 089 | 360 URL 构造与参数编码 | [`TestBuildURL`](../../so360/url_test.go#L10) | 通过（0.01s） |
| 090 | 360 区分广告和普通结果 | [`TestParseHTMLMarksAds`](../../so360/parse_html_test.go#L77) | 通过（0.00s） |

## 启动、发布配置与备份恢复

| # | 验证项 | 测试源码 | 结果 |
| --- | --- | --- | --- |
| 091 | 镜像环境变量契约 | [`TestDockerfileEnvContract`](../../cmd/dockerfile_env_test.go#L15) | 通过（0.02s） |
| 092 | 镜像默认配置契约 | [`TestContainerConfigShipsDefaults`](../../cmd/dockerfile_env_test.go#L51) | 通过（0.00s） |
| 093 | 镜像包安装失败传递错误 | [`TestDockerfilePackageInstallFailureCannotBeMasked`](../../cmd/dockerfile_env_test.go#L69) | 通过（0.00s） |
| 094 | Compose 运行权限与配置一致性 | [`TestComposeManifestsShareContainerSecurityBaseline`](../../cmd/dockerfile_env_test.go#L88) | 通过（0.00s） |
| 095 | 无效配置拒绝启动 | [`TestInitializeConfigRejectsMalformedFile`](../../cmd/log_level_test.go#L123) | 通过（0.01s） |
| 096 | 备份输出不得指向源存储 | [`TestAdminBackupRejectsOutputThatAliasesSourceDatabase`](../../cmd/admin_backup_test.go#L19) | 通过（0.06s） |
| 097 | 完整备份跨目录恢复 | [`TestAdminFullBackupAndRestoreRoundTrip`](../../cmd/admin_backup_test.go#L76) | 通过（0.34s） |
| 098 | 恢复条目留在目标目录边界内 | [`TestAdminFullRestoreRejectsPathTraversal`](../../cmd/admin_backup_test.go#L172) | 通过（0.03s） |
| 099 | 恢复前检查清单校验值 | [`TestAdminFullRestoreRejectsManifestChecksumMismatch`](../../cmd/admin_backup_test.go#L198) | 通过（0.08s） |
| 100 | 挂载卷原地恢复及原数据保留 | [`TestApplyStagedAdminBundleInPlacePreservesMountedVolumeContents`](../../cmd/admin_backup_test.go#L250) | 通过（0.08s） |
