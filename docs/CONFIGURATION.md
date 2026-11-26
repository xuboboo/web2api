# 配置参考

配置来源与优先级（Viper）：

```
CLI 参数  >  WEB2API_* 环境变量  >  config.yaml  >  代码内置默认值
```

环境变量命名规则：`WEB2API_` + 配置键转大写、`.` 换成 `_`。例如：

| 配置键 | 环境变量 |
| --- | --- |
| `server.port` | `WEB2API_SERVER_PORT` |
| `app.browser_path` | `WEB2API_APP_BROWSER_PATH` |
| `baidu.disabled` | `WEB2API_BAIDU_DISABLED` |
| `sogou.rate_requests` | `WEB2API_SOGOU_RATE_REQUESTS` |
| `platform.admin_password` | `WEB2API_PLATFORM_ADMIN_PASSWORD` |

> 注意:整个应用固定部署在 `/web-tool` 子路径下(代码内常量 `core.BasePath`),
> 没有 `base_path` 之类的配置项,也不支持根路径挂载。

下文“默认值”指代码内置默认（`cmd/root.go` 的 `setConfigDefaults` 与 `core.SearchEngineOptions.Init`），“本仓库”指仓库自带 `config.yaml` 的实际取值。

## server

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `host` | `127.0.0.1` | `0.0.0.0` | 监听地址（容器内需 0.0.0.0） |
| `port` | `7070` | `7000` | 监听端口 |
| `debug` | `false` | `false` | 调试日志并强制浏览器有头模式 |
| `verbose` | `false` | `false` | 请求级 debug 日志 |
| `quiet` | `false` | — | 抑制 stderr 的 info 日志（CLI 默认安静） |
| `raw_requests` | `false` | `false` | 全局纯 HTTP 模式（跳过浏览器，仅限支持 raw 的引擎） |
| `insecure` | `false` | `false` | 跳过出站 TLS 证书校验；仅在受控排障时临时开启。容器安装 `ca-certificates` |

## app

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `log_format` | 空（按模式推导） | `text` | `json` 或 `text` |
| `log_level` | 空（CLI=warn，serve=info） | 空 | `error/warn/info/debug/trace` |
| `save_log` | `true` | `true` | serve 模式额外写 `./logs.txt` |
| `log_max_mb` | `10` | `10` | `logs.txt` 单文件上限（MB），0=仅按天保留、不按大小轮转 |
| `log_backups` | `5` | `5` | 历史日志份数（审计日志另有独立上限，不受 0 影响） |
| `log_retention_days` | `30` | `30` | 滚动日志按 mtime 保留天数（详见下文「app 日志轮转」） |
| `timeout` | `30` | `15` | 单次引擎尝试超时（秒） |
| `browser_path` | 空（自动探测） | 空 | 自定义 Chrome/Chromium 路径 |
| `profiles` | 空 | 空 | 覆盖内置指纹目录的 JSON 文件 |
| `head` | `false` | `false` | 有头模式 |
| `leakless` | `false` | `false` | 每次请求后强制结束浏览器 |
| `leave_head` | `false` | `false` | 保留标签页（调试用） |
| `block_resources` | 空 | `image,font,css,media` | 浏览器模式屏蔽的子资源 |
| `block_trackers` | `false` | `true` | 屏蔽已知追踪域名 |
| `debug_endpoints` | `false` | — | 开启 `/debug/fingerprint-check` |
| `max_processes` | `4` | `1` | 并发 Chrome 进程上限（代码默认 4；本仓库为低内存场景固定为 1） |
| `idle_ttl` | `10m` | `45s` | 空闲 Chrome 回收时间（低内存优化；默认 10m） |
| `max_concurrent_requests` | `0`（不限） | `2` | 在途搜索/收录请求并发上限，0=不限。小内存机器用它压峰值内存，超出排队 |

