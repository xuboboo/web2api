package baidu

import (
	"context"
	"errors"
	neturl "net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/xuboboo/web2api/core"
)

// Baidu implements core.SearchEngine for Baidu SERP pages.
type Baidu struct {
	core.Browser
	core.SearchEngineOptions
	logger *core.EngineLogger
	// gate remembers a recent browser-side challenge so the plain-HTTP path is
	// preferred while it is active.
	gate *core.CaptchaGate
}

// New creates a Baidu engine instance with browser/runtime options applied.
func New(browser core.Browser, opts core.SearchEngineOptions) *Baidu {
	baid := Baidu{Browser: browser, gate: &core.CaptchaGate{}}
	opts.Init()
	baid.SearchEngineOptions = opts
	baid.logger = core.NewEngineLogger("Baidu")
	return &baid
}

// Name returns the stable engine identifier.
func (baid *Baidu) Name() string {
	return "baidu"
}

// classifyBlockPage runs the same captcha/timeout/no-results rules the raw
// HTML path uses (classifyBaiduDocument), against a snapshot of the live
// page, so both paths can't drift. ErrEmptyResult is not a block: callers
// handle that themselves once they've also checked for parsed results.
func (baid *Baidu) classifyBlockPage(page *rod.Page, url string) error {
	if err := baid.securityInterstitialErr(page); err != nil {
		return err
	}
	err := core.ClassifyFromPage(page, classifyBaiduDocument)
	if errors.Is(err, core.ErrEmptyResult) {
		return nil
	}
	if err != nil {
		baid.logger.Error("Page classified as %v: %s", err, url)
	}
	return err
}

// securityInterstitialErr recognizes Baidu's challenge pages by URL and page
// title so callers get ErrCaptcha (429 captcha_detected) instead of a
// misleading timeout. Two shapes exist in the wild: the classic wappass
// 图形验证码 redirect, and the newer bio-capability interstitial (百度安全验证),
// which JS-redirects to the bioc-static/seccaptcha CDN (ds_svcp.html). The
// redirect can happen while the caller is still polling for result cards, so
// the wait loop runs this check on every poll. The page title is the most
// variant-proof signal: it survives future CDN bucket moves.
func (baid *Baidu) securityInterstitialErr(page *rod.Page) error {
	if page == nil {
		return nil
	}
	info, err := page.Info()
	if err != nil || info == nil {
		return nil
	}
	if u, perr := neturl.Parse(info.URL); perr == nil {
		host := strings.ToLower(u.Hostname())
		path := strings.ToLower(u.Path)
		if strings.Contains(host, "wappass") ||
			strings.Contains(host, "seccaptcha") ||
			strings.Contains(host, "bioc-static") ||
			strings.Contains(path, "captcha") ||
			strings.Contains(path, "verify") ||
			strings.Contains(path, "ds_svcp") {
			baid.logger.Error("Captcha detected: %s", info.URL)
			return core.ErrCaptcha
		}
	}
	if strings.Contains(info.Title, "安全验证") {
		baid.logger.Error("Captcha detected by page title: %s", info.Title)
		return core.ErrCaptcha
	}
	return nil
}

// Search executes a Baidu web search and returns normalized search results.
// When the browser session is challenged, it falls back to the plain-HTTP path.
func (baid *Baidu) Search(ctx context.Context, query core.Query) ([]core.SearchResult, error) {
	rawQuery := query
	rawQuery.Insecure = rawQuery.Insecure || baid.Browser.Insecure

	// While the browser path is known to be challenged, try the plain-HTTP path
	// first: it stays usable much longer on a shared exit IP.
	if baid.gate.Blocked() {
		if raw, rawErr := SearchRaw(ctx, rawQuery); rawErr == nil && len(raw) > 0 {
			return raw, nil
		}
	}

	results, err := baid.searchBrowser(ctx, query)
	if err == nil {
		baid.gate.Clear()
		return results, nil
	}
	if errors.Is(err, core.ErrCaptcha) || errors.Is(err, core.ErrSearchTimeout) || errors.Is(err, core.ErrBlocked) || errors.Is(err, core.ErrParser) {
		baid.gate.Trip(0)
		if raw, rawErr := SearchRaw(ctx, rawQuery); rawErr == nil && len(raw) > 0 {
			baid.logger.WithRequest(ctx).Info("baidu browser path failed (%v); served %d results over HTTP", err, len(raw))
			return raw, nil
		}
	}
	return nil, err
}

