# 上线前审计：计划与真实架构档案

> 本文件是这次上线审计的**唯一事实来源**：所有结论都必须能追溯到代码位置、接口调用或测试输出。
> 未经验证的内容一律标 `待验证`，不允许写成结论。

审计日期：2026-11-25（复审）
被审计对象：`D:\web2api`（web2api 收录查询开放平台）
审计优先级：**稳定性 > 安全性 > 核心业务准确性 > 性能 > 用户体验 > 功能扩展**

---

## 1. 真实技术栈（来自 go.mod / Dockerfile / 代码，非推测）

| 层 | 技术 | 版本 | 依据 |
| --- | --- | --- | --- |
| 语言 | Go | `go 1.26.0` | `go.mod` |
| HTTP 框架 | Fiber | v2.52.16 | `go.mod` |
| 浏览器自动化 | go-rod | v0.116.2 | `go.mod` |
| TLS 指纹 HTTP | bogdanfinn/tls-client | v1.15.1 | `go.mod` |
| 存储 | bbolt（内嵌 KV） | v1.4.0 | `go.mod`、`platform/store_bolt.go` |
| 配置 | Viper + pflag/cobra | v1.20.1 | `go.mod`、`cmd/root.go` |
| 日志 | logrus | v1.9.4 | `go.mod` |
| 认证 | 自研 HS256 JWT + bcrypt | `golang.org/x/crypto v0.58.0` | `platform/jwt.go`、`platform/platform.go` |
| 限流 | `golang.org/x/time/rate` | v0.14.0 | `go.mod`、`core/resilient.go` |
| 验证码 | 2captcha SDK（可选，默认关） | v1.1.10 | `go.mod`、`config.yaml` |
| 前端 | 原生 JS 单文件控制台 + 静态官网 | 无构建链 | `web/admin/assets/app.js`、`web/site/index.html` |
| 容器 | chromedp/headless-shell + tini | digest 固定 | `Dockerfile` |

**没有**Redis、没有外部数据库、没有消息队列、没有 K8s manifests —— Go 服务二进制 + 单文件 KV；官网和控制台从部署目录的 `web/site`、`web/admin` 磁盘资源读取，不编入二进制。这一点后面所有容量与部署结论都以此为前提，不允许按「有 Redis/MySQL」的惯性去写方案。

## 2. 进程与模块

```
main.go → cmd (cobra)
  ├── serve        → core.Server (Fiber) + platform (bbolt) + mcp
  ├── search       单次检索 CLI
  ├── check-index  收录判定 CLI
  └── admin-*      停服态运维（backup/restore/reset/export）
```

| 包 | 职责 | 规模 |
| --- | --- | --- |
| `core` | 引擎抽象、浏览器池、限流/熔断/冷却、收录判定、任务队列、HTTP 服务 | 最大 |
| `platform` | API key、配额、用量账本、JWT/会话、审计、限流、静态面板路由 | 大 |
| `mcp` | MCP streamable HTTP（6 工具）+ 内嵌 openapi.yaml/llms.txt | 中 |
| `baidu`/`bing`/`sogou`/`so360` | 四引擎实现（SERP 解析、验证码识别、指纹通道） | 中 |
| `cmd` | 配置装载/校验/默认值、命令、路由装配 | 中 |
| `testutil`/`examples` | 测试辅助与示例 | 小 |

## 3. HTTP 接口清单（代码级，逐条 grep 得到）

### 3.1 运维/公开（核心注册，`core/server.go:267-289`）
| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| GET | `/web-tool/health` | 无 | 健康检查（Dockerfile HEALTHCHECK 用） |
| GET | `/web-tool/ready` | 无 | 就绪 |
| GET | `/web-tool/metrics` | Bearer `metrics_token`，空则仅 loopback | OpenMetrics |
| GET | `/web-tool/stats`、`/stats/cache`、`/stats/proxy`、`/stats/cb` | `security.data_plane_auth` 决定 | 运行统计 |
| GET | `/web-tool/debug/fingerprint-check` | 仅 `app.debug_endpoints=true` | 指纹自检 |
| GET | `/web-tool/{baidu,sogou,so360,360,bing}/search` | `data_plane_auth` | 单引擎检索 |

