package core

import "testing"

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
