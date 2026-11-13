package sogou

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/karust/openserp/core"
	"github.com/sirupsen/logrus"
)

// webSearchURL is Sogou's web SERP endpoint.
const webSearchURL = "https://www.sogou.com/web"

// resultsPerPage is the organic result count Sogou renders per SERP page.
const resultsPerPage = 10

// BuildURL builds a Sogou web search URL from Query fields.
// Query text, site and filetype filters are folded into the query string the
// same way Sogou's own advanced-search box does it.
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
	params.Set("query", text)
	params.Set("ie", "utf8")

	if q.LangCode != "" {
		// Sogou exposes no per-request language switch; it follows the locale.
		logrus.Warn("Language search not supported")
	}
	if q.DateInterval != "" {
		// tsn only offers fixed windows (day/week/month/year), not ranges.
		logrus.Warn("Date range search not supported")
	}

	if q.Start < 0 {
		return "", errors.New("incorrect start provided")
	}
	if q.Start > 0 {
		// Sogou pages are 1-based and hold resultsPerPage organic rows.
		params.Set("page", strconv.Itoa(q.Start/resultsPerPage+1))
	}

	base.RawQuery = params.Encode()
	return base.String(), nil
}