## platform（收录查询平台：管理控制台 + /v1/* 公网 API + /api/* 管理 API）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `enabled` | `true` | `true` | 关闭后不挂载 `/api/*` 与 `/v1/*`，官网与控制台静态页与健康端点仍可用 |
| `data_dir` | `./data` | `./data` | bbolt 数据库 `web2api.db`、JWT 密钥 `secret.key` 的存放目录 |
| `admin_username` | `admin` | `admin` | 控制台管理员登录名 |
| `admin_password` | `admin123` | `admin123` | 控制台管理员密码（生产环境务必修改） |
| `jwt_expire_hours` | `24` | `24` | 管理员 JWT 有效期（小时） |
| `usage_retention_days` | `30` | `30` | 用量账本保留天数，启动时与每 256 条写入后滚动清理；0=不限 |

说明:存储主路径为 bbolt(纯 Go 无 CGO)。S4-B-06 起**锁冲突 fail-closed**:
数据目录被第二个进程占用时启动直接失败(避免双实例分裂记账);JSON 文件回退
(`<data_dir>/web2api.json`)仅覆盖非锁类打开失败,`/health` 的 `storage` 字段
暴露实际存储类型。API key 只存 SHA-256 哈希,完整 key 仅在创建/轮换时返回一次。
1 条 URL × 1 个引擎 = 1 次;`daily_quota=0` 为不限。管理员会话(sessions 桶)支持
逐会话撤销与滑动续期,审计轨迹落 `<data_dir>/audit.log`(JSON Lines,与
app.log_max_mb/log_backups 同轮转机制)。

角色：平台固定三种角色 `admin` / `operator` / `viewer`（权限 `admin > operator > viewer`），没有新增平台配置键。`admin_username` / `admin_password` 仅在首次启动迁移进 `users` 桶，之后用户库是唯一事实来源；不含 `uid`/`role` 声明的旧 JWT 会按 `sub` 查找持久化用户，并按其**当前角色与 active 状态**逐请求校验，不会自动按 `admin` 放行；仅当用户库为空时才回退到内置 bootstrap admin。MCP 平面的 JWT 还必须携带 `sid` 且持久化角色为 `admin`，否则 403 `insufficient_role`。各 `/api/*` 端点按最小角色守卫，详见 [API.md](API.md) §6。

## proxies

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `global` | 空 | 空 | 全局代理（等价 `--proxy`） |
| `entries[]` | 空 | 空 | 代理池：`{url, tags[]}` |
| `allow_request_proxy_url` | `false` | `false` | 允许 `X-Proxy-URL` 头覆盖(S4-B-03 出厂默认已翻转;既有部署若依赖自带代理需显式改回 `true`) |
| `health.failure_threshold` | `3` | `2` | 连续失败多少次判定代理不健康 |
| `lanes.enabled` | `true` | `true` | 按“引擎+代理会话”复用浏览器 cookie |
| `lanes.max_lanes` | `100` | `100` | sticky lane 的 LRU 上限 |
| `lanes.drop_cookies_on_challenge` | `true` | `true` | 遇验证码时清该 lane 的 cookie |

## cache / resilience / circuit_breaker

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `cache.ttl_seconds` | `300` | `120` | 专用端点 JSON 缓存 TTL，0 关闭 |
| `cache.max_size` | `1000` | `1000` | 缓存条目上限 |
| `cache.index_check_ttl_seconds` | `0` | `0` | S4: index/check 批量响应缓存 TTL（秒），0=关（保持每次真实打引擎）；命中响应 `meta.cache="hit"`（additive），含错误/冷却单元的响应不缓存 |
| `index_jobs.enabled` | `true` | `true` | 开启异步批量收录任务（>20 条 URL 时返回 202 + job_id） |
| `index_jobs.dir` | 空 | 空 | 任务状态与结果落盘目录，空 = `<platform.data_dir>/index_jobs`；进程重启后从这里续跑 |
| `index_jobs.max_urls` | `5000` | `5000` | 单次提交上限（去重后计数） |
| `index_jobs.chunk` | `1` | `1` | 每处理多少条落一次进度（1 = 每条都落；设大只会让进度条按 2 分钟一跳，省不下什么 IO） |
| `index_jobs.max_unit_retries` | `6` | `6` | 单条 URL 被冷却/验证码挡住后的重排次数 |
| `resilience.max_retries` | `3` | `1` | 每引擎重试次数 |
| `resilience.allow_endpoint_fallback` | `false` | `false` | 允许专用端点失败后回退到其它引擎 |
| `circuit_breaker.failures` | `5` | `8` | 连续失败多少次打开熔断 |
| `circuit_breaker.recovery_seconds` | `60` | `20` | 熔断后多久进入半开 |
| `circuit_breaker.successes` | `2` | `1` | 半开状态下成功几次恢复 |

