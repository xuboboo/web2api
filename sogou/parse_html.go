package sogou

import (
	"errors"
	"io"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/karust/openserp/core"
)

// ParseHTML parses a Sogou SERP HTML document and returns search results.
// No network I/O.
func ParseHTML(r io.Reader) ([]core.SearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	if pageStatus := classifySogouDocument(doc); pageStatus != nil {
		if errors.Is(pageStatus, core.ErrEmptyResult) {
			return []core.SearchResult{}, nil
		}
		return nil, pageStatus
	}
	return parseSogouDocument(doc), nil
}

func classifySogouDocument(doc *goquery.Document) error {
	return core.ClassifyChallengeDocument(doc, core.DocSignals{
		CaptchaSelectors: Selectors.Captcha,
		EmptySelectors:   Selectors.NoResults,
	})
}

func parseSogouDocument(doc *goquery.Document) []core.SearchResult {
	var results []core.SearchResult

	doc.Find(Selectors.Results).Each(func(_ int, item *goquery.Selection) {
		if isHiddenSogouCard(item) {
			return
		}
		link := item.Find(Selectors.Title).First()
		if link.Length() == 0 {
			// Search-suggestion rows and other helper cards carry no result
			// heading; they are not organic results.
			return
		}

		title := core.NormalizeWhitespace(link.Text())
		href, ok := link.Attr("href")
		if !ok {
			return
		}
		href = absolutizeSogouURL(strings.TrimSpace(href))
		if title == "" || href == "" {
			return
		}

		desc := ""
		for _, selector := range Selectors.Desc {
			node := item.Find(selector).First()
			if node.Length() == 0 {
				continue
			}
			if text := core.NormalizeWhitespace(node.Text()); text != "" {
				desc = text
				break
			}
		}

		results = append(results, core.SearchResult{
			URL:         href,
			Title:       title,
			Description: desc,
			DisplayURL:  sogouAttribution(item),
			Ad:          sogouIsAd(item),
		})
	})

	return rankSogouResults(results)
}

// isHiddenSogouCard drops prefetched cards and inline suggestion rows that
// Sogou keeps in the DOM with display:none.
func isHiddenSogouCard(item *goquery.Selection) bool {
	if style, ok := item.Attr("style"); ok {
		if strings.Contains(strings.ToLower(style), "display:none") || strings.Contains(strings.ToLower(style), "display: none") {
			return true
		}
	}
	return item.HasClass("hint-mid") || item.Find(".hint-mid").Length() > 0
}

// sogouAttribution returns "site name · visible url" when the card renders it.
func sogouAttribution(item *goquery.Selection) string {
	cite := item.Find(Selectors.Cite).First()
	if cite.Length() == 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	cite.Find("span").Each(func(_ int, span *goquery.Selection) {
		text := core.NormalizeWhitespace(span.Text())
		if text == "" {
			return
		}
		parts = append(parts, text)
	})
	if len(parts) == 0 {
		return core.NormalizeWhitespace(cite.Text())
	}
	return strings.Join(parts, " ")
}

func sogouIsAd(item *goquery.Selection) bool {
	for _, selector := range Selectors.AdMarkers {
		if item.Is(selector) || item.Find(selector).Length() > 0 {
			return true
		}
	}
	for _, label := range Selectors.AdLabels {
		// Sogou prints the promo label in the card's top-left corner, so only
		// short standalone labels count; a snippet that merely mentions 推广
		// must not turn an organic row into an ad.
		found := false
		item.Find("span, i, em, div").EachWithBreak(func(_ int, marker *goquery.Selection) bool {
			text := strings.TrimSpace(marker.Text())
			if text == label {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// absolutizeSogouURL turns Sogou's site-relative /link?url= redirects into
// absolute URLs. The parser keeps the redirect: Search resolves it to the real
// destination when that succeeds.
func absolutizeSogouURL(href string) string {
	switch {
	case href == "", href == "#", strings.HasPrefix(href, "javascript:"):
		return ""
	case strings.HasPrefix(href, "//"):
		return "https:" + href
	case strings.HasPrefix(href, "/"):
		return "https://www.sogou.com" + href
	default:
		return href
	}
}

// rankSogouResults deduplicates by URL and assigns a clean 1..N organic rank
// plus a mixed absolute rank.
func rankSogouResults(results []core.SearchResult) []core.SearchResult {
	deduped := core.DeduplicateResults(results)
	rank := core.NewRankState(0)
	for i := range deduped {
		rankValue, absoluteRank := rank.Next(deduped[i].Ad)
		deduped[i].Rank = rankValue
		deduped[i].AbsoluteRank = absoluteRank
	}
	return deduped
}
