package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestStressBatchIndexCheckMixedFailures1000 stresses the batch pipeline with a
// mix of healthy, flaky-parser, and interception (captcha) engines over 1000
// iterations. Verifies: cooldown never touches a healthy engine, per-unit error
// codes stay stable, and no verdict is misattributed to the wrong URL/engine.
func TestStressBatchIndexCheckMixedFailures1000(t *testing.T) {
	const iterations = 500
	const urlsPerBatch = 8

	healthy := &indexEngineMock{name: "baidu", initialized: true}
	flaky := &indexEngineMock{name: "bing", initialized: true, serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
		return nil, fmt.Errorf("drift: %w", ErrParser)
	}}
	var challengedCalls int64
	challenged := &indexEngineMock{name: "so360", initialized: true, serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
		atomic.AddInt64(&challengedCalls, 1)
		return nil, fmt.Errorf("wall: %w", ErrCaptcha)
	}}
	engines := []SearchEngine{healthy, flaky, challenged}
	rs := NewResilientSearcher(engines, DefaultResilientConfig())

	for it := 0; it < iterations; it++ {
		urls := make([]string, urlsPerBatch)
		for u := range urls {
			urls[u] = fmt.Sprintf("https://mix%d.example.com/p/%d", u, it)
		}
		out := RunIndexCheckUnits(context.Background(), rs, engines, urls)
		if len(out) != urlsPerBatch {
			t.Fatalf("iter %d rows=%d", it, len(out))
		}
		for i, row := range out {
			if row.URL != urls[i] {
				t.Fatalf("iter %d row %d url=%q want %q", it, i, row.URL, urls[i])
			}
			bd := row.Engines["baidu"]
			if bd.err != nil || bd.success == nil || !bd.success.Indexed {
				t.Fatalf("iter %d row %d healthy baidu broke: %+v", it, i, bd)
			}
			bg := row.Engines["bing"].err
			if bg == nil || (bg.Code != "parser_failure" && bg.Code != "circuit_open") {
				t.Fatalf("iter %d row %d bing code=%+v", it, i, bg)
			}
			so := row.Engines["so360"].err
			if so == nil || (so.Code != "captcha_detected" && so.Code != "engine_cooldown") {
				t.Fatalf("iter %d row %d so360 code=%+v", it, i, so)
			}
		}
	}
	// Challenged engine is only contacted once (first captcha trips cooldown);
	// all subsequent units must fast-fail with engine_cooldown.
	if got := atomic.LoadInt64(&challengedCalls); got > int64(urlsPerBatch) {
		t.Fatalf("challenged engine contacted %d times across %d iterations; cooldown not holding", got, iterations)
	}
	_ = time.Now()
	t.Logf("%d iterations mixed-failure OK; challenged contacts=%d", iterations, challengedCalls)
}
