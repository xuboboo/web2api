package core

import (
	"context"
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
		Engines: []string{"baidu"}, Total: 3, Completed: 1, Indexed: 1, URLs: urls,
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

	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, targetURL string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	m, _ := newTestJobManager(t, dir, []SearchEngine{eng})

	done := waitForJob(t, m, job.ID, 5*time.Second)
	if done.Completed != 3 {
		t.Fatalf("resumed job completed %d of %d", done.Completed, done.Total)
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
