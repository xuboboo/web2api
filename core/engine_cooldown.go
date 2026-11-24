package core

// Engine cooldown state machine (契约 A 核心: IP 冷却封禁).
//
// One independent state per engine. Trigger: the engine's real request came
// back intercepted (blocked HTTP 403 / HTTP 429 / captcha) — classified with
// IsProxyChallengeError AFTER the resilient pipeline exhausted its retries and
// proxy rotation. Tier 1/2/3+ consecutive blocks cool the engine for
// steps_minutes[0]/[1]/[2+] (default 10/30/120 minutes). A clean streak of
// reset_after_minutes (default 240) zeroes the consecutive counter.
//
// While an engine is cooling, ALL of its search/check requests fail fast at
// the orchestration layer (search + index-check entry points) without touching
// the engine — CaptchaGate trips and circuit-breaker counters are therefore
// frozen naturally. When the cooldown expires exactly ONE probe request is
// let through:
//
//   - probe succeeds            → engine recovered (cooldown cleared)
//   - probe intercepted         → consecutive counter +1, next tier
//   - probe fails otherwise     → probe incomplete: postpone by the current
//     tier duration, no tier advance, no counter reset
//   - circuit breaker still open at expiry → no probe is sent, cooldown
//     expiry is postponed by the current tier duration
//
// Cooldown and circuit breaker count independently and never trigger each
// other: blocks recorded here never feed the breaker and breaker state never
// creates cooldown time.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// CooldownReasonNatural is the reason for engine-triggered cooldowns (blocked /
// rate-limited / captcha). The fixed reason carried by the contract's 503 body.
const CooldownReasonNatural = "COOLDOWN"

// CooldownReasonForced is the reason for maintenance-window cooldowns created
// through POST /api/engines/{name}/cooldown/force.
const CooldownReasonForced = "FORCED"

// ErrEngineCooldown is the sentinel wrapped by every cooldown fast-fail error.
// Use IsEngineCooldownError (or errors.Is) to detect it.
var ErrEngineCooldown = errors.New("engine cooldown")

// errProbeIncomplete settles a granted cooldown probe whose request never
// reached the engine (validation failure, client disconnect): the finish path
// treats it as a non-interception outcome and postpones the cooldown.
var errProbeIncomplete = errors.New("cooldown probe did not reach the engine")

// IsEngineCooldownError reports whether err is (or wraps) an engine-cooldown
// fast-fail. This is the seam the MCP layer maps JSON-RPC tool errors with.
func IsEngineCooldownError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrEngineCooldown) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode == "engine_cooldown"
	}
	return false
}

// EngineCooldownEntry is one meta.engines_cooldown item:
// {"engine":"baidu","until":"2026-01-01T00:10:00Z","reason":"COOLDOWN"}.
type EngineCooldownEntry struct {
	Engine string `json:"engine"`
	// Until is the RFC3339 UTC instant the cooldown lifts.
	Until string `json:"until"`
	// Reason is CooldownReasonNatural ("COOLDOWN") or CooldownReasonForced ("FORCED").
	Reason string `json:"reason"`
}

// EngineCooldownConfig is the `stability.engine_cooldown` config node.
type EngineCooldownConfig struct {
	// StepsMinutes holds the cooldown duration in minutes for the 1st, 2nd,
	// 3rd (and beyond — the last value repeats) consecutive block.
	StepsMinutes []int `mapstructure:"steps_minutes"`
	// ResetAfterMinutes is the clean streak (no interception) after which the
	// consecutive-block counter zeroes.
	ResetAfterMinutes int `mapstructure:"reset_after_minutes"`
	// PerEngine overrides the tiers for named engines. Baidu recovers from a
	// challenge after ~2 minutes of rest (measured), so the global 10/30/120
	// default parks it far longer than needed; per-engine tiers keep the
	// punitive default for engines that really need it.
	PerEngine map[string]EngineCooldownConfig `mapstructure:"per_engine"`
}

// StabilityConfig is the `stability:` config node (mounted into cmd.Config by
// the integrator, following the core.ProxiesConfig pattern).
type StabilityConfig struct {
	EngineCooldown EngineCooldownConfig `mapstructure:"engine_cooldown"`
	IndexCheck     IndexCheckConfig     `mapstructure:"index_check"`
}

