# 运维文档（OPERATIONS）

面向：接管日常运维、排障与应急的人。所有命令都对应本仓库真实实现（`cmd/admin_*.go`、`/web-tool/*` 端点、`platform/*` 代码）。

---

## 1. 日常巡检（每天 5 分钟）

Windows 本机可运行 `./deploy/sample-memory.ps1 -DurationSeconds 1800 -IntervalSeconds 10` 采样监听 7000 的 Web2API 及其子进程。CSV 包含进程树工作集、私有内存和系统空闲内存。可用 `-Port` 指定隔离实例，用 `-OutputPath` 指定新文件；脚本不会覆盖已有文件。工作集相加可能重复计算共享页，应结合私有内存和系统空闲内存判断。

```bash
BASE=http://127.0.0.1:7070/web-tool      # 按实际地址替换
KEY=<某个有效 API Key>
TOKEN=<管理员 JWT>                 # POST /api/auth/login 返回的 token

curl -s $BASE/health                       # {"status":"ok",...}
curl -s $BASE/ready                        # 引擎/浏览器可用性
curl -s $BASE/metrics | head -40           # OpenMetrics
curl -s $BASE/stats -H "X-API-Key: $KEY"   # 进程级统计
```

需要盯的指标（`/web-tool/metrics`）：

| 指标 | 含义 | 告警阈值建议 |
| --- | --- | --- |
| **`/health.storage`** | 当前存储后端，正常为 `bbolt` | **变成 `json` 立即按 P1 告警**：bbolt 打开失败已触发 fallback，当前 Key/账本可能与原库分裂 |
| 登录失败计数 | 暴力破解迹象 | 5 分钟内 > 10 次；注意锁定按**用户名跨 IP**，换 IP 不会重置失败预算 |
| 熔断状态 | 某引擎连续失败 | 任一引擎 open |
| 引擎冷却 | 被风控冷却中 | 持续 > 30 分钟 |
| 请求延迟 | 单条查询耗时 | P95 > 20 s |
| 进程 RSS | 浏览器池内存 | > 70% 物理内存 |
| 进程句柄数（长跑观察） | 泄漏迹象（含 Chrome 子进程） | 预热后**持续单调上涨**才报警；稳态小幅摆动正常 |

**长跑判读（30 分钟实测定标，见 `docs/audit/EVIDENCE.md` E31）**：RSS 冷启动 17MB → 稳态 27MB（峰值 31MB），goroutine 常驻 10（查询瞬间 19 后回落），句柄启动 162 → 预热后 407–415 饱和。**要看斜率，不要看绝对值**：

```bash
# 每小时各采一次，连续 24h；把两组数算前半程/后半程均值比
curl -s $BASE/api/runtime -H "Authorization: Bearer $TOKEN"   # goroutines / memory_mb / uptime_seconds
curl -s -o /dev/null -w '%{http_code}\n' $BASE/health          # 必须始终 200
ps -o rss=,nlwp= -p <pid>                                      # Linux RSS(KB)
ls /proc/<pid>/fd | wc -l                                      # Linux 句柄
# Windows: (Get-Process web2api).WorkingSet64 / .Handles
```

判据：**后半程均值比前半程高 > 20% 且单调** → 视为泄漏，重启并保留现场；摆动在 ±10% 内视为稳态。

> **数据库损坏的真实表现**（本次审计已实测）：`web2api.db` 损坏时，进程会按契约回退到 `web2api.json`，`/health` 仍返回 200、登录仍可用，但 API Key 与用量账本从空状态开始。**只看 HTTP 200 会漏报数据事故**。发现 `storage=json` 后：停止服务 → 隔离数据目录（不要删除）→ 从最近备份恢复 `web2api.db` → 补回 `secret.key` 与 `index_jobs/` → 启动后核对 Key 数量和 `total_units`。注意：`admin-backup` / `admin-restore`（含 `--full`）只处理 bbolt `web2api.db`，不能从 JSON fallback `web2api.json` 单独恢复；`/health.storage=json` 时请按文件级备份处理。

