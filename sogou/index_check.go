package sogou

import (
	"context"
	"fmt"
	"strings"

	"github.com/xuboboo/web2api/core"
)

// Compile-time proof that Sogou participates in index checks.
var _ core.IndexChecker = (*Sogou)(nil)

// CheckIndexed implements core.IndexChecker: it looks targetURL up through
// Sogou's own SERP pipeline — the URL itself is the query phrase, limit 10 —
// and returns the parsed candidates for core's normalization matching.
// The existing Search path is reused unchanged, so browser fingerprints,
// captcha gating, the plain-HTTP (SearchRaw) fallback, and the in-engine
// /link redirect resolution all stay in effect. runtime carries proxy/TLS
// settings selected by the resilience layer.
func (sog *Sogou) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}
	return sog.Search(ctx, core.IndexCheckQuery(targetURL, runtime))
}
