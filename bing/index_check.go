package bing

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
)

// Compile-time proof that Bing participates in index checks.
var _ core.IndexChecker = (*Bing)(nil)

// CheckIndexed implements core.IndexChecker: it looks targetURL up through
// Bing's own SERP pipeline, limit 10, and returns the parsed candidates for
// core's normalization matching. The existing Search path is reused unchanged,
// so browser fingerprints and captcha handling stay in effect. runtime carries
// proxy/TLS settings selected by the resilience layer.
//
// Query form (2026-11 实测):
//   - 历史 "url:<url>" 操作符在 cn.bing.com 已失效(4/4 miss,返回无关页),弃用;
//   - 单一查询词不稳: 带 scheme 的完整 URL 对 go.dev 等域名会被当成 "https 是什么"
//     命中百科,而去 scheme 的 www.xxx 深链接(如 onlinedown.net/article/...)
//     会被当成 "www 是什么" 命中百科。两种形式各有盲区。
//   - 故按优先级依次尝试带引号的完整 URL、完整 URL、去 scheme 的 host+path;
//     任一 SERP 含目标 URL 的精确候选即早停返回,全部未命中才返回最后一次结果。
//     匹配始终由 core 用原始 targetURL 归一化,查询词只影响 Bing 检索。
func (bing *Bing) CheckIndexed(ctx context.Context, targetURL string, runtime core.Query) ([]core.SearchResult, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("%w: index check target URL is empty", core.ErrEngineInternal)
	}

	queries := bingIndexQueries(targetURL)
	var union []core.SearchResult
	for i, queryText := range queries {
		results, err := bing.Search(ctx, core.IndexCheckQuery(queryText, runtime))
		if err != nil {
			// Technical failures (captcha/timeout) are returned immediately so the
			// resilience layer can classify them; a non-final empty SERP is not an
			// error - try the next query form.
			if i == 0 || core.IsContextDone(err) {
				return nil, err
			}
			logrus.WithField("engine", "bing").WithError(err).Debug("index check query variant failed")
			continue
		}
		if candidate, found := core.IndexSERPContains(targetURL, results); found {
			logrus.WithFields(logrus.Fields{"engine": "bing", "query_variant": i, "candidate": candidate}).Debug("index check hit")
			return results, nil
		}
		union = append(union, results...)
	}
	// No form produced a direct hit. Return the union so core's pass 2 (opaque
	// redirect resolution) and total_results still see every candidate observed.
	return core.DeduplicateResults(union), nil
}

// bingIndexQueries returns the SERP query forms to try, most reliable first.
// 2026-11 live probe: the quoted full URL is a strict superset of every other
// form (10/10 indexed URLs hit vs 9/10 unquoted full, 8/10 scheme-stripped).
// It is therefore primary; the other two are fallbacks for upstream drift.
// The retired "url:" operator is intentionally absent.
func bingIndexQueries(targetURL string) []string {
	forms := []string{"\"" + targetURL + "\""}
	if targetURL != "" && !containsString(forms, targetURL) {
		forms = append(forms, targetURL)
	}
	if stripped := bingIndexQuery(targetURL); stripped != targetURL && !containsString(forms, stripped) {
		forms = append(forms, stripped)
	}
	return forms
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// bingIndexQuery strips the URL scheme for the SERP query phrase. Bing treats
// "https://" as a keyword on some hosts (e.g. https://go.dev -> "https" baike
// article), while the bare host+path resolves to the site's own result. The
// returned candidates are still matched against the original targetURL by core.
func bingIndexQuery(targetURL string) string {
	if i := strings.Index(targetURL, "://"); i >= 0 {
		return targetURL[i+3:]
	}
	return targetURL
}
