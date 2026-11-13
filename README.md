# web2api

面向国内搜索引擎的 **Web → API** 服务：把百度、搜狗、360 搜索的结果页变成结构化 JSON，自带正文提取、多引擎聚合、缓存、限流与反爬兜底，一条 `docker compose up` 就能跑起来。

> 本项目基于开源项目 [OpenSERP](https://github.com/karust/openserp)（MIT）改造，保留其 API 契约与引擎框架，重点做了**国内引擎适配**：新增搜狗、360 搜索，修复百度 `limit`，补齐国内反爬场景下的稳定性机制。

## 支持的引擎

| 引擎 | 端点 | 说明 |
| --- | --- | --- |
| 百度 | `/baidu/search` | 浏览器渲染 + 纯 HTTP 兜底 |
| 搜狗 | `/sogou/search` | 浏览器渲染，`sogou.com/link` 跳转自动解析为真实地址 |
| 360 搜索 | `/360/search`、`/so360/search` | 双端点同一引擎，优先取 `data-mdurl` 直达链接 |
| Bing 中国 | `/bing/search` | 自动走 `cn.bing.com`，作为兜底 |

多引擎聚合：`/mega/search`（`balanced` / `any` / `fast` 三种模式，返回去重后的 `clusters` 与逐引擎排名）。
正文提取：`/extract`、`/extract/batch`，搜索时也可用 `extract=N` 直接内联网页 Markdown。

> 境外引擎（google / yandex / duckduckgo / ecosia）代码保留但默认 `disabled: true`——无代理时会被 captcha/403/429 拦截。接入代理后把 `config.yaml` 里对应 `disabled` 改回 `false` 即可。

## 快速开始

```bash
docker compose -f deploy/docker-compose.yml up -d --build
curl "http://127.0.0.1:7000/health"
curl "http://127.0.0.1:7000/baidu/search?text=golang&limit=5"
```

服务默认监听 `127.0.0.1:7000`，容器内使用 chromedp/headless-shell 运行真实浏览器。

常用查询参数：`text`、`limit`、`start`、`site`、`filetype`、`lang`、`region`、`format=json|markdown|text|ndjson`、`extract=N`。

```bash
# 多引擎聚合
curl "http://127.0.0.1:7000/mega/search?text=大模型&mode=balanced&engines=baidu,sogou,360&limit=10"

# 正文提取（Markdown）
curl "http://127.0.0.1:7000/extract?url=https://example.com&format=markdown"

# 批量提取（死链不会拖垮整批）
curl -X POST "http://127.0.0.1:7000/extract/batch" \
  -H "Content-Type: application/json" \
  -d '{"urls":["https://example.com","https://go.dev"],"mode":"fast"}'
```

## 国内反爬怎么处理

国内搜索引擎按“出口 IP + 浏览器指纹 + 频次”综合判定，因此项目做了三层设计：

1. **桌面指纹**：`<engine>.profile: chrome-win-uhd620`。实测 Linux headless 指纹几乎必被拦截，固定桌面 Windows 指纹后百度/360 明显更稳。
2. **双通道**：浏览器通道被拦时，自动改用纯 HTTP 拉取同一 SERP、复用同一解析器，并用 10 分钟门控（`core/captcha_gate.go`）优先走 HTTP，避免反复撞浏览器。
3. **保守限速 + 熔断**：百度 12 次/分、搜狗 8 次/分、360 20 次/分；连续失败 8 次熔断，20 秒后半开恢复。

单出口 IP 无法支撑长时间高并发抓取，这是上游策略而非本服务缺陷。需要稳定吞吐请配置代理（按引擎打 tag）：

```yaml
proxies:
  entries:
    - url: http://user:pass@proxy-a:8080
      tags: [baidu, sogou, so360]
```

## 测试

```bash
# 端到端 + 稳定性 + 审计（9 组 35 项）
node deploy/e2e-domestic.mjs http://127.0.0.1:7000

# 低节流稳态观测（每个引擎 3 次，间隔 20s）
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000

# 单元测试
go test ./...
```

测试设计要点：引擎级反爬记为**环境事件**，产品级 5xx/契约错误才算失败；能力类用例自动在可用引擎间回退（含 Bing 兜底），因此上游封禁不会掩盖真实回归。

详细交付说明（部署、引擎实现、验收口径、运维手册）见 **[deploy/README.md](deploy/README.md)**。

## 项目结构

```
baidu/ sogou/ so360/ bing/ google/ yandex/ duckduckgo/ ecosia/   各引擎实现（URL 构造、解析、搜索）
core/        服务器、浏览器池、代理、缓存、熔断、提取、错误契约
extract/     正文提取（trafilatura/readability + Markdown）
cmd/         CLI 与 serve 命令、引擎注册
deploy/      Docker 编排、E2E 测试、交付文档
docs/        OpenAPI 规范、架构文档、上游 README
```

## 许可证

MIT，见 [LICENSE](LICENSE)。上游项目：[karust/openserp](https://github.com/karust/openserp)。
