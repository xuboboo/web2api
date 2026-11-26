package core

// MCP-facing job operations.
//
// The MCP plane speaks JSON-RPC, not HTTP, so it cannot use the 202/poll_url
// contract directly. These methods expose the same job queue as plain Go calls
// returning JSON-serialisable payloads; the MCP tools (check_indexed →
// get_index_job) are thin wrappers over them.
//
// Billing note: the MCP guard charges check_indexed as urls × engines, so a job
// submission is billed by the submitted size — the same rule the HTTP 202 uses.

import (
	"context"
	"fmt"
	"time"
)

type jobSubmitContextKey struct{}

// WithIndexJobSubmission installs a billing-aware submitter for authenticated
// adapters. It only intercepts actual asynchronous submissions.
func WithIndexJobSubmission(ctx context.Context, submit func(context.Context, []string, []string) (*IndexJob, error)) context.Context {
	return context.WithValue(ctx, jobSubmitContextKey{}, submit)
}

// IndexJobRetentionPreview returns the estimated terminal-job cleanup impact.
func (s *Server) IndexJobRetentionPreview(days int) (int, int64, error) {
	if !s.JobsEnabled() {
		return 0, 0, fmt.Errorf("index jobs are not enabled")
	}
	return s.jobs.RetentionPreview(days, time.Now())
}

// JobsEnabled reports whether the async job queue is wired in this deployment.
func (s *Server) JobsEnabled() bool { return s != nil && s.jobs != nil }

// PrepareBilledIndexJob persists the task while keeping it out of the runner.
func (s *Server) PrepareBilledIndexJob(ctx context.Context, urls, engines []string, id, marker, fingerprint string, units int64) (*IndexJob, error) {
	if !s.JobsEnabled() {
		return nil, &APIError{HTTPStatus: 501, ErrorCode: "not_implemented", Reason: "NOT_IMPLEMENTED", Message: "index jobs are not enabled"}
	}
	if s.draining.Load() {
		return nil, &APIError{HTTPStatus: 503, ErrorCode: "server_draining", Reason: "SERVER_DRAINING", Message: "server is shutting down; retry later", RetryAfterSeconds: 5}
	}
	job, apiErr := s.jobs.PrepareBilled(ctx, urls, engines, id, marker, fingerprint, units)
	if apiErr != nil {
		return nil, apiErr
	}
	return job, nil
}

func (s *Server) ActivateBilledIndexJob(id, marker, fingerprint string) (*IndexJob, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled")
	}
	return s.jobs.ActivateBilled(id, marker, fingerprint)
}

func (s *Server) DiscardPreparedIndexJob(id, marker, fingerprint string) error {
	if !s.JobsEnabled() {
		return fmt.Errorf("index jobs are not enabled")
	}
	return s.jobs.DiscardPrepared(id, marker, fingerprint)
}

func (s *Server) PendingBilledIndexJobs() []IndexJob {
	if !s.JobsEnabled() {
		return nil
	}
	return s.jobs.PendingBilledJobs()
}

func (s *Server) RetryFailedIndexJob(ctx context.Context, id string) (*IndexJob, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled")
	}
	var job *IndexJob
	var ok bool
	if principal, isConsole := consoleJobPrincipal(ctx); isConsole {
		job, ok = s.jobs.GetForUser(id, principal.userID, principal.admin)
	} else {
		job, ok = s.jobs.Get(id, JobOwner(ctx))
	}
	if !ok {
		return nil, &APIError{HTTPStatus: 404, ErrorCode: "not_found", Reason: "NOT_FOUND", Message: "job not found"}
	}
	// Get intentionally drops URLs for disk-backed API snapshots. Retry needs
	// the original URL list to preserve input order and select failed entries;
	// reload the full metadata only after the tenant visibility check above.
	if len(job.URLs) == 0 {
		full, loadErr := s.jobs.Store().LoadMeta(job.ID)
		if loadErr != nil {
			return nil, &APIError{HTTPStatus: 404, ErrorCode: "not_found", Reason: "NOT_FOUND", Message: "job not found"}
		}
		job = full
	}
	rows, err := s.jobs.Store().LoadResults(job.ID)
	if err != nil {
		return nil, err
	}
	failed := map[string]bool{}
	for _, row := range rows {
		for _, out := range row.Engines {
			if _, e := out.Verdict(); e != nil {
				failed[row.URL] = true
				break
			}
		}
	}
	urls := make([]string, 0, len(failed))
	for _, u := range job.URLs {
		if failed[u] {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil, &APIError{HTTPStatus: 409, ErrorCode: "conflict", Reason: "CONFLICT", Message: "no failed URLs to retry"}
	}
	if submit, ok := ctx.Value(jobSubmitContextKey{}).(func(context.Context, []string, []string) (*IndexJob, error)); ok {
		return submit(ctx, urls, job.Engines)
	}
	return nil, &APIError{HTTPStatus: 503, ErrorCode: "billing_unavailable", Reason: "BILLING_UNAVAILABLE", Message: "retry requires the platform billing-aware submitter"}
}

func (s *Server) JobResults(id string) ([]IndexCheckURLResult, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled")
	}
	return s.jobs.Store().LoadResults(id)
}

func (s *Server) ConsoleJobOwned(id, userID string) bool {
	if !s.JobsEnabled() {
		return false
	}
	_, ok := s.jobs.GetForUser(id, userID, false)
	return ok
}

