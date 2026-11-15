package sogou

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/xuboboo/web2api/core"
)

func fetchCounter(t *testing.T) (core.IndexCheckFetcher, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	fetch := func(ctx context.Context, wrapped string, q core.Query) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, errors.New("network disabled in tests")
	}
	return fetch, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// TestCheckIndexedQueryBuildsURLPhrase locks in the contract's query rule for
// sogou: the target URL itself is the query phrase with a depth of 10.
func TestCheckIndexedQueryBuildsURLPhrase(t *testing.T) {
	t.Parallel()

	target := "https://example.com/post/1"
	q := core.IndexCheckQuery(target, core.Query{})
	if q.Text != target || q.Limit != core.IndexCheckLimit || q.Start != 0 {
		t.Fatalf("query=%+v, want text=%s limit=%d start=0", q, target, core.IndexCheckLimit)
	}
	// Sogou resolves /link redirects in-engine; the depth must be unlocked.
	if !q.ResolveURLs || q.ResolveTop != core.IndexCheckLimit {
		t.Fatalf("resolve settings must unwrap all candidates, got %+v", q)
	}
}

// TestCheckIndexedRejectsEmptyTarget keeps the engine entry point honest.
func TestCheckIndexedRejectsEmptyTarget(t *testing.T) {
	t.Parallel()

	sog := &Sogou{}
	if _, err := sog.CheckIndexed(context.Background(), "", core.Query{}); !errors.Is(err, core.ErrEngineInternal) {
		t.Fatalf("expected ErrEngineInternal for empty target, got %v", err)
	}
}

// TestCheckIndexedImplementsIndexChecker is the compile-time interface guard.
func TestCheckIndexedImplementsIndexChecker(t *testing.T) {
	t.Parallel()

	var _ core.IndexChecker = (*Sogou)(nil)
}

// TestIndexVerdictFromFixtureSERP runs the platform matching over the real
// Sogou SERP fixture. The fixture's first row is a direct baike.sogou.com
// destination, so a case-folded target with a trailing slash hits exactly even
// though the SERP row carries query parameters (target has none). The other
// rows are /link?url= wrappers; a target that only matches after unwrapping
// hits through the (stubbed) redirect resolution. No network I/O.
func TestIndexVerdictFromFixtureSERP(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/search_results.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	results, err := ParseHTML(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseHTML() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("fixture produced no results")
	}
	if !strings.HasPrefix(results[0].URL, "https://baike.sogou.com/") {
		t.Fatalf("fixture row 0 should be a direct destination, got %q", results[0].URL)
	}
	hasWrapped := false
	for _, r := range results {
		if strings.Contains(r.URL, "sogou.com/link") {
			hasWrapped = true
		}
	}
	if !hasWrapped {
		t.Fatalf("fixture should contain /link wrappers: %+v", results)
	}

	// Direct hit: case-folded host + trailing slash; target has no query, so
	// the row's ch=/fromTitle= parameters are ignored per contract.
	hit := core.EvaluateIndexSERP(context.Background(), "HTTPS://BAIKE.sogou.com/v64548119.htm/", results,
		core.IndexResolveOptions{Fetch: func(ctx context.Context, wrapped string, q core.Query) (*http.Response, error) {
			t.Errorf("direct hit must not fetch %q", wrapped)
			return nil, errors.New("network disabled in tests")
		}})
	if !hit.Indexed || hit.Match != core.IndexMatchExact {
		t.Fatalf("expected exact hit against fixture, got %+v", hit)
	}
	if want := core.CountOrganicResults(results); hit.TotalResults != want {
		t.Fatalf("total_results=%d, want %d", hit.TotalResults, want)
	}

	// Wrapped hit: the target exists only behind a /link wrapper; the stub
	// fetch answers every wrapper with a redirect to it.
	const target = "https://example.org/article/"
	wrappedHit := core.EvaluateIndexSERP(context.Background(), target, results,
		core.IndexResolveOptions{Fetch: func(ctx context.Context, wrapped string, q core.Query) (*http.Response, error) {
			if !strings.Contains(wrapped, "sogou.com/link") {
				return nil, errors.New("unexpected wrapper host")
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{target}},
				Body:       http.NoBody,
			}, nil
		}})
	if !wrappedHit.Indexed || wrappedHit.Match != core.IndexMatchExact {
		t.Fatalf("expected wrapped-link hit, got %+v", wrappedHit)
	}
	if wrappedHit.MatchedResultURL != target {
		t.Fatalf("matched_result_url=%q, want %q", wrappedHit.MatchedResultURL, target)
	}

	// Miss: resolution fails (404), verdict stays none.
	miss, calls := fetchCounter(t)
	none := core.EvaluateIndexSERP(context.Background(), "http://absent.example.cn/never-crawled", results,
		core.IndexResolveOptions{Fetch: miss})
	if none.Indexed || none.Match != core.IndexMatchNone {
		t.Fatalf("expected none verdict, got %+v", none)
	}
	if calls() == 0 {
		t.Fatal("expected the wrapped candidates to be resolved on a miss")
	}
}
