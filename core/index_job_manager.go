package core

// IndexJobManager executes submitted index-check jobs one at a time at the
// engine's measured-safe pace.
//
// Ordering is deliberately serial: the engines are rate-limited per exit IP, so
// running two jobs concurrently would only interleave their tokens while making
// progress reports meaningless. A job therefore waits in a FIFO queue and, once
// running, owns the engines.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// IndexJobOptions tunes the job runner.
type IndexJobOptions struct {
	// MaxURLs caps one job (0 = DefaultIndexJobMaxURLs).
	MaxURLs int
	// Chunk is how many URLs are evaluated per progress write (0 = 1).
	//
	// One URL per write keeps progress honest: the runner can only report a row
	// after the engine answered for it (~12s on Baidu), so a larger chunk makes
	// the counters jump in two-minute steps and a fresh job looks stuck at 0/N.
	// Each write is a ~1KB atomic rename, so per-URL granularity is free.
	Chunk int
	// MaxUnitRetries is how often a URL is retried after engine-level failures
	// such as a cooldown or a captcha (0 = 6).
	MaxUnitRetries int
	// WaitPollCap bounds one cooldown wait (0 = 60s).
	WaitPollCap time.Duration
}

// DefaultIndexJobMaxURLs is the per-job ceiling: one export is ~800 URLs, and
// 5000 keeps a full-site batch in one job while bounding memory and disk.
const DefaultIndexJobMaxURLs = 5000

func (o IndexJobOptions) normalized() IndexJobOptions {
	if o.MaxURLs <= 0 {
		o.MaxURLs = DefaultIndexJobMaxURLs
	}
	if o.Chunk <= 0 {
		o.Chunk = 1
	}
	if o.MaxUnitRetries <= 0 {
		o.MaxUnitRetries = 6
	}
	if o.WaitPollCap <= 0 {
		o.WaitPollCap = 60 * time.Second
	}
	return o
}

// jobState is the in-memory half of a job (meta + scheduling bookkeeping).
type jobState struct {
	mu       sync.Mutex
	job      IndexJob
	pending  []string
	attempts map[string]int
	cancel   context.CancelFunc
	running  bool
}

// releaseLocked drops the heavy part of a finished job (the submitted URL list
// and the pending queue) from memory. The record itself stays for progress
// queries; the full state is on disk. Caller must hold s.mu.
//
// Terminal transitions call this while still holding the lock, so "status is
// terminal" and "URL list is gone" hold at the same instant. Clearing afterwards
// left a real window: an observer (progress query, test) could read a finished
// status while the URL list was still resident — surfaced by
// TestIndexJobReleasesMemoryWhenDone under -race.
func (s *jobState) releaseLocked() {
	if !s.job.Status.Terminal() {
		return
	}
	s.job.URLs = nil
	s.pending = nil
	s.attempts = nil
}

// maxResidentJobs bounds how many finished jobs stay in memory. Older ones are
// dropped here and served from disk by Get/List on demand.
const maxResidentJobs = 500

// evictTerminal keeps the resident job table from growing without bound. The
// runner only ever touches queued/running jobs, so evicting finished ones never
// affects scheduling.
func (m *IndexJobManager) evictTerminal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobs) <= maxResidentJobs {
		return
	}
	type victim struct {
		id string
		at time.Time
	}
	victims := make([]victim, 0, len(m.jobs))
	for id, st := range m.jobs {
		st.mu.Lock()
		terminal := st.job.Status.Terminal()
		created := st.job.CreatedAt
		st.mu.Unlock()
		if terminal {
			victims = append(victims, victim{id: id, at: created})
		}
	}
	if len(victims) == 0 {
		return
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].at.Before(victims[j].at) })
	remove := len(m.jobs) - maxResidentJobs
	for i := 0; i < remove && i < len(victims); i++ {
		delete(m.jobs, victims[i].id)
	}
}

func (s *jobState) snapshot() IndexJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.job
	out.URLs = nil // the status endpoint never echoes the submitted list
	return out
}

