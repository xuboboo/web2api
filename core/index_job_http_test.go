package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newJobTestServer mounts the index routes the platform normally registers, so
// the handler contract is exercised end to end (202 → poll → CSV).
func newJobTestServer(t *testing.T, withJobs bool) (*Server, *indexEngineMock) {
	t.Helper()
	eng := &indexEngineMock{
		name: "baidu", initialized: true,
		serpFn: func(_ context.Context, targetURL string, _ Query) ([]SearchResult, error) {
			return []SearchResult{{Rank: 1, URL: targetURL, Title: "self"}}, nil
		},
	}
	opts := DefaultServerOptions()
	opts.RequestTimeout = 0
	opts.IndexRequestTimeout = 0
	if withJobs {
		opts.IndexJobDir = t.TempDir()
		opts.IndexJobChunk = 5
	}
	srv := NewServerWithOptions("127.0.0.1", 0, opts, eng)
	t.Cleanup(func() { _ = srv.Shutdown() })

	app := srv.FiberApp()
	app.Post("/v1/index/check", srv.IndexCheckHandler)
	app.Get("/v1/index/check/jobs", srv.IndexJobListHandler)
	app.Get("/v1/index/check/jobs/:id", srv.IndexJobStatusHandler)
	app.Get("/v1/index/check/jobs/:id/results", srv.IndexJobResultsHandler)
	app.Delete("/v1/index/check/jobs/:id", srv.IndexJobCancelHandler)
	return srv, eng
}

func postIndexCheck(t *testing.T, srv *Server, urls []string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"urls": urls, "engines": []string{"baidu"}})
	req := httptest.NewRequest(http.MethodPost, "/v1/index/check", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.FiberApp().Test(req, -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

func getJSON(t *testing.T, srv *Server, path string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp, err := srv.FiberApp().Test(req, -1)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	payload := map[string]any{}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") {
		_ = json.NewDecoder(resp.Body).Decode(&payload)
	}
	return resp, payload
}

func TestIndexCheckLargeBatchReturnsJob(t *testing.T) {
	srv, _ := newJobTestServer(t, true)

	urls := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		urls = append(urls, fmt.Sprintf("https://example.test/a/%d", i))
	}
	resp := postIndexCheck(t, srv, urls)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", resp.StatusCode, body)
	}
	var accepted indexJobAccepted
	if err := json.Unmarshal([]byte(body), &accepted); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if accepted.JobID == "" || accepted.Total != 25 {
		t.Fatalf("accepted = %+v", accepted)
	}
	if !strings.HasSuffix(accepted.PollURL, "/v1/index/check/jobs/"+accepted.JobID) {
		t.Fatalf("poll_url = %q", accepted.PollURL)
	}
	if !strings.Contains(accepted.CSVURL, "format=csv") {
		t.Fatalf("csv_url = %q", accepted.CSVURL)
	}

	// Poll until done.
	var view map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, view = getJSON(t, srv, "/v1/index/check/jobs/"+accepted.JobID)
		if view["status"] == "done" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if view["status"] != "done" {
		t.Fatalf("job did not finish: %v", view)
	}
	if int(view["completed"].(float64)) != 25 || int(view["indexed"].(float64)) != 25 {
		t.Fatalf("counters = %v", view)
	}
	if view["progress"].(float64) != 1 {
		t.Fatalf("progress = %v", view["progress"])
	}

	// Results as CSV.
	req := httptest.NewRequest(http.MethodGet, "/v1/index/check/jobs/"+accepted.JobID+"/results?format=csv", nil)
	csvResp, err := srv.FiberApp().Test(req, -1)
	if err != nil {
		t.Fatalf("csv request: %v", err)
	}
	csvBody := readAll(t, csvResp)
	if !strings.Contains(csvResp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("content type = %q", csvResp.Header.Get("Content-Type"))
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(csvBody, "\ufeff")), "\r\n")
	if len(lines) != 26 {
		t.Fatalf("csv rows = %d, want 26 (header + 25): %q", len(lines), csvBody[:min(len(csvBody), 300)])
	}

	// Paged JSON results.
	_, page := getJSON(t, srv, "/v1/index/check/jobs/"+accepted.JobID+"/results?offset=20&limit=10")
	if int(page["count"].(float64)) != 5 {
		t.Fatalf("paged count = %v", page["count"])
	}
}

func TestDrainingRejectsAsyncSubmissionsWithoutScheduling(t *testing.T) {
	srv, eng := newJobTestServer(t, true)
	srv.SetDraining(true)
	urls := make([]string, MaxIndexCheckURLs+1)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.test/drain/%d", i)
	}
	resp := postIndexCheck(t, srv, urls)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" ||
		!strings.Contains(body, "server_draining") {
		t.Fatalf("REST draining response: status=%d body=%s", resp.StatusCode, body)
	}
	_, err := srv.SubmitIndexJob(t.Context(), urls, []string{"baidu"})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.ErrorCode != "server_draining" {
		t.Fatalf("MCP draining response: %v", err)
	}
	stored, err := srv.jobs.Store().ListMeta()
	if err != nil || len(stored) != 0 || len(eng.calls()) != 0 {
		t.Fatalf("drain scheduled work: jobs=%d engine calls=%d err=%v", len(stored), len(eng.calls()), err)
	}
}

func TestIndexCheckSmallBatchStaysSynchronous(t *testing.T) {
	srv, _ := newJobTestServer(t, true)
	resp := postIndexCheck(t, srv, []string{"https://example.test/a/1", "https://example.test/a/2"})
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (sync contract): %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "\"results\"") {
		t.Fatalf("sync body missing results: %s", body)
	}
}

func TestIndexCheckLargeBatchWithoutJobsIsRejected(t *testing.T) {
	srv, _ := newJobTestServer(t, false)
	urls := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		urls = append(urls, fmt.Sprintf("https://example.test/b/%d", i))
	}
	resp := postIndexCheck(t, srv, urls)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "TOO_MANY_URLS") {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
}

func TestIndexJobStatusUnknownIDIs404(t *testing.T) {
	srv, _ := newJobTestServer(t, true)
	resp, _ := getJSON(t, srv, "/v1/index/check/jobs/job_does_not_exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// The console playground renders the synchronous envelope only, so it must keep
// the 20-URL ceiling even when the job queue is enabled.
func TestPlaygroundKeepsSynchronousCeiling(t *testing.T) {
	srv, _ := newJobTestServer(t, true)
	app := srv.FiberApp()
	app.Post("/v1/api/playground/check", srv.IndexCheckHandler)

	urls := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		urls = append(urls, fmt.Sprintf("https://example.test/p/%d", i))
	}
	body, _ := json.Marshal(map[string]any{"urls": urls, "engines": []string{"baidu"}})
	req := httptest.NewRequest(http.MethodPost, "/v1/api/playground/check", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	payload := readAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(payload, "TOO_MANY_URLS") {
		t.Fatalf("playground must stay synchronous: status=%d body=%s", resp.StatusCode, payload)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer closeTestResource(t, resp.Body)
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
