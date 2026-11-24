package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/sirupsen/logrus"
	browserprofile "github.com/xuboboo/web2api/core/browser"
	"github.com/xuboboo/web2api/core/fpcheck"
	"github.com/xuboboo/web2api/core/fpcheck/detectors"
	"golang.org/x/time/rate"
)

var credentialedURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/@]+(?::[^\s/@]*)?@[^\s]+`)

// DefaultFingerprintArtifactDir is the artifact directory used when none is
// configured. It is relative to the server's working directory at start time.
var DefaultFingerprintArtifactDir = filepath.Join("core", "testdata")

// BasePath is the fixed URL prefix the whole application is served under:
// the admin panel, /v1/* public API, /api/* admin API, and the health/stats
// endpoints all live below it (e.g. /web-tool/health, /web-tool/v1/index/check,
// /web-tool/api/auth/login). It is intentionally NOT a configuration knob —
// deployments (container probes, reverse proxies) can rely on the constant.
// Nothing is mounted at the root; there is no unprefixed copy of any route.
const BasePath = "/web-tool"

// SearchEngine defines the contract required by the HTTP server and resilient
// search pipeline.
type SearchEngine interface {
	// Search runs a web search request and returns normalized results.
	// Implementations should return sentinel errors such as ErrCaptcha and
	// ErrSearchTimeout for policy-aware handling.
	Search(context.Context, Query) ([]SearchResult, error)
	// IsInitialized reports whether the engine is ready to serve requests.
	IsInitialized() bool
	// Name returns a stable engine identifier used in routes and telemetry.
	Name() string
	// GetRateLimiter returns an engine-specific limiter used by resilient search.
	GetRateLimiter() *rate.Limiter
}

// Server exposes Web2API HTTP endpoints backed by one or more search engines.
type Server struct {
	app           *fiber.App
	addr          string
	searchEngines []SearchEngine
	cache         *ResponseCache
	// indexCache caches index/check batch responses (S4-GAP 性能项 a) when
	// ServerOptions.IndexCheckCacheTTL > 0. Errors/cooldown units are never cached.
	indexCache *ResponseCache
	resilient  *ResilientSearcher
	startTime  time.Time
	opts       ServerOptions
	draining   atomic.Bool
	// storageKind is injected by the serve command ("bbolt"/"json") for
	// /health.storage (B-06).
	storageKind atomic.Pointer[string]
	// jobs runs large index-check batches (see index_job_manager.go). Nil when
	// ServerOptions.IndexJobDir is empty.
	jobs *IndexJobManager
}

// ServerOptions configures HTTP server middleware and resilience behavior.
type ServerOptions struct {
	// CacheTTL controls response cache entry lifetime. Zero disables caching.
	CacheTTL time.Duration
	// CacheMaxSize is the maximum number of cached entries.
	CacheMaxSize int
	// EnableCORS enables cross-origin headers with the CORS config below.
	EnableCORS bool
	// CORS contains allowed origins, methods, and headers when CORS is enabled.
	CORS CORSConfig
	// AllowEndpointFallback allows dedicated engine routes to fall back to other
	// healthy engines when the primary engine fails.
	AllowEndpointFallback bool
	// EnableDebugEndpoints enables debug-only routes such as fingerprint checks.
	EnableDebugEndpoints bool
	// FingerprintArtifactDir is where debug fingerprint screenshots are written.
	FingerprintArtifactDir string
	// FingerprintBrowserOpts are the defaults for debug fingerprint runs.
	FingerprintBrowserOpts BrowserOpts
	// Resilience defines retry/circuit-breaker/proxy strategy settings.
	Resilience ResilientConfig
	// RequestTimeout bounds wall-clock time of any request. Exceeding it
	// returns 504 request_timeout. Zero disables the bound. The serve command
	// derives it from the engine timeout and retry budget via
	// RequestTimeoutForRetries instead of exposing a separate config knob.
	RequestTimeout time.Duration
	// IndexRequestTimeout is the wall-clock cap for index-check batch routes
	// (/v1/index/check, /api/playground/check). A batch's engine rate-limiter
	// queue time scales with unit count, so these routes need a larger budget
	// than the base RequestTimeout; CheckIndexBatch narrows it per request to
	// the projected queue time. Zero keeps the base timeout everywhere.
	IndexRequestTimeout time.Duration
	// MaxConcurrentRequests bounds in-flight search/index requests to cap peak
	// browser memory on small servers. Zero disables the gate.
	MaxConcurrentRequests int

	// ResolveConfig configures wrapped result URL resolution limits.
	ResolveConfig URLResolveConfig
	// MetricsToken guards GET <BasePath>/metrics (S4-GAP 运维指标). Empty =
	// loopback-only access; non-empty = Bearer token required (any client IP).
	MetricsToken string
	// IndexCheckCacheTTL caches index/check batch responses for this long
	// (S4-GAP 性能项 a). 0 = off (每次真实打引擎, 现语义). Cache hits add
	// meta.cache:"hit" (additive); units with errors/cooldown are not cached.
	IndexCheckCacheTTL time.Duration
	// RequestAuth, when non-nil, guards the data-plane routes (dedicated
	// /{engine}/search and /stats/*) with an authentication check (契约: 数据
	// 面可选鉴权开关). It is a validator: return nil to allow, an error to
	// reject; it must NOT call c.Next().
	RequestAuth fiber.Handler
	// ExtraMiddleware runs at the very front of the chain (right after panic
	// recovery, before every route registration). The serve command uses it
	// for site-wide security headers (审计 A-19) so even the API routes core
	// registers at construction time carry them.
	ExtraMiddleware []fiber.Handler
	// IndexJobDir enables the asynchronous index-check job queue when non-empty:
	// batches larger than MaxIndexCheckURLs are accepted with 202 + job id and
	// drained at the engine's rate, with their state persisted under this
	// directory so a restart resumes them.
	IndexJobDir string
	// IndexJobMaxURLs caps one job (0 = DefaultIndexJobMaxURLs).
	IndexJobMaxURLs int
	// IndexJobChunk sizes one progress write (0 = 10 URLs).
	IndexJobChunk int
	// IndexJobMaxRetries retries a URL blocked by a transient engine condition
	// such as a cooldown or a captcha (0 = 6).
	IndexJobMaxRetries int
}

// DefaultServerOptions returns production-oriented defaults for cache, CORS,
// and resilient search policies.
func DefaultServerOptions() ServerOptions {
	return ServerOptions{
		CacheTTL:               5 * time.Minute,
		CacheMaxSize:           1000,
		EnableCORS:             true,
		CORS:                   DefaultCORSConfig(),
		AllowEndpointFallback:  false,
		EnableDebugEndpoints:   false,
		FingerprintArtifactDir: DefaultFingerprintArtifactDir,
		FingerprintBrowserOpts: BrowserOpts{
			IsHeadless: true,
			Timeout:    30 * time.Second,
		},
		Resilience: DefaultResilientConfig(),
		// 30s matches the default engine timeout (app.timeout); the serve
		// command re-derives from the configured values.
		RequestTimeout: RequestTimeoutForRetries(30*time.Second, DefaultRetryConfig()),
		ResolveConfig:  DefaultURLResolveConfig(),
	}
}

// NewServer creates a Server with DefaultServerOptions and registers all
// routes for the provided engines.
func NewServer(host string, port int, searchEngines ...SearchEngine) *Server {
	return NewServerWithOptions(host, port, DefaultServerOptions(), searchEngines...)
}

// NewServerWithOptions builds a Server, installs middleware, and registers API
// routes. The returned server is ready to Listen; call Shutdown for graceful
// stop.
func NewServerWithOptions(host string, port int, opts ServerOptions, searchEngines ...SearchEngine) *Server {
	addr := fmt.Sprintf("%s:%d", host, port)
	app := fiber.New(fiber.Config{
		ErrorHandler: JSONErrorMiddleware(),
		BodyLimit:    10 * 1024 * 1024,
		// Bounded server defaults: without these fasthttp accepts unbounded
		// connections and never times them out, so a slow client (or an
		// exposed 0.0.0.0 host) can exhaust memory. Read covers a 10MB parse
		// body on a slow link; handler time is governed separately by the
		// request-timeout middleware, and keep-alive Concurrency is left at
		// fasthttp defaults so in-process rate-limiter queuing still works.
		// WriteTimeout must cover the handler: fasthttp arms the write
		// deadline before the handler runs, so a full index-check batch
		// (queue time scales with unit count, capped by
		// IndexRequestTimeout) would otherwise lose its response at 60s.
		ReadTimeout:  60 * time.Second,
		WriteTimeout: writeTimeoutFor(opts),
		IdleTimeout:  120 * time.Second,
	})

	serv := Server{
		app:           app,
		addr:          addr,
		searchEngines: searchEngines,
		resilient:     NewResilientSearcher(searchEngines, opts.Resilience),
		startTime:     time.Now(),
		opts:          opts,
	}
	serv.draining.Store(false)
	logrus.Info("Resilient search enabled: retry + circuit breaker")
	if opts.AllowEndpointFallback {
		logrus.Warn("Dedicated endpoint fallback is enabled")
	}
	if opts.CacheTTL > 0 && opts.CacheMaxSize > 0 {
		serv.cache = NewResponseCache(opts.CacheTTL, opts.CacheMaxSize)
		logrus.WithFields(logrus.Fields{
			"cache_ttl":      opts.CacheTTL.String(),
			"cache_max_size": opts.CacheMaxSize,
		}).Info("Response cache enabled")
	}
	if opts.IndexCheckCacheTTL > 0 && opts.CacheMaxSize > 0 {
		serv.indexCache = NewResponseCache(opts.IndexCheckCacheTTL, opts.CacheMaxSize)
		logrus.WithFields(logrus.Fields{
			"cache_ttl":      opts.IndexCheckCacheTTL.String(),
			"cache_max_size": opts.CacheMaxSize,
		}).Info("Index-check response cache enabled")
	}
	if opts.IndexJobDir != "" {
		serv.jobs = NewIndexJobManager(opts.IndexJobDir, searchEngines, serv.resilient, IndexJobOptions{
			MaxURLs:        opts.IndexJobMaxURLs,
			Chunk:          opts.IndexJobChunk,
			MaxUnitRetries: opts.IndexJobMaxRetries,
		})
		serv.jobs.Start(context.Background())
		logrus.WithFields(logrus.Fields{
			"dir":       opts.IndexJobDir,
			"max_urls":  serv.jobs.opts.MaxURLs,
			"chunk":     serv.jobs.opts.Chunk,
			"max_tries": serv.jobs.opts.MaxUnitRetries,
		}).Info("Index-check job queue enabled")
	}

	// Defense-in-depth: engine panics are recovered in the resilient layer
	// (invokeEngine); this catches panics in handlers that bypass it (stats,
	// fingerprint check) so the process survives.
	app.Use(fiberrecover.New(fiberrecover.Config{EnableStackTrace: true}))
	// Caller-supplied middleware goes next so it precedes every route
	// (fiber matches in registration order) — security headers must wrap
	// responses from the API routes registered below, not only the panels.
	for _, mw := range opts.ExtraMiddleware {
		if mw != nil {
			app.Use(mw)
		}
	}
	app.Use(RequestContextMiddleware())
	if opts.RequestTimeout > 0 {
		app.Use(RequestTimeoutMiddleware(opts.RequestTimeout, opts.IndexRequestTimeout))
	}
	if opts.EnableCORS {
		app.Use(CORSMiddleware(opts.CORS))
	}
	app.Use(RequestLoggerMiddleware())
	if opts.MaxConcurrentRequests > 0 {
		app.Use(ConcurrencyLimitMiddleware(opts.MaxConcurrentRequests))
	}

	// Every route — health/stats endpoints, dedicated engine search, and the
	// platform /api /v1 groups mounted later by the serve command — lives under
	// the fixed BasePath. The root serves nothing.
	app.Get(BasePath+"/health", serv.handleHealthCheck)
	app.Get(BasePath+"/ready", serv.handleReadinessCheck)
	app.Get(BasePath+"/metrics", serv.handleMetrics)
	serv.registerDataPlaneRoute(app, BasePath+"/stats", serv.handleStats)
	serv.registerDataPlaneRoute(app, BasePath+"/stats/cache", serv.handleCacheStats)
	serv.registerDataPlaneRoute(app, BasePath+"/stats/proxy", serv.handleProxyStats)
	serv.registerDataPlaneRoute(app, BasePath+"/stats/cb", serv.handleCircuitBreakerStats)
	if opts.EnableDebugEndpoints {
		app.Get(BasePath+"/debug/fingerprint-check", serv.handleFingerprintCheck)
	}

	for _, engine := range searchEngines {
		locEngine := engine

		endpointName := engineEndpointName(locEngine.Name())

		searchHandler := func(c *fiber.Ctx) error {
			return serv.handleDedicatedEndpoint(c, locEngine)
		}

		serv.registerDataPlaneRoute(app, fmt.Sprintf("%s/%s/search", BasePath, endpointName), searchHandler)
		if canonicalName := strings.ToLower(locEngine.Name()); canonicalName != endpointName {
			serv.registerDataPlaneRoute(app, fmt.Sprintf("%s/%s/search", BasePath, canonicalName), searchHandler)
		}
	}

	return &serv
}

// registerDataPlaneRoute mounts one data-plane route, applying the optional
// RequestAuth validator (数据面可选鉴权) when configured. Health/ready stay
// public for orchestrator probes.
func (s *Server) registerDataPlaneRoute(app *fiber.App, path string, handler fiber.Handler) {
	if s.opts.RequestAuth == nil {
		app.Get(path, handler)
		return
	}
	auth := s.opts.RequestAuth
	app.Get(path, func(c *fiber.Ctx) error {
		if err := auth(c); err != nil {
			return err
		}
		return handler(c)
	})
}

func (s *Server) handleDedicatedEndpoint(c *fiber.Ctx, engine SearchEngine) error {
	startedAt := time.Now()
	requestCtx := withRequestUsage(c.UserContext(), engine.Name())
	c.SetUserContext(requestCtx)
	defer setNetworkBytesHeader(c, requestCtx)
	defer setBrowserProfileHeader(c, requestCtx)

	// 契约 A: cooldown gate first — a cooling engine fails fast (503 +
	// Retry-After) without touching the engine, before any parsing/cache work.
	gate, gateErr := s.resilient.GateEngine(requestCtx, engine.Name())
	if gateErr != nil {
		WithRequest(requestCtx).WithError(gateErr).Warn("Engine cooldown gate canceled")
		return gateErr
	}
	if gate.Blocked() {
		WithRequest(requestCtx).WithFields(logrus.Fields{
			"engine": engine.Name(),
			"reason": gate.Reason(),
			"until":  gate.Until().UTC().Format(time.RFC3339),
		}).Info("Engine in cooldown, failing fast")
		return s.errEngineCooldownForServer(engine.Name(), gate)
	}

	// A granted probe MUST be settled exactly once even when the handler
	// returns early (bad params etc.): default the outcome to "incomplete"
	// (postpones the cooldown) and overwrite it with the real search error.
	var probeErr error
	if gate.IsProbe() {
		probeErr = errProbeIncomplete
		defer func() { gate.FinishProbe(probeErr) }()
	}

	if profileID := strings.TrimSpace(c.Get(useProfileHeader)); profileID != "" {
		if _, ok := browserprofile.ProfileByID(profileID); !ok {
			return errInvalidParam(fmt.Sprintf("%s: unknown profile id %q", useProfileHeader, profileID))
		}
		requestCtx = WithForcedProfileID(requestCtx, profileID)
		c.SetUserContext(requestCtx)
	}

	q := Query{ResolveConfig: &s.opts.ResolveConfig}
	if err := q.InitFromContext(c); err != nil {
		WithRequest(c.UserContext()).WithError(err).Warn("Invalid query parameters")
		return err
	}
	if err := s.validateRequestProxyURL(&q); err != nil {
		WithRequest(c.UserContext()).WithError(err).Warn("Invalid request proxy URL")
		return err
	}

	format, err := resolveFormat(c)
	if err != nil {
		return err
	}
	// Agent-friendly alias: ?output=markdown behaves like format=markdown
	// (one LLM-quotable line per result); plain JSON responses are unchanged.
	if strings.EqualFold(strings.TrimSpace(c.Query("output", "")), "markdown") {
		format = "markdown"
	}

	requestCtx = WithQueryHash(c.UserContext(), QueryHashFromQuery(q))
	c.SetUserContext(requestCtx)

	requestID := RequestIDFromContext(requestCtx)

	action := "search"
	WithRequest(requestCtx).
		WithField("action", action).
		Debugf("Starting %s request for query: %s", action, q.Text)

	// The cooldown-expiry probe must hit the real engine — cache hits would
	// fake a recovery — so probe requests bypass the response cache.
	if format == "json" && !gate.IsProbe() && !ShouldBypassCacheForProxyMarket(q) {
		if hit, err := s.tryServeCacheHit(
			c,
			startedAt,
			cacheHitCandidate{
				key:        BuildCacheKey(engine.Name(), action, q),
				logMessage: fmt.Sprintf("Cache hit for %s %s: %s", engine.Name(), action, q.Text),
			},
		); hit || err != nil {
			return err
		}
	}

	engineNames := []string{engine.Name()}

	var (
		res        []SearchResult
		usedEngine string
		proxyMeta  ProxyExecutionMeta
		searchErr  error
	)
	if gate.IsProbe() {
		// Single-engine probe: fallback would dilute the recovery signal.
		res, usedEngine, proxyMeta, searchErr = s.resilient.SearchPrimary(requestCtx, engine, q)
		probeErr = searchErr
	} else if s.opts.AllowEndpointFallback {
		res, usedEngine, proxyMeta, searchErr = s.resilient.SearchWithFallback(requestCtx, engine, q)
	} else {
		res, usedEngine, proxyMeta, searchErr = s.resilient.SearchPrimary(requestCtx, engine, q)
	}
	s.applyProxyHeaders(c, proxyMeta)

	if searchErr != nil {
		WithRequest(requestCtx).WithFields(logrus.Fields{"action": action}).WithError(searchErr).Error("Search failed")
		return searchAPIError(searchErr, usedEngine, q, proxyMeta)
	}

	env := NewEnvelope(q, requestID, startedAt, engineNames)
	if usedEngine != "" && usedEngine != engine.Name() {
		env.Meta.EnginesFailed = []string{engine.Name()}
	}
	env.Meta.EnginesCooldown = s.enginesCooldownMeta()
	ectx := EnrichContext{Engine: usedEngine, Query: q}
	for _, r := range res {
		AppendEnrichedSearchResult(env, r, ectx, startedAt)
	}
	env.Finalize(startedAt, q)

	if format == "json" {
		cacheStatus := s.cacheEnvelopeIfEligible(engine.Name(), usedEngine, action, q, env)
		if cacheStatus != "" {
			c.Set("X-Cache", cacheStatus)
		}
	}
	if usedEngine != "" && usedEngine != engine.Name() {
		c.Set("X-Fallback-Engine", usedEngine)
	}

	completionCtx := requestCtx
	if usedEngine != "" {
		completionCtx = WithEngine(completionCtx, usedEngine)
	}
	WithRequest(completionCtx).WithFields(logrus.Fields{"action": action, "results_count": len(res)}).Info("Search completed")
	return sendEnvelope(c, format, env)
}

type searchErrorSpec struct {
	status  int
	code    string
	message string
}

func mapSearchError(err error) searchErrorSpec {
	switch {
	case errors.Is(err, ErrCaptcha):
		return searchErrorSpec{status: fiber.StatusTooManyRequests, code: "captcha_detected", message: "captcha detected"}
	case errors.Is(err, ErrBlocked):
		return searchErrorSpec{status: fiber.StatusForbidden, code: "blocked", message: "search engine blocked the request"}
	case errors.Is(err, ErrRateLimited):
		return searchErrorSpec{status: fiber.StatusTooManyRequests, code: "blocked", message: "search engine blocked the request (HTTP 429)"}
	case errors.Is(err, ErrSearchTimeout):
		return searchErrorSpec{status: fiber.StatusGatewayTimeout, code: "search_timeout", message: ErrSearchTimeout.Error()}
	case errors.Is(err, ErrProxyAuth):
		return searchErrorSpec{status: fiber.StatusServiceUnavailable, code: "proxy_auth", message: "proxy authentication failed"}
	case errors.Is(err, ErrProxyConnect):
		return searchErrorSpec{status: fiber.StatusServiceUnavailable, code: "proxy_connect", message: "proxy connection failed"}
	case errors.Is(err, ErrTimeout):
		return searchErrorSpec{status: fiber.StatusServiceUnavailable, code: "proxy_timeout", message: "proxy request timed out"}
	case errors.Is(err, ErrProxyUnavailable):
		return searchErrorSpec{status: fiber.StatusServiceUnavailable, code: "proxy_unavailable", message: "proxy unavailable"}
	case errors.Is(err, ErrParser):
		return searchErrorSpec{status: fiber.StatusBadGateway, code: "parser_failure", message: "parser failure"}
	case errors.Is(err, ErrEngineInternal):
		return searchErrorSpec{status: fiber.StatusBadGateway, code: "engine_internal", message: "engine internal error"}
	case errors.Is(err, ErrNotImplemented):
		return searchErrorSpec{status: fiber.StatusNotImplemented, code: "not_implemented", message: "capability not implemented for this engine"}
	case errors.Is(err, ErrAllEnginesFailed):
		return searchErrorSpec{status: fiber.StatusBadGateway, code: "all_engines_failed", message: "all search engines failed"}
	case errors.Is(err, ErrCircuitOpen):
		return searchErrorSpec{status: fiber.StatusServiceUnavailable, code: "circuit_open", message: "engine circuit breaker is open"}
	// 限流排队死亡: the unit's projected rate-limiter token wait exceeded the
	// remaining request budget, so it never reached the engine. Reporting it
	// as request_timeout would blame the engine for a queue-budget problem.
	case IsRateLimiterQueueErr(err):
		return searchErrorSpec{status: fiber.StatusGatewayTimeout, code: "engine_queue_exceeded", message: "engine rate-limiter queue exceeded the request budget (unit never reached the engine)"}
	case errors.Is(err, context.DeadlineExceeded):
		return searchErrorSpec{status: fiber.StatusGatewayTimeout, code: "request_timeout", message: "request timed out"}
	case errors.Is(err, context.Canceled):
		return searchErrorSpec{status: fiber.StatusServiceUnavailable, code: "request_canceled", message: "request canceled"}
	}
	// B-08: uncategorized engine errors keep a fixed message — the raw error
	// string may carry internal paths or upstream URL fragments. The original
	// error goes to the server logs (call sites log "Search failed" with the
	// error attached) and the sanitized excerpt stays in meta.error_detail.
	return searchErrorSpec{status: fiber.StatusBadGateway, code: "engine_internal", message: "engine internal error"}
}

func searchAPIError(err error, engineName string, q Query, proxyMeta ProxyExecutionMeta) *APIError {
	spec := mapSearchError(err)
	meta := searchErrorMeta(engineName, q, proxyMeta)
	if errors.Is(err, ErrRateLimited) {
		meta["upstream_status"] = fiber.StatusTooManyRequests
	}
	addErrorDetail(meta, err, spec.message, q)
	return &APIError{
		HTTPStatus: spec.status,
		ErrorCode:  spec.code,
		Message:    spec.message,
		Meta:       meta,
	}
}

func searchErrorMeta(engineName string, q Query, proxyMeta ProxyExecutionMeta) map[string]interface{} {
	meta := map[string]interface{}{}
	if strings.TrimSpace(engineName) != "" {
		meta["engine"] = engineName
	}
	proxyUsed := strings.TrimSpace(proxyMeta.Used)
	if proxyUsed == "" && strings.TrimSpace(q.ProxyURL) != "" {
		proxyUsed = MaskProxyURL(q.ProxyURL)
	}
	if proxyUsed != "" {
		meta["proxy_used"] = proxyUsed
	}
	if q.ProxyCountry != "" {
		meta["proxy_country"] = q.ProxyCountry
	}
	if q.ProxyClass != "" {
		meta["proxy_class"] = q.ProxyClass
	}
	if q.ProxyProvider != "" {
		meta["proxy_provider"] = q.ProxyProvider
	}
	if q.ProxySessionID != "" {
		meta["proxy_session_id"] = q.ProxySessionID
	}
	return meta
}

func addErrorDetail(meta map[string]interface{}, err error, message string, q Query) {
	if err == nil {
		return
	}
	detail := sanitizeErrorDetail(err.Error(), q)
	if detail == "" || detail == message {
		return
	}
	meta["error_detail"] = detail
}

func sanitizeErrorDetail(detail string, q Query) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	if q.ProxyURL != "" {
		detail = strings.ReplaceAll(detail, q.ProxyURL, MaskProxyURL(q.ProxyURL))
	}
	return maskCredentialedURLs(detail)
}

func maskCredentialedURLs(detail string) string {
	return credentialedURLPattern.ReplaceAllStringFunc(detail, func(raw string) string {
		trimmed := strings.TrimRight(raw, `.,;)]}`)
		suffix := strings.TrimPrefix(raw, trimmed)
		return MaskProxyURL(trimmed) + suffix
	})
}

// cacheEnvelopeIfEligible stores the envelope JSON and returns the cache status header value.
func (s *Server) cacheEnvelopeIfEligible(engineName, usedEngine, action string, q Query, payload interface{}) string {
	if s.cache == nil {
		return ""
	}
	if ShouldBypassCacheForProxyMarket(q) {
		s.cache.RecordBypass()
		return "BYPASS"
	}
	// Don't cache fallback responses so the primary engine can recover.
	if usedEngine != engineName {
		s.cache.RecordBypass()
		return "BYPASS"
	}
	// Detect empty results via reflection-free type switch.
	switch v := payload.(type) {
	case *Envelope:
		if len(v.Results) == 0 {
			s.cache.RecordBypass()
			return "BYPASS"
		}
	}
	cacheKey := BuildCacheKey(engineName, action, q)
	if s.cacheJSON(cacheKey, payload) {
		return "MISS"
	}
	return "BYPASS"
}

// HealthStatus is returned by /health and summarizes service state.
// B-07 版本指纹收敛: the former system block (goroutines/memory_mb/go_version)
// moved behind the admin-plane JWT (GET /web-tool/api/runtime); the public
// endpoint only carries status/uptime/engines plus the storage kind
// (B-06: storage=bbolt|json, omitted when the platform is disabled).
type HealthStatus struct {
	Status  string         `json:"status"`
	Uptime  string         `json:"uptime"`
	Engines []EngineHealth `json:"engines"`
	Storage string         `json:"storage,omitempty"`
}

// ReadinessStatus is returned by /ready to indicate if this instance can
// receive new traffic.
type ReadinessStatus struct {
	Status string `json:"status"`
}

// EngineHealth describes availability of one configured engine.
type EngineHealth struct {
	Name        string `json:"name"`
	Initialized bool   `json:"initialized"`
	Status      string `json:"status"`
}

// handleHealthCheck returns current service and engine status.
// Degraded state stays HTTP 200 to avoid unnecessary restarts in orchestrators.
func (s *Server) handleHealthCheck(c *fiber.Ctx) error {
	engines := make([]EngineHealth, 0, len(s.searchEngines))
	availableEngines := 0

	for _, engine := range s.searchEngines {
		status := "ready"
		isAvailable := true
		if !engine.IsInitialized() {
			status = "not_initialized"
			isAvailable = false
		}

		for _, cbStat := range s.resilient.GetCircuitBreakerStats() {
			engineName, _ := cbStat["engine"].(string)
			if engineName != engine.Name() {
				continue
			}
			circuitState, _ := cbStat["state"].(string)
			if circuitState == "open" {
				status = "circuit_open"
				isAvailable = false
			}
			break
		}

		if isAvailable {
			availableEngines++
		}

		engines = append(engines, EngineHealth{
			Name:        engine.Name(),
			Initialized: engine.IsInitialized(),
			Status:      status,
		})
	}

	overallStatus := "healthy"
	totalEngines := len(s.searchEngines)
	switch {
	case totalEngines == 0 || availableEngines == 0:
		overallStatus = "unhealthy"
	case availableEngines < totalEngines:
		overallStatus = "degraded"
	}

	health := HealthStatus{
		Status:  overallStatus,
		Uptime:  time.Since(s.startTime).Round(time.Second).String(),
		Engines: engines,
	}
	if kind := s.storageKind.Load(); kind != nil {
		health.Storage = *kind
	}

	if overallStatus == "unhealthy" {
		c.Status(fiber.StatusServiceUnavailable)
	}
	return c.JSON(health)
}

func (s *Server) handleReadinessCheck(c *fiber.Ctx) error {
	status := ReadinessStatus{Status: "ready"}
	if s.draining.Load() {
		status.Status = "draining"
		return c.Status(fiber.StatusServiceUnavailable).JSON(status)
	}
	return c.JSON(status)
}

// SetStorageKind injects the storage kind reported by /health.storage
// (B-06: storage=bbolt|json; omitted when the platform is disabled).
func (s *Server) SetStorageKind(kind string) {
	s.storageKind.Store(&kind)
}

// handleMetrics serves the hand-written OpenMetrics text (S4-GAP 运维指标).
// Auth: security.metrics_token Bearer when configured; otherwise only
// loopback clients may scrape (挂载于公开前缀,防止指纹与负载数据外泄).
func (s *Server) handleMetrics(c *fiber.Ctx) error {
	if token := strings.TrimSpace(s.opts.MetricsToken); token != "" {
		auth := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
		if auth != "Bearer "+token {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
	} else if !ipIsLoopback(c.IP()) {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
	}
	c.Set(fiber.HeaderContentType, "text/plain; version=1.0.0; charset=utf-8")
	return c.Send([]byte(Metrics().RenderOpenMetrics()))
}

func (s *Server) handleStats(c *fiber.Ctx) error {
	return c.JSON(map[string]interface{}{
		"cache":            s.cacheStatsPayload(),
		"proxy":            s.resilient.GetProxyStats(),
		"circuit_breakers": s.resilient.GetCircuitBreakerStats(),
		"captcha":          CaptchaSolverMetrics(),
	})
}

func (s *Server) handleCacheStats(c *fiber.Ctx) error {
	return c.JSON(s.cacheStatsPayload())
}

func (s *Server) handleProxyStats(c *fiber.Ctx) error {
	return c.JSON(s.resilient.GetProxyStats())
}

func (s *Server) handleCircuitBreakerStats(c *fiber.Ctx) error {
	return c.JSON(map[string]interface{}{
		"circuit_breakers": s.resilient.GetCircuitBreakerStats(),
	})
}

func (s *Server) handleFingerprintCheck(c *fiber.Ctx) error {
	requestCtx := withRequestUsage(c.UserContext(), "fingerprint")
	c.SetUserContext(requestCtx)
	defer setNetworkBytesHeader(c, requestCtx)
	defer setBrowserProfileHeader(c, requestCtx)

	if profileID := strings.TrimSpace(c.Get(useProfileHeader)); profileID != "" {
		if _, ok := browserprofile.ProfileByID(profileID); !ok {
			return errInvalidParam(fmt.Sprintf("%s: unknown profile id %q", useProfileHeader, profileID))
		}
		requestCtx = WithForcedProfileID(requestCtx, profileID)
		c.SetUserContext(requestCtx)
	}

	req, err := s.parseFingerprintCheckRequest(c)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithTimeout(requestCtx, time.Duration(req.timeoutMs)*time.Millisecond)
	defer cancel()

	browser, err := NewBrowser(req.browserOpts)
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, fmt.Sprintf("failed to create debug browser: %v", err))
	}
	defer func() {
		if closeErr := browser.Close(); closeErr != nil {
			WithRequest(c.UserContext()).WithError(closeErr).Warn("failed to close debug fingerprint browser")
		}
	}()

	artifactDir := defaultFingerprintArtifactDir(s.opts.FingerprintArtifactDir)

	reports := make([]fpcheck.Report, 0, len(req.detectors))
	for idx, detector := range req.detectors {
		runOpts := fpcheck.RunOptions{
			ArtifactDir: artifactDir,
		}
		if req.waitMs > 0 && idx == len(req.detectors)-1 {
			runOpts.WaitBeforeExtract = time.Duration(req.waitMs) * time.Millisecond
		}

		report, runErr := fpcheck.RunWithOptions(runCtx, browser, detector, runOpts)
		if runErr != nil {
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				return fiber.NewError(fiber.StatusGatewayTimeout, fmt.Sprintf("fingerprint check timed out after %dms", req.timeoutMs))
			}
			return fiber.NewError(
				fiber.StatusServiceUnavailable,
				fmt.Sprintf("detector %s failed: %v", detector.Name(), runErr),
			)
		}
		reports = append(reports, report)
	}

	return c.JSON(reports)
}

type fingerprintCheckRequest struct {
	detectors   []fpcheck.Detector
	timeoutMs   int
	waitMs      int
	browserOpts BrowserOpts
}

func (s *Server) parseFingerprintCheckRequest(c *fiber.Ctx) (fingerprintCheckRequest, error) {
	detectorName := strings.TrimSpace(c.Query("detector", "all"))
	customURL := strings.TrimSpace(c.Query("url", ""))
	customSelector := strings.TrimSpace(c.Query("selector", ""))
	selectedDetectors, err := detectors.SelectWithCustomSelector(detectorName, customURL, customSelector)
	if err != nil {
		return fingerprintCheckRequest{}, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	headless, err := parseOptionalBoolQuery(c.Query("headless", ""), true)
	if err != nil {
		return fingerprintCheckRequest{}, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid headless query value: %v", err))
	}
	if !headless && strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		WithRequest(c.UserContext()).Warn("headless=false ignored because DISPLAY is not set; forcing headless mode")
		headless = true
	}

	timeoutMs, err := parsePositiveIntQuery(c.Query("timeout_ms", ""), 150000)
	if err != nil {
		return fingerprintCheckRequest{}, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid timeout_ms query value: %v", err))
	}
	waitMs, err := parseNonNegativeIntQuery(c.Query("wait_ms", ""), 0)
	if err != nil {
		return fingerprintCheckRequest{}, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid wait_ms query value: %v", err))
	}

	browserOpts := s.opts.FingerprintBrowserOpts
	browserOpts.IsHeadless = headless
	browserOpts.Timeout = time.Duration(timeoutMs) * time.Millisecond
	browserOpts.LeavePageOpen = false
	browserOpts.UserAgent = strings.TrimSpace(c.Query("user_agent", browserOpts.UserAgent))
	browserOpts.LanguageCode = strings.TrimSpace(c.Query("language", browserOpts.LanguageCode))

	// The ?proxy= query goes through the exact same gate as the X-Proxy-URL
	// header so it cannot bypass AllowRequestProxyURL.
	requestProxyURL := strings.TrimSpace(c.Query("proxy", ""))
	if headerProxyURL := strings.TrimSpace(c.Get("X-Proxy-URL")); headerProxyURL != "" {
		requestProxyURL = headerProxyURL
	}
	if requestProxyURL != "" {
		if !s.opts.Resilience.Proxy.Proxies.AllowRequestProxyURL {
			return fingerprintCheckRequest{}, &APIError{
				HTTPStatus: fiber.StatusBadRequest,
				ErrorCode:  "bad_request",
				Reason:     ReasonRequestProxyURLDisabled,
				Message:    "X-Proxy-URL is disabled by server configuration",
			}
		}
		normalized, err := NormalizeProxyURL(requestProxyURL)
		if err != nil {
			return fingerprintCheckRequest{}, errInvalidParam(fmt.Sprintf("X-Proxy-URL: %v", err))
		}
		if IsAuthenticatedSocksProxyURL(normalized) {
			return fingerprintCheckRequest{}, &APIError{
				HTTPStatus: fiber.StatusBadRequest,
				ErrorCode:  "bad_request",
				Reason:     ReasonUnsupportedProxyScheme,
				Message:    "authenticated SOCKS proxies are not supported in browser mode",
			}
		}
		browserOpts.ProxyURL = normalized
	}

	insecureDefault := browserOpts.Insecure
	if detectors.IsCustom(detectorName) {
		insecureDefault = true
	}
	insecure, err := parseOptionalBoolQuery(c.Query("insecure", ""), insecureDefault)
	if err != nil {
		return fingerprintCheckRequest{}, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid insecure query value: %v", err))
	}
	browserOpts.Insecure = insecure

	return fingerprintCheckRequest{
		detectors:   selectedDetectors,
		timeoutMs:   timeoutMs,
		waitMs:      waitMs,
		browserOpts: browserOpts,
	}, nil
}

func defaultFingerprintArtifactDir(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultFingerprintArtifactDir
	}
	return trimmed
}

func parseOptionalBoolQuery(raw string, defaultValue bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, err
	}
	return value, nil
}

func parsePositiveIntQuery(raw string, defaultValue int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return value, nil
}

func parseNonNegativeIntQuery(raw string, defaultValue int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return value, nil
}

// engineEndpointName maps an engine name to its public URL slug. 360 Search is
// best known as "360"; serve /360/search and keep /so360/search working too.
func engineEndpointName(name string) string {
	name = strings.ToLower(name)
	switch name {
	case "so360":
		return "360"
	}
	return name
}

func resolveEngineAlias(name string) string {
	switch strings.ToLower(name) {
	case "360", "so", "so.com":
		return "so360"
	case "sougou":
		return "sogou"
	default:
		return strings.ToLower(name)
	}
}

type cacheHitCandidate struct {
	key        string
	logMessage string
}

func (s *Server) tryServeCacheHit(c *fiber.Ctx, startedAt time.Time, candidates ...cacheHitCandidate) (bool, error) {
	if s.cache == nil {
		return false, nil
	}
	for _, candidate := range candidates {
		cached, ok := s.cache.Get(candidate.key)
		if !ok {
			continue
		}
		cached = refreshCachedMeta(cached, RequestIDFromContext(c.UserContext()), startedAt)
		c.Set("Content-Type", "application/json")
		c.Set("X-Cache", "HIT")
		WithRequest(c.UserContext()).Debug(candidate.logMessage)
		return true, c.Send(cached)
	}
	return false, nil
}

func refreshCachedMeta(data []byte, requestID string, startedAt time.Time) []byte {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return data
	}
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		return data
	}
	meta["request_id"] = requestID
	meta["requested_at"] = startedAt.UTC().Format(time.RFC3339)
	delete(meta, "timestamp")
	meta["took_ms"] = time.Since(startedAt).Milliseconds()

	refreshed, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return refreshed
}

func (s *Server) cacheJSON(cacheKey string, payload interface{}) bool {
	if s.cache == nil {
		return false
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.cache.RecordBypass()
		return false
	}
	s.cache.Set(cacheKey, data)
	return true
}

func (s *Server) cacheStatsPayload() interface{} {
	if s.cache == nil {
		return map[string]interface{}{"status": false}
	}
	return s.cache.Stats()
}

func (s *Server) applyProxyHeaders(c *fiber.Ctx, meta ProxyExecutionMeta) {
	mode := meta.Mode
	if mode == "" {
		mode = ProxyModeOff
	}

	tag := meta.Tag
	used := meta.Used

	if mode == ProxyModeOff {
		tag = ""
		used = "direct"
	}

	c.Set("X-Proxy-Mode", mode)
	if tag != "" {
		c.Set("X-Proxy-Tag", tag)
	}
	c.Set("X-Proxy-Used", used)
	if meta.Attempts > 1 {
		c.Set("X-Proxy-Attempts", strconv.Itoa(meta.Attempts))
	}
}

func setNetworkBytesHeader(c *fiber.Ctx, ctx context.Context) {
	c.Set("X-Network-Bytes", strconv.FormatInt(NetworkBytesFromContext(ctx), 10))
}

func setBrowserProfileHeader(c *fiber.Ctx, ctx context.Context) {
	profileIDs := BrowserProfileIDsFromContext(ctx)
	if len(profileIDs) == 0 {
		return
	}
	c.Set(browserProfileIDHeader, strings.Join(profileIDs, ","))
}

func withRequestUsage(ctx context.Context, engine string) context.Context {
	return WithBrowserProfileUsage(WithNetworkUsage(WithEngine(ctx, engine)))
}

func (s *Server) validateRequestProxyURL(q *Query) error {
	if q == nil || strings.TrimSpace(q.ProxyURL) == "" || q.ProxyOverride == ProxyOverrideDirect {
		return nil
	}

	if !s.opts.Resilience.Proxy.Proxies.AllowRequestProxyURL {
		return &APIError{
			HTTPStatus: fiber.StatusBadRequest,
			ErrorCode:  "bad_request",
			Reason:     ReasonRequestProxyURLDisabled,
			Message:    "X-Proxy-URL is disabled by server configuration",
		}
	}

	if s.opts.Resilience.Proxy.Runtime == ProxyRuntimeBrowser && IsAuthenticatedSocksProxyURL(q.ProxyURL) {
		return &APIError{
			HTTPStatus: fiber.StatusBadRequest,
			ErrorCode:  "bad_request",
			Reason:     ReasonUnsupportedProxyScheme,
			Message:    "authenticated SOCKS proxies are not supported in browser mode",
		}
	}
	return nil
}

// resolveFormat returns the output format from ?format= or Accept header.
// Supported values: "json" (default), "markdown", "text", "ndjson".
func resolveFormat(c *fiber.Ctx) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(c.Query("format", "")))
	if raw == "" {
		accept := strings.ToLower(c.Get("Accept"))
		switch {
		case strings.Contains(accept, "text/markdown"):
			raw = "markdown"
		case strings.Contains(accept, "text/plain"):
			raw = "text"
		case strings.Contains(accept, "application/x-ndjson"):
			raw = "ndjson"
		default:
			raw = "json"
		}
	}
	switch raw {
	case "json", "markdown", "text", "ndjson":
		return raw, nil
	}
	return "", &APIError{HTTPStatus: 400, Reason: ReasonUnknownFormat,
		Message: fmt.Sprintf("unknown format %q: accepted values are json, markdown, text, ndjson", raw)}
}

// sendEnvelope serialises env according to the requested format and writes the
// response. For non-JSON formats the envelope is NOT cached because format
// variants would pollute the JSON cache.
func sendEnvelope(c *fiber.Ctx, format string, env *Envelope) error {
	switch format {
	case "markdown":
		c.Set("Content-Type", "text/markdown; charset=utf-8")
		return c.Send(RenderMarkdown(env))
	case "text":
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.Send(RenderText(env))
	case "ndjson":
		c.Set("Content-Type", "application/x-ndjson; charset=utf-8")
		return c.Send(RenderNDJSON(env))
	default:
		return c.JSON(env)
	}
}

// indexCheckRequestBody is the POST /v1/index/check JSON body.
type indexCheckRequestBody struct {
	URLs    []string `json:"urls"`
	Engines []string `json:"engines"`
}

// parseIndexCheckRequest reads the request parameters for both supported
// shapes: POST body {"urls":[...],"engines":[...]} and GET
// ?url=...&engines=baidu,bing. It returns the URL list (trimmed, at least one
// entry; the 20-URL sync ceiling and the job ceiling are enforced by the
// handler so each deployment's limit applies), the requested engine names (may
// be empty = all engines), or a 400 APIError with reason EMPTY_QUERY |
// INVALID_PARAM.
func parseIndexCheckRequest(c *fiber.Ctx) ([]string, []string, *APIError) {
	var (
		rawURLs    []string
		rawEngines []string
	)
	if c.Method() == fiber.MethodPost {
		var body indexCheckRequestBody
		if err := c.BodyParser(&body); err != nil {
			return nil, nil, errInvalidParam(fmt.Sprintf(
				"request body must be JSON: {\"urls\":[\"...\"],\"engines\":[\"...\"]}: %v", err))
		}
		rawURLs, rawEngines = body.URLs, body.Engines
	} else {
		if raw := c.Query("url"); strings.TrimSpace(raw) != "" {
			rawURLs = []string{raw}
		}
		if engines := strings.TrimSpace(c.Query("engines")); engines != "" {
			rawEngines = strings.Split(engines, ",")
		}
	}

	urls := make([]string, 0, len(rawURLs))
	for _, raw := range rawURLs {
		urls = append(urls, strings.TrimSpace(raw))
	}
	switch {
	case len(urls) == 0:
		return nil, nil, errEmptyQuery()
	}
	for i, u := range urls {
		if _, ok := indexURLKeyFrom(u); !ok {
			return nil, nil, errInvalidParam(fmt.Sprintf("urls[%d]: %q is not a valid absolute URL", i, u))
		}
	}
	return urls, rawEngines, nil
}

// IndexCheckHandler serves the public index-status API:
//
//	POST /v1/index/check  body {"urls":["..."](1..index_jobs.max_urls),"engines":[...](optional)}
//	GET  /v1/index/check?url=...&engines=baidu,bing
//
// Up to MaxIndexCheckURLs (20) URLs answer synchronously with the contract
// envelope; a larger batch is accepted into the async job queue and answers 202
// with a job id (see index_job_http.go). The console playground keeps the
// 20-URL ceiling because it renders the synchronous shape only.
//
// Every (URL × engine) unit runs through the resilient pipeline; engine-level
// failures (403/429/captcha/circuit open/cooldown/timeout) are reported inside
// that unit's error field and the HTTP status stays 200 — unless EVERY
// selected engine is cooling, when the whole request 503s with
// engine_cooldown + Retry-After. Authentication, quota accounting, and route
// registration are owned by the platform module. The response structure is
// byte-identical to POST /api/playground/check and the MCP check_indexed tool
// because all three mount points share CheckIndexBatch.
func (s *Server) IndexCheckHandler(c *fiber.Ctx) error {
	startedAt := time.Now()
	requestCtx := withRequestUsage(c.UserContext(), "index_check")
	c.SetUserContext(requestCtx)

	urls, rawEngines, apiErr := parseIndexCheckRequest(c)
	if apiErr != nil {
		WithRequest(requestCtx).WithField("reason", apiErr.Reason).Warn("Invalid index check parameters")
		return apiErr
	}

	// 批量提交: keeping the ≤20 sync contract means existing callers need no
	// change; a bigger batch becomes a job instead of a 400, so one submission
	// can cover a whole export and the server owns the pacing.
	if len(urls) > MaxIndexCheckURLs {
		// The console playground renders the synchronous envelope only, so it
		// keeps the 20-URL ceiling instead of receiving a job it cannot follow.
		if s.jobs == nil || IsPlaygroundIndexCheckPath(c.Path()) {
			return errTooManyURLs(fmt.Sprintf("urls accepts at most %d entries, got %d", MaxIndexCheckURLs, len(urls)))
		}
		return s.submitIndexJob(c, requestCtx, urls, rawEngines)
	}

	payload, err := s.CheckIndexBatch(requestCtx, urls, rawEngines)
	if err != nil {
		return err
	}
	WithRequest(requestCtx).WithField("took_ms", time.Since(startedAt).Milliseconds()).Info("Index check completed")
	return c.JSON(payload)
}

// CheckIndexBatch is the shared synchronous index-check orchestration used by
// /v1/index/check (≤20 URLs), POST /api/playground/check and the MCP
// check_indexed tool. urls must be 1..20 absolute URLs — larger batches go
// through IndexJobManager instead; engines holds the raw requested names
// (nil/empty = every available engine). It returns an IndexCheckResponse, or
// an *APIError: 400 for invalid parameters, 503 engine_cooldown (with
// Retry-After and meta.engines_cooldown) when every selected engine is
// currently cooling.
func (s *Server) CheckIndexBatch(ctx context.Context, urls []string, engines []string) (any, error) {
	startedAt := time.Now()
	requestCtx := withRequestUsage(ctx, "index_check")

	urls, apiErr := validateIndexCheckURLs(urls)
	if apiErr != nil {
		WithRequest(requestCtx).WithField("reason", apiErr.Reason).Warn("Invalid index check parameters")
		return nil, apiErr
	}
	selected, selErr := SelectIndexCheckEngines(s.searchEngines, engines)
	if selErr != nil {
		WithRequest(requestCtx).WithField("reason", selErr.Reason).Warn("Invalid index check engines")
		return nil, selErr
	}

	// 收录批量预算: the middleware base budget covers one unit's retry cycle,
	// not the batch's rate-limiter queue time (baidu issues one token per 5s).
	// Narrow the index-route cap to the projected queue time so the batch
	// genuinely completes; when the request carries its own shorter deadline
	// (MCP caller), context.WithTimeout keeps the tighter one.
	if s.opts.RequestTimeout > 0 {
		if dl := IndexCheckBatchDeadline(s.opts.RequestTimeout, len(urls), selected, s.resilient.retryCfg.MaxRetries); dl > s.opts.RequestTimeout {
			var cancel context.CancelFunc
			requestCtx, cancel = context.WithTimeout(requestCtx, dl)
			defer cancel()
			WithRequest(requestCtx).WithFields(logrus.Fields{
				"batch_deadline_ms": dl.Milliseconds(),
				"units":             len(urls) * len(selected),
			}).Debug("Index check batch deadline scaled to rate-limiter queue time")
		}
	}

	requestID := RequestIDFromContext(requestCtx)
	WithRequest(requestCtx).WithFields(logrus.Fields{
		"urls":    len(urls),
		"engines": len(selected),
		"units":   len(urls) * len(selected),
	}).Info("Index check started")

	// 契约 A batch semantics: only when EVERY selected engine is cooling does
	// the whole request 503 (fail fast, zero engine contact). Otherwise units
	// of cooling engines answer engine_cooldown while the rest still judge.
	if cooling := s.coolingEntriesFor(selected); len(selected) > 0 && len(cooling) == len(selected) {
		err := errCooldownBatchAsAPIError(&engineCooldownBatchError{
			RetryAfterSeconds: retryAfterSecondsFor(cooling, time.Now()),
			Cooldowns:         cooling,
		})
		WithRequest(requestCtx).WithField("engines", strings.Join(IndexCheckEngineNames(selected), ",")).Info("All requested engines in cooldown, failing fast")
		return nil, err
	}

	// S4-GAP 性能项(a): index/check response cache. Default off
	// (cache.index_check_ttl_seconds=0 keeps the "every check hits the real
	// engine" semantics); when enabled, a fully-successful batch for the same
	// (urls × engines) set within the TTL answers from memory with
	// meta.cache="hit" (additive). Responses with any unit error / cooldown
	// verdict are never cached, so recovery is never masked beyond the TTL.
	if s.indexCache != nil {
		key := buildIndexCheckCacheKey(urls, selected)
		if cached, ok := s.indexCache.Get(key); ok {
			var hit IndexCheckResponse
			if err := json.Unmarshal(cached, &hit); err == nil {
				hit.ID = requestID
				hit.CreatedAt = startedAt.UTC().Format(time.RFC3339)
				hit.Meta.RequestID = requestID
				hit.Meta.TookMs = time.Since(startedAt).Milliseconds()
				hit.Meta.EnginesCooldown = s.enginesCooldownMeta()
				hit.Meta.Cache = "hit"
				WithRequest(requestCtx).WithField("took_ms", hit.Meta.TookMs).Debug("Index check cache hit")
				return hit, nil
			}
		}
	}

	results := RunIndexCheckUnits(requestCtx, s.resilient, selected, urls)

	WithRequest(requestCtx).WithField("took_ms", time.Since(startedAt).Milliseconds()).Info("Index check completed")
	resp := IndexCheckResponse{
		ID:        requestID,
		CreatedAt: startedAt.UTC().Format(time.RFC3339),
		Results:   results,
		Meta: IndexCheckMeta{
			RequestID:       requestID,
			TookMs:          time.Since(startedAt).Milliseconds(),
			Units:           len(urls) * len(selected),
			EnginesCooldown: s.enginesCooldownMeta(),
		},
	}
	if s.indexCache != nil && !indexCheckHasUnitError(results) {
		if data, err := json.Marshal(resp); err == nil {
			s.indexCache.Set(buildIndexCheckCacheKey(urls, selected), data)
		}
	}
	return resp, nil
}

// buildIndexCheckCacheKey derives the index-check cache key from the cleaned
// URL list and the selected engine names (order-sensitive on URLs, because
// the response rows follow the request order).
func buildIndexCheckCacheKey(urls []string, engines []SearchEngine) string {
	raw := "ic|" + strings.Join(urls, "\x1f") + "|" + strings.Join(IndexCheckEngineNames(engines), ",")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// indexCheckHasUnitError reports whether any unit carried an error verdict —
// such batches are excluded from the index-check response cache.
func indexCheckHasUnitError(results []IndexCheckURLResult) bool {
	for _, r := range results {
		for _, outcome := range r.Engines {
			if outcome.err != nil {
				return true
			}
		}
	}
	return false
}

// validateIndexCheckURLs applies the platform URL rules (trim, 1..20 entries,
// absolute URL) shared by the fiber request parser and CheckIndexBatch.
func validateIndexCheckURLs(urls []string) ([]string, *APIError) {
	cleaned := make([]string, 0, len(urls))
	for _, raw := range urls {
		cleaned = append(cleaned, strings.TrimSpace(raw))
	}
	switch {
	case len(cleaned) == 0:
		return nil, errEmptyQuery()
	case len(cleaned) > MaxIndexCheckURLs:
		return nil, errTooManyURLs(fmt.Sprintf("urls accepts at most %d entries, got %d", MaxIndexCheckURLs, len(cleaned)))
	}
	for i, u := range cleaned {
		if _, ok := indexURLKeyFrom(u); !ok {
			return nil, errInvalidParam(fmt.Sprintf("urls[%d]: %q is not a valid absolute URL", i, u))
		}
	}
	return cleaned, nil
}

// engineByName resolves a requested engine name (alias-aware: 360/so/so.com →
// so360, sougou → sogou) against the registered engines; nil when unknown.
func (s *Server) engineByName(name string) SearchEngine {
	wanted := resolveEngineAlias(strings.TrimSpace(name))
	for _, engine := range s.searchEngines {
		if strings.ToLower(engine.Name()) == wanted {
			return engine
		}
	}
	return nil
}

// coolingEntriesFor renders meta.engines_cooldown entries for the given
// engines (registration order) that are currently cooling.
func (s *Server) coolingEntriesFor(engines []SearchEngine) []EngineCooldownEntry {
	entries := make([]EngineCooldownEntry, 0, len(engines))
	for _, engine := range engines {
		st := s.resilient.EngineCooldownStatus(engine.Name())
		if st.InCooldown {
			entries = append(entries, cooldownEntryFromStatus(engine.Name(), st))
		}
	}
	return entries
}

// enginesCooldownMeta renders meta.engines_cooldown for ALL registered engines
// currently cooling (nil when none — omitted from JSON).
func (s *Server) enginesCooldownMeta() []EngineCooldownEntry {
	return s.coolingEntriesFor(s.searchEngines)
}

// SearchViaEngine runs one search through the existing resilient pipeline for
// the named engine with the cooldown gate applied (契约 A seam for the MCP
// web_search tool). It returns LLM-ready result maps
// ({"rank","title","url","snippet"}) or an *APIError (400 unknown engine /
// empty query, 503 engine_cooldown with Retry-After, or the mapped search
// error).
func (s *Server) SearchViaEngine(ctx context.Context, engineName, text string, limit int) ([]map[string]any, error) {
	engine := s.engineByName(engineName)
	if engine == nil {
		return nil, errInvalidParam(fmt.Sprintf(
			"unknown engine %q: available engines are %s",
			strings.TrimSpace(engineName), strings.Join(IndexCheckEngineNames(s.searchEngines), ", ")))
	}
	if strings.TrimSpace(text) == "" {
		return nil, errEmptyQuery()
	}
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	requestCtx := withRequestUsage(ctx, engine.Name())
	gate, gateErr := s.resilient.GateEngine(requestCtx, engine.Name())
	if gateErr != nil {
		return nil, gateErr
	}
	if gate.Blocked() {
		return nil, s.errEngineCooldownForServer(engine.Name(), gate)
	}

	q := Query{Text: strings.TrimSpace(text), Limit: limit, ResolveConfig: &s.opts.ResolveConfig}
	results, _, _, searchErr := s.resilient.SearchPrimary(requestCtx, engine, q)
	if gate.IsProbe() {
		gate.FinishProbe(searchErr)
	}
	if searchErr != nil {
		WithRequest(requestCtx).WithError(searchErr).Error("Search failed")
		return nil, searchAPIError(searchErr, engine.Name(), q, ProxyExecutionMeta{})
	}

	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"rank":    r.Rank,
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Description,
		})
	}
	return out, nil
}

// EnginesStatusWithCooldown renders GET /api/engines (契约 A 管理面): one entry
// per registered engine with the cooldown extension fields
// {state:"ok|cooldown|circuit_open", until, reason, consecutive_blocks,
// last_error_at} on top of the shared engine/endpoint/enabled/breaker block.
// until/last_error_at are RFC3339 UTC strings (null when absent); reason is
// "COOLDOWN"|"FORCED" ("" when ok).
func (s *Server) EnginesStatusWithCooldown() any {
	breakers := make(map[string]map[string]interface{}, len(s.searchEngines))
	for _, stat := range s.resilient.GetCircuitBreakerStats() {
		if name, _ := stat["engine"].(string); name != "" {
			breakers[name] = stat
		}
	}

	snapshot := make([]map[string]interface{}, 0, len(s.searchEngines))
	for _, engine := range s.searchEngines {
		breaker := map[string]interface{}{
			"engine":        engine.Name(),
			"state":         "closed",
			"failure_count": 0,
		}
		if stat, ok := breakers[engine.Name()]; ok {
			breaker = stat
		}
		breakerState, _ := breaker["state"].(string)

		cooldown := s.resilient.EngineCooldownStatus(engine.Name())
		state := "ok"
		switch {
		case cooldown.InCooldown:
			state = "cooldown"
		case breakerState == "open":
			state = "circuit_open"
		}

		until := interface{}(nil)
		reason := ""
		if cooldown.InCooldown {
			until = cooldown.Until.UTC().Format(time.RFC3339)
			reason = cooldown.Reason
		}
		lastErrorAt := interface{}(nil)
		if !cooldown.LastErrorAt.IsZero() {
			lastErrorAt = cooldown.LastErrorAt.UTC().Format(time.RFC3339)
		}

		snapshot = append(snapshot, map[string]interface{}{
			"engine":             engine.Name(),
			"endpoint":           fmt.Sprintf("%s/%s/search", BasePath, engineEndpointName(engine.Name())),
			"enabled":            engine.IsInitialized(),
			"breaker":            breaker,
			"state":              state,
			"until":              until,
			"reason":             reason,
			"consecutive_blocks": cooldown.ConsecutiveBlocks,
			"last_error_at":      lastErrorAt,
		})
	}
	return snapshot
}

// ResetCooldown force-recovers the named engine's cooldown (契约 A 管理面).
func (s *Server) ResetCooldown(name string) error {
	engine := s.engineByName(name)
	if engine == nil {
		return errInvalidParam(fmt.Sprintf("unknown engine %q: available engines are %s",
			strings.TrimSpace(name), strings.Join(IndexCheckEngineNames(s.searchEngines), ", ")))
	}
	s.resilient.ResetCooldown(engine.Name())
	logrus.WithField("engine", engine.Name()).Info("Engine cooldown reset by admin")
	return nil
}

// ForceCooldown puts the named engine into a maintenance-window cooldown for
// the given minutes (契约 A 管理面). minutes must be positive.
func (s *Server) ForceCooldown(name string, minutes int) error {
	engine := s.engineByName(name)
	if engine == nil {
		return errInvalidParam(fmt.Sprintf("unknown engine %q: available engines are %s",
			strings.TrimSpace(name), strings.Join(IndexCheckEngineNames(s.searchEngines), ", ")))
	}
	if minutes <= 0 {
		return errInvalidParam(fmt.Sprintf("minutes must be > 0, got %d", minutes))
	}
	s.resilient.ForceCooldown(engine.Name(), minutes)
	logrus.WithFields(logrus.Fields{"engine": engine.Name(), "minutes": minutes}).Info("Engine forced into cooldown by admin")
	return nil
}

// SetDraining controls readiness state exposed by /ready.
func (s *Server) SetDraining(draining bool) {
	s.draining.Store(draining)
}

// FiberApp exposes the underlying fiber app so the serve command can mount the
// platform API groups (/api/*, /v1/*) and the admin panel static files under
// the same BasePath as the routes registered here. API groups are registered
// before the static wildcard so API routes take priority.
func (s *Server) FiberApp() *fiber.App {
	return s.app
}

// EngineStatusSnapshot is the reusable engine list + circuit-breaker state
// source shared by GET /web-tool/v1/engines (public) and GET /web-tool/api/engines
// (admin). It returns one entry per registered engine, in registration order:
//
//	{"engine": "baidu", "endpoint": "/web-tool/baidu/search", "enabled": true,
//	 "breaker": {"engine": "baidu", "state": "closed", "failure_count": 0, ...}}
//
// Engines without a created breaker yet report a closed breaker.
func (s *Server) EngineStatusSnapshot() any {
	breakers := make(map[string]map[string]interface{}, len(s.searchEngines))
	for _, stat := range s.resilient.GetCircuitBreakerStats() {
		if name, _ := stat["engine"].(string); name != "" {
			breakers[name] = stat
		}
	}

	snapshot := make([]map[string]interface{}, 0, len(s.searchEngines))
	for _, engine := range s.searchEngines {
		breaker := map[string]interface{}{
			"engine":        engine.Name(),
			"state":         "closed",
			"failure_count": 0,
		}
		if stat, ok := breakers[engine.Name()]; ok {
			breaker = stat
		}
		snapshot = append(snapshot, map[string]interface{}{
			"engine":   engine.Name(),
			"endpoint": fmt.Sprintf("%s/%s/search", BasePath, engineEndpointName(engine.Name())),
			"enabled":  engine.IsInitialized(),
			"breaker":  breaker,
		})
	}
	return snapshot
}

const defaultShutdownTimeout = 30 * time.Second

// Listen starts the Fiber HTTP server on the configured address.
func (s *Server) Listen() error {
	s.SetDraining(false)
	return s.app.Listen(s.addr)
}

// Shutdown gracefully stops the Fiber HTTP server.
func (s *Server) Shutdown() error {
	return s.ShutdownWithTimeout(defaultShutdownTimeout)
}

// ShutdownWithTimeout drains the server before force-closing active
// connections when timeout is exceeded.
func (s *Server) ShutdownWithTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	// Stop the job runner first: it owns engine work that must not outlive the
	// process (its state is on disk, so a restart resumes it).
	s.jobs.Stop()
	s.SetDraining(true)
	return s.app.ShutdownWithTimeout(timeout)
}
