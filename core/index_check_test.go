package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// --- normalization -------------------------------------------------------

func TestIndexURLKeyFromNormalization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw      string
		ok       bool
		host     string
		path     string
		hasQuery bool
		query    string
	}{
		{raw: "HTTPS://EXAMPLE.com:443/path/", ok: true, host: "example.com", path: "/path"},
		{raw: "http://example.com:80", ok: true, host: "example.com", path: "/"},
		{raw: "https://example.com:8443/x", ok: true, host: "example.com:8443", path: "/x"},
		{raw: "https://example.com/a#section", ok: true, host: "example.com", path: "/a"},
		{raw: "https://example.com/a%20b", ok: true, host: "example.com", path: "/a b"},
		{raw: "example.com/a", ok: true, host: "example.com", path: "/a"},
		{raw: "https://example.com/a?b=2&a=1", ok: true, host: "example.com", path: "/a", hasQuery: true, query: "a=1&b=2"},
		{raw: "  https://example.com/x?token=%2Fabc%3F  ", ok: true, host: "example.com", path: "/x", hasQuery: true, query: "token=/abc?"},
		{raw: "", ok: false},
		{raw: "   ", ok: false},
		{raw: "not a url", ok: false},
		{raw: "http:///nohost", ok: false},
	}
	for _, tc := range cases {
		key, ok := indexURLKeyFrom(tc.raw)
		if ok != tc.ok {
			t.Fatalf("indexURLKeyFrom(%q) ok=%v, want %v", tc.raw, ok, tc.ok)
		}
		if !tc.ok {
			continue
		}
		if key.host != tc.host || key.path != tc.path {
			t.Fatalf("indexURLKeyFrom(%q) = %q/%q, want %q/%q", tc.raw, key.host, key.path, tc.host, tc.path)
		}
		if key.hasQuery != tc.hasQuery || key.query != tc.query {
			t.Fatalf("indexURLKeyFrom(%q) query=%q/%v, want %q/%v", tc.raw, key.query, key.hasQuery, tc.query, tc.hasQuery)
		}
	}
}

// --- matching rules ------------------------------------------------------

func TestIndexURLsMatchContractRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		target    string
		candidate string
		want      bool
	}{
		{"host is case-insensitive", "HTTP://EXAMPLE.com/A", "http://example.com/A", true},
		{"trailing slash removed", "https://example.com/a/", "https://example.com/a", true},
		{"root keeps its slash", "https://example.com/", "https://example.com", true},
		{"percent-encoded path decodes", "https://example.com/a%2Fb", "https://example.com/a/b", true},
		{"target without query ignores candidate query", "https://example.com/a", "https://example.com/a?b=2&a=1", true},
		{"target query sorted equality", "https://example.com/a?b=2&a=1", "https://example.com/a?a=1&b=2", true},
		{"target query mismatch", "https://example.com/a?b=2", "https://example.com/a?b=3", false},
		{"target query vs candidate without query", "https://example.com/a?b=2", "https://example.com/a", false},
		{"different host", "https://example.com/a", "https://other.com/a", false},
		{"different path", "https://example.com/a", "https://example.com/b", false},
		{"default port removed", "http://example.com:80/a", "https://example.com/a", true},
		{"non-default port kept", "http://example.com:8080/a", "http://example.com/a", false},
	}
	for _, tc := range cases {
		target, okT := indexURLKeyFrom(tc.target)
		candidate, okC := indexURLKeyFrom(tc.candidate)
		if !okT || !okC {
			t.Fatalf("%s: keys failed to parse (%v/%v)", tc.name, okT, okC)
		}
		if got := indexURLsMatch(target, candidate); got != tc.want {
			t.Fatalf("%s: match=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- EvaluateIndexSERP ---------------------------------------------------

func TestEvaluateIndexSERPHitAndMiss(t *testing.T) {
	t.Parallel()

	results := []SearchResult{
		{Rank: 1, URL: "https://ads.example.com/first", Title: "ad", Ad: true},
		{Rank: 1, URL: "https://other.org/page", Title: "one"},
		{Rank: 2, URL: "http://TEST.test/dir/page.html", Title: "two"},
	}

	// Hit: host case + trailing slash + fragment + no-target-query rules.
	hit := EvaluateIndexSERP(context.Background(), "https://TEST.test/dir/page.html#top", results, IndexResolveOptions{})
	if !hit.Indexed || hit.Match != IndexMatchExact {
		t.Fatalf("expected exact hit, got %+v", hit)
	}
	if hit.MatchedResultURL == "" {
		t.Fatalf("expected matched_result_url to be recorded, got %+v", hit)
	}
	if hit.TotalResults != 2 {
		t.Fatalf("TotalResults=%d, want 2 (ads excluded)", hit.TotalResults)
	}

	// Target query must be honored when present.
	withQuery := EvaluateIndexSERP(context.Background(), "https://test.test/dir/page.html?id=7", results, IndexResolveOptions{})
	if withQuery.Indexed {
		t.Fatalf("expected miss when target query differs, got %+v", withQuery)
	}

	// Miss: verdict carries the organic count.
	miss := EvaluateIndexSERP(context.Background(), "https://nowhere.example.cn/absent", results, IndexResolveOptions{})
	if miss.Indexed || miss.Match != IndexMatchNone || miss.MatchedResultURL != "" {
		t.Fatalf("expected none verdict, got %+v", miss)
	}
	if miss.TotalResults != 2 {
		t.Fatalf("miss TotalResults=%d, want 2", miss.TotalResults)
	}
}

func TestEvaluateIndexSERPResolvesWrappedLink(t *testing.T) {
	t.Parallel()

	target := "https://example.org/article"
	results := []SearchResult{
		{Rank: 1, URL: "https://www.baidu.com/link?url=opaque-token", Title: "wrapped"},
		{Rank: 2, URL: "https://www.sogou.com/link?url=another", Title: "sogou wrapped"},
	}

	var fetches int
	var mu sync.Mutex
	fetch := func(ctx context.Context, wrapped string, q Query) (*http.Response, error) {
		mu.Lock()
		fetches++
		mu.Unlock()
		if wrapped == "https://www.baidu.com/link?url=opaque-token" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{target}},
				Body:       http.NoBody,
			}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
	}

	hit := EvaluateIndexSERP(context.Background(), "https://Example.org/article/", results, IndexResolveOptions{Fetch: fetch})
	if !hit.Indexed || hit.Match != IndexMatchExact {
		t.Fatalf("expected resolved wrapped link to hit, got %+v", hit)
	}
	if hit.MatchedResultURL != target {
		t.Fatalf("matched_result_url=%q, want %q", hit.MatchedResultURL, target)
	}

	// A direct offline hit must not spend any redirect lookups.
	direct := []SearchResult{{Rank: 1, URL: "https://example.org/article", Title: "direct"}}
	fetchBefore := fetchCalls(t, &mu, &fetches)
	EvaluateIndexSERP(context.Background(), target, direct, IndexResolveOptions{Fetch: fetch})
	if got := fetchCalls(t, &mu, &fetches); got != fetchBefore {
		t.Fatalf("offline hit must not fetch, fetches went %d -> %d", fetchBefore, got)
	}

	// Resolution failure keeps the none verdict.
	miss := EvaluateIndexSERP(context.Background(), target, results, IndexResolveOptions{
		Fetch: func(ctx context.Context, wrapped string, q Query) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		},
	})
	if miss.Indexed || miss.Match != IndexMatchNone {
		t.Fatalf("expected none after failed resolution, got %+v", miss)
	}

	// Wrapper that answers 403 is treated as a failure, not a destination.
	blocked := EvaluateIndexSERP(context.Background(), target, []SearchResult{{Rank: 1, URL: "https://www.so.com/link?m=x"}}, IndexResolveOptions{
		Fetch: func(ctx context.Context, wrapped string, q Query) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
		},
	})
	if blocked.Indexed {
		t.Fatalf("expected none on 403 resolve, got %+v", blocked)
	}
}

