# 测试指南

仓库里有三类测试：**单元测试**（Go）、**端到端 + 稳定性 + 审计套件**（Node，无需依赖）、**低节流稳态观测脚本**（Node）。

## 1. 单元测试

```bash
go test ./...
```

不需要浏览器或网络。覆盖：

- 各引擎 `BuildURL`（含中文编码、site/filetype 折叠、翻页换算、非法入参）
- 各引擎 `ParseHTML`（真实 SERP 快照，位于 `*/testdata/`；含广告识别、隐藏卡片过滤、去重与排名）
- 响应信封、缓存键、熔断、重试、提取、代理策略、`core.CaptchaGate` 等核心模块

搜狗、360 的解析器断言包括：`/link` 相对地址绝对化、`data-mdurl` 直达链接优先、captcha / no-result 分类。

## 2. 端到端套件：`deploy/e2e-domestic.mjs`

35 个检查项，9 个分组，零依赖（Node 18+ 内置 fetch）。默认目标 `http://127.0.0.1:7000`。

```bash
node deploy/e2e-domestic.mjs http://127.0.0.1:7000
# 或
OPENSERP_BASE_URL=http://127.0.0.1:7000 node deploy/e2e-domestic.mjs
```

| 分组 | 数量 | 覆盖内容 |
| --- | --- | --- |
| 引擎可用性探针（起跑前） | — | 逐个探测 baidu/sogou/360/bing，形成可用性矩阵 |
| `infra` | 5 | `/health` 引擎清单、`/ready`、境外引擎 404、`/openapi.yaml`+`/docs`、CORS 与 `X-Request-ID` |
| `errors` | 4 | 缺 `text`→400、`limit` 越界→400、未知引擎→400、未实现能力→501 |
| `engines` | 10 | 至少 1 个引擎可用（硬性）、≥2 可用且≥1 国产（信息项）、三个国产引擎中文搜索、360 双端点、Bing 中国、翻页绝对排名、`site:` 过滤、反爬语义（429 而非 504） |
| `formats` | 3 | `format=markdown/text/ndjson` 的 Content-Type 与结构 |
| `mega` | 2 | `balanced`（clusters + 引擎归属）、`any` 顺序回退 |
| `extract` | 4 | 单页 markdown、POST JSON、批量死链容错、搜索内联 `extract=N` |
| `cache` | 1 | 相同请求第二次命中 `X-Cache: HIT` |
| `stability` | 3 | 串行压测、并发压测、压测后内存与熔断状态 |
| `audit` | 3 | 响应结构一致性、`/stats*` 可用、压测后 404/400 契约仍成立 |

### 可调参数（环境变量）

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `OPENSERP_BASE_URL` / 第 1 个参数 | `http://127.0.0.1:7000` | 目标地址 |
| `E2E_PACE_MS` | `0` | 组间与探针间隔（毫秒） |
| `E2E_SOAK_PACE_MS` | `12000` | 串行压测每次请求间隔 |
| `E2E_SOAK_RUNS` | `9` | 串行压测次数 |
| `E2E_CONCURRENCY` | `6` | 并发压测并发数 |

单出口 IP 建议：`E2E_PACE_MS=8000~15000`、`E2E_SOAK_PACE_MS=20000~30000`、`E2E_SOAK_RUNS=3`、`E2E_CONCURRENCY=2`。

### 判定语义（重要）

- **环境事件**：429 `captcha_detected`、403 `blocked`、503 `circuit_open` 等上游反爬/熔断，不计入关键失败；报告里标记 `classification: "environment"`。
- **产品缺陷**：500/502（`parser_failure`/`engine_internal`）、契约错误（状态码或字段不符合预期）、稳定性压测中出现产品级 5xx。
- **硬性 vs 信息项**：至少 1 个引擎可用 + 全部契约/能力检查是硬性；“≥2 个引擎可用且≥1 个国产”是信息项，上游整窗封禁时只记录不失败。
- **能力用例回退**：格式、缓存、聚合、内联提取等与具体引擎无关的用例，会在可用引擎间轮换，最后回退到 Bing。
- **退出码**：仅当存在关键失败时返回 1，可直接接入 CI。

报告写入 `deploy/e2e-results/domestic-<时间戳>.json`，包含每个用例的分组、耗时、错误、分类，以及引擎可用性矩阵。

## 3. 低节流稳态观测：`deploy/steady-check.mjs`

用于观察“单 IP + 合理节流”下的持续可用性，不建议作为 CI 门禁。

```bash
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000   # 每个引擎 3 次，间隔 20s
STEADY_ENGINES=baidu,sogou node deploy/steady-check.mjs ...   # 自定义引擎集合
```

输出逐次请求的状态与耗时，最后给出每引擎成功率；失败率 > 20% 时退出码为 1。

## 4. 旧版全引擎套件：`deploy/e2e.mjs`

这是改造前的“六引擎国际版”套件（Google/Yandex/Baidu/Bing/DuckDuckGo/Ecosia），保留用于启用境外引擎（配置代理）后的回归。当前 `config.yaml` 默认关闭境外引擎，直接运行会大量失败，属预期。

```bash
node deploy/e2e.mjs http://127.0.0.1:7000
```

## 5. 运行建议

```powershell
# 常规验收（单机、单出口 IP）
$env:E2E_PACE_MS='8000'; $env:E2E_SOAK_PACE_MS='20000'
$env:E2E_SOAK_RUNS='3'; $env:E2E_CONCURRENCY='2'
node deploy/e2e-domestic.mjs http://127.0.0.1:7000

# 观察引擎冷却是否恢复
node deploy/steady-check.mjs http://127.0.0.1:7000 3 30000
```

排障对照：

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| `429 captcha_detected` | 该引擎出口被判为机器人 | 等冷却（实测 20–40 分钟量级）或换代理 |
| `503 circuit_open` | 熔断保护中 | 等待 `circuit_breaker.recovery_seconds` 后自动半开 |
| `501 not_implemented` | 能力未实现（图片搜索） | 改用 baidu/bing 的图片端点 |
| 全部引擎 `429` | 出口 IP 被多引擎同时封禁 | 配代理或长时间静默 |