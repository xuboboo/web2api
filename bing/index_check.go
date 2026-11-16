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
// Query form (2026-11 实测): the historical "url:<url>" operator has degraded
// on cn.bing.com — a live browser query for "url:https://github.com" returns
// unrelated pages (e.g. lzufe.edu.cn / ctrip), and 4/4 probe targets missed
// their own URL. Searching the bare URL keeps github.com/python.org at result
// #1 and recovered baidu.com too (bare domain hit). All other engines already
// search the bare target URL, so Bing now follows the same convention. The
// residual "PDF/深链接 not in top-10" limitation is unchanged and documented.
func (bing *Bing) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}
	return bing.Search(ctx, core.IndexCheckQuery(targetURL, runtime))
}