## 2. 日志

| 位置 | 内容 |
| --- | --- |
| stdout/stderr（`docker compose logs`） | 结构化运行日志（logrus） |
| `<platform.data_dir>/audit.log` | 管理面审计：登录成功/失败/锁定、密钥增删改、冷却操作 |
| `logs.txt`（`app.save_log=true` 时） | 轮转日志，`app.log_max_mb` / `app.log_backups` 控制 |
| `logs.txt.N` / `audit.log.N` | 历史滚动文件，按 `app.log_retention_days` 自动按天清理 |

常用排查：

```bash
docker compose logs --since 30m web2api | grep -Ei "captcha|cooldown|circuit|error"
grep -i "登录失败" /var/lib/web2api/audit.log | tail -20
```

注意：日志里**不应**出现 API Key 明文与管理员密码。密钥只以 `prefix` 形式出现（`sk-w2a-xxxxxx`）。

### 日志保留与自动清理

`app.log_retention_days`（默认 **30 天**；`0` = 不按时间删除，只保留 `app.log_backups` 份）按文件 **mtime** 删除超过窗口的滚动文件，**只动 `logs.txt.N` 与 `audit.log.N`**，不删除当前的 `logs.txt` / `audit.log`。触发时机：**服务启动时、每次轮转后、以及每 12 小时一次**；份数仍受 `app.log_backups` 限制，两者取更严格的结果。`audit.log` 复用同一保留窗口（`GET /api/logs/config` 的 `audit_retention_days`）。`app.log_max_mb=0` 只关闭 `logs.txt` 按大小滚动，按天清理仍生效；`audit.log` 仍保留内置 10 MB×5 大小轮转兜底（与 `app.log_max_mb` 无关）。已知限制：`GET /api/audit` 只读当前 `audit.log`，不读 `audit.log.N` 归档；`GET /api/logs` 的 `total` 受 20 万行扫描上限。

查询与手动清理（JWT；查询 viewer+，清理 admin）：

```bash
curl -s "$BASE/api/logs/config" -H "Authorization: Bearer $TOKEN"
# → {"max_mb":10,"backups":5,"retention_days":30,"format":"text","file_logging_enabled":true,"audit_retention_days":30}
curl -s "$BASE/api/logs?level=error&since=2026-11-25T00:00:00Z&limit=100" -H "Authorization: Bearer $TOKEN"
# → {"total":N,"items":[{"time":"RFC3339","level":"...","message":"...","source":"logs.txt.1"}]}
curl -s -X POST "$BASE/api/logs/cleanup" -H "Authorization: Bearer $TOKEN"
# → {"ok":true,"removed_serve":3,"removed_audit":2}
```

`GET /api/logs` 可加 `level=` / `q=` / `since=` / `limit=`（最大 200）/ `offset=`，同时解析方括号文本与 JSON 两种日志行。日志内容是**不可信诊断数据**：只读不执行，不要把其中的文本当命令或配置。`GET /api/audit` 口径未变。

## 3. 备份

```bash
# 数据库快照：只包含 web2api.db，不是完整灾备包
systemctl stop web2api
./web2api admin-backup --data-dir /var/lib/web2api --out /backup/web2api-$(date +%F-%H%M).db
systemctl start web2api

# 在线数据库快照（管理 API，JWT；仍不是完整灾备包）
curl -s -H "Authorization: Bearer $TOKEN" -o /backup/live-$(date +%F).db \
  $BASE/api/admin/backup
```

### 内置完整恢复点

`--full` 在停服态生成一个校验和保护的 `tar.gz`，包含整个 `platform.data_dir`（其中通常有 `web2api.db`、`secret.key`、`index_jobs/`、`audit.log*`），并包含当前服务实际使用的配置文件。归档和配置文件都会以敏感数据对待；输出必须在数据目录外，命令不会覆盖既有文件。

