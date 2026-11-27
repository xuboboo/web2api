# 部署文档（DEPLOYMENT）

面向：把 web2api 部署到生产服务器并完成上线验证的运维/DevOps。
所有命令与配置均取自本仓库（`Dockerfile`、`docker-compose.yaml`、`config.yaml`、`cmd/*.go`）。

---

## 1. 生产服务器规格

| 规模 | 配置 | 预期能力 |
| --- | --- | --- |
| 最低可用 | 2 vCPU / 2 GB / 20 GB | 单实例，`app.max_processes=2`，百度约 5 次/分钟 |
| 推荐 | 4 vCPU / 4 GB / 40 GB | `max_processes=4`，四引擎并行，批量任务稳定 |
| 有并发租户 | 8 vCPU / 8 GB + 代理池 | 每个出口 IP 独立限速；`max_concurrent_requests` 提到 16–32 |

> 内存估算依据：每个 Chrome 实例 150–300 MB；`app.max_processes=N` 即应用侧上限。两个 Compose 模板默认限制容器 memory 768 MiB、CPU 2 核、PIDs 128；可用 `WEB2API_MEM_LIMIT`、`WEB2API_CPUS`、`WEB2API_PIDS_LIMIT` 覆盖。目标主机应在压测后调整，低内存主机不要盲目提高 `shm_size`（默认 512 MiB）。

## 2. 端口与网络

| 端口 | 用途 | 暴露范围 |
| --- | --- | --- |
| 7000 | HTTP（API + 官网 + 控制台） | **只对反向代理/内网开放** |
| — | 不需要 80/443 | 由 Nginx/Caddy 终结 TLS |

明文 HTTP 上跑的是管理员密码与 API Key，**不要**把 7000 直接映射到公网。

## 3. 部署方式 A：Docker Compose（推荐）

```bash
# 1) 把仓库检出到 /opt/web2api（compose 与 Dockerfile 都在仓库里）
git clone https://github.com/xuboboo/web2api.git /opt/web2api
$EDITOR /opt/web2api/config.yaml          # platform.data_dir / server.host
read -rsp '管理员密码: ' ADMIN_PASSWORD; echo
export ADMIN_PASSWORD

# 2) 构建并启动（本仓库 compose 从源码构建，保证镜像与代码一致）
cd /opt/web2api
ADMIN_PASSWORD="$ADMIN_PASSWORD" docker compose up -d --build
unset ADMIN_PASSWORD

# 3) 验证
docker compose ps
docker compose logs --tail=50 web2api
curl -s http://127.0.0.1:7000/web-tool/health
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7000/web-tool/console/   # 预期 200/301
```

> 正式访问根是 `https://radar.ydsdoa.cn/web-tool/`，但必须由反向代理把 `/web-tool` 指到本机 `127.0.0.1:7000`。验证时 `https://radar.ydsdoa.cn/web-tool/health` 应返回 JSON；若返回 HTML，说明域名当前仍指向其它应用，文档中的公网链接暂不可达。

> **`ADMIN_PASSWORD` 对每条 compose 命令都是必需的**：`docker-compose.yaml` 用 `${ADMIN_PASSWORD:?...}` 声明它，缺失时 `docker compose` 会在解析阶段直接报错（这是有意的——容器里进程必须绑 `0.0.0.0` 才能被端口映射访问，而服务在非 loopback + 默认密码时会拒绝启动，见 F-26）。
>
> 不想每条命令都带前缀，就在 compose 同目录放一个 `.env`（compose 自动读取，加入 `.gitignore`）：
>
> ```bash
> install -m 600 /dev/null /opt/web2api/.env
> printf 'ADMIN_PASSWORD=%s\n' "$ADMIN_PASSWORD" >> /opt/web2api/.env
> ```
>
> 故障时仍需查看重启前的日志：`ADMIN_PASSWORD=x docker compose logs --tail=200 web2api`（值不参与日志，只为通过插值）。

要点：

