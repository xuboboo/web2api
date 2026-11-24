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
	// Chunk is how many URLs are evaluated per progress write (0 = 10).
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
		o.Chunk = 10
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

// release drops the heavy part of a finished job (the submitted URL list and the
// pending queue) from memory. The record itself stays for progress queries; the
// full state is on disk.
func (s *jobState) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	order []string
	queue chan string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	runCtx := m.ctx
	m.mu.Unlock()

	m.resume(runCtx)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.loop(runCtx)
	}()
}

// Stop cancels the runner and every in-flight job.
func (m *IndexJobManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

// resume reloads unfinished jobs from disk. Results already on disk stay
// applied, so a restart continues where the previous process stopped.
func (m *IndexJobManager) resume(ctx context.Context) {
	metas, err := m.store.ListMeta()
	if err != nil {
		return
	}
	for _, meta := range metas {
		if meta.Status.Terminal() {
			continue
		}
		rows, err := m.store.LoadResults(meta.ID)
		if err != nil {
			continue
		}
		done := make(map[string]bool, len(rows))
		for _, row := range rows {
			done[row.URL] = true
		}
		pending := make([]string, 0, len(meta.URLs))
		for _, u := range meta.URLs {
			if !done[u] {
				pending = append(pending, u)
			}
		}
		st := &jobState{job: *meta, pending: pending, attempts: map[string]int{}}
		st.job.Status = IndexJobQueued
		st.job.Completed = meta.Completed
		m.mu.Lock()
		m.jobs[meta.ID] = st
		m.order = append(m.order, meta.ID)
		m.mu.Unlock()
		_ = m.store.SaveMeta(&st.job)
		m.enqueue(ctx, meta.ID)
		WithRequest(ctx).WithField("job_id", meta.ID).WithField("pending", len(pending)).
			Info("index job restored after restart")
	}
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

	now := time.Now().UTC()
	job := IndexJob{
		ID:         newIndexJobID(now),
		Status:     IndexJobQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
		OwnerKeyID: JobOwner(ctx),
		Engines:    append([]string(nil), engines...),
		Total:      len(clean),
		URLs:       clean,
	}
	if err := m.store.SaveMeta(&job); err != nil {
		return nil, &APIError{HTTPStatus: 500, ErrorCode: "internal_error", Reason: "INTERNAL", Message: "cannot persist the job"}
	}
	st := &jobState{job: job, pending: clean, attempts: map[string]int{}}
	m.mu.Lock()
	m.jobs[job.ID] = st
	m.order = append(m.order, job.ID)
	m.mu.Unlock()
	m.enqueue(ctx, job.ID)

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
	m.mu.Lock()
	st, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	st.mu.Lock()
	if !jobVisibleTo(st.job.OwnerKeyID, ownerKeyID) {
		st.mu.Unlock()
		return false
	}
	if st.job.Status.Terminal() {
		st.mu.Unlock()
		return false
	}
	if st.cancel != nil {
		st.cancel()
	}
	st.job.Status = IndexJobCancelled
	now := time.Now().UTC()
	st.job.FinishedAt = &now
	st.job.UpdatedAt = now
	snap := st.job
	st.mu.Unlock()
	_ = m.store.SaveMeta(&snap)
	st.release()
	m.evictTerminal()
	return true
}

// enqueue hands the id to the runner. It blocks (respecting ctx) rather than
// spawning a goroutine when the buffer is full: a second concurrent runner would
// break the one-job-at-a-time pacing contract the rate limiter depends on.
func (m *IndexJobManager) enqueue(ctx context.Context, id string) {
	select {
	case m.queue <- id:
	case <-ctx.Done():
	}
}

func (m *IndexJobManager) loop(ctx context.Context) {
	for {
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
	snapshot := st.job
	st.mu.Unlock()
	defer func() {
		cancel()
		st.mu.Lock()
		st.running = false
		st.cancel = nil
		st.mu.Unlock()
	}()
	_ = m.store.SaveMeta(&snapshot)

	selected := m.selectedEngines(st.job.Engines)

	for {
		if jobCtx.Err() != nil {
			m.finish(st, IndexJobCancelled, "cancelled by caller")
			return
		}

		st.mu.Lock()
		if len(st.pending) == 0 {
			st.mu.Unlock()
			m.finish(st, IndexJobDone, "")
			return
		}
		if wait := m.waitForEngines(selected); wait > 0 {
			st.job.Status = IndexJobWaiting
			st.job.WaitSeconds = int(wait.Seconds())
			st.job.UpdatedAt = time.Now().UTC()
			snapshot := st.job
			st.mu.Unlock()
			_ = m.store.SaveMeta(&snapshot)
			if err := SleepContext(jobCtx, wait); err != nil {
				m.finish(st, IndexJobCancelled, "cancelled by caller")
				return
			}
			st.mu.Lock()
			st.job.Status = IndexJobRunning
			st.job.WaitSeconds = 0
		}

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
		for _, row := range rows {
			if m.shouldRetry(row) && st.attempts[row.URL] < m.opts.MaxUnitRetries {
				st.attempts[row.URL]++
				retry = append(retry, row.URL)
				continue
			}
			if err := m.store.AppendResult(id, row); err != nil {
				st.job.LastError = err.Error()
			}
			st.job.Completed++
			if m.rowFailed(row) {
				st.job.Failed++
			} else if m.rowIndexed(row) {
				st.job.Indexed++
			} else {
				st.job.NotIndexed++
			}
		}
		st.job.Retrying = len(retry)
		st.pending = append(st.pending, retry...)
		st.job.UpdatedAt = time.Now().UTC()
		snapshot := st.job
		st.mu.Unlock()
		_ = m.store.SaveMeta(&snapshot)
	}
}

// selectedEngines resolves the job's engines against the live registry.
func (m *IndexJobManager) selectedEngines(names []string) []SearchEngine {
	selected, err := SelectIndexCheckEngines(m.engines, names)
	if err != nil || len(selected) == 0 {
		return m.engines
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
	snapshot := st.job
	st.mu.Unlock()
	_ = m.store.SaveMeta(&snapshot)
	st.release()
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