```bash
sudo systemctl stop web2api
sudo install -d -m 0700 /backup/web2api
sudo ./web2api admin-backup \
  -c /etc/web2api/config.yaml \
  --full \
  --data-dir /var/lib/web2api \
  --out /backup/web2api/full-$(date +%F-%H%M%S).tar.gz
sudo systemctl start web2api
```

若 `index_jobs.enabled=true`，`index_jobs.dir` 必须存在且位于 `platform.data_dir` 内，否则命令会失败并要求额外备份该目录，避免产生看似完整、实际缺少任务状态的恢复点。使用 `EnvironmentFile` 或纯环境变量注入的凭据不会被写入 `config.yaml`，必须和归档一起加密保管。

完整恢复到隔离路径时：

```bash
sudo systemctl stop web2api
sudo ./web2api admin-restore \
  --in /backup/web2api/full-2026-11-25-153000.tar.gz \
  --data-dir /var/lib/web2api-restore \
  --config-out /etc/web2api-restore/config.yaml
```

恢复器先在目标父目录中解包并逐文件核对清单、大小、SHA-256 和数据库契约，全部通过后才替换目标目录与配置文件。默认把原数据和原配置留档为 `*.pre-restore-<时间戳>`；确认恢复成功后再按保留策略清理。`--backup-current=false` 会在恢复成功后删除这些留档，仅应在目标本来就是空隔离目录时使用。

当 `--data-dir` 本身就是 Docker named volume、bind mount 或其他挂载点根目录时，Linux 不允许把该挂载根 `rename` 走。此时加 `--preserve-in /volume/outside-data`：恢复器先把旧数据完整复制到该外部保留目录，再清空挂载点内的子项并写回已验证数据；复制或写入失败会尝试从保留目录回滚。`--preserve-in` 必须位于被恢复的数据目录之外，普通目录恢复仍应使用上面的原子目录替换路径。

### systemd 目录部署：完整离线备份

没有 `web2api` 二进制、或需要额外包含环境文件与自定义路径时，可使用下面的通用离线归档。下例适用于 [DEPLOYMENT.md](DEPLOYMENT.md) 的 systemd 布局；先停服，使 bbolt、`secret.key`、审计日志和异步任务文件来自同一静止时点。归档包含管理员环境文件时会含敏感凭据，备份目录与归档必须仅管理员可读，并应加密后异地保存。

```bash
set -e
sudo install -d -m 0700 /backup/web2api
sudo systemctl stop web2api
trap 'sudo systemctl start web2api' EXIT
stamp=$(date +%F-%H%M%S)
tmp=/backup/web2api/.web2api-$stamp.tar.gz.tmp
archive=/backup/web2api/web2api-$stamp.tar.gz
# 如果 web2api.env 不存在，删除最后一个 `web2api.env` 参数。
sudo tar -czf "$tmp" \
  -C /var/lib web2api \
  -C /etc/web2api config.yaml web2api.env
sudo chmod 0600 "$tmp"
sudo mv "$tmp" "$archive"
sudo systemctl start web2api
trap - EXIT
```

归档应包含 `web2api/web2api.db`、`web2api/secret.key`、`web2api/index_jobs/`、`web2api/audit.log*`、`config.yaml`；若使用 EnvironmentFile，还应包含 `web2api.env`。定期用 `tar -tzf "$archive"` 检查清单，并把归档复制到独立故障域；同机备份和保留少于 7 天不满足发布准入。

恢复时先解到全新的隔离目录，确认清单与关键文件存在：

```bash
restore=/var/lib/web2api-restore
sudo install -d -m 0700 "$restore"
sudo tar -xzf "$archive" -C "$restore"
sudo test -s "$restore/web2api/web2api.db"
sudo test -s "$restore/web2api/secret.key"
sudo test -f "$restore/web2api/index_jobs" || sudo test -d "$restore/web2api/index_jobs"
sudo test -s "$restore/config.yaml"
```

