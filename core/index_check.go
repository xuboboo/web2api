// Package-level index (收录) checking: decides whether a target URL appears in
// an engine's own SERP when the URL itself is used as the search phrase.
//
// Pipeline per (URL × engine) unit:
//  1. CheckIndexWithProtection routes the lookup through the resilient search
//     pipeline (per-engine rate limiter, circuit breaker, retry, proxy policy,
//     panic recovery) into the engine's CheckIndexed implementation, which
//     reuses the engine's existing Search/search_raw path unchanged.
//  2. EvaluateIndexSERP matches the parsed candidates against the target using
//     the platform normalization rules, resolving opaque wrapped links
//     (baidu.com/link?url=, sogou.com/link, so.com/link) through the existing
//     raw-click redirect machinery when offline unwrapping is not enough.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IndexCheckLimit is the SERP depth requested for one index lookup: engines
// search with the target URL as the query phrase and keep the first 10 organic
// results as candidates.
const IndexCheckLimit = 10

// MaxIndexCheckURLs is the platform contract maximum for one /v1/index/check
// request (1..20 URLs).
const MaxIndexCheckURLs = 20

// IndexMatchExact / IndexMatchNone are the only match values defined by the
// platform contract.
const (
	IndexMatchExact = "exact"
	IndexMatchNone  = "none"
)

// IndexChecker is implemented by engines that can answer "is this URL
// indexed". runtime carries the proxy/TLS settings selected by the resilience
// layer for this attempt and must be merged into the SERP query.
type IndexChecker interface {
	CheckIndexed(ctx context.Context, targetURL string, runtime Query) ([]SearchResult, error)
}

// IndexCheckQuery builds the SERP query for one index lookup. queryText is the
// target URL itself (baidu/sogou/so360) or "url:<url>" (bing). runtime carries
// proxy/TLS options chosen by the resilience layer; everything that would
// broaden or filter the SERP (pagination, features, site/filetype/date) is
// reset so the SERP is exactly the engine's answer to "where does this URL
// rank".
func IndexCheckQuery(queryText string, runtime Query) Query {
	q := runtime
	q.Text = queryText
	q.Limit = IndexCheckLimit
	q.Start = 0
	q.Features = false
	q.Filter = false
	q.DateInterval = ""
	q.Filetype = ""
	q.Site = ""
	// Let engine-local wrapped-link resolution (sogou/so360 /link redirect
	// resolvers) unwrap candidates in-engine, with the same proxy the SERP used.
	q.ResolveURLs = true
	q.ResolveTop = IndexCheckLimit
	return q
}

// IndexCheckEngineError is the per-unit failure payload: engine-level failures
// (403/429/captcha/circuit open/cooldown) never fail the whole request.
type IndexCheckEngineError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// RetryAfterSeconds is set on engine_cooldown units (契约 A batch path).
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
}

// IndexCheckSuccess is the per-unit verdict.
type IndexCheckSuccess struct {
	Indexed          bool   `json:"indexed"`
	Match            string `json:"match"`
	MatchedResultURL string `json:"matched_result_url"`
	TotalResults     int    `json:"total_results"`
	TookMs           int64  `json:"took_ms"`
}

// IndexCheckEngineOutcome marshals to either the success shape
// {"indexed","match","matched_result_url","total_results","took_ms"} or
// {"error":{"code","message"}} — never a mix.
type IndexCheckEngineOutcome struct {
	// success is set when the engine produced a verdict.
	success *IndexCheckSuccess
	// err is set when the engine failed for this unit.
	err *IndexCheckEngineError
}

// MarshalJSON implements the contract's union shape.
func (o IndexCheckEngineOutcome) MarshalJSON() ([]byte, error) {
	if o.err != nil {
		return json.Marshal(struct {
			Error *IndexCheckEngineError `json:"error"`
		}{Error: o.err})
	}
	if o.success == nil {
		// Unreachable through RunIndexCheckUnits; kept as a defensive default.
		return json.Marshal(struct {
			Error *IndexCheckEngineError `json:"error"`
		}{Error: &IndexCheckEngineError{Code: "engine_internal", Message: "no verdict produced"}})
	}
	return json.Marshal(o.success)
}

