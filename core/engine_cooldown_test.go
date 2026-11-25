package core

// 契约 A unit tests: engine cooldown state machine (no network).
//
// Coverage per the plan: state progression (10/30/120 tiers), counter zeroing
// after reset_after_minutes, ForceCooldown (sets until, never advances the
// counter), ResetCooldown (clears everything), probe recovery, probe
// interception advancing a tier, probe non-interception postponing, open
// circuit postponing the probe, and zero engine contact while cooling — plus
// the orchestration-layer interception for dedicated search and index check.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// fakeClock is a controllable time source for the cooldown manager.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestCooldownManager wires a manager with the fake clock.
func newTestCooldownManager(cfg EngineCooldownConfig) (*engineCooldownManager, *fakeClock) {
	clock := newFakeClock()
	m := newEngineCooldownManager(cfg)
	m.nowFn = clock.Now
	return m, clock
}

func cooldownCfg() EngineCooldownConfig {
	return EngineCooldownConfig{StepsMinutes: []int{10, 30, 120}, ResetAfterMinutes: 240}
}

// TestCooldownTierProgression: consecutive interceptions walk 10 → 30 → 120
// minutes and stay at 120 for further blocks.
func TestCooldownTierProgression(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())

	cases := []struct {
		block   int
		wantDur time.Duration
	}{
		{1, 10 * time.Minute},
		{2, 30 * time.Minute},
		{3, 120 * time.Minute},
		{4, 120 * time.Minute},
	}
	for _, tc := range cases {
		m.RecordBlock("baidu", ErrCaptcha)
		st := m.Status("baidu")
		if !st.InCooldown {
			t.Fatalf("block %d: expected engine cooling", tc.block)
		}
		got := st.Until.Sub(clock.Now())
		if got != tc.wantDur {
			t.Fatalf("block %d: cooldown %s, want %s", tc.block, got, tc.wantDur)
		}
		if st.ConsecutiveBlocks != tc.block {
			t.Fatalf("block %d: consecutive=%d", tc.block, st.ConsecutiveBlocks)
		}
		if st.Reason != CooldownReasonNatural {
			t.Fatalf("block %d: reason=%q, want COOLDOWN", tc.block, st.Reason)
		}
		// Advance past this cooldown so the next RecordBlock is a fresh probe
		// outcome (same as the gate letting a probe through after expiry).
		clock.Advance(tc.wantDur + time.Minute)
	}
}

// TestCooldownCounterResetAfterCleanStreak: after reset_after_minutes without
// interception the consecutive counter zeroes, so the next block starts at
// tier 1 again.
func TestCooldownCounterResetAfterCleanStreak(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())

	m.RecordBlock("baidu", ErrBlocked)
	m.RecordBlock("baidu", ErrBlocked)
	if got := m.Status("baidu").ConsecutiveBlocks; got != 2 {
		t.Fatalf("consecutive=%d, want 2", got)
	}

	// Clean streak of 239 minutes keeps the counter; reaching 240 zeroes it.
	clock.Advance(239 * time.Minute)
	if got := m.Status("baidu").ConsecutiveBlocks; got != 2 {
		t.Fatalf("consecutive after 239m=%d, want 2 (counter must persist until reset_after)", got)
	}
	clock.Advance(1 * time.Minute)
	if got := m.Status("baidu").ConsecutiveBlocks; got != 0 {
		t.Fatalf("consecutive after 240m=%d, want 0", got)
	}

	m.RecordBlock("baidu", ErrCaptcha)
	st := m.Status("baidu")
	if st.ConsecutiveBlocks != 1 {
		t.Fatalf("post-reset block consecutive=%d, want 1", st.ConsecutiveBlocks)
	}
	if got := st.Until.Sub(clock.Now()); got != 10*time.Minute {
		t.Fatalf("post-reset cooldown=%s, want 10m (tier 1 again)", got)
	}
}

// TestForceCooldownDoesNotAdvanceCounter: 强制冷却 sets until with reason
// FORCED and leaves consecutive_blocks untouched.
func TestForceCooldownDoesNotAdvanceCounter(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha)
	before := m.Status("baidu").ConsecutiveBlocks

	m.ForceCooldown("baidu", 45)
	st := m.Status("baidu")
	if !st.InCooldown {
		t.Fatal("forced cooldown not active")
	}
	if got := st.Until.Sub(clock.Now()); got != 45*time.Minute {
		t.Fatalf("forced cooldown=%s, want 45m", got)
	}
	if st.Reason != CooldownReasonForced {
		t.Fatalf("reason=%q, want FORCED", st.Reason)
	}
	if st.ConsecutiveBlocks != before {
		t.Fatalf("consecutive advanced by force: %d → %d", before, st.ConsecutiveBlocks)
	}
}

