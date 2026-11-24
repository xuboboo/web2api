package core

import (
	"strings"
	"testing"
)

// The sync path must collapse duplicates exactly like the async job path does,
// otherwise an input's cost depends only on how many copies it contains.
func TestValidateIndexCheckURLsCollapsesDuplicates(t *testing.T) {
	urls, apiErr := validateIndexCheckURLs([]string{
		"https://example.test/a",
		" https://example.test/a ",     // trims to the same URL
		"HTTPS://Example.Test/a",       // scheme/host case folded by the key
		"https://example.test/a/",      // trailing slash ignored by the key
		"https://example.test/b",
	})
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 unique URLs, got %d: %v", len(urls), urls)
	}
	if urls[0] != "https://example.test/a" {
		t.Fatalf("first occurrence must be preserved, got %q", urls[0])
	}
	if urls[1] != "https://example.test/b" {
		t.Fatalf("order must follow the request, got %q", urls[1])
	}
}

// Distinct pages that only differ by a meaningful path/query must stay distinct.
func TestValidateIndexCheckURLsKeepsDistinctPages(t *testing.T) {
	urls, apiErr := validateIndexCheckURLs([]string{
		"https://example.test/a?id=1",
		"https://example.test/a?id=2",
		"https://example.test/a",
	})
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if len(urls) != 3 {
		t.Fatalf("expected 3 URLs, got %d: %v", len(urls), urls)
	}
}

// Only http/https can be indexed; a bare host or host:port stays accepted.
func TestValidateIndexCheckURLsRejectsUnsupportedSchemes(t *testing.T) {
	bad := []string{
		"ftp://example.test/x",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html,hi",
		"mailto:someone@example.test",
	}
	for _, raw := range bad {
		_, apiErr := validateIndexCheckURLs([]string{raw})
		if apiErr == nil {
			t.Fatalf("%q must be rejected", raw)
		}
		// The message must name the scheme problem, so a caller learns why
		// instead of guessing at the URL shape.
		if !strings.Contains(strings.ToLower(apiErr.Message), "scheme") {
			t.Fatalf("%q: error should name the scheme problem, got %q", raw, apiErr.Message)
		}
	}

	good := []string{
		"https://example.test/x",
		"http://example.test/x",
		"example.test",              // bare host → http
		"example.test:8080/x",       // host:port is not a scheme
		"www.example.test/a?b=1",    // bare host with query
	}
	for _, raw := range good {
		if _, apiErr := validateIndexCheckURLs([]string{raw}); apiErr != nil {
			t.Fatalf("%q must be accepted, got %+v", raw, apiErr)
		}
	}
}

// A single-label host can never be in a public index; answering "not indexed"
// and billing for it is worse than refusing the input.
func TestValidateIndexCheckURLsRejectsHostWithoutDomain(t *testing.T) {
	for _, raw := range []string{"not-a-url", "localhost", "http://intranet/x"} {
		_, apiErr := validateIndexCheckURLs([]string{raw})
		if apiErr == nil {
			t.Fatalf("%q must be rejected", raw)
		}
		if !strings.Contains(apiErr.Message, "domain") {
			t.Fatalf("%q: error should mention the missing domain, got %q", raw, apiErr.Message)
		}
	}
}

// An oversized list is still refused by raw count, even when every entry is a
// duplicate of the same page.
func TestValidateIndexCheckURLsRefusesOversizedRawList(t *testing.T) {
	raw := make([]string, MaxIndexCheckURLs+1)
	for i := range raw {
		raw[i] = "https://example.test/same"
	}
	if _, apiErr := validateIndexCheckURLs(raw); apiErr == nil || apiErr.Reason != ReasonTooManyURLs {
		t.Fatalf("expected TOO_MANY_URLS, got %+v", apiErr)
	}
}
