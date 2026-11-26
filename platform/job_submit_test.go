package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
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
