package so360

import (
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/karust/openserp/core"
)

// ParseHTML parses a 360 Search SERP HTML document and returns search results.
// No network I/O.
func ParseHTML(r io.Reader) ([]core.SearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	if pageStatus := classifySo360Document(doc); pageStatus != nil {
		if errors.Is(pageStatus, core.ErrEmptyResult) {
			return []core.SearchResult{}, nil
		}
		return nil, pageStatus
	}
	return parseSo360Document(doc), nil
}

func classifySo360Document(doc *goquery.Document) error {
	return core.ClassifyChallengeDocument(doc, core.DocSignals{
		CaptchaSelectors: Selectors.Captcha,
		EmptySelectors:   Selectors.NoResults,
	})
}

func parseSo360Document(doc *goquery.Document) []core.SearchResult {
	var results []core.SearchResult

	doc.Find(Selectors.Results).Each(func(_ int, item *goquery.Selection) {
		if style, ok := item.Attr("style"); ok {
			lowered := strings.ToLower(style)
			if strings.Contains(lowered, "display:none") || strings.Contains(lowered, "display: none") {
				return
			}
		}

		link := item.Find(Selectors.Title).First()
		if link.Length() == 0 {
			return
		}
		title := core.NormalizeWhitespace(link.Text())
		if title == "" {
			return
		}

		href := so360Destination(link)
		if href == "" {
			return
		}

		desc := core.NormalizeWhitespace(item.Find(Selectors.Desc).First().Text())
		cite := core.NormalizeWhitespace(item.Find(Selectors.Cite).First().Text())

		results = append(results, core.SearchResult{
			URL:         href,
			Title:       title,
			Description: desc,
			DisplayURL:  cite,
			Ad:          so360IsAd(item),
		})
	})

	return rankSo360Results(results)
}

// so360Destination prefers the card's data-mdurl attribute, which carries the
// real destination, over the opaque www.so.com/link?m= redirect in href.
func so360Destination(link *goquery.Selection) string {
	if raw, ok := link.Attr("data-mdurl"); ok {
		if target := absoluteSo360URL(strings.TrimSpace(raw)); target != "" {
			return target
		}
	}
	if raw, ok := link.Attr("href"); ok {
		return absoluteSo360URL(strings.TrimSpace(raw))
	}
	return ""
}

func absoluteSo360URL(raw string) string {
	switch {
	case raw == "", raw == "#", strings.HasPrefix(raw, "javascript:"):
		return ""
	case strings.HasPrefix(raw, "//"):
		return "https:" + raw
	case strings.HasPrefix(raw, "/"):
		return "https://www.so.com" + raw
	default:
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return ""
		}
		return raw
	}
}

func so360IsAd(item *goquery.Selection) bool {
	if kind, ok := item.Attr("data-result-type"); ok {
		value := strings.ToLower(strings.TrimSpace(kind))
		for _, adType := range Selectors.AdTypes {
			if value == adType {
				return true
			}
		}
	}
	for _, selector := range Selectors.AdMarkers {
		if item.Is(selector) || item.Find(selector).Length() > 0 {
			return true
		}
	}
	// 360 prints a small "广告"/"推广" badge on promoted cards.
	adFound := false
	item.Find("span, i, em, div").EachWithBreak(func(_ int, marker *goquery.Selection) bool {
		text := strings.TrimSpace(marker.Text())
		for _, label := range Selectors.AdLabels {
			if text == label {
				adFound = true
				return false
			}
		}
		return true
	})
	return adFound
}

func rankSo360Results(results []core.SearchResult) []core.SearchResult {
	deduped := core.DeduplicateResults(results)
	rank := core.NewRankState(0)
	for i := range deduped {
		rankValue, absoluteRank := rank.Next(deduped[i].Ad)
		deduped[i].Rank = rankValue
		deduped[i].AbsoluteRank = absoluteRank
	}
	return deduped
}
