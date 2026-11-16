package bing

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
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

// TestBingIndexQueryStripsScheme locks in bing's SERP query form: the scheme
// is dropped because cn.bing.com mis-parses "https://" as a keyword (10-site
// probe: full URL hit 4/10 vs scheme-less 7/10). No "url:" operator either.
func TestBingIndexQueryStripsScheme(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"https://example.com/post/1", "example.com/post/1"},
		{"http://go.dev", "go.dev"},
		{"example.com", "example.com"},       // already scheme-less
		{"https://www.python.org/", "www.python.org/"},
	}
	for _, tc := range cases {
		if got := bingIndexQuery(tc.in); got != tc.want {
			t.Errorf("bingIndexQuery(%q)=%q, want %q", tc.in, got, tc.want)
		}
		if strings.HasPrefix(bingIndexQuery(tc.in), "url:") {
			t.Errorf("bing query must not use retired url: operator: %q", tc.in)
		}
	}

	// The SERP query itself stays plain (limit 10, no filters/features).
	q := core.IndexCheckQuery(bingIndexQuery("https://example.com/post/1"), core.Query{})
	if q.Text != "example.com/post/1" || q.Limit != core.IndexCheckLimit || q.Start != 0 || q.Features {
		t.Fatalf("index SERP query must be scheme-less plain with limit %d, got %+v", core.IndexCheckLimit, q)
	}
}

// TestCheckIndexedRejectsEmptyTarget keeps the engine entry point honest.
func TestCheckIndexedRejectsEmptyTarget(t *testing.T) {
	t.Parallel()

	bing := &Bing{}
	if _, err := bing.CheckIndexed(context.Background(), "", core.Query{}); !errors.Is(err, core.ErrEngineInternal) {
		t.Fatalf("expected ErrEngineInternal for empty target, got %v", err)
	}
}

// TestCheckIndexedImplementsIndexChecker is the compile-time interface guard.
func TestCheckIndexedImplementsIndexChecker(t *testing.T) {
	t.Parallel()

	var _ core.IndexChecker = (*Bing)(nil)
}

// TestIndexVerdictFromFixtureSERP runs the platform matching over the real
// Bing SERP fixture with case-folded host and trailing-slash variants. No
// network I/O.
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

	// Pick the first usable organic URL; the fixture rows point at
	// http://test.test, so a case/trailing-slash variant must hit exactly.
	var firstOrganic string
	for _, r := range results {
		if r.Ad || r.URL == "" || !core.IsUsableResultURL(r.URL) {
			continue
		}
		firstOrganic = r.URL
		break
	}
	if firstOrganic == "" {
		t.Fatalf("fixture has no usable organic URL: %+v", results)
	}
	// Uppercase scheme and host, append a trailing slash: normalization must
	// fold all of that away.
	rest := firstOrganic
	scheme := ""
	if i := strings.Index(rest, "://"); i >= 0 {
		scheme = strings.ToUpper(rest[:i+3])
		rest = rest[i+3:]
	}
	host := rest
	if j := strings.Index(rest, "/"); j >= 0 {
		host = rest[:j]
	}
	upper := scheme + strings.ToUpper(host) + "/"

	hit := core.EvaluateIndexSERP(context.Background(), upper, results, core.IndexResolveOptions{Fetch: mustFailFetch(t)})
	if !hit.Indexed || hit.Match != core.IndexMatchExact {
		t.Fatalf("expected exact hit for %q against fixture, got %+v", upper, hit)
	}
	if hit.MatchedResultURL == "" {
		t.Fatal("matched_result_url must be recorded on a hit")
	}
	if want := core.CountOrganicResults(results); hit.TotalResults != want {
		t.Fatalf("total_results=%d, want %d", hit.TotalResults, want)
	}

	miss := core.EvaluateIndexSERP(context.Background(), "http://absent.example.cn/never-crawled", results,
		core.IndexResolveOptions{Fetch: mustFailFetch(t)})
	if miss.Indexed || miss.Match != core.IndexMatchNone {
		t.Fatalf("expected none verdict, got %+v", miss)
	}
}