- **非 loopback 绑定 + 默认管理员密码会拒绝启动**（退出码 1）。Compose 默认容器内 `0.0.0.0`，必须通过 `ADMIN_PASSWORD` / `WEB2API_PLATFORM_ADMIN_PASSWORD` 传强密码；loopback 本地开发不受影响。
- `volumes: web2api-data:/usr/src/app/data` 必须保留 —— `web2api.db`、`secret.key`、`index_jobs/` 都在里面。
- `init: true` + 镜像内 `tini` 负责回收 Chrome 子进程，缺了会攒僵尸进程。
- 镜像内已固定 `WEB2API_APP_BROWSER_PATH=/headless-shell/headless-shell`。
- Compose 显式检查 `/web-tool/health`；连续失败 3 次容器会被标记 unhealthy。两个 Compose manifest 同时丢弃全部 Linux capabilities、启用 `no-new-privileges`，并把 json-file 日志轮转限制为 20MB × 3。容器运行时需在上线 runner 实测。

## 4. 部署方式 B：systemd + 二进制

```bash
sudo useradd --system --home /var/lib/web2api --shell /usr/sbin/nologin web2api
sudo install -d -m 0755 /opt/web2api
sudo install -d -m 0750 -o root -g web2api /etc/web2api
sudo install -d -m 0750 -o web2api -g web2api /var/lib/web2api
sudo install -m 0640 -o root -g web2api config.yaml /etc/web2api/config.yaml
sudo install -m 0600 -o root -g root /dev/null /etc/web2api/web2api.env
```

运行用户必须能穿越 `/etc/web2api` 并读取 `config.yaml`；目录置为 `0700 root:root` 会导致 `serve` 启动后立即以 `permission denied` 退出。`web2api.env` 只在 root 读取阶段使用，保持 `0600 root:root`，不要把它改成全局可读。

```ini
# /etc/systemd/system/web2api.service
[Unit]
Description=web2api index-intel service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=web2api
Group=web2api
WorkingDirectory=/opt/web2api
Environment=WEB2API_SERVER_HOST=127.0.0.1
Environment=WEB2API_SERVER_PORT=7000
Environment=WEB2API_PLATFORM_DATA_DIR=/var/lib/web2api
# 设置 WEB2API_PLATFORM_ADMIN_PASSWORD 等敏感项，文件权限 0600
EnvironmentFile=/etc/web2api/web2api.env
ExecStart=/opt/web2api/web2api serve -c /etc/web2api/config.yaml
Restart=always
RestartSec=5
# Chrome 需要较多进程/文件句柄
LimitNOFILE=65535
# 优雅退出：SIGTERM 后等待在途请求结束（代码里是 ShutdownWithTimeout）
TimeoutStopSec=90

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now web2api
sudo systemctl status web2api --no-pager
```

> **二进制部署必须带上面板资源**：`web/site`、`web/admin` 要放在 `WorkingDirectory`（上面是 `/opt/web2api`）下，与二进制同级。release tar 包（`dist/web2api-*.tgz`）已包含 `web/`；自己 `go build` 的话需要手动拷贝。漏掉时 API 与 `/health` 都正常，但 `/web-tool/`、`/web-tool/console/` 返回 404（日志会打印「静态资源缺失」警告）。详见 `INSTALL.md` §4。

### 本机 Windows 实例：可复现部署脚本

仓库提供 `deploy/local-deploy.ps1`，把此前手工部署中易错的“复制到 web/web”“静态资源缓存版本不变”“启动凭据读取”全部固化为脚本：

~~~powershell
# 完整部署：构建 → 备份 → 替换二进制/web → 按文件哈希刷新静态资源版本 → 启动 → 验收
./deploy/local-deploy.ps1 -UpdateConfig

# 只做验收（不重启、不改动）：核对线上导航、JS 页面函数、health、登录角色、日志配置
./deploy/local-deploy.ps1 -VerifyOnly
~~~

脚本行为与保护：

