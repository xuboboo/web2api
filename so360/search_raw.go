package so360

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/PuerkitoBio/goquery"
	"github.com/karust/openserp/core"
)

func classifySo360RawHTML(body []byte) error {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	return classifySo360Document(doc)
}

// SearchRaw fetches a 360 SERP over plain HTTP and parses it. 360 gates
// headless browsers by fingerprint long before it gates plain HTTP clients, so
// this path keeps the engine usable when the browser session is challenged.
func SearchRaw(ctx context.Context, query core.Query) ([]core.SearchResult, error) {
	ctx = core.PrepareEngineContext(ctx, query, "so360")

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

	htmlStatus := classifySo360RawHTML(body)
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
		return nil, fmt.Errorf("%w: 360 raw search returned no parseable results", core.ErrParser)
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

	parsed = core.StripResultFeatures(core.DeduplicateResults(parsed), query.Features)
	parsed = core.LimitOrganicResults(parsed, query.Limit)
	return parsed, nil
}
