package platform

// Coverage for console roles, IP blacklist and the log maintenance endpoints.

import (
	"net/http"
	"testing"

	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func requireStatus(t *testing.T, got, want int, body any, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status=%d want=%d body=%v", what, got, want, body)
	}
}

func loginAs(t *testing.T, app *fiber.App, username, password string) string {
	t.Helper()
	status, body := doJSON(t, app, http.MethodPost, "/api/auth/login", map[string]any{
		"username": username, "password": password,
	}, nil)
	requireStatus(t, status, http.StatusOK, body, "login "+username)
	token, ok := body["token"].(string)
	if !ok || token == "" {
		t.Fatalf("login %s: missing token: %v", username, body)
	}
	return token
}

func createConsoleUser(t *testing.T, app *fiber.App, adminToken, username, role string) string {
	t.Helper()
	status, body := doJSON(t, app, http.MethodPost, "/api/users", map[string]any{
		"username": username, "password": "pw-123456", "role": role,
	}, adminHeaders(adminToken))
	requireStatus(t, status, http.StatusOK, body, "create user "+username)
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("create user response: %v", body)
	}
	id, _ := user["id"].(string)
	if id == "" {
		t.Fatalf("create user: missing id: %v", body)
	}
	return id
}

func TestConsoleUserRolesAndDisable(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	adminToken := login(t, env.app)

	status, me := doJSON(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(adminToken))
	requireStatus(t, status, http.StatusOK, me, "admin me")
	if me["role"] != RoleAdmin {
		t.Fatalf("admin role: %v", me)
	}

	viewerID := createConsoleUser(t, env.app, adminToken, "viewer1", RoleViewer)
	operatorID := createConsoleUser(t, env.app, adminToken, "operator1", RoleOperator)
	viewerToken := loginAs(t, env.app, "viewer1", "pw-123456")
	operatorToken := loginAs(t, env.app, "operator1", "pw-123456")

	// viewer: read-only, no user management
	status, body := doJSON(t, env.app, http.MethodGet, "/api/users", nil, adminHeaders(viewerToken))
	requireStatus(t, status, http.StatusForbidden, body, "viewer users")
	if body["code"] != "insufficient_role" {
		t.Fatalf("viewer 403 envelope: %v", body)
	}
	status, body = doJSON(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(viewerToken))
	requireStatus(t, status, http.StatusOK, body, "viewer me")
	if body["role"] != RoleViewer {
		t.Fatalf("viewer role: %v", body)
	}

	// operator: can manage the blacklist, not users
	status, body = doJSON(t, env.app, http.MethodGet, "/api/users", nil, adminHeaders(operatorToken))
	requireStatus(t, status, http.StatusForbidden, body, "operator users")
	status, body = doJSON(t, env.app, http.MethodGet, "/api/blacklist", nil, adminHeaders(operatorToken))
	requireStatus(t, status, http.StatusOK, body, "operator blacklist")

	// disabling a user takes effect on an already-issued token
	status, body = doJSON(t, env.app, http.MethodPatch, "/api/users/"+viewerID,
		map[string]any{"status": UserStatusDisabled}, adminHeaders(adminToken))
	requireStatus(t, status, http.StatusOK, body, "disable viewer")
	status, body = doJSON(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(viewerToken))
	requireStatus(t, status, http.StatusUnauthorized, body, "disabled viewer me")

	// last active admin cannot be deleted; operator can be removed.
	users, err := env.p.store.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	adminID := ""
	for _, u := range users {
		if u.Role == RoleAdmin {
			adminID = u.ID
		}
	}
	status, body = doJSON(t, env.app, http.MethodDelete, "/api/users/"+adminID, nil, adminHeaders(adminToken))
	requireStatus(t, status, http.StatusBadRequest, body, "delete last admin")
	status, body = doJSON(t, env.app, http.MethodDelete, "/api/users/"+operatorID, nil, adminHeaders(adminToken))
	requireStatus(t, status, http.StatusOK, body, "delete operator")
}

