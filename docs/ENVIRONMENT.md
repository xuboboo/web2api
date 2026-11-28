# 环境变量与配置项（ENVIRONMENT）

> 本文依据 `cmd/root.go` 中 80 个 `v.SetDefault(...)` 调用整理（含 1 个多行 map）；引擎通用 schema 另列于文末表格，只有代码明确设置默认值的项目才保证可通过对应环境变量覆盖。
> 命名规则：`WEB2API_` + 键名大写、`.` 换 `_`（例：`platform.admin_password` → `WEB2API_PLATFORM_ADMIN_PASSWORD`）。
> 优先级：命令行参数 > 环境变量 > 配置文件 > 内置默认值。

## 使用方式

```bash
# 单进程
export WEB2API_PLATFORM_ADMIN_PASSWORD='换成强密码'
export WEB2API_SERVER_HOST=0.0.0.0 WEB2API_SERVER_PORT=7000
web2api serve -c /etc/web2api/config.yaml
```

```yaml
# docker compose（等价写法）
environment:
  WEB2API_SERVER_HOST: "0.0.0.0"
  WEB2API_PLATFORM_ADMIN_PASSWORD: "${ADMIN_PASSWORD:?required}"
```

## 必读：三个不能带出厂值的项

| 配置项 | 出厂值 | 上线要求 |
| --- | --- | --- |
| `platform.admin_password` | 代码内置的公开默认密码 | **必须改**，否则任何人都能登录管理面 |
| `platform.data_dir` | `./data` | 必须指向**持久化**目录（bbolt、`secret.key`、任务结果都在这里；丢 `secret.key` 会让所有 JWT 失效） |
| `security.metrics_token` | 空 | 若 `/metrics` 需要跨机抓取，必须设令牌；否则保持空 + 仅本机采集 |

## 全量清单


### 服务监听（`server.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `server.host` | `WEB2API_SERVER_HOST` | "127.0.0.1" | 条件 | 监听地址。容器内必须 0.0.0.0；裸机建议保持 127.0.0.1 走反代 |
| `server.port` | `WEB2API_SERVER_PORT` | 7070 | 条件 | 监听端口 |
| `server.debug` | `WEB2API_SERVER_DEBUG` | false | 否 | 调试日志（生产关闭） |
| `server.verbose` | `WEB2API_SERVER_VERBOSE` | false | 否 | 更详细的引擎日志 |
| `server.quiet` | `WEB2API_SERVER_QUIET` | false | 否 | 静默模式，仅错误 |
| `server.raw_requests` | `WEB2API_SERVER_RAW_REQUESTS` | false | 否 | 仅用 HTTP 通道（不启浏览器），排障用 |
| `server.insecure` | `WEB2API_SERVER_INSECURE` | false | 否 | 跳过 TLS 校验，仅内网排障 |

### 进程/浏览器/日志（`app.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `app.log_format` | `WEB2API_APP_LOG_FORMAT` | "" | 否 | 日志格式（json / text） |
| `app.log_level` | `WEB2API_APP_LOG_LEVEL` | "" | 否 | 日志级别 |
| `app.save_log` | `WEB2API_APP_SAVE_LOG` | true | 否 | 是否落盘 |
| `app.log_max_mb` | `WEB2API_APP_LOG_MAX_MB` | 10 | 否 | 单个日志文件上限（MB） |
| `app.log_backups` | `WEB2API_APP_LOG_BACKUPS` | 5 | 否 | 保留的历史日志个数 |
| `app.log_retention_days` | `WEB2API_APP_LOG_RETENTION_DAYS` | 30 | 否 | 按 mtime 删除超过 N 天的 `logs.txt.N`/`audit.log.N`；0=不按天删除 |
| `app.timeout` | `WEB2API_APP_TIMEOUT` | 30 | 是 | 单次引擎请求超时（秒），决定单条查询的失败边界 |
| `app.browser_path` | `WEB2API_APP_BROWSER_PATH` | "" | 条件 | Chrome/Chromium 可执行文件。容器内由 Dockerfile 固定为 headless-shell |
| `app.profiles` | `WEB2API_APP_PROFILES` | "" | 否 | 浏览器指纹 profile 目录/集合 |
| `app.head` | `WEB2API_APP_HEAD` | false | 否 | 无头模式 |
| `app.leave_head` | `WEB2API_APP_LEAVE_HEAD` | false | 否 | 保留窗口（调试） |
| `app.leakless` | `WEB2API_APP_LEAKLESS` | false | 否 | 防僵尸进程模式（容器内由 tini 负责） |
| `app.block_resources` | `WEB2API_APP_BLOCK_RESOURCES` | "" | 否 | 拦截的资源类型（加速） |
| `app.block_trackers` | `WEB2API_APP_BLOCK_TRACKERS` | false | 否 | 拦截追踪器 |
| `app.debug_endpoints` | `WEB2API_APP_DEBUG_ENDPOINTS` | false | 否 | 兼容旧配置。指纹诊断仅可通过本机 `web2api fingerprint-check` 运行。 |
| `app.max_processes` | `WEB2API_APP_MAX_PROCESSES` | 4 | 是 | 浏览器进程池上限，直接决定并发与内存 |
| `app.idle_ttl` | `WEB2API_APP_IDLE_TTL` | "10m" | 否 | 空闲浏览器回收时间 |
| `app.max_concurrent_requests` | `WEB2API_APP_MAX_CONCURRENT_REQUESTS` | 0 | 是 | 在途请求上限，0=不限（不建议） |

