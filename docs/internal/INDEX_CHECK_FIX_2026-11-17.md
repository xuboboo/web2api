# 收录查询精准度修复报告 (2026-11-17)

## 用户反馈
- 测试 URL: https://www.onlinedown.net/article/10047660.htm
- 百度稳定可用，但搜狗/必应/360 查询不准

## 复现结果（修复前，实测 API）
- baidu: indexed=true, exact ✓
- bing: indexed=false（返回 10 条无关结果，漏报）
- sogou/so360: captcha_detected

## 根因与修复

### 1. Bing 漏报 —— 已修复（核心 bug）
根因：bing/index_check.go 的 bingIndexQuery() 去掉 URL scheme。
- 去 scheme 的 "www.onlinedown.net/article/..." 被 Bing 当成关键词 "www 是什么"，返回百科/无关页 → 漏报；
- 带 scheme 的完整 URL 对该 URL 恰好命中 rank 1，但对 go.dev 等短域名又会命中 "https 是什么" 百科 → 单一形式均不可靠。

修复：
- 新增多查询形式回退 + 早停：优先带引号的完整 URL（10/10 命中，严格优于其它形式），
  其次完整 URL、去 scheme 形式；任一 SERP 含目标 URL 精确候选即早停。
- 全部未命中时返回所有候选去重并集，保留 core 的 wrapped-link 解析与 total_results。
- 新增 core.IndexSERPContains() 导出助手供引擎做离线精确判定（补单元测试）。

实测验证（修复后）：
- onlinedown URL: bing indexed=true, exact ✓
- go.dev: bing indexed=true, exact ✓
- www.python.org: bing indexed=true, exact ✓

### 2. Sogou/360 captcha_detected —— 真实 IP 级反爬，非代码 bug
证据：
- 服务器日志显示真实上游跳转到 qcaptcha.so.com / sogou.com/captcha；
- 同机直接 HTTP 探测：连 "golang tutorial" 这种无害查询都 302 到 captcha；
- SearchRaw 回退确实尝试了，但 raw 客户端不跟随跳转，且 302 被 ClassifySearchHTTPStatus 
  误判为 parser_failure（应为 captcha_detected）；
- 即便跟随跳转，下一跳就是 captcha 页 —— 必须从当前出口 IP 之外访问。

代码层面真实缺陷 —— 已修复：
- ReadRawSearchBody() 现在先检查 3xx 的 Location 是否指向 captcha/verify/antispider/
  wappass 等反爬端点，命中返回 ErrCaptcha（补单元测试）；
- sogou/360 Search() 的 raw 回退失败新增 Debug 日志，便于运维区分 captcha 与解析漂移。

运维建议（非代码，需用户配置）：
- 为 sogou/so360 配置 2+ 代理池（proxies.entries + per-engine proxy 标签）实现出口 IP 轮换；
- 或在 captcha 配置中启用 2captcha 自动解题。
- 当前出口 IP 已被 sogou/360 硬封，配置代理前这两个引擎仍会返回 captcha_detected（属正确行为）。

### 3. 百度
无需改动，实测持续稳定。

## 改动文件
- core/index_check.go / test: 导出 IndexSERPContains 助手
- bing/index_check.go / test: 多查询形式回退 + 早停 + 并集
- core/http_client.go / test: captcha 重定向正确分类为 ErrCaptcha
- sogou/search.go, so360/search.go: raw 回退失败日志

## 验证
- go build ./... 通过
- go test -p 1 ./... 全部通过
- 最终实测（服务器 127.0.0.1:7703）：
  * onlinedown URL: baidu=true, bing=true (exact)
  * go.dev: bing=true (exact)
