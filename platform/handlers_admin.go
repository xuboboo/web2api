package platform

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
	"golang.org/x/crypto/bcrypt"
)

// apiKeyView is the admin-facing key object (never contains the full key).
// S4-GAP 密钥增强 adds expires_at / allowed_ips (additive; nil/empty keeps
// the pre-S4 shape for clients that do not know the fields).
type apiKeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Status     string     `json:"status"`
	DailyQuota int64      `json:"daily_quota"`
	UsedToday  int64      `json:"used_today"`
	UsedTotal  int64      `json:"used_total"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	AllowedIPs []string   `json:"allowed_ips"`
}

// keyCreatedView additionally carries the one-time full key.
type keyCreatedView struct {
	apiKeyView
	Key string `json:"key"`
}

func newKeyView(k *APIKey, now time.Time) apiKeyView {
	allowedIPs := k.AllowedIPs
	if allowedIPs == nil {
		allowedIPs = []string{}
	}
	return apiKeyView{
		ID:         k.ID,
		Name:       k.Name,
		Prefix:     k.Prefix,
		Status:     k.Status,
		DailyQuota: k.DailyQuota,
		UsedToday:  usedTodayUnits(k, now),
		UsedTotal:  k.UsedTotal,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
		AllowedIPs: allowedIPs,
	}
}

// EngineControl is the cooldown management surface the platform needs from
// the engine orchestration layer (契约 A 管理面). It is defined here so the
// platform never imports core: *core.Server satisfies it structurally and the
// serve command injects it via RegisterAdmin. A nil EngineControl degrades
// the cooldown endpoints to 503 engine_control_unavailable and keeps
// GET /api/engines on the legacy shape.
type EngineControl interface {
	// EnginesStatusWithCooldown renders the per-engine snapshot with the
	// cooldown extension fields {state, until, reason, consecutive_blocks,
	// last_error_at}.
	EnginesStatusWithCooldown() any
	// ResetCooldown force-recovers one engine's cooldown (强制恢复).
	ResetCooldown(name string) error
	// ForceCooldown puts one engine into a maintenance-window cooldown
	// (强制冷却) for minutes.
	ForceCooldown(name string, minutes int) error
}

// RegisterAdmin mounts the /api/* endpoints on the given router (pass the root
// fiber app; paths are registered verbatim). Everything except
// POST /api/auth/login requires a valid admin JWT.
//
// 契约 A/B extensions over the first delivery round:
//   - indexCheck is mounted as POST /api/playground/check behind the JWT guard
//     (not billed; the response structure is identical to /v1/index/check);
//   - engineControl wires GET /api/engines cooldown fields and the
//     POST /api/engines/:name/cooldown/{reset,force} management endpoints;
//   - every /api/* route sits behind the admin rate limiter (global budget
//     security.admin_rate_per_minute + S4-B-05 per-IP buckets → 429+Retry-After);
//   - POST /api/auth/password changes the admin password.
//
// S4 additions (只增不改):
//   - POST /api/auth/logout, POST /api/auth/refresh (会话系统);
//   - GET /api/runtime (B-07 版本指纹收敛的运行时信息落点);
//   - GET /api/sessions, DELETE /api/sessions/:id (会话管理);
//   - POST /api/keys/:id/rotate (密钥轮换);
//   - GET /api/audit (审计轨迹查询);
//   - GET /api/usage/export (用量导出), GET /api/reconcile (对账);
//   - GET /api/admin/backup (bbolt 一致性快照下载).
func (p *Platform) RegisterAdmin(app fiber.Router, indexCheck fiber.Handler, engineControl EngineControl) {
	p.engineControl = engineControl
	limiter := p.adminRateGuard()

	app.Post("/api/auth/login", limiter, p.handleLogin)
	app.Post("/api/auth/password", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handlePasswordChange)
	app.Post("/api/auth/logout", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleLogout)
	app.Post("/api/auth/refresh", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleAuthRefresh)
	app.Get("/api/me", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleMe)
	app.Get("/api/runtime", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleRuntime)
	app.Post("/api/keys", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleCreateKey)
	app.Get("/api/keys", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleListKeys)
	app.Patch("/api/keys/:id", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleUpdateKey)
	app.Post("/api/keys/:id/rotate", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleRotateKey)
	app.Delete("/api/keys/:id", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleDeleteKey)
	app.Get("/api/stats/overview", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleStatsOverview)
	app.Get("/api/stats/timeseries", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleStatsTimeseries)
	app.Get("/api/usage", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleUsage)
	app.Get("/api/usage/export", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleUsageExport)
	app.Get("/api/reconcile", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleReconcile)
	app.Get("/api/engines", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleAdminEngines)
	app.Post("/api/engines/:name/cooldown/reset", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleCooldownReset)
	app.Post("/api/engines/:name/cooldown/force", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleCooldownForce)
	app.Get("/api/sessions", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleListSessions)
	app.Delete("/api/sessions/:id", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleRevokeSession)
	app.Get("/api/audit", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleAudit)
	app.Get("/api/admin/backup", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleBackupDownload)
	// 新增: 用户与角色 / 黑名单 / 日志。
	app.Get("/api/users", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleListUsers)
	app.Post("/api/users", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleCreateUser)
	app.Patch("/api/users/:id", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleUpdateUser)
	app.Delete("/api/users/:id", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleDeleteUser)
	app.Post("/api/users/:id/password", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleResetUserPassword)
	app.Get("/api/blacklist", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleListBlacklist)
	app.Post("/api/blacklist", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleCreateBlacklist)
	app.Patch("/api/blacklist/:id", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleUpdateBlacklist)
	app.Delete("/api/blacklist/:id", limiter, p.jwtGuard(), p.requireRole(RoleOperator), p.handleDeleteBlacklist)
	app.Get("/api/logs", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleLogs)
	app.Get("/api/logs/config", limiter, p.jwtGuard(), p.requireRole(RoleViewer), p.handleLogsConfig)
	app.Post("/api/logs/cleanup", limiter, p.jwtGuard(), p.requireRole(RoleAdmin), p.handleLogsCleanup)
	if indexCheck != nil {
		app.Post("/api/playground/check", limiter, p.jwtGuard(), p.requireRole(RoleViewer), indexCheck)
	}
}

// handleLogin answers POST /api/auth/login {"username","password"} with
// {"token","expires_in","user":{"username"}}; any credential mismatch is 401
// {"error":"unauthorized"}; a locked username answers 429
// {"error":"too_many_attempts"} + Retry-After even with correct credentials.
// The lock is per username across source IPs: rotating IPs must not reset the
// brute-force budget. IP is retained in logs and audit records for triage.
//
// S4 additions: B-04 登录时延一致 — an unknown username also runs one bcrypt
// comparison against a package-level dummy hash, so response timing no longer
// enumerates the admin username. S4-GAP 会话系统 — the response token is bound
// to a stored session and an httpOnly SameSite=Strict cookie is set alongside
// (Bearer 契约不变, body 仍返回 token). S4-GAP 审计 — success/failure/lockout
// land in the audit trail.
func (p *Platform) handleLogin(c *fiber.Ctx) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	u, err := p.store.GetUserByUsername(req.Username)
	if err != nil {
		return errInternal(c, err)
	}
	if u == nil {
		u = p.legacyAdminUser(req.Username)
	}
	usernameOK := u != nil && u.Status == UserStatusActive
	if usernameOK {
		p.adminAuthMu.Lock()
		defer p.adminAuthMu.Unlock()
	}
	loginName := normalizeUsername(req.Username)
	now := time.Now()
	if lockedUntil, locked := p.loginLockedAt(loginName, now); locked {
		remaining := int((lockedUntil.Sub(now) + time.Second - time.Nanosecond) / time.Second)
		logrus.WithFields(logrus.Fields{
			"username": req.Username, "ip": c.IP(),
			"locked_until": lockedUntil.Format(time.RFC3339),
		}).Warn("platform: 登录锁定中,拒绝登录尝试")
		p.auditFrom(c, "", AuditActionLoginLockout, req.Username, "locked")
		return errTooManyAttempts(c, remaining)
	}
	if !usernameOK {
		// S4-B-04: equalize the timing by running the same bcrypt work.
		_ = bcrypt.CompareHashAndPassword(dummyAdminHash, []byte(req.Password))
	}
	if !verifyUserPassword(u, req.Password) || !usernameOK {
		p.recordLoginFailure(loginName, c.IP(), now)
		core.Metrics().IncLoginFailure()
		p.auditFrom(c, req.Username, AuditActionLoginFailed, req.Username, "401")
		return errUnauthorized(c)
	}
	p.clearLoginFailures(loginName)
	version, err := p.tokenVersion()
	if err != nil {
		return errInternal(c, err)
	}
	session, err := p.createUserSession(u, c.IP(), c.Get(fiber.HeaderUserAgent), p.jwtTTL())
	if err != nil {
		return errInternal(c, err)
	}
	token, err := p.issueUserToken(u, session.ID, version)
	if err != nil {
		return errUnauthorized(c)
	}
	p.setSessionCookie(c, token, p.jwtTTL())
	p.auditFrom(c, req.Username, AuditActionLoginSuccess, req.Username, "ok")
	// 登录成功日志缺口补齐(S4-GAP): the failure/lockout paths already logged.
	logrus.WithFields(logrus.Fields{"ip": c.IP()}).Info("platform: 管理员登录成功")
	return c.JSON(fiber.Map{
		"token":      token,
		"expires_in": int64(p.jwtTTL() / time.Second),
		"user":       fiber.Map{"username": u.Username, "role": u.Role, "user_id": u.ID},
	})
}

// setSessionCookie writes the httpOnly admin session cookie (S4-B-12):
// HttpOnly; SameSite=Strict; Path=/web-tool; Max-Age=expires_in; Secure when
// the request arrived over https, using Fiber's trusted-proxy-aware protocol
// detection (反代 TLS 场景).
func (p *Platform) setSessionCookie(c *fiber.Ctx, token string, ttl time.Duration) {
	secure := strings.EqualFold(strings.TrimSpace(c.Protocol()), "https")
	parts := []string{
		adminSessionCookie + "=" + token,
		"Path=" + core.BasePath,
		"HttpOnly",
		"SameSite=Strict",
		"Max-Age=" + itoa(int(ttl/time.Second)),
	}
	if secure {
		parts = append(parts, "Secure")
	}
	c.Set(fiber.HeaderSetCookie, strings.Join(parts, "; "))
}

// clearSessionCookie expires the admin session cookie.
func clearSessionCookie(c *fiber.Ctx) {
	c.Set(fiber.HeaderSetCookie, adminSessionCookie+"=; Path="+core.BasePath+"; HttpOnly; SameSite=Strict; Max-Age=0")
}

// handlePasswordChange answers POST /api/auth/password
// {"old_password","new_password"}: the new bcrypt hash is stored in the
// settings bucket and overrides the config default; token_version is bumped so
// every previously issued token is invalidated immediately, and every stored
// admin session is revoked (S4-GAP 会话). The response carries a fresh token —
// bound to a new session — so the operator stays logged in. S4-B-11: the new
// password is validated against security.password_policy with a
// natural-language rejection.
func (p *Platform) handlePasswordChange(c *fiber.Ctx) error {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	p.adminAuthMu.Lock()
	defer p.adminAuthMu.Unlock()
	version, err := p.tokenVersion()
	if err != nil {
		return errInternal(c, err)
	}
	authorizedVersion, ok := c.Locals(localAdminTokenVersion).(int)
	if !ok || authorizedVersion != version {
		return errUnauthorized(c)
	}
	u, ok := p.currentRequestUser(c)
	if !ok {
		return errUnauthorized(c)
	}
	if !verifyUserPassword(u, req.OldPassword) {
		core.Metrics().IncLoginFailure()
		return errUnauthorized(c)
	}
	newPassword := strings.TrimSpace(req.NewPassword)
	if msg := p.validatePassword(newPassword); msg != "" {
		return errBadRequestMsg(c, "INVALID_PARAM", msg)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return errInternal(c, err)
	}
	// Non-admin accounts change only their own user record; the global admin
	// credential/token_version stays untouched. Admin changes keep the original
	// contract: settings hash override + global invalidation.
	// Only the configured bootstrap admin owns the global credential settings.
	// Other admins change just their own user record and sessions.
	isAdmin := u.Role == RoleAdmin && u.Username == normalizeUsername(p.cfg.AdminUsername)
	revoked := 0
	if isAdmin {
		if version >= 1<<30 {
			return errInternal(c, fmt.Errorf("admin token version limit reached"))
		}
		version++
		if err := p.store.PutSettings(map[string][]byte{
			settingAdminHash:           hash,
			settingAdminPasswordCustom: []byte("1"),
			settingTokenVersion:        []byte(strconv.Itoa(version)),
		}); err != nil {
			return errInternal(c, err)
		}
		p.setAdminHash(hash)
		revoked, err = p.revokeAllSessions()
	} else {
		revoked, err = p.revokeUserSessions(u.ID)
	}
	if err != nil {
		logrus.WithError(err).Warn("platform: could not mark old sessions revoked after password change")
	}
	u.PasswordHash = hash
	if err := p.store.PutUser(u); err != nil {
		return errInternal(c, err)
	}
	session, err := p.createUserSession(u, c.IP(), c.Get(fiber.HeaderUserAgent), p.jwtTTL())
	if err != nil {
		return errInternal(c, err)
	}
	token, err := p.issueUserToken(u, session.ID, version)
	if err != nil {
		return errInternal(c, err)
	}
	p.setSessionCookie(c, token, p.jwtTTL())
	p.auditFrom(c, u.Username, AuditActionPasswordChange, u.Username,
		fmt.Sprintf("ok,revoked_sessions=%d", revoked))
	if isAdmin {
		logrus.Info("platform: 管理员密码已修改,全部旧 token 与会话已失效")
	} else {
		logrus.WithField("username", u.Username).Info("platform: 用户密码已修改,该用户会话已失效")
	}
	return c.JSON(fiber.Map{
		"ok":            true,
		"token":         token,
		"expires_in":    int64(p.jwtTTL() / time.Second),
		"token_version": version,
	})
}

// handleAdminEngines answers GET /api/engines: the engine snapshot extended
// with the cooldown fields {state, until, reason, consecutive_blocks,
// last_error_at} when an EngineControl was injected; otherwise the legacy
// shape (degradation per the assembly contract).
func (p *Platform) handleAdminEngines(c *fiber.Ctx) error {
	if p.engineControl != nil {
		return c.JSON(fiber.Map{"engines": p.engineControl.EnginesStatusWithCooldown()})
	}
	return c.JSON(fiber.Map{"engines": p.engineStatusValue()})
}

// handleCooldownReset answers POST /api/engines/:name/cooldown/reset →
// {"ok":true,"engine":name} (强制恢复). 503 engine_control_unavailable without
// an EngineControl; 400 for an unknown engine name.
func (p *Platform) handleCooldownReset(c *fiber.Ctx) error {
	if p.engineControl == nil {
		return errEngineControlUnavailable(c)
	}
	name := c.Params("name")
	if err := p.engineControl.ResetCooldown(name); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	p.auditFrom(c, "", AuditActionCooldownReset, name, "ok")
	return c.JSON(fiber.Map{"ok": true, "engine": name})
}

// handleCooldownForce answers POST /api/engines/:name/cooldown/force
// {"minutes":N} → {"ok":true,"engine":name,"minutes":N} (强制冷却,维护窗口).
func (p *Platform) handleCooldownForce(c *fiber.Ctx) error {
	if p.engineControl == nil {
		return errEngineControlUnavailable(c)
	}
	var req struct {
		Minutes int `json:"minutes"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	name := c.Params("name")
	if err := p.engineControl.ForceCooldown(name, req.Minutes); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	p.auditFrom(c, "", AuditActionCooldownForce, name, fmt.Sprintf("minutes=%d", req.Minutes))
	return c.JSON(fiber.Map{"ok": true, "engine": name, "minutes": req.Minutes})
}

