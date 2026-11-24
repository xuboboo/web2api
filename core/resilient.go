package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// ResilientSearcher wraps engines with retry and circuit breaker protection.
type ResilientSearcher struct {
	engines   []SearchEngine
	cbManager *CircuitBreakerManager
	retryCfg  RetryConfig
	// indexRetryCfg bounds index-check retries (see ResilientConfig.IndexRetry).
	indexRetryCfg RetryConfig

	proxyRuntime  string
	proxyCfg      ProxyConfig
	proxyRegistry *ProxyRegistry
	proxyDefaults ProxyPolicy

	effectivePolicies map[string]ProxyPolicy

	// cdManager is the per-engine cooldown state machine (契约 A). Requests
	// gate through it at the orchestration layer; interception outcomes are
	// recorded here so every pipeline entry (search, index check, fallback)
	// feeds the same state machine.
	cdManager *engineCooldownManager
}

type ProxyExecutionMeta struct {
	Mode string `json:"mode"`
	Tag  string `json:"tag,omitempty"`
	Used string `json:"used"`
	// Attempts is how many proxies were tried; >1 means a challenged proxy was
	// rotated out in tag-pool mode.
	Attempts int `json:"attempts,omitempty"`
}

type ResilientConfig struct {
	Retry          RetryConfig
	CircuitBreaker CircuitBreakerConfig
	Proxy          ProxyConfig
	// Cooldown configures the per-engine IP cooldown state machine (契约 A);
	// zero values fall back to DefaultEngineCooldownConfig.
	Cooldown EngineCooldownConfig
	// IndexRetry bounds retries for index-check units. It defaults to a single
	// retry: a failed index lookup is usually "the engine blocked us", and a
	// 4-attempt cycle only feeds the challenge rate limiter.
	IndexRetry RetryConfig
}

type proxyLaneCookieDropper interface {
	DropProxyLaneCookies(context.Context, Query)
}

type proxyLaneStatser interface {
	ProxyLaneStats() LaneStats
}

type browserPoolStatser interface {
	BrowserPoolStats() BrowserPoolStats
}

func DefaultResilientConfig() ResilientConfig {
	return ResilientConfig{
		Retry:          DefaultRetryConfig(),
		CircuitBreaker: DefaultCircuitBreakerConfig(),
		Proxy:          DefaultProxyConfig(),
	}
}

func NewResilientSearcher(engines []SearchEngine, cfg ResilientConfig) *ResilientSearcher {
	proxyCfg, err := NormalizeProxyConfig(cfg.Proxy)
	if err != nil {
		logrus.WithError(err).Error("Invalid proxy config, using defaults")
		proxyCfg = DefaultProxyConfig()
		proxyCfg, _ = NormalizeProxyConfig(proxyCfg)
	}

	rs := &ResilientSearcher{
		engines:           engines,
		cbManager:         NewCircuitBreakerManager(cfg.CircuitBreaker),
		retryCfg:          cfg.Retry,
		indexRetryCfg:     normalizeIndexRetryConfig(cfg.IndexRetry, cfg.Retry),
		proxyRuntime:      proxyCfg.Runtime,
		proxyCfg:          proxyCfg,
		proxyRegistry:     proxyCfg.Registry,
		proxyDefaults:     ResolveEffectiveProxyPolicy(proxyCfg.Proxies.Global, ""),
		effectivePolicies: make(map[string]ProxyPolicy, len(engines)),
		cdManager:         newEngineCooldownManager(cfg.Cooldown),
	}

	for _, engine := range engines {
		engineName := normalizeEngineName(engine.Name())
		override := proxyCfg.EnginePolicies[engineName]
		effective := ResolveEffectiveProxyPolicy(proxyCfg.Proxies.Global, override)
		rs.effectivePolicies[engineName] = effective
	}

	return rs
}

// SearchPrimary keeps dedicated endpoints engine-pure (no fallback).
func (rs *ResilientSearcher) SearchPrimary(ctx context.Context, primaryEngine SearchEngine, q Query) ([]SearchResult, string, ProxyExecutionMeta, error) {
	results, proxyMeta, err := rs.searchWithProtection(ctx, primaryEngine, q)
	if err != nil {
		return nil, primaryEngine.Name(), proxyMeta, err
	}
	return results, primaryEngine.Name(), proxyMeta, nil
}

// SearchWithFallback retries primary and then tries other initialized engines.
func (rs *ResilientSearcher) SearchWithFallback(ctx context.Context, primaryEngine SearchEngine, q Query) ([]SearchResult, string, ProxyExecutionMeta, error) {
	return rs.searchWithFallback(ctx, primaryEngine, q)
}