先停止正式实例，再创建临时 drop-in（如备份含 `web2api.env`，沿用它以验证相同管理员密码）：

```bash
sudo systemctl stop web2api
sudo install -d -m 0755 /etc/systemd/system/web2api.service.d
sudo tee /etc/systemd/system/web2api.service.d/90-restore-test.conf >/dev/null <<'EOF'
[Service]
ExecStart=
ExecStart=/opt/web2api/web2api serve -c /var/lib/web2api-restore/config.yaml
Environment=WEB2API_PLATFORM_DATA_DIR=/var/lib/web2api-restore/web2api
Environment=WEB2API_SERVER_HOST=127.0.0.1
Environment=WEB2API_SERVER_PORT=7001
EnvironmentFile=
EnvironmentFile=-/var/lib/web2api-restore/web2api.env
EOF
sudo chown -R web2api:web2api /var/lib/web2api-restore
sudo systemctl daemon-reload
sudo systemctl start web2api
curl -fsS http://127.0.0.1:7001/web-tool/health
```

在端口 7001 的隔离实例验证登录、Key 数量、`total_units` 和任务历史；检查后停止服务并仅移除本次创建的 drop-in：

```bash
sudo systemctl stop web2api
sudo rm /etc/systemd/system/web2api.service.d/90-restore-test.conf
sudo systemctl daemon-reload
sudo systemctl start web2api
```

确认正式实例正常后，再按变更流程切换正式数据目录。保留旧数据目录和原 unit，直到观察期结束，回滚时切回原路径。该流程已在干净 Debian 12 systemd 容器实测：7001 隔离实例健康、管理员登录和 Key 数量与备份前一致（E63）；外部云主机重启、正式域名/CA 和生产凭据仍按 [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) 验收。

### Docker named volume

两份 Compose 使用 `web2api-data` named volume，不能直接把上面的 `/var/lib/web2api` 路径照搬成宿主机目录命令。下列流程已在 Docker Desktop Linux engine 29.9.3、非 root `chrome` 用户、`cap_drop=ALL` 与 `no-new-privileges` 下实测（E60）。卷名和配置文件路径按实际 Compose project 替换；先停服，所有辅助容器都只在停服态运行。

```bash
DATA_VOLUME=web2api-data
BACKUP_VOLUME=web2api-backup
RESTORE_DATA_VOLUME=web2api-restore-data
RESTORE_CONFIG_VOLUME=web2api-restore-config
PRESERVE_VOLUME=web2api-restore-preserve
CONFIG_FILE=/opt/web2api/config.yaml

docker volume create "$BACKUP_VOLUME"
docker run --rm --user 0:0 \
  --mount "source=$BACKUP_VOLUME,target=/backup" \
  --entrypoint /bin/sh web2api:local \
  -c 'chown chrome:chrome /backup'

docker compose stop web2api
docker run --rm --cap-drop ALL --security-opt no-new-privileges:true \
  --entrypoint /usr/local/bin/web2api \
  --mount "source=$DATA_VOLUME,target=/usr/src/app/data,readonly" \
  --mount "type=bind,source=$CONFIG_FILE,target=/usr/src/app/config.yaml,readonly" \
  --mount "source=$BACKUP_VOLUME,target=/backup" \
  web2api:local admin-backup \
  --full --data-dir /usr/src/app/data \
  -c /usr/src/app/config.yaml --out /backup/full.tar.gz

for volume in "$RESTORE_DATA_VOLUME" "$RESTORE_CONFIG_VOLUME" "$PRESERVE_VOLUME"; do
  docker volume create "$volume"
done
docker run --rm --user 0:0 \
  --mount "source=$RESTORE_DATA_VOLUME,target=/restored-data" \
  --mount "source=$RESTORE_CONFIG_VOLUME,target=/restore-config" \
  --mount "source=$PRESERVE_VOLUME,target=/preserve" \
  --entrypoint /bin/sh web2api:local \
  -c 'chown -R chrome:chrome /restored-data /restore-config /preserve'

docker run --rm --cap-drop ALL --security-opt no-new-privileges:true \
  --entrypoint /usr/local/bin/web2api \
  --mount "source=$BACKUP_VOLUME,target=/backup,readonly" \
  --mount "source=$RESTORE_DATA_VOLUME,target=/usr/src/app/restored-data" \
  --mount "source=$RESTORE_CONFIG_VOLUME,target=/restore-config" \
  --mount "source=$PRESERVE_VOLUME,target=/preserve" \
  web2api:local admin-restore \
  --in /backup/full.tar.gz \
  --data-dir /usr/src/app/restored-data \
  --config-out /restore-config/config.yaml \
  --preserve-in /preserve
```

