# 部署与运维（deploy/）

本目录提供 web2api 的独立部署编排、端到端测试脚本与运维说明。应用本身的配置项见 [../docs/CONFIGURATION.md](../docs/CONFIGURATION.md)，测试设计见 [../docs/TESTING.md](../docs/TESTING.md)。

## 1. 目录内容

| 文件 | 用途 |
| --- | --- |
| `docker-compose.yml` | 独立部署编排：从仓库根构建镜像、映射 `127.0.0.1:7000`、挂载 `config.yaml`、内置健康检查与日志轮转 |
| `docker-compose.local.yaml` | 覆盖层：复用上游 `docker-compose.yaml` 时改用本地镜像（一般不单独使用） |
| `e2e-domestic.mjs` | 端到端 + 稳定性 + 审计套件（35 项） |
| `e2e.mjs` | 旧版“六引擎国际版”套件（启用境外引擎时使用） |
| `steady-check.mjs` | 低节流稳态观测脚本 |
| `run-e2e.ps1` | PowerShell 包装器 |
| `e2e-results/` | 每次运行的 JSON 报告 |

## 2. 部署

```powershell
# 构建并启动（首次约 2 分钟，主要耗时在 Go 编译与 Chrome 镜像层）
docker compose -f deploy/docker-compose.yml up -d --build

# 状态与健康
docker compose -f deploy/docker-compose.yml ps
curl.exe -s http://127.0.0.1:7000/health
curl.exe -s http://127.0.0.1:7000/mega/engines
```

部署形态：

- 容器名 `serp`，镜像 `openserp:local`（构建自仓库 `Dockerfile`，运行时为 `chromedp/headless-shell`）
- 端口 `127.0.0.1:7000`（仅回环；需要外部访问时改 compose 里的映射或在前面加反向代理）
- `shm_size: 2gb`（Chrome 渲染必需）
- `init: true`（tini 回收 Chrome 子进程）
- 配置以只读方式挂载：`../config.yaml:/usr/src/app/config.yaml:ro`
- 健康检查：容器内 `wget --spider http://127.0.0.1:7000/health`，30s 间隔
- 日志：json-file，单文件 20MB × 3

> 修改 `config.yaml` 后需要 `--force-recreate` 才生效（挂载的是文件而非目录）。

## 3. 本仓库的配置取值

完整字段说明见 [../docs/CONFIGURATION.md](../docs/CONFIGURATION.md)。与部署相关的关键值：

| 配置 | 取值 | 原因 |
| --- | --- | --- |
| `server.host/port` | `0.0.0.0:7000` | 容器内监听，宿主机映射回环 |
| `server.insecure` | `true` | 容器缺企业根证书，浏览器/HTTP 通道需放宽 TLS |
| `app.block_resources` | `image,font,css,media` | 降低渲染开销 |
| `app.max_processes` | `6` | 并发 Chrome 上限 |
| `cache.ttl_seconds` | `120` | 专用端点 JSON 缓存 |
| `resilience.max_retries` | `1` | 反爬场景下重试收益低，避免放大封禁 |
| `circuit_breaker` | `8 / 20s / 1` | 放宽阈值、缩短恢复，适配国内引擎波动 |
| `baidu/sogou/so360.profile` | `chrome-win-uhd620` | Linux headless 指纹几乎必被拦截 |
| `google/yandex/duckduckgo/ecosia.disabled` | `true` | 无代理时不可用 |

> 代理示例（未启用）：

```yaml
proxies:
  entries:
    - url: http://user:pass@proxy-a:8080
      tags: [baidu, sogou, so360]
    - url: socks5h://user:pass@proxy-b:1080
      tags: [sogou]
```

## 4. 国内引擎适配要点

| 引擎 | 实现 | 关键细节 |
| --- | --- | --- |
| 百度 | 浏览器 + HTTP 双通道 | `limit` 生效；识别 `wappass.baidu.com` 验证码页，返回 429 而不是 504；卡片 `mu=` 属性优先作为真实 URL |
| 搜狗 | 浏览器 + HTTP 双通道 | `sogou.com/link?url=` 返回的是 **200 + JS 跳转**（不是 302），解析器从 `window.location.replace(...)` / meta refresh 中还原目标地址 |
| 360 | 浏览器 + HTTP 双通道 | 双端点 `/360` 与 `/so360`；标题链接优先取 `data-mdurl`；识别 `qcaptcha.so.com` 验证页 |
| Bing 中国 | 浏览器 | 首页搜索框提交（`bing.search_box`），`www.bing.com` 自动跳 `cn.bing.com` |