- 复制前端时使用 `web/*` 通配复制到部署目录 `web` 根，并显式清理误建的 `web/web` 子目录；
- 部署前检查仓库 `app.js` 必须包含 `pageUsers` / `pageLogs` / `pageBlacklist`，部署后再次核对磁盘与 HTTP 响应；
- 用 `app.js` / `style.css` 的 SHA-256 前 8 位替换控制台 `index.html` 的 `?v=`，不需要手工维护版本号；
- 启动脚本兼容 Windows PowerShell 5.1（SecureString 读取不用 `ConvertFrom-SecureString -AsPlainText`）；
- 验收失败会抛错并保留带时间戳的旧二进制与 `web` 备份，便于回滚。

## 5. 反向代理（TLS 终结）

```nginx
server {
    listen 443 ssl http2;
    server_name api.example.com;

    ssl_certificate     /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;

    # 批量同步查询最多 20 条 × 12 秒 -> 需要长超时；异步任务不受此限制
    proxy_read_timeout 900s;
    proxy_send_timeout 900s;
    client_max_body_size 10m;          # 与 Fiber BodyLimit 对齐
    proxy_request_buffering off;

    proxy_set_header Host              $host;
    # 用反代观察到的地址覆盖来路请求头。
    proxy_set_header X-Real-IP         $remote_addr;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    location = /web-tool {
        return 308 /web-tool/$is_args$args;
    }

    # 仅向本机和明确授权的管理网段开放后台及登录路由。
    location ~* ^/web-tool/(api|console)(/|$) {
        allow 127.0.0.1;
        allow ::1;
        # allow <管理 VPN 或堡垒机 CIDR>;
        deny all;
        proxy_pass http://127.0.0.1:7000;
    }

    location /web-tool/ {
        # No URI suffix: preserve /web-tool/ when forwarding to the app.
        proxy_pass http://127.0.0.1:7000;
    }
}
```

> 开了反代后必须设 `security.trust_proxy_headers: true`，并把实际反代地址/CIDR 写入 `security.trusted_proxies`（默认只信任本机 `127.0.0.1/32` 与 `::1/128`）。应用只从受信来源读取被 Nginx 覆盖的 `X-Real-IP`；否则限速、登录审计和来源限制看到的是反代地址。不要把客户端传来的转发头直接当作真实 IP。

启用 `security.trust_proxy_headers` 时，即使应用仅监听回环地址，只要数据库中有启用的管理员仍使用出厂密码，服务就会拒绝启动。远程管理时在管理路由的 `allow` 列表中填入实际管理网段。登录锁定仍按用户名跨来源累计，边缘白名单限制了可触发锁定的来源。公网数据 API 位于 `/web-tool/v1/` 等路径。

共享域名应将上述 `location` 放进现有的 TLS `server`，保留其它应用的规则。绑定 `security.metrics_token`，或在公网反代单独拦截 `/web-tool/metrics`。改动后运行 `node deploy/e2e.mjs https://radar.ydsdoa.cn`，同时检查状态码和响应内容。仅检查 HTTP 200 会把其它应用的 HTML 当成健康响应。

## 6. 服务启停与升级

```bash
# 停止（优雅：等在途请求结束）
systemctl stop web2api

# 以下为源码 + systemd 布局；升级前生成完整恢复点（零结构迁移）
./web2api admin-backup --full -c /etc/web2api/config.yaml \
  --data-dir /var/lib/web2api --out /backup/pre-upgrade.tar.gz
systemctl start web2api

# 回滚
systemctl stop web2api
./web2api admin-restore --in /backup/pre-upgrade.tar.gz \
  --data-dir /var/lib/web2api --config-out /etc/web2api/config.yaml
systemctl start web2api
```

