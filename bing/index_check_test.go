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

// TestCheckIndexedQueryUsesBareURL locks in bing's index query contract: the
// target URL is searched verbatim (no "url:" operator). The url: operator was
// retired in 2026-11 after live probes showed cn.bing.com returning unrelated
// pages for it (4/4 targets missed), while the bare URL kept root pages at #1.
func TestCheckIndexedQueryUsesBareURL(t *testing.T) {
	t.Parallel()

	target := "https://example.com/post/1"
	q := core.IndexCheckQuery(target, core.Query{})
	if q.Text != target {
		t.Fatalf("query text=%q, want bare target %q", q.Text, target)
	}
	if strings.HasPrefix(q.Text, "url:") {
		t.Fatalf("bing index query must not use the retired url: operator, got %q", q.Text)
	}
	if q.Limit != core.IndexCheckLimit || q.Start != 0 || q.Features {
		t.Fatalf("index SERP query must be plain with limit %d, got %+v", core.IndexCheckLimit, q)
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