// handleMe answers GET /api/me. S4-B-07: the real app version is exposed here
// (authenticated endpoint) since it left the public /health.
func (p *Platform) handleMe(c *fiber.Ctx) error {
	keys, err := p.store.ListKeys()
	if err != nil {
		return errInternal(c, err)
	}
	agg, err := p.store.UsageAggregate()
	if err != nil {
		return errInternal(c, err)
	}
	role, _ := c.Locals(localAdminRole).(string)
	uid, _ := c.Locals(localAdminUserID).(string)
	username, _ := c.Locals(localAdminUsername).(string)
	if username == "" {
		username = p.cfg.AdminUsername
	}
	resp := fiber.Map{
		"username":    username,
		"role":        role,
		"user_id":     uid,
		"keys_count":  len(keys),
		"today_calls": agg.TodayCalls,
		"total_calls": agg.TotalCalls,
	}
	if v := p.Version(); v != "" {
		resp["version"] = v
	}
	return c.JSON(resp)
}

// handleCreateKey answers POST /api/keys {"name","daily_quota"?,
// "expires_at"?,"allowed_ips"?}. The full key (sk-w2a-<32 hex>) is returned
// exactly once; storage keeps only its hash. expires_at accepts RFC3339 or
// the literal null (omit = never expires); allowed_ips accepts bare IPs and
// CIDR ranges (empty = unrestricted).
func (p *Platform) handleCreateKey(c *fiber.Ctx) error {
	var req struct {
		Name       string   `json:"name"`
		DailyQuota *int64   `json:"daily_quota"`
		ExpiresAt  *string  `json:"expires_at"`
		AllowedIPs []string `json:"allowed_ips"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errBadRequest(c, "INVALID_PARAM")
	}
	var quota int64
	if req.DailyQuota != nil {
		if *req.DailyQuota < 0 {
			return errBadRequest(c, "INVALID_PARAM")
		}
		quota = *req.DailyQuota
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		parsed, err := parseTimeParam(*req.ExpiresAt)
		if err != nil {
			return errBadRequestMsg(c, "INVALID_PARAM", "过期时间格式无效,应为 RFC3339(如 2026-12-31T23:59:59+08:00)")
		}
		expiresAt = &parsed
	}
	allowedIPs, errIP := normalizeAllowedIPs(req.AllowedIPs)
	if errIP != nil {
		return errBadRequestMsg(c, "INVALID_PARAM", "IP 白名单格式无效: "+errIP.Error())
	}
	now := time.Now()
	full, hash := generateAPIKeyMaterial()
	key := &APIKey{
		ID:         newKeyID(),
		Name:       name,
		KeyHash:    hash,
		Prefix:     full[:keyPrefixDisplayLen],
		Status:     KeyStatusActive,
		DailyQuota: quota,
		CreatedAt:  now,
		TodayDate:  localDate(now),
		ExpiresAt:  expiresAt,
		AllowedIPs: allowedIPs,
	}
	if err := p.store.PutKey(key); err != nil {
		return errInternal(c, err)
	}
	p.auditFrom(c, "", AuditActionKeyCreate, key.Name, "ok")
	view := newKeyView(key, now)
	return c.JSON(keyCreatedView{apiKeyView: view, Key: full})
}

// handleListKeys answers GET /api/keys (newest first).
func (p *Platform) handleListKeys(c *fiber.Ctx) error {
	keys, err := p.store.ListKeys()
	if err != nil {
		return errInternal(c, err)
	}
	now := time.Now()
	views := make([]apiKeyView, 0, len(keys))
	for i := range keys {
		views = append(views, newKeyView(&keys[i], now))
	}
	return c.JSON(views)
}

// handleUpdateKey answers PATCH /api/keys/:id
// {"name"?,"status"?,"daily_quota"?, "expires_at"?, "allowed_ips"?}. S4-GAP
// 密钥增强: expires_at accepts RFC3339 or null/"" (clears); allowed_ips
// accepts bare IPs / CIDR ranges, empty list clears the restriction.
func (p *Platform) handleUpdateKey(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Name       *string `json:"name"`
		Status     *string `json:"status"`
		DailyQuota *int64  `json:"daily_quota"`
		// ExpiresAt is raw so an explicit JSON null (clears the expiry) is
		// distinguishable from an absent field.
		ExpiresAt  json.RawMessage `json:"expires_at"`
		AllowedIPs []string        `json:"allowed_ips"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	var update APIKey
	changed := []string{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return errBadRequest(c, "INVALID_PARAM")
		}
		update.Name = name
		changed = append(changed, "name")
	}
	if req.Status != nil {
		if *req.Status != KeyStatusActive && *req.Status != KeyStatusDisabled {
			return errBadRequest(c, "INVALID_PARAM")
		}
		update.Status = *req.Status
		changed = append(changed, "status")
	}
	if req.DailyQuota != nil {
		if *req.DailyQuota < 0 {
			return errBadRequest(c, "INVALID_PARAM")
		}
		update.DailyQuota = *req.DailyQuota
		changed = append(changed, "daily_quota")
	}
	if req.ExpiresAt != nil {
		raw := strings.TrimSpace(string(req.ExpiresAt))
		if raw != "" && raw != "null" {
			var rawTime string
			if err := json.Unmarshal(req.ExpiresAt, &rawTime); err != nil {
				return errBadRequestMsg(c, "INVALID_PARAM", "过期时间格式无效,应为 RFC3339(如 2026-12-31T23:59:59+08:00)")
			}
			parsed, perr := parseTimeParam(rawTime)
			if perr != nil {
				return errBadRequestMsg(c, "INVALID_PARAM", "过期时间格式无效,应为 RFC3339(如 2026-12-31T23:59:59+08:00)")
			}
			update.ExpiresAt = &parsed
		}
		changed = append(changed, "expires_at")
	}
	if req.AllowedIPs != nil {
		normalized, errIP := normalizeAllowedIPs(req.AllowedIPs)
		if errIP != nil {
			return errBadRequestMsg(c, "INVALID_PARAM", "IP 白名单格式无效: "+errIP.Error())
		}
		update.AllowedIPs = normalized
		changed = append(changed, "allowed_ips")
	}
	key, err := p.store.UpdateKey(id, func(current *APIKey) {
		if req.Name != nil {
			current.Name = update.Name
		}
		if req.Status != nil {
			current.Status = update.Status
		}
		if req.DailyQuota != nil {
			current.DailyQuota = update.DailyQuota
		}
		if req.ExpiresAt != nil {
			current.ExpiresAt = update.ExpiresAt
		}
		if req.AllowedIPs != nil {
			current.AllowedIPs = update.AllowedIPs
		}
	})
	if err != nil {
		return errInternal(c, err)
	}
	if key == nil {
		return errNotFound(c)
	}
	p.auditFrom(c, "", AuditActionKeyUpdate, key.Name, strings.Join(changed, ","))
	return c.JSON(newKeyView(key, time.Now()))
}

