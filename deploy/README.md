# OpenSERP 本地部署与端到端测试交付

> 交付时间：2026-11-13  ·  环境：Windows + Docker Desktop 29.7.2  ·  代码版本：`929310a`（本仓库 main）+ 本次修复

## 1. 交付物清单

| 文件 | 说明 |
| --- | --- |
| `deploy/docker-compose.yml` | 独立部署编排（构建本仓库源码、127.0.0.1:7000、healthcheck、日志轮转） |
| `deploy/docker-compose.local.yaml` | 可选覆盖层，用于复用上游 `docker-compose.yaml` 时改用本地镜像 |
| `deploy/e2e.mjs` | 端到端测试套件（31 个用例，Node 18+，零依赖） |
| `deploy/run-e2e.ps1` | PowerShell 包装器：`./deploy/run-e2e.ps1 [baseUrl]` |
| `deploy/e2e-results/*.json` | 每次运行的机器可读测试报告（含最终通过报告） |
| `baidu/search.go` | 修复：Baidu 搜索结果未遵守 `limit` 参数 |

## 2. 部署架构

```
Dockerfile（多阶段）
  ├─ builder: golang:1.24.6-bookworm   → go mod download / CGO_ENABLED=0 go build
  └─ runtime: chromedp/headless-shell  → Chrome + openserp 二进制 + config.yaml
                                         (tini 作为 PID 1，chrome 用户非 root)
```

- 镜像：`openserp:local`（395 MB，由当前仓库源码构建）
- 容器：`serp`，`init: true`，`shm_size: 2gb`（Chrome 渲染必需），`restart: unless-stopped`
- 端口：`127.0.0.1:7000 -> 7000`（仅回环，避免暴露到局域网）
- 配置：仓库 `config.yaml` 以只读方式挂载到 `/usr/src/app/config.yaml`
- 健康检查：容器内 `wget --spider http://127.0.0.1:7000/health`，30s 间隔

## 3. 部署步骤（可复现）

```powershell
# 1) 构建并用本地源码启动
docker compose -f deploy/docker-compose.yml up -d --build

# 2) 等待健康（首次启动约 10-20s，Chrome 池初始化）
docker compose -f deploy/docker-compose.yml ps
curl.exe -s http://127.0.0.1:7000/health

# 3) 端到端测试
node deploy/e2e.mjs http://127.0.0.1:7000

# 4) 查看日志 / 停止 / 升级
docker logs -f serp
docker compose -f deploy/docker-compose.yml down
docker compose -f deploy/docker-compose.yml up -d --build   # 重新构建并替换
```

快速冒烟（部署后手工验证）：

```powershell
curl.exe -s "http://127.0.0.1:7000/bing/search?text=golang&limit=3"
curl.exe -s "http://127.0.0.1:7000/mega/search?text=golang&limit=5&mode=balanced&engines=google,bing"
curl.exe -s "http://127.0.0.1:7000/extract?url=https://example.com&format=markdown"
curl.exe -s -X POST "http://127.0.0.1:7000/extract/batch" -H "Content-Type: application/json" -d '{"urls":["https://example.com"],"mode":"fast"}'
```

## 4. 端到端测试设计（`deploy/e2e.mjs`）

7 组、31 个用例，覆盖从进程健康到业务契约的完整链路：

| 分组 | 覆盖点 |
| --- | --- |
| `infra` (8) | `/health`（6 引擎状态与 degraded 语义一致）、`/ready`、`/mega/engines`、`/openapi.yaml`、`/docs`、4 个 `/stats/*`、CORS 预检、`X-Request-ID` 透传 |
| `errors` (4) | 缺 `text` → 400、未知路由 → 404、未知引擎 → 400、`limit` 越界 → 400 |
| `engines` (6+1) | 6 个引擎独立搜索 + 聚合覆盖率断言（≥3 个引擎可用，且失败必须是环境类） |
| `formats` (3) | `format=markdown/text/ndjson` 的 Content-Type 与结构 |
| `images` (1) | `/bing/image` 图片结果 |
| `mega` + `extract` (7) | balanced/any/fast 三种聚合模式、`clusters` 去重聚合、单页提取、批量提取（死链不拖垮批次）、`extract=N` 内联正文 |
| `cache` (1) | 相同请求第二次命中缓存（`X-Cache: HIT`） |

测试设计要点：

- **对抗反爬波动**：搜索引擎会对自动化请求返回 403/429/captcha/circuit_open。用例按“引擎级失败 = 环境事件、覆盖率断言 = 产品契约”分层，避免环境噪声掩盖真实回归；只有非环境类引擎故障才会让测试失败。
- **多引擎回退**：格式、内联提取、缓存等能力用例在 google/bing/duckduckgo/baidu 间回退，单个引擎被临时封禁时不误报。
- **瞬时故障重试**：提取类用例允许 1 次重试（实测出现过一次上游 `EOF`）。
- **缓存屏蔽**：探针查询按分钟轮换，避免上一轮缓存掩盖引擎真实可用性。
- **退出码**：仅“关键用例失败”返回 1，可直接接入 CI。

## 5. 测试结果（最终一轮）

命令：`node deploy/e2e.mjs http://127.0.0.1:7000`
报告：`deploy/e2e-results/e2e-2026-11-13T07-26-32-509Z.json`

