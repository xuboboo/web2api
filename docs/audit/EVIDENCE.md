# 上线审计：实机验证证据

记录方式：命令 + 实际输出摘录 + 时间。所有针对接口的验证都在**独立数据目录**（`data-audit-r1`）与**独立端口 7077** 上完成，未触碰生产数据；临时配置、二进制、数据目录在验证后已删除。

---

## E1 鉴权基线（未携带凭据时的行为）

```
health            : 200      # 公开探活
v1 no key         : 401      # POST /v1/index/check
v1 bad key        : 401      # 伪造 X-API-Key
jobs no key       : 401      # GET /v1/index/check/jobs
quota no key      : 401
admin keys no jwt : 401      # GET /api/keys
admin stats no jwt: 401
mcp no key        : 401      # POST /mcp tools/list
metrics (loopback): 200      # 空 metrics_token 时仅本机
stats (no key)    : 401      # data_plane_auth=true 生效
```

结论：数据面、管理面、MCP 面三处入口都强制鉴权；`/stats/*` 的默认安全姿态（需 key）实际生效。

## E2 静态资源目录穿越

```
/web-tool/console/../config.yaml        : 404
/web-tool/console/%2e%2e%2fconfig.yaml  : 404
/web-tool/config.yaml                   : 404
/web-tool/console/../../server.go       : 404
```

结论：`config.yaml`（含管理员密码）与源码均无法通过静态路由取出；Fiber 的路径规范化 + 目录限定有效。

## E3 配额与请求体上限

| 场景 | 结果 |
| --- | --- |
| key `daily_quota=1`，第 1 次单 URL 查询 | 200 |
| 同 key 第 2 次 | **429**（`quota_exceeded`） |
| `urls` 5001 条（0.14MB） | **400** `TOO_MANY_URLS: urls accepts at most 5000 entries per job, got 5001` |
| `urls` 200000 条（5.81MB） | **400** 同上（`got 200000`） |
| 上述请求后 `/health` | 200（进程未受影响） |

配置 `app.max_concurrent_requests: 2`、`BodyLimit=10MB`、`ReadTimeout=60s`、`WriteTimeout` 由批量预算推导、`IdleTimeout=120s`。

## E4 异步任务越权（F-09 修复前 → 修复后）

创建 key A 提交 21 条 → `202 job_20261124T071313_d5f7dca9c1`；用 key B 访问：

| 操作 | 修复前 | 修复后 |
| --- | --- | --- |
| B `GET /v1/index/check/jobs/{A}` | 200（读到进度） | **404** |
| B `GET …/{A}/results` | 200（读到结果） | **404** |
| B `DELETE …/{A}` | 200 `cancelled` | **404** |
| B `GET /v1/index/check/jobs` | `count=1` | **0** |
| A 读自己 / 取消自己 | 200 / 200 | 200 / 200 |

## E5 构建上下文（F-02）

静态规则重放（同一套 `.dockerignore` pattern 作用于工作区）：

```
before: 772 MB   after: 2 MB   saved: 770 MB
```

`data/` 内含 `secret.key`(64B)、`web2api.db`(128KB)、`audit.log`，均不再进入构建上下文。

## E6 测试与静态检查基线

```
go vet ./...                                 无输出（通过）
go test -p 1 -count=1 ./...                  全部 ok（baidu/bing/cmd/core/core/*/mcp/platform/so360/sogou）
go build -trimpath -ldflags="-s -w" (linux/amd64, CGO_ENABLED=0)  → 22.3 MB 静态二进制
go test -race ./...                          本机不可执行：无 gcc/clang（CI 的 ubuntu job 覆盖）
```

并行跑全量测试时出现过一次 `VirtualAlloc failed errno=1455`（Windows 提交内存耗尽：27.2/30.3 GB，本机另有 67 个 Chrome 进程），改为 `-p 1` 后连续两次全绿 —— 属本机资源限制，非代码缺陷。
