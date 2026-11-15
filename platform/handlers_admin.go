package platform

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// apiKeyView is the admin-facing key object (never contains the full key).
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
}

// keyCreatedView additionally carries the one-time full key.
type keyCreatedView struct {
	apiKeyView
	Key string `json:"key"`
}

func newKeyView(k *APIKey, now time.Time) apiKeyView {
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
//   - every /api/* route sits behind the global admin rate limiter
//     (security.admin_rate_per_minute, default 60 req/min → 429+Retry-After);
//   - POST /api/auth/password changes the admin password.
func (p *Platform) RegisterAdmin(app fiber.Router, indexCheck fiber.Handler, engineControl EngineControl) {
	p.engineControl = engineControl
	limiter := p.adminRateGuard()

	app.Post("/api/auth/login", limiter, p.handleLogin)
	app.Post("/api/auth/password", limiter, p.jwtGuard(), p.handlePasswordChange)
	app.Get("/api/me", limiter, p.jwtGuard(), p.handleMe)
	app.Post("/api/keys", limiter, p.jwtGuard(), p.handleCreateKey)
	app.Get("/api/keys", limiter, p.jwtGuard(), p.handleListKeys)
	app.Patch("/api/keys/:id", limiter, p.jwtGuard(), p.handleUpdateKey)
	app.Delete("/api/keys/:id", limiter, p.jwtGuard(), p.handleDeleteKey)
	app.Get("/api/stats/overview", limiter, p.jwtGuard(), p.handleStatsOverview)
	app.Get("/api/stats/timeseries", limiter, p.jwtGuard(), p.handleStatsTimeseries)
	app.Get("/api/usage", limiter, p.jwtGuard(), p.handleUsage)
	app.Get("/api/engines", limiter, p.jwtGuard(), p.handleAdminEngines)
	app.Post("/api/engines/:name/cooldown/reset", limiter, p.jwtGuard(), p.handleCooldownReset)
	app.Post("/api/engines/:name/cooldown/force", limiter, p.jwtGuard(), p.handleCooldownForce)
	if indexCheck != nil {
		app.Post("/api/playground/check", limiter, p.jwtGuard(), indexCheck)
	}
}

// handleLogin answers POST /api/auth/login {"username","password"} with
// {"token","expires_in","user":{"username"}}; any credential mismatch is 401
// {"error":"unauthorized"}; a locked username+IP pair answers 429
// {"error":"too_many_attempts"} + Retry-After even with correct credentials.
func (p *Platform) handleLogin(c *fiber.Ctx) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	now := time.Now()
	if lockedUntil, locked := p.loginLockedAt(req.Username, c.IP(), now); locked {
		remaining := int((lockedUntil.Sub(now) + time.Second - time.Nanosecond) / time.Second)
		logrus.WithFields(logrus.Fields{
			"username": req.Username, "ip": c.IP(),
			"locked_until": lockedUntil.Format(time.RFC3339),
		}).Warn("platform: 登录锁定中,拒绝登录尝试")
		return errTooManyAttempts(c, remaining)
	}
	if req.Username != p.cfg.AdminUsername ||
		bcrypt.CompareHashAndPassword(p.adminHash, []byte(req.Password)) != nil {
		p.recordLoginFailure(req.Username, c.IP(), now)
		return errUnauthorized(c)
	}
	p.clearLoginFailures(req.Username, c.IP())
	token, err := p.issueToken(req.Username)
	if err != nil {
		return errUnauthorized(c)
	}
	return c.JSON(fiber.Map{
		"token":      token,
		"expires_in": int64(p.jwtTTL() / time.Second),
		"user":       fiber.Map{"username": req.Username},
	})
}

// handlePasswordChange answers POST /api/auth/password
// {"old_password","new_password"} (新密码≥8位): the new bcrypt hash is stored
// in the settings bucket and overrides the config default; token_version is
// bumped so every previously issued token is invalidated immediately. The
// response carries a fresh token at the new version so the operator stays
// logged in.
func (p *Platform) handlePasswordChange(c *fiber.Ctx) error {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	if bcrypt.CompareHashAndPassword(p.adminHash, []byte(req.OldPassword)) != nil {
		return errUnauthorized(c)
	}
	newPassword := strings.TrimSpace(req.NewPassword)
	if utf8.RuneCountInString(newPassword) < 8 {
		return errBadRequest(c, "INVALID_PARAM")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return errInternal(c, err)
	}
	if err := p.store.PutSetting(settingAdminHash, hash); err != nil {
		return errInternal(c, err)
	}
	if err := p.store.PutSetting(settingAdminPasswordCustom, []byte("1")); err != nil {
		return errInternal(c, err)
	}
	p.adminHash = hash
	version := p.bumpTokenVersion()
	token, err := p.issueToken(p.cfg.AdminUsername)
	if err != nil {
		return errInternal(c, err)
	}
	logrus.Info("platform: 管理员密码已修改,全部旧 token 已失效")
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
	return c.JSON(fiber.Map{"ok": true, "engine": name, "minutes": req.Minutes})
}