func TestIndexSERPContains(t *testing.T) {
	t.Parallel()

	results := []SearchResult{
		{Rank: 1, URL: "https://www.bing.com/ck/a?u=a1" + base64.RawURLEncoding.EncodeToString([]byte("https://www.onlinedown.net/article/10047660.htm")), Title: "wrapped"},
		{Rank: 2, URL: "https://other.org/x", Title: "other"},
	}
	candidate, found := IndexSERPContains("https://www.onlinedown.net/article/10047660.htm", results)
	if !found || candidate == "" {
		t.Fatalf("expected bing click hit, got found=%v candidate=%q", found, candidate)
	}
	if _, found := IndexSERPContains("https://absent.example.cn/x", results); found {
		t.Fatal("unexpected hit for absent target")
	}
}

func fetchCalls(t *testing.T, mu *sync.Mutex, counter *int) int {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	return *counter
}

func TestEvaluateIndexSERPUnwrapsBingClickOffline(t *testing.T) {
	t.Parallel()

	target := "https://go.dev/doc/"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(target))
	results := []SearchResult{
		{Rank: 1, URL: "https://www.bing.com/ck/a?!&&p=abc&u=a1" + encoded + "&ntb=1", Title: "click wrapped"},
	}

	hit := EvaluateIndexSERP(context.Background(), target, results, IndexResolveOptions{
		Fetch: func(ctx context.Context, wrapped string, q Query) (*http.Response, error) {
			t.Fatal("bing click unwrap must be offline")
			return nil, nil
		},
	})
	// matched_result_url is the decoded click destination as-is; the match
	// itself is computed on the contract-normalized form.
	if !hit.Indexed || hit.Match != IndexMatchExact || hit.MatchedResultURL != "https://go.dev/doc/" {
		t.Fatalf("expected offline bing unwrap hit, got %+v", hit)
	}
}

// TestEvaluateIndexSERPKeepsQueryParameters pins the contract rule that the
// candidate's query is compared verbatim (sorted) against the target's — no
// tracking-parameter stripping may sneak into the verdict.
func TestEvaluateIndexSERPKeepsQueryParameters(t *testing.T) {
	t.Parallel()

	equalQuery := []SearchResult{{Rank: 1, URL: "https://example.com/a?ref=x", Title: "row"}}
	hit := EvaluateIndexSERP(context.Background(), "https://example.com/a?ref=x", equalQuery, IndexResolveOptions{
		Fetch: func(ctx context.Context, wrapped string, q Query) (*http.Response, error) {
			t.Fatal("equal query must match offline")
			return nil, nil
		},
	})
	if !hit.Indexed || hit.Match != IndexMatchExact {
		t.Fatalf("expected hit for identical query, got %+v", hit)
	}

	extraQuery := []SearchResult{{Rank: 1, URL: "https://example.com/a?ref=x&utm_source=z", Title: "row"}}
	miss := EvaluateIndexSERP(context.Background(), "https://example.com/a?ref=x", extraQuery, IndexResolveOptions{})
	if miss.Indexed {
		t.Fatalf("sorted params must be equal; candidate has an extra param, got %+v", miss)
	}
}

// --- orchestrator --------------------------------------------------------

type indexEngineMock struct {
	name        string
	initialized bool
	limiter     *rate.Limiter
	serpFn      func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error)

	mu     sync.Mutex
	target []string
}

func (e *indexEngineMock) Name() string        { return e.name }
func (e *indexEngineMock) IsInitialized() bool { return e.initialized }
func (e *indexEngineMock) GetRateLimiter() *rate.Limiter {
	if e.limiter == nil {
		return rate.NewLimiter(rate.Inf, 0)
	}
	return e.limiter
}

func (e *indexEngineMock) Search(ctx context.Context, q Query) ([]SearchResult, error) {
	return nil, fmt.Errorf("unexpected plain Search call for %s", e.name)
}