func (rs *ResilientSearcher) searchWithFallback(ctx context.Context, primaryEngine SearchEngine, q Query) ([]SearchResult, string, ProxyExecutionMeta, error) {
	ctx = EnsureContext(ctx)

	results, proxyMeta, err := rs.searchWithProtection(ctx, primaryEngine, q)
	if err == nil {
		return results, primaryEngine.Name(), proxyMeta, nil
	}
	if ctx.Err() != nil {
		return nil, primaryEngine.Name(), proxyMeta, ctx.Err()
	}
	if errors.Is(err, ErrProxyUnavailable) {
		WithRequestEngine(ctx, primaryEngine.Name()).WithError(err).Warn("Proxy policy failed closed")
		return nil, primaryEngine.Name(), proxyMeta, err
	}

	WithRequestEngine(ctx, primaryEngine.Name()).
		WithError(err).
		Warn("Primary engine failed, trying fallbacks")
	for _, fallbackEngine := range rs.engines {
		if ctx.Err() != nil {
			return nil, primaryEngine.Name(), proxyMeta, ctx.Err()
		}
		if fallbackEngine.Name() == primaryEngine.Name() || !fallbackEngine.IsInitialized() {
			continue
		}

		results, fallbackMeta, fallbackErr := rs.searchWithProtection(ctx, fallbackEngine, q)
		if fallbackErr == nil {
			WithRequestEngine(ctx, fallbackEngine.Name()).
				WithField("results_count", len(results)).
				Infof("Fallback to %s succeeded with %d results", fallbackEngine.Name(), len(results))
			return results, fallbackEngine.Name(), fallbackMeta, nil
		}
		WithRequestEngine(ctx, fallbackEngine.Name()).WithError(fallbackErr).Debug("Fallback engine also failed")
	}

	return nil, primaryEngine.Name(), proxyMeta, ErrAllEnginesFailed
}

func (rs *ResilientSearcher) searchWithProtection(ctx context.Context, engine SearchEngine, q Query) ([]SearchResult, ProxyExecutionMeta, error) {
	return rs.searchWithProtectionCfg(ctx, engine, q, rs.retryCfg)
}

// normalizeIndexRetryConfig fills the index-check retry budget: one retry by
// default, inheriting the search backoff unless the caller configured one.
func normalizeIndexRetryConfig(index, fallback RetryConfig) RetryConfig {
	cfg := index
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = fallback.InitialBackoff
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = DefaultRetryConfig().InitialBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = fallback.MaxBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultRetryConfig().MaxBackoff
	}
	if cfg.BackoffFactor <= 0 {
		cfg.BackoffFactor = fallback.BackoffFactor
	}
	if cfg.BackoffFactor <= 0 {
		cfg.BackoffFactor = 2.0
	}
	return cfg
}

