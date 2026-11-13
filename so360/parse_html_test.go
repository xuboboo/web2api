package so360

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/karust/openserp/core"
)

func loadFixture(t *testing.T) *os.File {
	t.Helper()
	file, err := os.Open("testdata/search_results.html")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestParseHTMLFixture(t *testing.T) {
	results, err := ParseHTML(loadFixture(t))
	if err != nil {
		t.Fatalf("ParseHTML() error = %v", err)
	}
	// The fixture keeps five <li class="res-list"> cards; the leading one is a
	// card without a result heading (dropped by the parser), so four rows are
	// expected.
	if len(results) < 4 {
		t.Fatalf("expected at least 4 results, got %d", len(results))
	}

	organicRank := 0
	for _, result := range results {
		if result.Title == "" {
			t.Fatalf("result without title: %+v", result)
		}
		if !strings.HasPrefix(result.URL, "http://") && !strings.HasPrefix(result.URL, "https://") {
			t.Fatalf("result URL is not absolute: %+v", result)
		}
		if result.Ad {
			continue
		}
		organicRank++
		if result.Rank != organicRank {
			t.Fatalf("organic ranks must be sequential: %+v got=%d want=%d", result, result.Rank, organicRank)
		}
	}

	// 360 keeps the real destination in data-mdurl, so the parser must return it
	// instead of the www.so.com/link?m= redirect.
	foundRealDestination := false
	for _, result := range results {
		if result.URL == "https://www.jb51.net/list/list_246_1.htm" {
			foundRealDestination = true
		}
		if strings.Contains(result.URL, "so.com/link") {
			t.Fatalf("data-mdurl destination was not preferred: %+v", result)
		}
	}
	if !foundRealDestination {
		t.Fatalf("expected data-mdurl destination in results, got %+v", results)
	}

	withDesc := 0
	for _, result := range results {
		if result.Description != "" {
			withDesc++
		}
	}
	if withDesc < 3 {
		t.Fatalf("expected snippets on most results, got %d/%d", withDesc, len(results))
	}
}

func TestParseHTMLMarksAds(t *testing.T) {
	html := `<html><body><ul class="result">
		<li class="res-list" data-result-type="ad">
			<h3 class="res-title"><a href="https://www.so.com/link?m=ad" data-mdurl="https://ad.example.com/">promoted</a></h3>
			<p class="res-desc">ad snippet</p>
		</li>
		<li class="res-list" data-result-type="normal">
			<h3 class="res-title"><a href="https://www.so.com/link?m=ok" data-mdurl="https://example.com/page">organic</a></h3>
			<p class="res-desc">organic snippet</p>
		</li>
	</ul></body></html>`
	results, err := ParseHTML(strings.NewReader(html))
	if err != nil {
		t.Fatalf("ParseHTML() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	if !results[0].Ad {
		t.Fatalf("first result should be an ad: %+v", results[0])
	}
	if results[1].Ad || results[1].URL != "https://example.com/page" {
		t.Fatalf("unexpected organic result: %+v", results[1])
	}
	if results[1].Rank != 1 {
		t.Fatalf("organic rank should start at 1, got %d", results[1].Rank)
	}
}

func TestParseHTMLCaptcha(t *testing.T) {
	_, err := ParseHTML(strings.NewReader(`<html><body><div id="captcha"></div></body></html>`))
	if !errors.Is(err, core.ErrCaptcha) {
		t.Fatalf("expected ErrCaptcha, got %v", err)
	}
}

func TestParseHTMLNoResults(t *testing.T) {
	results, err := ParseHTML(strings.NewReader(`<html><body><div class="no-result">抱歉，没有找到相关结果</div></body></html>`))
	if err != nil {
		t.Fatalf("ParseHTML() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}