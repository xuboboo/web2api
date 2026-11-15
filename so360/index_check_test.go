package so360

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
// 360: the target URL itself is the query phrase with a depth of 10.
func TestCheckIndexedQueryBuildsURLPhrase(t *testing.T) {
	t.Parallel()

	target := "https://example.com/post/1"
	q := core.IndexCheckQuery(target, core.Query{})
	if q.Text != target || q.Limit != core.IndexCheckLimit || q.Start != 0 {
		t.Fatalf("query=%+v, want text=%s limit=%d start=0", q, target, core.IndexCheckLimit)
	}
	// 360 resolves /link redirects in-engine; the depth must be unlocked.
	if !q.ResolveURLs || q.ResolveTop != core.IndexCheckLimit {
		t.Fatalf("resolve settings must unwrap all candidates, got %+v", q)
	}
}

// TestCheckIndexedRejectsEmptyTarget keeps the engine entry point honest.
func TestCheckIndexedRejectsEmptyTarget(t *testing.T) {
	t.Parallel()

	eng := &So360{}
	if _, err := eng.CheckIndexed(context.Background(), "", core.Query{}); !errors.Is(err, core.ErrEngineInternal) {
		t.Fatalf("expected ErrEngineInternal for empty target, got %v", err)
	}
}

// TestCheckIndexedImplementsIndexChecker is the compile-time interface guard.
func TestCheckIndexedImplementsIndexChecker(t *testing.T) {
	t.Parallel()

	var _ core.IndexChecker = (*So360)(nil)
}

// TestIndexVerdictFromFixtureSERP runs the platform matching over the real
// 360 SERP fixture. Every fixture row carries a data-mdurl destination, so the
// parser hands us direct URLs (baike.so.com, golang.google.cn, ...) that hit
// after case-fold/trailing-slash normalization. The wrapped-link path is
// exercised separately below with a synthetic /link?m= row, matching SERPs
// that omit data-mdurl. No network I/O.
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

	// Direct hit on the first usable destination row.
	var direct string
	for _, r := range results {
		if r.Ad || r.URL == "" {
			continue
		}
		if strings.Contains(r.URL, "so.com/link") {
			t.Fatalf("fixture parse should prefer data-mdurl over %q", r.URL)
		}
		if direct == "" && core.IsUsableResultURL(r.URL) {
			direct = r.URL
		}
	}
	if direct == "" {
		t.Fatalf("fixture has no usable direct destination: %+v", results)
	}

	// Uppercase scheme+host (path case preserved) and add a trailing slash;
	// normalization must fold all of that away.
	rest := direct
	scheme := ""
	if i := strings.Index(rest, "://"); i >= 0 {
		scheme = strings.ToUpper(rest[:i+3])
		rest = rest[i+3:]
	}
	host := rest
	path := "/"
	if j := strings.Index(rest, "/"); j >= 0 {
		host = rest[:j]
		path = rest[j:]
	}
	target := scheme + strings.ToUpper(host) + path + "/"

	hit := core.EvaluateIndexSERP(context.Background(), target, results,
		core.IndexResolveOptions{Fetch: func(ctx context.Context, wrapped string, q core.Query) (*http.Response, error) {
			t.Errorf("direct hit must not fetch %q", wrapped)
			return nil, errors.New("network disabled in tests")
		}})
	if !hit.Indexed || hit.Match != core.IndexMatchExact {
		t.Fatalf("expected exact hit for %q against fixture, got %+v", target, hit)
	}
	if hit.MatchedResultURL == "" {
		t.Fatal("matched_result_url must be recorded on a hit")
	}
	if want := core.CountOrganicResults(results); hit.TotalResults != want {
		t.Fatalf("total_results=%d, want %d", hit.TotalResults, want)
	}

	// Wrapped hit through a stubbed /link redirect: SERPs without data-mdurl
	// leave opaque www.so.com/link?m= rows behind.
	const wrappedTarget = "https://example.org/article/"
	wrappedRows := append([]core.SearchResult(nil), results...)
	wrappedRows = append(wrappedRows, core.SearchResult{Rank: 5, URL: "https://www.so.com/link?m=opaque", Title: "wrapped"})
	wrappedHit := core.EvaluateIndexSERP(context.Background(), wrappedTarget, wrappedRows,
		core.IndexResolveOptions{Fetch: func(ctx context.Context, wrapped string, q core.Query) (*http.Response, error) {
			if !strings.Contains(wrapped, "so.com/link") {
				return nil, errors.New("unexpected wrapper host")
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{wrappedTarget}},
				Body:       http.NoBody,
			}, nil
		}})
	if !wrappedHit.Indexed || wrappedHit.Match != core.IndexMatchExact {
		t.Fatalf("expected wrapped-link hit, got %+v", wrappedHit)
	}
	if wrappedHit.MatchedResultURL != wrappedTarget {
		t.Fatalf("matched_result_url=%q, want %q", wrappedHit.MatchedResultURL, wrappedTarget)
	}

	// Miss: resolution fails (404), verdict stays none.
	miss, calls := fetchCounter(t)
	none := core.EvaluateIndexSERP(context.Background(), "http://absent.example.cn/never-crawled", wrappedRows,
		core.IndexResolveOptions{Fetch: miss})
	if none.Indexed || none.Match != core.IndexMatchNone {
		t.Fatalf("expected none verdict, got %+v", none)
	}
	if calls() == 0 {
		t.Fatal("expected the wrapped candidates to be resolved on a miss")
	}
}
