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
| `insecure` | `false` | `true` | 允许不安全 TLS（容器/Linux 缺根证书时需要） |

## app

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `log_format` | 空（按模式推导） | `text` | `json` 或 `text` |
| `log_level` | 空（CLI=warn，serve=info） | 空 | `error/warn/info/debug/trace` |
| `save_log` | `true` | `true` | serve 模式额外写 `./logs.txt` |
| `timeout` | `30` | `15` | 单次引擎尝试超时（秒） |
| `browser_path` | 空（自动探测） | 空 | 自定义 Chrome/Chromium 路径 |
| `profiles` | 空 | 空 | 覆盖内置指纹目录的 JSON 文件 |
| `head` | `false` | `false` | 有头模式 |
| `leakless` | `false` | `false` | 每次请求后强制结束浏览器 |
| `leave_head` | `false` | `false` | 保留标签页（调试用） |
| `block_resources` | 空 | `image,font,css,media` | 浏览器模式屏蔽的子资源 |
| `block_trackers` | `false` | `true` | 屏蔽已知追踪域名 |
| `debug_endpoints` | `false` | — | 开启 `/debug/fingerprint-check` |
| `max_processes` | `4` | `6` | 并发 Chrome 进程上限 |
| `idle_ttl` | `10m` | `5m` | 空闲 Chrome 回收时间 |

## platform（收录查询平台：管理控制台 + /v1/* 公网 API + /api/* 管理 API）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `enabled` | `true` | `true` | 关闭后不挂载 `/api/*` 与 `/v1/*`，官网与控制台静态页与健康端点仍可用 |
| `data_dir` | `./data` | `./data` | bbolt 数据库 `web2api.db`、JWT 密钥 `secret.key` 的存放目录 |
| `admin_username` | `admin` | `admin` | 控制台管理员登录名 |
| `admin_password` | `admin123` | `admin123` | 控制台管理员密码（生产环境务必修改） |
| `jwt_expire_hours` | `24` | `24` | 管理员 JWT 有效期（小时） |

说明:存储主路径为 bbolt(纯 Go 无 CGO)。S4-B-06 起**锁冲突 fail-closed**:
数据目录被第二个进程占用时启动直接失败(避免双实例分裂记账);JSON 文件回退
(`<data_dir>/web2api.json`)仅覆盖非锁类打开失败,`/health` 的 `storage` 字段
暴露实际存储类型。API key 只存 SHA-256 哈希,完整 key 仅在创建/轮换时返回一次。
1 URL × 1 引擎 = 1 unit;`daily_quota=0` 为不限。管理员会话(sessions 桶)支持
逐会话撤销与滑动续期,审计轨迹落 `<data_dir>/audit.log`(JSON Lines,与
app.log_max_mb/log_backups 同轮转机制)。

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
| `resilience.max_retries` | `3` | `1` | 每引擎重试次数 |
| `resilience.allow_endpoint_fallback` | `false` | `false` | 允许专用端点失败后回退到其它引擎 |
| `circuit_breaker.failures` | `5` | `8` | 连续失败多少次打开熔断 |
| `circuit_breaker.recovery_seconds` | `60` | `20` | 熔断后多久进入半开 |
| `circuit_breaker.successes` | `2` | `1` | 半开状态下成功几次恢复 |

注意：captcha / blocked / rate_limited / parser / engine_internal / not_implemented 属于**不重试**错误，直接进入熔断统计。

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
| `data_plane_auth` | `false` | `true` | S4-B-03: 数据面鉴权开关（本仓库模板已翻转；代码默认仍 false 兼容既有部署） |
| `trust_proxy_headers` | `false` | `false` | S4-B-14: TLS 反代场景开启后按 `X-Forwarded-Proto=https` 下发 HSTS |
| `mcp_allowed_origins` | 空 | 空 | S4-B-10: MCP Origin 白名单（空=仅同源；空 Origin 始终放行） |
| `metrics_token` | 空 | 空 | S4: `/web-tool/metrics` Bearer 令牌（空=仅 loopback 抓取） |
| `login_lockout.fails` | `5` | `5` | 登录锁定阈值 |
| `login_lockout.minutes` | `15` | `15` | 登录锁定时长 |
| `password_policy.min_length` | `8` | `8` | S4-B-11: 改密最小长度 |
| `password_policy.min_classes` | `0` | `0` | S4-B-11: 改密字符类数要求（内置常用密码黑名单始终生效） |

## app 日志轮转（S4 新增键）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `app.log_max_mb` | `10` | `10` | serve 模式 `logs.txt` 单文件上限（MB），超过滚动为 `logs.txt.1/.2…`（0=不轮转） |
| `app.log_backups` | `5` | `5` | 保留的历史日志份数（`audit.log` 同机制） |

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

### Bing 额外项

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `bing.search_box` | `true` | 通过首页搜索框提交查询（更贴近真实用户，避免不相干结果） |

## 本仓库的实际取值（摘要）

```yaml
baidu: { rate_requests: 12, rate_burst: 2, profile: chrome-win-uhd620 }
bing:  { rate_requests: 60, rate_burst: 3, search_box: true }
sogou: { rate_requests: 8,  rate_burst: 1, profile: chrome-win-uhd620 }
so360: { rate_requests: 20, rate_burst: 2, profile: chrome-win-uhd620 }
circuit_breaker: { failures: 8, recovery_seconds: 20, successes: 1 }
platform: { enabled: true, data_dir: ./data, admin_username: admin, admin_password: admin123, jwt_expire_hours: 24 }
```

## 其它环境变量

| 变量 | 说明 |
| --- | --- |
| `WEB2API_ENRICHMENT_DOMAINS_FILE` | 覆盖域名分类数据文件（`core/enrichment_domains.yaml`） |
| `WEB2API_SERVER_CONFIG_PATH` | 显式指定配置文件路径（等价 `--config`） |