恢复结果应包含 `/restore-config/config.yaml`；旧数据位于 `/preserve/restored-data.pre-restore-*`。启动隔离容器验证后，才能把 Compose 数据卷和配置挂载切到恢复卷：

```bash
docker run -d --name web2api-restore-check \
  --cap-drop ALL --security-opt no-new-privileges:true \
  --mount "source=$RESTORE_DATA_VOLUME,target=/usr/src/app/restored-data" \
  --mount "source=$RESTORE_CONFIG_VOLUME,target=/restore-config" \
  -e WEB2API_PLATFORM_DATA_DIR=/usr/src/app/restored-data \
  -p 127.0.0.1:17001:7000 web2api:local \
  serve -c /restore-config/config.yaml

curl -fsS http://127.0.0.1:17001/web-tool/health
```

隔离实例必须返回 `healthy`/`bbolt`，并能用备份时相同的管理员凭据登录；核对 Key 数量、用量与任务历史后删除 `web2api-restore-check`。数据卷必须在重新启动正式服务前保留，不能把 `docker compose down -v` 当作回滚操作。

备份必须覆盖的东西：

| 路径 | 内容 | 丢了会怎样 |
| --- | --- | --- |
| `web2api.db` | API Key（哈希）、配额、用量账本、管理员 bcrypt、会话、控制台用户与角色（`users` 桶）、IP/CIDR 黑名单（`blacklist` 桶）、设置 | 全部租户、账号角色、黑名单与账单丢失 |
| `secret.key` | JWT 签名密钥（0600） | 不影响 API Key，但所有管理员会话立即失效（需重新登录） |
| `index_jobs/` | 异步任务元数据与结果 JSONL | 任务历史丢失（进行中的任务无法续跑） |
| `config.yaml` | 全部配置 | 需重建配置 |

> **实测结论（本次审计）**：不带 `--full` 的 `admin-backup` 只导出 `web2api.db`，恢复后登录与 API Key 计数完好（恢复后 `acc-sample` 的 `used_today` 仍为 4）。`--full` 已在隔离临时目录完成往返验证：数据库、`secret.key`、任务 JSONL、审计日志和配置逐项恢复；路径穿越、未列入清单的条目和校验和篡改均在替换目标前被拒绝（E59）。Docker named volume 的原地恢复、容器重建持久性、管理员登录和 Key 恢复也已实测（E60）；Debian systemd 全目录归档、7001 隔离 unit 恢复与升级/回滚见 E63。

## 4. 恢复

```bash
systemctl stop web2api
./web2api admin-restore --in /backup/web2api-2026-11-24-1530.db --data-dir /var/lib/web2api
# 默认会把当前库留档为 web2api.db.pre-restore-<时间戳>，回滚只需改回来
systemctl start web2api
curl -s $BASE/health && curl -s -X POST $BASE/api/auth/login -d '{"username":"admin","password":"..."}'
```

