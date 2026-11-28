package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/mcp"
	"golang.org/x/time/rate"
)

type consoleJobEngine struct{}

func (consoleJobEngine) Name() string                  { return "baidu" }
func (consoleJobEngine) IsInitialized() bool           { return true }
func (consoleJobEngine) GetRateLimiter() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }
func (consoleJobEngine) Search(context.Context, core.Query) ([]core.SearchResult, error) {
	return nil, nil
}
func (consoleJobEngine) CheckIndexed(_ context.Context, url string, _ core.Query) ([]core.SearchResult, error) {
	return []core.SearchResult{{Rank: 1, URL: url, Title: "self"}}, nil
}

func TestAPIKeyAsyncSubmissionsConsumeOneRateToken(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	env.app = fiber.New(fiber.Config{ErrorHandler: core.JSONErrorMiddleware()})
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = filepath.Join(t.TempDir(), "jobs")
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	env.p.RegisterAdmin(env.app, nil, srv)
	env.p.RegisterPublic(env.app, srv.IndexCheckHandler)
	if err := mcp.Register(env.app.Group("/web-tool"), env.p.AuthAny(), mcp.Deps{SubmitIndexJob: srv.SubmitIndexJob}); err != nil {
		t.Fatal(err)
	}
	sec := env.p.securityValue()
	sec.APIRatePerMinute = 1
	env.p.SetSecurity(sec)
	admin := login(t, env.app)

	for _, path := range []string{"/v1/index/check?async=1", "/web-tool/mcp"} {
		t.Run(path, func(t *testing.T) {
			keyID, key := createKey(t, env, admin, path, 100)
			urls := []string{"https://example.test/initial"}
			for i := 0; path == "/web-tool/mcp" && i < 20; i++ {
				urls = append(urls, fmt.Sprintf("https://example.test/%d", i))
			}
			args := map[string]any{"urls": urls, "engines": []string{"baidu"}}
			payload, expected := any(args), http.StatusAccepted
			if path == "/web-tool/mcp" {
				payload = map[string]any{
					"jsonrpc": "2.0", "id": 1, "method": "tools/call",
					"params": map[string]any{"name": "check_indexed", "arguments": args},
				}
				expected = http.StatusOK
			}
			status, body := doJSON(t, env.app, http.MethodPost, path, payload, keyHeaders(key))
			if status != expected {
				t.Fatalf("submit status=%d body=%v", status, body)
			}
			stored, err := env.p.store.GetKeyByID(keyID)
			if err != nil || stored == nil || stored.UsedTotal != int64(len(urls)) {
				t.Fatalf("async submission charge: key=%+v err=%v want %d units", stored, err, len(urls))
			}
			if env.p.allowKeyRate(keyID) {
				t.Fatal("successful submission should have consumed the last rate token")
			}
		})
	}
}

func TestScheduledBillingUsesStoredKeyWithoutInboundIP(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	keyID, _ := createKey(t, env, login(t, env.app), "scheduled-ip-key", 10)
	key, err := env.p.store.GetKeyByID(keyID)
	if err != nil || key == nil {
		t.Fatalf("load key: key=%+v err=%v", key, err)
	}
	key.AllowedIPs = []string{"203.0.113.8"}
	if err := env.p.store.PutKey(key); err != nil {
		t.Fatal(err)
	}
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = filepath.Join(t.TempDir(), "jobs")
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	env.p.engineControl = srv
	ctx := core.WithJobUser(core.WithJobOwner(context.Background(), keyID), "operator")
	urls, engines := []string{"https://example.test/a"}, []string{"baidu"}
	if _, _, err := env.p.enqueueBilled(ctx, "operator", keyID, "schedule-test-ip-plain-01", urls, engines, "", false); err == nil {
		t.Fatal("ordinary billed submission bypassed the key IP allowlist")
	} else if apiErr, ok := err.(*core.APIError); !ok || apiErr.HTTPStatus != http.StatusForbidden {
		t.Fatalf("ordinary submission error = %T %v, want 403", err, err)
	}
	if _, _, err := env.p.enqueueBilled(withScheduledBilling(ctx), "operator", keyID, "schedule-test-ip-cron-01", urls, engines, "", false); err != nil {
		t.Fatalf("authorized internal schedule was rejected by inbound IP policy: %v", err)
	}
}