func TestBlacklistGuardAndExpiry(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	// The guard is mounted by cmd/serve; mount it here in front of the routes.
	app := fiber.New()
	app.Use(env.p.BlacklistGuard())
	env.p.RegisterAdmin(app, nil, nil)
	token := login(t, app)

	status, body := doJSON(t, app, http.MethodPost, "/api/blacklist", map[string]any{
		"kind": "ip", "value": "0.0.0.0", "reason": "test block", "force": true,
	}, adminHeaders(token))
	requireStatus(t, status, http.StatusOK, body, "create blacklist")
	entry, ok := body["entry"].(map[string]any)
	if !ok {
		t.Fatalf("blacklist response: %v", body)
	}
	id, _ := entry["id"].(string)

	// In-process Fiber tests report 0.0.0.0 as c.IP().
	status, body = doJSON(t, app, http.MethodGet, "/api/me", nil, adminHeaders(token))
	requireStatus(t, status, http.StatusForbidden, body, "blacklisted me")
	if body["reason"] != "blacklisted" {
		t.Fatalf("blacklist 403 envelope: %v", body)
	}

	// An expired rule must stop blocking without being deleted.
	e, err := env.p.store.GetBlacklist(id)
	if err != nil || e == nil {
		t.Fatalf("get blacklist: %v %v", e, err)
	}
	past := time.Now().Add(-time.Minute)
	e.ExpiresAt = &past
	if err := env.p.store.PutBlacklist(e); err != nil {
		t.Fatal(err)
	}
	status, body = doJSON(t, app, http.MethodGet, "/api/me", nil, adminHeaders(token))
	requireStatus(t, status, http.StatusOK, body, "expired blacklist me")

	status, body = doJSON(t, app, http.MethodDelete, "/api/blacklist/"+id, nil, adminHeaders(token))
	requireStatus(t, status, http.StatusOK, body, "delete blacklist")

	// Invalid values are rejected with a natural-language message.
	status, body = doJSON(t, app, http.MethodPost, "/api/blacklist", map[string]any{
		"kind": "cidr", "value": "not-an-ip",
	}, adminHeaders(token))
	requireStatus(t, status, http.StatusBadRequest, body, "invalid blacklist")
}

func TestResetBootstrapAdminViaUsersUpdatesGlobalCredential(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	oldToken := login(t, env.app)
	statusUsers, usersRaw := doJSON(t, env.app, http.MethodGet, "/api/users", nil, adminHeaders(oldToken))
	requireStatus(t, statusUsers, http.StatusOK, usersRaw, "list users")
	users := usersRaw
	adminID := ""
	if users != nil {
		for _, u := range users["items"].([]any) {
			entry := u.(map[string]any)
			if entry["username"] == "admin" {
				adminID = entry["id"].(string)
			}
		}
	}
	if adminID == "" {
		t.Fatal("bootstrap admin not found")
	}
	newPassword := "OperatorPass2026!"
	status, body := doJSON(t, env.app, http.MethodPost, "/api/users/"+adminID+"/password",
		map[string]any{"new_password": newPassword}, adminHeaders(oldToken))
	requireStatus(t, status, http.StatusOK, body, "reset bootstrap admin password")
	settingsHash, err := env.p.store.GetSetting(settingAdminHash)
	if err != nil || len(settingsHash) == 0 {
		t.Fatalf("settings hash missing: %v", err)
	}
	if bcrypt.CompareHashAndPassword(settingsHash, []byte(newPassword)) != nil {
		t.Fatal("settings hash not updated to new password")
	}
	custom, _ := env.p.store.GetSetting(settingAdminPasswordCustom)
	if len(custom) == 0 {
		t.Fatal("admin_password_custom marker missing")
	}
	status, _ = doJSON(t, env.app, http.MethodGet, "/api/me", nil, adminHeaders(oldToken))
	requireStatus(t, status, http.StatusUnauthorized, nil, "old token after reset")
	status, body = doJSON(t, env.app, http.MethodPost, "/api/auth/login",
		map[string]any{"username": "admin", "password": newPassword}, nil)
	requireStatus(t, status, http.StatusOK, body, "login with reset password")
}

