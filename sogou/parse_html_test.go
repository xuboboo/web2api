package sogou

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
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d: %+v", len(results), results)
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
			t.Fatalf("organic ranks must be sequential: result=%+v got=%d want=%d", result, result.Rank, organicRank)
		}
		if result.AbsoluteRank <= 0 {
			t.Fatalf("absolute rank must be set: %+v", result)
		}
	}

	if !strings.Contains(results[0].Title, "搜狗百科") {
		t.Fatalf("first result should be the encyclopedia card, got %q", results[0].Title)
	}
	if !strings.HasPrefix(results[0].URL, "https://baike.sogou.com/") {
		t.Fatalf("first result URL = %q, want a baike.sogou.com destination", results[0].URL)
	}
	// Sogou serves organic destinations through /link?url=; the parser keeps
	// them absolute so Search can resolve them later.
	linkRows := 0
	for _, result := range results {
		if strings.Contains(result.URL, "sogou.com/link") {
			linkRows++
		}
	}
	if linkRows == 0 {
		t.Fatalf("expected /link redirect rows in fixture, got none: %+v", results)
	}
	if results[1].DisplayURL == "" {
		t.Fatalf("expected visible attribution on the second card, got %+v", results[1])
	}
}

func TestParseHTMLSkipsHiddenCards(t *testing.T) {
	html := `<html><body><div class="vrwrap" style="display:none">
			<h3 class="vr-title"><a href="/link?url=hidden">hidden result</a></h3></div>
		<div class="vrwrap">
			<h3 class="vr-title"><a href="/link?url=visible">visible result</a></h3>
			<div class="text-layout"><p class="star-wiki">snippet</p></div></div>
		</body></html>`
	results, err := ParseHTML(strings.NewReader(html))
	if err != nil {
		t.Fatalf("ParseHTML() error = %v", err)
	}
	if len(results) != 1 || results[0].Title != "visible result" {
		t.Fatalf("hidden card must be dropped, got %+v", results)
	}
	if !strings.HasPrefix(results[0].URL, "https://www.sogou.com/link") {
		t.Fatalf("relative /link URL must be absolutized, got %q", results[0].URL)
	}
}

func TestParseHTMLCaptcha(t *testing.T) {
	html := `<html><body><div id="seccodeImage"></div></body></html>`
	_, err := ParseHTML(strings.NewReader(html))
	if !errors.Is(err, core.ErrCaptcha) {
		t.Fatalf("expected ErrCaptcha, got %v", err)
	}
}

func TestParseHTMLNoResults(t *testing.T) {
	html := `<html><body><div class="noresult">抱歉，没有找到相关结果</div></body></html>`
	results, err := ParseHTML(strings.NewReader(html))
	if err != nil {
		t.Fatalf("ParseHTML() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}