// TestResetCooldownClearsEverything: 强制恢复 clears cooldown, counter and
// last-block bookkeeping.
func TestResetCooldownClearsEverything(t *testing.T) {
	m, _ := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha)
	m.RecordBlock("baidu", ErrCaptcha)

	m.ResetCooldown("baidu")
	st := m.Status("baidu")
	if st.InCooldown || !st.Until.IsZero() {
		t.Fatalf("still cooling after reset: %+v", st)
	}
	if st.ConsecutiveBlocks != 0 {
		t.Fatalf("consecutive=%d after reset, want 0", st.ConsecutiveBlocks)
	}
	if !st.LastErrorAt.IsZero() && m.Status("baidu").InCooldown {
		t.Fatal("inconsistent state after reset")
	}
	if names := m.CooldownEngineNames(); len(names) != 0 {
		t.Fatalf("cooldown names after reset: %v", names)
	}
}

// TestProbeRecovery: 冷却到期放行一个探测请求 — the first gate after expiry
// grants a probe; success recovers the engine; concurrent waiters re-gate.
func TestCooldownProbeRecovery(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha)
	clock.Advance(11 * time.Minute)

	gate, err := m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.IsProbe() {
		t.Fatalf("expected probe grant at expiry, got decision=%d", gate.decision)
	}
	gate.FinishProbe(nil)

	st := m.Status("baidu")
	if st.InCooldown {
		t.Fatalf("engine still cooling after successful probe: %+v", st)
	}
	if st.ConsecutiveBlocks != 1 {
		t.Fatalf("successful probe must not change the counter, got %d", st.ConsecutiveBlocks)
	}

	// Next request is a normal request, not another probe.
	gate, err = m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if gate.IsProbe() || gate.Blocked() {
		t.Fatalf("expected plain proceed after recovery, got decision=%d", gate.decision)
	}
}

// TestProbeInterceptionAdvancesTier: 探测返回拦截类错误 → 连续+1 进下一档.
func TestCooldownProbeInterceptionAdvancesTier(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrRateLimited)
	clock.Advance(11 * time.Minute)

	gate, err := m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.IsProbe() {
		t.Fatalf("expected probe grant, got decision=%d", gate.decision)
	}
	// Simulate the resilient layer having recorded the interception (as
	// searchWithProtection does) before FinishProbe runs.
	m.RecordBlock("baidu", ErrRateLimited)
	gate.FinishProbe(ErrRateLimited)

	st := m.Status("baidu")
	if !st.InCooldown {
		t.Fatal("engine not cooling after intercepted probe")
	}
	if st.ConsecutiveBlocks != 2 {
		t.Fatalf("consecutive=%d, want 2", st.ConsecutiveBlocks)
	}
	if got := st.Until.Sub(clock.Now()); got != 30*time.Minute {
		t.Fatalf("cooldown after intercepted probe=%s, want 30m", got)
	}
}

// TestProbeInterceptionWithoutPriorRecord: FinishProbe must still put the
// engine into the next tier when the interception was not recorded elsewhere.
func TestCooldownProbeInterceptionWithoutPriorRecord(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha)
	clock.Advance(11 * time.Minute)

	gate, err := m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	gate.FinishProbe(fmt.Errorf("wrapped: %w", ErrBlocked))

	st := m.Status("baidu")
	if !st.InCooldown || st.ConsecutiveBlocks != 2 {
		t.Fatalf("expected tier 2 after intercepted probe, got %+v", st)
	}
	if got := st.Until.Sub(clock.Now()); got != 30*time.Minute {
		t.Fatalf("cooldown=%s, want 30m", got)
	}
}

// TestProbeNonInterceptionPostpones: 探测返回非拦截错误 → 顺延当前档位时长,
// 不进档不清零.
func TestCooldownProbeNonInterceptionPostpones(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha) // tier 1 → 10m
	clock.Advance(11 * time.Minute)

	gate, err := m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	gate.FinishProbe(context.DeadlineExceeded) // timeout, not interception

	st := m.Status("baidu")
	if !st.InCooldown {
		t.Fatal("expected postponed cooldown after incomplete probe")
	}
	if got := st.Until.Sub(clock.Now()); got != 10*time.Minute {
		t.Fatalf("postponed cooldown=%s, want 10m (current tier)", got)
	}
	if st.ConsecutiveBlocks != 1 {
		t.Fatalf("consecutive=%d, want 1 (不进档不清零)", st.ConsecutiveBlocks)
	}

	// The postponed window blocks again; a new probe is granted after it.
	gate, err = m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.Blocked() {
		t.Fatal("expected blocked gate during postponed window")
	}
	clock.Advance(11 * time.Minute)
	gate, err = m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.IsProbe() {
		t.Fatal("expected a new probe after postponed window")
	}
	gate.FinishProbe(nil)
	if m.Status("baidu").InCooldown {
		t.Fatal("expected recovery after eventual successful probe")
	}
}

