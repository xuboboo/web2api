package platform

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/xuboboo/web2api/core"
)

func TestScheduleCreateDisabledByDefault(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	opts := core.DefaultServerOptions()
	opts.IndexJobDir = t.TempDir()
	srv := core.NewServerWithOptions("127.0.0.1", 0, opts, consoleJobEngine{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	env.p.engineControl = srv
	env.p.RegisterAdmin(env.app, nil, nil)
	token := login(t, env.app)
	keyID, _ := createKey(t, env, token, "schedule", 100)
	status, body := doJSON(t, env.app, http.MethodPost, "/api/index/check/schedules", map[string]any{"name": "Daily audit", "key_id": keyID, "urls": []string{"https://example.test"}, "engines": []string{"baidu"}, "frequency": "daily", "enabled": true}, adminHeaders(token))
	if status != http.StatusCreated || body["enabled"] != false {
		t.Fatalf("new schedule not safely disabled: %d %v", status, body)
	}
	status, listed := doJSON(t, env.app, http.MethodGet, "/api/index/check/schedules", nil, adminHeaders(token))
	if status != 200 || len(listed["items"].([]any)) != 1 {
		t.Fatalf("list schedule: %d %v", status, listed)
	}
	id := body["id"].(string)
	status, changed := doJSON(t, env.app, http.MethodPatch, "/api/index/check/schedules/"+id, map[string]any{"enabled": true}, adminHeaders(token))
	if status != http.StatusOK || changed["enabled"] != true {
		t.Fatalf("enable schedule: %d %v", status, changed)
	}
	status, _ = doJSON(t, env.app, http.MethodPatch, "/api/index/check/schedules/"+id, map[string]any{"enabled": false}, adminHeaders(token))
	if status != http.StatusOK {
		t.Fatalf("disable schedule status %d", status)
	}
	status, _ = doJSON(t, env.app, http.MethodPost, "/api/index/check/schedules", map[string]any{"name": "bad", "key_id": keyID, "urls": []string{"file:///etc/passwd"}, "engines": []string{"baidu"}, "frequency": "daily"}, adminHeaders(token))
	if status != http.StatusBadRequest {
		t.Fatalf("invalid URL status %d", status)
	}
	longURL := "https://example.test/" + strings.Repeat("a", maxScheduledURLBytes)
	status, _ = doJSON(t, env.app, http.MethodPost, "/api/index/check/schedules", map[string]any{"name": "long URL", "key_id": keyID, "urls": []string{longURL}, "engines": []string{"baidu"}, "frequency": "daily"}, adminHeaders(token))
	if status != http.StatusBadRequest {
		t.Fatalf("overlong URL status %d", status)
	}
}

func TestScheduleCapacityBoundsPersistentSettings(t *testing.T) {
	base := map[string]scheduledJob{
		"one": {UserID: "owner", URLs: make([]string, 5000)},
	}
	if scheduleCapacityExceeded(base, "owner", 5000) {
		t.Fatal("capacity should allow the 10000 URL limit")
	}
	if !scheduleCapacityExceeded(base, "owner", 5001) {
		t.Fatal("capacity should reject URL total above 10000")
	}
	for i := 1; i < maxSchedulesPerUser; i++ {
		base[fmt.Sprintf("user-%d", i)] = scheduledJob{UserID: "owner", URLs: []string{"https://example.test"}}
	}
	if !scheduleCapacityExceeded(base, "owner", 1) {
		t.Fatal("capacity should reject more than 20 schedules for one user")
	}
	total := make(map[string]scheduledJob, maxSchedulesTotal)
	for i := 0; i < maxSchedulesTotal; i++ {
		total[fmt.Sprintf("schedule-%d", i)] = scheduledJob{UserID: fmt.Sprintf("owner-%d", i), URLs: []string{"https://example.test"}}
	}
	if !scheduleCapacityExceeded(total, "new-owner", 1) {
		t.Fatal("capacity should reject more than 100 schedules globally")
	}
}