**双通道 + 门控**：浏览器通道被拦截后，引擎自动用纯 HTTP 拉取同一 SERP 并用同一解析器解析（`*/search_raw.go`）；`core/captcha_gate.go` 记录拦截状态，10 分钟内优先走 HTTP，减少无效浏览器请求。

**为什么固定指纹**：同一出口 IP 下，Linux headless 指纹会被百度/360 直接判死（429 / qcaptcha）；固定为桌面 Windows 指纹后可正常返回。配置项 `<engine>.profile`。

## 5. 测试

```powershell
# 常规验收（单 IP 建议放宽节流）
$env:E2E_PACE_MS='8000'; $env:E2E_SOAK_PACE_MS='20000'
$env:E2E_SOAK_RUNS='3'; $env:E2E_CONCURRENCY='2'
node deploy/e2e-domestic.mjs http://127.0.0.1:7000

# 稳态观测
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000

# 单元测试
go test ./...
```

判定语义、参数与报告字段见 [../docs/TESTING.md](../docs/TESTING.md)。最近一次验收（2026-11-13）：

```
infra 5/5 · errors 4/4 · engines 8/10 · formats 3/3 · mega 2/2
extract 4/4 · cache 1/1 · stability 3/3 · audit 3/3
total=35 passed=33 failed=2 critical_failures=0 environment_issues=2
report=e2e-results/domestic-2026-11-13T11-50-14-757Z.json
```

两项失败均为百度/搜狗上游 429（`captcha_detected`），按设计计为环境事件；此时能力类用例自动切到 360/Bing 通道并通过。

## 6. 已知限制

1. **单出口 IP 的吞吐上限**：百度/搜狗/360 对同 IP 高频访问会弹验证码。实测连续压测后需要约 20–40 分钟静默才恢复；持续抓取请配代理或降低频率。
2. **图片搜索**：搜狗/360 未实现，`/sogou/image`、`/360/image` 返回 501 `not_implemented`；百度/Bing 支持。
3. **参数支持差异**：`date` 仅百度/Bing；`lang/region` 仅 Bing 真正生效（其余引擎会告警忽略）；`site:` 在百度/Bing 生效，搜狗会忽略（已实测）。
4. **SERP 结构变化**：国内引擎改版频繁，解析器选择器集中在各引擎 `selectors.go`，改版时优先修这里并补 testdata 快照。

## 7. 运维手册

```powershell
docker logs -f serp                       # 实时日志（含 engine / request_id / status）
docker logs serp --tail 200 | Select-String 'error'
docker stats --no-stream serp             # 资源占用
docker compose -f deploy/docker-compose.yml restart
docker compose -f deploy/docker-compose.yml up -d --build --force-recreate   # 升级
docker compose -f deploy/docker-compose.yml down
```

运行指标：`/stats`（缓存、熔断、代理、限流计数）、`/stats/cache`、`/stats/cb`、`/stats/proxy`。

排障：

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| `429 captcha_detected` | 引擎出口被反爬拦截 | 等冷却或换代理 |
| `503 circuit_open` | 熔断中（默认 20s 后半开） | 等待；持续出现说明出口持续被封 |
| `502 parser_failure` | SERP 结构变化导致解析失败 | 检查对应引擎 `selectors.go`，用 `/{engine}/parse` 复现 |
| `501 not_implemented` | 能力未实现（图片搜索） | 用支持该能力的引擎 |
| 端口冲突 | 宿主机 7000 被占用 | 改 compose 的端口映射，或清理残留容器 |
| 容器不 healthy | 浏览器启动失败/配置错误 | `docker logs serp` 查看启动日志 |

## 8. 变更记录

- `1c24945` 文档更新（根 README、上游 README 移入 `docs/`）
- `1c2c012` E2E 硬性/信息项口径、Bing 能力回退
- `83ab8cc` E2E 环境分类、轻量验收参数
- `43e5b3c` 国内引擎 HTTP 兜底通道 + CAPTCHA 门控
- `e9f5e9c` 国内引擎固定桌面指纹；稳态检查脚本
- `abd6b78` 新增搜狗/360 引擎、国内优先配置、E2E 套件
- `d0ef4e8` Docker 部署与 E2E 初版；百度 `limit` 修复

## 9. 上游与许可

基于 [OpenSERP](https://github.com/karust/openserp)（MIT）改造；上游 README 存档于 [../docs/UPSTREAM-README.md](../docs/UPSTREAM-README.md)。
