package bing

import (
	"context"
	"fmt"
	"strings"

	"github.com/xuboboo/web2api/core"
)

// Compile-time proof that Bing participates in index checks.
var _ core.IndexChecker = (*Bing)(nil)

// CheckIndexed implements core.IndexChecker: it looks targetURL up through
// Bing's own SERP pipeline, limit 10, and returns the parsed candidates for
// core's normalization matching. The existing Search path is reused unchanged,
// so browser fingerprints and captcha handling stay in effect. runtime carries
// proxy/TLS settings selected by the resilience layer.
//
// Query form (2026-11 实测,十轮真实网站对照):
//   - 历史 "url:<url>" 操作符在 cn.bing.com 已失效(4/4 miss,返回无关页),弃用;
//   - 带 scheme 的完整 URL 亦不稳: "https://go.dev" 被 Bing 当成 "https 是什么",
//     命中百度百科;10 站对照 "完整 URL 命中 4/10 vs 去 scheme 命中 7/10"。
//   - 故查询词去掉 scheme 前缀(保留 host+path),匹配仍由 core 用原 URL 归一化,
//     两者不冲突:查询词只用于 Bing 检索,候选匹配用原始 targetURL。
// 其它引擎用完整 URL 正常,仅 Bing 需要去 scheme。深链接前 10 条漏报属已知约束。
func (bing *Bing) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}
	return bing.Search(ctx, core.IndexCheckQuery(bingIndexQuery(targetURL), runtime))
}

// bingIndexQuery strips the URL scheme for the SERP query phrase. Bing treats
// "https://" as a keyword on some hosts (e.g. https://go.dev -> "https" baike
// article), while the bare host+path resolves to the site's own result. The
// returned candidates are still matched against the original targetURL by core.
func bingIndexQuery(targetURL string) string {
	if i := strings.Index(targetURL, "://"); i >= 0 {
		return targetURL[i+3:]
	}
	return targetURL
}