### 代理池（`proxies.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `proxies.entries` | `WEB2API_PROXIES_ENTRIES` | []interface{}{} | 否 | 见 docs/CONFIGURATION.md |
| `proxies.global` | `WEB2API_PROXIES_GLOBAL` | "" | 否 | 见 docs/CONFIGURATION.md |
| `proxies.allow_request_proxy_url` | `WEB2API_PROXIES_ALLOW_REQUEST_PROXY_URL` | false | 否 | 见 docs/CONFIGURATION.md |
| `proxies.health.failure_threshold` | `WEB2API_PROXIES_HEALTH_FAILURE_THRESHOLD` | core.DefaultProxyFailureThreshold | 否 | 见 docs/CONFIGURATION.md |
| `proxies.lanes.enabled` | `WEB2API_PROXIES_LANES_ENABLED` | true | 否 | 见 docs/CONFIGURATION.md |
| `proxies.lanes.max_lanes` | `WEB2API_PROXIES_LANES_MAX_LANES` | core.DefaultProxyLaneMaxLanes | 否 | 见 docs/CONFIGURATION.md |
| `proxies.lanes.drop_cookies_on_challenge` | `WEB2API_PROXIES_LANES_DROP_COOKIES_ON_CHALLENGE` | true | 否 | 见 docs/CONFIGURATION.md |

### 缓存（`cache.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `cache.ttl_seconds` | `WEB2API_CACHE_TTL_SECONDS` | 300 | 否 | 检索响应缓存 TTL，0=关 |
| `cache.max_size` | `WEB2API_CACHE_MAX_SIZE` | 1000 | 否 | 缓存条目上限 |
| `cache.index_check_ttl_seconds` | `WEB2API_CACHE_INDEX_CHECK_TTL_SECONDS` | 0 | 否 | 收录查询缓存 TTL，0=关（推荐关） |

### 重试与降级（`resilience.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `resilience.max_retries` | `WEB2API_RESILIENCE_MAX_RETRIES` | 3 | 是 | 单引擎重试次数 |
| `resilience.allow_endpoint_fallback` | `WEB2API_RESILIENCE_ALLOW_ENDPOINT_FALLBACK` | false | 否 | 专用端点失败是否回退到其它引擎 |

### 熔断（`circuit_breaker.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `circuit_breaker.failures` | `WEB2API_CIRCUIT_BREAKER_FAILURES` | 5 | 是 | 连续失败多少次打开熔断 |
| `circuit_breaker.recovery_seconds` | `WEB2API_CIRCUIT_BREAKER_RECOVERY_SECONDS` | 60 | 是 | 熔断后半开等待秒数 |
| `circuit_breaker.successes` | `WEB2API_CIRCUIT_BREAKER_SUCCESSES` | 2 | 是 | 半开状态恢复所需成功次数 |

### 跨域（`cors.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `cors.enabled` | `WEB2API_CORS_ENABLED` | true | 否 | 见 docs/CONFIGURATION.md |
| `cors.allow_origins` | `WEB2API_CORS_ALLOW_ORIGINS` | "*" | 否 | 见 docs/CONFIGURATION.md |
| `cors.allow_methods` | `WEB2API_CORS_ALLOW_METHODS` | "GET, POST, OPTIONS" | 否 | 见 docs/CONFIGURATION.md |
| `cors.allow_headers` | `WEB2API_CORS_ALLOW_HEADERS` | core.DefaultCORSAllowHeaders | 否 | 见 docs/CONFIGURATION.md |
| `cors.max_age` | `WEB2API_CORS_MAX_AGE` | 86400 | 否 | 见 docs/CONFIGURATION.md |

### 验证码求解（`captcha.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `captcha.solver_enabled` | `WEB2API_CAPTCHA_SOLVER_ENABLED` | false | 否 | 见 docs/CONFIGURATION.md |