// TestProbeWaitsWhileInFlight: concurrent requests during expiry wait for the
// single probe instead of starting their own.
func TestCooldownProbeWaitsWhileInFlight(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha)
	clock.Advance(11 * time.Minute)

	first, err := m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !first.IsProbe() {
		t.Fatal("first gate at expiry must be the probe")
	}

	probeDone := make(chan struct{})
	go func() {
		second, gateErr := m.Gate(context.Background(), "baidu", nil)
		if gateErr != nil {
			t.Errorf("waiter gate: %v", gateErr)
			close(probeDone)
			return
		}
		// The first probe succeeded → engine recovered → the waiter proceeds
		// as a normal (non-probe) request.
		if second.IsProbe() || second.Blocked() {
			t.Errorf("waiter should proceed normally after successful probe, decision=%d", second.decision)
		}
		close(probeDone)
	}()

	select {
	case <-probeDone:
		t.Fatal("waiter proceeded while the probe was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	first.FinishProbe(nil) // recovery

	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never re-gated after probe completion")
	}
}

// TestOpenCircuitPostponesProbe: 熔断 open 时不发起探测,冷却到期时间顺延.
func TestOpenCircuitPostponesProbe(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha) // tier 1 → 10m
	clock.Advance(11 * time.Minute)    // cooldown expired, circuit still open

	circuitOpen := true
	gate, err := m.Gate(context.Background(), "baidu", func() bool { return circuitOpen })
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.Blocked() {
		t.Fatal("expected blocked gate while the circuit is open at expiry")
	}
	if gate.RetryAfterSeconds() < 1 {
		t.Fatalf("RetryAfterSeconds=%d, want >= 1", gate.RetryAfterSeconds())
	}
	st := m.Status("baidu")
	if got := st.Until.Sub(clock.Now()); got != 10*time.Minute {
		t.Fatalf("postponed until=%s from now, want 10m", got)
	}

	// Once the circuit closes, the next gate grants the probe.
	circuitOpen = false
	clock.Advance(11 * time.Minute)
	gate, err = m.Gate(context.Background(), "baidu", func() bool { return circuitOpen })
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.IsProbe() {
		t.Fatal("expected probe grant once the circuit closed")
	}
	gate.FinishProbe(nil)
}