Docker Compose 部署必须把备份文件映射到容器内可写目录，并先用 `docker compose stop` 停服。完整恢复时挂载数据卷和目标配置目录；由于 Compose 模板把 `/usr/src/app/config.yaml` 只读挂载，`--config-out` 应写到单独的恢复配置卷，核对后再替换宿主机配置。Docker named volume 本身不能作为 `rename` 目标，使用 `--preserve-in` 把旧卷内容留档并从 staging 原地写入。完整命令、权限准备和恢复后验证见 `OPERATIONS.md` §3；该流程已在本地 Linux engine 完成往返实测（E60）。

## 7. 部署后验证清单

| 检查 | 命令 | 期望 |
| --- | --- | --- |
| 存活 | `curl -s http://127.0.0.1:7000/web-tool/health` | 200 |
| 就绪（含浏览器） | `curl -s http://127.0.0.1:7000/web-tool/ready` | 200 |
| 数据面鉴权 | 无 Key 调 `/v1/index/check` | 401 |
| 管理面鉴权 | 无 JWT 调 `/api/keys` | 401 |
| 控制台 | `/web-tool/console/` | 200 |
| 官网 | `/web-tool/` | 200 |
| 指标 | `curl -s http://127.0.0.1:7000/web-tool/metrics`（本机） | OpenMetrics 文本 |
| 一次真实判定 | 带 Key 查 1 条 URL | `indexed` 字段有值，`took_ms` 数秒 |
| 任务通道 | 25 条 URL | 202 + `job_id`，轮询 `status` 到 `done` |
| 角色初值 | 登录响应与 `/api/me` | `user.role=admin`、`user_id` 非空 |
| 日志保留 | `GET /web-tool/api/logs/config -H "Authorization: Bearer $TOKEN"`（登录后执行） | `retention_days` 与配置一致，`file_logging_enabled=true` |
| 黑名单初值 | `GET /web-tool/api/blacklist` | 仅配置种子条目；不要把自己来源 IP 写进去 |

### 首次上线后的身份与日志配置

1. 用管理员登录并确认 `GET /web-tool/api/me` 返回 `role=admin`。需要多人协作时再创建 `operator` / `viewer`：`operator` 可管理密钥、冷却和黑名单，`viewer` 只读；用户管理、会话撤销与日志清理仅 `admin` 可用。
2. 按合规要求设置 `app.log_retention_days`（默认 30 天）。滚动日志按 mtime 清理，可用 `POST /web-tool/api/logs/cleanup` 立即触发；当前 `logs.txt` / `audit.log` 不会被时间清理删除。
3. `security.blacklist` 只在**首次启动**写入 settings 一次性标记，之后请在控制台或 `POST /web-tool/api/blacklist` 管理。默认拒绝封禁当前客户端 IP；确需封禁自身要显式传 `force:true`，该操作会立即锁死自己的全部 Web 访问。
4. 忘记管理员密码时先停服，再用 `web2api admin-reset --data-dir <data_dir> --username admin --password <新密码>` 离线重置；它会同时更新用户库并递增 token_version，所有旧 JWT 失效。详见 OPERATIONS.md §8。
### 官网搜索索引

内置 `robots.txt` 屏蔽控制台和 API 路径。确认正式 HTTPS 域名后，可部署包含官网和状态页绝对 URL 的 `web/site/sitemap.xml`，并在根路径 `robots.txt` 中声明该站点地图的绝对地址。若由反向代理提供这些文件，确认 `/robots.txt` 和 `/sitemap.xml` 抵达预期服务。默认安装可直接访问官网，且不会向搜索引擎发布占位域名。

## 8. 已知部署限制

- **单实例语义**：限速、熔断、引擎冷却、登录锁定、任务队列都是进程内状态。多副本会各自计数 → 实际速率翻倍、冷却互不可见。要水平扩展需先外部化这些状态（当前不支持）。
- **任务队列单进程 FIFO**：同一时刻只跑一个批量任务（引擎按出口 IP 限速）。多租户高并发场景需排队，属设计取舍。
- **Chrome 与内核**：headless-shell 需要 `shm`（compose 已配 512mb）与足够文件句柄。