// IndexCheckConfig is the `stability.index_check` node.
type IndexCheckConfig struct {
	// MaxRetries is the retry budget for one index-check unit (default 1).
	MaxRetries int `mapstructure:"max_retries"`
}

// DefaultEngineCooldownConfig returns the contract defaults: 10/30/120 minute
// tiers with a 240 minute clean-streak counter reset.
func DefaultEngineCooldownConfig() EngineCooldownConfig {
	return EngineCooldownConfig{
		StepsMinutes:      []int{10, 30, 120},
		ResetAfterMinutes: 240,
	}
}

// DefaultStabilityConfig returns the default `stability:` node.
func DefaultStabilityConfig() StabilityConfig {
	return StabilityConfig{
		EngineCooldown: DefaultEngineCooldownConfig(),
		IndexCheck:     IndexCheckConfig{MaxRetries: 1},
	}
}

// normalized fills empty/invalid values with the contract defaults so a
// partially written config node still yields a working state machine.
func (c EngineCooldownConfig) normalized() EngineCooldownConfig {
	steps := make([]int, 0, len(c.StepsMinutes))
	for _, m := range c.StepsMinutes {
		if m > 0 {
			steps = append(steps, m)
		}
	}
	if len(steps) == 0 {
		steps = DefaultEngineCooldownConfig().StepsMinutes
	}
	reset := c.ResetAfterMinutes
	if reset <= 0 {
		reset = DefaultEngineCooldownConfig().ResetAfterMinutes
	}
	normalized := EngineCooldownConfig{StepsMinutes: steps, ResetAfterMinutes: reset}
	if len(c.PerEngine) > 0 {
		normalized.PerEngine = make(map[string]EngineCooldownConfig, len(c.PerEngine))
		for engine, override := range c.PerEngine {
			normalized.PerEngine[engine] = override.withoutPerEngine().normalized()
		}
	}
	return normalized
}

// withoutPerEngine copies the config without nested overrides (one level only).
func (c EngineCooldownConfig) withoutPerEngine() EngineCooldownConfig {
	return EngineCooldownConfig{StepsMinutes: c.StepsMinutes, ResetAfterMinutes: c.ResetAfterMinutes}
}

// engineCooldownState is the per-engine state machine state.
type engineCooldownState struct {
	// until is the instant the cooldown lifts; zero means not cooling.
	until time.Time
	// reason is CooldownReasonNatural or CooldownReasonForced.
	reason string
	// consecutiveBlocks counts consecutive interception events (never reset by
	// ForceCooldown; lazily zeroed once the clean streak reaches
	// reset_after_minutes).
	consecutiveBlocks int
	// lastBlockAt is when the last interception was recorded; drives the
	// reset_after_minutes counter zeroing.
	lastBlockAt time.Time
	// lastErrorAt is when the engine last returned an interception (admin UI).
	lastErrorAt time.Time
	// stepDuration is the duration of the tier currently cooling (used to
	// postpone expiry for incomplete probes / open circuits).
	stepDuration time.Duration
	// probing reports that the single expiry probe is in flight.
	probing bool
	// probeDone is closed when the in-flight probe completes; waiters re-gate.
	probeDone chan struct{}
	// generation bumps on ResetCooldown/ForceCooldown so stale in-flight
	// probes cannot clobber operator actions.
	generation uint64
}

// EngineCooldownStatus is the admin-facing state of one engine's cooldown.
type EngineCooldownStatus struct {
	InCooldown        bool
	Until             time.Time
	Reason            string
	ConsecutiveBlocks int
	LastErrorAt       time.Time
}

// engineCooldownManager owns one engineCooldownState per engine.
type engineCooldownManager struct {
	mu     sync.Mutex
	cfg    EngineCooldownConfig
	states map[string]*engineCooldownState
	// nowFn is the wall-clock source; nil means time.Now. Tests swap in a
	// controllable clock — injection point only, never set in production.
	nowFn func() time.Time
}

