package core

// Page-title probing for index checks.
//
// Baidu does not reliably answer "wd=<article URL>" for a page it has indexed:
// the URL-as-phrase SERP routinely comes back empty while a query built from
// the page title returns the very same article in the first results. Index
// checks therefore fetch the target page's <title> once (over the plain-HTTP
// TLS channel, never through the search engine) and search with that phrase.
//
// The fetch is cached because two call sites need the same title for one
// lookup: the engine builds the SERP query, and the evaluator matches result
// titles against it.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

const (
	// pageTitleCacheTTL is how long a successfully probed title stays fresh.
	pageTitleCacheTTL = time.Hour
	// pageTitleNegativeTTL keeps a failed probe from hammering the site.
	pageTitleNegativeTTL = 5 * time.Minute
	// pageTitleMaxEntries bounds the cache; index batches are small.
	pageTitleMaxEntries = 4096
	// pageTitleMaxBytes bounds how much of the document is read.
	pageTitleMaxBytes = 256 << 10
	// pageTitleTimeout bounds one title probe.
	pageTitleTimeout = 4 * time.Second
	// maxTitleSuffixRunes is the longest trailing segment still treated as a
	// site label ("_华军软件园", " - 某某网") rather than title text.
	maxTitleSuffixRunes = 12
)

type pageTitleEntry struct {
	title  string
	ok     bool
	at     time.Time
	reason string
}

var pageTitleCache = struct {
	sync.Mutex
	entries map[string]pageTitleEntry
}{entries: map[string]pageTitleEntry{}}

// FetchPageTitle returns the normalized <title> of targetURL, fetching it over
// the plain-HTTP TLS channel when the cache has no fresh entry. It reports
// false when the page cannot be fetched or carries no usable title; callers
// must then fall back to the URL-as-phrase lookup.
func FetchPageTitle(ctx context.Context, targetURL string, q Query) (string, bool) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", false
	}
	if entry, found := cachedPageTitle(targetURL); found {
		return entry.title, entry.ok
	}

	ctx = EnsureContext(ctx)
	title, err := fetchPageTitle(ctx, targetURL, q)
	if ctx.Err() != nil {
		return "", false
	}
	if err != nil || title == "" {
		reason := "empty title"
		if err != nil {
			reason = err.Error()
		}
		storePageTitle(targetURL, "", false, reason)
		return "", false
	}
	storePageTitle(targetURL, title, true, "")
	return title, true
}

// ResetPageTitleCache clears the title cache (tests and admin resets).
func ResetPageTitleCache() {
	pageTitleCache.Lock()
	defer pageTitleCache.Unlock()
	pageTitleCache.entries = map[string]pageTitleEntry{}
}

func cachedPageTitle(targetURL string) (pageTitleEntry, bool) {
	pageTitleCache.Lock()
	defer pageTitleCache.Unlock()
	entry, ok := pageTitleCache.entries[targetURL]
	if !ok {
		return pageTitleEntry{}, false
	}
	ttl := pageTitleNegativeTTL
	if entry.ok {
		ttl = pageTitleCacheTTL
	}
	if time.Since(entry.at) > ttl {
		delete(pageTitleCache.entries, targetURL)
		return pageTitleEntry{}, false
	}
	return entry, true
}

func storePageTitle(targetURL, title string, ok bool, reason string) {
	pageTitleCache.Lock()
	defer pageTitleCache.Unlock()
	if len(pageTitleCache.entries) >= pageTitleMaxEntries {
		// Drop the oldest entry; the cache only needs to span one batch.
		var oldestKey string
		var oldest time.Time
		for key, entry := range pageTitleCache.entries {
			if oldestKey == "" || entry.at.Before(oldest) {
				oldestKey, oldest = key, entry.at
			}
		}
		delete(pageTitleCache.entries, oldestKey)
	}
	pageTitleCache.entries[targetURL] = pageTitleEntry{title: title, ok: ok, at: time.Now(), reason: reason}
}

// fetchPageTitle performs the single HTTP probe and extracts the title.
func fetchPageTitle(ctx context.Context, targetURL string, q Query) (string, error) {
	ctx, cancel := context.WithTimeout(EnsureContext(ctx), pageTitleTimeout)
	defer cancel()

	// The probe target is caller-supplied (anyone holding an API key picks the
	// URL), so it must never reach loopback/private/link-local addresses or a
	// cloud metadata endpoint: without the guard the server itself becomes the
	// SSRF request source. A remote proxy resolves the target independently
	// after our validation, reopening a DNS-rebinding window, so guarded title
	// probes always dial directly. GuardedDialContext pins the resolved IP and
	// every redirect hop is re-validated. If direct egress is unavailable, the
	// caller falls back to the URL-as-phrase SERP query (which may use a proxy).
	q = guardedTitleProbeQuery(q)
	resp, err := RawSearchRequest(ctx, targetURL, q)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &titleProbeError{status: resp.StatusCode}
	}

	reader, err := charset.NewReader(io.LimitReader(resp.Body, pageTitleMaxBytes), resp.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return "", err
	}
	return normalizePageTitle(doc.Find("title").First().Text()), nil
}

func guardedTitleProbeQuery(q Query) Query {
	q.ProxyURL = ""
	q.GuardPrivateNetworks = true
	q.Text = ""
	return q
}

type titleProbeError struct{ status int }

func (e *titleProbeError) Error() string { return "title probe HTTP " + http.StatusText(e.status) }

// normalizePageTitle trims and strips the trailing site name that Baidu SERPs
// and browsers append ("标题_华军软件园", "标题 - 站点名", "标题 | 站点名").
func normalizePageTitle(raw string) string {
	title := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
	if title == "" {
		return ""
	}
	// Drop a short trailing segment after a title/site separator. Only
	// separators that actually split the string are considered, and only the
	// last segment is dropped (site labels are short; article clauses are not).
	trimmed := title
	for _, sep := range []string{"_", "｜", "|", " — ", " – ", " - ", "－"} {
		parts := strings.Split(trimmed, sep)
		if len(parts) < 2 {
			continue
		}
		last := strings.TrimSpace(parts[len(parts)-1])
		if utf8.RuneCountInString(last) <= maxTitleSuffixRunes {
			trimmed = strings.TrimSpace(strings.Join(parts[:len(parts)-1], sep))
		}
	}
	if utf8.RuneCountInString(trimmed) < 2 {
		return title
	}
	return trimmed
}
