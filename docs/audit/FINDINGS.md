# 上线审计：问题清单（滚动更新）

分级：**P0** 致命（必须立即修）· **P1** 高危（上线前必修）· **P2** 中危（建议上线前）· **P3** 低危（可迭代）

状态：`已修复` / `已确认待修` / `待复测` / `环境受限` / `已接受`

| ID | 级别 | 标题 | 状态 |
| --- | --- | --- | --- |
| F-01 | P1 | 收录查询的标题探测是 SSRF：`GuardPrivateNetworks` 在生产路径从未开启 | 已修复 |
| F-02 | P1 | Docker 构建上下文泄露运行态密钥（`data/secret.key`、`web2api.db`）且体积 772MB | 已修复 |
| F-09 | P1 | 异步收录任务越权：任意 API Key 可读/列/取消其他 Key 的任务 | 已修复 |
| F-03 | P2 | `docker-compose.yaml` 注释宣称「API 无鉴权层」，与实际（API Key + JWT）相反 | 已修复 |
| F-04 | P2 | CI 的 golangci-lint 版本无法 lint go1.26 模块（v1.64.8 由 go1.24 构建） | 已确认待修 |
| F-05 | P3 | 工作区残留 531MB 可执行文件 + 146MB QA 目录 | 已确认待修 |
| F-06 | P2 | 登录锁定仅按 (用户名, IP) 且纯内存，换 IP 可绕过、多实例失效 | 已确认待评估 |
| F-07 | P3 | 本机无 C 工具链，`go test -race` 无法执行（CI 有覆盖） | 环境受限 |
| F-08 | P3 | `go.mod` go1.26.0 / 开发工具链 1.27.4 / 镜像 1.26.11 三者不一致 | 待验证 |
| F-10 | P3 | 管理员（JWT）没有查询异步任务的接口，任务只有持 key 方可见 | 已确认 |

---

## F-09 异步收录任务越权（P1，已修复）

**证据（修复前，实机复现）**
1. 用 key A 提交 21 条 URL → `202 job_id=job_...d5f7dca9c1`。
2. 用**另一个** key B 直接请求：
   - `GET /v1/index/check/jobs/{id}` → **200**，返回 A 的任务进度
   - `GET /v1/index/check/jobs/{id}/results` → **200**，读到 A 的结果集
   - `GET /v1/index/check/jobs` → `count=1`（A 的任务出现在 B 的列表里）
   - `DELETE /v1/index/check/jobs/{id}` → **200**，`status=cancelled`（B 取消了 A 正在跑的任务）

**影响**：这是收费多租户平台里的水平越权（IDOR）：
- 数据泄露：租户提交的 URL 列表与收录结论属于其业务数据；
- 拒绝服务：任意 key 可取消他人批量任务，且列表接口暴露全局任务量；
- 计费纠纷：被取消的任务已按提交量扣费。

**根因**：任务模型只存了"任务"，没有存"属主"。`IndexJob` 没有 owner 字段，`Get/List/Cancel` 也没有调用方身份参数——鉴权只验证了"是不是合法 key"，没有验证"是不是你的任务"。

**修复**
1. `IndexJob` 新增持久化字段 `OwnerKeyID`（写盘，重启后仍生效）。
2. 新增 `core/index_job_owner.go`：`WithJobOwner(ctx, keyID)` / `JobOwner(ctx)` / `jobVisibleTo()`；约定"空 owner = 管理员（JWT），可见全部"。
3. 平台两处鉴权成功后把 key 绑定进请求上下文：
   - `platform/middleware.go` `APIKeyGuard`（/v1 数据面）
   - `platform/security.go` `AuthAny`（MCP 平面，`check_indexed` 提交任务走这里）
4. 所有访问口改为按属主过滤：`Get(id, owner)`、`List(owner, limit)`、`Cancel(id, owner)`；`Submit(ctx, …)` 从 ctx 取属主。外部访问他人任务统一返回 **404 not_found**（而不是 403），避免成为"任务是否存在"的探测口。

**验证**
- 单测 `TestIndexJobIsScopedToOwner`：属主可见、他人 not-found、他人无法取消、他人列表为 0、管理员可见全部。
- 实机复测（同一套 curl）：