```
infra      pass=8 fail=0
errors     pass=4 fail=0
engines    pass=5 fail=2
formats    pass=3 fail=0
images     pass=1 fail=0
mega       pass=3 fail=0
extract    pass=4 fail=0
cache      pass=1 fail=0
----------------------------------------------
total=31 passed=29 failed=2 critical_failures=0 environment_issues=2
duration=95.0s
退出码：0
```

关键实测指标（同一轮）：

- `/health`：6 个引擎全部 initialized；google/bing/duckduckgo/baidu `ready`
- 单引擎搜索：google 4.4s、bing 3.5s、duckduckgo 2.4s、baidu 1.6s（limit=5 生效）
- `/mega/search?mode=balanced`：7.5s，返回 clusters + 逐引擎 rank 归属
- 容器资源：CPU 0.03%、内存 158 MiB（空闲态）

单元测试（在 `golang:1.24.6-bookworm` 容器内执行 `go test ./baidu/... ./core/...`）：

```
ok  github.com/karust/openserp/baidu
ok  github.com/karust/openserp/core
ok  github.com/karust/openserp/core/browser
ok  github.com/karust/openserp/core/fpcheck/detectors
ok  github.com/karust/openserp/core/region
```

## 6. 测试中发现并修复的缺陷

**Baidu 搜索结果不遵守 `limit`**（`baidu/search.go`）

- 现象：`GET /baidu/search?text=go语言&limit=3` 返回 9 条 organic（limit=5 时同样返回 9 条），与 google/bing/yandex/duckduckgo/ecosia 的行为不一致，也让 `/mega/search` 的结果聚合超出请求上限。
- 根因：`Baidu.Search` 在返回前只做了 rank 偏移与 `StripResultFeatures`，没有调用 `core.LimitOrganicResults`；其余浏览器引擎都有该调用。
- 修复：在返回前补上 `core.LimitOrganicResults(searchResults, query.Limit)`（广告等非 organic 行保留、不计数，与其他引擎语义一致）。
- 验证：重新构建镜像并替换容器后，`limit=3 → 3 条 organic`、`limit=5 → 5 条 organic`；E2E 的 `limit` 断言由失败转为通过。
- 修复前报告（含缺陷现场）：`deploy/e2e-results/e2e-2026-11-13T07-11-24-830Z.json`

## 7. 已知环境限制（非代码缺陷）

| 引擎 | 现象 | 影响与处置 |
| --- | --- | --- |
| Yandex | `/yandex/search` 返回 429 `captcha_detected`；连续失败后熔断 503 `circuit_open` | 该出口 IP 被 Yandex 判定为机器人。配置代理后即可恢复：在 `config.yaml` 的 `proxies.entries` 中加入代理并给 `yandex` 打 tag |
| Ecosia | `/ecosia/search` 返回 403 `blocked`，随后熔断 503 | 同上，Ecosia 对数据中心/自动化流量做 IP 级封禁，需代理 |
| Google | 短时间高频自动化请求会返回 429 `rate_limited` | 属上游限流；测试已内置查询轮换与回退，冷却 1-2 分钟后恢复。生产建议加代理池或降低并发 |

其余四个引擎（google/bing/duckduckgo/baidu）在无代理环境下稳定可用，满足“≥3 引擎可用”的覆盖率契约。

## 8. 运维手册

```powershell
docker compose -f deploy/docker-compose.yml ps          # 状态 + 健康
docker logs serp --tail 100                            # 运行日志（config.yaml: save_log）
docker stats --no-stream serp                          # 资源占用
docker compose -f deploy/docker-compose.yml restart     # 重启
docker compose -f deploy/docker-compose.yml up -d --build --force-recreate   # 重建升级
docker compose -f deploy/docker-compose.yml down        # 下线
```

常见问题：

1. **端口 7000 被占用**：报错 `failed to bind host port 127.0.0.1:7000` 时，先 `docker ps -a --filter name=serp` 清理残留容器再启动；若仍失败，把 `deploy/docker-compose.yml` 的 `ports` 改成如 `127.0.0.1:7010:7000`，并用 `node deploy/e2e.mjs http://127.0.0.1:7010` 复测。
2. **不要用 `-f docker-compose.yaml -f deploy/docker-compose.local.yaml` 叠加 `ports`**：Compose 的 `ports` 是追加合并，会同时出现 `7000:7000` 与 `127.0.0.1:7000:7000` 造成绑定冲突。叠加层只改 `image/build/volumes`，端口用独立的 `deploy/docker-compose.yml`。
3. **Chrome 需要足够的 shm**：务必保留 `shm_size: 2gb`，否则页面加载会出现 OOM。
4. **首次请求偏慢**：首次调用某引擎需要启动 Chrome 与浏览器 profile，google 首查约 8-9s，之后 2-4s。

## 9. 验收结论

- 服务已以 Docker 方式部署在 `http://127.0.0.1:7000`，容器 `serp` 处于 `healthy`，6 个引擎全部初始化。
- 端到端套件 31 项：29 通过、2 项为环境类（Yandex/Ecosia 反爬），**关键用例 0 失败，退出码 0**。
- 测试过程中发现并修复了 Baidu `limit` 失效缺陷，已重新构建镜像、重新部署并回归通过。
- 单元测试（baidu、core 等包）全部通过。
