# 运维文档（OPERATIONS）

面向：接管日常运维、排障与应急的人。所有命令都对应本仓库真实实现（`cmd/admin_*.go`、`/web-tool/*` 端点、`platform/*` 代码）。

---

## 1. 日常巡检（每天 5 分钟）

```bash
BASE=http://127.0.0.1:7070/web-tool      # 按实际地址替换
KEY=<某个有效 API Key>

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

> **数据库损坏的真实表现**（本次审计已实测）：`web2api.db` 损坏时，进程会按契约回退到 `web2api.json`，`/health` 仍返回 200、登录仍可用，但 API Key 与用量账本从空状态开始。**只看 HTTP 200 会漏报数据事故**。发现 `storage=json` 后：停止服务 → 隔离数据目录（不要删除）→ 从最近备份恢复 `web2api.db` → 补回 `secret.key` 与 `index_jobs/` → 启动后核对 Key 数量和 `total_units`。

## 2. 日志

| 位置 | 内容 |
| --- | --- |
| stdout/stderr（`docker compose logs`） | 结构化运行日志（logrus） |
| `<platform.data_dir>/audit.log` | 管理面审计：登录成功/失败/锁定、密钥增删改、冷却操作 |
| `logs.txt`（`app.save_log=true` 时） | 轮转日志，`app.log_max_mb` / `app.log_backups` 控制 |

常用排查：

```bash
docker compose logs --since 30m web2api | grep -Ei "captcha|cooldown|circuit|error"
grep -i "登录失败" /var/lib/web2api/audit.log | tail -20
```

注意：日志里**不应**出现 API Key 明文与管理员密码。密钥只以 `prefix` 形式出现（`sk-w2a-xxxxxx`）。

## 3. 备份

```bash
# 方式 A：停服备份（最稳，bbolt 一致性有保证）
systemctl stop web2api
./web2api admin-backup --data-dir /var/lib/web2api --out /backup/web2api-$(date +%F-%H%M).db
systemctl start web2api

# 方式 B：在线快照（管理 API，JWT）
curl -s -H "Authorization: Bearer $TOKEN" -o /backup/live-$(date +%F).db \
  $BASE/api/admin/backup
```

备份必须覆盖的东西：

| 路径 | 内容 | 丢了会怎样 |
| --- | --- | --- |
| `web2api.db` | API Key（哈希）、配额、用量账本、管理员 bcrypt、会话、设置 | 全部租户与账单丢失 |
| `secret.key` | JWT 签名密钥（0600） | 不影响 API Key，但所有管理员会话立即失效（需重新登录） |
| `index_jobs/` | 异步任务元数据与结果 JSONL | 任务历史丢失（进行中的任务无法续跑） |
| `config.yaml` | 全部配置 | 需重建配置 |

> **实测结论（本次审计）**：`admin-backup` 只导出 `web2api.db`，恢复后登录与 API Key 计数完好（已实机验证：恢复后 `acc-sample` 的 `used_today` 仍为 4）。`secret.key` 与 `index_jobs/` 需单独备份。

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
| 磁盘涨满 | 任务结果 + 日志 | 清理 `index_jobs/` 老任务、设 `app.log_backups`、扩盘 |
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

> 轮换密码会 bump 内部 token version，使**所有**已签发 JWT 失效（设计如此）。

## 9. 值班速查（一屏）

```
健康  : curl -s $BASE/health ; curl -s $BASE/ready
日志  : docker compose logs --tail=200 web2api | grep -Ei "captcha|cooldown|error"
引擎  : curl -s $BASE/api/engines -H "Authorization: Bearer $TOKEN"
任务  : curl -s $BASE/v1/index/check/jobs -H "X-API-Key: $KEY"
配额  : curl -s $BASE/v1/quota -H "X-API-Key: $KEY"
备份  : ./web2api admin-backup --out /backup/x.db   （先停服）
恢复  : ./web2api admin-restore --in /backup/x.db
改密  : ./web2api admin-reset --password '<新密码>'  （停服状态，忘记密码时）
```
