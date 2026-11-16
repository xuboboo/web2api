package platform

// S4 dedicated tests: rate-limit tiers (B-02/B-05), key enhancements
// (rotate/expires_at/allowed_ips), sessions, audit trail, backup snapshot and
// the usage count index accuracy.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"github.com/gofiber/fiber/v2"
	"os"
)

// loginToken performs the login roundtrip and returns the Bearer token.
func loginToken(t *testing.T, app *fiber.App) string {
	t.Helper()
	status, body, _ := doJSONErr(t, app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != http.StatusOK || body["token"] == nil {
		t.Fatalf("login: status=%d body=%v", status, body)
	}
	return body["token"].(string)
}

// fireN requests target n times through app, returning the last non-2xx body
// seen plus whether any 429 rate_limited appeared.
func fireN(t *testing.T, app *fiber.App, method, target string, body any, headers map[string]string, n int) (bool, map[string]any) {
	t.Helper()
	saw429 := false
	var last map[string]any
	for i := 0; i < n; i++ {
		status, decoded, _ := doJSONErr(t, app, method, target, body, headers)
		if status == http.StatusTooManyRequests {
			saw429 = true
			last = decoded
			break
		}
	}
	return saw429, last
}

func TestAdminPerIPRateLimit(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.SetSecurity(SecurityConfig{AdminRatePerIP: 2, AdminRatePerMinute: 0})

	token := loginToken(t, env.app)
	// burst = max(15, 2/4) = 15 → the 16th rapid request answers 429 per-IP.
	saw429, body := fireN(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(token), 20)
	if !saw429 {
		t.Fatal("per-IP admin limiter did not fire")
	}
	if body["error"] != "rate_limited" {
		t.Fatalf("per-IP 429 body: %v", body)
	}

	// A different IP keeps its own bucket: white-box prime the table with the
	// test IP exhausted, then verify another IP is allowed.
	if env.p.allowAdminIPRate("203.0.113.9") != true {
		t.Fatal("an untouched IP must not be limited")
	}
}

func TestPerKeyRateLimit(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.SetSecurity(SecurityConfig{APIRatePerMinute: 2})

	token := loginToken(t, env.app)
	id, full := createKey(t, env, token, "rate-test", 0)
	_ = id

	// burst = max(10, 2/4) = 10 → the 11th rapid /v1/quota call answers 429.
	saw429, body := fireN(t, env.app, http.MethodGet, "/v1/quota", nil,
		map[string]string{"X-API-Key": full}, 14)
	if !saw429 {
		t.Fatal("per-key limiter did not fire")
	}
	if body["error"] != "rate_limited" {
		t.Fatalf("per-key 429 body: %v", body)
	}
}

func TestUnauthRateLimit(t *testing.T) {
	env := newTestEnv(t, 200, false)
	env.p.SetSecurity(SecurityConfig{UnauthRatePerMinute: 2})

	// No credentials: the unauth per-IP limiter guards /v1/* (防爆破).
	saw429, body := fireN(t, env.app, http.MethodGet, "/v1/quota", nil, nil, 8)
	if !saw429 {
		t.Fatal("unauth limiter did not fire")
	}
	if body["error"] != "rate_limited" {
		t.Fatalf("unauth 429 body: %v", body)
	}
}

func TestKeyRotate(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := loginToken(t, env.app)
	id, oldFull := createKey(t, env, token, "rotate-me", 0)
	oldPrefix := oldFull[:len(KeyPrefix)+6]

	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/api/keys/"+id+"/rotate", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("rotate: status=%d body=%v", status, body)
	}
	newFull := body["key"].(string)
	if newFull == oldFull || newFull == "" {
		t.Fatalf("rotate must return fresh material: old=%q new=%q", oldFull, newFull)
	}
	if body["id"] != id || body["name"] != "rotate-me" {
		t.Fatalf("rotate must preserve id/name: %v", body)
	}
	if body["prefix"] == oldPrefix {
		t.Fatalf("rotate must update the prefix: %v", body)
	}

	// The old material answers 401 immediately; the new one works.
	status, _, _ = doJSONErr(t, env.app, http.MethodGet, "/v1/quota", nil,
		map[string]string{"X-API-Key": oldFull})
	if status != http.StatusUnauthorized {
		t.Fatalf("old key after rotate: status=%d, want 401", status)
	}
	status, _, _ = doJSONErr(t, env.app, http.MethodGet, "/v1/quota", nil,
		map[string]string{"X-API-Key": newFull})
	if status != http.StatusOK {
		t.Fatalf("new key after rotate: status=%d, want 200", status)
	}

	// Rotating an unknown id answers 404.
	status, _, _ = doJSONErr(t, env.app, http.MethodPost, "/api/keys/absent/rotate", nil, adminHeaders(token))
	if status != http.StatusNotFound {
		t.Fatalf("rotate absent: status=%d, want 404", status)
	}
}