// IndexJobManager owns the queue, the runner and the on-disk store.
type IndexJobManager struct {
	opts    IndexJobOptions
	store   *IndexJobStore
	engines []SearchEngine
	rs      *ResilientSearcher

	mu    sync.Mutex
	jobs  map[string]*jobState
	queue chan string

	lifecycle sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewIndexJobManager builds a manager. engines/rs are the same instances the
// HTTP index handler uses, so a job shares the rate limiter, cooldown state and
// browser pool with synchronous requests.
func NewIndexJobManager(dir string, engines []SearchEngine, rs *ResilientSearcher, opts IndexJobOptions) *IndexJobManager {
	return &IndexJobManager{
		opts:    opts.normalized(),
		store:   NewIndexJobStore(dir),
		engines: engines,
		rs:      rs,
		jobs:    map[string]*jobState{},
		queue:   make(chan string, 1024),
	}
}

// Store exposes the job store (results, CSV).
func (m *IndexJobManager) Store() *IndexJobStore { return m.store }

// Start launches the runner and re-queues jobs left unfinished by a previous
// process.
func (m *IndexJobManager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	recovered := m.resume(runCtx)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for _, id := range recovered {
			if runCtx.Err() != nil {
				return
			}
			m.runJob(runCtx, id)
		}
		m.loop(runCtx)
	}()
}

// Stop cancels the runner and every in-flight job.
func (m *IndexJobManager) Stop() {
	if m == nil {
		return
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
		m.cancel = nil
	}
}

// resume reloads unfinished jobs from disk. Results already on disk stay
// applied, so a restart continues where the previous process stopped.
func (m *IndexJobManager) resume(ctx context.Context) []string {
	metas, err := m.store.ListMeta()
	if err != nil {
		WithRequest(ctx).WithError(err).Error("cannot list index jobs for recovery")
		return nil
	}
	var recovered []string
	// ListMeta returns newest first. Run the restored backlog in FIFO order.
	for i := len(metas) - 1; i >= 0; i-- {
		meta := metas[i]
		if meta.Status.Terminal() {
			continue
		}
		rows, err := m.store.LoadResults(meta.ID)
		if err != nil {
			WithRequest(ctx).WithError(err).WithField("job_id", meta.ID).Error("cannot load index job results for recovery")
			continue
		}
		st := &jobState{job: *meta, attempts: map[string]int{}}
		st.job.Completed, st.job.Indexed, st.job.NotIndexed, st.job.Failed = 0, 0, 0, 0
		done := make(map[string]bool, len(rows))
		for _, row := range rows {
			if !done[row.URL] {
				done[row.URL] = true
				m.countResult(st, row)
			}
		}
		pending := make([]string, 0, len(meta.URLs))
		for _, u := range meta.URLs {
			if !done[u] {
				pending = append(pending, u)
			}
		}
		st.pending = pending
		st.job.Status = IndexJobQueued
		st.job.WaitSeconds, st.job.Retrying = 0, 0
		m.mu.Lock()
		m.jobs[meta.ID] = st
		m.mu.Unlock()
		recovered = append(recovered, meta.ID)
		WithRequest(ctx).WithField("job_id", meta.ID).WithField("pending", len(pending)).
			Info("index job restored after restart")
	}
	return recovered
}

