package sogou

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/PuerkitoBio/goquery"
	"github.com/karust/openserp/core"
)

func classifySogouRawHTML(body []byte) error {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	return classifySogouDocument(doc)
}

// SearchRaw fetches a Sogou SERP over plain HTTP and parses it. It backs the
// browser engine's fallback path: Sogou sometimes challenges the headless
// browser session while still serving plain clients.
func SearchRaw(ctx context.Context, query core.Query) ([]core.SearchResult, error) {
	ctx = core.PrepareEngineContext(ctx, query, "sogou")

	searchURL, err := BuildURL(query)
	if err != nil {
		return nil, err
	}

	res, err := core.RawSearchRequest(ctx, searchURL, query)
	if err != nil {
		return nil, err
	}
	defer core.DrainAndCloseResponse(res)

	body, err := core.ReadRawSearchBody(res)
	if err != nil {
		return nil, err
	}

	htmlStatus := classifySogouRawHTML(body)
	if htmlStatus != nil && !errors.Is(htmlStatus, core.ErrEmptyResult) {
		return nil, htmlStatus
	}

	parsed, err := ParseHTML(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		if errors.Is(htmlStatus, core.ErrEmptyResult) {
			return []core.SearchResult{}, nil
		}
		return nil, fmt.Errorf("%w: sogou raw search returned no parseable results", core.ErrParser)
	}

	if query.Start > 0 {
		for i := range parsed {
			if parsed[i].AbsoluteRank > 0 {
				parsed[i].AbsoluteRank += query.Start
			}
			if parsed[i].Ad {
				continue
			}
			parsed[i].Rank = query.Start + parsed[i].Rank
		}
	}

	parsed = SearchResolveRedirects(ctx, parsed, query)
	parsed = core.StripResultFeatures(core.DeduplicateResults(parsed), query.Features)
	parsed = core.LimitOrganicResults(parsed, query.Limit)
	return parsed, nil
}
