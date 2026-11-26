package core

// HTTP surface for index-check jobs.
//
//	POST   /v1/index/check                 >20 URLs → 202 + job id (else sync)
//	GET    /v1/index/check/jobs            recent jobs
//	GET    /v1/index/check/jobs/:id        progress + ETA
//	GET    /v1/index/check/jobs/:id/results[?format=csv&offset=&limit=]
//	DELETE /v1/index/check/jobs/:id        cancel
//
// The sync contract is untouched: callers that keep sending ≤20 URLs see the
// same response as before. Large batches get a job id instead of a 400, which is
// what lets one submission cover an entire export without the client pacing it.

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// indexJobAccepted is the 202 body returned for an accepted batch.
type indexJobAccepted struct {
	JobID      string    `json:"job_id"`
	Status     string    `json:"status"`
	Total      int       `json:"total"`
	Engines    []string  `json:"engines"`
	CreatedAt  time.Time `json:"created_at"`
	PollURL    string    `json:"poll_url"`
	ResultsURL string    `json:"results_url"`
	CSVURL     string    `json:"csv_url"`
	Note       string    `json:"note"`
}

// indexJobView is the status body.
type indexJobView struct {
	JobID       string     `json:"job_id"`
	Status      string     `json:"status"`
	Progress    float64    `json:"progress"`
	Total       int        `json:"total"`
	Completed   int        `json:"completed"`
	Indexed     int        `json:"indexed"`
	NotIndexed  int        `json:"not_indexed"`
	Failed      int        `json:"failed"`
	Retrying    int        `json:"retrying"`
	WaitSeconds int        `json:"wait_seconds,omitempty"`
	ETASeconds  int64      `json:"eta_seconds,omitempty"`
	IntervalMS  int64      `json:"interval_ms,omitempty"`
	Engines     []string   `json:"engines"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	PollURL     string     `json:"poll_url"`
	ResultsURL  string     `json:"results_url"`
	CSVURL      string     `json:"csv_url"`
}

// submitIndexJob queues an oversized batch and answers 202.
func (s *Server) submitIndexJob(c *fiber.Ctx, requestCtx context.Context, urls, engines []string) error {
	job, apiErr := s.queueIndexJob(requestCtx, urls, engines)
	if apiErr != nil {
		WithRequest(requestCtx).WithField("reason", apiErr.Reason).Warn("Invalid index job parameters")
		return apiErr
	}
	poll, results, csv := jobURLs(c, job.ID)
	WithRequest(requestCtx).
		WithField("job_id", job.ID).
		WithField("urls", job.Total).
		Info("Index job accepted")
	c.Status(fiber.StatusAccepted)
	return c.JSON(indexJobAccepted{
		JobID:      job.ID,
		Status:     string(job.Status),
		Total:      job.Total,
		Engines:    job.Engines,
		CreatedAt:  job.CreatedAt,
		PollURL:    poll,
		ResultsURL: results,
		CSVURL:     csv,
		Note:       "queued; the server drains this batch at the engine's safe pace. Poll poll_url for progress.",
	})
}

// IndexJobListHandler serves GET /v1/index/check/jobs.
func (s *Server) IndexJobListHandler(c *fiber.Ctx) error {
	if s.jobs == nil {
		return &APIError{HTTPStatus: 501, ErrorCode: "not_implemented", Reason: "NOT_IMPLEMENTED", Message: "index jobs are not enabled"}
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	var jobs []IndexJob
	if principal, ok := consoleJobPrincipal(c.UserContext()); ok {
		jobs = s.jobs.ListForUser(principal.userID, principal.admin, limit)
	} else {
		jobs = s.jobs.List(JobOwner(c.UserContext()), limit)
	}
	views := make([]indexJobView, 0, len(jobs))
	for i := range jobs {
		views = append(views, s.jobView(c, &jobs[i]))
	}
	return c.JSON(fiber.Map{"count": len(views), "jobs": views})
}

// IndexJobStatusHandler serves GET /v1/index/check/jobs/:id.
func (s *Server) IndexJobStatusHandler(c *fiber.Ctx) error {
	job, err := s.lookupJob(c)
	if err != nil {
		return err
	}
	return c.JSON(s.jobView(c, job))
}

// IndexJobResultsHandler serves GET /v1/index/check/jobs/:id/results, with
// ?format=csv for the spreadsheet-friendly rendering.
func (s *Server) IndexJobResultsHandler(c *fiber.Ctx) error {
	job, err := s.lookupJob(c)
	if err != nil {
		return err
	}
	offset, limit := pageParams(c)
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "csv") {
		f, size, err := s.jobs.Store().OpenResultsSnapshot(job.ID)
		if err != nil {
			return &APIError{HTTPStatus: 500, ErrorCode: "internal_error", Reason: "INTERNAL", Message: "cannot read job results"}
		}
		engines := s.jobEngineNames(job)
		c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
		c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", job.ID+".csv"))
		jobID := job.ID
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			if f != nil {
				defer func() {
					if closeErr := f.Close(); closeErr != nil {
						WithRequest(context.Background()).WithError(closeErr).WithField("job_id", jobID).Warn("cannot close job CSV snapshot")
					}
				}()
			}
			if err := StreamJobCSV(w, f, size, engines, offset, limit); err != nil {
				WithRequest(context.Background()).WithError(err).WithField("job_id", jobID).Warn("cannot stream job CSV")
			}
		})
		return nil
	}
	rows, readErr := s.jobs.Store().LoadResultsPage(job.ID, offset, limit)
	if readErr != nil {
		return &APIError{HTTPStatus: 500, ErrorCode: "internal_error", Reason: "INTERNAL", Message: "cannot read job results"}
	}
	return c.JSON(fiber.Map{
		"job_id":  job.ID,
		"status":  job.Status,
		"total":   job.Total,
		"offset":  offset,
		"count":   len(rows),
		"results": rows,
	})
}

// IndexJobCancelHandler serves DELETE /v1/index/check/jobs/:id.
func (s *Server) IndexJobCancelHandler(c *fiber.Ctx) error {
	job, err := s.lookupJob(c)
	if err != nil {
		return err
	}
	var cancelled bool
	if principal, ok := consoleJobPrincipal(c.UserContext()); ok {
		cancelled, err = s.jobs.CancelForUser(job.ID, principal.userID, principal.admin)
	} else {
		cancelled, err = s.jobs.cancelJob(job.ID, JobOwner(c.UserContext()))
	}
	if err != nil {
		return &APIError{HTTPStatus: 500, ErrorCode: "internal_error", Reason: "INTERNAL", Message: "cannot persist job cancellation"}
	}
	if !cancelled {
		return &APIError{HTTPStatus: 409, ErrorCode: "conflict", Reason: "CONFLICT", Message: "job already finished"}
	}
	updated, _ := s.lookupJob(c)
	if updated == nil {
		updated = job
	}
	return c.JSON(s.jobView(c, updated))
}

// IndexJobRetryHandler queues a separately billed retry for URLs with at least
// one engine error; the original task and verdicts remain immutable.
func (s *Server) IndexJobRetryHandler(c *fiber.Ctx) error {
	job, err := s.RetryFailedIndexJob(c.UserContext(), strings.TrimSpace(c.Params("id")))
	if err != nil {
		return err
	}
	c.Status(fiber.StatusAccepted)
	return c.JSON(s.jobView(c, job))
}

// lookupJob resolves :id or returns the 404/501 error to render.
func (s *Server) lookupJob(c *fiber.Ctx) (*IndexJob, error) {
	if s.jobs == nil {
		return nil, &APIError{HTTPStatus: 501, ErrorCode: "not_implemented", Reason: "NOT_IMPLEMENTED", Message: "index jobs are not enabled"}
	}
	id := strings.TrimSpace(c.Params("id"))
	var job *IndexJob
	var ok bool
	if principal, console := consoleJobPrincipal(c.UserContext()); console {
		job, ok = s.jobs.GetForUser(id, principal.userID, principal.admin)
	} else {
		job, ok = s.jobs.Get(id, JobOwner(c.UserContext()))
	}
	if !ok {
		return nil, &APIError{HTTPStatus: 404, ErrorCode: "not_found", Reason: "NOT_FOUND", Message: "job not found"}
	}
	return job, nil
}

// jobView renders one job with self links for HTTP callers.
func (s *Server) jobView(c *fiber.Ctx, job *IndexJob) indexJobView {
	view := s.jobViewData(job)
	view.PollURL, view.ResultsURL, view.CSVURL = jobURLs(c, job.ID)
	return view
}

// jobViewData renders one job without HTTP links (MCP callers have no path).
func (s *Server) jobViewData(job *IndexJob) indexJobView {
	interval := s.jobs.EngineInterval(job.Engines)
	view := indexJobView{
		JobID:       job.ID,
		Status:      string(job.Status),
		Progress:    job.Progress(),
		Total:       job.Total,
		Completed:   job.Completed,
		Indexed:     job.Indexed,
		NotIndexed:  job.NotIndexed,
		Failed:      job.Failed,
		Retrying:    job.Retrying,
		WaitSeconds: job.WaitSeconds,
		IntervalMS:  interval.Milliseconds(),
		Engines:     s.jobEngineNames(job),
		CreatedAt:   job.CreatedAt,
		UpdatedAt:   job.UpdatedAt,
		FinishedAt:  job.FinishedAt,
		LastError:   job.LastError,
	}
	if !job.Status.Terminal() {
		view.ETASeconds = int64(job.ETA(interval).Seconds())
	}
	return view
}

// jobEngineNames resolves the job's engine selection to canonical names.
func (s *Server) jobEngineNames(job *IndexJob) []string {
	selected, err := SelectIndexCheckEngines(s.searchEngines, job.Engines)
	if err != nil || len(selected) == 0 {
		return append([]string(nil), job.Engines...)
	}
	names := make([]string, 0, len(selected))
	for _, engine := range selected {
		names = append(names, engine.Name())
	}
	return names
}

// jobURLs builds self links that keep the caller's mount prefix.
func jobURLs(c *fiber.Ctx, id string) (poll, results, csv string) {
	prefix := ""
	if path := c.Path(); path != "" {
		if idx := strings.Index(path, "/api/index/check/jobs"); idx >= 0 {
			poll = path[:idx] + "/api/index/check/jobs/" + id
			return poll, poll + "/results", poll + "/results?format=csv"
		}
		if idx := strings.Index(path, "/v1/index/check"); idx >= 0 {
			prefix = path[:idx]
		}
	}
	poll = prefix + "/v1/index/check/jobs/" + id
	return poll, poll + "/results", poll + "/results?format=csv"
}

func pageParams(c *fiber.Ctx) (offset, limit int) {
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	return offset, limit
}
