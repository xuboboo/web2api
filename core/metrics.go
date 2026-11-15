package core

// Hand-written OpenMetrics text registry (S4-GAP 运维项). 单二进制约束: only
// the standard library is used — no Prometheus client dependency, no scrape
// config, no new storage. Counters are process-lifetime atomics; the render
// path formats the OpenMetrics exposition text on demand for
// GET /web-tool/metrics (auth: security.metrics_token Bearer, empty token =
// loopback-only).

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// durationBuckets is the sparse request-duration histogram (seconds). Sparse
// on purpose: the platform's fast paths (quota, admin API) sit in the first
// buckets, engine-backed calls trail off into the last ones.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type metricsRegistry struct {
	usageUnitsTotal  atomic.Int64
	quotaBlocked     atomic.Int64
	rateLimited      atomic.Int64
	loginFailures    atomic.Int64
	httpRequests     sync.Map // route|code -> *atomic.Int64
	engineCalls      sync.Map // engine|ok -> *atomic.Int64
	durationCount    atomic.Int64
	durationSumMicro atomic.Int64
	durationBucketsM sync.Map // bucket-index -> *atomic.Int64
}

var globalMetrics = &metricsRegistry{}

// Metrics returns the process-wide metrics registry.
func Metrics() *metricsRegistry { return globalMetrics }

func (m *metricsRegistry) counterMap(sm *sync.Map, key string) *atomic.Int64 {
	if v, ok := sm.Load(key); ok {
		return v.(*atomic.Int64)
	}
	c := &atomic.Int64{}
	actual, _ := sm.LoadOrStore(key, c)
	return actual.(*atomic.Int64)
}

// IncHTTP counts one HTTP response by route pattern and status code.
func (m *metricsRegistry) IncHTTP(route string, code int) {
	route = strings.TrimSpace(route)
	if route == "" {
		route = "unmatched"
	}
	m.counterMap(&m.httpRequests, fmt.Sprintf("%s|%d", route, code)).Add(1)
}

// AddUsageUnits accumulates billed index-check units (B-01 口径: 只计实扣).
func (m *metricsRegistry) AddUsageUnits(n int64) {
	if n > 0 {
		m.usageUnitsTotal.Add(n)
	}
}

// IncQuotaBlocked counts /v1/* and MCP requests rejected by the daily-quota
// pre-check.
func (m *metricsRegistry) IncQuotaBlocked() { m.quotaBlocked.Add(1) }

// IncRateLimited counts every rate-limit rejection (per-key, unauth, admin
// per-IP/global, MCP plane).
func (m *metricsRegistry) IncRateLimited() { m.rateLimited.Add(1) }

// IncLoginFailure counts one rejected admin login (wrong credentials; lockout
// rejections are counted by the caller as rate limited, not as new failures).
func (m *metricsRegistry) IncLoginFailure() { m.loginFailures.Add(1) }

// IncEngineCall counts one real engine invocation through the resilient
// pipeline (ok=false covers errors and panics).
func (m *metricsRegistry) IncEngineCall(engine string, ok bool) {
	okKey := "false"
	if ok {
		okKey = "true"
	}
	m.counterMap(&m.engineCalls, engine+"|"+okKey).Add(1)
}

// ObserveRequestDuration records one request latency in seconds.
func (m *metricsRegistry) ObserveRequestDuration(seconds float64) {
	m.durationCount.Add(1)
	m.durationSumMicro.Add(int64(seconds * 1e6))
	for i, upper := range durationBuckets {
		if seconds <= upper {
			m.counterMap(&m.durationBucketsM, fmt.Sprintf("%d", i)).Add(1)
		}
	}
	m.counterMap(&m.durationBucketsM, "inf").Add(1)
}

// RenderOpenMetrics renders the OpenMetrics exposition text (Content-Type
// text/plain; version=1.0.0). Output is deterministic and scrape-friendly.
func (m *metricsRegistry) RenderOpenMetrics() string {
	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	w("# HELP http_requests_total Total HTTP responses by route pattern and status code.")
	w("# TYPE http_requests_total counter")
	type kv struct {
		k string
		v int64
	}
	var httpRows []kv
	m.httpRequests.Range(func(key, value any) bool {
		httpRows = append(httpRows, kv{key.(string), value.(*atomic.Int64).Load()})
		return true
	})
	sort.Slice(httpRows, func(i, j int) bool { return httpRows[i].k < httpRows[j].k })
	for _, row := range httpRows {
		parts := strings.SplitN(row.k, "|", 2)
		w("http_requests_total{route=%q,code=%q} %d", escapeLabel(parts[0]), parts[1], row.v)
	}

	w("# HELP usage_units_total Billed index-check units (charged units only).")
	w("# TYPE usage_units_total counter")
	w("usage_units_total %d", m.usageUnitsTotal.Load())

	w("# HELP quota_blocked_total Requests rejected by the daily quota pre-check.")
	w("# TYPE quota_blocked_total counter")
	w("quota_blocked_total %d", m.quotaBlocked.Load())

	w("# HELP rate_limited_total Requests rejected by any rate limiter.")
	w("# TYPE rate_limited_total counter")
	w("rate_limited_total %d", m.rateLimited.Load())

	w("# HELP login_failures_total Rejected admin login attempts (wrong credentials).")
	w("# TYPE login_failures_total counter")
	w("login_failures_total %d", m.loginFailures.Load())

	w("# HELP engine_calls_total Real engine invocations by engine and outcome.")
	w("# TYPE engine_calls_total counter")
	var engineRows []kv
	m.engineCalls.Range(func(key, value any) bool {
		engineRows = append(engineRows, kv{key.(string), value.(*atomic.Int64).Load()})
		return true
	})
	sort.Slice(engineRows, func(i, j int) bool { return engineRows[i].k < engineRows[j].k })
	for _, row := range engineRows {
		parts := strings.SplitN(row.k, "|", 2)
		w("engine_calls_total{engine=%q,ok=%q} %d", escapeLabel(parts[0]), parts[1], row.v)
	}

	w("# HELP request_duration_seconds Request duration in seconds (sparse histogram).")
	w("# TYPE request_duration_seconds histogram")
	count := m.durationCount.Load()
	cumulative := int64(0)
	for i, upper := range durationBuckets {
		if v, ok := m.durationBucketsM.Load(fmt.Sprintf("%d", i)); ok {
			cumulative = v.(*atomic.Int64).Load()
		}
		w("request_duration_seconds_bucket{le=%q} %d", fmt.Sprintf("%g", upper), cumulative)
	}
	if v, ok := m.durationBucketsM.Load("inf"); ok {
		cumulative = v.(*atomic.Int64).Load()
	}
	w("request_duration_seconds_bucket{le=\"+Inf\"} %d", cumulative)
	w("request_duration_seconds_sum %.6f", float64(m.durationSumMicro.Load())/1e6)
	w("request_duration_seconds_count %d", count)

	return b.String()
}

// escapeLabel escapes an OpenMetrics label value (backslash, quote, newline).
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// ipIsLoopback reports whether the fiber client IP is a loopback address.
func ipIsLoopback(ip string) bool {
	ip = strings.TrimSpace(ip)
	ip = strings.Trim(ip, "[]")
	if parsed := net.ParseIP(ip); parsed != nil {
		return parsed.IsLoopback()
	}
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}