### 3.2 数据面（平台注册，`platform/handlers_public.go`，API Key）
| 方法 | 路径 | 计费 |
| --- | --- | --- |
| POST | `/web-tool/v1/index/check` | ≤20 同步 / >20 转任务 |
| GET | `/web-tool/v1/index/check?url=` | 1×引擎数 |
| GET | `/web-tool/v1/index/check/jobs`、`/jobs/:id`、`/jobs/:id/results`、DELETE `/jobs/:id` | 不计费 |
| GET | `/web-tool/v1/quota`、`/v1/engines` | 不计费 |

### 3.3 管理面（`platform/handlers_admin.go`，JWT + 限流）
登录 / 改密 / 刷新 / 退出；keys CRUD + rotate；stats overview|timeseries；usage + export；reconcile；engines + cooldown force|reset；sessions 列表/撤销；audit；admin/backup；playground/check。共 22 个端点（详见代码）。

### 3.4 Agent 面
`POST/GET/DELETE /web-tool/mcp`、`GET /web-tool/openapi.yaml`、`GET /web-tool/llms.txt`。

### 3.5 静态
`/web-tool/`（官网）、`/web-tool/status`、`/web-tool/console/`（控制台）、SPA fallback `/web-tool/*`。

## 4. 数据模型（bbolt）

`<platform.data_dir>/web2api.db`，5 个 bucket（`platform/store.go:13-17`）：

| bucket | 内容 | 关键点 |
| --- | --- | --- |
| `keys` | API key 记录（hash、配额、状态、IP 白名单、过期） | key 明文不落库 |
| `usage` | 逐笔用量账本（时间、key、endpoint、units、status、引擎明细） | 有保留期清理 |
| `usage_daily` | 按日聚合（对账用） | |
| `sessions` | 管理员会话（sid、IP、UA、过期） | 撤销即时生效 |
| `settings` | 管理员密码 bcrypt、token_version、自定义密码标记等 | |

同目录 `secret.key`：32 字节随机 JWT 签名密钥，**0600**（`platform/platform.go:169-184`）。

## 5. 第三方依赖与外部服务

| 依赖 | 必需 | 失效影响 |
| --- | --- | --- |
| 百度/搜狗/360/必应 页面 | 是 | 检索与收录判定全部失败；有冷却/熔断降级 |
| 本机 Chrome（或 Docker 内 headless-shell） | 是 | 浏览器通道不可用，回退 HTTP 通道 |
| 2captcha | 否（默认关） | 验证码只能等冷却 |
| 目标站点（抓 `<title>`） | 百度收录判定需要 | 抓不到标题时回退 URL 短语检索 |

## 6. 审计阶段与交付物

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| P0 | 摸底：技术栈/接口/数据结构/依赖 | ✅ 完成（本文件 §1-5） |
| P1 | 仓库与构建卫生、可部署性 | ✅ Dockerfile、TLS、最小权限、named-volume 恢复、可信反代来源和 DNS 重绑定均修复并实跑（F-43/F-44/F-46/F-48/F-51/F-52，E60/E62/E63） |
| P2 | 核心业务：百度收录判定准确性（真实数据回放） | ✅ 既有流程与严格集成 smoke 之外，本轮完成 69 条低频采样和三正三负同口径桌面百度人工对照（E64） |
| P3 | 接口与鉴权：逐端点越权/参数/限速验证 | 🟡 既有鉴权/越权实测于 E1/E4/E33；本轮未重新做逐端点接口穿透与滥用实测 |
| P4 | 并发与容量：任务队列、限流、资源占用 | 🟡 既有并发/竞态/队列证据留档；本轮当前树全量测试、race、严格集成 race 与 lint 通过（E37）；历史负载数据未重跑 |
| P5 | 稳定性：异常注入（引擎挂、浏览器崩、磁盘满、重启） | 🟡 既有崩溃恢复、任务目录故障和重启续跑证据；真实写满磁盘仍未做 |
| P6 | 数据安全：备份/恢复实测、密钥与日志 | 🟡 既有备份恢复实测于 E15；本轮未重复恢复演练 |
| P7 | 前端与 UX：真实浏览器走查 | 🟡 已登录走查主要控制台页面（E35）；完整桌面/移动视口矩阵仍待验证 |
| P8 | 文档交付：INSTALL/DEPLOYMENT/ENVIRONMENT/OPERATIONS/RELEASE_CHECKLIST | ✅ 五类文档已存在；干净 Debian 12 systemd/NGINX 走查发现并修复目录权限缺口，安装、TLS、备份恢复和升级回滚已跑通（E63） |