func (m *engineCooldownManager) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func newEngineCooldownManager(cfg EngineCooldownConfig) *engineCooldownManager {
	return &engineCooldownManager{
		cfg:    cfg.normalized(),
		states: map[string]*engineCooldownState{},
	}
}

func (m *engineCooldownManager) state(engine string) *engineCooldownState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.states[engine]
	if !ok {
		st = &engineCooldownState{}
		m.states[engine] = st
	}
	return st
}

// cfgFor returns the effective cooldown config for engine (per-engine override
// when configured, otherwise the global tiers).
func (m *engineCooldownManager) cfgFor(engine string) EngineCooldownConfig {
	if override, ok := m.cfg.PerEngine[engine]; ok {
		return override.withoutPerEngine().normalized()
	}
	return m.cfg.withoutPerEngine().normalized()
}

// tier returns the cooldown duration for the n-th (1-based) consecutive block.
func (m *engineCooldownManager) tier(engine string, consecutive int) time.Duration {
	cfg := m.cfgFor(engine)
	if consecutive < 1 {
		consecutive = 1
	}
	idx := consecutive - 1
	if idx >= len(cfg.StepsMinutes) {
		idx = len(cfg.StepsMinutes) - 1
	}
	return time.Duration(cfg.StepsMinutes[idx]) * time.Minute
}

func (m *engineCooldownManager) resetAfter(engine string) time.Duration {
	return time.Duration(m.cfgFor(engine).ResetAfterMinutes) * time.Minute
}

// normalizeLocked zeroes the consecutive counter once the clean streak (time
// since the last interception) reached reset_after_minutes. Callers must hold
// the manager lock.
func (st *engineCooldownState) normalizeLocked(now time.Time, resetAfter time.Duration) {
	if st.consecutiveBlocks > 0 && !st.lastBlockAt.IsZero() && now.Sub(st.lastBlockAt) >= resetAfter {
		st.consecutiveBlocks = 0
	}
}

// recordBlockLocked registers one interception: counter +1, cooldown for the
// matching tier. Callers must hold the manager lock.
func (st *engineCooldownState) recordBlockLocked(now time.Time, tier time.Duration) {
	st.consecutiveBlocks++
	st.until = now.Add(tier)
	st.reason = CooldownReasonNatural
	st.stepDuration = tier
	st.lastBlockAt = now
	st.lastErrorAt = now
}

// RecordBlock registers one interception event for engine (cause is the
// engine's challenge error; used for logs and last_error_at).
func (m *engineCooldownManager) RecordBlock(engine string, cause error) {
	st := m.state(engine)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	st.normalizeLocked(now, m.resetAfter(engine))
	st.recordBlockLocked(now, m.tier(engine, st.consecutiveBlocks+1))
	if cause != nil {
		st.lastErrorAt = now
	}
}

// ResetCooldown force-recovers engine: cooldown cleared and the consecutive
// counter zeroed (强制恢复). In-flight probes from before the reset no longer
// mutate the state.
func (m *engineCooldownManager) ResetCooldown(engine string) {
	st := m.state(engine)
	m.mu.Lock()
	defer m.mu.Unlock()
	if st.probing {
		st.generation++
		close(st.probeDone)
		st.probing = false
		st.probeDone = nil
	}
	st.until = time.Time{}
	st.reason = ""
	st.consecutiveBlocks = 0
	st.lastBlockAt = time.Time{}
	st.stepDuration = 0
}

// ForceCooldown puts engine into a maintenance-window cooldown for minutes
// (强制冷却). It does not touch the consecutive counter; an in-flight probe is
// invalidated so its completion cannot lift the forced cooldown.
func (m *engineCooldownManager) ForceCooldown(engine string, minutes int) {
	st := m.state(engine)
	m.mu.Lock()
	defer m.mu.Unlock()
	if st.probing {
		st.generation++
		close(st.probeDone)
		st.probing = false
		st.probeDone = nil
	}
	now := m.now()
	if minutes <= 0 {
		minutes = int((m.tier(engine, 1)).Minutes())
		if minutes < 1 {
			minutes = 1
		}
	}
	duration := time.Duration(minutes) * time.Minute
	st.until = now.Add(duration)
	st.reason = CooldownReasonForced
	st.stepDuration = duration
	st.generation++
}

