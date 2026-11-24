package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/time/rate"
)

// 收录批量预算: the request deadline must scale with the batch's rate-limiter
// queue time, otherwise the queued tail fails (engine_queue_exceeded) without
// ever reaching the engine.
func TestIndexCheckBatchDeadline(t *testing.T) {
	// baidu 生产配置: 5 次/分钟 → 12s 一个令牌（实测的安全节奏）。
	baidu := &engineMock{name: "baidu", initialized: true,
		limiter: rate.NewLimiter(rate.Every(12*time.Second), 1)}
	// sogou 生产配置: 8 次/分钟 → 7.5s 一个令牌。
	sogou := &engineMock{name: "sogou", initialized: true,
		limiter: rate.NewLimiter(rate.Every(7500*time.Millisecond), 1)}
	// 人为的慢引擎，用于验证 12 分钟封顶仍然生效。
	slow := &engineMock{name: "slow", initialized: true,
		limiter: rate.NewLimiter(rate.Every(30*time.Second), 1)}
	noLimiter := &engineMock{name: "google", initialized: true}

	base := 36500 * time.Millisecond

	cases := []struct {
		name    string
		urls    int
		engines []SearchEngine
		retries int
		want    time.Duration
	}{
		{
			name:    "single unit: base plus one retry token wait",
			urls:    1,
			engines: []SearchEngine{baidu},
			retries: 1,
			want:    base + 12*time.Second,
		},
		{
			name:    "no limiter info keeps the base budget",
			urls:    20,
			engines: []SearchEngine{noLimiter},
			retries: 1,
			want:    base,
		},
		{
			name:    "20 urls on baidu at 12s: base + last-token queue (39×12s)",
			urls:    20,
			engines: []SearchEngine{baidu},
			retries: 1,
			want:    base + 468*time.Second,
		},
		{
			name:    "20 urls on a 30s engine clamps to the 12min cap",
			urls:    20,
			engines: []SearchEngine{slow},
			retries: 1,
			want:    IndexCheckDeadlineCap,
		},
		{
			name:    "20 urls on sogou fit inside the cap now",
			urls:    20,
			engines: []SearchEngine{sogou},
			retries: 1,
			want:    base + 292500*time.Millisecond,
		},
		{
			name:    "zero retries: queue time counts one attempt per unit",
			urls:    4,
			engines: []SearchEngine{baidu},
			retries: 0,
			want:    base + 36*time.Second,
		},
		{
			name:    "slowest engine wins across the selection",
			urls:    2,
			engines: []SearchEngine{baidu, noLimiter},
			retries: 0,
			want:    base + 12*time.Second,
		},
	}
	for _, tc := range cases {
		if got := IndexCheckBatchDeadline(base, tc.urls, tc.engines, tc.retries); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}

	if got := IndexCheckBatchDeadline(0, 20, []SearchEngine{baidu}, 1); got != 0 {
		t.Errorf("base<=0 must stay unbounded, got %s", got)
	}
}

// The middleware grants index-check batch routes the larger budget while
// every other route keeps the base one.
func TestRequestTimeoutMiddlewareScalesIndexCheckPaths(t *testing.T) {
	base, indexBudget := 100*time.Millisecond, 5*time.Second
	app := fiber.New()
	app.Use(RequestTimeoutMiddleware(base, indexBudget))

	remaining := map[string]time.Duration{}
	record := func(path string) fiber.Handler {
		return func(c *fiber.Ctx) error {
			deadline, ok := c.UserContext().Deadline()
			if ok {
				remaining[path] = time.Until(deadline)
			}
			return c.SendStatus(http.StatusOK)
		}
	}
	app.Get(BasePath+"/v1/index/check", record("index"))
	app.Post(BasePath+"/api/playground/check", record("playground"))
	app.Get(BasePath+"/google/search", record("search"))

	req := httptest.NewRequest(http.MethodGet, BasePath+"/v1/index/check", nil)
	if resp, err := app.Test(req, -1); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("index check request failed: resp=%v err=%v", resp.StatusCode, err)
	}
	preq := httptest.NewRequest(http.MethodPost, BasePath+"/api/playground/check", nil)
	if resp, err := app.Test(preq, -1); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("playground request failed: err=%v", err)
	}
	req = httptest.NewRequest(http.MethodGet, BasePath+"/google/search", nil)
	if resp, err := app.Test(req, -1); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("search request failed: err=%v", err)
	}

	if got := remaining["index"]; got < 4*time.Second {
		t.Errorf("index path remaining budget %s, want ~%s", got, indexBudget)
	}
	if got := remaining["playground"]; got < 4*time.Second {
		t.Errorf("playground path remaining budget %s, want ~%s", got, indexBudget)
	}
	if got := remaining["search"]; got > 200*time.Millisecond {
		t.Errorf("search path remaining budget %s, want ~base(%s)", got, base)
	}
}

// 限流排队死亡必须独立成码: queue-budget failures are not the engine's
// request_timeout, and a genuinely elapsed context stays request_timeout.
func TestMapSearchEngineQueueExceeded(t *testing.T) {
	queueErr := fmt.Errorf("%w: %v", context.DeadlineExceeded, errors.New("rate: Wait(n=1) would exceed context deadline"))
	if spec := mapSearchError(queueErr); spec.code != "engine_queue_exceeded" {
		t.Errorf("queue failure mapped to %q, want engine_queue_exceeded", spec.code)
	}
	if spec := mapSearchError(context.DeadlineExceeded); spec.code != "request_timeout" {
		t.Errorf("bare deadline mapped to %q, want request_timeout", spec.code)
	}
	if !IsRateLimiterQueueErr(errors.New("context deadline exceeded: rate: Wait(n=1) would exceed context deadline")) {
		t.Error("IsRateLimiterQueueErr missed the normalized queue error")
	}
	if IsRateLimiterQueueErr(context.DeadlineExceeded) {
		t.Error("bare context deadline must stay request_timeout, not queue exceeded")
	}
}
