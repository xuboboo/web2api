package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/xuboboo/web2api/core"
)

const jobTestKey = "sk-test-123"

func TestCheckIndexedLargeBatchBecomesJob(t *testing.T) {
	var submitted []string
	var submittedEngines []string
	deps := Deps{
		SubmitIndexJob: func(_ context.Context, urls []string, engines []string) (any, error) {
			submitted = urls
			submittedEngines = engines
			return map[string]any{
				"job_id": "job_test_1",
				"status": "queued",
				"total":  len(urls),
				"note":   "queued",
			}, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)

	urls := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		urls = append(urls, fmt.Sprintf("https://example.test/%d", i))
	}
	body := rpcMsg(1, "tools/call", map[string]any{
		"name":      toolCheckIndexed,
		"arguments": map[string]any{"urls": urls, "engines": []string{"baidu"}},
	})
	_, _, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	payload := mustResult(t, out)

	if payload["job_id"] != "job_test_1" {
		t.Fatalf("payload = %v", payload)
	}
	if len(submitted) != 25 {
		t.Fatalf("submitted %d URLs, want 25", len(submitted))
	}
	if len(submittedEngines) != 1 || submittedEngines[0] != "baidu" {
		t.Fatalf("engines forwarded as %v", submittedEngines)
	}
}

func TestCheckIndexedQueueFullReturnsRetryableHTTPError(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{
		SubmitIndexJob: func(context.Context, []string, []string) (any, error) {
			return nil, &core.APIError{HTTPStatus: 503, Reason: "JOB_QUEUE_FULL", RetryAfterSeconds: 60}
		},
	})
	body := rpcMsg(1, "tools/call", map[string]any{
		"name":      toolCheckIndexed,
		"arguments": map[string]any{"urls": twentyOneURLs()},
	})
	status, resp, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	if status != 503 || resp.Header.Get("Retry-After") != "60" || out["error"] == nil {
		t.Fatalf("queue-full response: status=%d retry-after=%q body=%v", status, resp.Header.Get("Retry-After"), out)
	}
}

func TestCheckIndexedLargeBatchWithoutJobsIsInvalidParams(t *testing.T) {
	app := newTestApp(t, fakeAuth, Deps{})
	urls := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		urls = append(urls, fmt.Sprintf("https://example.test/%d", i))
	}
	body := rpcMsg(2, "tools/call", map[string]any{
		"name":      toolCheckIndexed,
		"arguments": map[string]any{"urls": urls},
	})
	_, _, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	rpcErr, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON-RPC error, got %v", out)
	}
	if !strings.Contains(fmt.Sprint(rpcErr["message"]), "at most 20") {
		t.Fatalf("error message = %v", rpcErr["message"])
	}
}

func TestGetIndexJobStatusAndResults(t *testing.T) {
	deps := Deps{
		IndexJobStatus: func(_ context.Context, jobID string) (any, error) {
			if jobID != "job_test_2" {
				return nil, fmt.Errorf("job %q not found", jobID)
			}
			return map[string]any{"job_id": jobID, "status": "running", "progress": 0.5, "completed": 5, "total": 10}, nil
		},
		IndexJobResults: func(_ context.Context, jobID string, offset, limit int) (any, error) {
			return map[string]any{
				"job_id": jobID, "offset": offset, "count": 1,
				"results": []map[string]any{{"url": "https://example.test/1", "engines": map[string]any{}}},
			}, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)

	body := rpcMsg(3, "tools/call", map[string]any{
		"name":      toolGetIndexJob,
		"arguments": map[string]any{"job_id": "job_test_2"},
	})
	_, _, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	status := mustResult(t, out)
	if status["status"] != "running" || status["progress"] != 0.5 {
		t.Fatalf("status payload = %v", status)
	}
	if _, hasResults := status["results"]; hasResults {
		t.Fatal("include_results default must not attach rows")
	}

	body = rpcMsg(4, "tools/call", map[string]any{
		"name":      toolGetIndexJob,
		"arguments": map[string]any{"job_id": "job_test_2", "include_results": true, "offset": 5, "limit": 5},
	})
	_, _, out = rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	withResults := mustResult(t, out)
	if _, ok := withResults["results"]; !ok {
		t.Fatalf("include_results=true must attach rows: %v", withResults)
	}
}

func TestGetIndexJobUnknownIDIsToolError(t *testing.T) {
	deps := Deps{
		IndexJobStatus: func(_ context.Context, jobID string) (any, error) {
			return nil, fmt.Errorf("job %q not found", jobID)
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	body := rpcMsg(5, "tools/call", map[string]any{
		"name":      toolGetIndexJob,
		"arguments": map[string]any{"job_id": "missing"},
	})
	_, _, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	if out["error"] != nil {
		t.Fatalf("a missing job is a tool-level failure, not a protocol error: %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result = %v, want isError=true", result)
	}
}

func TestCancelIndexJob(t *testing.T) {
	cancelled := ""
	deps := Deps{
		CancelIndexJob: func(_ context.Context, jobID string) (any, error) {
			cancelled = jobID
			return map[string]any{"job_id": jobID, "status": "cancelled"}, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	body := rpcMsg(6, "tools/call", map[string]any{
		"name":      toolCancelIndexJob,
		"arguments": map[string]any{"job_id": "job_test_3"},
	})
	_, _, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	payload := mustResult(t, out)
	if cancelled != "job_test_3" || payload["status"] != "cancelled" {
		t.Fatalf("cancelled=%q payload=%v", cancelled, payload)
	}
}

func TestToolsListAdvertisesJobTools(t *testing.T) {
	specs := toolSpecs()
	names := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if name, ok := spec["name"].(string); ok {
			names[name] = true
		}
	}
	for _, want := range []string{toolCheckIndexed, toolGetIndexJob, toolCancelIndexJob} {
		if !names[want] {
			t.Errorf("tools/list is missing %s", want)
		}
	}
	// check_indexed must advertise the job ceiling, not the sync one.
	for _, spec := range specs {
		if spec["name"] != toolCheckIndexed {
			continue
		}
		schema := spec["inputSchema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		urls := props["urls"].(map[string]any)
		if urls["maxItems"] != maxJobURLs {
			t.Errorf("check_indexed urls.maxItems = %v, want %d", urls["maxItems"], maxJobURLs)
		}
	}
}

func TestCheckIndexedSmallBatchStaysSynchronous(t *testing.T) {
	called := false
	deps := Deps{
		CheckIndexed: func(_ context.Context, urls []string, engines []string) (any, error) {
			called = true
			return map[string]any{"results": []any{}}, nil
		},
		SubmitIndexJob: func(_ context.Context, urls []string, engines []string) (any, error) {
			t.Fatal("≤20 URLs must not become a job")
			return nil, nil
		},
	}
	app := newTestApp(t, fakeAuth, deps)
	body := rpcMsg(7, "tools/call", map[string]any{
		"name":      toolCheckIndexed,
		"arguments": map[string]any{"urls": []string{"https://example.test/1"}},
	})
	_, _, out := rpc(t, app, body, map[string]string{"X-API-Key": jobTestKey})
	mustResult(t, out)
	if !called {
		t.Fatal("synchronous check was not called")
	}
}