注意：captcha / blocked / rate_limited / parser / engine_internal / not_implemented 属于**不重试**错误，直接进入熔断统计。

### 批量任务队列（`index_jobs`）

`index_jobs.retention_days` 默认为 0，长期保留任务。设为正整数后，启动时及每 12 小时清理完成时间超过该天数的终态任务。运行中的任务及尚未记录完成时间的历史任务会保留。

定时巡检计划通过管理 API 创建，默认计划均为暂停状态。`index_jobs.schedule_enabled` 默认为 `false`；只有显式设为 true 才启动周期调度器，`schedule_interval` 默认 1h。计划按 UTC 每日或每周运行，重启后按持久化的下一次运行时间恢复；密钥失效时计划暂停。计划数据存于平台设置存储中，并受全局计划数、每用户计划数和累计 URL 数上限保护，防止设置记录和调度载入无界增长。

一次提交超过 20 条 URL 时，`POST /v1/index/check` 不再报错，而是返回 `202` + `job_id`，由服务端按引擎节奏排队执行（详见 [API.md §2.2b](API.md)）：

- **同步契约不变**：≤20 条仍是 200 同步返回，控制台在线调试也保持 20 条上限（它只渲染同步信封）。
- **串行执行**：同一时刻只跑一个任务——引擎按出口 IP 限速，并发只会互相抢令牌；其余任务 FIFO 排队。
- **冷却不烧额度**：所选引擎全部冷却时任务转 `waiting`，等冷却结束再继续；单条被冷却/验证码挡住会重排 `max_unit_retries` 次。
- **断点续跑**：`<dir>/<job_id>/job.json` 存元数据、`results.jsonl` 逐条追加；进程重启后自动续跑，已完成的 URL 不重复查询。
- **计费**：入队时按 `条数 × 引擎数` 一次性计费；查进度、取结果、取消都不计费。
- 关掉 `enabled` 后队列不启动，>20 条恢复为 400 `TOO_MANY_URLS`，`/v1/index/check/jobs*` 路由不注册（404）。

## cors

| 键 | 默认值 | 本仓库 |
| --- | --- | --- |
| `enabled` | `true` | `true` |
| `allow_origins` | `*` | `*` |
| `allow_methods` | `GET, POST, OPTIONS` | 同默认 |
| `allow_headers` | 见 `cmd/root.go` | 默认列表 + `X-Use-Profile` |
| `max_age` | `86400` | `86400` |

## security（S4 新增键）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `admin_rate_per_minute` | `60` | `60` | `/api/*` 全局限速 |
| `admin_rate_per_ip` | `30` | `30` | S4-B-05: `/api/*` 按 IP 分桶限速（0=关） |
| `api_rate_per_minute` | `120` | `120` | S4-B-02: `/v1/*` 与 MCP 每 key 限速（0=关） |
| `unauth_rate_per_minute` | `30` | `30` | S4-B-02: 无有效凭据请求按 IP 前置限速（0=关） |
| `data_plane_auth` | `true` | `true` | `serve` 的 Viper 默认与本仓库模板均为 true；只有显式设为 false 才开放匿名专用数据面。`platform.DefaultSecurityConfig()` 的 false 仅用于直接构造结构体，不影响 CLI/serve 默认值 |
| `trust_proxy_headers` | `false` | `false` | TLS 反代场景开启；仅受信代理来源可按 `X-Real-IP` 取得客户端 IP，受信 HTTPS 请求下发 HSTS |
| `trusted_proxies` | `["127.0.0.1/32","::1/128"]` | `["127.0.0.1/32","::1/128"]` | 允许提供 `X-Real-IP` 的反代 IP/CIDR；Nginx 必须用 `$remote_addr` 覆盖该头。跨容器反代要显式加入其地址/CIDR |
| `blacklist` | `[]` | `[]` | 初始 IP/CIDR 黑名单规则（如 `["203.0.113.7","198.51.100.0/24"]`），**仅首次启动写入**；之后以控制台/API 管理为准。客户端 IP 取自 `trust_proxy_headers` / `trusted_proxies` |
| `mcp_allowed_origins` | 空 | 空 | S4-B-10: MCP Origin 白名单（空=仅同源；空 Origin 始终放行） |
| `metrics_token` | 空 | 空 | S4: `/web-tool/metrics` Bearer 令牌（空=仅 loopback 抓取） |
| `login_lockout.fails` | `5` | `5` | 登录锁定阈值 |
| `login_lockout.minutes` | `15` | `15` | 登录锁定时长 |
| `password_policy.min_length` | `8` | `8` | S4-B-11: 改密最小长度 |
| `password_policy.min_classes` | `0` | `0` | S4-B-11: 改密字符类数要求（内置常用密码黑名单始终生效） |

