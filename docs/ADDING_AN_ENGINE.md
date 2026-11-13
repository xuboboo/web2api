# 新增一个搜索引擎

以一个能跑通的最小 PR 为目标：**搜索 + 解析器测试 + 注册**。图片搜索、特性模块可以后续补。

## 1. 建包

以 `sogou/`、`so360/` 或 `baidu/` 为模板，一个完整引擎通常包含：

| 文件 | 作用 |
| --- | --- |
| `url.go` | 纯函数 URL 构造，拒绝空查询，处理翻页/过滤参数 |
| `selectors.go` | 选择器集中定义（改版时只改这里） |
| `parse_html.go` | `ParseHTML(io.Reader)`，纯函数、无网络，供测试与 `POST /{engine}/parse` 复用 |
| `search.go` | 浏览器通道：`Navigate` → 轮询 `page.HTML()` → `ParseHTML`，并做双通道编排 |
| `search_raw.go` | 纯 HTTP 通道（强烈建议实现，反爬兜底与 CLI raw 模式都依赖它） |
| `features.go` | 可选：SERP 特性模块提取 |
| `testdata/` + `*_test.go` | 真实 SERP 快照与表驱动单测 |

选择器优先级：稳定的数据属性（`data-mdurl`、`mu=`）> 语义类名 > 生成类名。脆弱处放 2–3 个候选，按顺序回退。

## 2. 实现双通道编排

参考 `baidu/search.go`、`sogou/search.go`、`so360/search.go` 的 `Search`：

```go
func (e *Engine) Search(ctx context.Context, q core.Query) ([]core.SearchResult, error) {
    rawQuery := q
    rawQuery.Insecure = q.Insecure || e.Browser.Insecure

    // 1) 浏览器通道近期被判过 → 先走 HTTP
    if e.gate.Blocked() {
        if raw, err := SearchRaw(ctx, rawQuery); err == nil && len(raw) > 0 {
            return raw, nil
        }
    }

    // 2) 浏览器通道
    results, err := e.searchBrowser(ctx, q)
    if err == nil {
        e.gate.Clear()
        return results, nil
    }

    // 3) 浏览器被反爬/解析错误 → Trip 门控并尝试 HTTP
    if errors.Is(err, core.ErrCaptcha) || errors.Is(err, core.ErrSearchTimeout) ||
        errors.Is(err, core.ErrBlocked) || errors.Is(err, core.ErrParser) {
        e.gate.Trip(0) // 默认 10 分钟
        if raw, rawErr := SearchRaw(ctx, rawQuery); rawErr == nil && len(raw) > 0 {
            return raw, nil
        }
    }
    return nil, err
}
```

要点：

- 结构体里放 `gate *core.CaptchaGate`（指针，避免拷贝锁）
- 浏览器通道的等待循环要处理“首屏之后才水合”的情况：轮询 + 必要时滚动，直到拿到目标条数或 DOM 稳定（参考 `minDomWait` 与 `stableFor`）
- 识别验证码页：优先看**落地 URL**（如 `wappass.baidu.com`、`qcaptcha.so.com`、`/antispider`、`/captcha`），再看页面选择器，返回 `core.ErrCaptcha`

## 3. 注册

- `cmd/engines.go` 加一行 `engineSpec`：`name`、`aliases`、`factory`、`rawSearchFn`（有 HTTP 通道时）、`parseHTMLFn`、`cfg`
- `cmd/root.go`：`Config` 结构体加字段；`rootsDefaults` 的 `engineSpecs()` 循环会自动补 `<engine>.disabled`
- `cmd/proxy_policy.go`：代理策略表加一行
- `core/response_builder.go`：引擎首页映射（用于补全显示 URL）
- `core/server.go`：仅当需要**特殊端点别名**时改 `engineEndpointName`/`resolveEngineAlias`（如 `so360` ↔ `360`）
- `config.yaml`：限速、`profile`、可选代理 tag
- 文档：根 `README.md` 的引擎表、`docs/openapi.yaml` 的引擎枚举、`docs/CONFIGURATION.md` 的引擎段

## 4. 测试

必须补：

- `url_test.go`：中文编码、site/filetype 折叠、翻页换算、非法入参
- `parse_html_test.go`：用 `testdata/` 的真实快照断言标题/URL/摘要、广告标记、排名连续、去重
- 验证码/无结果分支：构造最小 HTML 断言 `ErrCaptcha` / 空结果

测试必须离线可跑：

```bash
go test ./...
```

真实网络验证用 E2E：

```bash
node deploy/e2e-domestic.mjs http://127.0.0.1:7000
```

## 5. 提交前检查

```bash
gofmt -w <新引擎目录>
go vet ./...
go test ./...
docker compose -f deploy/docker-compose.yml up -d --build   # 真实链路验证
node deploy/e2e-domestic.mjs http://127.0.0.1:7000
```

PR 保持聚焦：不要顺手改共享的浏览器/服务端行为，除非新引擎不这么改就跑不通。
