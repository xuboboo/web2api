# 低内存架构优化报告（2026-11-17）

## 目标
服务器仅 1.4G 内存，需在**不牺牲用户体验（结果准确性、反爬指纹一致性、响应速度）**前提下降低占用。

## 实测基线（优化前）
- Go 本体：空闲 ~23MB，请求时 ~25MB（不是瓶颈）。
- Chrome 进程树：单次搜索峰值 ~460MB WS，稳定 ~325MB；其中 browser ~162MB、GPU/SwiftShader ~115MB、2 个 renderer ~130-150MB。
- 8 并发峰值 ~555MB（百度限流导致 1 失败，非内存）。
- 浏览器空闲后被回收（默认 idle_ttl=5m），实测归零。

## 关键判断：哪些不能动
GPU/SwiftShader、1920×1080 模拟视口、headless=new 都是**反爬指纹的组成部分**：
- core/browser.go:309-311 注释明确：legacy headless 会破坏 WebGL；
- core/browser/profile.go:204-270 依据真实 WebGL 选择 swiftshader 指纹，不再伪造；
- profile_coherence_test.go 断言主线程/worker 的 WebGL vendor/renderer 一致。
实测把启动窗口 1920×1040 → 1024×768 也**无效**（462MB vs 460MB），因为内存由 CDP 模拟视口驱动。
因此：禁 GPU、改视口、--single-process、禁 JS、泄漏式清理等方案全部否决——会破坏指纹或改变搜索结果。

## 已实施的优化（均已实测验证）

### 1. idle_ttl 5m → 45s（config.yaml，零代码风险）
空闲 45s 即回收 Chrome，消除空闲间隙 ~325MB 的稳定态占用。
实测：请求前 0 个 Chrome → 请求中 5 个 → 空闲 75s 后 0 个。
代价：空闲后首个请求多一次 Chrome 冷启动（数百毫秒），流量持续时无感知。

### 2. max_processes 6 → 1（config.yaml）
每个不同代理会各起一个 Chrome（~325MB/个）。1.4G 机器限制为 1，避免多代理叠加爆内存。
无代理时所有请求共享 direct Chrome，此项无副作用。

### 3. 新增 max_concurrent_requests 并发闸门（默认 0=不限；本仓库配置为 2）
新增 core.ConcurrencyLimitMiddleware：在途搜索/收录请求超过上限时排队（受请求 deadline 约束，超时返回 503），
不修改任何引擎行为、指纹或结果。配置项 app.max_concurrent_requests。
实测 6 个并发请求：峰值从无限制时的 ~555MB 降到 **469MB**，且 6 个全部成功。
代码位置：core/middleware.go（中间件）、core/server.go（ServerOptions + 挂载）、
cmd/root.go（配置键/默认值/白名单）、cmd/serve.go（注入）。含单元测试 TestConcurrencyLimitMiddleware。

### 4. 主程序已重新编译
D:\web2api\web2api.exe 已于 2026-11-17 20:21 重建，包含上述全部改动。

## 验证
- go build ./... 通过；go test -p 1 ./... 全部通过。
- 优化后实测收录判定：onlinedown URL → baidu=true, bing=true(exact)，与优化前一致（无结果回归）。
- 空闲回收、并发峰值均按上述数据实测确认。

## 用户需要做的
- 这些改动尚未提交，请 review 后 commit。
- 1.4G 机器建议：保持 config.yaml 的新默认值（idle_ttl=45s、max_processes=1、max_concurrent_requests=2）；
  若前端能接受排队，可把 max_concurrent_requests 设为 1 以进一步压峰值（代价是并发串行）。
- 上线前在目标机器用真实流量观察 RSS 峰值，确认余量。
- 若后续必须加多代理（sogou/360 解封），每加 1 个代理约多 300MB，1.4G 下不建议超过 1 个。
