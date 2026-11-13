# Contributing

## 快速路径

1. 选一个聚焦的问题（一个 bug、一个解析器修复、一篇文档或一个引擎特性）。
2. 保持 PR 范围小：不要顺手重构共享的浏览器/服务端/响应结构。
3. 提交前跑通：`gofmt`、`go vet ./...`、`go test ./...`，涉及真实链路的改动再跑一遍 E2E。
4. PR 说明里写清“改了什么、为什么、怎么验证的”。

新增引擎见 [ADDING_AN_ENGINE.md](ADDING_AN_ENGINE.md)。

## 开发环境

- Go 1.24+
- Chromium/Chrome（仅浏览器模式与 E2E 需要；容器部署已自带）
- 可选：Docker（推荐，真实链路更接近线上）

```bash
git clone https://github.com/xuboboo/web2api.git
cd web2api
go build -o web2api .
./web2api search baidu "golang" --format markdown    # CLI，无需启动服务
./web2api serve -a 127.0.0.1 -p 7000                 # HTTP 服务
```

> 注意：仓库 `config.yaml` 默认把 `server.port` 设为 7000；不带 `-c config.yaml` 时使用内置默认 7070。

## 测试

```bash
go test ./...                                  # 单测，无需网络/浏览器
docker compose -f deploy/docker-compose.yml up -d --build
node deploy/e2e-domestic.mjs http://127.0.0.1:7000
node deploy/steady-check.mjs http://127.0.0.1:7000 3 20000
```

集成测试由 `testutil.RequireIntegration(t)` 控制（默认跳过）：

```bash
OPENSERP_INTEGRATION_TESTS=1 go test -tags=integration ./...
```

约定：

- 不要在 `init()` 或包级变量里创建浏览器实例。
- 解析器测试用 `testdata/` 的真实快照，不要访问网络。
- 选择器统一放在引擎的 `selectors.go`，脆弱处给 2–3 个候选。

## 提交 PR

```bash
gofmt -w <改动目录>
go vet ./...
go test ./...
```

文档改动请同步检查：

- 根 `README.md`（对外说明）
- `docs/CONFIGURATION.md`（新增配置项）
- `docs/TESTING.md`（新增/调整用例）
- `docs/openapi.yaml`（端点或参数变化）

## 项目约定

- 引擎错误使用 `core` 中的哨兵错误（`ErrCaptcha`、`ErrBlocked`、`ErrRateLimited`、`ErrSearchTimeout`、`ErrParser`、`ErrNotImplemented` …），由服务端统一映射成 HTTP 状态与机器码。
- 国内引擎保持“浏览器通道 + HTTP 通道 + `CaptchaGate`”的编排，新增逻辑不要绕开它。
- 修改 `config.yaml` 后，容器需要 `--force-recreate` 才能生效（挂载的是文件）。