恢复后验证三件事：能登录、`/api/keys` 里 Key 数量正确、`/api/stats/overview` 的 `total_units` 与备份前一致。

## 5. 引擎被风控时怎么办

```bash
# 看当前冷却状态（管理面）
curl -s $BASE/api/engines -H "Authorization: Bearer $TOKEN"

# 手动解除某个引擎的冷却（确认 IP 已恢复时）
curl -s -X POST $BASE/api/engines/baidu/cooldown/reset -H "Authorization: Bearer $TOKEN"

# 强制某引擎进入冷却（例如要临时停掉百度）
curl -s -X POST $BASE/api/engines/baidu/cooldown/force \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"minutes":30}'
```

判据：百度返回 302 到 `bioc-static.cdn.bcebos.com/pages/ds_svcp.html` 或 `wappass.baidu.com/.../tuxing_v2.html` = 安全验证。**不要**用降低 `rate_requests` 之外的方式硬扛——继续高频只会延长封禁。

### IP 黑名单运维

`security.blacklist` 只是**首次启动**的种子规则（如 `["203.0.113.7","198.51.100.0/24"]`）；之后一律通过控制台或 API 增删，改配置文件不再生效。管理需 operator 及以上：

```bash
curl -s "$BASE/api/blacklist" -H "Authorization: Bearer $TOKEN"
curl -s -X POST "$BASE/api/blacklist" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"cidr","value":"198.51.100.0/24","reason":"abusive crawler","expires_at":"2026-12-01T00:00:00Z"}'
curl -s -X PATCH "$BASE/api/blacklist/<id>" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"expires_at":""}'   # 空串=永久
curl -s -X DELETE "$BASE/api/blacklist/<id>" -H "Authorization: Bearer $TOKEN"
```

- `kind` 省略时按 `value` 推断 `ip` / `cidr`；非法 IP/CIDR 或重复规则返回 400 `INVALID_PARAM`（中文消息）。`expires_at` 须为 RFC3339 且晚于当前时间；`PATCH` 传 `""` 改为永久。
- 不要把当前客户端 IP 写进规则：默认返回 400 `INVALID_PARAM`（"不能屏蔽当前请求来源 IP;如确需封禁自身请显式设置 force:true"）；确需自锁须显式传 `force:true`，此后该 IP 立即失去包括解除规则在内的所有 Web 访问，请从其它 IP 操作。
- 守卫作为全站前置中间件，覆盖官网、控制台、`/api/*`、`/v1/*` 与 `/mcp` 全平面。
- **过期规则只是被忽略，不会自动删除，也不会被日志清理删除**；定期看 `expired:true` 的条目并用 `DELETE` 清理。
- 命中时返回 `403 {"error":"forbidden","reason":"blacklisted"}`，写 `blacklist.hit` 审计并累加 `hits` / `last_hit_at`。
- **客户端 IP 依赖 `security.trust_proxy_headers` + `trusted_proxies`**：反代部署必须把代理地址写进 `trusted_proxies`，并让代理用 `$remote_addr` 覆盖 `X-Real-IP`，否则可能把代理 IP 当成客户端 IP（误封/漏封）。规则存储读取失败时守卫 **fail-open**（不拦截），需结合 `/health.storage` 与审计日志留意存储状态。

## 6. 故障处理手册