// Status returns the cooldown state of engine.
func (m *engineCooldownManager) Status(engine string) EngineCooldownStatus {
	st := m.state(engine)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	st.normalizeLocked(now, m.resetAfter(engine))
	inCooldown := !st.until.IsZero() && now.Before(st.until)
	return EngineCooldownStatus{
		InCooldown:        inCooldown,
		Until:             st.until,
		Reason:            st.reason,
		ConsecutiveBlocks: st.consecutiveBlocks,
		LastErrorAt:       st.lastErrorAt,
	}
}

// CooldownEngineNames returns the engines currently cooling, sorted by name.
func (m *engineCooldownManager) CooldownEngineNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	names := make([]string, 0, len(m.states))
	for engine, st := range m.states {
		st.normalizeLocked(now, m.resetAfter(engine))
		if !st.until.IsZero() && now.Before(st.until) {
			names = append(names, engine)
		}
	}
	sort.Strings(names)
	return names
}

// EngineGate is the cooldown gate decision handed to orchestration callers.
type EngineGate struct {
	decision engineGateDecision
	// state snapshot for blocked decisions (manager lock already released).
	until        time.Time
	reason       string
	retryAfter   time.Duration
	probe        *engineProbe
	cooldownMsec *engineCooldownState
	mgr          *engineCooldownManager
}

type engineGateDecision int

const (
	gateProceed engineGateDecision = iota
	gateProbe
	gateBlocked
)

// engineProbe is the grant to run the single expiry probe. The caller MUST
// call FinishProbe exactly once with the search outcome.
type engineProbe struct {
	mgr        *engineCooldownManager
	st         *engineCooldownState
	engine     string
	generation uint64
	finished   bool
}

// Blocked reports whether the request must fail fast (engine cooling, or the
// expiry probe was postponed because the circuit breaker is still open).
func (g EngineGate) Blocked() bool { return g.decision == gateBlocked }

// IsProbe reports whether this request IS the single cooldown-expiry probe;
// the caller must invoke FinishProbe with the engine outcome.
func (g EngineGate) IsProbe() bool { return g.decision == gateProbe }

// Until returns the cooldown lift instant (zero when not blocked).
func (g EngineGate) Until() time.Time { return g.until }

// Reason returns CooldownReasonNatural or CooldownReasonForced when blocked.
func (g EngineGate) Reason() string { return g.reason }

