package baidu

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/xuboboo/web2api/core"
)

func mustFailFetch(t *testing.T) core.IndexCheckFetcher {
	t.Helper()
	return func(ctx context.Context, wrapped string, q core.Query) (*http.Response, error) {
		t.Errorf("unexpected network fetch for %q", wrapped)
		return nil, errors.New("network disabled in tests")
	}
}

// TestCheckIndexedQueryBuildsURLPhrase locks in the contract's query rule for
// baidu: the target URL itself is the query phrase with a depth of 10, and the
// runtime proxy/TLS settings chosen by the resilience layer are preserved.
func TestCheckIndexedQueryBuildsURLPhrase(t *testing.T) {
	t.Parallel()

	target := "https://example.com/post/1"
	q := core.IndexCheckQuery(target, core.Query{ProxyURL: "socks5h://1.2.3.4:1080", Insecure: true})
	if q.Text != target {
		t.Fatalf("query text=%q, want target URL %q", q.Text, target)
	}
	if q.Limit != core.IndexCheckLimit || q.Start != 0 {
		t.Fatalf("limit/start=%d/%d, want %d/0", q.Limit, q.Start, core.IndexCheckLimit)
	}
	if q.Features || q.Site != "" || q.DateInterval != "" || q.Filetype != "" {
		t.Fatalf("index SERP query must be plain, got %+v", q)
	}
	if q.ResolveTop != core.IndexCheckLimit {
		t.Fatalf("resolve_top=%d, want %d (unwrap every wrapped candidate)", q.ResolveTop, core.IndexCheckLimit)
	}
	if q.ProxyURL == "" || !q.Insecure {
		t.Fatalf("runtime proxy/TLS settings must be preserved, got %+v", q)
	}
}

// TestCheckIndexedRejectsEmptyTarget keeps the engine entry point honest.
func TestCheckIndexedRejectsEmptyTarget(t *testing.T) {
	t.Parallel()

	baid := &Baidu{}
	if _, err := baid.CheckIndexed(context.Background(), "   ", core.Query{}); !errors.Is(err, core.ErrEngineInternal) {
		t.Fatalf("expected ErrEngineInternal for empty target, got %v", err)
	}
}

// TestCheckIndexedImplementsIndexChecker is the compile-time interface guard.
func TestCheckIndexedImplementsIndexChecker(t *testing.T) {
	t.Parallel()

	var _ core.IndexChecker = (*Baidu)(nil)
}

// TestIndexVerdictFromFixtureSERP runs the platform matching over the real
// Baidu SERP fixture: the fixture's organic rows point at http://test.test,
// so a case-folded target with a trailing slash and fragment must hit exactly,
// and an unrelated URL must not. No network I/O.
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

	hit := core.EvaluateIndexSERP(context.Background(), "HTTP://TEST.TEST/#top", results,
		core.IndexResolveOptions{Fetch: mustFailFetch(t)})
	if !hit.Indexed || hit.Match != core.IndexMatchExact {
		t.Fatalf("expected exact hit against fixture, got %+v", hit)
	}
	if hit.MatchedResultURL == "" {
		t.Fatal("matched_result_url must be recorded on a hit")
	}
	if want := core.CountOrganicResults(results); hit.TotalResults != want {
		t.Fatalf("total_results=%d, want %d", hit.TotalResults, want)
	}

	miss := core.EvaluateIndexSERP(context.Background(), "http://absent.example.cn/never-crawled", results,
		core.IndexResolveOptions{Fetch: mustFailFetch(t)})
	if miss.Indexed || miss.Match != core.IndexMatchNone || miss.MatchedResultURL != "" {
		t.Fatalf("expected none verdict, got %+v", miss)
	}
	if want := core.CountOrganicResults(results); miss.TotalResults != want {
		t.Fatalf("miss total_results=%d, want %d", miss.TotalResults, want)
	}
}