### 平台（API Key、配额、管理员）（`platform.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `platform.enabled` | `WEB2API_PLATFORM_ENABLED` | true | 是 | 是否启用 API Key/JWT/用量记账（关闭=R1 裸服务） |
| `platform.data_dir` | `WEB2API_PLATFORM_DATA_DIR` | "./data" | 是 | bbolt 数据库、secret.key、任务目录所在目录，必须可写并持久化 |
| `platform.admin_username` | `WEB2API_PLATFORM_ADMIN_USERNAME` | "admin" | 是 | 管理员用户名 |
| `platform.admin_password` | `WEB2API_PLATFORM_ADMIN_PASSWORD` | "admin123" | 是 | 管理员密码。**默认值是公开的出厂值，上线前必须改** |
| `platform.jwt_expire_hours` | `WEB2API_PLATFORM_JWT_EXPIRE_HOURS` | 24 | 是 | 管理员 JWT 有效期（小时） |
| `platform.usage_retention_days` | `WEB2API_PLATFORM_USAGE_RETENTION_DAYS` | 30 | 否 | 用量账本保留天数 |

### 稳定性（冷却/收录重试）（`stability.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `stability.engine_cooldown.steps_minutes` | `WEB2API_STABILITY_ENGINE_COOLDOWN_STEPS_MINUTES` | []int{10, 30, 120} | 否 | 通用引擎惩罚档位（分钟） |
| `stability.engine_cooldown.reset_after_minutes` | `WEB2API_STABILITY_ENGINE_COOLDOWN_RESET_AFTER_MINUTES` | 240 | 否 | 连续无风控多久后清空冷却档位（分钟） |
| `stability.engine_cooldown.per_engine` | 见下方（Viper map 由 YAML 配置） | `baidu.steps_minutes: []int{2, 10, 30}` | 否 | 按引擎覆盖档位；百度首档 2 分钟，其余引擎沿用 10/30/120 |
| `stability.index_check.max_retries` | `WEB2API_STABILITY_INDEX_CHECK_MAX_RETRIES` | 1 | 否 | 见 docs/CONFIGURATION.md |

### 异步批量任务（`index_jobs.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `index_jobs.enabled` | `WEB2API_INDEX_JOBS_ENABLED` | true | 否 | 开启异步批量收录任务（>20 条转 202） |
| `index_jobs.dir` | `WEB2API_INDEX_JOBS_DIR` | "" | 否 | 任务状态/结果落盘目录，空=data_dir/index_jobs |
| `index_jobs.max_urls` | `WEB2API_INDEX_JOBS_MAX_URLS` | 5000 | 否 | 单任务 URL 上限 |
| `index_jobs.chunk` | `WEB2API_INDEX_JOBS_CHUNK` | 1 | 否 | 每 N 条落一次进度 |
| `index_jobs.max_unit_retries` | `WEB2API_INDEX_JOBS_MAX_UNIT_RETRIES` | 6 | 否 | 单条被冷却/验证码挡住后的重排次数 |

### 引擎：百度（`baidu.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `baidu.index_title_probe` | `WEB2API_BAIDU_INDEX_TITLE_PROBE` | true | 否 | 见 docs/CONFIGURATION.md |
| `baidu.cookie_rotate_every` | `WEB2API_BAIDU_COOKIE_ROTATE_EVERY` | 8 | 否 | 见 docs/CONFIGURATION.md |
| `baidu.homepage_warm_on_rotate` | `WEB2API_BAIDU_HOMEPAGE_WARM_ON_ROTATE` | true | 否 | 见 docs/CONFIGURATION.md |