func TestBlacklistSeedRunsOnlyOnce(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	if err := env.p.SeedBlacklist([]string{"198.51.100.7"}, "test"); err != nil {
		t.Fatal(err)
	}
	entries, err := env.p.store.ListBlacklist()
	if err != nil || len(entries) != 1 {
		t.Fatalf("seed entries=%d err=%v", len(entries), err)
	}
	if _, err := env.p.store.DeleteBlacklist(entries[0].ID); err != nil {
		t.Fatal(err)
	}
	// A restart must not resurrect a rule the operator deleted.
	if err := env.p.SeedBlacklist([]string{"198.51.100.7"}, "test"); err != nil {
		t.Fatal(err)
	}
	entries, err = env.p.store.ListBlacklist()
	if err != nil || len(entries) != 0 {
		t.Fatalf("rule resurrected after restart: entries=%d err=%v", len(entries), err)
	}
}

func TestConfigPasswordChangeSyncsUserRecord(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	adminToken := login(t, env.app)
	_ = adminToken
	env.p.cfg.AdminPassword = "NewStrongPass2026"
	if err := env.p.initAdminPassword(); err != nil {
		t.Fatal(err)
	}
	if err := env.p.bootstrapAdminUser(); err != nil {
		t.Fatal(err)
	}
	status, body := doJSON(t, env.app, http.MethodPost, "/api/auth/login", map[string]any{
		"username": "admin", "password": "NewStrongPass2026",
	}, nil)
	requireStatus(t, status, http.StatusOK, body, "login with changed config password")
	u, err := env.p.store.GetUserByUsername("admin")
	if err != nil || u == nil || u.PasswordHash == nil {
		t.Fatalf("user record not synced: %v %v", u, err)
	}
}

func TestMCPRejectsNonAdminJWT(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	token := login(t, env.app)
	_ = createConsoleUser(t, env.app, token, "mcp_viewer", RoleViewer)
	viewerToken := loginAs(t, env.app, "mcp_viewer", "pw-123456")
	app := fiber.New()
	app.Post("/web-tool/mcp", env.p.AuthAny(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})
	status, body := doJSON(t, app, http.MethodPost, "/web-tool/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, adminHeaders(viewerToken))
	requireStatus(t, status, http.StatusForbidden, body, "viewer MCP")
	if body["code"] != "insufficient_role" {
		t.Fatalf("MCP 403 envelope: %v", body)
	}
}

func TestLogQueryConfigAndCleanupEndpoints(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	token := login(t, env.app)

	status, body := doJSON(t, env.app, http.MethodGet, "/api/logs/config", nil, adminHeaders(token))
	requireStatus(t, status, http.StatusOK, body, "logs config")
	if _, ok := body["retention_days"]; !ok {
		t.Fatalf("logs config missing retention_days: %v", body)
	}

	status, body = doJSON(t, env.app, http.MethodGet, "/api/logs?limit=10&level=info", nil, adminHeaders(token))
	requireStatus(t, status, http.StatusOK, body, "logs query")
	if _, ok := body["items"]; !ok {
		t.Fatalf("logs query missing items: %v", body)
	}

	status, body = doJSON(t, env.app, http.MethodPost, "/api/logs/cleanup", nil, adminHeaders(token))
	requireStatus(t, status, http.StatusOK, body, "logs cleanup")
	if body["ok"] != true {
		t.Fatalf("logs cleanup response: %v", body)
	}

	// viewer can query logs but cannot trigger cleanup.
	_ = createConsoleUser(t, env.app, token, "viewer2", RoleViewer)
	viewerToken := loginAs(t, env.app, "viewer2", "pw-123456")
	status, body = doJSON(t, env.app, http.MethodGet, "/api/logs", nil, adminHeaders(viewerToken))
	requireStatus(t, status, http.StatusOK, body, "viewer logs")
	status, body = doJSON(t, env.app, http.MethodPost, "/api/logs/cleanup", nil, adminHeaders(viewerToken))
	requireStatus(t, status, http.StatusForbidden, body, "viewer logs cleanup")
}
