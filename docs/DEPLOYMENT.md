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
# 1) 准备配置：务必先改管理员密码
cp config.yaml /opt/web2api/config.yaml
$EDITOR /opt/web2api/config.yaml          # platform.admin_password / platform.data_dir

# 2) 构建并启动（本仓库 compose 从源码构建，保证镜像与代码一致）
cd /opt/web2api
docker compose up -d --build

# 3) 验证
docker compose ps
docker compose logs --tail=50 web2api
curl -s http://127.0.0.1:7000/web-tool/health
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7000/web-tool/console/   # 预期 200/301
```

要点：

- `volumes: web2api-data:/usr/src/app/data` 必须保留 —— `web2api.db`、`secret.key`、`index_jobs/` 都在里面。
- `init: true` + 镜像内 `tini` 负责回收 Chrome 子进程，缺了会攒僵尸进程。
- 镜像内已固定 `WEB2API_APP_BROWSER_PATH=/headless-shell/headless-shell`。
- `HEALTHCHECK` 打 `/web-tool/health`，失败 3 次容器会被标记 unhealthy。

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
docker compose stop          # 或 systemctl stop web2api

# 升级（零结构迁移：bbolt 直接被新版本打开）
./web2api admin-backup --data-dir /var/lib/web2api --out /backup/pre-upgrade.db   # 先备份
docker compose up -d --build && docker compose logs -f --tail=50 web2api
curl -s http://127.0.0.1:7000/web-tool/ready

# 回滚
docker compose down
docker run --rm -v web2api-data:/data -v /backup:/backup alpine \
  cp /backup/pre-upgrade.db /data/web2api.db          # 或 ./web2api admin-restore --in ...
docker tag <上一个镜像> web2api:local && docker compose up -d
```

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