// ConsoleJobBillingInfo returns the source job billing key after user scoping.
func (s *Server) ConsoleJobBillingInfo(id, userID string) (string, bool) {
	job, ok := s.jobs.GetForUser(id, userID, false)
	if !ok || job.OwnerKeyID == "" {
		return "", false
	}
	return job.OwnerKeyID, true
}

func (s *Server) ConsoleJobBillingInfoAdmin(id string) (string, bool) {
	job, ok := s.jobs.Get(id, "")
	if !ok || job.OwnerKeyID == "" {
		return "", false
	}
	return job.OwnerKeyID, true
}

func (s *Server) ConsoleJobOwner(id string) (string, bool) {
	job, ok := s.jobs.Get(id, "")
	if !ok {
		return "", false
	}
	return job.OwnerUserID, true
}

// ExistingBilledIndexJob looks up a previously charged job without creating it.
func (s *Server) ExistingBilledIndexJob(id, marker, fingerprint string) (*IndexJob, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled")
	}
	job, ok := s.jobs.Get(id, "")
	if !ok {
		return nil, &APIError{HTTPStatus: 410, ErrorCode: "job_expired", Reason: "JOB_EXPIRED", Message: "the previously submitted job is no longer available"}
	}
	if job.BillingMarker != marker || job.BillingFingerprint != fingerprint {
		return nil, &APIError{HTTPStatus: 409, ErrorCode: "conflict", Reason: "CONFLICT", Message: "idempotency key used for a different request"}
	}
	return job, nil
}

func (s *Server) queueIndexJob(ctx context.Context, urls, engines []string) (*IndexJob, *APIError) {
	if submit, ok := ctx.Value(jobSubmitContextKey{}).(func(context.Context, []string, []string) (*IndexJob, error)); ok {
		job, err := submit(ctx, urls, engines)
		if err != nil {
			if e, ok := err.(*APIError); ok {
				return nil, e
			}
			return nil, &APIError{HTTPStatus: 500, ErrorCode: "internal_error", Reason: "INTERNAL_ERROR", Message: "failed to persist billed task"}
		}
		return job, nil
	}
	if s.draining.Load() {
		return nil, &APIError{HTTPStatus: 503, ErrorCode: "server_draining", Reason: "SERVER_DRAINING",
			Message: "server is shutting down; retry later", RetryAfterSeconds: 5}
	}
	return s.jobs.Submit(ctx, urls, engines)
}

// SubmitIndexJob queues a large batch for MCP callers. The owner (API key)
// travels in ctx, so the job stays visible only to the key that paid for it.
func (s *Server) SubmitIndexJob(ctx context.Context, urls, engines []string) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, apiErr := s.queueIndexJob(ctx, urls, engines)
	if apiErr != nil {
		return nil, apiErr
	}
	return map[string]any{
		"job_id":     job.ID,
		"status":     string(job.Status),
		"total":      job.Total,
		"engines":    s.jobEngineNames(job),
		"created_at": job.CreatedAt,
		"note": "queued; the server drains this batch at the engine's safe pace. " +
			"Call get_index_job with this job_id to follow progress and read results.",
	}, nil
}

// IndexJobSnapshot returns one job's progress for MCP callers.
func (s *Server) IndexJobSnapshot(ctx context.Context, jobID string) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, ok := s.jobs.Get(jobID, JobOwner(ctx))
	if !ok {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	return s.jobViewData(job), nil
}

// IndexJobResultsPage returns finished rows for MCP callers.
func (s *Server) IndexJobResultsPage(ctx context.Context, jobID string, offset, limit int) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, ok := s.jobs.Get(jobID, JobOwner(ctx))
	if !ok {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	rows, err := s.jobs.Store().LoadResultsPage(jobID, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("cannot read job results")
	}
	return map[string]any{
		"job_id":   job.ID,
		"status":   string(job.Status),
		"total":    job.Total,
		"offset":   offset,
		"count":    len(rows),
		"results":  rows,
		"progress": job.Progress(),
	}, nil
}

// CancelIndexJob stops a queued or running job for MCP callers.
func (s *Server) CancelIndexJob(ctx context.Context, jobID string) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	cancelled, err := s.jobs.cancelJob(jobID, JobOwner(ctx))
	if err != nil {
		return nil, fmt.Errorf("cannot persist job cancellation")
	}
	if !cancelled {
		if _, ok := s.jobs.Get(jobID, JobOwner(ctx)); !ok {
			return nil, fmt.Errorf("job %q not found", jobID)
		}
		return nil, fmt.Errorf("job %q already finished", jobID)
	}
	job, _ := s.jobs.Get(jobID, JobOwner(ctx))
	if job == nil {
		return map[string]any{"job_id": jobID, "status": string(IndexJobCancelled)}, nil
	}
	return s.jobViewData(job), nil
}

// IndexJobCSV renders a job as CSV for callers that want the table form.
func (s *Server) IndexJobCSV(ctx context.Context, jobID string) ([]byte, string, error) {
	if !s.JobsEnabled() {
		return nil, "", fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, ok := s.jobs.Get(jobID, JobOwner(ctx))
	if !ok {
		return nil, "", fmt.Errorf("job %q not found", jobID)
	}
	rows, err := s.jobs.Results(jobID)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read job results")
	}
	return WriteCSV(rows, s.jobEngineNames(job)), job.ID + ".csv", nil
}