// Submit validates and queues a batch for the caller identified by ctx.
//
// The owner is read from the request context (core.WithJobOwner), so a job is
// bound to the API key that paid for it: another key cannot read, list or cancel
// it. An empty owner means an admin submission (JWT), which may see every job.
func (m *IndexJobManager) Submit(ctx context.Context, urls []string, engines []string) (*IndexJob, *APIError) {
	if m == nil {
		return nil, &APIError{HTTPStatus: 501, ErrorCode: "not_implemented", Reason: "NOT_IMPLEMENTED", Message: "index jobs are not enabled"}
	}
	clean := make([]string, 0, len(urls))
	seen := make(map[string]bool, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" || seen[u] {
			continue
		}
		if _, ok := indexURLKeyFrom(u); !ok {
			return nil, errInvalidParam(fmt.Sprintf("%q is not a valid absolute URL", u))
		}
		seen[u] = true
		clean = append(clean, u)
	}
	if len(clean) == 0 {
		return nil, errEmptyQuery()
	}
	if len(clean) > m.opts.MaxURLs {
		return nil, errTooManyURLs(fmt.Sprintf("urls accepts at most %d entries per job, got %d", m.opts.MaxURLs, len(clean)))
	}
	selected, apiErr := SelectIndexCheckEngines(m.engines, engines)
	if apiErr != nil {
		return nil, apiErr
	}

	now := time.Now().UTC()
	job := IndexJob{
		ID:         newIndexJobID(now),
		Status:     IndexJobQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
		OwnerKeyID: JobOwner(ctx),
		Engines:    IndexCheckEngineNames(selected),
		Total:      len(clean),
		URLs:       clean,
	}
	// Submitters share the queue lock so the capacity check remains valid
	// until this job is persisted and published to the runner.
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) == cap(m.queue) {
		return nil, &APIError{HTTPStatus: 503, ErrorCode: "job_queue_full", Reason: "JOB_QUEUE_FULL", Message: "index job queue is full; retry later", RetryAfterSeconds: 60}
	}
	if err := m.store.SaveMeta(&job); err != nil {
		return nil, &APIError{HTTPStatus: 500, ErrorCode: "internal_error", Reason: "INTERNAL", Message: "cannot persist the job"}
	}
	st := &jobState{job: job, pending: clean, attempts: map[string]int{}}
	m.jobs[job.ID] = st
	m.queue <- job.ID

	out := job
	out.URLs = nil
	return &out, nil
}

// Get returns one job's snapshot, but only when ownerKeyID may see it. A
// foreign job reports "not found" — never "forbidden" — so the endpoint does not
// confirm that somebody else's batch exists.
func (m *IndexJobManager) Get(id, ownerKeyID string) (*IndexJob, bool) {
	m.mu.Lock()
	st, ok := m.jobs[id]
	m.mu.Unlock()
	if ok {
		snap := st.snapshot()
		if !jobVisibleTo(snap.OwnerKeyID, ownerKeyID) {
			return nil, false
		}
		return &snap, true
	}
	// Not resident (previous process): fall back to disk.
	meta, err := m.store.LoadMeta(id)
	if err != nil {
		return nil, false
	}
	if !jobVisibleTo(meta.OwnerKeyID, ownerKeyID) {
		return nil, false
	}
	meta.URLs = nil
	return meta, true
}

// List returns the jobs visible to ownerKeyID, newest first.
func (m *IndexJobManager) List(ownerKeyID string, limit int) []IndexJob {
	m.mu.Lock()
	resident := make([]IndexJob, 0, len(m.jobs))
	for _, st := range m.jobs {
		resident = append(resident, st.snapshot())
	}
	m.mu.Unlock()

	metas, _ := m.store.ListMeta()
	seen := make(map[string]bool, len(resident))
	out := make([]IndexJob, 0, len(metas)+len(resident))
	for _, job := range resident {
		if !jobVisibleTo(job.OwnerKeyID, ownerKeyID) {
			continue
		}
		seen[job.ID] = true
		out = append(out, job)
	}
	for _, meta := range metas {
		if seen[meta.ID] || !jobVisibleTo(meta.OwnerKeyID, ownerKeyID) {
			continue
		}
		snap := *meta
		snap.URLs = nil
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Results returns every finished row of a job.
func (m *IndexJobManager) Results(id string) ([]IndexCheckURLResult, error) {
	return m.store.LoadResults(id)
}

// Cancel stops a queued or running job owned by ownerKeyID.
func (m *IndexJobManager) Cancel(id, ownerKeyID string) bool {
	cancelled, _ := m.cancelJob(id, ownerKeyID)
	return cancelled
}

func (m *IndexJobManager) cancelJob(id, ownerKeyID string) (bool, error) {
	m.mu.Lock()
	st, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	st.mu.Lock()
	if !jobVisibleTo(st.job.OwnerKeyID, ownerKeyID) || st.job.Status.Terminal() {
		st.mu.Unlock()
		return false, nil
	}
	now := time.Now().UTC()
	next := st.job
	next.Status = IndexJobCancelled
	next.FinishedAt, next.UpdatedAt = &now, now
	next.WaitSeconds, next.Retrying = 0, 0
	if err := m.store.SaveMeta(&next); err != nil {
		st.mu.Unlock()
		WithRequest(context.Background()).WithError(err).WithField("job_id", id).Error("cannot persist index job cancellation")
		return false, err
	}
	if st.cancel != nil {
		st.cancel()
	}
	st.job = next
	st.releaseLocked()
	st.mu.Unlock()
	m.evictTerminal()
	return true, nil
}

func (m *IndexJobManager) loop(ctx context.Context) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case id := <-m.queue:
			m.runJob(ctx, id)
		}
	}
}