func (e *indexEngineMock) CheckIndexed(ctx context.Context, targetURL string, runtime Query) ([]SearchResult, error) {
	e.mu.Lock()
	e.target = append(e.target, targetURL)
	e.mu.Unlock()
	if e.serpFn == nil {
		return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
	}
	return e.serpFn(ctx, targetURL, runtime)
}

func (e *indexEngineMock) calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.target...)
}

func TestRunIndexCheckUnitsIsolatesEngineFailures(t *testing.T) {
	t.Parallel()

	healthy := &indexEngineMock{name: "baidu", initialized: true}
	// Parser drift (NOT an interception class): it must neither retry nor trip
	// the cooldown state machine, so both units carry parser_failure and the
	// engine is contacted once per URL. Interception-class failures (captcha/
	// block/429) trip the cooldown instead — covered by
	// TestRunIndexCheckUnitsInterceptionTripsCooldown.
	failing := &indexEngineMock{
		name:        "bing",
		initialized: true,
		serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
			return nil, fmt.Errorf("boom: %w", ErrParser)
		},
	}

	urls := []string{"https://a.example.com/one", "https://b.example.com/two"}
	out := RunIndexCheckUnits(context.Background(), NewResilientSearcher([]SearchEngine{healthy, failing}, DefaultResilientConfig()), []SearchEngine{healthy, failing}, urls)

	if len(out) != 2 {
		t.Fatalf("expected one row per URL, got %d", len(out))
	}
	for i, row := range out {
		if row.URL != urls[i] {
			t.Fatalf("row %d url=%q, want %q", i, row.URL, urls[i])
		}
		if len(row.Engines) != 2 {
			t.Fatalf("row %d engines=%v", i, row.Engines)
		}
		baiduOutcome := row.Engines["baidu"]
		if baiduOutcome.err != nil {
			t.Fatalf("row %d baidu should succeed, got %+v", i, baiduOutcome)
		}
		if !baiduOutcome.success.Indexed || baiduOutcome.success.Match != IndexMatchExact {
			t.Fatalf("row %d baidu verdict=%+v", i, baiduOutcome.success)
		}
		if baiduOutcome.success.TookMs < 0 {
			t.Fatalf("row %d baidu took_ms=%d", i, baiduOutcome.success.TookMs)
		}
		bingErr := row.Engines["bing"].err
		if bingErr == nil {
			t.Fatalf("row %d bing should carry an error", i)
		}
		if bingErr.Code != "parser_failure" {
			t.Fatalf("row %d bing error code=%q", i, bingErr.Code)
		}
	}

	if got := len(healthy.calls()); got != 2 {
		t.Fatalf("healthy engine calls=%d, want 2", got)
	}
	if got := len(failing.calls()); got != 2 {
		t.Fatalf("bing calls=%d, want 1 per URL (parser failure is non-retryable)", got)
	}
}

// TestRunIndexCheckUnitsInterceptionTripsCooldown: an interception-class
// failure (captcha) records a cooldown block, so the NEXT batch request for
// that engine alone fails fast with engine_cooldown — zero engine contact.
func TestRunIndexCheckUnitsInterceptionTripsCooldown(t *testing.T) {
	t.Parallel()

	failing := &indexEngineMock{
		name:        "bing",
		initialized: true,
		serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
			return nil, fmt.Errorf("boom: %w", ErrCaptcha)
		},
	}
	rs := NewResilientSearcher([]SearchEngine{failing}, DefaultResilientConfig())

	first := RunIndexCheckUnits(context.Background(), rs, []SearchEngine{failing}, []string{"https://a.example.com/one"})
	units := first[0].Engines["bing"]
	if units.err == nil || units.err.Code != "captcha_detected" {
		t.Fatalf("first batch bing unit=%+v, want captcha_detected", units)
	}
	if got := len(failing.calls()); got != 1 {
		t.Fatalf("bing calls after first batch=%d, want 1 (captcha is non-retryable)", got)
	}

	// Second batch: bing is cooling now — the whole (single-engine) request
	// must be rejected before the engine is touched.
	out2 := RunIndexCheckUnits(context.Background(), rs, []SearchEngine{failing}, []string{"https://a.example.com/one"})
	units2 := out2[0].Engines["bing"]
	if units2.err == nil || units2.err.Code != "engine_cooldown" {
		t.Fatalf("second batch bing unit=%+v, want engine_cooldown", units2)
	}
	if units2.err.RetryAfterSeconds < 1 {
		t.Fatalf("engine_cooldown unit missing retry_after_seconds: %+v", units2.err)
	}
	if got := len(failing.calls()); got != 1 {
		t.Fatalf("bing touched while cooling: calls=%d", got)
	}
}

