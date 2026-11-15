package so360

import (
	"context"
	"fmt"
	"strings"

	"github.com/xuboboo/web2api/core"
)

// Compile-time proof that 360 Search participates in index checks.
var _ core.IndexChecker = (*So360)(nil)

// CheckIndexed implements core.IndexChecker: it looks targetURL up through
// 360 Search's own SERP pipeline — the URL itself is the query phrase,
// limit 10 — and returns the parsed candidates for core's normalization
// matching. The existing Search path is reused unchanged, so browser
// fingerprints, captcha gating, the plain-HTTP (SearchRaw) fallback, and the
// in-engine /link redirect resolution all stay in effect. runtime carries
// proxy/TLS settings selected by the resilience layer.
func (eng *So360) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}
	return eng.Search(ctx, core.IndexCheckQuery(targetURL, runtime))
}