func TestConsoleJobSubmissionChargesOnce(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	env.app = fiber.New(fiber.Config{ErrorHandler: core.JSONErrorMiddleware()})
	env.p.RegisterAdmin(env.app, nil, nil)
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = filepath.Join(t.TempDir(), "jobs")
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	env.p.engineControl = srv
	env.p.RegisterConsoleJobs(env.app, IndexJobHandlers{List: srv.IndexJobListHandler, Status: srv.IndexJobStatusHandler, Results: srv.IndexJobResultsHandler, Cancel: srv.IndexJobCancelHandler})
	admin := login(t, env.app)
	keyID, _ := createKey(t, env, admin, "jobs", 1)
	headers := adminHeaders(admin)
	headers["Idempotency-Key"] = "test-unique-request-0001"
	body := map[string]any{"key_id": keyID, "urls": []string{"https://example.test/a"}, "engines": []string{"baidu"}}
	status, first := doJSON(t, env.app, http.MethodPost, "/api/index/check/jobs", body, headers)
	if status != http.StatusAccepted || first["job_id"] == "" {
		t.Fatalf("submit status=%d body=%v", status, first)
	}
	status, replay := doJSON(t, env.app, http.MethodPost, "/api/index/check/jobs", body, headers)
	if status != http.StatusAccepted || replay["job_id"] != first["job_id"] || replay["replayed"] != true {
		t.Fatalf("replay status=%d body=%v", status, replay)
	}
	key, err := env.p.store.GetKeyByID(keyID)
	if err != nil || key.UsedTotal != 1 {
		t.Fatalf("charged key=%+v err=%v", key, err)
	}
	body["urls"] = []string{"https://example.test/b"}
	status, conflict := doJSON(t, env.app, http.MethodPost, "/api/index/check/jobs", body, headers)
	if status != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%v", status, conflict)
	}
	headers["Idempotency-Key"] = "test-unique-request-0002"
	status, quota := doJSON(t, env.app, http.MethodPost, "/api/index/check/jobs", body, headers)
	if status != http.StatusTooManyRequests {
		t.Fatalf("quota status=%d body=%v", status, quota)
	}
	if pending := srv.PendingBilledIndexJobs(); len(pending) != 0 {
		t.Fatalf("quota failure left preparations: %+v", pending)
	}
}

func TestPublicAsyncJobRejectsTooManyURLsWithContractReason(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	env.app = fiber.New(fiber.Config{ErrorHandler: core.JSONErrorMiddleware()})
	env.p.RegisterAdmin(env.app, nil, nil)
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = filepath.Join(t.TempDir(), "jobs")
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	env.p.engineControl = srv
	env.p.RegisterPublic(env.app, srv.IndexCheckHandler)
	admin := login(t, env.app)
	keyID, key := createKey(t, env, admin, "too-many", 100_000)
	urls := make([]string, maxBilledIndexJobURLs+1)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.test/%d", i)
	}
	status, body, _ := doJSONErr(t, env.app, http.MethodPost, "/v1/index/check", map[string]any{"urls": urls}, keyHeaders(key))
	if status != http.StatusBadRequest || body["reason"] != "TOO_MANY_URLS" {
		t.Fatalf("too many URLs: status=%d body=%v", status, body)
	}
	stored, err := env.p.store.GetKeyByID(keyID)
	if err != nil || stored == nil || stored.UsedTotal != 0 {
		t.Fatalf("invalid oversized batch must not be billed: key=%+v err=%v", stored, err)
	}
}

