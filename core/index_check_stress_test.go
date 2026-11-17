package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestStressBatchIndexCheck1000 is a deterministic 1000-iteration stress harness.
// It uses mock engines (no real search engine contact) to verify batch invariants:
//  1. one row per input URL, in input order
//  2. each engine called exactly once per URL per iteration (retry-free success path)
//  3. no verdict cross-contamination under concurrency
//  4. per-unit TookMs is always >= 0
func TestStressBatchIndexCheck1000(t *testing.T) {
	const iterations = 1000
	const urlsPerBatch = 20
	const enginesN = 4

	names := []string{"baidu", "sogou", "so360", "bing"}
	engines := make([]SearchEngine, 0, enginesN)
	callCounts := make([]*int64, enginesN)
	for i, name := range names {
		c := new(int64)
		callCounts[i] = c
		engines = append(engines, &indexEngineMock{
			name: name, initialized: true,
			serpFn: func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
				atomic.AddInt64(c, 1)
				return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
			},
		})
	}
	rs := NewResilientSearcher(engines, DefaultResilientConfig())

	totalUnits := int64(0)
	start := time.Now()
	for it := 0; it < iterations; it++ {
		urls := make([]string, urlsPerBatch)
		for u := range urls {
			urls[u] = fmt.Sprintf("https://s%d.example.com/p/%d?it=%d", u, u, it)
		}
		out := RunIndexCheckUnits(context.Background(), rs, engines, urls)
		if len(out) != urlsPerBatch {
			t.Fatalf("iter %d: rows=%d want %d", it, len(out), urlsPerBatch)
		}
		for i, row := range out {
			if row.URL != urls[i] {
				t.Fatalf("iter %d row %d: url=%q want %q", it, i, row.URL, urls[i])
			}
			if len(row.Engines) != enginesN {
				t.Fatalf("iter %d row %d: engines=%d", it, i, len(row.Engines))
			}
			for _, name := range names {
				o := row.Engines[name]
				if o.err != nil {
					t.Fatalf("iter %d row %d %s: err=%+v", it, i, name, o.err)
				}
				if o.success == nil || !o.success.Indexed || o.success.Match != IndexMatchExact {
					t.Fatalf("iter %d row %d %s: verdict=%+v", it, i, name, o.success)
				}
				if o.success.TookMs < 0 {
					t.Fatalf("iter %d row %d %s: took_ms=%d", it, i, name, o.success.TookMs)
				}
				totalUnits++
			}
		}
	}
	wantCalls := int64(iterations * urlsPerBatch)
	for i, c := range callCounts {
		if got := atomic.LoadInt64(c); got != wantCalls {
			t.Fatalf("engine %s calls=%d want %d", names[i], got, wantCalls)
		}
	}
	if totalUnits != int64(iterations*urlsPerBatch*enginesN) {
		t.Fatalf("total units=%d", totalUnits)
	}
	t.Logf("1000 iterations x %d urls x %d engines = %d units in %s; all invariants OK", urlsPerBatch, enginesN, totalUnits, time.Since(start))
}
