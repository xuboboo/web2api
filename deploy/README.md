# 部署与运维（deploy/）

本目录提供 Web2API 收录查询平台的独立部署编排、端到端测试脚本与运维说明。应用配置项见 [../docs/CONFIGURATION.md](../docs/CONFIGURATION.md)，测试设计见 [../docs/TESTING.md](../docs/TESTING.md)。

> **子路径**:整个应用(官网、管理控制台、`/v1/*` 公网 API、`/api/*` 管理 API、`/health /ready /stats`、`/{engine}/search`)固定部署在 **`/web-tool`** 前缀之下(代码内常量,无配置项)。所有探针、反向代理统一使用 `/web-tool/health` 等带前缀路径。

## 1. 目录内容

| 文件 | 用途 |
| --- | --- |
| `docker-compose.yml` | 独立部署编排：从仓库根构建镜像、映射 `127.0.0.1:7000`、挂载 `config.yaml`、内置健康检查与日志轮转 |
| `docker-compose.local.yaml` | 覆盖层：复用根目录 `docker-compose.yaml` 时改用本地镜像（一般不单独使用） |
| `e2e.mjs` | 平台 E2E 冒烟套件(前缀/官网与控制台静态挂载/管理 API/公网 API 契约、MCP 握手链、冷却 force→503→reset、openapi.yaml/llms.txt、登录锁定;零依赖 Node 18+) |
| `steady-check.mjs` | 低节流稳态观测脚本(对搜索引擎的持续可用性,冷却 503 单独标记) |
| `run-e2e.ps1` | PowerShell 包装器 |
| `e2e-results/` | 每次运行的 JSON 报告 |

## 2. 部署

```powershell
# 构建并启动（首次约 2 分钟，主要耗时在 Go 编译与 Chrome 镜像层）
# ADMIN_PASSWORD 是必需的：容器必须绑 0.0.0.0 才能被端口映射访问，而服务在
# "非 loopback + 默认管理员密码"时会拒绝启动（见 docs/audit/FINDINGS.md F-26）。
# 缺这个变量时 docker compose 会在解析阶段直接报错，不会进重启循环。
$env:ADMIN_PASSWORD = '<强密码>'
docker compose -f deploy/docker-compose.yml up -d --build

# 状态与健康
docker compose -f deploy/docker-compose.yml ps
curl.exe -s http://127.0.0.1:7000/web-tool/health
```

> 之后每条 `docker compose -f deploy/docker-compose.yml ...` 命令都需要该变量（compose 每次都会插值整个文件）。不想每次设置就在 `deploy/` 下放一个 `.env`（已被 `.gitignore` 与 `.dockerignore` 排除）：
>
> ```powershell
> 'ADMIN_PASSWORD=<强密码>' | Out-File -Encoding ascii deploy\.env
> ```

部署形态：

- 容器名 `serp`，镜像 `web2api:local`（构建自仓库 `Dockerfile`，运行时为 `chromedp/headless-shell`）
- 官网入口 **`http://127.0.0.1:7000/web-tool/`**；管理控制台 **`http://127.0.0.1:7000/web-tool/console/`**（根路径 `/` 无任何路由）
- 端口 `127.0.0.1:7000`（仅回环；需要外部访问时改 compose 里的映射或在前面加反向代理,见下文 nginx 示例）
- `shm_size: 512mb`（Chrome 渲染所需共享内存；配合已启用的 `--disable-dev-shm-usage`，1.4G 小内存机器可安全使用。内存充足的机器可调回 `2gb`）、`init: true`（tini 回收 Chrome 子进程）
- 配置以只读方式挂载：`../config.yaml:/usr/src/app/config.yaml:ro`
- 健康检查：容器内 `wget --spider http://127.0.0.1:7000/web-tool/health`，30s 间隔
- 日志：json-file，单文件 20MB × 3
- 平台数据（API key、用量、JWT 密钥）写在容器 `/usr/src/app/data`;已挂载命名卷 `web2api-data` 做持久化,重建容器数据不丢失。首次升级从旧版（无卷）迁移时,旧容器内的数据不会自动进入新卷,请在升级前用 `docker cp serp:/usr/src/app/data ./data` 备份,升级后再写回卷

> 修改 `config.yaml` 后需要 `--force-recreate` 才生效（挂载的是文件而非目录）。

### nginx 反向代理(不去前缀)

```nginx
location /web-tool/ {
    proxy_pass http://127.0.0.1:7000;   # 不重写路径,原样透传 /web-tool 前缀
    proxy_set_header Host $host;
    proxy_read_timeout 120s;
}
```

## 3. 本仓库的配置取值