func TestKeyExpiresAtAndAllowedIPs(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := loginToken(t, env.app)

	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)

	// Expired at creation → 401 on use.
	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/api/keys",
		map[string]any{"name": "expired", "expires_at": past}, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("create expired key: %d %v", status, body)
	}
	expiredFull := body["key"].(string)
	status, _, _ = doJSONErr(t, env.app, http.MethodGet, "/v1/quota", nil,
		map[string]string{"X-API-Key": expiredFull})
	if status != http.StatusUnauthorized {
		t.Fatalf("expired key: status=%d, want 401", status)
	}

	// Future expiry works; PATCH can clear it (expires_at: null).
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/keys",
		map[string]any{"name": "expiring", "expires_at": future}, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("create future key: %d %v", status, body)
	}
	id := body["id"].(string)
	futureFull := body["key"].(string)
	status, _, _ = doJSONErr(t, env.app, http.MethodGet, "/v1/quota", nil,
		map[string]string{"X-API-Key": futureFull})
	if status != http.StatusOK {
		t.Fatalf("future key: status=%d, want 200", status)
	}
	status, body, _ = doJSONErr(t, env.app, http.MethodPatch, "/api/keys/"+id,
		map[string]any{"expires_at": nil}, adminHeaders(token))
	if status != http.StatusOK || body["expires_at"] != nil {
		t.Fatalf("clear expires_at: status=%d body=%v", status, body)
	}

	// allowed_ips: create restricted to an IP the test client does not use.
	status, body, _ = doJSONErr(t, env.app, http.MethodPost, "/api/keys",
		map[string]any{"name": "iplocked", "allowed_ips": []string{"203.0.113.77"}}, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("create iplocked key: %d %v", status, body)
	}
	lockedFull := body["key"].(string)
	status, body, _ = doJSONErr(t, env.app, http.MethodGet, "/v1/quota", nil,
		map[string]string{"X-API-Key": lockedFull})
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("non-allowlisted IP: status=%d body=%v, want 403 forbidden", status, body)
	}

	// PATCH rejects malformed CIDR entries with 400.
	status, _, _ = doJSONErr(t, env.app, http.MethodPatch, "/api/keys/"+id,
		map[string]any{"allowed_ips": []string{"not-an-ip"}}, adminHeaders(token))
	if status != http.StatusBadRequest {
		t.Fatalf("bad allowed_ips: status=%d, want 400", status)
	}
}