// runJob drains one job to completion, terminal status, or cancellation.
func (m *IndexJobManager) runJob(parent context.Context, id string) {
	if parent.Err() != nil {
		return
	}
	m.mu.Lock()
	st, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return
	}

	st.mu.Lock()
	if st.job.Status.Terminal() || st.running {
		st.mu.Unlock()
		return
	}
	jobCtx, cancel := context.WithCancel(parent)
	st.cancel = cancel
	st.running = true
	st.job.Status = IndexJobRunning
	st.job.UpdatedAt = time.Now().UTC()
	m.saveJobLocked(st)
	engineNames := st.job.Engines
	st.mu.Unlock()
	defer func() {
		cancel()
		st.mu.Lock()
		st.running = false
		st.cancel = nil
		st.mu.Unlock()
	}()
	selected, engineErr := SelectIndexCheckEngines(m.engines, engineNames)
	if engineErr != nil {
		m.finish(st, IndexJobFailed, engineErr.Message)
		return
	}

	for {
		if jobCtx.Err() != nil {
			m.suspend(st)
			return
		}

		st.mu.Lock()
		if st.job.Status.Terminal() {
			st.mu.Unlock()
			return
		}
		if len(st.pending) == 0 {
			st.mu.Unlock()
			m.finish(st, IndexJobDone, "")
			return
		}
		if wait := m.waitForEngines(selected); wait > 0 {
			st.job.Status = IndexJobWaiting
			st.job.WaitSeconds = int(wait.Seconds())
			st.job.UpdatedAt = time.Now().UTC()
			m.saveJobLocked(st)
			st.mu.Unlock()
			if err := SleepContext(jobCtx, wait); err != nil {
				m.suspend(st)
				return
			}
			continue
		}
		st.job.Status = IndexJobRunning
		st.job.WaitSeconds = 0

		take := m.opts.Chunk
		if take > len(st.pending) {
			take = len(st.pending)
		}
		chunk := append([]string(nil), st.pending[:take]...)
		st.pending = append([]string(nil), st.pending[take:]...)
		st.mu.Unlock()

		rows := RunIndexCheckUnits(jobCtx, m.rs, selected, chunk)

		var retry []string
		st.mu.Lock()
		if jobCtx.Err() != nil || st.job.Status.Terminal() {
			st.mu.Unlock()
			m.suspend(st)
			return
		}
		for _, row := range rows {
			if m.shouldRetry(row) && st.attempts[row.URL] < m.opts.MaxUnitRetries {
				st.attempts[row.URL]++
				retry = append(retry, row.URL)
				continue
			}
			if err := m.store.AppendResult(id, row); err != nil {
				st.mu.Unlock()
				WithRequest(jobCtx).WithError(err).WithField("job_id", id).Error("cannot persist index job result")
				m.finish(st, IndexJobFailed, "cannot persist index job result")
				return
			}
			m.countResult(st, row)
		}
		st.job.Retrying = len(retry)
		st.pending = append(st.pending, retry...)
		st.job.UpdatedAt = time.Now().UTC()
		m.saveJobLocked(st)
		st.mu.Unlock()
	}
}

// selectedEngines resolves the job's engines against the live registry.
func (m *IndexJobManager) selectedEngines(names []string) []SearchEngine {
	selected, err := SelectIndexCheckEngines(m.engines, names)
	if err != nil {
		return nil
	}
	return selected
}

