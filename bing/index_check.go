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
// Bing's own SERP pipeline with the "url:<url>" query operator, limit 10, and
// returns the parsed candidates for core's normalization matching. The
// existing Search path is reused unchanged, so browser fingerprints and
// captcha handling stay in effect. runtime carries proxy/TLS settings
// selected by the resilience layer.
func (bing *Bing) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}
	return bing.Search(ctx, core.IndexCheckQuery("url:"+targetURL, runtime))
}