### 已产出的审计交付物

- `docs/audit/AUDIT-PLAN.md`（本文件）：架构档案 + 阶段计划 + 环境限制
- `docs/audit/FINDINGS.md`：滚动问题清单（F-01…F-47，含证据/根因/修复/验证/回滚）
- `docs/audit/EVIDENCE.md`：实机验证输出（鉴权、穿越、配额、越权前后对比、构建上下文、测试基线）

### 执行纪律

- 每项修复必须带：证据 → 根因 → 修复 → 验证（单测/实机）→ 回归。
- 涉及行为变更的修复必须在 FINDINGS 里写明兼容性与回滚方式。
- 无法在本环境验证的结论，一律进 `EVIDENCE.md` 的"环境限制"，不写成"已验证"。


### 6.1 本轮复审证据状态（2026-11-25）

- 已在当前树复测：全量 Go 测试、vet、race 单测、严格集成 race、golangci-lint 与 govulncheck（证据 E36–E38）。低频百度/必应 live smoke 通过，但不代表准确率样本回放或全引擎容量测试。
- Docker Desktop Linux engine 已用于最终镜像构建、fresh named volume、非 root/最小权限、HTTPS、完整恢复与容器重建持久性验证（E60）；干净 Debian 12 systemd + NGINX、可信客户端 IP、升级回滚和隔离恢复见 E63。
- 已完成隔离浏览器主要管理页面走查和 bbolt 备份/恢复登录验证（E35）；精确桌面/移动断点矩阵与生产环境恢复演练仍待完成。
- 五份运维交付文档均存在，INSTALL/DEPLOYMENT 已在干净 Debian 12/systemd 容器端到端走通；生产监控、备份保留、正式域名/CA 和告警仍由部署方配置。
- 代理 DNS rebinding、三组同口径人工百度正负样本、干净 Linux/systemd 部署已闭环；剩余未勾项主要是生产凭据、监控告警、异地保留、容量/SLA 与极端故障注入。详细以 `docs/RELEASE_CHECKLIST.md` 为准。
## 7. 环境限制（影响结论强度，必须写明）

- 本轮通过 Go 官方页面校验并使用临时 Go 1.26.11 工具链完成 `go test`、`go vet` 和 Linux/amd64 交叉构建；工具链留在系统临时目录，不修改 PATH。
- 初次验证时缺少可用 `gcc`/`clang`；MinGW 工具链配置后，当前树 race 与严格集成 race 于 2026-11-25 通过（E37）。
- 本机为 Windows；Docker Desktop Linux engine 已完成镜像和 named-volume 验证（E60）。另启动干净 Debian 12 systemd 容器完成安装、NGINX TLS、可信 IP、重启/崩溃、升级回滚和隔离恢复（E63）；该环境仍不是外部云主机或正式域名/CA。
- 百度出口 IP 处于风控敏感状态，高频真实查询会触发验证码 → 业务准确性测试必须控速并批量回放历史样本。