func TestRunIndexCheckUnitsRecoversEnginePanic(t *testing.T) {
	t.Parallel()

	panicky := &indexEngineMock{
		name:        "sogou",
		initialized: true,
		serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
			panic("rod exploded")
		},
	}

	out := RunIndexCheckUnits(context.Background(),
		NewResilientSearcher([]SearchEngine{panicky}, DefaultResilientConfig()),
		[]SearchEngine{panicky}, []string{"https://example.com/x"})

	outcome := out[0].Engines["sogou"]
	if outcome.err == nil {
		t.Fatalf("expected recovered panic to surface as error, got %+v", outcome)
	}
	if outcome.err.Code != "engine_internal" {
		t.Fatalf("error code=%q, want engine_internal", outcome.err.Code)
	}
	// S4-B-08: the unit message is the fixed taxonomy text — the recovered
	// panic detail (incl. engine internals and stack) stays in the server log
	// only.
	if outcome.err.Message != "engine internal error" {
		t.Fatalf("expected fixed message per B-08, got %q", outcome.err.Message)
	}
}

func TestRunIndexCheckUnitsRateLimitedQueueing(t *testing.T) {
	t.Parallel()

	// A 1 req/200ms limiter shared by one engine: three units must all run,
	// proving concurrent units queue on the engine limiter instead of failing.
	limiter := rate.NewLimiter(rate.Every(200*time.Millisecond), 1)
	engine := &indexEngineMock{name: "so360", initialized: true, limiter: limiter}

	out := RunIndexCheckUnits(context.Background(),
		NewResilientSearcher([]SearchEngine{engine}, DefaultResilientConfig()),
		[]SearchEngine{engine}, []string{"https://a.test/", "https://b.test/", "https://c.test/"})

	if got := len(engine.calls()); got != 3 {
		t.Fatalf("engine calls=%d, want 3", got)
	}
	for i, row := range out {
		if row.Engines["so360"].err != nil {
			t.Fatalf("row %d unexpectedly failed: %+v", i, row.Engines["so360"])
		}
	}
}

// --- engine selection ----------------------------------------------------

func TestSelectIndexCheckEngines(t *testing.T) {
	t.Parallel()

	available := []SearchEngine{
		&indexEngineMock{name: "baidu", initialized: true},
		&indexEngineMock{name: "sogou", initialized: true},
		&indexEngineMock{name: "so360", initialized: true},
		&indexEngineMock{name: "bing", initialized: true},
	}

	all, apiErr := SelectIndexCheckEngines(available, nil)
	if apiErr != nil || len(all) != 4 {
		t.Fatalf("empty selection should pick all engines, got %d, err=%v", len(all), apiErr)
	}

	alias, apiErr := SelectIndexCheckEngines(available, []string{"360", "baidu", "360"})
	if apiErr != nil {
		t.Fatalf("alias selection failed: %v", apiErr)
	}
	if len(alias) != 2 || alias[0].Name() != "baidu" || alias[1].Name() != "so360" {
		t.Fatalf("alias/dedupe selection wrong: %v", engineNamesForTest(alias))
	}

	_, apiErr = SelectIndexCheckEngines(available, []string{"google"})
	if apiErr == nil || apiErr.HTTPStatus != 400 || apiErr.Reason != ReasonInvalidParam {
		t.Fatalf("unknown engine should be 400 INVALID_PARAM, got %+v", apiErr)
	}
}