## stability（引擎 IP 冷却状态机）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `engine_cooldown.steps_minutes` | `[10, 30, 120]` | 同默认 | 连续第 1/2/3+ 次被拦截（blocked 403 / 429 / captcha）的冷却分钟数（最后一个值向后延续） |
| `engine_cooldown.reset_after_minutes` | `240` | 同默认 | 连续无拦截达此时长后连续计数归零，重新从第 1 档起算 |
| `engine_cooldown.per_engine.<engine>.steps_minutes` | 空 | `baidu: [2,10,30]` | 按引擎覆盖档位；本仓库给 `baidu` 配 `[2, 10, 30]`（实测停 2 分钟即可恢复） |
| `index_check.max_retries` | `1` | `1` | 收录单元的重试次数；被拦即失败，避免把请求量放大 4 倍喂同一出口 IP |

冷却期内该引擎所有 search/check 快速失败（503 `engine_cooldown` 信封 + `Retry-After`），不发真实请求；到期放行一个探测请求——成功即恢复，再次被拦截进下一档，其他失败（如超时）按当前档时长顺延重试。运维干预：`POST /web-tool/api/engines/{name}/cooldown/reset` 强制恢复、`cooldown/force {"minutes":N}` 强制冷却（维护窗口）。

## app 日志轮转（S4 新增键）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `app.log_max_mb` | `10` | `10` | serve 模式 `logs.txt` 单文件上限（MB），超过滚动为 `logs.txt.1/.2…`；`0` 仅关闭按大小轮转，按天清理与份数上限仍生效 |
| `app.log_backups` | `5` | `5` | 保留的历史日志份数（`audit.log` 同机制） |
| `app.log_retention_days` | `30` | `30` | 按 mtime 删除超过 N 天的滚动日志（`logs.txt.N` / `audit.log.N`）；`0` 关闭按天删除，只按 `log_backups` 份数保留 |

历史滚动文件在启动时、每次轮转后以及每 12 小时自动清理一次；管理员也可调用 `POST /api/logs/cleanup` 立即清理（见 [API.md](API.md) §6.13、[OPERATIONS.md](OPERATIONS.md) §2）。当前 `logs.txt` / `audit.log` 不会被时间清理删除。

启动校验（S4-GAP 运维）: 不认识的配置键启动时输出 Warn 清单；
`WEB2API_STRICT_CONFIG=1` 时转为启动失败（退出码非 0）。

## 优雅停机与停止信号（运维注记，T-01）

serve 收到首个停止信号后进入排水（30 秒上限），随后关闭日志/端口/存储并把
**成功停机的退出码统一为 0**。停止信号的可投递方式按平台区分：

- **Linux/macOS**：`SIGTERM`（`kill <pid>`、systemd stop）与控制台 `Ctrl+C`（`SIGINT`）均有效。
- **Windows**：进程注册的 `SIGTERM` 不可投递——请使用**控制台事件**（`Ctrl+C`、
  `taskkill /pid <pid>`（不带 `/F`，触发 CTRL_BREAK））或服务包装器（NSSM /
  Windows Service）投递停止信号；`taskkill /F` 为强杀，不走排水。Windows 控制
  台以 `Ctrl+C` 停止时，进程显式 `os.Exit(0)` 收尾，避免旧版以 0xC000013A
  （STATUS_CONTROL_C_EXIT）作为退出码。