### 安全与限速（`security.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `security.admin_rate_per_minute` | `WEB2API_SECURITY_ADMIN_RATE_PER_MINUTE` | 60 | 是 | 管理面全局每分钟请求上限 |
| `security.admin_rate_per_ip` | `WEB2API_SECURITY_ADMIN_RATE_PER_IP` | 30 | 是 | 管理面单 IP 每分钟上限（防单客户端打满全局额度） |
| `security.api_rate_per_minute` | `WEB2API_SECURITY_API_RATE_PER_MINUTE` | 120 | 是 | 单个 API Key 每分钟查询上限 |
| `security.unauth_rate_per_minute` | `WEB2API_SECURITY_UNAUTH_RATE_PER_MINUTE` | 30 | 是 | 未通过鉴权请求的单 IP 上限 |
| `security.data_plane_auth` | `WEB2API_SECURITY_DATA_PLANE_AUTH` | true | 是 | 数据面是否强制 X-API-Key。**默认 true；裸启也不匿名开放** |
| `security.trust_proxy_headers` | `WEB2API_SECURITY_TRUST_PROXY_HEADERS` | false | 条件 | 仅供受信 TLS 反代场景；启用后按受信来源的 X-Real-IP 取客户端 IP |
| `security.trusted_proxies` | `WEB2API_SECURITY_TRUSTED_PROXIES` | `["127.0.0.1/32","::1/128"]` | 条件 | 允许提供 X-Real-IP 的反代 IP/CIDR；跨容器反代需显式加入其 CIDR |
| `security.mcp_allowed_origins` | `WEB2API_SECURITY_MCP_ALLOWED_ORIGINS` | []string{} | 否 | MCP 允许的跨域 Origin 列表 |
| `security.metrics_token` | `WEB2API_SECURITY_METRICS_TOKEN` | "" | 条件 | /metrics 的 Bearer；空=仅 loopback 可抓 |
| `security.login_lockout.fails` | `WEB2API_SECURITY_LOGIN_LOCKOUT_FAILS` | 5 | 是 | 同一用户名跨 IP 连续失败多少次后锁定 |
| `security.login_lockout.minutes` | `WEB2API_SECURITY_LOGIN_LOCKOUT_MINUTES` | 15 | 是 | 锁定时长（分钟） |
| `security.password_policy.min_length` | `WEB2API_SECURITY_PASSWORD_POLICY_MIN_LENGTH` | 8 | 否 | 改密最小长度 |
| `security.password_policy.min_classes` | `WEB2API_SECURITY_PASSWORD_POLICY_MIN_CLASSES` | 0 | 否 | 见 docs/CONFIGURATION.md |
| `security.blacklist` | `WEB2API_SECURITY_BLACKLIST` | `[]` | 否 | 首启种子 IP/CIDR 黑名单（一次性标记落 settings 桶），之后以 API/控制台为准 |

### 引擎：必应（`bing.*`）

| 配置项 | 环境变量 | 默认值 | 必需 | 用途 |
| --- | --- | --- | --- | --- |
| `bing.search_box` | `WEB2API_BING_SEARCH_BOX` | true | 否 | 见 docs/CONFIGURATION.md |

### 其它运行时环境变量（非 SetDefault 键）

| 变量 | 说明 |
| --- | --- |
| `WEB2API_SERVER_CONFIG_PATH` | 显式指定配置文件路径（等价 `--config`） |
| `WEB2API_STRICT_CONFIG=1` | 未知配置键由启动告警升级为启动失败 |
| `WEB2API_ENRICHMENT_DOMAINS_FILE` | 覆盖域名分类数据文件 |
| `WEB2API_2CAPTCHA_APIKEY` | 仅当 config.yaml 存在 `2captcha:` 节时绑定；推荐密钥不落盘 |

## 引擎级配置（按引擎重复）

`baidu.*` / `bing.*` / `sogou.*` / `so360.*` 共享同一组键，环境变量同样按引擎前缀命名：

| 键 | 环境变量示例 | 默认 | 说明 |
| --- | --- | --- | --- |
| `rate_requests` | `WEB2API_BAIDU_RATE_REQUESTS` | 6 | 限速窗口内请求数；百度实测建议 5（=12 秒/条） |
| `rate_seconds` | `WEB2API_BAIDU_RATE_SECONDS` | 60 | 限速窗口 |
| `rate_burst` | `WEB2API_BAIDU_RATE_BURST` | 1 | 突发令牌数，生产建议 1 |
| `selector_timeout` | `WEB2API_BAIDU_SELECTOR_TIMEOUT` | 5 | 等待页面元素秒数 |
| `captcha` | `WEB2API_BAIDU_CAPTCHA` | false | 引擎级验证码求解开关 |
| `profile` | `WEB2API_BAIDU_PROFILE` | 空 | 固定浏览器指纹 |
| `proxy` | `WEB2API_BAIDU_PROXY` | 空 | 绑定 `proxies.entries` 的 tag |
| `index_title_probe` | `WEB2API_BAIDU_INDEX_TITLE_PROBE` | false | 收录查询用页面标题检索（百度建议开） |
| `cookie_rotate_every` | `WEB2API_BAIDU_COOKIE_ROTATE_EVERY` | 0 | 每 N 次导航清 Cookie |
| `homepage_warm_on_rotate` | `WEB2API_BAIDU_HOMEPAGE_WARM_ON_ROTATE` | false | 轮换后预热首页 |
| `disabled` | `WEB2API_BAIDU_DISABLED` | false | 关闭该引擎端点 |

## 敏感项清单（禁止进入版本库）

- `platform.admin_password`、`security.metrics_token`、`2captcha.apikey`
- `proxies.entries[].url`（含代理账号密码）
- `data/secret.key`、`data/web2api.db`（API key 哈希 + 管理员 bcrypt + 用量）

已核对：`.gitignore` 忽略 `data/`、`data-*/`、`config-qa*`；`.dockerignore` 已排除 `data/`、`*.exe`、`logs*`（本次审计修复，见 `docs/audit/FINDINGS.md` F-02）。
