// Package so360 implements a 360 Search (so.com, 360搜索) web SERP scraper.
//
// 360 Search is one of the major mainland-China search engines. It renders
// most organic cards server-side but hydrates ad/AI blocks with JS, so the
// engine drives a real browser and parses the resulting DOM with the shared
// goquery parser (also exposed as POST /so360/parse).
package so360

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/karust/openserp/core"
)

// minDomWait is the floor for waiting on hydrated result cards.
const minDomWait = 8 * time.Second

// resolveTimeout bounds one 360 /link redirect lookup.
const resolveTimeout = 3 * time.Second

// resolveConcurrency caps parallel redirect lookups per search.
const resolveConcurrency = 4

// defaultRedirectResolves caps how many destinations a single search resolves;
// each one is an extra first-party request to the engine.
const defaultRedirectResolves = 5

// maxRedirectResolves honors an explicit resolve_top, defaulting to 5.
func maxRedirectResolves(query core.Query) int {
	if query.ResolveTop > 0 {
		return query.ResolveTop
	}
	return defaultRedirectResolves
}

// redirectUserAgent keeps the redirect lookup a plain, first-party page load.
const redirectUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// newRedirectClient builds a plain net/http client that never follows
// redirects, so the Location header can be read.
func newRedirectClient(query core.Query) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy := strings.TrimSpace(query.ProxyURL); proxy != "" {
		if normalized, err := core.NormalizeProxyURL(proxy); err == nil {
			if proxyURL, err := url.Parse(normalized); err == nil {
				transport.Proxy = http.ProxyURL(proxyURL)
			}
		}
	}
	// Mirror the search requests: when insecure TLS is allowed (the default for
	// self-hosted deployments behind an intercepting proxy), the plain resolver
	// must accept the same certificates the browser does.
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: query.Insecure}
	return &http.Client{
		Transport:     transport,
		Timeout:       resolveTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// newInsecureRedirectClient is the fallback used when the trust store cannot
// verify the engine certificate (intercepting proxy deployments).
func newInsecureRedirectClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{
		Transport:     transport,
		Timeout:       resolveTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// So360 implements core.SearchEngine for 360 Search SERP pages.
type So360 struct {
	core.Browser
	core.SearchEngineOptions
	logger *core.EngineLogger
	// gate remembers a recent browser-side challenge so the plain-HTTP path is
	// preferred while it is active.
	gate *core.CaptchaGate
}

// New creates a 360 Search engine instance with browser/runtime options applied.
func New(browser core.Browser, opts core.SearchEngineOptions) *So360 {
	eng := So360{Browser: browser, gate: &core.CaptchaGate{}}
	opts.Init()
	eng.SearchEngineOptions = opts
	eng.logger = core.NewEngineLogger("360")
	return &eng
}

// Name returns the stable engine identifier.
func (eng *So360) Name() string {
	return "so360"
}

// Search executes a 360 Search web search and returns normalized results.
// When the browser session is challenged (captcha / timeout / block), it falls
// back to the plain-HTTP path, which 360 gates far less aggressively.
func (eng *So360) Search(ctx context.Context, query core.Query) ([]core.SearchResult, error) {
	rawQuery := query
	rawQuery.Insecure = rawQuery.Insecure || eng.Browser.Insecure

	// While the browser path is known to be challenged, try the plain-HTTP path
	// first: it stays usable much longer on a shared exit IP.
	if eng.gate.Blocked() {
		if raw, rawErr := SearchRaw(ctx, rawQuery); rawErr == nil && len(raw) > 0 {
			return raw, nil
		}
	}

	results, err := eng.searchBrowser(ctx, query)
	if err == nil {
		eng.gate.Clear()
		return results, nil
	}
	if errors.Is(err, core.ErrCaptcha) || errors.Is(err, core.ErrSearchTimeout) || errors.Is(err, core.ErrBlocked) || errors.Is(err, core.ErrParser) {
		eng.gate.Trip(0)
		if raw, rawErr := SearchRaw(ctx, rawQuery); rawErr == nil && len(raw) > 0 {
			eng.logger.WithRequest(ctx).Info("360 browser path failed (%v); served %d results over HTTP", err, len(raw))
			return raw, nil
		}
	}
	return nil, err
}

func (eng *So360) searchBrowser(ctx context.Context, query core.Query) (results []core.SearchResult, err error) {
	ctx = core.PrepareEngineContext(ctx, query, eng.Name())
	if profileID := strings.TrimSpace(eng.ProfileID); profileID != "" {
		ctx = core.WithForcedProfileID(ctx, profileID)
	}
	scoped := *eng
	scoped.logger = eng.logger.WithRequest(ctx)
	if scoped.Browser.WaitLoadTime == 0 || scoped.Browser.WaitLoadTime > 250*time.Millisecond {
		scoped.Browser.WaitLoadTime = 250 * time.Millisecond
	}
	eng = &scoped

	eng.logger.Debug("Starting search, query: %+v", query)

	searchURL, err := BuildURL(query)
	if err != nil {
		return nil, err
	}

	page, err := eng.Navigate(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	defer core.DeferClosePage(ctx, page, &eng.Browser)()

	if err := eng.classifyBlockPage(page, searchURL); err != nil {
		return nil, err
	}

	parsed, err := eng.waitForParsedSearchResults(ctx, page, searchURL, query.Limit)
	if err != nil {
		return nil, err
	}

	for i := range parsed {
		if parsed[i].AbsoluteRank > 0 {
			parsed[i].AbsoluteRank += query.Start
		}
		if parsed[i].Ad {
			continue
		}
		parsed[i].Rank = query.Start + parsed[i].Rank
	}

	parsed = eng.resolveRedirects(ctx, parsed, query)
	parsed = core.StripResultFeatures(parsed, query.Features)
	parsed = core.LimitOrganicResults(parsed, query.Limit)
	return parsed, nil
}

// SearchImage reports that 360 image search is not implemented yet; callers
// get a typed engine error instead of an empty (and misleading) result set.
func (eng *So360) SearchImage(_ context.Context, _ core.Query) ([]core.SearchResult, error) {
	return nil, fmt.Errorf("%w: 360 image search is not supported", core.ErrNotImplemented)
}

// looksLikeSo360Challenge reports whether the live URL is 360's verification
// or captcha interstitial (qcaptcha.so.com, /captcha, /verify).
func looksLikeSo360Challenge(raw string) bool {
	lowered := strings.ToLower(raw)
	if lowered == "" {
		return false
	}
	for _, marker := range []string{"qcaptcha", "captcha", "/verify", "wappass", "antispider"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// classifyBlockPage returns ErrCaptcha for 360's verification pages.
func (eng *So360) classifyBlockPage(page *rod.Page, searchURL string) error {
	if page != nil {
		if info, err := page.Info(); err == nil {
			// 360 answers throttled clients with a redirect to qcaptcha.so.com,
			// where the path is "/" and only the host says captcha.
			if looksLikeSo360Challenge(info.URL) {
				eng.logger.Error("Captcha detected: %s", info.URL)
				return core.ErrCaptcha
			}
		}
	}
	pageErr := core.ClassifyFromPage(page, classifySo360Document)
	if errors.Is(pageErr, core.ErrEmptyResult) {
		return nil
	}
	if pageErr != nil {
		eng.logger.Error("Page classified as %v: %s", pageErr, searchURL)
	}
	return pageErr
}

func (eng *So360) waitForParsedSearchResults(ctx context.Context, page *rod.Page, searchURL string, limit int) ([]core.SearchResult, error) {
	timeout := eng.GetSelectorTimeout()
	if timeout < minDomWait {
		// These SERPs hydrate result cards after first paint; a 5s ceiling under
		// load is too tight and shows up as spurious timeouts.
		timeout = minDomWait
	}
	deadline := time.Now().Add(timeout)
	// 360 interleaves rich modules (AI answer, news, games) with organic rows, so
	// the requested count is often unreachable: stop once the DOM stops growing.
	stableFor := 1200 * time.Millisecond
	var (
		best        []core.SearchResult
		bestOrganic int
		lastChange  = time.Now()
		sawResults  bool
		lastErr     error
		scrolled    bool
	)
	pollStart := time.Now()
	target := limit
	if target <= 0 || target > resultsPerPage {
		target = resultsPerPage
	}

	for {
		html, err := page.HTML()
		if err == nil {
			results, parseErr := ParseHTML(strings.NewReader(html))
			if parseErr == nil {
				if organic := countOrganic(results); organic > bestOrganic {
					best, bestOrganic, lastChange = results, organic, time.Now()
				}
				if bestOrganic >= target {
					return best, nil
				}
			} else if !errors.Is(parseErr, core.ErrEmptyResult) {
				eng.logger.Error("Page classified as %v: %s", parseErr, searchURL)
				return nil, parseErr
			} else {
				lastErr = parseErr
			}
		} else {
			lastErr = err
			if blockErr := eng.classifyBlockPage(page, searchURL); blockErr != nil {
				return nil, blockErr
			}
		}

		if core.HasAnySelector(page, []string{Selectors.Results}) {
			sawResults = true
		}
		if !scrolled && time.Since(pollStart) > time.Second {
			// Nudge lazy loading: both engines render below-the-fold cards only
			// after the viewport moves.
			if _, err := page.Eval(`() => window.scrollTo(0, document.body.scrollHeight)`); err == nil {
				scrolled = true
			}
		}
		if bestOrganic > 0 && time.Since(lastChange) > stableFor {
			return best, nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		if err := core.SleepContext(ctx, 120*time.Millisecond); err != nil {
			return nil, err
		}
	}

	if bestOrganic > 0 {
		return best, nil
	}
	if sawResults || (lastErr != nil && !errors.Is(lastErr, core.ErrEmptyResult)) {
		if lastErr != nil {
			eng.logger.Debug("360 result containers found but HTML parsing failed: %v", lastErr)
		} else {
			eng.logger.Debug("360 result containers found but no parseable organic results")
		}
		return nil, core.ErrParser
	}
	if info, infoErr := page.Info(); infoErr == nil {
		eng.logger.Debug("360 timeout: last url=%s", info.URL)
	}
	if html, htmlErr := page.HTML(); htmlErr == nil {
		eng.logger.Debug("360 timeout: html bytes=%d hasVerify=%t hasCaptcha=%t", len(html), strings.Contains(html, "验证"), strings.Contains(strings.ToLower(html), "captcha"))
	}
	eng.logger.Debug("No result containers or block markers within selector timeout")
	return nil, core.ErrSearchTimeout
}

// countOrganic reports how many organic rows a parse produced.
func countOrganic(results []core.SearchResult) int {
	total := 0
	for _, result := range results {
		if !result.Ad {
			total++
		}
	}
	return total
}

// resolveRedirects unwraps 360's /link?m= redirects for cards that expose no
// data-mdurl destination.
func (eng *So360) resolveRedirects(ctx context.Context, results []core.SearchResult, query core.Query) []core.SearchResult {
	query.Insecure = query.Insecure || eng.Browser.Insecure
	targets := make([]int, 0, len(results))
	for i, result := range results {
		if result.Ad || result.URL == "" {
			continue
		}
		if len(targets) >= maxRedirectResolves(query) {
			break
		}
		u, err := url.Parse(result.URL)
		if err != nil || u.Hostname() != "www.so.com" || !strings.HasPrefix(u.Path, "/link") {
			continue
		}
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return results
	}

	client := newRedirectClient(query)

	sem := make(chan struct{}, resolveConcurrency)
	var wg sync.WaitGroup
	for _, idx := range targets {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reqCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, results[idx].URL, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", redirectUserAgent)
			req.Header.Set("Referer", "https://www.so.com/")
			resp, err := client.Do(req)
			if err != nil && strings.Contains(err.Error(), "x509") {
				// The deployment may sit behind an intercepting proxy whose root CA
				// is not in the container trust store; the browser is configured the
				// same way, so retry once without verification.
				resp, err = newInsecureRedirectClient().Do(req.Clone(reqCtx))
			}
			if err != nil {
				return
			}
			defer core.DrainAndCloseResponse(resp)

			location := strings.TrimSpace(resp.Header.Get("Location"))
			if location == "" {
				return
			}
			target, err := url.Parse(location)
			if err != nil || target.Hostname() == "" {
				return
			}
			if !strings.EqualFold(target.Scheme, "http") && !strings.EqualFold(target.Scheme, "https") {
				return
			}
			results[idx].URL = target.String()
		}(idx)
	}
	wg.Wait()
	return results
}