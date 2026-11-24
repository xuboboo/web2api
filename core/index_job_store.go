package core

// Index-check jobs: large URL batches submitted once and executed by the
// server at the engine's safe pace.
//
// Why a job exists at all: a full onlinedown export is ~800 URLs, and at the
// measured-safe Baidu pace (one token per 12s) that is ~2.7 hours. No HTTP
// request can hold that open, and letting the caller pace 20-URL chunks means
// the caller re-implements the backoff we measured. A job keeps the accepted
// batch on disk, drains it at the engine's rate, waits out cooldowns instead of
// burning units against a challenged IP, and survives a restart.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// IndexJobStatus is the lifecycle state of one index-check job.
type IndexJobStatus string

const (
	// IndexJobQueued waits for the runner.
	IndexJobQueued IndexJobStatus = "queued"
	// IndexJobRunning is draining URLs.
	IndexJobRunning IndexJobStatus = "running"
	// IndexJobWaiting deliberately pauses while every selected engine cools.
	IndexJobWaiting IndexJobStatus = "waiting"
	// IndexJobDone finished; every URL has a verdict or a final error.
	IndexJobDone IndexJobStatus = "done"
	// IndexJobCancelled was cancelled by the caller.
	IndexJobCancelled IndexJobStatus = "cancelled"
	// IndexJobFailed aborted for a reason that is not per-URL.
	IndexJobFailed IndexJobStatus = "failed"
)

// Terminal reports whether the status will not change again.
func (s IndexJobStatus) Terminal() bool {
	switch s {
	case IndexJobDone, IndexJobCancelled, IndexJobFailed:
		return true
	}
	return false
}

// IndexJob is the persisted metadata of one batch (results are stored
// separately, append-only, so a crash loses at most the row in flight).
type IndexJob struct {
	ID         string         `json:"job_id"`
	Status     IndexJobStatus `json:"status"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Engines    []string       `json:"engines"`
	Total      int            `json:"total"`
	Completed  int            `json:"completed"`
	Indexed    int            `json:"indexed"`
	NotIndexed int            `json:"not_indexed"`
	Failed     int            `json:"failed"`
	Retrying   int            `json:"retrying"`
	// WaitSeconds is the remaining cooldown the runner is waiting out.
	WaitSeconds int    `json:"wait_seconds,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	// URLs is persisted so a restart can resume the same batch.
	URLs []string `json:"urls"`
}

// Progress percent of finished URLs.
func (j *IndexJob) Progress() float64 {
	if j == nil || j.Total == 0 {
		return 0
	}
	done := j.Completed + j.Failed
	if done > j.Total {
		done = j.Total
	}
	return float64(done) / float64(j.Total)
}

// ETA estimates the remaining wall-clock time from the observed pace.
func (j *IndexJob) ETA(interval time.Duration) time.Duration {
	if j == nil || interval <= 0 {
		return 0
	}
	remaining := j.Total - j.Completed - j.Failed
	if remaining <= 0 {
		return 0
	}
	return time.Duration(remaining) * interval
}

// IndexJobStore persists jobs under one directory:
//
//	<job_id>/job.json       metadata (atomic replace)
//	<job_id>/results.jsonl  one finished row per line, append-only
type IndexJobStore struct {
	dir string
	mu  sync.Mutex
}

// NewIndexJobStore returns a store rooted at dir (created on first save).
func NewIndexJobStore(dir string) *IndexJobStore {
	return &IndexJobStore{dir: dir}
}

// Dir reports the store root.
func (s *IndexJobStore) Dir() string { return s.dir }

func (s *IndexJobStore) jobDir(id string) string {
	return filepath.Join(s.dir, sanitizeJobID(id))
}

// sanitizeJobID keeps an id from escaping the store directory.
func sanitizeJobID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || len(out) > 64 {
		return "invalid"
	}
	return out
}

// SaveMeta atomically writes job metadata.
func (s *IndexJobStore) SaveMeta(job *IndexJob) error {
	if job == nil {
		return errors.New("nil job")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.jobDir(job.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "job.json.tmp")
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "job.json"))
}

// AppendResult appends one finished row.
func (s *IndexJobStore) AppendResult(jobID string, row IndexCheckURLResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.jobDir(jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(payload, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// LoadResults returns every finished row in completion order.
func (s *IndexJobStore) LoadResults(jobID string) ([]IndexCheckURLResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(filepath.Join(s.jobDir(jobID), "results.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	rows := make([]IndexCheckURLResult, 0, 64)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row IndexCheckURLResult
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			// A torn tail line from a crash is the only expected corruption.
			continue
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

// LoadMeta reads one job's metadata.
func (s *IndexJobStore) LoadMeta(jobID string) (*IndexJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := os.ReadFile(filepath.Join(s.jobDir(jobID), "job.json"))
	if err != nil {
		return nil, err
	}
	var job IndexJob
	if err := json.Unmarshal(payload, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// ListMeta returns every stored job, newest first.
func (s *IndexJobStore) ListMeta() ([]*IndexJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]*IndexJob, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(s.dir, entry.Name(), "job.json"))
		if err != nil {
			continue
		}
		var job IndexJob
		if err := json.Unmarshal(payload, &job); err != nil {
			continue
		}
		jobs = append(jobs, &job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs, nil
}

// Delete removes a job directory.
func (s *IndexJobStore) Delete(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.jobDir(jobID))
}

// WriteCSV renders job rows as an Excel-friendly CSV (UTF-8 BOM, CRLF).
func WriteCSV(rows []IndexCheckURLResult, engines []string) []byte {
	var b strings.Builder
	b.WriteString("\ufeff")
	header := []string{"url"}
	for _, engine := range engines {
		header = append(header, engine+"_indexed", engine+"_match", engine+"_matched_result_url", engine+"_total_results", engine+"_error")
	}
	b.WriteString(strings.Join(header, ","))
	b.WriteString("\r\n")
	for _, row := range rows {
		fields := []string{csvField(row.URL)}
		for _, engine := range engines {
			outcome, ok := row.Engines[engine]
			if !ok {
				fields = append(fields, "", "", "", "", "")
				continue
			}
			success, engineErr := outcome.Verdict()
			if engineErr != nil {
				fields = append(fields, "", "", "", "", csvField(engineErr.Code))
				continue
			}
			if success == nil {
				fields = append(fields, "", "", "", "", "")
				continue
			}
			fields = append(fields,
				fmt.Sprintf("%t", success.Indexed),
				success.Match,
				csvField(success.MatchedResultURL),
				fmt.Sprintf("%d", success.TotalResults),
				"")
		}
		b.WriteString(strings.Join(fields, ","))
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\r\n") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}
