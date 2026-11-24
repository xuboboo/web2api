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
)

// JobsEnabled reports whether the async job queue is wired in this deployment.
func (s *Server) JobsEnabled() bool { return s != nil && s.jobs != nil }

// SubmitIndexJob queues a large batch for MCP callers.
func (s *Server) SubmitIndexJob(_ context.Context, urls, engines []string) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, apiErr := s.jobs.Submit(urls, engines)
	if apiErr != nil {
		return nil, fmt.Errorf("%s", apiErr.Message)
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
func (s *Server) IndexJobSnapshot(_ context.Context, jobID string) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, ok := s.jobs.Get(jobID)
	if !ok {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	return s.jobViewData(job), nil
}

// IndexJobResultsPage returns finished rows for MCP callers.
func (s *Server) IndexJobResultsPage(_ context.Context, jobID string, offset, limit int) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, ok := s.jobs.Get(jobID)
	if !ok {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	rows, err := s.jobs.Results(jobID)
	if err != nil {
		return nil, fmt.Errorf("cannot read job results")
	}
	rows = pageRows(rows, offset, limit)
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
func (s *Server) CancelIndexJob(_ context.Context, jobID string) (any, error) {
	if !s.JobsEnabled() {
		return nil, fmt.Errorf("index jobs are not enabled in this deployment")
	}
	if !s.jobs.Cancel(jobID) {
		if _, ok := s.jobs.Get(jobID); !ok {
			return nil, fmt.Errorf("job %q not found", jobID)
		}
		return nil, fmt.Errorf("job %q already finished", jobID)
	}
	job, _ := s.jobs.Get(jobID)
	if job == nil {
		return map[string]any{"job_id": jobID, "status": string(IndexJobCancelled)}, nil
	}
	return s.jobViewData(job), nil
}

// IndexJobCSV renders a job as CSV for callers that want the table form.
func (s *Server) IndexJobCSV(jobID string) ([]byte, string, error) {
	if !s.JobsEnabled() {
		return nil, "", fmt.Errorf("index jobs are not enabled in this deployment")
	}
	job, ok := s.jobs.Get(jobID)
	if !ok {
		return nil, "", fmt.Errorf("job %q not found", jobID)
	}
	rows, err := s.jobs.Results(jobID)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read job results")
	}
	return WriteCSV(rows, s.jobEngineNames(job)), job.ID + ".csv", nil
}