// handleMe answers GET /api/me.
func (p *Platform) handleMe(c *fiber.Ctx) error {
	keys, err := p.store.ListKeys()
	if err != nil {
		return errInternal(c, err)
	}
	agg, err := p.store.UsageAggregate()
	if err != nil {
		return errInternal(c, err)
	}
	return c.JSON(fiber.Map{
		"username":    p.cfg.AdminUsername,
		"keys_count":  len(keys),
		"today_calls": agg.TodayCalls,
		"total_calls": agg.TotalCalls,
	})
}

// handleCreateKey answers POST /api/keys {"name","daily_quota"?}. The full
// key (sk-w2a-<32 hex>) is returned exactly once; storage keeps only its hash.
func (p *Platform) handleCreateKey(c *fiber.Ctx) error {
	var req struct {
		Name       string `json:"name"`
		DailyQuota *int64 `json:"daily_quota"`
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
	}
	if err := p.store.PutKey(key); err != nil {
		return errInternal(c, err)
	}
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

// handleUpdateKey answers PATCH /api/keys/:id {"name"?,"status"?,"daily_quota"?}.
func (p *Platform) handleUpdateKey(c *fiber.Ctx) error {
	id := c.Params("id")
	key, err := p.store.GetKeyByID(id)
	if err != nil {
		return errInternal(c, err)
	}
	if key == nil {
		return errNotFound(c)
	}
	var req struct {
		Name       *string `json:"name"`
		Status     *string `json:"status"`
		DailyQuota *int64  `json:"daily_quota"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return errBadRequest(c, "INVALID_PARAM")
		}
		key.Name = name
	}
	if req.Status != nil {
		if *req.Status != KeyStatusActive && *req.Status != KeyStatusDisabled {
			return errBadRequest(c, "INVALID_PARAM")
		}
		key.Status = *req.Status
	}
	if req.DailyQuota != nil {
		if *req.DailyQuota < 0 {
			return errBadRequest(c, "INVALID_PARAM")
		}
		key.DailyQuota = *req.DailyQuota
	}
	if err := p.store.PutKey(key); err != nil {
		return errInternal(c, err)
	}
	return c.JSON(newKeyView(key, time.Now()))
}

// handleDeleteKey answers DELETE /api/keys/:id → {"ok":true}.
func (p *Platform) handleDeleteKey(c *fiber.Ctx) error {
	existed, err := p.store.DeleteKey(c.Params("id"))
	if err != nil {
		return errInternal(c, err)
	}
	if !existed {
		return errNotFound(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// handleStatsOverview answers GET /api/stats/overview. Aggregation is pushed
// down to the store so the response time does not degrade with journal size.
func (p *Platform) handleStatsOverview(c *fiber.Ctx) error {
	agg, err := p.store.UsageAggregate()
	if err != nil {
		return errInternal(c, err)
	}
	keys, err := p.store.ListKeys()
	if err != nil {
		return errInternal(c, err)
	}

	// 近 24h 无调用时返回 null(前端渲染为「—」),避免空态误显示 100%。
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
		byEngine[engine] = s
	}

	return c.JSON(fiber.Map{
		"today_calls":      agg.TodayCalls,
		"today_units":      agg.TodayUnits,
		"total_calls":      agg.TotalCalls,
		"total_units":      agg.TotalUnits,
		"active_keys":      activeKeys,
		"success_rate_24h": successRate,
		"by_engine":        byEngine,
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
}

// handleUsage answers GET /api/usage?limit=50&offset=0&key_id= — newest first,
// with the filtered total for pagination. Pagination/filtering is pushed down
// to the store so a large journal cannot slow the admin log down.
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

	records, total, err := p.store.ListUsagePage(UsagePageFilter{KeyID: keyID, Offset: offset, Limit: limit})
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
		})
	}

	return c.JSON(fiber.Map{
		"total": total,
		"items": items,
	})
}