// UnmarshalJSON is the inverse of MarshalJSON so clients (and tests) can round
// trip the union shape.
func (o *IndexCheckEngineOutcome) UnmarshalJSON(data []byte) error {
	var probe struct {
		Error            *IndexCheckEngineError `json:"error"`
		Indexed          bool                   `json:"indexed"`
		Match            string                 `json:"match"`
		MatchedResultURL string                 `json:"matched_result_url"`
		TotalResults     int                    `json:"total_results"`
		TookMs           int64                  `json:"took_ms"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Error != nil {
		*o = IndexCheckEngineOutcome{err: probe.Error}
		return nil
	}
	*o = IndexCheckEngineOutcome{success: &IndexCheckSuccess{
		Indexed:          probe.Indexed,
		Match:            probe.Match,
		MatchedResultURL: probe.MatchedResultURL,
		TotalResults:     probe.TotalResults,
		TookMs:           probe.TookMs,
	}}
	return nil
}

// IndexCheckURLResult is one URL's per-engine verdicts.
type IndexCheckURLResult struct {
	URL     string                             `json:"url"`
	Engines map[string]IndexCheckEngineOutcome `json:"engines"`
}

// IndexCheckMeta is the batch-level metadata block.
type IndexCheckMeta struct {
	RequestID string `json:"request_id"`
	TookMs    int64  `json:"took_ms"`
	Units     int    `json:"units"`
	// EnginesCooldown lists the engines currently in an IP cooldown at
	// response time (契约 A): omitted when no engine is cooling.
	EnginesCooldown []EngineCooldownEntry `json:"engines_cooldown,omitempty"`
	// Cache is set to "hit" when the response came from the index-check
	// response cache (S4-GAP 性能项, additive; omitted on live engine runs).
	Cache string `json:"cache,omitempty"`
}

// IndexCheckResponse is the /v1/index/check response body.
type IndexCheckResponse struct {
	ID        string                `json:"id"`
	CreatedAt string                `json:"created_at"`
	Results   []IndexCheckURLResult `json:"results"`
	Meta      IndexCheckMeta        `json:"meta"`
}

// IndexCheckFetcher performs one opaque-redirect lookup. It mirrors the
// rawClickRequest signature so the production fetcher can be swapped in tests.
type IndexCheckFetcher func(ctx context.Context, wrappedURL string, q Query) (*http.Response, error)

// indexRedirectFetch is the production opaque-redirect fetcher; tests swap it.
var indexRedirectFetch IndexCheckFetcher = rawClickRequest

// indexResolveTimeout bounds one wrapped-link redirect lookup.
const indexResolveTimeout = 3 * time.Second

// IndexResolveOptions tunes candidate resolution inside EvaluateIndexSERP.
type IndexResolveOptions struct {
	// Query carries proxy/TLS settings for the redirect lookup (the same
	// settings the SERP fetch used).
	Query Query
	// Fetch overrides the opaque-redirect fetcher; nil uses production.
	// Test-only hook so unit tests never touch the network.
	Fetch IndexCheckFetcher
}

// EvaluateIndexSERP decides whether targetURL is present in the parsed SERP
// candidates. Pass 1 matches purely offline (Bing click / Google plaintext
// unwrapping + normalization); pass 2 resolves opaque wrapped links one at a
// time and stops at the first match. The verdict is always non-nil: a SERP
// without the target yields indexed=false, match="none", and the organic
// result count as TotalResults.
func EvaluateIndexSERP(ctx context.Context, targetURL string, results []SearchResult, opts IndexResolveOptions) IndexCheckSuccess {
	verdict := IndexCheckSuccess{Match: IndexMatchNone}
	target, ok := indexURLKeyFrom(targetURL)
	if !ok {
		return verdict
	}
	verdict.TotalResults = CountOrganicResults(results)

	// Pass 1: offline normalization only.
	for _, r := range results {
		if r.Ad || r.URL == "" {
			continue
		}
		candidate := unwrapIndexCandidateOffline(r.URL)
		if key, ok := indexURLKeyFrom(candidate); ok && indexURLsMatch(target, key) {
			verdict.Indexed = true
			verdict.Match = IndexMatchExact
			verdict.MatchedResultURL = candidate
			return verdict
		}
	}

	// Pass 2: resolve opaque wrapped links (network) until first match.
	fetch := opts.Fetch
	if fetch == nil {
		fetch = indexRedirectFetch
	}
	resolvedCache := map[string]string{}
	for _, r := range results {
		if r.Ad || r.URL == "" {
			continue
		}
		candidate := unwrapIndexCandidateOffline(r.URL)
		if !isOpaqueWrappedIndexURL(candidate) {
			continue
		}
		resolved, ok := resolvedCache[candidate]
		if !ok {
			if dest, err := resolveOpaqueIndexRedirect(ctx, candidate, opts.Query, fetch); err == nil {
				resolved = dest
			}
			resolvedCache[candidate] = resolved
		}
		if resolved == "" {
			continue
		}
		if key, ok := indexURLKeyFrom(resolved); ok && indexURLsMatch(target, key) {
			verdict.Indexed = true
			verdict.Match = IndexMatchExact
			verdict.MatchedResultURL = resolved
			return verdict
		}
	}
	return verdict
}

// indexURLKey is the canonical comparison key for one URL.
type indexURLKey struct {
	host     string
	path     string
	query    string // decoded, key=value pairs sorted and joined with "&"
	hasQuery bool
}

// indexURLKeyFrom applies the platform normalization rules: scheme and host
// lowercased, default port removed, fragment removed, trailing slash removed
// (root path keeps its slash), percent-encoding decoded. A URL without a
// scheme is treated as http. It fails for input that cannot carry a host.
func indexURLKeyFrom(raw string) (indexURLKey, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return indexURLKey{}, false
	}
	if strings.HasPrefix(raw, "//") {
		raw = "http:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return indexURLKey{}, false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return indexURLKey{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if port := u.Port(); port != "" {
		defaultPort := (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
		if !defaultPort {
			host = host + ":" + port
		}
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
	}

	key := indexURLKey{host: host, path: path}
	if u.RawQuery != "" {
		key.hasQuery = true
		key.query = canonicalIndexQuery(u.Query())
	}
	return key, true
}

// canonicalIndexQuery renders decoded query parameters as sorted "k=v" pairs,
// matching the contract's "有 query 时参数排序后须相等" rule.
func canonicalIndexQuery(values url.Values) string {
	pairs := make([]string, 0, len(values))
	for name, vs := range values {
		if len(vs) == 0 {
			pairs = append(pairs, name+"=")
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, name+"="+v)
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// indexURLsMatch reports whether candidate is the target URL under the
// platform rules: host and path must be equal; when the target carries a query
// string the candidate's sorted parameters must be equal too, and when it does
// not, the candidate's query is ignored.
func indexURLsMatch(target, candidate indexURLKey) bool {
	if target.host != candidate.host || target.path != candidate.path {
		return false
	}
	if target.hasQuery && target.query != candidate.query {
		return false
	}
	return true
}

// unwrapIndexCandidateOffline unwraps a SERP result URL without any network
// I/O: Bing /ck/a click tokens decode to their destination. Deliberately NOT
// normalizeURL: that strips tracking parameters, which would break the
// contract's query rule ("有 query 时参数排序后须相等") for candidates whose
// query legitimately carries a tracking-style parameter.
func unwrapIndexCandidateOffline(raw string) string {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return ""
	}
	if unwrapped := UnwrapBingURL(candidate); unwrapped != "" {
		return unwrapped
	}
	return candidate
}

// isOpaqueWrappedIndexURL reports whether raw is an engine redirect whose
// destination is only discoverable by following it (baidu.com/link?url=,
// sogou.com/link, so.com/link and their www./m. host variants).
func isOpaqueWrappedIndexURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "baidu.com", "sogou.com", "so.com":
	default:
		return false
	}
	path := strings.ToLower(u.Path)
	return path == "/link" || strings.HasPrefix(path, "/link?")
}

// resolveOpaqueIndexRedirect follows one opaque engine redirect and returns the
// destination. It reuses the raw-click request machinery (fingerprint TLS
// profile, proxy support, no redirect following) and reads the Location
// header. Sogou's 200+JS interstitial is resolved in-engine during Search, so
// a response without Location is treated as a failure and the caller keeps the
// wrapper.
func resolveOpaqueIndexRedirect(ctx context.Context, wrapped string, q Query, fetch IndexCheckFetcher) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, indexResolveTimeout)
	defer cancel()
	resp, err := fetch(ctx, wrapped, q)
	if err != nil {
		return "", err
	}
	defer DrainAndCloseResponse(resp)
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: wrapped link resolve HTTP %d", ErrBlocked, resp.StatusCode)
	}
	location, ok := redirectLocation(resp)
	if !ok {
		return "", fmt.Errorf("wrapped link resolve: HTTP %d without redirect", resp.StatusCode)
	}
	dest, err := resolveRedirectURL(wrapped, location)
	if err != nil {
		return "", err
	}
	if !IsUsableResultURL(dest) {
		return "", fmt.Errorf("wrapped link resolve: %q is not a destination", dest)
	}
	return dest, nil
}

// indexLookupEngine adapts one CheckIndexed call to the SearchEngine shape so
// the lookup rides the full resilient pipeline (rate limiter, circuit breaker,
// retry, proxy policy, panic recovery) unchanged.
type indexLookupEngine struct {
	engine    SearchEngine
	targetURL string

	mu      sync.Mutex
	runtime Query
}

func (l *indexLookupEngine) Name() string        { return l.engine.Name() }
func (l *indexLookupEngine) IsInitialized() bool { return l.engine.IsInitialized() }
func (l *indexLookupEngine) GetRateLimiter() *rate.Limiter {
	return l.engine.GetRateLimiter()
}

func (l *indexLookupEngine) Search(ctx context.Context, q Query) ([]SearchResult, error) {
	checker, ok := l.engine.(IndexChecker)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not support index checks", ErrNotImplemented, l.engine.Name())
	}
	l.mu.Lock()
	l.runtime = q
	l.mu.Unlock()
	return checker.CheckIndexed(ctx, l.targetURL, q)
}

// runtimeQuery returns the runtime query of the last attempt (proxy settings
// included) for candidate resolution after a successful lookup.
func (l *indexLookupEngine) runtimeQuery() Query {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.runtime
}

// CheckIndexWithProtection runs one index lookup through the resilient search
// pipeline. It returns the parsed SERP candidates plus the runtime query of
// the last attempt so callers can resolve wrapped candidate links with the
// same proxy the SERP used.
func (rs *ResilientSearcher) CheckIndexWithProtection(ctx context.Context, engine SearchEngine, targetURL string) ([]SearchResult, Query, error) {
	lookup := &indexLookupEngine{engine: engine, targetURL: targetURL}
	results, _, err := rs.searchWithProtection(ctx, lookup, Query{})
	return results, lookup.runtimeQuery(), err
}

// indexCheckWorkerLimit caps concurrent (URL × engine) units. Per-engine load
// is additionally serialized by each engine's own rate limiter.
const indexCheckWorkerLimit = 8

// RunIndexCheckUnits evaluates every URL against every engine concurrently
// (up to indexCheckWorkerLimit workers) and returns one result row per URL in
// input order. Engine-level failures become per-unit error outcomes; the batch
// itself never fails. Cooling engines fail their units fast (engine_cooldown)
// without touching the engine; the single cooldown-expiry probe rides the
// first unit that reaches an expired engine.
func RunIndexCheckUnits(ctx context.Context, rs *ResilientSearcher, engines []SearchEngine, urls []string) []IndexCheckURLResult {
	out := make([]IndexCheckURLResult, len(urls))
	for i := range out {
		out[i] = IndexCheckURLResult{URL: urls[i], Engines: map[string]IndexCheckEngineOutcome{}}
	}
	if len(urls) == 0 || len(engines) == 0 || rs == nil {
		return out
	}

	type unit struct{ urlIdx, engineIdx int }
	units := make(chan unit)
	var (
		wg      sync.WaitGroup
		writeMu sync.Mutex
	)
	workers := indexCheckWorkerLimit
	if total := len(urls) * len(engines); workers > total {
		workers = total
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range units {
				engine := engines[u.engineIdx]
				startedAt := time.Now()
				var outcome IndexCheckEngineOutcome

				gate, gateErr := rs.GateEngine(ctx, engine.Name())
				switch {
				case gateErr != nil:
					outcome = indexCheckErrorOutcome(ctx, gateErr)
				case gate.Blocked():
					// 契约 A batch semantics: cooling engines answer per-unit
					// engine_cooldown errors; the other engines still judge.
					outcome = cooldownUnitOutcome(gate)
				default:
					var probeErr error
					if gate.IsProbe() {
						probeErr = errProbeIncomplete
					}
					serp, runtimeQ, err := rs.CheckIndexWithProtection(ctx, engine, out[u.urlIdx].URL)
					if gate.IsProbe() {
						probeErr = err
						gate.FinishProbe(probeErr)
					}
					if err != nil {
						outcome = indexCheckErrorOutcome(ctx, err)
					} else {
						success := EvaluateIndexSERP(ctx, out[u.urlIdx].URL, serp, IndexResolveOptions{Query: runtimeQ})
						outcome = IndexCheckEngineOutcome{success: &success}
					}
				}
				outcome.success.setTookIfSuccess(time.Since(startedAt).Milliseconds())

				writeMu.Lock()
				out[u.urlIdx].Engines[engine.Name()] = outcome
				writeMu.Unlock()
			}
		}()
	}
	for ui := range urls {
		for ei := range engines {
			units <- unit{urlIdx: ui, engineIdx: ei}
		}
	}
	close(units)
	wg.Wait()
	return out
}

// setTookIfSuccess stamps TookMs on success verdicts only.
func (s *IndexCheckSuccess) setTookIfSuccess(tookMs int64) {
	if s == nil {
		return
	}
	s.TookMs = tookMs
}

// indexCheckErrorOutcome maps an engine failure to the per-unit error payload
// using the same error taxonomy as the search endpoints. B-08 治理: message
// comes from the fixed taxonomy text (never the raw error string, which may
// carry internal paths or upstream URL fragments); the original error goes to
// the server logs only.
func indexCheckErrorOutcome(ctx context.Context, err error) IndexCheckEngineOutcome {
	spec := mapSearchError(err)
	WithRequest(ctx).WithError(err).
		WithField("code", spec.code).
		Warn("Index check unit failed")
	return IndexCheckEngineOutcome{err: &IndexCheckEngineError{
		Code:    spec.code,
		Message: spec.message,
	}}
}

// IndexCheckEngineNames lists the engines an index-check request may select,
// in registration order.
func IndexCheckEngineNames(engines []SearchEngine) []string {
	names := make([]string, 0, len(engines))
	for _, engine := range engines {
		names = append(names, engine.Name())
	}
	return names
}

// SelectIndexCheckEngines filters available engines by the requested names
// (aliases such as "360" accepted, duplicates dropped). Empty or nil requested
// selects every available engine. Unknown names yield a 400 INVALID_PARAM.
func SelectIndexCheckEngines(available []SearchEngine, requested []string) ([]SearchEngine, *APIError) {
	if len(requested) == 0 {
		return available, nil
	}
	wanted := make(map[string]bool, len(requested))
	for _, raw := range requested {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		wanted[resolveEngineAlias(name)] = true
	}
	selected := make([]SearchEngine, 0, len(wanted))
	for _, engine := range available {
		if wanted[engine.Name()] {
			selected = append(selected, engine)
			delete(wanted, engine.Name())
		}
	}
	if len(wanted) > 0 {
		unknown := make([]string, 0, len(wanted))
		for name := range wanted {
			unknown = append(unknown, name)
		}
		sort.Strings(unknown)
		return nil, errInvalidParam(fmt.Sprintf(
			"unknown engines %s: available engines are %s",
			strings.Join(unknown, ", "), strings.Join(IndexCheckEngineNames(available), ", ")))
	}
	return selected, nil
}
