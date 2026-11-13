# 配置参考

配置来源与优先级（Viper）：

```
CLI 参数  >  OPENSERP_* 环境变量  >  config.yaml  >  代码内置默认值
```

环境变量命名规则：`OPENSERP_` + 配置键转大写、`.` 换成 `_`。例如：

| 配置键 | 环境变量 |
| --- | --- |
| `server.port` | `OPENSERP_SERVER_PORT` |
| `app.browser_path` | `OPENSERP_APP_BROWSER_PATH` |
| `baidu.disabled` | `OPENSERP_BAIDU_DISABLED` |
| `sogou.rate_requests` | `OPENSERP_SOGOU_RATE_REQUESTS` |
| `google.resolve_urls` | `OPENSERP_GOOGLE_RESOLVE_URLS` |

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
| `mega_timeout` | `90s` | `90s` | `/mega/*` 总预算，超时返回部分结果 |

## extract（`/extract`、`/extract/batch`、内联 `extract=N`）

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `enabled` | `true` | `true` | 关闭后端点返回 404 |
| `default_mode` | `auto` | `auto` | `auto/fast/rendered` |
| `timeout` | `20s` | `20s` | 单页面提取超时 |
| `max_bytes` | `2000000` | `2000000` | 抓取字节上限 |
| `max_concurrent` | `2` | `2` | 并发提取数 |
| `allow_private_networks` | `false` | — | 允许抓取内网地址（默认禁止） |

批量端点单次最多 20 个 URL（硬编码 `maxBatchExtractURLs`）。

## proxies

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `global` | 空 | 空 | 全局代理（等价 `--proxy`） |
| `entries[]` | 空 | 空 | 代理池：`{url, tags[]}` |
| `allow_request_proxy_url` | `false` | `true` | 允许 `X-Proxy-URL` 头覆盖 |
| `health.failure_threshold` | `3` | `2` | 连续失败多少次判定代理不健康 |
| `lanes.enabled` | `true` | `true` | 按“引擎+代理会话”复用浏览器 cookie |
| `lanes.max_lanes` | `100` | `100` | sticky lane 的 LRU 上限 |
| `lanes.drop_cookies_on_challenge` | `true` | `true` | 遇验证码时清该 lane 的 cookie |

## cache / resilience / circuit_breaker

| 键 | 默认值 | 本仓库 | 说明 |
| --- | --- | --- | --- |
| `cache.ttl_seconds` | `300` | `120` | 专用端点 JSON 缓存 TTL，0 关闭 |
| `cache.max_size` | `1000` | `1000` | 缓存条目上限 |
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

## captcha / 2captcha

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `captcha.solver_enabled` | `false` | 全局验证码求解开关 |
| `2captcha.apikey` | 空 | 2captcha key，配合上一项使用 |

被拦截时服务会返回 429 `captcha_detected`；开启求解后部分引擎会尝试自动过验证码。

## 引擎通用配置

每个引擎（`google` `yandex` `baidu` `bing` `duckduckgo` `ecosia` `sogou` `so360`）都支持：

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `rate_requests` | `6` | 限速窗口内允许的请求数 |
| `rate_seconds` | `60` | 限速窗口（秒） |
| `rate_burst` | `1` | 突发令牌数 |
| `selector_timeout` | `5` | 等待页面元素的秒数（搜狗/360 内部下限 8s） |
| `captcha` | `false` | 引擎级验证码求解开关 |
| `profile` | 空（自动选） | 固定浏览器指纹，如 `chrome-win-uhd620` |
| `proxy` | 空 | 绑定 `proxies.entries` 中的 tag |
| `disabled` | `false` | 关闭后不注册端点、不参与 mega |

### Bing 额外项

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `bing.search_box` | `true` | 通过首页搜索框提交查询（更贴近真实用户，避免不相干结果） |

### Google 额外项

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `google.resolve_urls` | `true` | 解析 Google 跳转链接：`true`/`false`/`N` |
| `google.resolve_concurrency` | `4` | 并发解析数 |
| `google.resolve_timeout` | `3s` | 单次解析预算 |
| `google.resolve_cooldown` | `10m` | 命中挑战后的冷却时间 |

## 本仓库的实际取值（摘要）

```yaml
baidu: { rate_requests: 12, rate_burst: 2, profile: chrome-win-uhd620 }
bing:  { rate_requests: 60, rate_burst: 3, search_box: true }
sogou: { rate_requests: 8,  rate_burst: 1, profile: chrome-win-uhd620 }
so360: { rate_requests: 20, rate_burst: 2, profile: chrome-win-uhd620 }
google/yandex/duckduckgo/ecosia: { disabled: true }
circuit_breaker: { failures: 8, recovery_seconds: 20, successes: 1 }
```

## 其它环境变量

| 变量 | 说明 |
| --- | --- |
| `OPENSERP_ENRICHMENT_DOMAINS_FILE` | 覆盖域名分类数据文件（`core/enrichment_domains.yaml`） |
| `OPENSERP_SERVER_CONFIG_PATH` | 显式指定配置文件路径（等价 `--config`） |