// searchWithProtectionCfg is searchWithProtection with an explicit retry budget.
func (rs *ResilientSearcher) searchWithProtectionCfg(ctx context.Context, engine SearchEngine, q Query, retryCfg RetryConfig) ([]SearchResult, ProxyExecutionMeta, error) {
	ctx = EnsureContext(ctx)

	if ctx.Err() != nil {
		return nil, ProxyExecutionMeta{}, ctx.Err()
	}
	cb := rs.cbManager.Get(engine.Name())
	engineCtx := WithEngine(ctx, engine.Name())
	if !cb.AllowRequest(engineCtx) {
		return nil, ProxyExecutionMeta{}, ErrCircuitOpen
	}

	policy := rs.effectivePolicyForQuery(engine.Name(), q)
	attemptMeta := rs.baseProxyMeta(policy)

	startedAt := time.Now()

	// lastProxyURL is the unmasked proxy of the last attempt, for rotation below.
	lastProxyURL := ""
	runOnce := func() RetryResult {
		return RetryableSearch(ctx, retryCfg, engine.Name(), func(callCtx context.Context) ([]SearchResult, error) {
			limiter := engine.GetRateLimiter()
			if limiter != nil {
				if err := limiter.Wait(callCtx); err != nil {
					return nil, normalizeLimiterWaitErr(callCtx, err)
				}
			}

			attemptQuery := q
			proxyURL := ""
			reportToRegistry := false
			attemptMeta = rs.baseProxyMeta(policy)

			switch policy.Mode {
			case ProxyModeOff:
				attemptQuery.ProxyURL = ""
				attemptMeta.Used = "direct"
			case ProxyModeRequestURL:
				proxyURL = q.ProxyURL
				attemptQuery.ProxyURL = proxyURL
				attemptMeta.Used = MaskProxyURL(proxyURL)
			case ProxyModeTagPool:
				proxyURL = rs.selectProxyForQuery(policy, q, engineCtx)
				if proxyURL == "" {
					return nil, fmt.Errorf("%w: no healthy proxy available for tag %q", ErrProxyUnavailable, policy.Tag)
				}
				attemptQuery.ProxyURL = proxyURL
				reportToRegistry = policy.Tag != ""
				attemptMeta.Used = MaskProxyURL(proxyURL)
			}
			lastProxyURL = proxyURL

			requestCtx := proxyRequestContext(callCtx, engine.Name(), attemptQuery)
			results, err := invokeEngine(requestCtx, engine, attemptQuery)

			if reportToRegistry {
				rs.reportProxyAttempt(engineCtx, proxyURL, err)
			}
			if err != nil && errors.Is(err, ErrCaptcha) && rs.proxyCfg.Proxies.Lanes.DropCookiesOnChallenge {
				// Recompute lane key only to gate the call: empty key means we have no
				// session to drop cookies for. The dropper recomputes the key itself
				// when it actually needs to mutate lane state.
				if !ProxyLaneKeyForTenant(engine.Name(), TenantFromContext(callCtx), attemptQuery, attemptQuery.ProxyURL).Empty() {
					if dropper, ok := engine.(proxyLaneCookieDropper); ok {
						dropper.DropProxyLaneCookies(callCtx, attemptQuery)
					}
				}
			}

			return results, err
		})
	}

	result := runOnce()
	attemptMeta.Attempts = 1

	// On a captcha/block/rate-limit (non-retryable inside RetryableSearch), if
	// the tag pool has another healthy proxy, deprioritize the burned one and
	// retry once with the next. Tag-pool only - direct/request-url/global can't
	// rotate.
	canRotateChallengedProxy := result.Err != nil &&
		policy.Mode == ProxyModeTagPool &&
		policy.Tag != "" &&
		rs.proxyRegistry != nil &&
		IsProxyChallengeError(result.Err) &&
		rs.proxyRegistry.HealthyCountForTag(policy.Tag) >= 2 &&
		ctx.Err() == nil
	if canRotateChallengedProxy {
		rs.proxyRegistry.ReportChallenged(engineCtx, lastProxyURL)
		WithRequestEngine(ctx, engine.Name()).WithError(result.Err).
			Debug("Challenged proxy rotated out, retrying once with next proxy")
		result = runOnce()
		attemptMeta.Attempts = 2
	} else if result.Err != nil && IsProxyChallengeError(result.Err) && strings.TrimSpace(lastProxyURL) != "" {
		WithRequestEngine(ctx, engine.Name()).WithError(result.Err).
			WithField("proxy", MaskProxyURL(lastProxyURL)).
			Info("single proxy challenged and cannot rotate; configure proxies.entries with 2+ IPs")
	}

	if result.Err != nil {
		if shouldRecordCircuitFailure(result.Err) {
			cb.RecordFailure(engineCtx)
		}
		// 契约 A trigger point: after retries/proxy rotation the engine still
		// answered blocked(403)/429/captcha → one cooldown block per request.
		// Cooldown and circuit breaker count independently (never cross-feed).
		if rs.cdManager != nil && IsProxyChallengeError(result.Err) {
			rs.cdManager.RecordBlock(engine.Name(), result.Err)
		}
		return nil, attemptMeta, result.Err
	}

	cb.RecordSuccessDuration(engineCtx, time.Since(startedAt))
	return result.Results, attemptMeta, nil
}

// IsProxyChallengeError reports whether err is a captcha/block/rate-limit — an
// IP-reputation problem another proxy might dodge.
func IsProxyChallengeError(err error) bool {
	return errors.Is(err, ErrCaptcha) ||
		errors.Is(err, ErrBlocked) ||
		errors.Is(err, ErrRateLimited)
}

func shouldRecordCircuitFailure(err error) bool {
	return err != nil &&
		!IsContextDone(err) &&
		!errors.Is(err, ErrProxyUnavailable) &&
		!errors.Is(err, ErrCircuitOpen)
}

// normalizeLimiterWaitErr maps rate.Limiter.Wait failures back to the caller's
// context error. Wait reports a bare "would exceed context deadline" error that
// errors.Is cannot trace to the context, so without this an impatient client
// would be counted as an engine failure by the circuit breaker.
func normalizeLimiterWaitErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return fmt.Errorf("%w: %v", context.DeadlineExceeded, err)
	}
	return err
}