## captcha / 2captcha

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `captcha.solver_enabled` | `false` | 全局验证码求解开关 |
| `2captcha.apikey` | 空 | 2captcha key，配合上一项使用 |

被拦截时服务会返回 429 `captcha_detected`；开启求解后部分引擎会尝试自动过验证码。

## 引擎通用配置

每个引擎（`baidu` `sogou` `so360` `bing`）都支持：

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `rate_requests` | `6` | 限速窗口内允许的请求数 |
| `rate_seconds` | `60` | 限速窗口（秒） |
| `rate_burst` | `1` | 突发令牌数 |
| `selector_timeout` | `5` | 等待页面元素的秒数（搜狗/360 内部下限 8s） |
| `captcha` | `false` | 引擎级验证码求解开关 |
| `profile` | 空（自动选） | 固定浏览器指纹，如 `chrome-win-uhd620` |
| `proxy` | 空 | 绑定 `proxies.entries` 中的 tag |
| `disabled` | `false` | 关闭后不注册端点 |
| `index_title_probe` | `false` | 收录查询用页面标题而不是 URL 当检索词（百度建议开启，见下） |
| `cookie_rotate_every` | `0` | 每 N 次导航清一次 Cookie 罐并可选预热首页，`0`=不轮换 |
| `homepage_warm_on_rotate` | `false` | 轮换后访问引擎首页，模拟回访用户 |

### 百度收录查询（index_title_probe / 节奏）

实测（767 条真实链接）：

- 用 `wd=<文章URL>` 检索，**已收录**的文章也常返回空结果 → 漏判；改用页面 `<title>` 检索可消除漏判，标题匹配阈值 0.72（带截断省略号/来源后缀归一化）。
- 触发验证码与请求节奏强相关：`3.2s/条` 约 50%，`10s/条` 约 1/23，`12s/条`+每 8 条轮换 Cookie 约 1/132。
- 单次验证码停 ~2 分钟即可恢复，首档冷却 10 分钟会显著拖低吞吐。

推荐配置：

```yaml
baidu:
  rate_requests: 5      # 12s/条
  rate_burst: 1
  index_title_probe: true
  cookie_rotate_every: 8
  homepage_warm_on_rotate: true
stability:
  engine_cooldown:
    per_engine:
      baidu: { steps_minutes: [2, 10, 30] }
  index_check:
    max_retries: 1
```

`index_title_probe` 需要目标页可直连取标题（HTTP GET，不经过百度或搜索代理；为避免远端代理 DNS 解析绕过私网防护）。取不到或服务器不能直连时自动回退到 URL 短语检索，搜索引擎 SERP 请求仍按引擎代理配置发送。

### Bing 额外项

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `bing.search_box` | `true` | 通过首页搜索框提交查询（更贴近真实用户，避免不相干结果） |

## 本仓库的实际取值（摘要）

```yaml
baidu: { rate_requests: 5, rate_burst: 1, profile: chrome-win-uhd620,
         index_title_probe: true, cookie_rotate_every: 8, homepage_warm_on_rotate: true }
bing:  { rate_requests: 60, rate_burst: 3, search_box: true }
sogou: { rate_requests: 8,  rate_burst: 1, profile: chrome-win-uhd620 }
so360: { rate_requests: 20, rate_burst: 2, profile: chrome-win-uhd620 }
stability: { engine_cooldown: { per_engine: { baidu: { steps_minutes: [2, 10, 30] } } }, index_check: { max_retries: 1 } }
circuit_breaker: { failures: 8, recovery_seconds: 20, successes: 1 }
platform: { enabled: true, data_dir: ./data, admin_username: admin, admin_password: admin123, jwt_expire_hours: 24 }
```

## 其它环境变量

| 变量 | 说明 |
| --- | --- |
| `WEB2API_ENRICHMENT_DOMAINS_FILE` | 覆盖域名分类数据文件（`core/enrichment_domains.yaml`） |
| `WEB2API_SERVER_CONFIG_PATH` | 显式指定配置文件路径（等价 `--config`） |