func engineNamesForTest(engines []SearchEngine) []string {
	names := make([]string, 0, len(engines))
	for _, e := range engines {
		names = append(names, e.Name())
	}
	return names
}

// --- handler -------------------------------------------------------------

func indexCheckTestServer(t *testing.T) *Server {
	t.Helper()
	baidu := &indexEngineMock{name: "baidu", initialized: true}
	bing := &indexEngineMock{
		name:        "bing",
		initialized: true,
		serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
			// Mocks see only the runtime query; the real "url:<url>" operator
			// wiring is asserted in the bing package tests.
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	challenged := &indexEngineMock{
		name:        "so360",
		initialized: true,
		serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
			// Non-retryable so the handler test stays fast; exercises the
			// per-unit error path end to end.
			return nil, fmt.Errorf("engine challenged: %w", ErrCaptcha)
		},
	}
	sogou := &indexEngineMock{name: "sogou", initialized: true}

	srv := NewServerWithOptions("127.0.0.1", 7121, DefaultServerOptions(), baidu, sogou, challenged, bing)
	srv.app.Post("/v1/index/check", srv.IndexCheckHandler)
	srv.app.Get("/v1/index/check", srv.IndexCheckHandler)
	return srv
}

func indexCheckDo(t *testing.T, srv *Server, req *http.Request) (int, string) {
	t.Helper()
	resp, err := srv.app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer closeTestResource(t, resp.Body)
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, buf.String()
}