// IsRateLimiterQueueErr reports whether err is the normalized limiter-wait
// failure: the unit's projected token wait exceeded the remaining request
// budget, so the unit never reached the engine. The bare ctx-error shape (the
// context already done when Wait returned) is intentionally excluded — that
// one IS a genuine request timeout. Matching by message is the only option:
// rate.Wait's error carries no sentinel.
func IsRateLimiterQueueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "rate: Wait(n=")
}

// invokeEngine is the single panic-recovery point for every engine call made
// through the resilient pipeline (browser, raw, and any future engine method).
// A rod/CDP panic surfaces as ErrEngineInternal instead of killing the process.
// S4-GAP 指标: every real invocation feeds engine_calls_total{engine,ok}.
func invokeEngine(ctx context.Context, engine SearchEngine, q Query) (results []SearchResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			results = nil
			err = RecoverEnginePanicWithContext(ctx, engine.Name(), recovered, nil)
			Metrics().IncEngineCall(engine.Name(), false)
		}
	}()

	results, err = engine.Search(ctx, q)
	Metrics().IncEngineCall(engine.Name(), err == nil)
	return results, err
}

func (rs *ResilientSearcher) GetCircuitBreakerStats() []map[string]interface{} {
	return rs.cbManager.AllStats()
}

// GateEngine applies the cooldown gate for one engine at the orchestration
// layer. Callers must treat a Blocked gate as a fast-fail (engine untouched),
// and a Probe gate as THE single cooldown-expiry probe: run the engine once
// and finish it with EngineGate.FinishProbe(err).
func (rs *ResilientSearcher) GateEngine(ctx context.Context, engine string) (EngineGate, error) {
	if rs.cdManager == nil {
		return EngineGate{decision: gateProceed}, nil
	}
	return rs.cdManager.Gate(ctx, engine, func() bool {
		return !rs.cbManager.Get(engine).AllowRequest(ctx)
	})
}

// EngineCooldownStatus exposes one engine's cooldown state to admin surfaces.
func (rs *ResilientSearcher) EngineCooldownStatus(engine string) EngineCooldownStatus {
	if rs.cdManager == nil {
		return EngineCooldownStatus{}
	}
	return rs.cdManager.Status(engine)
}

// ResetCooldown force-recovers an engine's cooldown (强制恢复).
func (rs *ResilientSearcher) ResetCooldown(engine string) {
	if rs.cdManager != nil {
		rs.cdManager.ResetCooldown(engine)
	}
}

// ForceCooldown puts an engine into a maintenance-window cooldown (强制冷却).
func (rs *ResilientSearcher) ForceCooldown(engine string, minutes int) {
	if rs.cdManager != nil {
		rs.cdManager.ForceCooldown(engine, minutes)
	}
}

func (rs *ResilientSearcher) GetProxyStats() ProxyStats {
	stats := ProxyStats{
		ConfiguredCount:        0,
		HealthyCount:           0,
		UnhealthyCount:         0,
		RequestProxyURLEnabled: rs.proxyCfg.Proxies.AllowRequestProxyURL,
		Lanes:                  rs.proxyLaneStats(),
		Tags:                   map[string]ProxyTagSummary{},
		Entries:                []ProxyStatsEntry{},
	}

	if rs.proxyRegistry != nil {
		stats = rs.proxyRegistry.BuildStats()
	}
	stats.RequestProxyURLEnabled = rs.proxyCfg.Proxies.AllowRequestProxyURL
	stats.Lanes = rs.proxyLaneStats()
	stats.BrowserProcesses = rs.browserPoolStats()

	engines := map[string]ProxyEngineStats{}
	for _, engine := range rs.engines {
		engineName := normalizeEngineName(engine.Name())
		policy := rs.effectivePolicyForEngine(engineName)
		engineStats := ProxyEngineStats{}

		switch policy.Mode {
		case ProxyModeOff:
			engineStats.SelectedProxy = "direct"
		case ProxyModeTagPool:
			engineStats.Tag = policy.Tag
			if global := strings.TrimSpace(rs.proxyCfg.Proxies.Global); global != "" {
				engineStats.SelectedProxy = MaskProxyURL(global)
			} else {
				engineStats.SelectedProxy = "pooled"
			}
		}

		engines[engineName] = engineStats
	}
	if len(engines) > 0 {
		stats.Engines = engines
	}

	return stats
}

