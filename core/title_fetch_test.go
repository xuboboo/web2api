package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

type testNetIPResolver func(context.Context, string, string) ([]netip.Addr, error)

func (f testNetIPResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

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
	if got, ok := cachedPageTitle("https://example.test/a"); !ok || !got.ok || got.title != "标题甲" {
		t.Fatalf("positive cache miss: %+v found=%v", got, ok)
	}
	storePageTitle("https://example.test/b", "", false, "probe failed")
	if got, found := cachedPageTitle("https://example.test/b"); !found || got.ok {
		t.Fatal("negative cache entry must be a cached miss")
	}
	ResetPageTitleCache()
	if _, ok := cachedPageTitle("https://example.test/a"); ok {
		t.Fatal("reset must clear the cache")
	}
}

func TestFetchPageTitleReusesNegativeResultUntilExpiry(t *testing.T) {
	ResetPageTitleCache()
	t.Cleanup(ResetPageTitleCache)

	const target = "https://negative.web2api.test/article"
	lookups := 0
	ctx := context.WithValue(t.Context(), guardedNetworkHooksKey{}, guardedNetworkHooks{
		resolver: testNetIPResolver(func(context.Context, string, string) ([]netip.Addr, error) {
			lookups++
			return nil, errors.New("DNS unavailable")
		}),
	})
	for i := 0; i < 2; i++ {
		if title, ok := FetchPageTitle(ctx, target, Query{}); ok || title != "" {
			t.Fatalf("negative probe returned title %q, ok %v", title, ok)
		}
	}
	if lookups != 1 {
		t.Fatalf("DNS lookups = %d, want 1", lookups)
	}
	pageTitleCache.Lock()
	entry := pageTitleCache.entries[target]
	entry.at = time.Now().Add(-pageTitleNegativeTTL - time.Second)
	pageTitleCache.entries[target] = entry
	pageTitleCache.Unlock()
	FetchPageTitle(ctx, target, Query{})
	if lookups != 2 {
		t.Fatalf("DNS lookups after expiry = %d, want 2", lookups)
	}
}

func TestFetchPageTitleEmptyURL(t *testing.T) {
	if _, ok := FetchPageTitle(t.Context(), "   ", Query{}); ok {
		t.Fatal("empty target URL must not produce a title")
	}
}

func TestGuardedTitleProbeDoesNotUseRemoteProxy(t *testing.T) {
	q := guardedTitleProbeQuery(Query{
		ProxyURL: "http://proxy.example:8080",
		Text:     "must not leak into the target GET",
	})
	if q.ProxyURL != "" || !q.GuardPrivateNetworks || q.Text != "" {
		t.Fatalf("unsafe title-probe query: proxy=%q guarded=%v text=%q", q.ProxyURL, q.GuardPrivateNetworks, q.Text)
	}
}

