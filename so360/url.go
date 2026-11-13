package so360

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/karust/openserp/core"
	"github.com/sirupsen/logrus"
)

// webSearchURL is 360 Search's web SERP endpoint.
const webSearchURL = "https://www.so.com/s"

// resultsPerPage is the organic result count 360 renders per SERP page.
const resultsPerPage = 10

// BuildURL builds a 360 Search web URL from Query fields.
// Site and filetype filters are folded into the query text, matching the way
// 360's advanced search box serializes them.
func BuildURL(q core.Query) (string, error) {
	base, err := url.Parse(webSearchURL)
	if err != nil {
		return "", err
	}

	text := strings.TrimSpace(q.Text)
	if q.Site != "" {
		text = strings.TrimSpace(text + " site:" + q.Site)
	}
	if q.Filetype != "" {
		text = strings.TrimSpace(text + " filetype:" + q.Filetype)
	}
	if text == "" {
		return "", errors.New("empty query built")
	}

	params := url.Values{}
	params.Set("q", text)

	if q.LangCode != "" {
		logrus.Warn("Language search not supported")
	}
	if q.DateInterval != "" {
		// 360 only exposes fixed time windows, not arbitrary date ranges.
		logrus.Warn("Date range search not supported")
	}

	if q.Start < 0 {
		return "", errors.New("incorrect start provided")
	}
	if q.Start > 0 {
		// 360 pages are 1-based and hold resultsPerPage organic rows.
		params.Set("pn", strconv.Itoa(q.Start/resultsPerPage+1))
	}

	base.RawQuery = params.Encode()
	return base.String(), nil
}