| 操作 | 修复前 | 修复后 |
| --- | --- | --- |
| B 读 A 的任务状态 | 200 | **404** |
| B 读 A 的结果 | 200 | **404** |
| B 取消 A 的任务 | 200 cancelled | **404** |
| B 的任务列表 | 1（含 A 的） | **0** |
| A 读自己的 | 200 | 200 |
| A 取消自己的 | 200 | 200 |

**回归**：`go test -count=1 ./...` 全绿（见本轮末尾输出）。

**兼容性**：≤20 条同步接口、计费口径、任务 API 形状都没变；只是把"不属于你的任务"从 200 变成 404。对单一 key 的自用部署零影响。

**回滚**：去掉 `OwnerKeyID` 过滤即可，数据字段向后兼容（老任务 owner 为空 = 管理员任务）。

---

## F-10 管理员看不到异步任务（P3，已确认）

`/v1/index/check/jobs*` 只接受 API Key；管理面 22 个端点里没有任务列表。运维/客服排查"某个任务为什么卡住"目前只能拿用户 key 或看日志/磁盘目录。
建议（未实现）：在管理面加只读 `GET /api/index/jobs`（JWT），复用 `m.jobs.List("", limit)`（空 owner = 管理员，已支持）。

---

## F-01 收录查询标题探测的 SSRF（P1，已修复）

**证据**
- `core/title_fetch.go:125` `fetchPageTitle` → `RawSearchRequest(ctx, targetURL, q)`，`targetURL` 直接来自 API 调用方（`/v1/index/check` 的 `urls[]`、MCP `check_indexed`、CLI）。
- 代码里**存在**完整的私有网段防护：`core/network_guard.go` 的 `GuardPrivateNetworks` / `GuardedDialContext` / `ValidatePublicHTTPURL`（拒绝 loopback、RFC1918、link-local、CGNAT、DNS rebinding，并对每次重定向复核）。
- 但 `grep GuardPrivateNetworks` 显示：**除定义与测试外，生产代码从未把它置 true**（`core/common.go:316` 只是字段声明）。防护是"休眠"的。

**影响**
持有任意有效 API Key 的调用方可以让服务器发起任意 GET：`http://127.0.0.1:7000/web-tool/api/*`（自身管理面）、`http://169.254.169.254/latest/meta-data/`（云元数据）、内网主机与端口。虽然响应体只用于提取 `<title>` 并最终只回吐"是否收录"的判定，但仍是服务器侧请求伪造（内网探测/盲打带副作用的 GET 接口/日志污染），且属于越权访问基础设施。等级定为 P1 而非 P0：不是直接数据读取原语，但违反"服务端不得替用户访问内网"的底线。

**根因**：安全能力做成了"默认关闭的开关"，而唯一需要它的调用点没有打开。默认值方向与信任边界相反。

**修复**
```go
// core/title_fetch.go
q.GuardPrivateNetworks = true   // 调用方给的 URL 一律走私有网段防护
```
放在 `fetchPageTitle` 内（唯一出网口），而不是放在 baidu 引擎里——这样任何现在或将来接入标题探测的引擎都自动受保护。

**验证**
- 新增 `TestFetchPageTitleRejectsPrivateTargets`：起一个 loopback `httptest` 服务器，断言 `FetchPageTitle` 对 `127.0.0.1`、`169.254.169.254`、`10.0.0.1`、`[::1]` 全部返回 `ok=false`，且**服务器一次都没有被访问**（`hits == 0`），证明是在 dial 阶段被拦，不是靠超时。
- `go test ./core/ -run Title` 全绿。

**回滚**：单行删除即可回滚；影响面仅"私有地址不再被抓标题"，公开站点判定不受影响。

**残留**：私有地址被拦后回退到 URL 短语检索，仍会给出判定（通常为未收录）。需在 API.md 说明该行为，避免用户把"内网 URL 查不到"当 bug。

---

## F-02 构建上下文泄露运行态密钥（P1，已修复）

**证据**
- 工作区 `.dockerignore` 原内容只排除了 `.git`、`*.md`、`web2api`、`web2api.exe`、缓存目录。
- 工作区实际存在：`data/secret.key`（64 字节 = JWT 签名密钥）、`data/web2api.db`（128KB，含 API key 哈希、用量账本、管理员 bcrypt 哈希）、`data/audit.log`、`logs.txt`（1.8MB）。
- `Dockerfile` 构建阶段执行 `COPY . .` → 上述文件进入构建层；配合 `docker build --target builder` 或 registry 缓存导出（`--cache-to`）即可被取出。
- 上下文体积：实测工作区（除 `.git`）**772MB**，其中 `*.exe` 18 个共 **531MB**、QA/数据目录 812 个文件 **146MB**。