// Exercise the full title-probe path with an isolated DNS answer, HTTP target,
// and proxy. The first lookup passes validation as public; the second lookup
// rebinds to loopback. The guarded transport must refuse before dialing, and
// neither the private target nor the configured proxy may receive a request.
func TestFetchPageTitleDNSRebindingCannotReachPrivateTargetOrProxy(t *testing.T) {
	ResetPageTitleCache()
	t.Cleanup(ResetPageTitleCache)

	var publicHits, privateHits, proxyHits atomic.Int32
	publicTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		publicHits.Add(1)
		_, _ = w.Write([]byte("<html><head><title>public article title</title></head></html>"))
	}))
	defer publicTarget.Close()
	privateTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		privateHits.Add(1)
		_, _ = w.Write([]byte("<html><head><title>internal admin title</title></head></html>"))
	}))
	defer privateTarget.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		_, _ = w.Write([]byte("<html><head><title>proxy response must not be used</title></head></html>"))
	}))
	defer proxy.Close()

	publicURL, err := url.Parse(publicTarget.URL)
	if err != nil {
		t.Fatal(err)
	}
	targetURL := "http://rebind.web2api.test:" + publicURL.Port() + "/article"
	publicIP := netip.MustParseAddr("1.1.1.1")
	privateIP := netip.MustParseAddr("127.0.0.1")
	var lookups atomic.Int32
	resolver := testNetIPResolver(func(context.Context, string, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{publicIP}, nil
		}
		return []netip.Addr{privateIP}, nil
	})

	var dials atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if host != publicIP.String() {
			return nil, fmt.Errorf("transport tried to dial unvetted address %q", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, publicTarget.Listener.Addr().String())
	}
	ctx := context.WithValue(t.Context(), guardedNetworkHooksKey{}, guardedNetworkHooks{
		resolver: resolver,
		dial:     dial,
	})
	if title, ok := FetchPageTitle(ctx, targetURL, Query{ProxyURL: proxy.URL}); ok {
		t.Fatalf("DNS rebinding probe returned title %q; it must fail closed", title)
	}
	if got := lookups.Load(); got < 2 {
		t.Fatalf("expected validation and dial-time DNS lookups, got %d", got)
	}
	if got := dials.Load(); got != 0 {
		t.Fatalf("transport dialed %d time(s) after the address rebound to loopback", got)
	}
	if got := publicHits.Load(); got != 0 {
		t.Fatalf("public fixture received %d request(s) after rebinding", got)
	}
	if got := privateHits.Load(); got != 0 {
		t.Fatalf("private fixture received %d guarded request(s)", got)
	}
	if got := proxyHits.Load(); got != 0 {
		t.Fatalf("remote proxy received %d title-probe request(s)", got)
	}
}

// The same isolated transport must still fetch a title when DNS remains
// public, proving the rebinding protection does not disable normal probes.
// The original query keeps its proxy for the URL-phrase SERP fallback.
func TestGuardedTitleProbePublicPathAndFallbackRemainUsable(t *testing.T) {
	ResetPageTitleCache()
	t.Cleanup(ResetPageTitleCache)

	var publicHits, proxyHits atomic.Int32
	publicTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		publicHits.Add(1)
		_, _ = w.Write([]byte("<html><head><title>public article title</title></head></html>"))
	}))
	defer publicTarget.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	publicURL, err := url.Parse(publicTarget.URL)
	if err != nil {
		t.Fatal(err)
	}
	targetURL := "http://public.web2api.test:" + publicURL.Port() + "/article"
	publicIP := netip.MustParseAddr("1.1.1.1")
	resolver := testNetIPResolver(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{publicIP}, nil
	})
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if host != publicIP.String() {
			return nil, fmt.Errorf("transport tried to dial %q, want the validated public IP", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, publicTarget.Listener.Addr().String())
	}
	ctx := context.WithValue(t.Context(), guardedNetworkHooksKey{}, guardedNetworkHooks{
		resolver: resolver,
		dial:     dial,
	})
	runtime := Query{ProxyURL: proxy.URL}
	if title, ok := FetchPageTitle(ctx, targetURL, runtime); !ok || title != "public article title" {
		t.Fatalf("public path title = %q, ok=%v", title, ok)
	}
	if got := publicHits.Load(); got != 1 {
		t.Fatalf("public fixture hits = %d, want 1", got)
	}
	if got := proxyHits.Load(); got != 0 {
		t.Fatalf("proxy hits = %d, want 0 for title probe", got)
	}
	if runtime.ProxyURL != proxy.URL {
		t.Fatalf("FetchPageTitle mutated the caller query proxy to %q", runtime.ProxyURL)
	}
	fallback := IndexCheckQuery(targetURL, runtime)
	if fallback.ProxyURL != proxy.URL || fallback.Text != targetURL {
		t.Fatalf("URL-phrase fallback query lost its proxy or target: %+v", fallback)
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
	if got, found := cachedPageTitle(srv.URL); !found || got.ok {
		t.Fatal("blocked probe must cache a miss")
	}
}
