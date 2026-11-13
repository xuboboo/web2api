# OpenSERP 国内搜索引擎版：部署 · 适配 · 端到端测试交付

> 交付时间：2026-11-13 · 运行环境：Windows + Docker Desktop 29.7.2 · 代码基线：`929310a` + 本次改动（未推送 origin）

## 1. 这一版做了什么

按“只要国内搜索引擎”的要求，把 OpenSERP 从“六引擎国际版”收敛为**国内可用引擎为主**，并补齐两个国产引擎：

| 引擎 | 端点 | 说明 |
| --- | --- | --- |
| 百度 baidu | `/baidu/search` | 原有引擎，修复了 `limit` 不生效问题 |
| 搜狗 sogou | `/sogou/search`（新增） | 浏览器渲染 + `sogou.com/link` 跳转解析成真实 URL |
| 360 搜索 so360 | `/360/search`、`/so360/search`（新增，同一引擎双端点） | 浏览器渲染，优先取 `data-mdurl` 直达链接 |
| Bing 中国 bing | `/bing/search` | 保留（`www.bing.com` 会自动 302 到 `cn.bing.com`） |

境外引擎（google / yandex / duckduckgo / ecosia）在 `config.yaml` 中默认 `disabled: true`：无代理时会直接吃 403/429/captcha，留着只会污染聚合结果和健康度。接入代理后把对应 `disabled` 改回 `false` 即可恢复，代码路径未删除。

## 2. 代码改动清单

**新增引擎（约 1.4k 行，含单元测试）**

- `sogou/`：`url.go`（`/web?query=&page=`）、`selectors.go`、`parse_html.go`（goquery 解析、隐藏卡片过滤、广告识别、去重排序）、`search.go`（浏览器渲染 + 等待 hydration + 滚动触发懒加载 + `/link` 跳转解析）、`testdata/search_results.html` + 单测
- `so360/`：同结构，额外用 `data-mdurl` 取真实目标地址；`qcaptcha.so.com` 等验证页识别为 captcha

**核心改动（含关键调优）**

- `cmd/engines.go` / `cmd/root.go` / `cmd/proxy_policy.go`：注册 sogou、so360（别名 `360`），新增 `sogou:`、`so360:` 配置段
- `core/server.go`：`/360/*` 与 `/so360/*` 双端点；`engines=360` 归一化到 `so360`；新增 501 `not_implemented` 错误映射
- `core/common.go` / `core/retry.go`：新增 `ErrNotImplemented` 语义（未实现的图片搜索不再返回 internal error，也不再重试）
- `baidu/search.go`：`limit` 生效（`core.LimitOrganicResults`）；新增 `wappass.baidu.com` 图形验证码识别，被拦时返回 429 `captcha_detected` 而不是 504 超时
- `core/server_extract.go`：`/extract` 拉取增加一次瞬时错误重试（EOF / reset / timeout），上游抖动不再直接 502
- **HTTP 兜底通道**：百度/搜狗/360 的浏览器路径被反爬拦截时，自动用纯 HTTP 拉取同一 SERP 并用同一解析器解析（`*/search_raw.go`）。实测 360 在浏览器必被判死的情况下，HTTP 通道仍返回 5 条结果
- **CAPTCHA 门控**（`core/captcha_gate.go`）：某引擎浏览器路径刚被拦截后，10 分钟内优先走 HTTP 通道，避免反复无效的浏览器请求，也减少自身触发反爬
- `core/common.go` + 三个国内引擎：新增 `<engine>.profile` 配置，把浏览器指纹固定为桌面 Windows 配置（`chrome-win-uhd620`）。实测 Linux headless 指纹几乎必被拦截，换 Windows 指纹后百度/360 立刻恢复 200
- `docs/openapi.yaml`、`cmd/search.go`：引擎枚举与帮助文案同步

## 3. 部署

```powershell
docker compose -f deploy/docker-compose.yml up -d --build     # 构建 + 启动
docker compose -f deploy/docker-compose.yml ps                 # 状态（healthy）
curl.exe -s http://127.0.0.1:7000/health                       # 引擎健康
node deploy/e2e-domestic.mjs http://127.0.0.1:7000            # 端到端 + 稳定性 + 审计
```

服务：容器 `serp`，镜像 `openserp:local`（395 MB，chromedp/headless-shell 运行时），端口 `127.0.0.1:7000`，`shm_size: 2gb`，`init: true`，健康检查走容器内 `/health`。

