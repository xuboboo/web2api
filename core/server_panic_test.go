package core

import (
	"context"
	"errors"
	"testing"
)

// FP-1: panics from engine code must be converted to ErrEngineInternal by the
// central recovery in the resilient layer (invokeEngine) instead of killing
// the process.
func TestInvokeEngineRecoversPanics(t *testing.T) {
	engine := &engineMock{
		name:        "google",
		initialized: true,
		searchFn: func(_ context.Context, _ Query) ([]SearchResult, error) {
			panic("rod: page crashed")
		},
	}

	results, err := invokeEngine(context.Background(), engine, Query{Text: "golang"})
	if results != nil {
		t.Fatalf("expected nil results after panic, got %v", results)
	}
	if !errors.Is(err, ErrEngineInternal) {
		t.Fatalf("expected ErrEngineInternal, got %v", err)
	}
}