func (baid *Baidu) searchBrowser(ctx context.Context, query core.Query) (results []core.SearchResult, err error) {
	ctx = core.PrepareEngineContext(ctx, query, baid.Name())
	if profileID := strings.TrimSpace(baid.ProfileID); profileID != "" {
		ctx = core.WithForcedProfileID(ctx, profileID)
	}
	scoped := *baid
	scoped.logger = baid.logger.WithRequest(ctx)
	if scoped.Browser.WaitLoadTime == 0 || scoped.Browser.WaitLoadTime > 250*time.Millisecond {
		scoped.Browser.WaitLoadTime = 250 * time.Millisecond
	}
	baid = &scoped

	baid.logger.Debug("Starting search, query: %+v", query)

	// Build URL from query struct to open in browser
	url, err := BuildURL(query)
	if err != nil {
		return nil, err
	}

	page, err := baid.Navigate(ctx, url)
	if err != nil {
		return nil, err
	}
	defer core.DeferClosePage(ctx, page, &baid.Browser)()

	if err := baid.classifyBlockPage(page, url); err != nil {
		return nil, err
	}

	searchResults, err := baid.waitForParsedSearchResults(ctx, page, url)
	if err != nil {
		return nil, err
	}

	for i := range searchResults {
		if searchResults[i].AbsoluteRank > 0 {
			searchResults[i].AbsoluteRank += query.Start
		}
		if searchResults[i].Ad {
			continue
		}
		searchResults[i].Rank = query.Start + searchResults[i].Rank
	}
	searchResults = core.StripResultFeatures(searchResults, query.Features)
	// Honor the client's limit like every other browser engine does: ads and
	// typed rows pass through, organic rows are capped at query.Limit.
	searchResults = core.LimitOrganicResults(searchResults, query.Limit)
	return searchResults, nil
}

func (baid *Baidu) waitForParsedSearchResults(ctx context.Context, page *rod.Page, url string) ([]core.SearchResult, error) {
	timeout := baid.GetSelectorTimeout()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var sawResultContainer bool
	var lastErr error

	for {
		// The security-check interstitial usually JS-redirects after Navigate
		// returns, mid-poll. Catch it here so the challenge surfaces as
		// ErrCaptcha within one poll cycle instead of burning the whole
		// selector deadline on a page that will never show result cards.
		if blockErr := baid.securityInterstitialErr(page); blockErr != nil {
			return nil, blockErr
		}

		html, err := page.HTML()
		if err == nil {
			results, parseErr := ParseHTML(strings.NewReader(html))
			if parseErr == nil && len(results) > 0 {
				return results, nil
			}
			if parseErr != nil && !errors.Is(parseErr, core.ErrEmptyResult) {
				baid.logger.Error("Page classified as %v: %s", parseErr, url)
				return nil, parseErr
			}
			lastErr = parseErr
		} else {
			lastErr = err
			if blockErr := baid.classifyBlockPage(page, url); blockErr != nil {
				return nil, blockErr
			}
		}

		if core.HasAnySelector(page, baiduResultSelectors()) {
			sawResultContainer = true
		}
		if !time.Now().Before(deadline) {
			break
		}
		if err := core.SleepContext(ctx, 120*time.Millisecond); err != nil {
			return nil, err
		}
	}

	if sawResultContainer {
		if lastErr != nil {
			baid.logger.Debug("Baidu result containers found but HTML parsing failed: %v", lastErr)
		} else {
			baid.logger.Debug("Baidu result containers found but no parseable organic results")
		}
		return nil, core.ErrParser
	}
	// The page never reached a recognizable state: no result containers, no
	// captcha or timeout markers. Baidu hydrates result cards client-side and
	// can exceed the selector deadline, so report a timeout rather than a
	// successful empty SERP — callers must retry/skip, not trust 0 results.
	baid.logger.Debug("No result containers or block markers within selector timeout")
	return nil, core.ErrSearchTimeout
}
