# Web2API 上线准备复审

2026-11-26 以 `main` 的 `11f99b4` 为审计基线，对本机 Windows 实例交付并执行验收。独立 Docker 实例使用临时卷，执行密钥、冷却和登录锁定用例。正式域名只发起公开读取请求。以下结论来自本轮重跑。

## 发现与处理

| 编号 | 优先级 | 证据和影响 | 处理 |
| --- | --- | --- | --- |
| R1 | P1，公网发布 | `https://radar.ydsdoa.cn/web-tool/health`、`/ready`、独立文档、OpenAPI 和 `/v1/engines` 同时返回 HTTP 200 和资讯雷达系统 HTML。只查状态码会误报健康。 | 本轮本机验收通过。正式域名仍需将 `/web-tool` 路由到 Web2API。[反代配置](../DEPLOYMENT.md#5-反向代理tls-终结) |
| R2 | P1，发布验收 | 原脚本在公开 URL 上也会创建密钥、修改引擎状态和触发管理员锁定。原断言将 21 条认作越过批量上限，并假定 `/stats` 可匿名读取。 | 默认改为只读校验。独立实例显式开启完整契约，按默认 5000 条上限与现行鉴权语义断言，清理临时密钥。Docker CI 运行该契约。 |
| R3 | P2，浏览器 | Windows Chrome 中 390px 页面滚动宽度达 683px。独立文档内有控制台路由仅改变当前页的片段，部分资料链接指向正式域名。 | 移动端表格与区块标题可折行。Agent 链接进入控制台，规范与说明文件从当前实例打开。 |
| R4 | P2，运维 | 数据面开启鉴权时，稳态观测脚本请求未携带密钥。 | 通过 `WEB2API_API_KEY` 传递测试密钥，添加超时并禁止自动跟随重定向。具有鉴权校验的本机 HTTP 桩通过 1/1。 |
| R5 | P2，交付 | 发布归档缺少许可证副本和校验清单，容器缺少许可证副本。 | 归档加入 `LICENSE` 和一同上传的 `SHA256SUMS`，镜像包含 `LICENSE` 并由 CI 检查。 |
| R6 | P2，回归 | 仅校验接口转发的单测隐式启动 Chromium。在无系统浏览器的 Go 测试容器中触发 Rod 下载，离线回归依赖外部下载和本机浏览器状态。 | 在单测预置浏览器池条目，由已有集成测试验证真实的启动和崩溃恢复。 |
| R7 | P2，镜像依赖 | 原镜像固定的浏览器为 Chromium 152。供应方标记的 `stable` 已更新。 | 固定到供应方 amd64 / arm64 多架构摘要，实测浏览器版本为 159.0.8154.13，并由 Docker CI 验证容器内的浏览器可执行。[供应方镜像](https://hub.docker.com/r/chromedp/headless-shell/tags) |

## 本机证据

| 验证项 | 本轮结果 |
| --- | --- |
| Windows 部署 | 沿用 `127.0.0.1:7000`、原配置和 bbolt。部署后二进制与候选文件 SHA-256 相同，配置哈希相同。健康与就绪返回 200，匿名指标返回 401，调试路径返回 404。只读探针 2/2。 |
| 数据恢复 | 从运行中的 Windows 实例取得一致性 bbolt 快照，使用候选二进制恢复到独立目录。原二进制和原前端留有部署时间戳备份。Docker 完整归档也在隔离目录恢复，核对了数据库、配置、签名密钥和任务目录。 |
| 浏览器 | Windows Chrome 157 / Edge 158 合计两个浏览器，均通过 320px、390px、1440px 的官网、状态页、文档和登录页布局验证。通过导航、移动菜单、主题持久化、404、登录、Agent 链接和登出检查。 |
| 浏览器进程恢复 | Linux 容器使用候选镜像同源的 Chromium，在断网环境实测启动及崩溃恢复，6 个恢复场景全部通过。 |
| REST 和 MCP | Linux 临时容器独立契约 31/31，覆盖六个 MCP 工具、密钥生命周期、数据面 401、批量参数边界、冷却状态和管理员锁定。 |
| 真机实网业务 | Windows 实例通过 Bing 查询 `https://www.onlinedown.net/`，返回 `indexed=true`、`match=exact`、1 个计费单元。测试密钥已撤销。 |
| 镜像与归档 | 候选镜像以非 root 用户启动，初始数据目录为空，内含许可证。Linux、Windows 和 macOS 共八个架构目标全部构建并归档，八份 SHA-256 校验均通过。 |
| 静态与依赖 | `go vet` 和 `go mod verify` 通过。`govulncheck` 报告 0 个可达漏洞，另有 1 条依赖模块公告涉及未调用的代码。 |
| Go 竞态回归 | `go test -race -p 2 -count=1 ./...` 通过全部 11 个有测试的包，带 `integration` 标签的编译也通过。 |
| 万条账本 | Docker 磁盘上逐条同步写入，造数阶段在 150 秒时超时。以 tmpfs 重跑万条账本通过，验证查询功能。生产持久盘的写入吞吐仍待实测。 |
| Compose 与脚本 | 两份 Compose 配置通过 `config --quiet`，前端 JS 和验收脚本通过 `node --check`。 |
| 同域反代 | 在本机 NGINX 容器验证 `/` 仍由另一应用响应，`/web-tool/health` 为 Web2API bbolt JSON，OpenAPI 原始 YAML 可获取。反代保留原请求路径。 [NGINX `proxy_pass` 文档](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_pass) |

公开请求的实际路径为 域名 TLS → 当前资讯雷达站点 → HTML。验收目标的实际路径为 浏览器 → Windows 回环地址 → Web2API 静态页面或 JSON 路由。可用以下命令从同一工作区重跑：

```powershell
node deploy/e2e.mjs http://127.0.0.1:7000
node deploy/e2e.mjs https://radar.ydsdoa.cn
```

## 上线边界

本机通过范围见上表。正式发布仍须确认域名接入、生产监控与告警、异地备份保留、生产出口的真实引擎容量及 SLA，按 [上线检查清单](../RELEASE_CHECKLIST.md) 逐项验收。