// TestResetInvalidatesInFlightProbe: a probe granted before ResetCooldown /
// ForceCooldown must not clobber the operator action when it completes.
func TestCooldownResetInvalidatesInFlightProbe(t *testing.T) {
	m, clock := newTestCooldownManager(cooldownCfg())
	m.RecordBlock("baidu", ErrCaptcha)
	clock.Advance(11 * time.Minute)

	gate, err := m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.IsProbe() {
		t.Fatal("expected probe grant")
	}

	// Operator resets while the probe is in flight…
	m.ResetCooldown("baidu")
	// …and the probe then fails with an interception: it must NOT re-block.
	gate.FinishProbe(ErrCaptcha)
	if st := m.Status("baidu"); st.InCooldown || st.ConsecutiveBlocks != 0 {
		t.Fatalf("stale probe clobbered the reset: %+v", st)
	}

	// Same for ForceCooldown.
	m.ForceCooldown("baidu", 30)
	clock.Advance(31 * time.Minute)
	gate, err = m.Gate(context.Background(), "baidu", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !gate.IsProbe() {
		t.Fatal("expected probe grant after forced cooldown expiry")
	}
	m.ForceCooldown("baidu", 60)
	gate.FinishProbe(nil)
	st := m.Status("baidu")
	if !st.InCooldown || st.Reason != CooldownReasonForced {
		t.Fatalf("stale probe clobbered the forced cooldown: %+v", st)
	}
	if got := st.Until.Sub(clock.Now()); got != 60*time.Minute {
		t.Fatalf("forced cooldown window=%s, want 60m", got)
	}
}

// TestGateHealthyProceeds: a healthy engine gates through without probe.
func TestCooldownGateHealthyProceeds(t *testing.T) {
	m, _ := newTestCooldownManager(cooldownCfg())
	gate, err := m.Gate(context.Background(), "sogou", nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if gate.Blocked() || gate.IsProbe() {
		t.Fatalf("healthy engine must proceed, decision=%d", gate.decision)
	}
}

// TestCooldownNormalizedConfig: empty/invalid config falls back to defaults.
func TestCooldownNormalizedConfig(t *testing.T) {
	got := EngineCooldownConfig{}.normalized()
	if len(got.StepsMinutes) != 3 || got.StepsMinutes[0] != 10 || got.ResetAfterMinutes != 240 {
		t.Fatalf("normalized defaults wrong: %+v", got)
	}
	got = EngineCooldownConfig{StepsMinutes: []int{0, -5, 5}, ResetAfterMinutes: -1}.normalized()
	if len(got.StepsMinutes) != 1 || got.StepsMinutes[0] != 5 || got.ResetAfterMinutes != 240 {
		t.Fatalf("normalized invalid values wrong: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Orchestration-layer interception (Server level, still no network).
// ---------------------------------------------------------------------------

// newCooldownTestServer builds a Server over mock engines with tiny cooldown
// settings. searchErr/serp control engine behavior; calls counts engine hits.
func newCooldownTestServer(t *testing.T, searchErr error) (*Server, *engineMock, *engineMock) {
	t.Helper()
	failing := &engineMock{
		name:        "baidu",
		initialized: true,
		limiter:     rate.NewLimiter(rate.Inf, 1),
		searchFn: func(context.Context, Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: "https://example.com/baidu", Title: "baidu result"}}, searchErr
		},
	}
	healthy := &engineMock{
		name:        "sogou",
		initialized: true,
		limiter:     rate.NewLimiter(rate.Inf, 1),
	}
	// Mock engines also answer CheckIndexed via Search (indexLookupEngine
	// adapts CheckIndexed to Search through the IndexChecker interface; the
	// mock lacks it, so implement the capability below).
	opts := DefaultServerOptions()
	opts.CacheTTL = 0 // no cache: every request reaches the gate/engine
	srv := NewServerWithOptions("127.0.0.1", 7121, opts, failing, healthy)
	return srv, failing, healthy
}

// checkIndexedMock adds CheckIndexed capability to engineMock.
type checkIndexedMock struct {
	engineMock
	checkFn func(ctx context.Context, targetURL string, q Query) ([]SearchResult, error)

	mu         sync.Mutex
	checkCalls int
}

func (m *checkIndexedMock) CheckIndexed(ctx context.Context, targetURL string, q Query) ([]SearchResult, error) {
	m.mu.Lock()
	m.checkCalls++
	m.mu.Unlock()
	if m.checkFn != nil {
		return m.checkFn(ctx, targetURL, q)
	}
	return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
}

func (m *checkIndexedMock) checkCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkCalls
}

// TestDedicatedSearchCooldownFastFailAndZeroEngineContact: 强制冷却后
// /baidu/search 秒回 503 engine_cooldown + Retry-After,引擎零触达.
func TestDedicatedSearchCooldownFastFailAndZeroEngineContact(t *testing.T) {
	srv, baidu, _ := newCooldownTestServer(t, nil)

	// Healthy request works and touches the engine once.
	resp := request(t, srv, "/baidu/search?text=golang")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthy search status=%d", resp.StatusCode)
	}
	closeTestResource(t, resp.Body)
	if got := baidu.searchCalls; got != 1 {
		t.Fatalf("search calls after healthy request=%d, want 1", got)
	}

	if err := srv.ForceCooldown("baidu", 30); err != nil {
		t.Fatalf("force: %v", err)
	}

	for i := 0; i < 3; i++ {
		resp := request(t, srv, "/baidu/search?text=golang")
		defer closeTestResource(t, resp.Body)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("cooled search status=%d, want 503", resp.StatusCode)
		}
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter == "" || retryAfter == "0" {
			t.Fatalf("cooled search missing Retry-After header (got %q)", retryAfter)
		}
		raw, _ := io.ReadAll(resp.Body)
		var body struct {
			Error             string `json:"error"`
			Reason            string `json:"reason"`
			RetryAfterSeconds int    `json:"retry_after_seconds"`
			RequestID         string `json:"request_id"`
			Meta              struct {
				Engine          string `json:"engine"`
				EnginesCooldown []struct {
					Engine string `json:"engine"`
					Until  string `json:"until"`
					Reason string `json:"reason"`
				} `json:"engines_cooldown"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode 503 body: %v\n%s", err, raw)
		}
		if body.Error != "engine_cooldown" || body.Reason != "COOLDOWN" {
			t.Fatalf("body error=%q reason=%q", body.Error, body.Reason)
		}
		if body.RetryAfterSeconds < 1 {
			t.Fatalf("retry_after_seconds=%d", body.RetryAfterSeconds)
		}
		if body.RequestID == "" {
			t.Fatal("503 body missing request_id (must ride the request middleware)")
		}
		if body.Meta.Engine != "baidu" {
			t.Fatalf("meta.engine wrong: %q", body.Meta.Engine)
		}
		if len(body.Meta.EnginesCooldown) != 1 || body.Meta.EnginesCooldown[0].Engine != "baidu" || body.Meta.EnginesCooldown[0].Until == "" {
			t.Fatalf("meta.engines_cooldown wrong: %+v", body.Meta.EnginesCooldown)
		}
	}

	if got := baidu.searchCalls; got != 1 {
		t.Fatalf("engine touched while cooling: calls=%d, want 1 (pre-cooldown only)", got)
	}

	// Reset restores traffic.
	if err := srv.ResetCooldown("baidu"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	resp = request(t, srv, "/baidu/search?text=golang")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-reset search status=%d", resp.StatusCode)
	}
	closeTestResource(t, resp.Body)
	if got := baidu.searchCalls; got != 2 {
		t.Fatalf("search calls after reset=%d, want 2", got)
	}
}

// TestDedicatedSearchTriggeredByInterception: an engine answering captcha puts
// itself into cooldown — the NEXT request fails fast.
func TestCooldownDedicatedSearchTriggeredByInterception(t *testing.T) {
	srv, baidu, _ := newCooldownTestServer(t, ErrCaptcha)

	resp := request(t, srv, "/baidu/search?text=golang")
	defer closeTestResource(t, resp.Body)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("first (trigger) request status=%d, want 429 captcha_detected", resp.StatusCode)
	}
	if got := baidu.searchCalls; got != 1 {
		t.Fatalf("calls=%d, want 1", got)
	}

	resp = request(t, srv, "/baidu/search?text=golang")
	defer closeTestResource(t, resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second request status=%d, want 503 engine_cooldown", resp.StatusCode)
	}
	if got := baidu.searchCalls; got != 1 {
		t.Fatalf("engine touched during cooldown: calls=%d", got)
	}
}

// TestIndexCheckBatchCooldownSemantics covers the batch path:
//   - engines=[cooling] alone → whole request 503 engine_cooldown;
//   - engines=[cooling, healthy] → 200, cooling units answer engine_cooldown,
//     healthy units judge normally, meta.engines_cooldown present.
func TestIndexCheckBatchCooldownSemantics(t *testing.T) {
	baidu := &checkIndexedMock{engineMock: engineMock{name: "baidu", initialized: true, limiter: rate.NewLimiter(rate.Inf, 1)}}
	sogou := &checkIndexedMock{engineMock: engineMock{name: "sogou", initialized: true, limiter: rate.NewLimiter(rate.Inf, 1)}}
	opts := DefaultServerOptions()
	opts.CacheTTL = 0
	srv := NewServerWithOptions("127.0.0.1", 7122, opts, baidu, sogou)
	// /v1/index/check is mounted by the platform in production; register the
	// handler directly to exercise the same mount shape here.
	srv.FiberApp().Post(BasePath+"/v1/index/check", srv.IndexCheckHandler)

	if err := srv.ForceCooldown("baidu", 30); err != nil {
		t.Fatalf("force: %v", err)
	}

	// ---- all selected engines cooling → whole-request 503 ------------------
	payload, err := srv.CheckIndexBatch(context.Background(), []string{"https://a.example.com/"}, []string{"baidu"})
	if err == nil {
		t.Fatalf("expected engine_cooldown error, got payload=%+v", payload)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusServiceUnavailable || apiErr.ErrorCode != "engine_cooldown" {
		t.Fatalf("expected 503 engine_cooldown APIError, got %v", err)
	}
	if apiErr.RetryAfterSeconds < 1 {
		t.Fatalf("retry_after_seconds=%d", apiErr.RetryAfterSeconds)
	}
	if got := baidu.checkCallCount() + sogou.checkCallCount(); got != 0 {
		t.Fatalf("engines touched on all-cooldown batch: %d", got)
	}

	// ---- mixed batch → 200 with per-unit engine_cooldown -------------------
	payload, err = srv.CheckIndexBatch(context.Background(), []string{"https://a.example.com/"}, []string{"baidu", "sogou"})
	if err != nil {
		t.Fatalf("mixed batch: %v", err)
	}
	checkResp, ok := payload.(IndexCheckResponse)
	if !ok {
		t.Fatalf("payload type %T", payload)
	}
	if checkResp.Meta.Units != 2 {
		t.Fatalf("units=%d, want 2", checkResp.Meta.Units)
	}
	if len(checkResp.Meta.EnginesCooldown) != 1 || checkResp.Meta.EnginesCooldown[0].Engine != "baidu" {
		t.Fatalf("meta.engines_cooldown wrong: %+v", checkResp.Meta.EnginesCooldown)
	}
	units := checkResp.Results[0].Engines
	if units["sogou"].err != nil || !units["sogou"].success.Indexed {
		t.Fatalf("sogou unit should judge normally: %+v", units["sogou"])
	}
	if units["baidu"].err == nil || units["baidu"].err.Code != "engine_cooldown" || units["baidu"].err.RetryAfterSeconds < 1 {
		t.Fatalf("baidu unit should answer engine_cooldown: %+v", units["baidu"])
	}
	if got := baidu.checkCallCount(); got != 0 {
		t.Fatalf("cooling engine touched in mixed batch: %d", got)
	}
	if got := sogou.checkCallCount(); got != 1 {
		t.Fatalf("healthy engine calls=%d, want 1", got)
	}

	// ---- mixed batch through the HTTP handler keeps meta + status ----------
	req := httptest.NewRequest(http.MethodPost, BasePath+"/v1/index/check",
		strings.NewReader(`{"urls":["https://a.example.com/"],"engines":["baidu","sogou"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.app.Test(req, -1)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer closeTestResource(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mixed batch over HTTP status=%d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var httpBody struct {
		Meta struct {
			EnginesCooldown []EngineCooldownEntry `json:"engines_cooldown"`
		} `json:"meta"`
		Results []struct {
			Engines map[string]struct {
				Error *IndexCheckEngineError `json:"error"`
			} `json:"engines"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &httpBody); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	if len(httpBody.Meta.EnginesCooldown) != 1 || httpBody.Meta.EnginesCooldown[0].Engine != "baidu" {
		t.Fatalf("HTTP meta.engines_cooldown wrong: %+v", httpBody.Meta.EnginesCooldown)
	}
	if httpBody.Results[0].Engines["baidu"].Error == nil ||
		httpBody.Results[0].Engines["baidu"].Error.Code != "engine_cooldown" {
		t.Fatalf("HTTP baidu unit wrong: %+v", httpBody.Results[0].Engines["baidu"])
	}

	// ---- all-cooldown batch through the HTTP handler → 503 envelope --------
	req = httptest.NewRequest(http.MethodPost, BasePath+"/v1/index/check",
		strings.NewReader(`{"urls":["https://a.example.com/"],"engines":["baidu"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = srv.app.Test(req, -1)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer closeTestResource(t, resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("all-cooldown batch status=%d, want 503", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("all-cooldown batch missing Retry-After header")
	}
	raw, _ = io.ReadAll(resp.Body)
	var errBody struct {
		Error             string `json:"error"`
		Reason            string `json:"reason"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
		RequestID         string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &errBody); err != nil {
		t.Fatalf("decode 503: %v\n%s", err, raw)
	}
	if errBody.Error != "engine_cooldown" || errBody.Reason != "COOLDOWN" || errBody.RetryAfterSeconds < 1 || errBody.RequestID == "" {
		t.Fatalf("all-cooldown envelope wrong: %+v", errBody)
	}
}

// TestIndexCheckBatchCooldownResetRecovers: after ResetCooldown the batch
// judges the previously-cooled engine again.
func TestIndexCheckBatchCooldownResetRecovers(t *testing.T) {
	baidu := &checkIndexedMock{engineMock: engineMock{name: "baidu", initialized: true, limiter: rate.NewLimiter(rate.Inf, 1)}}
	opts := DefaultServerOptions()
	opts.CacheTTL = 0
	srv := NewServerWithOptions("127.0.0.1", 7123, opts, baidu)

	if err := srv.ForceCooldown("baidu", 30); err != nil {
		t.Fatalf("force: %v", err)
	}
	if _, err := srv.CheckIndexBatch(context.Background(), []string{"https://a.example.com/"}, []string{"baidu"}); err == nil {
		t.Fatal("expected all-cooldown 503 before reset")
	}
	if err := srv.ResetCooldown("baidu"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	payload, err := srv.CheckIndexBatch(context.Background(), []string{"https://a.example.com/"}, []string{"baidu"})
	if err != nil {
		t.Fatalf("post-reset batch: %v", err)
	}
	checkResp := payload.(IndexCheckResponse)
	if checkResp.Results[0].Engines["baidu"].err != nil {
		t.Fatalf("baidu unit error after reset: %+v", checkResp.Results[0].Engines["baidu"])
	}
}

// TestSearchViaEngineCooldownAndResults: the MCP seam — healthy engine returns
// result maps, cooling engine returns the engine_cooldown APIError.
func TestSearchViaEngineCooldownAndResults(t *testing.T) {
	srv, baidu, _ := newCooldownTestServer(t, nil)

	results, err := srv.SearchViaEngine(context.Background(), "baidu", "golang", 10)
	if err != nil {
		t.Fatalf("SearchViaEngine: %v", err)
	}
	if len(results) == 0 || results[0]["url"] == "" || results[0]["rank"] == nil {
		t.Fatalf("result maps wrong: %+v", results)
	}
	if _, ok := results[0]["snippet"]; !ok {
		t.Fatalf("result map missing snippet: %+v", results[0])
	}

	if err := srv.ForceCooldown("baidu", 15); err != nil {
		t.Fatalf("force: %v", err)
	}
	if _, err := srv.SearchViaEngine(context.Background(), "baidu", "golang", 10); !IsEngineCooldownError(err) {
		t.Fatalf("expected engine_cooldown error, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(mustErr(t, srv, "baidu"), &apiErr) || apiErr.HTTPStatus != http.StatusServiceUnavailable || apiErr.RetryAfterSeconds < 1 {
		t.Fatalf("cooldown error shape wrong: %v", mustErr(t, srv, "baidu"))
	}
	if got := baidu.searchCalls; got != 1 {
		t.Fatalf("engine touched during cooldown via SearchViaEngine: %d", got)
	}

	// Alias resolution: 360 → so360 (unknown for this server) errors 400.
	if _, err := srv.SearchViaEngine(context.Background(), "nosuchengine", "golang", 10); err == nil {
		t.Fatal("expected unknown engine error")
	}
}

func mustErr(t *testing.T, srv *Server, engine string) error {
	t.Helper()
	_, err := srv.SearchViaEngine(context.Background(), engine, "golang", 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

// TestEnginesStatusWithCooldownFields: /api/engines extension fields align
// with the contract: state/until/reason/consecutive_blocks/last_error_at.
func TestEnginesStatusWithCooldownFields(t *testing.T) {
	srv, _, _ := newCooldownTestServer(t, nil)

	entries, ok := srv.EnginesStatusWithCooldown().([]map[string]interface{})
	if !ok || len(entries) != 2 {
		t.Fatalf("snapshot shape wrong: %T", srv.EnginesStatusWithCooldown())
	}
	for _, entry := range entries {
		for _, field := range []string{"engine", "endpoint", "enabled", "breaker", "state", "until", "reason", "consecutive_blocks", "last_error_at"} {
			if _, present := entry[field]; !present {
				t.Fatalf("entry missing field %q: %+v", field, entry)
			}
		}
		if entry["state"] != "ok" {
			t.Fatalf("healthy engine state=%v", entry["state"])
		}
		if entry["until"] != nil || entry["reason"] != "" || entry["last_error_at"] != nil {
			t.Fatalf("healthy engine extension fields wrong: %+v", entry)
		}
	}

	if err := srv.ForceCooldown("baidu", 20); err != nil {
		t.Fatalf("force: %v", err)
	}
	var baiduEntry map[string]interface{}
	for _, entry := range srv.EnginesStatusWithCooldown().([]map[string]interface{}) {
		if entry["engine"] == "baidu" {
			baiduEntry = entry
		}
	}
	if baiduEntry == nil {
		t.Fatal("baidu entry missing")
	}
	if baiduEntry["state"] != "cooldown" || baiduEntry["reason"] != CooldownReasonForced {
		t.Fatalf("cooling entry state/reason wrong: %+v", baiduEntry)
	}
	if baiduEntry["until"] == nil {
		t.Fatalf("cooling entry until missing: %+v", baiduEntry)
	}
	if _, err := time.Parse(time.RFC3339, baiduEntry["until"].(string)); err != nil {
		t.Fatalf("until not RFC3339: %v", baiduEntry["until"])
	}
	// A forced cooldown carries no engine error yet → last_error_at stays nil.
	if baiduEntry["last_error_at"] != nil {
		t.Fatalf("forced cooldown must not fabricate last_error_at: %+v", baiduEntry)
	}
	if baiduEntry["consecutive_blocks"] != 0 {
		t.Fatalf("force must not advance consecutive_blocks: %+v", baiduEntry)
	}

	// A natural block records both the tier and last_error_at (white-box:
	// production records through searchWithProtection — see
	// TestDedicatedSearchTriggeredByInterception).
	srv.resilient.cdManager.RecordBlock("baidu", ErrCaptcha)
	for _, entry := range srv.EnginesStatusWithCooldown().([]map[string]interface{}) {
		if entry["engine"] == "baidu" {
			if entry["consecutive_blocks"] != 1 {
				t.Fatalf("consecutive_blocks after block=%v, want 1", entry["consecutive_blocks"])
			}
			if entry["last_error_at"] == nil {
				t.Fatalf("last_error_at missing after a real block: %+v", entry)
			}
			if _, err := time.Parse(time.RFC3339, entry["last_error_at"].(string)); err != nil {
				t.Fatalf("last_error_at not RFC3339: %v", entry["last_error_at"])
			}
			if entry["reason"] != CooldownReasonNatural {
				t.Fatalf("natural cooldown reason=%v", entry["reason"])
			}
		}
	}

	if err := srv.ResetCooldown("baidu"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	for _, entry := range srv.EnginesStatusWithCooldown().([]map[string]interface{}) {
		if entry["engine"] == "baidu" && entry["state"] != "ok" {
			t.Fatalf("baidu state after reset=%v", entry["state"])
		}
	}
}

// TestEnginesCooldownMetaOnSearchEnvelope: successful dedicated search carries
// meta.engines_cooldown for the engines that are cooling.
func TestEnginesCooldownMetaOnSearchEnvelope(t *testing.T) {
	srv, _, sogou := newCooldownTestServer(t, nil)
	if err := srv.ForceCooldown("sogou", 10); err != nil {
		t.Fatalf("force sogou cooldown: %v", err)
	}

	resp := request(t, srv, "/baidu/search?text=golang")
	defer closeTestResource(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Meta struct {
			EnginesCooldown []EngineCooldownEntry `json:"engines_cooldown"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	if len(body.Meta.EnginesCooldown) != 1 || body.Meta.EnginesCooldown[0].Engine != "sogou" {
		t.Fatalf("meta.engines_cooldown wrong: %+v", body.Meta.EnginesCooldown)
	}
	_ = sogou
}

// TestIsEngineCooldownErrorDetection: the seam the MCP layer maps with.
func TestIsEngineCooldownErrorDetection(t *testing.T) {
	if !IsEngineCooldownError(errEngineCooldown(EngineGate{decision: gateBlocked, until: time.Now().Add(time.Minute), reason: CooldownReasonNatural})) {
		t.Fatal("APIError cooldown not detected")
	}
	if !IsEngineCooldownError(ErrEngineCooldown) {
		t.Fatal("sentinel not detected")
	}
	if IsEngineCooldownError(ErrCaptcha) || IsEngineCooldownError(nil) {
		t.Fatal("false positive")
	}
}

// TestCooldownDoesNotFreezeOtherEngines: cooldown is per engine — sogou cooling
// never blocks baidu.
func TestCooldownDoesNotFreezeOtherEngines(t *testing.T) {
	srv, baidu, _ := newCooldownTestServer(t, nil)
	if err := srv.ForceCooldown("sogou", 10); err != nil {
		t.Fatalf("force sogou cooldown: %v", err)
	}

	resp := request(t, srv, "/baidu/search?text=golang")
	defer closeTestResource(t, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("other engine blocked by unrelated cooldown: status=%d", resp.StatusCode)
	}
	_ = baidu
}

// TestEnginesStatusCircuitOpenState: an engine whose circuit breaker opened
// (without any cooldown — parser failures are not interception) reports
// state="circuit_open" in /api/engines, with cooldown fields untouched.
func TestCooldownEnginesStatusCircuitOpenState(t *testing.T) {
	failing := &engineMock{
		name:        "baidu",
		initialized: true,
		limiter:     rate.NewLimiter(rate.Inf, 1),
		searchFn: func(context.Context, Query) ([]SearchResult, error) {
			return nil, fmt.Errorf("wrapped: %w", ErrParser)
		},
	}
	opts := DefaultServerOptions()
	opts.CacheTTL = 0
	opts.Resilience.CircuitBreaker = CircuitBreakerConfig{FailureThreshold: 3, RecoveryTimeout: time.Hour, SuccessThreshold: 1}
	srv := NewServerWithOptions("127.0.0.1", 7124, opts, failing)

	for i := 0; i < 4; i++ {
		resp := request(t, srv, "/baidu/search?text=golang")
		closeTestResource(t, resp.Body)
	}
	// The engine is failing via the circuit breaker now — no cooldown was
	// recorded (parser failures are not interception).
	if st := srv.resilient.EngineCooldownStatus("baidu"); st.InCooldown {
		t.Fatalf("parser failures must not trip the cooldown: %+v", st)
	}
	var state interface{}
	for _, entry := range srv.EnginesStatusWithCooldown().([]map[string]interface{}) {
		if entry["engine"] == "baidu" {
			state = entry["state"]
		}
	}
	if state != "circuit_open" {
		t.Fatalf("baidu state=%v, want circuit_open", state)
	}
}
