package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitForJob polls until the job reaches a terminal status.
func waitForJob(t *testing.T, m *IndexJobManager, id string, timeout time.Duration) *IndexJob {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, ok := m.Get(id, "")
		if ok && job.Status.Terminal() {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := m.Get(id, "")
	if job != nil {
		t.Fatalf("job %s did not finish, status=%s completed=%d/%d", id, job.Status, job.Completed, job.Total)
	}
	t.Fatalf("job %s disappeared", id)
	return nil
}

func newTestJobManager(t *testing.T, dir string, engines []SearchEngine) (*IndexJobManager, *ResilientSearcher) {
	t.Helper()
	cfg := DefaultResilientConfig()
	cfg.Retry = RetryConfig{MaxRetries: 0, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, BackoffFactor: 1}
	rs := NewResilientSearcher(engines, cfg)
	m := NewIndexJobManager(dir, engines, rs, IndexJobOptions{Chunk: 2})
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	return m, rs
}

func TestIndexJobSubmitValidation(t *testing.T) {
	eng := &indexEngineMock{name: "baidu", initialized: true}
	m, _ := newTestJobManager(t, t.TempDir(), []SearchEngine{eng})

	if _, apiErr := m.Submit(context.Background(), nil, nil); apiErr == nil || apiErr.Reason != "EMPTY_QUERY" {
		t.Fatalf("empty batch: got %+v", apiErr)
	}
	if _, apiErr := m.Submit(context.Background(), []string{"not a url"}, nil); apiErr == nil || apiErr.Reason != "INVALID_PARAM" {
		t.Fatalf("invalid url: got %+v", apiErr)
	}
	if _, apiErr := m.Submit(t.Context(), []string{"https://example.test/a"}, []string{"invalid-engine"}); apiErr == nil || apiErr.Reason != "INVALID_PARAM" {
		t.Fatalf("invalid engine: got %+v", apiErr)
	}

	tooMany := make([]string, DefaultIndexJobMaxURLs+1)
	for i := range tooMany {
		tooMany[i] = "https://example.test/" + time.Duration(i).String()
	}
	if _, apiErr := m.Submit(context.Background(), tooMany, nil); apiErr == nil || apiErr.Reason != "TOO_MANY_URLS" {
		t.Fatalf("oversized batch: got %+v", apiErr)
	}

	// Duplicates collapse so the same page is never queried twice.
	job, apiErr := m.Submit(context.Background(), []string{"https://example.test/a", " https://example.test/a ", "https://example.test/b"}, nil)
	if apiErr != nil {
		t.Fatalf("submit: %+v", apiErr)
	}
	if job.Total != 2 {
		t.Fatalf("duplicate URLs must collapse: total=%d", job.Total)
	}
	if len(job.URLs) != 0 {
		t.Fatalf("the accepted response must not echo the URL list: %v", job.URLs)
	}
}

func TestIndexJobQueueEnforcesAggregateURLBudgetAndReleasesIt(t *testing.T) {
	eng := &indexEngineMock{name: "baidu", initialized: true}
	m := NewIndexJobManager(t.TempDir(), []SearchEngine{eng}, nil, IndexJobOptions{
		MaxURLs: 10, MaxQueuedURLs: 2, MaxQueuedURLBytes: 256,
	})
	first, apiErr := m.Submit(context.Background(), []string{"https://example.test/a", "https://example.test/b"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatalf("first submit: %+v", apiErr)
	}
	budget := m.URLBudget()
	if budget.InUseURLs != 2 || budget.InUseURLBytes != int64(len("https://example.test/a")+len("https://example.test/b")) {
		t.Fatalf("wrong resident URL budget after submit: %+v", budget)
	}
	if _, apiErr = m.Submit(context.Background(), []string{"https://example.test/c"}, []string{"baidu"}); apiErr == nil || apiErr.HTTPStatus != 503 || apiErr.ErrorCode != "job_queue_full" {
		t.Fatalf("aggregate URL budget not enforced: %+v", apiErr)
	}
	if !m.Cancel(first.ID, "") {
		t.Fatal("cancel first queued task failed")
	}
	if budget := m.URLBudget(); budget.InUseURLs != 0 || budget.InUseURLBytes != 0 {
		t.Fatalf("cancel did not release URL budget: %+v", budget)
	}
	if _, apiErr = m.Submit(context.Background(), []string{"https://example.test/c"}, []string{"baidu"}); apiErr != nil {
		t.Fatalf("budget not released after cancel: %+v", apiErr)
	}
}

func TestIndexJobRecoveryReservesBudgetAndLoadsPayloadLazily(t *testing.T) {
	dir := t.TempDir()
	eng := &indexEngineMock{name: "baidu", initialized: true}
	opts := IndexJobOptions{MaxURLs: 5, MaxQueuedURLs: 2, MaxQueuedURLBytes: 256}
	beforeRestart := NewIndexJobManager(dir, []SearchEngine{eng}, nil, opts)
	job, apiErr := beforeRestart.Submit(context.Background(), []string{"https://example.test/a", "https://example.test/b"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatalf("submit before restart: %+v", apiErr)
	}
	afterRestart := NewIndexJobManager(dir, []SearchEngine{eng}, nil, opts)
	recovered := afterRestart.resume(context.Background())
	if len(recovered) != 1 || recovered[0] != job.ID {
		t.Fatalf("recovered IDs = %v", recovered)
	}
	state := afterRestart.jobs[job.ID]
	state.mu.Lock()
	lazy, payloadLoaded := state.lazyLoad, state.job.URLs != nil || state.pending != nil
	state.mu.Unlock()
	if !lazy || payloadLoaded {
		t.Fatalf("recovery eagerly loaded URL payload: lazy=%v payloadLoaded=%v", lazy, payloadLoaded)
	}
	if got := afterRestart.URLBudget(); got.InUseURLs != 2 {
		t.Fatalf("recovered backlog not reserved against budget: %+v", got)
	}
	if _, apiErr := afterRestart.Submit(context.Background(), []string{"https://example.test/c"}, []string{"baidu"}); apiErr == nil || apiErr.HTTPStatus != 503 {
		t.Fatalf("new submit exceeded recovered budget: %+v", apiErr)
	}
	if err := afterRestart.hydrateRestoredJob(state); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	lazy, pendingCount := state.lazyLoad, len(state.pending)
	state.mu.Unlock()
	if lazy || pendingCount != 2 {
		t.Fatalf("restored job hydration: lazy=%v pending=%d", lazy, pendingCount)
	}
	if !afterRestart.Cancel(job.ID, "") {
		t.Fatal("cancel restored task failed")
	}
	if got := afterRestart.URLBudget(); got.InUseURLs != 0 || got.InUseURLBytes != 0 {
		t.Fatalf("restored task cancellation did not release reservation: %+v", got)
	}
}

func TestIndexJobRejectsOverlongURL(t *testing.T) {
	eng := &indexEngineMock{name: "baidu", initialized: true}
	m := NewIndexJobManager(t.TempDir(), []SearchEngine{eng}, nil, IndexJobOptions{})
	raw := "https://example.test/" + strings.Repeat("a", DefaultIndexJobMaxURLBytes)
	if _, apiErr := m.Submit(context.Background(), []string{raw}, []string{"baidu"}); apiErr == nil || apiErr.Reason != "INVALID_PARAM" {
		t.Fatalf("overlong URL accepted: %+v", apiErr)
	}
}

func TestIndexJobRunsToDone(t *testing.T) {
	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, targetURL string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	m, _ := newTestJobManager(t, t.TempDir(), []SearchEngine{eng})

	job, apiErr := m.Submit(context.Background(), []string{
		"https://example.test/1", "https://example.test/2", "https://example.test/3",
	}, []string{"baidu"})
	if apiErr != nil {
		t.Fatalf("submit: %+v", apiErr)
	}

	done := waitForJob(t, m, job.ID, 5*time.Second)
	if done.Status != IndexJobDone {
		t.Fatalf("status = %s, want done (last_error=%q)", done.Status, done.LastError)
	}
	if done.Completed != 3 || done.Indexed != 3 || done.Failed != 0 {
		t.Fatalf("counters = completed %d indexed %d failed %d", done.Completed, done.Indexed, done.Failed)
	}
	if done.Progress() != 1 {
		t.Fatalf("progress = %v", done.Progress())
	}

	rows, err := m.Results(job.ID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("persisted rows = %d, want 3", len(rows))
	}
	success, engineErr := rows[0].Engines["baidu"].Verdict()
	if engineErr != nil || success == nil || !success.Indexed {
		t.Fatalf("row verdict = %+v", rows[0].Engines["baidu"])
	}
}

func TestIndexJobResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	urls := []string{"https://example.test/1", "https://example.test/2", "https://example.test/3"}

	// Simulate a process that died mid-job: metadata says running, one row is
	// already on disk.
	store := NewIndexJobStore(dir)
	created := time.Now().UTC().Add(-time.Minute)
	job := IndexJob{
		ID: "job_restart_case", Status: IndexJobRunning, CreatedAt: created, UpdatedAt: created,
		Engines: []string{"baidu"}, Total: 3, URLs: urls,
	}
	if err := store.SaveMeta(&job); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	first := IndexCheckURLResult{URL: urls[0], Engines: map[string]IndexCheckEngineOutcome{
		"baidu": {success: &IndexCheckSuccess{Indexed: true, Match: IndexMatchExact, MatchedResultURL: urls[0]}},
	}}
	if err := store.AppendResult(job.ID, first); err != nil {
		t.Fatalf("append result: %v", err)
	}
	// Crash between the result append and metadata save, followed by a torn row.
	f, err := os.OpenFile(filepath.Join(dir, job.ID, "results.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString("{\"url\":")
	if err := f.Close(); err != nil || writeErr != nil {
		t.Fatalf("write torn result: %v, close: %v", writeErr, err)
	}

	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, targetURL string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	m, _ := newTestJobManager(t, dir, []SearchEngine{eng})

	done := waitForJob(t, m, job.ID, 5*time.Second)
	if done.Completed != 3 || done.Indexed != 3 {
		t.Fatalf("resumed job completed %d of %d, indexed %d", done.Completed, done.Total, done.Indexed)
	}
	rows, _ := m.Results(job.ID)
	if len(rows) != 3 {
		t.Fatalf("rows after resume = %d, want 3", len(rows))
	}
	// The already-finished URL must not be queried again.
	for _, called := range eng.calls() {
		if called == urls[0] {
			t.Fatalf("resumed job re-queried the completed URL %s", called)
		}
	}
}

func TestIndexJobRetainsEngineSelectionAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store := NewIndexJobStore(dir)
	job := IndexJob{ID: "job_missing_engine", Status: IndexJobQueued, Engines: []string{"sogou"},
		URLs: []string{"https://example.test/a"}, Total: 1}
	if err := store.SaveMeta(&job); err != nil {
		t.Fatal(err)
	}
	fallback := &indexEngineMock{name: "baidu", initialized: true}
	m, _ := newTestJobManager(t, dir, []SearchEngine{fallback})
	terminal := waitForJob(t, m, job.ID, 5*time.Second)
	if terminal.Status != IndexJobFailed || terminal.Completed != 0 || len(fallback.calls()) != 0 {
		t.Fatalf("job switched engines after restart: %+v", terminal)
	}
}

func TestIndexJobRestoresBacklogBeyondQueueCapacity(t *testing.T) {
	m := NewIndexJobManager(t.TempDir(), nil, nil, IndexJobOptions{})
	m.queue = make(chan string, 1)
	for i := 0; i < 2; i++ {
		job := &IndexJob{ID: fmt.Sprintf("job_backlog_%d", i), Status: IndexJobQueued}
		if err := m.store.SaveMeta(job); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	go func() {
		m.Start(ctx)
		close(started)
	}()
	select {
	case <-started:
		t.Cleanup(m.Stop)
	case <-time.After(5 * time.Second):
		t.Fatal("job manager startup blocked on its recovery backlog")
	}
	for i := 0; i < 2; i++ {
		if got := waitForJob(t, m, fmt.Sprintf("job_backlog_%d", i), 5*time.Second); got.Status != IndexJobDone {
			t.Fatalf("restored job = %+v", got)
		}
	}
}

func TestIndexJobFullQueueRejectsWithoutPersisting(t *testing.T) {
	m := NewIndexJobManager(t.TempDir(), nil, nil, IndexJobOptions{})
	m.queue = make(chan string, 1)
	urls := []string{"https://example.test/1"}
	if _, err := m.Submit(t.Context(), urls, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(t.Context(), urls, nil); err == nil || err.Reason != "JOB_QUEUE_FULL" {
		t.Fatalf("full queue returned %v", err)
	}
	stored, err := m.store.ListMeta()
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored jobs = %d, err = %v", len(stored), err)
	}
}

func TestIndexJobShutdownLeavesInFlightJobResumable(t *testing.T) {
	started := make(chan struct{})
	eng := &indexEngineMock{name: "baidu", initialized: true,
		serpFn: func(ctx context.Context, _ string, _ Query) ([]SearchResult, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	m, _ := newTestJobManager(t, t.TempDir(), []SearchEngine{eng})
	job, apiErr := m.Submit(t.Context(), []string{"https://example.test/1"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not reach the engine")
	}
	m.Stop()
	stored, err := m.store.LoadMeta(job.ID)
	if err != nil || stored.Status != IndexJobQueued || stored.Completed != 0 {
		t.Fatalf("interrupted job = %+v, err = %v", stored, err)
	}
}

func TestIndexJobFailsWhenResultCannotBePersisted(t *testing.T) {
	urls := []string{"https://example.test/1"}
	eng := &indexEngineMock{name: "baidu", initialized: true,
		serpFn: func(_ context.Context, target string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: target}}, nil
		},
	}
	cfg := DefaultResilientConfig()
	rs := NewResilientSearcher([]SearchEngine{eng}, cfg)
	m := NewIndexJobManager(t.TempDir(), []SearchEngine{eng}, rs, IndexJobOptions{})
	job, apiErr := m.Submit(t.Context(), urls, []string{"baidu"})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	// A directory at the result path rejects writes while metadata stays writable.
	if err := os.Mkdir(filepath.Join(m.store.jobDir(job.ID), "results.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	m.runJob(t.Context(), job.ID)
	got, err := m.store.LoadMeta(job.ID)
	if err != nil || got.Status != IndexJobFailed || got.Completed != 0 || got.LastError == "" {
		t.Fatalf("failed job = %+v, err = %v", got, err)
	}
}

func TestIndexJobCancelRequiresPersistedTransition(t *testing.T) {
	m := NewIndexJobManager(t.TempDir(), nil, nil, IndexJobOptions{})
	ctx := WithJobOwner(t.Context(), "owner")
	job, apiErr := m.Submit(ctx, []string{"https://example.test/1"}, nil)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	blocked := filepath.Join(m.store.jobDir(job.ID), "job.json.tmp")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.cancelJob(job.ID, "owner"); ok || err == nil {
		t.Fatalf("failed cancellation: ok=%v err=%v", ok, err)
	}
	current, ok := m.Get(job.ID, "owner")
	if !ok || current.Status != IndexJobQueued {
		t.Fatalf("job changed after failed persistence: %+v", current)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.cancelJob(job.ID, "owner"); !ok || err != nil {
		t.Fatalf("retry cancellation: ok=%v err=%v", ok, err)
	}
	stored, err := m.store.LoadMeta(job.ID)
	if err != nil || stored.Status != IndexJobCancelled {
		t.Fatalf("persisted job = %+v, err = %v", stored, err)
	}
}

func TestIndexJobCancel(t *testing.T) {
	block := make(chan struct{})
	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(ctx context.Context, _ string, _ Query) ([]SearchResult, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-block:
				return nil, ctx.Err()
			}
		},
	}
	m, _ := newTestJobManager(t, t.TempDir(), []SearchEngine{eng})
	t.Cleanup(func() { close(block) })

	job, apiErr := m.Submit(context.Background(), []string{"https://example.test/1", "https://example.test/2"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatalf("submit: %+v", apiErr)
	}
	time.Sleep(50 * time.Millisecond)
	if !m.Cancel(job.ID, "") {
		t.Fatal("cancel reported no running job")
	}
	done := waitForJob(t, m, job.ID, 5*time.Second)
	if done.Status != IndexJobCancelled {
		t.Fatalf("status = %s, want cancelled", done.Status)
	}
	if m.Cancel(job.ID, "") {
		t.Fatal("cancelling a terminal job must report false")
	}
}

func TestIndexJobRetryClassification(t *testing.T) {
	m := &IndexJobManager{opts: IndexJobOptions{}.normalized()}
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"cooldown", "engine_cooldown", true},
		{"captcha", "captcha_detected", true},
		{"timeout", "request_timeout", true},
		{"blocked", "blocked", true},
		{"circuit", "circuit_open", true},
		{"not implemented", "not_implemented", false},
		{"invalid param", "INVALID_PARAM", false},
	}
	for _, tc := range cases {
		row := IndexCheckURLResult{URL: "https://example.test/x", Engines: map[string]IndexCheckEngineOutcome{
			"baidu": {err: &IndexCheckEngineError{Code: tc.code}},
		}}
		if got := m.shouldRetry(row); got != tc.want {
			t.Errorf("%s: shouldRetry(%s) = %v, want %v", tc.name, tc.code, got, tc.want)
		}
	}
	// Any real verdict ends the retry loop.
	withVerdict := IndexCheckURLResult{URL: "https://example.test/x", Engines: map[string]IndexCheckEngineOutcome{
		"baidu": {success: &IndexCheckSuccess{Indexed: false, Match: IndexMatchNone}},
	}}
	if m.shouldRetry(withVerdict) {
		t.Error("a produced verdict must not be retried")
	}
}

func TestIndexJobCSV(t *testing.T) {
	rows := []IndexCheckURLResult{
		{URL: "https://example.test/1", Engines: map[string]IndexCheckEngineOutcome{
			"baidu": {success: &IndexCheckSuccess{Indexed: true, Match: IndexMatchExact, MatchedResultURL: "https://example.test/1", TotalResults: 9}},
		}},
		{URL: "https://example.test/2,quoted", Engines: map[string]IndexCheckEngineOutcome{
			"baidu": {err: &IndexCheckEngineError{Code: "captcha_detected"}},
		}},
	}
	out := string(WriteCSV(rows, []string{"baidu"}))
	if !strings.HasPrefix(out, "\ufeff") {
		t.Fatal("CSV must start with a UTF-8 BOM for Excel")
	}
	lines := strings.Split(strings.TrimSpace(out), "\r\n")
	if len(lines) != 3 {
		t.Fatalf("CSV lines = %d, want header + 2 rows: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "baidu_indexed") {
		t.Errorf("header missing engine columns: %q", lines[0])
	}
	if !strings.Contains(lines[1], "true,exact,https://example.test/1,9,") {
		t.Errorf("indexed row = %q", lines[1])
	}
	if !strings.Contains(lines[2], "\"https://example.test/2,quoted\"") {
		t.Errorf("comma in URL must be quoted: %q", lines[2])
	}
	if !strings.Contains(lines[2], "captcha_detected") {
		t.Errorf("error row must carry the code: %q", lines[2])
	}
}

func TestSpreadsheetCSVCellPreservesLiteralText(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"sample", "sample"},
		{"=sample", "'=sample"},
		{"  +sample", "'  +sample"},
		{"＠sample", "'＠sample"},
		{"\tsample", "'\tsample"},
		{"a,b", "\"a,b\""},
		{"", ""},
	} {
		if got := SpreadsheetCSVCell(tc.input); got != tc.want {
			t.Errorf("SpreadsheetCSVCell(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// Jobs are tenant-scoped: another API key must not read, list or cancel them.
// Regression for the audit finding where any key could cancel any batch.
func TestIndexJobIsScopedToOwner(t *testing.T) {
	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, targetURL string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	m, _ := newTestJobManager(t, t.TempDir(), []SearchEngine{eng})

	ownerA := WithJobOwner(context.Background(), "key_owner_a")
	job, apiErr := m.Submit(ownerA, []string{"https://example.test/tenant-a"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatalf("submit: %+v", apiErr)
	}
	if job.OwnerKeyID != "key_owner_a" {
		t.Fatalf("owner not recorded: %q", job.OwnerKeyID)
	}
	waitForJob(t, m, job.ID, 5*time.Second)

	if _, ok := m.Get(job.ID, "key_owner_a"); !ok {
		t.Fatal("the owning key must see its own job")
	}
	if _, ok := m.Get(job.ID, "key_owner_b"); ok {
		t.Fatal("a foreign key must not see the job (reports not-found)")
	}
	if m.Cancel(job.ID, "key_owner_b") {
		t.Fatal("a foreign key must not cancel the job")
	}
	if visible := m.List("key_owner_b", 10); len(visible) != 0 {
		t.Fatalf("foreign key listed %d jobs", len(visible))
	}
	if visible := m.List("key_owner_a", 10); len(visible) != 1 {
		t.Fatalf("owning key listed %d jobs, want 1", len(visible))
	}

	// Admin (empty owner) keeps full visibility.
	if _, ok := m.Get(job.ID, ""); !ok {
		t.Fatal("admin must see every job")
	}
	if visible := m.List("", 10); len(visible) != 1 {
		t.Fatalf("admin listed %d jobs, want 1", len(visible))
	}
}

func TestConsoleJobOwnershipIsIndependentOfBillingKey(t *testing.T) {
	eng := &indexEngineMock{name: "baidu", initialized: true}
	m := NewIndexJobManager(t.TempDir(), []SearchEngine{eng}, nil, IndexJobOptions{})
	ctx := WithJobUser(WithJobOwner(context.Background(), "key_a"), "user_a")
	job, apiErr := m.Submit(ctx, []string{"https://example.test/a"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if job.OwnerKeyID != "key_a" || job.OwnerUserID != "user_a" {
		t.Fatalf("owners not persisted: %+v", job)
	}
	if _, ok := m.Get(job.ID, "key_a"); ok {
		t.Fatal("billing key must not expose console-owned job")
	}
	if _, ok := m.GetForUser(job.ID, "user_b", false); ok {
		t.Fatal("other user can read job")
	}
	if _, ok := m.GetForUser(job.ID, "", false); ok {
		t.Fatal("empty user can read job")
	}
	if _, ok := m.GetForUser(job.ID, "user_a", false); !ok {
		t.Fatal("submitting user cannot read job")
	}
	if _, ok := m.GetForUser(job.ID, "admin", true); !ok {
		t.Fatal("admin cannot read job")
	}
	if got := m.ListForUser("user_b", false, 10); len(got) != 0 {
		t.Fatalf("other user listed %d jobs", len(got))
	}
	if cancelled, err := m.CancelForUser(job.ID, "user_b", false); err != nil || cancelled {
		t.Fatalf("other user cancelled job: cancelled=%v err=%v", cancelled, err)
	}
	if cancelled, err := m.CancelForUser(job.ID, "user_a", false); err != nil || !cancelled {
		t.Fatalf("owner cannot cancel job: cancelled=%v err=%v", cancelled, err)
	}
	// Eviction and process restart must preserve the same isolation.
	reopened := NewIndexJobManager(m.store.Dir(), []SearchEngine{eng}, nil, IndexJobOptions{})
	if _, ok := reopened.GetForUser(job.ID, "user_a", false); !ok {
		t.Fatal("stored job lost its console owner")
	}
	if _, ok := reopened.Get(job.ID, "key_a"); ok {
		t.Fatal("stored console job became visible to billing key")
	}
}

// A finished job must stop holding its submitted URL list in memory, while
// staying readable (the record is on disk).
func TestIndexJobReleasesMemoryWhenDone(t *testing.T) {
	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, targetURL string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	m, _ := newTestJobManager(t, t.TempDir(), []SearchEngine{eng})
	job, apiErr := m.Submit(context.Background(), []string{"https://example.test/mem"}, []string{"baidu"})
	if apiErr != nil {
		t.Fatalf("submit: %+v", apiErr)
	}
	waitForJob(t, m, job.ID, 5*time.Second)

	m.mu.Lock()
	st, ok := m.jobs[job.ID]
	m.mu.Unlock()
	if !ok {
		t.Skip("job already evicted; nothing resident to inspect")
	}
	st.mu.Lock()
	urls, pending := st.job.URLs, st.pending
	st.mu.Unlock()
	if urls != nil {
		t.Fatalf("finished job still holds %d URLs in memory", len(urls))
	}
	if pending != nil {
		t.Fatalf("finished job still holds %d pending entries", len(pending))
	}
	// Still readable after the release.
	if _, ok := m.Get(job.ID, ""); !ok {
		t.Fatal("released job must stay readable")
	}
	rows, err := m.Results(job.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("results after release: %v len=%d", err, len(rows))
	}
}

// One URL per progress write is a UX contract, not a tuning knob: a larger
// default makes the job counters jump in two-minute steps and a fresh job looks
// stuck at 0/N. Regression guard for audit finding F-21.
func TestIndexJobProgressChunkDefaultsToPerURL(t *testing.T) {
	if got := (IndexJobOptions{}).normalized().Chunk; got != 1 {
		t.Fatalf("default chunk = %d, want 1 (per-URL progress)", got)
	}
	if got := (IndexJobOptions{Chunk: 10}).normalized().Chunk; got != 10 {
		t.Fatalf("explicit chunk must be kept, got %d", got)
	}
}

func TestIndexJobProgressCountsFailedRowsOnce(t *testing.T) {
	job := IndexJob{Total: 10, Completed: 4, Failed: 2}
	if got := job.Progress(); got != 0.4 {
		t.Fatalf("progress = %v, want 0.4", got)
	}
	if got := job.ETA(10 * time.Second); got != 60*time.Second {
		t.Fatalf("ETA = %v, want 60s", got)
	}
}

func TestPreparedBillingJobWaitsAcrossRestart(t *testing.T) {
	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, target string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: target, Title: "self"}}, nil
		},
	}
	dir := t.TempDir()
	rs := NewResilientSearcher([]SearchEngine{eng}, DefaultResilientConfig())
	m := NewIndexJobManager(dir, []SearchEngine{eng}, rs, IndexJobOptions{})
	m.Start(context.Background())
	ctx := WithJobUser(WithJobOwner(context.Background(), "key_a"), "alice")
	job, apiErr := m.PrepareBilled(ctx, []string{"https://example.test/a"}, []string{"baidu"}, "job_staged", "marker", "fingerprint", 1)
	if apiErr != nil || job.Status != IndexJobBillingPending {
		t.Fatalf("prepare: job=%+v error=%v", job, apiErr)
	}
	m.Stop()
	if len(eng.calls()) != 0 {
		t.Fatal("unbilled task ran before restart")
	}
	reopened := NewIndexJobManager(dir, []SearchEngine{eng}, rs, IndexJobOptions{})
	reopened.Start(context.Background())
	defer reopened.Stop()
	retry, apiErr := reopened.PrepareBilled(ctx, []string{"https://example.test/a"}, []string{"baidu"}, "job_staged", "marker", "fingerprint", 1)
	if apiErr != nil || retry.ID != job.ID || retry.Status != IndexJobBillingPending {
		t.Fatalf("retry: job=%+v error=%v", retry, apiErr)
	}
	if _, apiErr = reopened.PrepareBilled(ctx, []string{"https://example.test/b"}, []string{"baidu"}, "job_staged", "marker", "different", 1); apiErr == nil || apiErr.HTTPStatus != 409 {
		t.Fatalf("conflicting request error=%v", apiErr)
	}
	if len(eng.calls()) != 0 {
		t.Fatal("unbilled task ran during recovery")
	}
	pending := reopened.PendingBilledJobs()
	if len(pending) != 1 || pending[0].ID != job.ID || pending[0].URLs != nil {
		t.Fatalf("pending reconciliation records=%+v", pending)
	}
	if err := reopened.DiscardPrepared(job.ID, "marker", "wrong"); err == nil {
		t.Fatal("discard accepted mismatched fingerprint")
	}
	if _, err := reopened.ActivateBilled(job.ID, "marker", "fingerprint"); err != nil {
		t.Fatal(err)
	}
	waitForJob(t, reopened, job.ID, 5*time.Second)
	if got := len(eng.calls()); got != 1 {
		t.Fatalf("engine calls=%d, want 1", got)
	}
	if _, err := reopened.ActivateBilled(job.ID, "marker", "fingerprint"); err != nil {
		t.Fatalf("repeated activation: %v", err)
	}
	if err := reopened.DiscardPrepared(job.ID, "marker", "fingerprint"); err == nil {
		t.Fatal("discard removed an activated job")
	}
}

func TestPreparedBillingReservesQueueCapacity(t *testing.T) {
	eng := &indexEngineMock{name: "baidu", initialized: true}
	m := NewIndexJobManager(t.TempDir(), []SearchEngine{eng}, nil, IndexJobOptions{})
	m.queue = make(chan string, 1)
	ctx := WithJobOwner(context.Background(), "key")
	_, apiErr := m.PrepareBilled(ctx, []string{"https://example.test/a"}, []string{"baidu"}, "job_a", "marker", "hash", 1)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if _, apiErr = m.Submit(ctx, []string{"https://example.test/b"}, []string{"baidu"}); apiErr == nil || apiErr.ErrorCode != "job_queue_full" {
		t.Fatalf("preparation failed to reserve capacity: %v", apiErr)
	}
	if err := m.DiscardPrepared("job_a", "marker", "hash"); err != nil {
		t.Fatal(err)
	}
	if _, apiErr = m.Submit(ctx, []string{"https://example.test/b"}, []string{"baidu"}); apiErr != nil {
		t.Fatalf("discard failed to release capacity: %v", apiErr)
	}
}

func TestIndexJobStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	store := NewIndexJobStore(dir)
	now := time.Now().UTC()
	job := IndexJob{ID: "job_store_case", Status: IndexJobQueued, CreatedAt: now, UpdatedAt: now, Total: 1, URLs: []string{"https://example.test/1"}}
	if err := store.SaveMeta(&job); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := store.LoadMeta(job.ID)
	if err != nil || loaded.ID != job.ID {
		t.Fatalf("load: %v %+v", err, loaded)
	}
	list, err := store.ListMeta()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	summaries, err := store.ListMetaSummaries()
	if err != nil || len(summaries) != 1 || summaries[0].URLs != nil {
		t.Fatalf("summaries: %v %+v", err, summaries)
	}
	if len(list[0].URLs) != 1 {
		t.Fatal("full metadata listing no longer retains URLs")
	}
	if err := store.Delete(job.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.LoadMeta(job.ID); err == nil {
		t.Fatal("deleted job still readable")
	}
	// Path traversal attempts must not escape the store root.
	if _, err := store.LoadMeta("../../etc/passwd"); err == nil {
		t.Fatal("sanitized id still resolved")
	}
}

func TestIndexJobStoreResultsPage(t *testing.T) {
	store := NewIndexJobStore(t.TempDir())
	for _, url := range []string{"https://example.test/1", "https://example.test/2", "https://example.test/3"} {
		if err := store.AppendResult("job_page", IndexCheckURLResult{URL: url}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.LoadResultsPage("job_page", 1, 1)
	if err != nil || len(rows) != 1 || rows[0].URL != "https://example.test/2" {
		t.Fatalf("page: rows=%+v err=%v", rows, err)
	}
	rows, err = store.LoadResultsPage("job_page", 3, 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("past end: rows=%+v err=%v", rows, err)
	}
	rows, err = store.LoadResultsPage("job_page", 1, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("unbounded: rows=%+v err=%v", rows, err)
	}
}

func TestIndexJobCSVSnapshot(t *testing.T) {
	store := NewIndexJobStore(t.TempDir())
	first := IndexCheckURLResult{URL: "=https://example.test/first"}
	second := IndexCheckURLResult{URL: "https://example.test/second"}
	if err := store.AppendResult("job_csv", first); err != nil {
		t.Fatal(err)
	}
	f, size, err := store.OpenResultsSnapshot("job_csv")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, f)
	if err := store.AppendResult("job_csv", second); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := StreamJobCSV(&out, f, size, []string{"baidu"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	want := string(WriteCSV([]IndexCheckURLResult{first}, []string{"baidu"}))
	if out.String() != want {
		t.Fatalf("snapshot CSV = %q, want %q", out.String(), want)
	}
}

func TestIndexJobRetentionProtectsActiveAndUnfinishedJobs(t *testing.T) {
	m := NewIndexJobManager(t.TempDir(), nil, nil, IndexJobOptions{RetentionDays: 30})
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -31)
	jobs := []IndexJob{
		{ID: "expired", Status: IndexJobDone, FinishedAt: &old},
		{ID: "running", Status: IndexJobRunning, FinishedAt: &old},
		{ID: "recent", Status: IndexJobDone, FinishedAt: &now},
		{ID: "no_finish", Status: IndexJobFailed},
		{ID: "active_worker", Status: IndexJobCancelled, FinishedAt: &old},
	}
	for _, job := range jobs {
		if err := m.store.SaveMeta(&job); err != nil {
			t.Fatal(err)
		}
	}
	m.jobs["active_worker"] = &jobState{job: jobs[4], running: true}
	count, size, err := m.RetentionPreview(30, now)
	if err != nil || count != 1 || size <= 0 {
		t.Fatalf("preview count=%d size=%d err=%v", count, size, err)
	}
	if _, err := m.store.LoadMeta("expired"); err != nil {
		t.Fatalf("preview removed data: %v", err)
	}
	m.pruneExpired(now)
	if _, err := m.store.LoadMeta("expired"); !os.IsNotExist(err) {
		t.Fatalf("expired job still exists: %v", err)
	}
	for _, id := range []string{"running", "recent", "no_finish", "active_worker"} {
		if _, err := m.store.LoadMeta(id); err != nil {
			t.Fatalf("protected job %s: %v", id, err)
		}
	}
}