// waitForEngines returns how long to pause before the next chunk: the longest
// remaining cooldown when every selected engine is cooling, else zero.
func (m *IndexJobManager) waitForEngines(engines []SearchEngine) time.Duration {
	if m.rs == nil || len(engines) == 0 {
		return 0
	}
	var (
		longest time.Duration
		cooling int
	)
	now := time.Now()
	for _, engine := range engines {
		status := m.rs.EngineCooldownStatus(engine.Name())
		if !status.InCooldown {
			continue
		}
		cooling++
		if wait := status.Until.Sub(now); wait > longest {
			longest = wait
		}
	}
	if cooling < len(engines) || longest <= 0 {
		return 0
	}
	if longest > m.opts.WaitPollCap {
		return m.opts.WaitPollCap
	}
	return longest
}

// shouldRetry reports whether a row was blocked by a transient engine condition
// (cooldown/captcha/rate limit) and deserves another pass later.
func (m *IndexJobManager) shouldRetry(row IndexCheckURLResult) bool {
	retryable := false
	for _, outcome := range row.Engines {
		success, engineErr := outcome.Verdict()
		if success != nil {
			return false
		}
		if engineErr == nil {
			return false
		}
		switch engineErr.Code {
		case "engine_cooldown", "captcha_detected", "blocked", "rate_limited",
			"request_timeout", "engine_queue_exceeded", "circuit_open", "engine_internal", "proxy_unavailable":
			retryable = true
		default:
			return false
		}
	}
	return retryable
}

func (m *IndexJobManager) rowFailed(row IndexCheckURLResult) bool {
	if len(row.Engines) == 0 {
		return true
	}
	for _, outcome := range row.Engines {
		if success, _ := outcome.Verdict(); success != nil {
			return false
		}
	}
	return true
}

func (m *IndexJobManager) rowIndexed(row IndexCheckURLResult) bool {
	for _, outcome := range row.Engines {
		if success, _ := outcome.Verdict(); success != nil && success.Indexed {
			return true
		}
	}
	return false
}

func (m *IndexJobManager) countResult(st *jobState, row IndexCheckURLResult) {
	st.job.Completed++
	if m.rowFailed(row) {
		st.job.Failed++
	} else if m.rowIndexed(row) {
		st.job.Indexed++
	} else {
		st.job.NotIndexed++
	}
}

// suspend preserves an interrupted job for recovery after service shutdown.
func (m *IndexJobManager) suspend(st *jobState) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.job.Status.Terminal() {
		return
	}
	st.job.Status = IndexJobQueued
	st.job.WaitSeconds, st.job.Retrying = 0, 0
	st.job.UpdatedAt = time.Now().UTC()
	m.saveJobLocked(st)
}

// saveJobLocked keeps older snapshots from overwriting later transitions.
func (m *IndexJobManager) saveJobLocked(st *jobState) {
	if err := m.store.SaveMeta(&st.job); err != nil {
		WithRequest(context.Background()).WithError(err).WithField("job_id", st.job.ID).Error("cannot persist index job metadata")
	}
}

// finish records a terminal status.
func (m *IndexJobManager) finish(st *jobState, status IndexJobStatus, lastErr string) {
	now := time.Now().UTC()
	st.mu.Lock()
	if st.job.Status.Terminal() {
		st.mu.Unlock()
		return
	}
	st.job.Status = status
	st.job.FinishedAt = &now
	st.job.UpdatedAt = now
	if lastErr != "" {
		st.job.LastError = lastErr
	}
	m.saveJobLocked(st)
	st.releaseLocked()
	st.mu.Unlock()
	m.evictTerminal()
}

// EngineInterval reports the slowest selected engine's token interval, which is
// the pace a job drains at (used for the ETA).
func (m *IndexJobManager) EngineInterval(engines []string) time.Duration {
	selected := m.selectedEngines(engines)
	var slowest time.Duration
	for _, engine := range selected {
		limiter := engine.GetRateLimiter()
		if limiter == nil || limiter.Limit() <= 0 {
			continue
		}
		interval := time.Duration(float64(time.Second) / float64(limiter.Limit()))
		if interval > slowest {
			slowest = interval
		}
	}
	return slowest
}

// newIndexJobID builds a sortable, unguessable job id.
func newIndexJobID(now time.Time) string {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "job_" + now.Format("20060102T150405")
	}
	return "job_" + now.Format("20060102T150405") + "_" + hex.EncodeToString(buf)
}