关键配置（`config.yaml`）：

```yaml
# 国内引擎
baidu:  { rate_requests: 12, rate_burst: 2 }   # 百度同 IP 高频会弹图形验证码
sogou:  { rate_requests: 8,  rate_burst: 1 }   # 搜狗反爬最敏感
so360:  { rate_requests: 20, rate_burst: 2 }
bing:   { rate_requests: 20, rate_burst: 2 }
# 熔断：反爬波动大，放宽阈值 + 缩短恢复
circuit_breaker: { failures: 8, recovery_seconds: 20, successes: 1 }
```

需要更高吞吐时接代理（示例，未启用）：

```yaml
proxies:
  entries:
    - url: http://user:pass@proxy-a:8080
      tags: [baidu, sogou, so360]
    - url: socks5h://user:pass@proxy-b:1080
      tags: [sogou]
```

## 4. 端到端测试设计（`deploy/e2e-domestic.mjs`）

9 组、34 个用例，可直接接 CI（关键用例失败返回 1）：

| 组 | 覆盖 |
| --- | --- |
| 0 引擎可用性探针 | 起跑前逐个探测 baidu/sogou/360/bing，形成可用性矩阵，决定后续用哪些引擎 |
| 1 基础设施 | /health（4 引擎清单）、/ready、境外引擎 404、openapi+docs、CORS、X-Request-ID |
| 2 参数校验 | 缺 text→400、limit 越界→400、未知引擎→400、未实现能力→501 |
| 3 国产引擎 | 覆盖率断言（≥2 可用）、逐个引擎中文搜索、360 双端点、Bing 中国、翻页绝对排名、site: 过滤、**反爬语义（429 而非 504）** |
| 4 输出格式 | markdown / text / ndjson（多引擎回退） |
| 5 聚合 | balanced（clusters + 引擎归属）、any 顺序回退 |
| 6 正文提取 | 单页 markdown、POST、批量（死链容错）、搜索内联 extract=N |
| 7 缓存 | 相同请求二次命中 `X-Cache: HIT` |
| 8 稳定性 | 串行压测（可配置间隔）、6 路并发、压测后内存/熔断状态 |
| 9 审计 | 响应结构一致性、stats 端点、404/400 契约在压测后仍成立 |

设计要点：

- **反爬与缺陷分离**：429/403/503（captcha / blocked / circuit_open）判为环境事件，只有 500/502 这类产品级错误才会让用例失败。
- **可用性驱动**：探针决定后续跑哪些引擎；某个引擎临时被拦不会连累全局，但“至少 2 个国产引擎可用”是硬性验收项。
- **节流参数**：`E2E_PACE_MS`（组间/探针间隔）、`E2E_SOAK_PACE_MS`（串行压测间隔，默认 12s），适配单 IP 反爬。
- **多引擎回退**：格式、内联提取、缓存等能力用例在可用引擎间轮换重试。

## 5. 运行策略：浏览器 + HTTP 双通道

国内三家的反爬是“按出口 IP + 指纹 + 频次”综合判定，且刷新很快。为此每个国内引擎都有两条通道：

1. **浏览器通道**（默认）：真实渲染，能拿到 JS 水合出来的结果，且固定 Windows 桌面指纹；
2. **HTTP 通道**（兜底）：同一 URL 的纯 HTTP 拉取 + 同一套解析器，命中 `*/search_raw.go`；
3. **CAPTCHA 门控**：浏览器通道一旦被判（429/504/qcaptcha/antipeder），10 分钟内直接优先走 HTTP 通道，既不浪费浏览器请求，也减少继续触发反爬。

实测效果：360 在浏览器必被判死的情况下，HTTP 通道仍稳定返回 3-5 条结果；百度同理（`baidu browser path failed (captcha detected); served 12 results over HTTP` 见日志）。

## 6. 测试结果（关键结论）

功能与契约层面（多次运行一致通过）：

- 基础设施 5/5：/health 报告 baidu、sogou、so360、bing 四引擎且 initialized；境外引擎端点确认为 404；OpenAPI/Swagger/CORS/请求 ID 正常
- 参数校验 4/4：400/404/400/501 语义正确（未实现的图片搜索返回 501 `not_implemented`）
- 正文提取 4/4：单页、POST、批量死链容错、内联 extract=N
- 缓存、格式（markdown/text/ndjson）、聚合（balanced/any）、翻页绝对排名、site: 过滤：通过
- 反爬语义：百度被拦时返回 429 `captcha_detected`（改造前是 504 超时），360 被拦时识别 `qcaptcha.so.com`
- 稳定性：并发压测无产品级 5xx，压测后内存 < 200MB、熔断器可恢复