func (rs *ResilientSearcher) proxyLaneStats() LaneStats {
	var out LaneStats
	for _, engine := range rs.engines {
		statser, ok := engine.(proxyLaneStatser)
		if !ok {
			continue
		}
		stats := statser.ProxyLaneStats()
		out.Active += stats.Active
		out.EvictedLRU += stats.EvictedLRU
		out.CookiesDropped += stats.CookiesDropped
	}
	return out
}

// browserPoolStats reports the first non-zero browser pool stats found across
// engines. The pool is shared across engines, so reading from any engine that
// exposes it is sufficient; other engines' implementations return zero values.
func (rs *ResilientSearcher) browserPoolStats() BrowserPoolStats {
	for _, engine := range rs.engines {
		statser, ok := engine.(browserPoolStatser)
		if !ok {
			continue
		}
		stats := statser.BrowserPoolStats()
		if stats.Max > 0 || stats.Active > 0 || stats.EvictedLRU > 0 || stats.EvictedIdle > 0 {
			return stats
		}
	}
	return BrowserPoolStats{}
}

func (rs *ResilientSearcher) baseProxyMeta(policy ProxyPolicy) ProxyExecutionMeta {
	meta := ProxyExecutionMeta{Mode: policy.Mode}
	if policy.Mode == ProxyModeTagPool {
		meta.Tag = policy.Tag
		return meta
	}
	meta.Used = "direct"
	return meta
}

func (rs *ResilientSearcher) effectivePolicyForEngine(engineName string) ProxyPolicy {
	engineName = normalizeEngineName(engineName)
	if policy, ok := rs.effectivePolicies[engineName]; ok {
		return policy
	}
	return rs.proxyDefaults
}

func (rs *ResilientSearcher) effectivePolicyForQuery(engineName string, q Query) ProxyPolicy {
	switch q.ProxyOverride {
	case ProxyOverrideDirect:
		return ProxyPolicy{Mode: ProxyModeOff}
	}

	if strings.TrimSpace(q.ProxyURL) != "" && rs.proxyCfg.Proxies.AllowRequestProxyURL {
		return ProxyPolicy{Mode: ProxyModeRequestURL}
	}

	switch q.ProxyOverride {
	case "":
		return rs.effectivePolicyForEngine(engineName)
	default:
		return ProxyPolicy{Mode: ProxyModeTagPool, Tag: q.ProxyOverride}
	}
}

func (rs *ResilientSearcher) selectProxyForTag(ctx context.Context, tag string) string {
	if rs.proxyRegistry == nil {
		return ""
	}
	return rs.proxyRegistry.NextByTagWithContext(ctx, tag)
}

func (rs *ResilientSearcher) reportProxyAttempt(ctx context.Context, proxyURL string, err error) {
	if rs.proxyRegistry == nil || proxyURL == "" {
		return
	}

	if err != nil {
		// Only degrade proxy health for network-level errors. Captcha pages,
		// parser drift, and engine errors do not indicate a faulty proxy.
		if IsProxyNetworkError(err) {
			rs.proxyRegistry.ReportFailure(ctx, proxyURL)
		}
		return
	}

	rs.proxyRegistry.ReportSuccess(ctx, proxyURL)
}

func (rs *ResilientSearcher) selectProxyForQuery(policy ProxyPolicy, q Query, ctx context.Context) string {
	if policy.Mode != ProxyModeTagPool {
		return ""
	}
	if q.ProxyOverride == "" {
		if global := strings.TrimSpace(rs.proxyCfg.Proxies.Global); global != "" {
			return global
		}
	}
	return rs.selectProxyForTag(ctx, policy.Tag)
}

func proxyRequestContext(ctx context.Context, engineName string, q Query) context.Context {
	ctx = WithRequestProxyURL(ctx, q.ProxyURL)
	// Attach the lane key for direct traffic too (session "direct"): without it
	// every request opened a cold browser context, which is exactly the pattern
	// Baidu challenges after ~12 lookups. A sticky lane keeps cookies across
	// requests and lets cookie_rotate_every / drop_cookies_on_challenge work.
	if laneKey := ProxyLaneKeyForTenant(engineName, TenantFromContext(ctx), q, q.ProxyURL); !laneKey.Empty() {
		ctx = WithProxyLaneKey(ctx, laneKey)
		if q.ProxyURL != "" && q.ProxyCountry != "" {
			ctx = WithProfileRegion(ctx, q.ProxyCountry)
		}
	}
	return ctx
}

var ErrAllEnginesFailed = fmt.Errorf("all search engines failed")
