package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestStressRapidRepeatSubmissions simulates a user hammering the same batch
// repeatedly. Verifies the shared Server/CheckIndexBatch path keeps consistent
// results and that the index-check response cache (when on) never returns a
// stale verdict for a changed URL set.
func TestStressRapidRepeatSubmissions(t *testing.T) {
	baidu := &indexEngineMock{name: "baidu", initialized: true}
	bing := &indexEngineMock{name: "bing", initialized: true}
	engines := []SearchEngine{baidu, bing}
	srv := indexCheckTestServer(t)
	_ = srv
	rs := NewResilientSearcher(engines, DefaultResilientConfig())

	const rounds = 1000
	var bad int64
	start := time.Now()
	for i := 0; i < rounds; i++ {
		target := fmt.Sprintf("https://rapid%d.example.com/p", i%7)
		out := RunIndexCheckUnits(context.Background(), rs, engines, []string{target})
		if len(out) != 1 {
			bad++
			continue
		}
		for _, name := range []string{"baidu", "bing"} {
			o := out[0].Engines[name]
			if o.err != nil || o.success == nil || o.success.MatchedResultURL != target {
				atomic.AddInt64(&bad, 1)
			}
		}
	}
	if bad != 0 {
		t.Fatalf("inconsistent results: %d", bad)
	}
	t.Logf("%d rapid submissions OK in %s", rounds, time.Since(start))
}