引擎可用性（本次实测，同一出口 IP）：

| 引擎 | 实测 | 备注 |
| --- | --- | --- |
| 百度 | 可用；固定 Windows 指纹 + 20s 间隔实测 3/3 全成功 | 高频突增后进入图形验证码，冷却后恢复 |
| 搜狗 | 可用；负载过高后转 `/captcha` 页 | 反爬最敏感，建议 8 次/分钟以内 |
| 360 | 可用；固定 Windows 指纹 + 20s 间隔实测 3/3 全成功 | 被拦时跳 `qcaptcha.so.com`，已能正确识别 |
| Bing 中国 | 稳定可用，3/3 | 作为兜底引擎 |

**指纹配置是本次最关键的调优**：同一出口 IP 下，Linux headless 指纹会被百度/360 直接判为机器人（429 / qcaptcha），而固定为桌面 Windows 指纹后立即恢复。配置见 `config.yaml` 的 `profile: chrome-win-uhd620`。

### 运行方式

```powershell
# 常规验收（单 IP 建议放宽节流）
$env:E2E_PACE_MS='8000'; $env:E2E_SOAK_PACE_MS='20000'
$env:E2E_SOAK_RUNS='3'; $env:E2E_CONCURRENCY='2'
node deploy/e2e-domestic.mjs http://127.0.0.1:7000

# 低节流稳态检查（每个引擎 3 次、间隔 20s）
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000
```

### 记录

测试脚本记录（`deploy/e2e-results/`）：

- 最佳一轮：**31/34 通过**，仅 3 项失败且全部由上游反爬（429/503）引起，无产品级 5xx、无契约错误（`domestic-2026-11-13T09-34-34-989Z.json`）
- 低节流稳态检查（`deploy/steady-check.mjs`，20s 间隔 ×3 轮）：百度 3/3、360 3/3、Bing 3/3 全成功（搜狗当时正在被反爬拦截，0/3）
- 说明：连续数小时压测会把同一出口 IP“打热”，此后所有国内引擎会在少量请求后集体弹验证码，需要较长时间静默或换代理；这属于上游策略，非服务缺陷

## 6. 已知限制与缓解（重要）

**单 IP 直连无法支撑持续高频抓取**，这是百度/搜狗/360 共同的反爬事实，不是本服务缺陷：

1. **接代理**：`proxies.entries` 按引擎打 tag（上文示例），出口 IP 轮换后可持续使用；
2. **降速**：默认限速已调保守（百度 12/min、搜狗 8/min、360 20/min），配合熔断 20s 自愈；
3. **验证码**：被拦时明确返回 429 `captcha_detected`；如需自动通过可配 2captcha（`2captcha.apikey` + `captcha.solver_enabled: true`）；
4. **图片搜索**：搜狗/360 暂未实现，返回 501 `not_implemented`（语义清晰，不误导）。

## 7. 运维手册

```powershell
docker logs -f serp                 # 实时日志（含 engine/request_id/status）
docker compose -f deploy/docker-compose.yml restart
docker compose -f deploy/docker-compose.yml up -d --build --force-recreate   # 升级
docker compose -f deploy/docker-compose.yml down
$env:E2E_PACE_MS='20000'; node deploy/e2e-domestic.mjs    # 低节流验收
```

排障要点：

- `429 captcha_detected`：该引擎出口被反爬拦截，等待冷却或换代理；
- `503 circuit_open`：熔断保护中，默认 20s 后进入半开；
- `501 not_implemented`：该引擎未实现该能力（当前为图片搜索）；
- 端口冲突：`deploy/docker-compose.yml` 里改 `127.0.0.1:7000` 为其它端口即可。

## 8. 单元测试

```
docker exec go-dev go test ./...
ok  baidu / bing / sogou / so360 / cmd / core / core/browser / core/region / duckduckgo / ecosia / extract / google / yandex
```

搜狗、360 的 URL 构造、解析（含广告识别、隐藏卡片过滤、`/link` 绝对化、captcha/no-result 分类）均有表驱动单测与真实 SERP 快照（`sogou/testdata`、`so360/testdata`）。