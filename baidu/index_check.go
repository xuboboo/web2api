package baidu

import (
	"context"
	"fmt"
	"strings"

	"github.com/xuboboo/web2api/core"
)

// Compile-time proof that Baidu participates in index checks.
var _ core.IndexChecker = (*Baidu)(nil)

// Compile-time proof that Baidu advertises the title-phrase probe.
var _ core.TitleProbeEngine = (*Baidu)(nil)

// IndexUsesTitleProbe reports whether index checks query the page title.
func (baid *Baidu) IndexUsesTitleProbe() bool {
	return baid.IndexTitleProbe
}

// CheckIndexed implements core.IndexChecker: it looks targetURL up through
// Baidu's own SERP pipeline and returns the parsed candidates for core's
// matching.
//
// With index_title_probe enabled (the default), the query phrase is the target
// page's <title> fetched over the plain-HTTP channel. Baidu returns an empty
// SERP for "wd=<article URL>" even when the article is indexed (measured on
// 767 real pages), while the title phrasing surfaces it — so the URL phrase is
// kept only as the fallback when the page title cannot be read.
//
// The existing Search path is reused unchanged, so browser fingerprints,
// captcha gating, and the plain-HTTP (SearchRaw) fallback all stay in effect.
// runtime carries proxy/TLS settings selected by the resilience layer.
func (baid *Baidu) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}

	if baid.IndexTitleProbe {
		if title, ok := core.FetchPageTitle(ctx, targetURL, runtime); ok {
			// An empty slice is a verdict ("百度没有这条结果"), not a failure;
			// errors (captcha/block/timeout) stay errors and are never retried
			// here by spending a second Baidu request on the URL phrase.
			return baid.Search(ctx, core.IndexCheckQuery(title, runtime))
		}
		baid.logger.Debug("No page title for %s; falling back to the URL-phrase index check", targetURL)
	}
	return baid.Search(ctx, core.IndexCheckQuery(targetURL, runtime))
}