| 症状 | 判断 | 处置 |
| --- | --- | --- |
| `/health` 通但 `/ready` 失败 | 浏览器/引擎不可用 | 看日志 `chromium`；重启服务；检查 `app.browser_path` |
| 全部引擎 `engine_cooldown` | 出口 IP 被连续风控 | 换出口 IP / 上代理池；等冷却；必要时 `cooldown/reset` |
| 批量任务卡在 `waiting` | 所选引擎正在冷却 | 正常行为；`wait_seconds` 会倒计时，冷却结束自动继续 |
| 任务一直 `queued` | 前面有任务在跑（FIFO） | 属设计；用 `GET /v1/index/check/jobs` 看排队情况 |
| 429 大量出现 | 触发限速或配额 | 区分 `quota_exceeded`（提额/次日）与 `rate_limited`（降频） |
| 登录 429 | 登录锁定（5 次/15 分钟，按**用户名跨 IP**） | 等待，或从审计日志确认来源 IP 后封禁；`admin-reset` 只改密码，不清内存锁定，需重启或等窗口结束 |
| 启动即退出：`安全启动被拒绝` | 非 loopback 绑定且仍用公开默认管理员密码 | 设置 `WEB2API_PLATFORM_ADMIN_PASSWORD`（或 config）；不要通过继续公网暴露来保留默认密码 |
| 磁盘涨满 | 任务结果 + 日志 | 清理 `index_jobs/` 老任务、设 `app.log_backups` / `app.log_retention_days`（或 `POST /api/logs/cleanup`）、扩盘 |
| 请求一律 403 `{"error":"forbidden","reason":"blacklisted"}` | 客户端 IP/IP 段命中黑名单 | 核对 `GET /api/blacklist` 与反代 `X-Real-IP` / `trusted_proxies`，必要时 `DELETE` 规则 |
| 操作返回 403 `{"error":"forbidden","code":"insufficient_role"}` | 当前用户角色低于端点最小角色 | 用 `GET /api/me` 看 `role`，由 admin 在 `/api/users` 调整；最后一名 admin 不可降级/禁用/删除 |
| 内存持续上涨 | 浏览器进程堆积 | 看 `app.max_processes`、`app.idle_ttl`；必要时重启 |
| 重启后任务没续跑 | `index_jobs.dir` 不可写或数据卷丢失 | 检查目录权限与挂载 |

## 7. 重启与紧急回滚

```bash
# 单实例重启（优雅）
docker compose restart web2api     # 或 systemctl restart web2api

# 紧急：立刻停止对外服务（先止损，再排查）
docker compose stop web2api

# 回滚到上一版本
docker compose down
docker tag web2api:previous web2api:local && docker compose up -d
# 数据回滚（只有在确认数据损坏时才做）
```

判断标准：**先止血再定位**。对外错误率 > 50% 或管理面无法登录 → 直接停服/回滚，不要在线上边查边改。

## 8. 安全更新

```bash
# 依赖漏洞（Go 模块）
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...

# 镜像基础层（headless-shell 与 golang builder 都按 digest 固定，升版需同时更新 digest）
docker scout cves web2api:local      # 或 trivy image web2api:local

# 轮换管理员密码
curl -s -X POST $BASE/api/auth/password -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"old_password":"...","new_password":"..."}'
```

> 只有配置管理员（`platform.admin_username` 对应的 bootstrap admin）自助改密会 bump `token_version` 并使全局 JWT 失效；其他账号改密仅撤销本人会话。忘记密码时先停服再用 `admin-reset`，它同时更新用户库与 `token_version`。

## 9. 值班速查（一屏）

```
健康  : curl -s $BASE/health ; curl -s $BASE/ready
日志  : docker compose logs --tail=200 web2api | grep -Ei "captcha|cooldown|error"
引擎  : curl -s $BASE/api/engines -H "Authorization: Bearer $TOKEN"
任务  : curl -s $BASE/v1/index/check/jobs -H "X-API-Key: $KEY"
配额  : curl -s $BASE/v1/quota -H "X-API-Key: $KEY"
备份  : ./web2api admin-backup --full -c config.yaml --out /backup/x.tar.gz  （先停服）
恢复  : ./web2api admin-restore --in /backup/x.tar.gz --config-out config.yaml
改密  : ./web2api admin-reset --data-dir <dir> --username admin --password '<新密码>'（停服；同一事务更新 settings 与 users 桶并递增 token_version，旧 JWT 全部失效）
```