完整字段说明见 [../docs/CONFIGURATION.md](../docs/CONFIGURATION.md)。与部署相关的关键值：

| 配置 | 取值 | 原因 |
| --- | --- | --- |
| `server.host/port` | `0.0.0.0:7000` | 容器内监听，宿主机映射回环 |
| `server.insecure` | `true` | 容器缺企业根证书，浏览器/HTTP 通道需放宽 TLS |
| `app.block_resources` | `image,font,css,media` | 降低渲染开销 |
| `app.max_processes` | `1` | 并发 Chrome 上限（1.4G 低内存优化，原默认 6） |
| `app.idle_ttl` | `45s` | 空闲 Chrome 回收时间（低内存优化，原默认 5m） |
| `app.max_concurrent_requests` | `2` | 在途请求并发上限，0=不限；超出排队以压峰值内存 |
| `cache.ttl_seconds` | `120` | 专用端点 JSON 缓存 |
| `resilience.max_retries` | `1` | 反爬场景下重试收益低，避免放大封禁 |
| `circuit_breaker` | `8 / 20s / 1` | 放宽阈值、缩短恢复，适配国内引擎波动 |
| `baidu/sogou/so360.profile` | `chrome-win-uhd620` | Linux headless 指纹几乎必被拦截 |
| `platform.*` | `admin/admin123` | **部署后第一件事:改掉默认管理员密码** |

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
| 360 | 浏览器 + HTTP 双通道 | 双端点 `/web-tool/360/search` 与 `/web-tool/so360/search`；标题链接优先取 `data-mdurl`；识别 `qcaptcha.so.com` 验证页 |
| Bing 中国 | 浏览器 | 首页搜索框提交（`bing.search_box`），`www.bing.com` 自动跳 `cn.bing.com` |

**双通道 + 门控**：浏览器通道被拦截后，引擎自动用纯 HTTP 拉取同一 SERP 并用同一解析器解析（`*/search_raw.go`）；`core/captcha_gate.go` 记录拦截状态，10 分钟内优先走 HTTP，减少无效浏览器请求。

**为什么固定指纹**：同一出口 IP 下，Linux headless 指纹会被百度/360 直接判死（429 / qcaptcha）；固定为桌面 Windows 指纹后可正常返回。配置项 `<engine>.profile`。

## 5. 测试

```powershell
# 平台契约冒烟(前缀/官网与控制台/管理/公网 API + MCP 握手链 + 冷却 force→503→reset + 登录锁定;
# E2E_LIVE_INDEX_CHECK=1 追加真实收录判定;E2E_SKIP_LOCKOUT=1 跳过锁定测试——
# 该测试在套件末尾会锁定 admin@本机IP 默认 15 分钟)
node deploy/e2e.mjs http://127.0.0.1:7000

# 搜索引擎稳态观测(单 IP 建议放长间隔;引擎冷却 503 会被单独标记为 cooldown)
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000

# 单元测试
go test ./...
```

判定语义、参数与报告字段见 [../docs/TESTING.md](../docs/TESTING.md)。

## 6. 已知限制

1. **单出口 IP 的吞吐上限**：百度/搜狗/360 对同 IP 高频访问会弹验证码。实测连续压测后需要约 20–40 分钟静默才恢复；持续抓取请配代理或降低频率。
2. **收录判定准确率**:四引擎均以 URL 为查询词检索前 10 条; bing 采用多查询形式回退 + 命中早停(带引号完整 URL 优先);已收录但排名靠后(未进前 10)时会漏报(详见根 README)。
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

运行指标(均带前缀):`/web-tool/stats`(缓存、熔断、代理、限流计数)、`/web-tool/stats/cache`、`/web-tool/stats/cb`、`/web-tool/stats/proxy`。

排障：

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| `429 captcha_detected` | 引擎出口被反爬拦截 | 等冷却或换代理 |
| `503 circuit_open` | 熔断中（默认 20s 后半开） | 等待；持续出现说明出口持续被封 |
| `502 parser_failure` | SERP 结构变化导致解析失败 | 检查对应引擎 `selectors.go`,用引擎单元测试 + testdata 快照复现 |
| `401 unauthorized` | API key 无效/被禁用/已删除 | 控制台 `/web-tool/console/` → API 密钥页检查 |
| `429 quota_exceeded` | key 当日配额用尽 | 调高日配额或等次日清零 |
| 端口冲突 | 宿主机 7000 被占用 | 改 compose 的端口映射，或清理残留容器 |
| 容器不 healthy | 浏览器启动失败/配置错误 | `docker logs serp` 查看启动日志 |

## 8. 许可证

MIT，见 [../LICENSE](../LICENSE)。