// RetryAfterSeconds returns the suggested Retry-After header value in seconds
// (>= 1 while blocked, 0 otherwise).
func (g EngineGate) RetryAfterSeconds() int {
	if g.decision != gateBlocked || g.retryAfter <= 0 {
		return 0
	}
	secs := int((g.retryAfter + time.Second - time.Nanosecond) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return secs
}

// FinishProbe completes the cooldown-expiry probe with the engine outcome:
// success recovers the engine, interception errors have already been recorded
// by the resilient layer (RecordBlock) — FinishProbe only guarantees a tier is
// set — and any other error postpones the cooldown by the current tier
// duration without advancing the tier or resetting the counter.
func (g EngineGate) FinishProbe(err error) {
	if g.decision != gateProbe || g.probe == nil {
		return
	}
	g.probe.finish(err)
}

func (p *engineProbe) finish(err error) {
	mgr, st := p.mgr, p.st
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if p.finished || p.generation != st.generation {
		// Already finished, or a ResetCooldown/ForceCooldown invalidated this
		// probe: only release the probe slot.
		p.releaseLocked()
		return
	}
	p.finished = true
	p.releaseLocked()

	now := mgr.now()
	st.normalizeLocked(now, mgr.resetAfter(p.engine))
	switch {
	case err == nil:
		// Probe succeeded: engine recovered.
		st.until = time.Time{}
		st.reason = ""
	case IsProxyChallengeError(err):
		// The resilient pipeline already recorded the block (RecordBlock runs
		// inside searchWithProtection). Guarantee a tier is cooling in case the
		// block was recorded by a path that bypassed it.
		if st.until.IsZero() || !now.Before(st.until) {
			st.recordBlockLocked(now, mgr.tier(p.engine, st.consecutiveBlocks+1))
		}
	default:
		// Probe incomplete (timeout / network error): postpone by the current
		// tier duration — no tier advance, no counter reset.
		duration := st.stepDuration
		if duration <= 0 {
			duration = mgr.tier(p.engine, 1)
		}
		st.until = now.Add(duration)
		if st.reason == "" {
			st.reason = CooldownReasonNatural
		}
	}
}

// releaseLocked marks the probe slot free and wakes waiters. Caller holds the
// manager lock.
func (p *engineProbe) releaseLocked() {
	st := p.st
	if st.probing && st.probeDone != nil {
		close(st.probeDone)
	}
	st.probing = false
	st.probeDone = nil
}

// probeWaitCap bounds how long a request waits for an in-flight probe before
// re-gating; probes normally finish within the engine request deadline.
const probeWaitCap = 2 * time.Minute

// Gate decides whether a request for engine may proceed. circuitBlocked
// reports "the engine's circuit breaker refuses requests right now" and is
// only consulted at probe-grant time (pass nil when there is no breaker).
// A blocked decision never touched the engine. The returned error is only a
// ctx cancellation.
func (m *engineCooldownManager) Gate(ctx context.Context, engine string, circuitBlocked func() bool) (EngineGate, error) {
	for {
		st := m.state(engine)
		m.mu.Lock()
		now := m.now()
		st.normalizeLocked(now, m.resetAfter(engine))

		if !st.until.IsZero() && now.Before(st.until) {
			gate := EngineGate{
				decision:     gateBlocked,
				until:        st.until,
				reason:       st.reason,
				retryAfter:   st.until.Sub(now),
				cooldownMsec: st,
				mgr:          m,
			}
			m.mu.Unlock()
			return gate, nil
		}

		if st.until.IsZero() {
			// Healthy: straight through.
			m.mu.Unlock()
			return EngineGate{decision: gateProceed}, nil
		}

		// Cooldown expired → single-probe protocol.
		if st.probing {
			done := st.probeDone
			m.mu.Unlock()
			if done == nil {
				continue
			}
			timer := time.NewTimer(probeWaitCap)
			select {
			case <-done:
				timer.Stop()
			case <-ctx.Done():
				timer.Stop()
				return EngineGate{}, ctx.Err()
			case <-timer.C:
				// Re-gate: the probe should have finished by now.
			}
			continue
		}

		if circuitBlocked != nil && circuitBlocked() {
			// Circuit breaker still open: do not launch the probe; postpone
			// the cooldown expiry by the current tier duration.
			duration := st.stepDuration
			if duration <= 0 {
				duration = m.tier(engine, 1)
			}
			st.until = now.Add(duration)
			if st.reason == "" {
				st.reason = CooldownReasonNatural
			}
			gate := EngineGate{
				decision:     gateBlocked,
				until:        st.until,
				reason:       st.reason,
				retryAfter:   duration,
				cooldownMsec: st,
				mgr:          m,
			}
			m.mu.Unlock()
			return gate, nil
		}

		st.probing = true
		st.probeDone = make(chan struct{})
		probe := &engineProbe{mgr: m, st: st, engine: engine, generation: st.generation}
		m.mu.Unlock()
		return EngineGate{decision: gateProbe, probe: probe}, nil
	}
}

// errEngineCooldown builds the contract cooldown error for the fast-fail
// paths that go through the JSON error middleware: the envelope renders
// {"error":"engine_cooldown","reason":"COOLDOWN","retry_after_seconds":N}
// (top-level) plus the request_id, and the middleware echoes Retry-After.
func errEngineCooldown(gate EngineGate) *APIError {
	meta := map[string]interface{}{}
	if !gate.Until().IsZero() {
		meta["until"] = gate.Until().UTC().Format(time.RFC3339)
	}
	reason := gate.Reason()
	if reason == "" {
		reason = CooldownReasonNatural
	}
	return &APIError{
		HTTPStatus:        503,
		ErrorCode:         "engine_cooldown",
		Reason:            CooldownReasonNatural,
		RetryAfterSeconds: gate.RetryAfterSeconds(),
		Message: fmt.Sprintf("engine is in %s cooldown until %s",
			mapForced(reason), formatUntil(gate.Until())),
		Meta: meta,
	}
}

// errEngineCooldownForServer enriches the generic cooldown error with the
// engine name and the full meta.engines_cooldown snapshot so the 503 body
// carries the same cooldown list the success envelopes expose.
func (s *Server) errEngineCooldownForServer(engine string, gate EngineGate) *APIError {
	apiErr := errEngineCooldown(gate)
	if apiErr.Meta == nil {
		apiErr.Meta = map[string]interface{}{}
	}
	apiErr.Meta["engine"] = engine
	apiErr.Meta["engines_cooldown"] = s.enginesCooldownMeta()
	return apiErr
}

func mapForced(reason string) string {
	if reason == CooldownReasonForced {
		return "forced"
	}
	return "engine"
}

func formatUntil(until time.Time) string {
	if until.IsZero() {
		return "an unknown instant"
	}
	return until.UTC().Format(time.RFC3339)
}

// cooldownEntryFromStatus renders the meta.engines_cooldown element.
func cooldownEntryFromStatus(engine string, st EngineCooldownStatus) EngineCooldownEntry {
	return EngineCooldownEntry{
		Engine: engine,
		Until:  st.Until.UTC().Format(time.RFC3339),
		Reason: st.Reason,
	}
}

// retryAfterSecondsFor returns the smallest Retry-After (seconds, >= 1) among
// the given cooldown entries relative to now.
func retryAfterSecondsFor(entries []EngineCooldownEntry, now time.Time) int {
	best := 0
	for _, e := range entries {
		until, err := time.Parse(time.RFC3339, e.Until)
		if err != nil {
			continue
		}
		secs := int((until.Sub(now) + time.Second - time.Nanosecond) / time.Second)
		if secs < 1 {
			secs = 1
		}
		if best == 0 || secs < best {
			best = secs
		}
	}
	if best < 1 {
		best = 1
	}
	return best
}

// engineCooldownBatchError is returned by CheckIndexBatch when every engine a
// batch request selected is currently cooling: the whole request 503s. The
// handler renders the contract body (error/reason/retry_after_seconds +
// engines_cooldown) and the Retry-After header.
type engineCooldownBatchError struct {
	RetryAfterSeconds int
	Cooldowns         []EngineCooldownEntry
}

func (e *engineCooldownBatchError) Error() string {
	engines := make([]string, 0, len(e.Cooldowns))
	for _, c := range e.Cooldowns {
		engines = append(engines, c.Engine)
	}
	return fmt.Sprintf("all requested engines are in cooldown: %s (retry after %ds)",
		strings.Join(engines, ", "), e.RetryAfterSeconds)
}

// errCooldownBatchAsAPIError renders the batch 503 through the shared APIError
// envelope (error=engine_cooldown, reason=COOLDOWN, retry_after_seconds,
// meta.engines_cooldown) so every mount point (/v1/index/check and
// /api/playground/check) answers byte-identically.
func errCooldownBatchAsAPIError(e *engineCooldownBatchError) *APIError {
	meta := map[string]interface{}{
		"engines_cooldown": e.Cooldowns,
	}
	return &APIError{
		HTTPStatus:        503,
		ErrorCode:         "engine_cooldown",
		Reason:            CooldownReasonNatural,
		RetryAfterSeconds: e.RetryAfterSeconds,
		Message:           e.Error(),
		Meta:              meta,
	}
}

// cooldownUnitOutcome is the per-unit error for a cooling engine inside an
// otherwise-successful batch: {"error":{"code":"engine_cooldown",
// "message":..., "retry_after_seconds":N}}.
func cooldownUnitOutcome(gate EngineGate) IndexCheckEngineOutcome {
	return IndexCheckEngineOutcome{err: &IndexCheckEngineError{
		Code:              "engine_cooldown",
		Message:           fmt.Sprintf("engine in cooldown until %s", formatUntil(gate.Until())),
		RetryAfterSeconds: gate.RetryAfterSeconds(),
	}}
}
