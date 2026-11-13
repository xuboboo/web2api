# web2api

面向**国内搜索引擎**的 Web → API 服务：把百度、搜狗、360 搜索的结果页转换成结构化 JSON，并提供多引擎聚合、网页正文提取、缓存、限流与反爬兜底。一个 `docker compose up` 即可运行。

> 基于开源项目 [OpenSERP](https://github.com/karust/openserp)（MIT）改造：保留其 API 契约、响应模型与引擎框架，重点完成了**国内引擎适配**（新增搜狗、360）、百度 `limit` 修复，以及反爬场景下的稳定性机制（桌面指纹、HTTP 兜底、CAPTCHA 门控）。

## 引擎与端点

| 引擎 | 名称/别名 | 搜索端点 | 图片端点 | 备注 |
| --- | --- | --- | --- | --- |
| 百度 | `baidu` | `/baidu/search` | `/baidu/image` | 支持日期区间、翻页 |
| 搜狗 | `sogou` | `/sogou/search` | —（返回 501） | `sogou.com/link` 跳转自动解析为真实地址 |
| 360 搜索 | `so360` / `360` | `/360/search`、`/so360/search` | —（返回 501） | 同一引擎双端点；优先取 `data-mdurl` 直达链接 |
| Bing 中国 | `bing` | `/bing/search` | `/bing/image` | `www.bing.com` 自动跳转 `cn.bing.com`，作兜底 |

境外引擎（`google`、`yandex`、`duckduckgo`（别名 `duck`/`ddg`）、`ecosia`）代码保留，但在仓库的 `config.yaml` 中默认 `disabled: true`：无代理时会被 403/429/captcha 拦截。设置 `disabled: false` 并配好代理即可重新启用。

其他端点：

| 端点 | 说明 |
| --- | --- |
| `/mega/search` | 多引擎聚合，`mode=balanced\|any\|fast`，返回去重后的 `clusters` 与逐引擎排名 |
| `/mega/image` | 多引擎图片聚合 |
| `/mega/engines` | 引擎清单与熔断状态 |
| `/extract`（GET/POST） | 单页正文提取，支持 `format=json\|markdown\|text\|ndjson` |
| `/extract/batch` | 批量提取（≤20 个 URL），失败项以 `metadata.error` 返回，不拖垮整批 |
| `POST /{engine}/parse` | 请求体直接放 HTML（`text/html`），复用对应引擎解析器 |
| `/health` `/ready` `/stats` `/stats/cache` `/stats/proxy` `/stats/cb` | 健康检查与运行指标 |
| `/openapi.yaml` `/docs` | OpenAPI 规范与 Swagger UI |

## 快速开始

```bash
# 构建并启动（容器名 serp，监听 127.0.0.1:7000）
docker compose -f deploy/docker-compose.yml up -d --build

curl http://127.0.0.1:7000/health
curl "http://127.0.0.1:7000/baidu/search?text=golang&limit=5"
```

```bash
# 多引擎聚合
curl "http://127.0.0.1:7000/mega/search?text=大模型&mode=balanced&engines=baidu,sogou,360&limit=10"

# 搜索 + 内联正文（取前 2 条结果的 Markdown）
curl "http://127.0.0.1:7000/baidu/search?text=golang%20文档&limit=3&extract=2&format=json"

# 正文提取
curl "http://127.0.0.1:7000/extract?url=https://example.com&format=markdown"

# 批量提取（死链不会让整批失败）
curl -X POST "http://127.0.0.1:7000/extract/batch" \
  -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com","https://go.dev"],"mode":"fast"}'
```

也可用 CLI（无需启动服务）：

```bash
go build -o web2api . && ./web2api search baidu "golang" --format markdown
./web2api serve -a 127.0.0.1 -p 7000
```

## 请求参数

搜索类端点通用参数（`/{engine}/search`、`/mega/search`）：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `text` | — | 查询词；`text`、`site`、`file` 至少提供一个 |
| `limit` | 10 | 结果条数，1–100，只约束 organic 结果 |
| `start` | 0 | 偏移量；引擎内部换算成分页，返回的 `rank` 为跨页绝对排名 |
| `site` | — | 站内搜索，等价于查询词附加 `site:domain` |
| `file` | — | 文件类型过滤，附加 `filetype:pdf` |
| `date` | — | `YYYYMMDD..YYYYMMDD`（仅百度/Bing 实现） |
| `lang` / `region` | — | 语言/地区（Bing 通过 `setlang`/`cc` 实现；其余引擎忽略并告警） |
| `filter` | `true` | 是否过滤重复/低质结果 |
| `features` | `true` | 是否附带 SERP 特性模块（如相关搜索） |
| `extract` | `0` | `0/false` 关闭；`N` 为前 N 条结果内联正文（上限 5） |
| `extract_mode` | `auto` | `auto` / `fast` / `rendered`，出现即隐式开启提取 |
| `min_runes` | 0 | auto 模式下正文过短则升级为浏览器渲染 |
| `resolve_urls` | 依配置 | 解析引擎跳转链接：`true`/`false`/`N`（前 N 条） |
| `format` | `json` | `json` / `markdown` / `text` / `ndjson`，也可用 `Accept` 头 |

聚合端点额外参数：`engines=baidu,sogou,360`、`mode=balanced|any|fast`、`dedupe`、`merge`。

请求头：`X-Use-Proxy: direct|<tag>`、`X-Proxy-URL`、`X-Proxy-Country/Class/Provider/Session-ID`、`X-Request-ID`（原样回传）、`X-Tenant`、`X-Use-Profile`（指定浏览器指纹）。

## 响应与错误

搜索返回统一信封：`query`（含 `engines_requested`）、`meta`（`request_id`/`took_ms`/`engines_failed`/`version`）、`results`、`pagination`、`serp_features`，聚合额外返回 `clusters`。

错误体固定为 `{"error","code","request_id","message","reason"?,"meta"?}`：引擎级错误把机器码放在 `error`，参数校验错误 `error=bad_request`、具体原因放在 `reason`（大写常量）。

| HTTP | `error` | `reason`（校验类） | 含义 |
| --- | --- | --- | --- |
| 400 | `bad_request` | `INVALID_LIMIT` / `INVALID_START` / `INVALID_PARAM` / `EMPTY_QUERY` / `UNKNOWN_FORMAT` | 参数非法 |
| 403 | `blocked` | — | 上游判定为自动化流量 |
| 429 | `captcha_detected` / `blocked` | — | 触发验证码 / 上游限流 |
| 501 | `not_implemented` | — | 该引擎未实现此能力（如搜狗/360 图片搜索） |
| 502 | `parser_failure` / `engine_internal` / `all_engines_failed` | — | 解析失败 / 引擎内部错误 / 聚合全失败 |
| 503 | `circuit_open` / `proxy_auth` / `proxy_connect` / `proxy_timeout` / `proxy_unavailable` / `request_canceled` | — | 熔断中 / 代理异常 / 请求取消 |
| 504 | `search_timeout` / `request_timeout` | — | 引擎超时 / 请求超时 |

## 配置

配置优先级：**CLI 参数 > `OPENSERP_*` 环境变量 > `config.yaml` > 内置默认值**（Viper）。

仓库自带的 `config.yaml` 已按国内部署调好：四个国内可用引擎启用、境外引擎 `disabled`、限速与熔断按反爬强度设置、三个国产引擎固定桌面 Windows 指纹。完整字段说明见 [docs/CONFIGURATION.md](docs/CONFIGURATION.md)。

要点：

```yaml
baidu: { rate_requests: 12, rate_burst: 2, profile: chrome-win-uhd620 }
sogou: { rate_requests: 8,  rate_burst: 1, profile: chrome-win-uhd620 }
so360: { rate_requests: 20, rate_burst: 2, profile: chrome-win-uhd620 }
circuit_breaker: { failures: 8, recovery_seconds: 20, successes: 1 }
```

需要更高吞吐时按引擎接入代理：

```yaml
proxies:
  entries:
    - url: http://user:pass@proxy-a:8080
      tags: [baidu, sogou, so360]
```

## 反爬设计（为什么这样调）

国内引擎按“出口 IP + 浏览器指纹 + 访问频次”综合判定，因此：

1. **桌面指纹**：`<engine>.profile` 固定 `chrome-win-uhd620`。实测 Linux headless 指纹几乎必被判为机器人，换桌面 Windows 指纹后百度/360 立刻恢复。
2. **双通道**：浏览器通道被拦时自动用纯 HTTP 拉取同一 SERP，复用同一解析器（`*/search_raw.go`）；`core/captcha_gate.go` 让引擎在被拦后 10 分钟内优先走 HTTP。
3. **限速 + 熔断**：默认限速保守，连续失败 8 次熔断、20 秒后半开。

**单出口 IP 无法支撑长时间高并发**，这是上游策略而非服务缺陷；长期抓取请配代理或降低频率。

## 测试

```bash
# 端到端 + 稳定性 + 审计（9 组 35 项）
node deploy/e2e-domestic.mjs http://127.0.0.1:7000

# 低节流稳态观测（每个引擎 3 次、间隔 20s）
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000

# 单元测试（无需浏览器/网络）
go test ./...
```

验收口径：引擎级反爬（403/429/503）记为**环境事件**；只有产品级 5xx 与契约错误才判失败。能力类用例会在可用引擎间自动回退（含 Bing）。测试细节见 [docs/TESTING.md](docs/TESTING.md)，部署与运维见 [deploy/README.md](deploy/README.md)。

## 项目结构

```
baidu/ sogou/ so360/ bing/ google/ yandex/ duckduckgo/ ecosia/   引擎实现（URL、选择器、解析、浏览器/HTTP 双通道）
core/         HTTP 服务、浏览器池、代理、缓存、熔断、提取、响应构建、错误契约
extract/      正文提取（readability/trafilatura + Markdown）
cmd/          CLI（search / serve）与引擎注册表
deploy/       Docker 编排、E2E 测试脚本、交付与运维文档
docs/         架构、配置、测试文档、OpenAPI、上游 README 存档
```

## 许可证

MIT，见 [LICENSE](LICENSE)。上游项目：[karust/openserp](https://github.com/karust/openserp)。