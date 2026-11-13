// Package sogou implements a Sogou (搜狗) web SERP scraper.
//
// Sogou is one of the major mainland-China search engines. The SERP is a
// server-rendered page with JS hydration for some card types, so the engine
// drives a real browser and parses the resulting DOM with the shared goquery
// parser (also exposed as POST /sogou/parse).
package sogou

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/karust/openserp/core"
)

// minDomWait is the floor for waiting on hydrated result cards.
const minDomWait = 8 * time.Second

// resolveTimeout bounds one Sogou /link redirect lookup.
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

// sogouRedirectPatterns extract the destination from Sogou's interstitial
// page: it answers /link?url= with 200 + a JS/meta redirect (never a 302).
var sogouRedirectPatterns = []*regexp.Regexp{
	regexp.MustCompile(`window\.location\.replace\(\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)URL\s*=\s*['"]([^'"]+)['"]`),
	regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']?refresh["']?[^>]+content=["'][^"']*url=([^"'&>]+)`),
}

// maxRedirectBody bounds how much of the interstitial page is read.
const maxRedirectBody = 8 << 10

// redirectUserAgent keeps the interstitial request a plain, first-party page
// load; Sogou answers oddly to bare Go clients.
const redirectUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// newRedirectClient builds a plain net/http client that never follows
// redirects, so the interstitial response can be inspected. The fingerprint
// transport used for search requests follows redirects internally, which hides
// both the Location header and the interstitial body.
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

// Sogou implements core.SearchEngine for Sogou SERP pages.
type Sogou struct {
	core.Browser
	core.SearchEngineOptions
	logger *core.EngineLogger
}

// New creates a Sogou engine instance with browser/runtime options applied.
func New(browser core.Browser, opts core.SearchEngineOptions) *Sogou {
	sog := Sogou{Browser: browser}
	opts.Init()
	sog.SearchEngineOptions = opts
	sog.logger = core.NewEngineLogger("Sogou")
	return &sog
}

// Name returns the stable engine identifier.
func (sog *Sogou) Name() string {
	return "sogou"
}

// Search executes a Sogou web search and returns normalized search results.
// It may return core.ErrCaptcha or core.ErrSearchTimeout.
func (sog *Sogou) Search(ctx context.Context, query core.Query) (results []core.SearchResult, err error) {
	ctx = core.PrepareEngineContext(ctx, query, sog.Name())
	if profileID := strings.TrimSpace(sog.ProfileID); profileID != "" {
		ctx = core.WithForcedProfileID(ctx, profileID)
	}
	scoped := *sog
	scoped.logger = sog.logger.WithRequest(ctx)
	if scoped.Browser.WaitLoadTime == 0 || scoped.Browser.WaitLoadTime > 250*time.Millisecond {
		scoped.Browser.WaitLoadTime = 250 * time.Millisecond
	}
	sog = &scoped

	sog.logger.Debug("Starting search, query: %+v", query)

	searchURL, err := BuildURL(query)
	if err != nil {
		return nil, err
	}

	page, err := sog.Navigate(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	defer core.DeferClosePage(ctx, page, &sog.Browser)()

	if err := sog.classifyBlockPage(page, searchURL); err != nil {
		return nil, err
	}

	parsed, err := sog.waitForParsedSearchResults(ctx, page, searchURL, query.Limit)
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

	parsed = sog.resolveRedirects(ctx, parsed, query)
	parsed = core.StripResultFeatures(parsed, query.Features)
	parsed = core.LimitOrganicResults(parsed, query.Limit)
	return parsed, nil
}

// SearchImage reports that Sogou image search is not implemented yet; callers
// get a typed engine error instead of an empty (and misleading) result set.
func (sog *Sogou) SearchImage(_ context.Context, _ core.Query) ([]core.SearchResult, error) {
	return nil, fmt.Errorf("%w: sogou image search is not supported", core.ErrNotImplemented)
}

// classifyBlockPage returns ErrCaptcha for Sogou's anti-spider challenge pages.
// ErrEmptyResult is not a block: the caller treats a parsed empty page as zero
// results.
func (sog *Sogou) classifyBlockPage(page *rod.Page, searchURL string) error {
	if page != nil {
		if info, err := page.Info(); err == nil {
			// Sogou redirects flagged clients to /captcha or /antispider pages.
			if lowered := strings.ToLower(info.URL); strings.Contains(lowered, "antispider") || strings.Contains(lowered, "captcha") {
				sog.logger.Error("Captcha detected: %s", info.URL)
				return core.ErrCaptcha
			}
		}
	}
	pageErr := core.ClassifyFromPage(page, classifySogouDocument)
	if errors.Is(pageErr, core.ErrEmptyResult) {
		return nil
	}
	if pageErr != nil {
		sog.logger.Error("Page classified as %v: %s", pageErr, searchURL)
	}
	return pageErr
}

func (sog *Sogou) waitForParsedSearchResults(ctx context.Context, page *rod.Page, searchURL string, limit int) ([]core.SearchResult, error) {
	timeout := sog.GetSelectorTimeout()
	if timeout < minDomWait {
		// These SERPs hydrate result cards after first paint; a 5s ceiling under
		// load is too tight and shows up as spurious timeouts.
		timeout = minDomWait
	}
	deadline := time.Now().Add(timeout)
	// Sogou hydrates some cards after the first paint, so keep polling until the
	// requested number of organic rows is visible or the DOM stops growing.
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
				sog.logger.Error("Page classified as %v: %s", parseErr, searchURL)
				return nil, parseErr
			} else {
				lastErr = parseErr
			}
		} else {
			lastErr = err
			if blockErr := sog.classifyBlockPage(page, searchURL); blockErr != nil {
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
	if sawResults || !errors.Is(lastErr, core.ErrEmptyResult) && lastErr != nil {
		if lastErr != nil {
			sog.logger.Debug("Sogou result containers found but HTML parsing failed: %v", lastErr)
		} else {
			sog.logger.Debug("Sogou result containers found but no parseable organic results")
		}
		return nil, core.ErrParser
	}
	sog.logger.Debug("No result containers or block markers within selector timeout")
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

// resolveRedirects replaces Sogou's opaque /link?url= wrappers with the real
// destination, so callers get usable URLs and accurate domains. Failures keep
// the wrapper (it still redirects when followed).
func (sog *Sogou) resolveRedirects(ctx context.Context, results []core.SearchResult, query core.Query) []core.SearchResult {
	targets := make([]int, 0, len(results))
	for i, result := range results {
		if result.Ad || result.URL == "" {
			continue
		}
		if len(targets) >= maxRedirectResolves(query) {
			break
		}
		u, err := url.Parse(result.URL)
		if err != nil || u.Hostname() != "www.sogou.com" || !strings.HasPrefix(u.Path, "/link") {
			continue
		}
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return results
	}
	query.Insecure = query.Insecure || sog.Browser.Insecure
	sog.logger.Info("sogou redirect: resolving %d of %d results", len(targets), len(results))

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
			req.Header.Set("Referer", "https://www.sogou.com/")
			resp, err := client.Do(req)
			if err != nil && strings.Contains(err.Error(), "x509") {
				// The deployment may sit behind an intercepting proxy whose root CA
				// is not in the container trust store; the browser is configured the
				// same way, so retry once without verification.
				resp, err = newInsecureRedirectClient().Do(req.Clone(reqCtx))
			}
			if err != nil {
				sog.logger.Warn("sogou redirect request failed: %v", err)
				return
			}
			defer core.DrainAndCloseResponse(resp)

			target := sogouRedirectTarget(resp)
			if target == "" {
				sog.logger.Info("sogou redirect: no target (status %d)", resp.StatusCode)
				return
			}
			results[idx].URL = target
		}(idx)
	}
	wg.Wait()
	return results
}
// sogouRedirectTarget returns the destination carried by a Sogou /link
// response: the Location header when present, otherwise the URL embedded in the
// interstitial page's JS/meta redirect.
func sogouRedirectTarget(resp *http.Response) string {
	if location := strings.TrimSpace(resp.Header.Get("Location")); location != "" {
		if target := absoluteExternalURL(location); target != "" {
			return target
		}
	}
	if resp.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRedirectBody))
	if err != nil || len(body) == 0 {
		return ""
	}
	page := string(body)
	for _, pattern := range sogouRedirectPatterns {
		match := pattern.FindStringSubmatch(page)
		if len(match) < 2 {
			continue
		}
		if target := absoluteExternalURL(match[1]); target != "" {
			return target
		}
	}
	return ""
}

// absoluteExternalURL validates an http(s) destination that is not another
// Sogou redirect.
func absoluteExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return ""
	}
	if host := strings.ToLower(u.Hostname()); host == "www.sogou.com" || host == "sogou.com" {
		return ""
	}
	return u.String()
}