func TestIndexCheckHandlerPostAndGET(t *testing.T) {
	t.Parallel()
	srv := indexCheckTestServer(t)

	postBody := `{"urls":["https://a.example.com/one","https://b.example.com/two"],"engines":["baidu","bing","so360"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/index/check", strings.NewReader(postBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-idx-1")
	status, body := indexCheckDo(t, srv, req)
	if status != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", status, body)
	}

	var payload IndexCheckResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode response: %v\n%s", err, body)
	}
	if payload.ID != "req-idx-1" {
		t.Fatalf("id=%q, want request id echo", payload.ID)
	}
	if _, err := time.Parse(time.RFC3339, payload.CreatedAt); err != nil {
		t.Fatalf("created_at=%q is not RFC3339: %v", payload.CreatedAt, err)
	}
	if payload.Meta.RequestID != "req-idx-1" || payload.Meta.Units != 6 {
		t.Fatalf("meta=%+v, want request_id=req-idx-1 units=6 (2 urls x 3 engines)", payload.Meta)
	}
	if payload.Meta.TookMs < 0 {
		t.Fatalf("meta.took_ms=%d", payload.Meta.TookMs)
	}
	if len(payload.Results) != 2 || payload.Results[0].URL != "https://a.example.com/one" {
		t.Fatalf("results order/content wrong: %+v", payload.Results)
	}
	baiduHit := payload.Results[0].Engines["baidu"]
	if baiduHit.err != nil || !baiduHit.success.Indexed || baiduHit.success.Match != "exact" {
		t.Fatalf("baidu outcome=%+v", baiduOutcomeForTest(baiduHit))
	}
	bingHit := payload.Results[0].Engines["bing"]
	if bingHit.err != nil || !bingHit.success.Indexed {
		t.Fatalf("bing (url: operator) outcome should hit: %+v", bingOutcomeForTest(bingHit))
	}
	so360Err := payload.Results[0].Engines["so360"]
	if so360Err.err == nil {
		t.Fatalf("so360 should carry a per-unit error, got %+v", so360Err)
	}
	if so360Err.err.Code != "captcha_detected" {
		t.Fatalf("so360 error code=%q, want captcha_detected", so360Err.err.Code)
	}

	// Engine-level failure keeps the whole response at 200 (already asserted)
	// and the failing engine is absent from engines_failed semantics — the
	// error lives in the unit payload.

	// GET convenience form.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/index/check?url=https%3A%2F%2Fa.example.com%2Fone&engines=baidu,bing", nil)
	status, body = indexCheckDo(t, srv, getReq)
	if status != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", status, body)
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode GET response: %v\n%s", err, body)
	}
	if len(payload.Results) != 1 || payload.Meta.Units != 2 {
		t.Fatalf("GET payload wrong: %+v", payload)
	}
	if !payload.Results[0].Engines["bing"].success.Indexed {
		t.Fatalf("GET bing verdict should hit: %+v", payload.Results[0].Engines["bing"])
	}
}

func baiduOutcomeForTest(o IndexCheckEngineOutcome) string {
	if o.err != nil {
		return "error:" + o.err.Code
	}
	return fmt.Sprintf("success:%v/%s", o.success.Indexed, o.success.Match)
}

func bingOutcomeForTest(o IndexCheckEngineOutcome) string { return baiduOutcomeForTest(o) }

func TestIndexCheckHandlerValidation(t *testing.T) {
	t.Parallel()
	srv := indexCheckTestServer(t)

	cases := []struct {
		name   string
		req    *http.Request
		status int
		reason string
	}{
		{
			name:   "POST without urls",
			req:    postJSON(t, `{"engines":["baidu"]}`),
			status: 400, reason: ReasonEmptyQuery,
		},
		{
			name:   "POST with 21 urls",
			req:    postJSON(t, manyURLsBody(21)),
			status: 400, reason: ReasonTooManyURLs,
		},
		{
			name:   "POST with invalid url",
			req:    postJSON(t, `{"urls":["https://ok.example.com/a","not a url"]}`),
			status: 400, reason: ReasonInvalidParam,
		},
		{
			name:   "POST with malformed JSON",
			req:    postJSON(t, `{"urls":`),
			status: 400, reason: ReasonInvalidParam,
		},
		{
			name:   "POST with unknown engine",
			req:    postJSON(t, `{"urls":["https://ok.example.com/a"],"engines":["google"]}`),
			status: 400, reason: ReasonInvalidParam,
		},
		{
			name:   "GET without url",
			req:    httptest.NewRequest(http.MethodGet, "/v1/index/check", nil),
			status: 400, reason: ReasonEmptyQuery,
		},
		{
			name:   "GET with blank url",
			req:    httptest.NewRequest(http.MethodGet, "/v1/index/check?url=%20", nil),
			status: 400, reason: ReasonEmptyQuery,
		},
		{
			name:   "GET with invalid url",
			req:    httptest.NewRequest(http.MethodGet, "/v1/index/check?url=not%20a%20url", nil),
			status: 400, reason: ReasonInvalidParam,
		},
	}

	for _, tc := range cases {
		status, body := indexCheckDo(t, srv, tc.req)
		if status != tc.status {
			t.Fatalf("%s: status=%d body=%s", tc.name, status, body)
		}
		var errPayload struct {
			Error   string `json:"error"`
			Code    int    `json:"code"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(body), &errPayload); err != nil {
			t.Fatalf("%s: decode error body: %v\n%s", tc.name, err, body)
		}
		if errPayload.Error != "bad_request" || errPayload.Reason != tc.reason {
			t.Fatalf("%s: payload=%+v body=%s", tc.name, errPayload, body)
		}
		if errPayload.Code != tc.status {
			t.Fatalf("%s: code=%d, want %d", tc.name, errPayload.Code, tc.status)
		}
		if tc.reason == ReasonInvalidParam && errPayload.Message == "" {
			t.Fatalf("%s: expected helpful validation message, body=%s", tc.name, body)
		}
		if tc.name == "POST with invalid url" && !strings.Contains(errPayload.Message, "not a url") {
			t.Fatalf("%s: message should identify offending URL, got %q", tc.name, errPayload.Message)
		}
		if tc.name == "GET with invalid url" && !strings.Contains(errPayload.Message, "not a url") {
			t.Fatalf("%s: message should identify offending URL, got %q", tc.name, errPayload.Message)
		}
	}
}

func postJSON(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/index/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func manyURLsBody(n int) string {
	var b strings.Builder
	b.WriteString(`{"urls":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\"https://example.com/%d\"", i)
	}
	b.WriteString(`]}`)
	return b.String()
}
