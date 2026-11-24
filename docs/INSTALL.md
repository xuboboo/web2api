# 安装文档（INSTALL）

面向：第一次在一台干净机器上把 web2api 跑起来的人。
前提事实（来自本仓库代码与 Dockerfile，不是通用模板）：

- **单个 Go 二进制**，静态前端（官网 + 控制台）已编译进二进制，无 Node/前端构建步骤。
- **存储是内嵌 bbolt 文件**（`<platform.data_dir>/web2api.db`），没有 MySQL/Redis 需要安装。
- **必须有一个 Chrome/Chromium**：搜索引擎页面要真实浏览器渲染；没有浏览器会自动退化到 HTTP 通道（效果差、更容易被风控）。
- 平台层默认开启：数据面要 `X-API-Key`，管理面要 JWT。

---

## 1. 系统要求

| 项 | 最低 | 建议 | 说明 |
| --- | --- | --- | --- |
| OS | Linux x86_64（Ubuntu 22.04+ / Debian 12+） | 同上 | Windows/macOS 可用于开发 |
| CPU | 2 vCPU | 4 vCPU | 浏览器渲染是 CPU 密集型 |
| 内存 | 2 GB | 4 GB+ | 每个 Chrome 实例 150–300 MB；`app.max_processes` 决定峰值 |
| 磁盘 | 5 GB | 20 GB | 二进制 ~25 MB + Chrome ~400 MB + 日志/任务结果 |
| 软件 | Chrome/Chromium 120+ | 与 `chromedp/headless-shell` 同源版本 | 需 `--no-sandbox` 之外的常规依赖 |
| 网络 | 出网 80/443 | 固定出口 IP + 代理池 | 百度风控与出口 IP 强相关 |

## 2. 从源码安装

```bash
# 1) 工具链（go.mod 声明 go 1.26.0）
go version            # 需要 >= 1.26

# 2) 取代码并编译（静态链接，可跨机拷贝）
git clone <repo> web2api && cd web2api
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o web2api .

# 3) 准备配置与数据目录
sudo mkdir -p /etc/web2api /var/lib/web2api
sudo cp config.yaml /etc/web2api/config.yaml
sudo chown -R web2api:web2api /var/lib/web2api    # 数据目录必须可写
```

## 3. 首次启动（最小可用）

```bash
# 关键：先改管理员密码。默认值 admin123 仅在 loopback 开发时可用；
# 绑定 0.0.0.0 / 局域网地址时若仍用默认密码，服务会拒绝启动。
export WEB2API_PLATFORM_ADMIN_PASSWORD='<强密码，≥8 位>'

./web2api serve -c /etc/web2api/config.yaml
```

启动成功会在日志里看到监听地址、引擎注册情况、以及（若仍是默认密码）一条 `SECURITY` 警告。

## 4. 首次运行验证（逐条都要过）

```bash
BASE=http://127.0.0.1:7070/web-tool

# ① 存活
curl -s $BASE/health

# ② 就绪（含引擎/浏览器可用性）
curl -s $BASE/ready

# ③ 数据面默认要求鉴权：应为 401
curl -s -o /dev/null -w '%{http_code}\n' -X POST $BASE/v1/index/check \
  -H 'Content-Type: application/json' -d '{"urls":["https://example.com/"]}'

# ④ 登录拿 JWT（密码用上一步导出的那个；建议用 jq 提取）
TOKEN=$(curl -s -X POST $BASE/api/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<你的密码>"}' | jq -r .token)
echo "$TOKEN" | head -c 40

# ⑤ 建一个 API Key（保存返回的 key 明文，之后只显示前缀）
KEY_RESP=$(curl -s -X POST $BASE/api/keys -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"first-key"}')
KEY=$(printf '%s' "$KEY_RESP" | jq -r .key)

# ⑥ 用 Key 做一次真实收录查询（1 条，约 12 秒）
curl -s "$BASE/v1/index/check?url=https://www.baidu.com/&engines=baidu" \
  -H "X-API-Key: $KEY"

# ⑦ 控制台能打开
curl -s -o /dev/null -w '%{http_code}\n' $BASE/console/
```

## 5. 数据库初始化与迁移

- **初始化是自动的**：首次启动时 bbolt 会创建 `web2api.db` 与 5 个 bucket（`keys`/`usage`/`usage_daily`/`sessions`/`settings`），并生成 `secret.key`（0600）。
- **没有 SQL 迁移脚本**：结构由代码内 bucket/key 约定决定，升级二进制后旧库可直接打开。
- 升级前务必备份（见 `OPERATIONS.md`）：
  ```bash
  ./web2api admin-backup --data-dir /var/lib/web2api --out /backup/web2api-$(date +%F).db
  ```

## 6. Chrome 依赖

```bash
# Debian/Ubuntu 最省事的方式（版本与 Dockerfile 一致）
apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates fonts-liberation libasound2 libatk-bridge2.0-0 libatk1.0-0 \
  libc6 libcairo2 libcups2 libdbus-1-3 libexpat1 libfontconfig1 libgbm1 \
  libglib2.0-0 libgtk-3-0 libnspr4 libnss3 libpango-1.0-0 libpangocairo-1.0-0 \
  libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 libxi6 \
  libxrandr2 libxrender1 libxss1 libxtst6
```

指定路径：`app.browser_path: /usr/bin/chromium`（或环境变量 `WEB2API_APP_BROWSER_PATH`）。

## 7. 常见安装错误

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| `listen tcp 127.0.0.1:7070: bind: address already in use` | 端口被占 | 改 `server.port` 或 `lsof -i:7070` 找到占用者 |
| 启动即退出，日志 `bbolt: open ... permission denied` | `platform.data_dir` 不可写 | `chown` 给运行用户 |
| `/ready` 返回非 200，日志 `chromium not found` | 没有浏览器或路径不对 | 装 Chrome，或设 `app.browser_path` |
| 查询返回 `engine_internal` + 日志 `captcha` | 出口 IP 被抓 | 降低 `rate_requests`（百度 5/分钟），配代理，等冷却 |
| 管理面 401，确认密码正确 | 密码环境变量没生效 | `WEB2API_PLATFORM_ADMIN_PASSWORD` 拼写；或容器内没传进去 |
| 编译报 `go: downloading ... timeout` | 无外网/代理 | 配 `GOPROXY`，或改用 `Dockerfile` 构建 |
| 数据目录里出现 `secret.key` 但重启后 JWT 全失效 | 数据目录没持久化（容器重建） | 挂卷（compose 里的 `web2api-data`） |
