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

> 内存估算依据：每个 Chrome 实例 150–300 MB；`app.max_processes=N` 即上限。1.4 GB 的机器必须把 `shm_size` 降到 512mb（compose 已按此写好注释）。

## 2. 端口与网络

| 端口 | 用途 | 暴露范围 |
| --- | --- | --- |
| 7000 | HTTP（API + 官网 + 控制台） | **只对反向代理/内网开放** |
| — | 不需要 80/443 | 由 Nginx/Caddy 终结 TLS |

明文 HTTP 上跑的是管理员密码与 API Key，**不要**把 7000 直接映射到公网。

## 3. 部署方式 A：Docker Compose（推荐）

```bash
# 1) 准备配置与强密码（禁止把密码写在命令行历史里）
cp config.yaml /opt/web2api/config.yaml
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
EnvironmentFile=-/etc/web2api/web2api.env     # 放管理员密码等敏感项，权限 0600
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

    location / {
        proxy_pass http://127.0.0.1:7000;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

> 开了反代后必须设 `security.trust_proxy_headers: true`，否则限速与登录锁定看到的是反代的 IP，等于所有人共用一个额度。

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
| 存活 | `curl -s :7000/web-tool/health` | 200 |
| 就绪（含浏览器） | `curl -s :7000/web-tool/ready` | 200 |
| 数据面鉴权 | 无 Key 调 `/v1/index/check` | 401 |
| 管理面鉴权 | 无 JWT 调 `/api/keys` | 401 |
| 控制台 | `/web-tool/console/` | 200 |
| 官网 | `/web-tool/` | 200 |
| 指标 | `curl -s :7000/web-tool/metrics`（本机） | OpenMetrics 文本 |
| 一次真实判定 | 带 Key 查 1 条 URL | `indexed` 字段有值，`took_ms` 数秒 |
| 任务通道 | 25 条 URL | 202 + `job_id`，轮询 `status` 到 `done` |

## 8. 已知部署限制

- **单实例语义**：限速、熔断、引擎冷却、登录锁定、任务队列都是进程内状态。多副本会各自计数 → 实际速率翻倍、冷却互不可见。要水平扩展需先外部化这些状态（当前不支持）。
- **任务队列单进程 FIFO**：同一时刻只跑一个批量任务（引擎按出口 IP 限速）。多租户高并发场景需排队，属设计取舍。
- **Chrome 与内核**：headless-shell 需要 `shm`（compose 已配 512mb）与足够文件句柄。
