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

## 2. 平台契约冒烟：`deploy/e2e.mjs`

针对固定 `/web-tool` 前缀下整个平台的契约面，零依赖（Node 18+ 内置 fetch）。默认目标 `http://127.0.0.1:7000`（自动追加 `/web-tool`）。需要服务已在运行。

```bash
node deploy/e2e.mjs http://127.0.0.1:7000
# 或
WEB2API_BASE_URL=http://127.0.0.1:7000 node deploy/e2e.mjs
# 追加一次真实引擎收录判定（依赖引擎可用性,默认跳过）
E2E_LIVE_INDEX_CHECK=1 node deploy/e2e.mjs
# 人工环境跳过登录锁定测试(避免套件结束后 admin@本机IP 被锁 15 分钟)
E2E_SKIP_LOCKOUT=1 node deploy/e2e.mjs
```

| 分组 | 覆盖内容 |
| --- | --- |
| `infra` | 根路径无路由（404，含 `/mcp` 只在 `/web-tool` 下）、`/web-tool/health` 引擎清单、`/web-tool/ready` `/stats`、官网 `/web-tool/` 与管理控制台 `/web-tool/console/` 及各自静态资源 |
| `admin` | 登录成功/失败（401）、`/web-tool/api/me`、统计端点与 `/web-tool/api/engines` |
| `keys` | 创建（`sk-w2a-` 格式、完整 key 仅一次）、列表字段、停用→401、重启用、删除→401 |
| `public` | 401 信封、429 配额（`daily_quota=1` 第二次超额，含 `Retry-After` 头）、400 `EMPTY_QUERY/TOO_MANY_URLS/INVALID_PARAM`、`/web-tool/v1/engines`、别名 `360`、（可选）真实收录判定 |
| `agent` | MCP 握手链：未鉴权 401 → `initialize`（protocolVersion `2025-06-18`、serverInfo、`Mcp-Session-Id` 头）→ `notifications/initialized`（204）→ `tools/list`（4 工具 + inputSchema）→ `tools/call get_quota`；`/web-tool/openapi.yaml`（3.1 声明）与 `/web-tool/llms.txt` |
| `stability` | 冷却状态机：`cooldown/force` → `/web-tool/api/engines` 显示 `cooldown/until/reason/consecutive_blocks` → 冷却引擎 search 与 `/web-tool/v1/index/check` 快速失败 503 `engine_cooldown` + `Retry-After` → `cooldown/reset` 恢复；`/web-tool/api/playground/check` 与 `/web-tool/v1/index/check` 同构 |
| `security` | 登录连续失败锁定：失败达阈值（默认 5 次）后 429 `too_many_attempts` + `Retry-After`，锁定期内正确凭据同样 429。⚠ 该分组**最后执行**且会锁定 admin@<本机IP> 一个锁定窗口（默认 15 分钟）；`E2E_SKIP_LOCKOUT=1` 可跳过 |

退出码：存在契约失败时为 1，可直接接入 CI。报告写入 `deploy/e2e-results/platform-<时间戳>.json`。管理员凭据可用 `WEB2API_ADMIN_USER` / `WEB2API_ADMIN_PASSWORD` 覆盖（须与 config.yaml 一致）。

## 3. 低节流稳态观测：`deploy/steady-check.mjs`

用于观察“单 IP + 合理节流”下搜索引擎的持续可用性，不建议作为 CI 门禁。

```bash
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000   # 每个引擎 3 次，间隔 20s
STEADY_ENGINES=baidu,sogou node deploy/steady-check.mjs ...   # 自定义引擎集合
```

目标地址自动追加 `/web-tool` 前缀。输出逐次请求的状态与耗时，最后给出每引擎成功率；失败率 > 20% 时退出码为 1。

## 4. 运行建议

```powershell
# 平台契约验收
node deploy/e2e.mjs http://127.0.0.1:7000

# 观察引擎冷却是否恢复
node deploy/steady-check.mjs http://127.0.0.1:7000 3 30000
```

排障对照：

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| `429 captcha_detected` | 该引擎出口被判为机器人 | 触发引擎冷却（`stability.engine_cooldown`，第 1 档 10 分钟起）；等冷却解除或换代理 |
| `503 engine_cooldown` | 引擎处于冷却期（快速失败，未发真实请求） | 读 `retry_after_seconds`/`Retry-After`；运维可 `POST /web-tool/api/engines/{name}/cooldown/reset` 强制恢复 |
| `503 circuit_open` | 熔断保护中 | 等待 `circuit_breaker.recovery_seconds` 后自动半开 |
| `401 unauthorized` | API key 无效/禁用/删除 | 控制台检查密钥状态 |
| `429 too_many_attempts` | 登录连续失败被锁定（默认 5 次锁 15 分钟） | 等 `Retry-After`；锁定期内正确凭据同样被拒 |
| `429 quota_exceeded` | key 当日配额用尽 | 调高日配额或等次日清零 |
| 全部引擎 `429` | 出口 IP 被多引擎同时封禁 | 配代理或长时间静默 |