func TestConsoleJobRetryIsScopedAndBilledIdempotently(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	env.app = fiber.New(fiber.Config{ErrorHandler: core.JSONErrorMiddleware()})
	env.p.RegisterAdmin(env.app, nil, nil)
	admin := login(t, env.app)
	keyID, _ := createKey(t, env, admin, "retry", 10)
	ownerID := createConsoleUser(t, env.app, admin, "retry_owner", RoleOperator)
	owner := loginAs(t, env.app, "retry_owner", "pw-123456")
	createConsoleUser(t, env.app, admin, "retry_other", RoleOperator)
	other := loginAs(t, env.app, "retry_other", "pw-123456")
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = filepath.Join(t.TempDir(), "jobs")
	store := core.NewIndexJobStore(opts.IndexJobDir)
	created, finished := time.Now().Add(-time.Minute), time.Now()
	source := &core.IndexJob{ID: "job_retry_source", Status: core.IndexJobDone, CreatedAt: created, UpdatedAt: finished, FinishedAt: &finished, OwnerKeyID: keyID, OwnerUserID: ownerID, Engines: []string{"baidu"}, Total: 1, Completed: 1, Failed: 1, URLs: []string{"https://example.test/fail"}}
	if err := store.SaveMeta(source); err != nil {
		t.Fatal(err)
	}
	var row core.IndexCheckURLResult
	if err := json.Unmarshal([]byte(`{"url":"https://example.test/fail","engines":{"baidu":{"error":{"code":"request_timeout","message":"timeout"}}}}`), &row); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendResult(source.ID, row); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadResults(source.ID)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("load failed source result: rows=%d err=%v", len(loaded), err)
	}
	if _, failure := loaded[0].Engines["baidu"].Verdict(); failure == nil {
		t.Fatalf("persisted result is not classified as failed: %+v", loaded[0])
	}
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	env.p.engineControl = srv
	env.p.RegisterConsoleJobs(env.app, IndexJobHandlers{List: srv.IndexJobListHandler, Status: srv.IndexJobStatusHandler, Results: srv.IndexJobResultsHandler, Cancel: srv.IndexJobCancelHandler, Retry: srv.IndexJobRetryHandler})
	adminHeadersForProbe := adminHeaders(admin)
	probeStatus, probeBody := doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs/"+source.ID, nil, adminHeadersForProbe)
	if probeStatus != http.StatusOK {
		t.Fatalf("admin source lookup: status=%d body=%v", probeStatus, probeBody)
	}
	ownerProbe := adminHeaders(owner)
	probeStatus, probeBody = doJSON(t, env.app, http.MethodGet, "/api/index/check/jobs/"+source.ID, nil, ownerProbe)
	if probeStatus != http.StatusOK {
		t.Fatalf("owner source lookup: status=%d body=%v", probeStatus, probeBody)
	}
	headers := adminHeaders(owner)
	headers["Idempotency-Key"] = "retry-result-request-0001"
	path := "/api/index/check/jobs/" + source.ID + "/retry"
	status, first := doJSON(t, env.app, http.MethodPost, path, nil, headers)
	if status != http.StatusAccepted {
		t.Fatalf("retry: status=%d body=%v", status, first)
	}
	status, replay := doJSON(t, env.app, http.MethodPost, path, nil, headers)
	if status != http.StatusAccepted || replay["job_id"] != first["job_id"] {
		t.Fatalf("retry replay: status=%d body=%v", status, replay)
	}
	key, err := env.p.store.GetKeyByID(keyID)
	if err != nil || key.UsedTotal != 1 {
		t.Fatalf("retry should charge once: key=%+v err=%v", key, err)
	}
	status, _ = doJSON(t, env.app, http.MethodPost, path, nil, adminHeaders(other))
	if status != http.StatusNotFound {
		t.Fatalf("cross-user retry status=%d want=404", status)
	}
}
func TestBilledJobRecoveryAcrossRestart(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = filepath.Join(t.TempDir(), "jobs")
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	ctx := core.WithJobUser(core.WithJobOwner(context.Background(), "recovery-key"), "alice")
	if err := env.p.store.PutKey(testKey("recovery-key", "recovery", 5, time.Now())); err != nil {
		t.Fatal(err)
	}
	paid, err := srv.PrepareBilledIndexJob(ctx, []string{"https://example.test/a"}, []string{"baidu"}, "job_paid", "paid-marker", "paid-hash", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.PrepareBilledIndexJob(ctx, []string{"https://example.test/b"}, []string{"baidu"}, "job_unpaid", "unpaid-marker", "unpaid-hash", 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rec := &UsageRecord{Time: now, KeyID: "recovery-key", Units: 1, Status: http.StatusAccepted}
	charge := jobCharge{JobID: paid.ID, Fingerprint: "paid-hash", KeyID: "recovery-key", Units: 1}
	if _, _, err := env.p.store.(jobChargeStore).CommitJobCharge("paid-marker", charge, rec, "recovery-key", 1, now); err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = reopened.Shutdown() })
	if err := env.p.ReconcileBilledJobs(reopened); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ExistingBilledIndexJob("job_paid", "paid-marker", "paid-hash"); err != nil {
		t.Fatalf("paid task disappeared: %v", err)
	}
	if _, err := reopened.ExistingBilledIndexJob("job_unpaid", "unpaid-marker", "unpaid-hash"); err == nil {
		t.Fatal("unpaid preparation was not removed")
	}
	key, err := env.p.store.GetKeyByID("recovery-key")
	if err != nil || key.UsedTotal != 1 {
		t.Fatalf("recovered charge=%+v err=%v", key, err)
	}
}