**影响**
1. 安全：JWT 签名密钥与 API key 哈希进入镜像构建缓存；任何能拉取 builder 层或缓存的人可伪造管理员 JWT。
2. 可用性：700MB+ 的构建上下文让每次 `docker build` 传输/扫描变慢，CI 与低配服务器上容易超时。

**修复**：重写 `.dockerignore`，加入 `data/`、`data-*/`、`qa/`、`deploy/e2e-results/`、`logs*.txt`、`server-*.log`、`config-qa*.yaml`、`config-audit*.yaml`、`*.exe`、`*.exe~`、`docs/`、`.zcode/`。

**验证**：用同一套 pattern 规则对工作区做静态计算 → **772MB → 2MB**（见审计命令输出）。

**残留**：Docker daemon 未运行，镜像构建未实机验证（见 F-07 同类限制）；建议 CI 里加一步 `docker build` 并断言镜像内不含 `data/`。

---

## F-03 compose 注释与鉴权现实相反（P2，已修复）

`docker-compose.yaml` 原文："Loopback only: the API has no auth layer, never expose it beyond localhost."
实际：数据面强制 `X-API-Key`（`security.data_plane_auth` 默认 true），管理面强制 JWT。
这类注释会误导运维：要么以为有鉴权可以随便暴露，要么以为无鉴权而放弃部署。已改为准确表述（明文 HTTP + 凭据风险 + 需前置 TLS 反代）。

---

## F-04 CI lint 无法运行（P2，已确认待修）

**证据**：`.github/workflows/ci.yml` 固定 `golangci/golangci-lint-action@v6` + `version: v1.64.8`；`go.mod` 声明 `go 1.26.0`；`.golangci.yml` 使用 v1 配置格式（`linters.disable-all`、`run.go`）。
**依据**：golangci-lint 拒绝 lint 目标 Go 版本高于自身构建版本的模块，Go 1.26 支持从 v2.9.0 起（[issue #6272](https://github.com/golangci/golangci-lint/issues/6272)、[FAQ](https://golangci-lint.run/docs/welcome/faq/)）。
**影响**：lint gate 实际失效（要么报版本错误、要么整步失败），代码质量门禁形同虚设。不影响运行中的服务。
**修复方案**（未执行，需联网安装 v2 二进制验证）：CI 升 action 到 v8 + `version: v2.13.2`；`.golangci.yml` 迁移为 v2 格式（`version: "2"`、`linters.default: none`）。
**本次尝试**：`go install ...@v2.13.2` 因 sumdb/网络中断失败两次，未完成本地验证 → 不写入未验证的配置。

---

## F-06 登录锁定可被换 IP 绕过（P2，待评估）

**证据**：`platform/security.go:245-289`，锁定键为 `loginAttemptKey{username, ip}`，状态存在进程内 map。
**影响**：单管理员 + 单实例部署下，攻击者用代理池对同一用户名可继续尝试（每 IP 5 次/15 分钟）。若未来多实例，锁定完全失效。
**现状缓解**：密码为 bcrypt(DefaultCost)，登录路径做了时延对齐（未知用户也跑一次 bcrypt），并有审计与告警日志。
**待决策**：是否加"按用户名全局计数"（会引入对管理员自身的 DoS 风险）或"失败 N 次后强制冷却+验证码"。需产品取舍，未擅自实现。

---

## F-08 三处 Go 版本不一致（P3，待验证）

`go.mod` = `go 1.26.0`；本机工具链 = **go1.27.4**；`Dockerfile` = `golang:1.26.11`；CI = `go-version: "1.26"`。
风险：开发机上用 1.27 特性写代码 → CI/镜像 1.26 编译失败。当前 `go vet`/`go test` 通过只证明 1.27 能编译。
**验证方案**：CI 已用 1.26，等价覆盖；本地可通过 `GOTOOLCHAIN=go1.26.11 go build` 复现（需下载工具链，网络受限未执行）。