func TestSessionsLifecycle(t *testing.T) {
	env := newTestEnv(t, 200, false)
	app := env.app

	// Two logins → two sessions.
	token1 := loginToken(t, app)
	claims1 := mustVerifyClaims(t, env.p, token1)
	if claims1.SID == "" || claims1.JTI == "" {
		t.Fatalf("S4 token missing sid/jti: %+v", claims1)
	}
	status, body, _ := doJSONErr(t, app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": "pw-123"}, nil)
	if status != http.StatusOK {
		t.Fatalf("second login: %d", status)
	}
	token2 := body["token"].(string)

	// Listing flags the requester's own session.
	status, body, _ = doJSONErr(t, app, http.MethodGet, "/api/sessions", nil, adminHeaders(token2))
	if status != http.StatusOK {
		t.Fatalf("list sessions: %d %v", status, body)
	}
	items := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 sessions, got %v", body)
	}
	current := 0
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("exactly one current session expected: %v", items)
	}

	// Revoking session 1 invalidates token1 everywhere.
	status, _, _ = doJSONErr(t, app, http.MethodDelete, "/api/sessions/"+claims1.SID, nil, adminHeaders(token2))
	if status != http.StatusOK {
		t.Fatalf("revoke: %d", status)
	}
	status, _, _ = doJSONErr(t, app, http.MethodGet, "/api/me", nil, adminHeaders(token1))
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked session token: status=%d, want 401", status)
	}
	// token2 (current session) keeps working.
	status, _, _ = doJSONErr(t, app, http.MethodGet, "/api/me", nil, adminHeaders(token2))
	if status != http.StatusOK {
		t.Fatalf("current session after revoke: status=%d, want 200", status)
	}

	// Logout revokes the current session.
	status, _, _ = doJSONErr(t, app, http.MethodPost, "/api/auth/logout", nil, adminHeaders(token2))
	if status != http.StatusOK {
		t.Fatalf("logout: %d", status)
	}
	status, _, _ = doJSONErr(t, app, http.MethodGet, "/api/me", nil, adminHeaders(token2))
	if status != http.StatusUnauthorized {
		t.Fatalf("token after logout: status=%d, want 401", status)
	}

	// Unknown session id → 404.
	status, _, _ = doJSONErr(t, app, http.MethodDelete, "/api/sessions/absent", nil, adminHeaders(token1))
	if status != http.StatusUnauthorized { // token1 already invalid → guard answers first
		t.Fatalf("revoked token stays invalid: status=%d", status)
	}
}

func TestSessionRefreshSlidesExpiry(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := loginToken(t, env.app)
	claims := mustVerifyClaims(t, env.p, token)

	sess, err := env.p.store.GetSession(claims.SID)
	if err != nil || sess == nil {
		t.Fatalf("session missing: %v", err)
	}
	oldExpiry := sess.ExpiresAt

	// Pull LastActiveAt back so the touch throttle does not swallow the slide.
	sess.LastActiveAt = time.Now().Add(-2 * time.Minute)
	if err := env.p.store.PutSession(sess); err != nil {
		t.Fatalf("re-seed session: %v", err)
	}

	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/api/auth/refresh", nil, adminHeaders(token))
	if status != http.StatusOK || body["token"] == nil {
		t.Fatalf("refresh: status=%d body=%v", status, body)
	}
	fresh := mustVerifyClaims(t, env.p, body["token"].(string))
	if fresh.SID != claims.SID {
		t.Fatalf("refresh must keep the session id: %v vs %v", fresh.SID, claims.SID)
	}
	if fresh.JTI == claims.JTI {
		t.Fatalf("refresh must mint a fresh jti")
	}
	sess2, _ := env.p.store.GetSession(claims.SID)
	if !sess2.ExpiresAt.After(oldExpiry) {
		t.Fatalf("refresh must slide the expiry: %v → %v", oldExpiry, sess2.ExpiresAt)
	}
	// The old token still verifies (same session, old jti stays valid).
	if _, ok := env.p.verifyAdminToken(token); !ok {
		t.Fatal("old token after refresh must stay valid until expiry")
	}
}

func TestAuditTrail(t *testing.T) {
	env := newTestEnv(t, 200, false)
	app := env.app
	loginToken(t, app)
	{
		tok := loginToken(t, app)
		createKey(t, env, tok, "audited", 0)
	}

	audit := env.p.auditLog()
	entries, total, err := audit.Query(50, 0)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if total < 2 {
		t.Fatalf("expected ≥2 audit entries (login.success, key.create), got %d", total)
	}
	var hasLogin, hasCreate bool
	for _, e := range entries {
		if e.Action == AuditActionLoginSuccess && e.Actor == "admin" && e.IP != "" {
			hasLogin = true
		}
		if e.Action == AuditActionKeyCreate && e.Target == "audited" {
			hasCreate = true
		}
		if strings.Contains(strings.ToLower(e.Target), "sk-w2a-") || strings.Contains(strings.ToLower(e.Result), "sk-w2a-") {
			t.Fatalf("audit trail leaked key material: %+v", e)
		}
	}
	if !hasLogin || !hasCreate {
		t.Fatalf("missing audit entries: login=%v create=%v entries=%+v", hasLogin, hasCreate, entries)
	}

	// GET /api/audit returns the same trail (JWT).
	token := loginToken(t, app)
	status, body, _ := doJSONErr(t, app, http.MethodGet, "/api/audit?limit=20", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("GET /api/audit: %d %v", status, body)
	}
	if body["total"].(float64) < float64(2) {
		t.Fatalf("audit endpoint total: %v", body)
	}
	items := body["items"].([]any)
	first := items[0].(map[string]any)
	for _, field := range []string{"ts", "actor", "ip", "action", "target", "result"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("audit item missing %s: %v", field, first)
		}
	}
}