// handleDeleteKey answers DELETE /api/keys/:id → {"ok":true}.
func (p *Platform) handleDeleteKey(c *fiber.Ctx) error {
	id := c.Params("id")
	key, _ := p.store.GetKeyByID(id)
	name := ""
	if key != nil {
		name = key.Name
	}
	existed, err := p.store.DeleteKey(id)
	if err != nil {
		return errInternal(c, err)
	}
	if !existed {
		return errNotFound(c)
	}
	p.auditFrom(c, "", AuditActionKeyDelete, name, "ok")
	return c.JSON(fiber.Map{"ok": true})
}

// handleStatsOverview answers GET /api/stats/overview. Aggregation is pushed
// down to the store so the response time does not degrade with journal size.
// B-01/P-01 口径: today_calls/total_calls/success_rate_24h count REACHED
// calls (参数错误、配额与限速拒绝、冷却快速失败不计入); the additive fields
// today_calls_all / rejected_today_24h expose the full denominator (additive,
// 旧字段语义在 docs/API.md 记录).
func (p *Platform) handleStatsOverview(c *fiber.Ctx) error {
	agg, err := p.store.UsageAggregate()
	if err != nil {
		return errInternal(c, err)
	}
	keys, err := p.store.ListKeys()
	if err != nil {
		return errInternal(c, err)
	}

	// 近 24h 无(有效)调用时返回 null(前端渲染为「—」),避免空态误显示 100%。
	var successRate interface{}
	if agg.Calls24h > 0 {
		successRate = math.Round(float64(agg.OK24h)/float64(agg.Calls24h)*10000) / 100
	}

	activeKeys := 0
	for _, k := range keys {
		if k.Status == KeyStatusActive {
			activeKeys++
		}
	}

	byEngine := map[string]EngineStat{
		"baidu": {},
		"sogou": {},
		"so360": {},
		"bing":  {},
	}
	for engine, s := range agg.ByEngine {
		if engine == "" {
			continue
		}
		byEngine[engine] = s
	}

	return c.JSON(fiber.Map{
		"today_calls":        agg.TodayCalls,
		"today_units":        agg.TodayUnits,
		"total_calls":        agg.TotalCalls,
		"total_units":        agg.TotalUnits,
		"active_keys":        activeKeys,
		"success_rate_24h":   successRate,
		"by_engine":          byEngine,
		"today_calls_all":    agg.TodayCallsAll,
		"rejected_today_24h": agg.Rejected24h,
	})
}

