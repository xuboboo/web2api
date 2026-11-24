package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizePageTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CrowdStrike 收购 Zscaler：439亿美元，1.135换1股_华军软件园", "CrowdStrike 收购 Zscaler：439亿美元，1.135换1股"},
		{"某标题 - 站点名", "某标题"},
		{"标题A | 站点B", "标题A"},
		{"  多  空格  标题  ", "多 空格 标题"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizePageTitle(tc.in); got != tc.want {
			t.Errorf("normalizePageTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPageTitleCacheRespectsTTLAndNegatives(t *testing.T) {
	ResetPageTitleCache()
	storePageTitle("https://example.test/a", "标题甲", true, "")
	if got, ok := cachedPageTitle("https://example.test/a"); !ok || got != "标题甲" {
		t.Fatalf("positive cache miss: %q ok=%v", got, ok)
	}
	storePageTitle("https://example.test/b", "", false, "probe failed")
	if _, ok := cachedPageTitle("https://example.test/b"); ok {
		t.Fatal("negative cache entry must report not-ok")
	}
	ResetPageTitleCache()
	if _, ok := cachedPageTitle("https://example.test/a"); ok {
		t.Fatal("reset must clear the cache")
	}
}

func TestFetchPageTitleEmptyURL(t *testing.T) {
	if _, ok := FetchPageTitle(t.Context(), "   ", Query{}); ok {
		t.Fatal("empty target URL must not produce a title")
	}
}

// The title probe target comes from the API caller, so the probe must refuse
// loopback/private/link-local destinations (SSRF). Without this the server would
// happily GET its own admin API or a cloud metadata endpoint on the caller's
// behalf.
func TestFetchPageTitleRejectsPrivateTargets(t *testing.T) {
	ResetPageTitleCache()
	t.Cleanup(ResetPageTitleCache)

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("<html><head><title>internal-admin</title></head></html>"))
	}))
	defer srv.Close()

	cases := []string{
		srv.URL, // 127.0.0.1
		"http://169.254.169.254/latest/meta-data/", // link-local / cloud metadata
		"http://10.0.0.1/",                         // RFC1918
		"http://[::1]/",                            // IPv6 loopback
	}
	for _, target := range cases {
		title, ok := FetchPageTitle(t.Context(), target, Query{})
		if ok {
			t.Errorf("FetchPageTitle(%q) returned %q; private targets must be refused", target, title)
		}
	}
	if hits != 0 {
		t.Fatalf("guarded probe still reached the loopback server %d time(s)", hits)
	}

	// A blocked probe must be remembered as a negative result, not retried on
	// every unit of a batch.
	storePageTitle(srv.URL, "", false, "target not allowed")
	if _, ok := cachedPageTitle(srv.URL); ok {
		t.Fatal("negative cache entry must report not-ok")
	}
}