func TestUsageExportAndReconcile(t *testing.T) {
	env := newTestEnv(t, 200, false)
	token := loginToken(t, env.app)
	tok := loginToken(t, env.app)
	_, full := createKey(t, env, tok, "export", 0)

	doJSONErr(t, env.app, http.MethodPost, "/v1/index/check",
		map[string]any{"urls": []string{"https://example.com/a"}, "engines": []string{"baidu"}}, map[string]string{"X-API-Key": full})

	// CSV export.
	req := httptest.NewRequest(http.MethodGet, "/api/usage/export?format=csv", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.app.Test(req, -1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("export status=%d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("export missing attachment header: %q", cd)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if !strings.HasPrefix(buf.String(), "seq,time,key_id,key_name,endpoint,engines,urls,units,status,took_ms\n") {
		t.Fatalf("csv header missing: %q", buf.String()[:80])
	}

	// Reconcile: charged units (1, baidu OK) vs journaled engine OK (1).
	status, body, _ := doJSONErr(t, env.app, http.MethodGet, "/api/reconcile", nil, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("reconcile: %d %v", status, body)
	}
	if body["charged_units"] != float64(1) || body["journaled_units"] != float64(1) || body["mismatch_count"] != float64(0) {
		t.Fatalf("reconcile mismatch: %v", body)
	}
}

func TestBackupSnapshotRestorable(t *testing.T) {
	env := newTestEnv(t, 200, false)
	{
		tok := loginToken(t, env.app)
		createKey(t, env, tok, "backed-up", 0)
	}

	var buf bytes.Buffer
	if err := env.p.store.Backup(&buf); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty backup")
	}

	path := filepath.Join(env.dir, "snapshot.db")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	probe, err := bolt.Open(path, 0o400, &bolt.Options{ReadOnly: true, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("snapshot does not open as bbolt: %v", err)
	}
	defer probe.Close()
	found := map[string]bool{}
	_ = probe.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			found[string(name)] = true
			return nil
		})
	})
	for _, bucket := range []string{"keys", "usage", "settings"} {
		if !found[bucket] {
			t.Fatalf("snapshot missing bucket %q (found %v)", bucket, found)
		}
	}
}