// handleStatsTimeseries answers GET /api/stats/timeseries?days=14 with one
// {"date","calls","units","ok"} item per local day, ascending, zero-filled.
// Bucketing is pushed down to the store.
func (p *Platform) handleStatsTimeseries(c *fiber.Ctx) error {
	days := 14
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			days = v
		}
	}
	if days > 90 {
		days = 90
	}

	out, err := p.store.UsageTimeseries(days)
	if err != nil {
		return errInternal(c, err)
	}
	return c.JSON(out)
}

// usageItemView matches the contract fields for GET /api/usage items.
// S4-B-01 adds the outcome verdict (billed/engine_failed/rejected) additively;
// pre-S4 records carry "" (treated as reached by consumers).
type usageItemView struct {
	Time     time.Time `json:"time"`
	KeyID    string    `json:"key_id"`
	KeyName  string    `json:"key_name"`
	Endpoint string    `json:"endpoint"`
	Engines  []string  `json:"engines"`
	URLs     int       `json:"urls"`
	Units    int       `json:"units"`
	Status   int       `json:"status"`
	TookMS   int64     `json:"took_ms"`
	Outcome  string    `json:"outcome"`
}

// handleUsage answers GET /api/usage?limit=50&offset=0&key_id=&from=&to= —
// newest first, with the filtered total for pagination. Pagination/filtering
// is pushed down to the store so a large journal cannot slow the admin log
// down. S4-GAP 密钥增强: from/to bound the page by record time (accepts
// YYYY-MM-DD or RFC3339; to is inclusive for a date).
func (p *Platform) handleUsage(c *fiber.Ctx) error {
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 200 {
		limit = 200
	}
	offset := 0
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			offset = v
		}
	}
	keyID := strings.TrimSpace(c.Query("key_id"))

	from, err := parseOptionalTimeQuery(c.Query("from"), false)
	if err != nil {
		return errBadRequestMsg(c, "INVALID_PARAM", "from 时间格式无效,应为 YYYY-MM-DD 或 RFC3339")
	}
	to, err := parseOptionalTimeQuery(c.Query("to"), true)
	if err != nil {
		return errBadRequestMsg(c, "INVALID_PARAM", "to 时间格式无效,应为 YYYY-MM-DD 或 RFC3339")
	}

	records, total, err := p.store.ListUsagePage(UsagePageFilter{
		KeyID:  keyID,
		From:   from,
		To:     to,
		Offset: offset,
		Limit:  limit,
	})
	if err != nil {
		return errInternal(c, err)
	}

	items := make([]usageItemView, 0, len(records))
	for _, r := range records {
		engines := r.Engines
		if engines == nil {
			engines = []string{}
		}
		items = append(items, usageItemView{
			Time:     r.Time,
			KeyID:    r.KeyID,
			KeyName:  r.KeyName,
			Endpoint: r.Endpoint,
			Engines:  engines,
			URLs:     r.URLs,
			Units:    r.Units,
			Status:   r.Status,
			TookMS:   r.TookMS,
			Outcome:  r.Outcome,
		})
	}

	return c.JSON(fiber.Map{
		"total": total,
		"items": items,
	})
}