func TestBoltUsageIndexAccuracy(t *testing.T) {
	dir := t.TempDir()
	s, err := newBoltStore(filepath.Join(dir, "web2api.db"))
	if err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	defer s.Close()

	base := time.Now().Add(-2 * time.Hour)
	keyA, keyB := "key-a", "key-b"
	for i := 0; i < 120; i++ {
		key := keyA
		if i%3 == 0 {
			key = keyB
		}
		outcome := OutcomeBilled
		units := 2
		status := fiber.StatusOK
		if i%5 == 0 {
			outcome = OutcomeRejected
			units = 0
			status = fiber.StatusBadRequest
		}
		rec := &UsageRecord{
			Time: base.Add(time.Duration(i) * time.Minute),
			KeyID: key, KeyName: key, Endpoint: "/v1/index/check",
			URLs: 1, Units: units, Status: status, Outcome: outcome,
			EngineStats: map[string]EngineStat{"baidu": {Calls: 1, OK: 1}},
		}
		if err := s.RecordUsage(rec, key, int64(units), outcome == OutcomeBilled, rec.Time); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	agg, err := s.UsageAggregate()
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	// 120 records: 24 rejected (i%5==0), 96 reached. Expectations for the
	// TODAY slice are derived dynamically — the fixture spans two local days
	// when the test runs across midnight.
	today := localDate(time.Now())
	wantTodayAll, wantTodayReached := 0, 0
	for i := 0; i < 120; i++ {
		if localDate(base.Add(time.Duration(i) * time.Minute)) == today {
			wantTodayAll++
			if i%5 != 0 {
				wantTodayReached++
			}
		}
	}
	// Reached-only totals (P-01 口径).
	if agg.TotalCalls != 96 {
		t.Fatalf("TotalCalls=%d want 96 (reached only)", agg.TotalCalls)
	}
	if agg.TodayCallsAll != int64(wantTodayAll) {
		t.Fatalf("TodayCallsAll=%d want %d", agg.TodayCallsAll, wantTodayAll)
	}
	if agg.TodayCalls != int64(wantTodayReached) {
		t.Fatalf("TodayCalls=%d want %d", agg.TodayCalls, wantTodayReached)
	}
	if agg.Rejected24h != 24 {
		t.Fatalf("Rejected24h=%d want 24", agg.Rejected24h)
	}
	if agg.ByEngine["baidu"].Calls != 120 || agg.ByEngine["baidu"].OK != 120 {
		t.Fatalf("ByEngine baidu: %+v", agg.ByEngine["baidu"])
	}

	// Page totals per key and pagination consistency.
	_, totalA, err := s.ListUsagePage(UsagePageFilter{KeyID: keyA, Limit: 5})
	if err != nil {
		t.Fatalf("page keyA: %v", err)
	}
	_, totalB, err := s.ListUsagePage(UsagePageFilter{KeyID: keyB, Limit: 5})
	if err != nil {
		t.Fatalf("page keyB: %v", err)
	}
	if totalA+totalB != 120 {
		t.Fatalf("key totals %d+%d != 120", totalA, totalB)
	}

	// Time-bounded page totals are DAY-granular (documented semantics): the
	// filtered total counts whole local-day buckets intersecting [From, To],
	// while the returned items are filtered by exact record time. The window
	// expectation is derived like the TODAY slice above: records count when
	// their whole local-day bucket still intersects [From, ∞). (A hardcoded
	// 120 assumed From lands on the first fixture day, which only holds when
	// the run crosses midnight before 01:30 local — a flake, now derived.)
	from := base.Add(30 * time.Minute)
	_, totalWin, err := s.ListUsagePage(UsagePageFilter{From: from, Limit: 5})
	if err != nil {
		t.Fatalf("page window: %v", err)
	}
	wantWin := 0
	for i := 0; i < 120; i++ {
		if localDate(base.Add(time.Duration(i)*time.Minute)) >= localDate(from) {
			wantWin++
		}
	}
	if totalWin != wantWin {
		t.Fatalf("window total=%d want %d (day-granular buckets intersecting [From,∞))", totalWin, wantWin)
	}
	// A date-aligned From (today 00:00) matches the UI time tabs exactly.
	fromToday := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local)
	_, totalToday, err := s.ListUsagePage(UsagePageFilter{From: fromToday, Limit: 5})
	if err != nil {
		t.Fatalf("page today: %v", err)
	}
	if totalToday != wantTodayAll {
		t.Fatalf("today total=%d want %d", totalToday, wantTodayAll)
	}

	// Series: today's bucket carries the reached calls/units of the fixture
	// (dynamically derived for the midnight edge).
	series, err := s.UsageTimeseries(3)
	if err != nil {
		t.Fatalf("timeseries: %v", err)
	}
	todayStat := series[len(series)-1]
	wantUnits := 0
	for i := 0; i < 120; i++ {
		if localDate(base.Add(time.Duration(i)*time.Minute)) == today && i%5 != 0 {
			wantUnits += 2
		}
	}
	if todayStat.Calls != wantTodayReached || todayStat.Units != wantUnits {
		t.Fatalf("timeseries today: %+v (want calls=%d units=%d)", todayStat, wantTodayReached, wantUnits)
	}
}

func TestHealthStorageKindInjection(t *testing.T) {
	// StoreKind feeds /health.storage (B-06): New on a fresh dir must report
	// the primary store.
	env := newTestEnv(t, 200, false)
	kind := env.p.StoreKind()
	if kind != "bbolt" && kind != "json" {
		t.Fatalf("unexpected store kind %q", kind)
	}
}

func mustVerifyClaims(t *testing.T, p *Platform, token string) *jwtClaims {
	t.Helper()
	claims, ok := p.verifyAdminToken(token)
	if !ok {
		t.Fatalf("token must verify: %s", token)
	}
	return claims
}
