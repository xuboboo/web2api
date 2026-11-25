package platform

// S4 admin-plane extensions (只增不改): session management, key rotation,
// audit trail query, usage export/reconcile, backup download, runtime info.
// Response shapes are additive to the pre-S4 contract.

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
)

// Locals keys written by jwtGuard.
const (
	localAdminSessionID    = "platform_session_id"
	localAdminUsername     = "platform_username"
	localAdminTokenVersion = "platform_token_version"
	localAdminUserID       = "platform_user_id"
	localAdminRole         = "platform_role"
)

// Audit action names (audit trail + GET /api/audit).
const (
	AuditActionKeyCreate       = "key.create"
	AuditActionKeyUpdate       = "key.update"
	AuditActionKeyDelete       = "key.delete"
	AuditActionKeyRotate       = "key.rotate"
	AuditActionPasswordChange  = "password.change"
	AuditActionLoginSuccess    = "login.success"
	AuditActionLoginFailed     = "login.failed"
	AuditActionLoginLockout    = "login.lockout"
	AuditActionCooldownReset   = "cooldown.reset"
	AuditActionCooldownForce   = "cooldown.force"
	AuditActionSessionRevoke   = "session.revoke"
	AuditActionSessionLogout   = "session.logout"
	AuditActionSessionRefresh  = "session.refresh"
	AuditActionUserCreate      = "user.create"
	AuditActionUserUpdate      = "user.update"
	AuditActionUserDelete      = "user.delete"
	AuditActionUserPassword    = "user.password"
	AuditActionBlacklistAdd    = "blacklist.add"
	AuditActionBlacklistUpdate = "blacklist.update"
	AuditActionBlacklistDelete = "blacklist.delete"
	AuditActionLogsCleanup     = "logs.cleanup"
	AuditActionBlacklistHit    = "blacklist.hit"
	AuditActionBackupExport    = "backup.export"
	AuditActionUsageExport     = "usage.export"
)

// shortSID renders a session id for audit targets (前 12 位,避免 64 位 hex
// 撑爆面板表格;完整 id 仍可在会话列表核对).
func shortSID(sid string) string {
	if len(sid) > 12 {
		return sid[:12] + "…"
	}
	return sid
}

// auditFrom appends one audit entry for the request context. actor falls back
// to the authenticated admin username, then to "system". Never fails the
// request that triggered it. Key material and passwords never enter the trail
// (脱敏红线): callers pass key NAME or record id as target only.
func (p *Platform) auditFrom(c *fiber.Ctx, actor, action, target, result string) {
	if actor == "" {
		if name, ok := c.Locals(localAdminUsername).(string); ok && name != "" {
			actor = name
		} else {
			actor = "system"
		}
	}
	audit := p.auditLog()
	if audit == nil {
		return
	}
	audit.Append(AuditEntry{
		TS:        time.Now(),
		Actor:     actor,
		IP:        c.IP(),
		Action:    action,
		Target:    target,
		Result:    result,
		RequestID: strings.TrimSpace(c.Get("X-Request-ID")),
	})
}

// ---------------------------------------------------------------------------
// Time / IP parameter helpers (S4-GAP 密钥增强)
// ---------------------------------------------------------------------------

// parseTimeParam accepts RFC3339 or a local YYYY-MM-DD date (midnight).
func parseTimeParam(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: expected RFC3339 or YYYY-MM-DD", raw)
}

// parseOptionalTimeQuery parses a from/to query value; asEndDateTo moves a
// YYYY-MM-DD to to the end of that local day so the range is inclusive.
func parseOptionalTimeQuery(raw string, asEndDateTo bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := parseTimeParam(raw)
	if err != nil {
		return time.Time{}, err
	}
	if asEndDateTo && !strings.Contains(raw, "T") {
		t = t.AddDate(0, 0, 1).Add(-time.Millisecond)
	}
	return t, nil
}

// normalizeAllowedIPs validates the caller-provided IP allowlist: bare IPs
// are stored verbatim (ipAllowedFor treats them as exact matches), CIDR
// ranges must parse.
func normalizeAllowedIPs(ips []string) ([]string, error) {
	out := make([]string, 0, len(ips))
	for _, raw := range ips {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			if net.ParseIP(entry) == nil {
				return nil, fmt.Errorf("%q is not a valid IP", entry)
			}
			out = append(out, entry)
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return nil, fmt.Errorf("%q is not a valid CIDR range", entry)
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Sessions (S4-GAP 会话系统)
// ---------------------------------------------------------------------------

// handleLogout answers POST /api/auth/logout: revoke the current session and
// expire the httpOnly cookie.
func (p *Platform) handleLogout(c *fiber.Ctx) error {
	sid, _ := c.Locals(localAdminSessionID).(string)
	if sid != "" {
		if _, err := p.revokeSession(sid); err != nil {
			return errInternal(c, err)
		}
		p.auditFrom(c, "", AuditActionSessionLogout, shortSID(sid), "ok")
	}
	clearSessionCookie(c)
	return c.JSON(fiber.Map{"ok": true})
}

// handleAuthRefresh answers POST /api/auth/refresh (滑动续期): the current
// session's expiry slides forward and a fresh token (same session, new jti)
// is returned in the body AND set as the cookie. Bearer-only clients keep
// working through the body token.
func (p *Platform) handleAuthRefresh(c *fiber.Ctx) error {
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
	sid, _ := c.Locals(localAdminSessionID).(string)
	if sid != "" {
		active, err := p.touchSession(sid, true)
		if err != nil {
			return errInternal(c, err)
		}
		if !active {
			return errUnauthorized(c)
		}
	}
	u, ok := p.currentRequestUser(c)
	if !ok {
		return errUnauthorized(c)
	}
	if sid == "" {
		// Legacy tokens had no session; refresh must bind a revocable one.
		session, err := p.createUserSession(u, c.IP(), c.Get(fiber.HeaderUserAgent), p.jwtTTL())
		if err != nil {
			return errInternal(c, err)
		}
		sid = session.ID
	}
	token, err := p.issueUserToken(u, sid, version)
	if err != nil {
		return errInternal(c, err)
	}
	if sid != "" {
		p.setSessionCookie(c, token, p.jwtTTL())
		p.auditFrom(c, "", AuditActionSessionRefresh, shortSID(sid), "ok")
	}
	return c.JSON(fiber.Map{"token": token, "expires_in": int64(p.jwtTTL() / time.Second)})
}

// handleListSessions answers GET /api/sessions: active sessions newest-active
// first with IP / last-active / current-session flag (面板会话管理卡数据源).
func (p *Platform) handleListSessions(c *fiber.Ctx) error {
	current, _ := c.Locals(localAdminSessionID).(string)
	items := p.listSessions(current)
	if items == nil {
		items = []AdminSession{}
	}
	return c.JSON(fiber.Map{"items": items})
}

// handleRevokeSession answers DELETE /api/sessions/:id → {"ok":true}; 404 for
// an unknown session. Revoking the current session acts as a hard logout (the
// cookie is cleared as well).
func (p *Platform) handleRevokeSession(c *fiber.Ctx) error {
	id := c.Params("id")
	existed, err := p.revokeSession(id)
	if err != nil {
		return errInternal(c, err)
	}
	if !existed {
		return errNotFound(c)
	}
	p.auditFrom(c, "", AuditActionSessionRevoke, shortSID(id), "ok")
	if current, _ := c.Locals(localAdminSessionID).(string); current == id {
		clearSessionCookie(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// ---------------------------------------------------------------------------
// Runtime (S4-B-07 版本指纹收敛: the /health system block moved here, JWT)
// ---------------------------------------------------------------------------

// handleRuntime answers GET /api/runtime with the runtime counters that used
// to be public on /health (goroutines / memory / go version) plus uptime and
// the app version.
func (p *Platform) handleRuntime(c *fiber.Ctx) error {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	resp := fiber.Map{
		"go_version":     runtime.Version(),
		"goroutines":     runtime.NumGoroutine(),
		"memory_mb":      mem.Alloc / 1024 / 1024,
		"uptime_seconds": int64(time.Since(p.started).Seconds()),
	}
	if v := p.Version(); v != "" {
		resp["version"] = v
	}
	return c.JSON(resp)
}

// ---------------------------------------------------------------------------
// Key rotation (S4-GAP 密钥增强)
// ---------------------------------------------------------------------------

// handleRotateKey answers POST /api/keys/:id/rotate: the key material is
// replaced (new one-time plaintext in the response) while id, name, status,
// quota, usage counters, expires_at and allowed_ips are preserved — callers
// update their stored key without losing history. The old key answers 401
// from the next request on.
func (p *Platform) handleRotateKey(c *fiber.Ctx) error {
	id := c.Params("id")
	full, hash := generateAPIKeyMaterial()
	key, err := p.store.UpdateKey(id, func(current *APIKey) {
		current.KeyHash = hash
		current.Prefix = full[:keyPrefixDisplayLen]
	})
	if err != nil {
		return errInternal(c, err)
	}
	if key == nil {
		return errNotFound(c)
	}
	p.auditFrom(c, "", AuditActionKeyRotate, key.Name, "ok")
	view := newKeyView(key, time.Now())
	return c.JSON(keyCreatedView{apiKeyView: view, Key: full})
}

// ---------------------------------------------------------------------------
// Audit trail query (S4-GAP 审计日志)
// ---------------------------------------------------------------------------

// handleAudit answers GET /api/audit?limit=20&offset=0 — newest first with the
// unfiltered total.
func (p *Platform) handleAudit(c *fiber.Ctx) error {
	limit := 20
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
	audit := p.auditLog()
	if audit == nil {
		return c.JSON(fiber.Map{"total": 0, "items": []AuditEntry{}})
	}
	items, total, err := audit.Query(limit, offset)
	if err != nil {
		return errInternal(c, err)
	}
	if items == nil {
		items = []AuditEntry{}
	}
	return c.JSON(fiber.Map{"total": total, "items": items})
}

// ---------------------------------------------------------------------------
// Usage export / reconcile (S4-GAP 密钥增强)
// ---------------------------------------------------------------------------

// handleUsageExport answers GET /api/usage/export?format=csv|json&key_id=&from=&to=
// as a download attachment (JWT). Records are bounded by the retention window;
// the walk is the store's full-journal reader (export is an explicit,
// administrator-triggered operation).
func (p *Platform) handleUsageExport(c *fiber.Ctx) error {
	format := strings.ToLower(strings.TrimSpace(c.Query("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		return errBadRequestMsg(c, "INVALID_PARAM", "format 仅支持 csv 或 json")
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

	all, aerr := p.store.AllUsage()
	if aerr != nil {
		return errInternal(c, aerr)
	}
	records := make([]UsageRecord, 0, len(all))
	for _, r := range all {
		if keyID != "" && r.KeyID != keyID {
			continue
		}
		if !from.IsZero() && r.Time.Before(from) {
			continue
		}
		if !to.IsZero() && r.Time.After(to) {
			continue
		}
		records = append(records, r)
	}

	stamp := time.Now().Format("20060102-150405")
	p.auditFrom(c, "", AuditActionUsageExport, keyID, fmt.Sprintf("format=%s,count=%d", format, len(records)))

	switch format {
	case "json":
		c.Set(fiber.HeaderContentType, "application/json; charset=utf-8")
		c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=\"usage-export-%s.json\"", stamp))
		return c.JSON(records)
	default:
		c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
		c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=\"usage-export-%s.csv\"", stamp))
		var b strings.Builder
		b.WriteString("seq,time,key_id,key_name,endpoint,engines,urls,units,status,took_ms\n")
		for _, r := range records {
			engines := strings.Join(r.Engines, "|")
			fmt.Fprintf(&b, "%d,%s,%s,%s,%s,%s,%d,%d,%d,%d\n",
				r.Seq,
				r.Time.Format(time.RFC3339),
				core.SpreadsheetCSVCell(r.KeyID), core.SpreadsheetCSVCell(r.KeyName), core.SpreadsheetCSVCell(r.Endpoint), core.SpreadsheetCSVCell(engines),
				r.URLs, r.Units, r.Status, r.TookMS)
		}
		return c.SendString(b.String())
	}
}

// reconcileSample is one mismatching record in GET /api/reconcile samples.
type reconcileSample struct {
	Seq          int64     `json:"seq"`
	Time         time.Time `json:"time"`
	KeyID        string    `json:"key_id"`
	KeyName      string    `json:"key_name"`
	Endpoint     string    `json:"endpoint"`
	Status       int       `json:"status"`
	Outcome      string    `json:"outcome"`
	ChargedUnits int       `json:"charged_units"`
	EngineOKUnit int       `json:"engine_ok_units"`
}

// handleReconcile answers GET /api/reconcile?date=YYYY-MM-DD: compares the
// charged units (record.Units) against the per-envelope engine OK sum for the
// local day. B-01 修复后两者应一致;历史记录(修复前)允许出现 mismatch —
// samples 列出前 10 条供人工核对。T-02: 无引擎明细的记录(MCP web_search 等
// 非逐引擎计费调用、或响应信封解析失败的记录)不参与逐单元对账——按落账
// 口径计入 journaled,不判 mismatch;有引擎明细的记录仍按 charged vs engineOK
// 核对。
func (p *Platform) handleReconcile(c *fiber.Ctx) error {
	dateRaw := strings.TrimSpace(c.Query("date"))
	day := time.Now()
	if dateRaw != "" {
		var err error
		day, err = time.ParseInLocation("2006-01-02", dateRaw, time.Local)
		if err != nil {
			return errBadRequestMsg(c, "INVALID_PARAM", "date 格式无效,应为 YYYY-MM-DD")
		}
	}
	dateStr := localDate(day)

	all, err := p.store.AllUsage()
	if err != nil {
		return errInternal(c, err)
	}
	var chargedUnits, journaledUnits, mismatch, unverified int64
	var samples []reconcileSample
	for _, r := range all {
		if localDate(r.Time) != dateStr {
			continue
		}
		chargedUnits += int64(r.Units)
		if len(r.EngineStats) == 0 {
			// T-02: 无引擎明细 → 无法逐单元核对,按落账即口径计入 journaled。
			journaledUnits += int64(r.Units)
			unverified++
			continue
		}
		engineOK := int64(0)
		for _, s := range r.EngineStats {
			engineOK += s.OK
		}
		journaledUnits += engineOK
		if int64(r.Units) != engineOK {
			mismatch++
			if len(samples) < 10 {
				samples = append(samples, reconcileSample{
					Seq: r.Seq, Time: r.Time, KeyID: r.KeyID, KeyName: r.KeyName,
					Endpoint: r.Endpoint, Status: r.Status, Outcome: r.Outcome,
					ChargedUnits: r.Units, EngineOKUnit: int(engineOK),
				})
			}
		}
	}
	if samples == nil {
		samples = []reconcileSample{}
	}
	return c.JSON(fiber.Map{
		"date":             dateStr,
		"charged_units":    chargedUnits,
		"journaled_units":  journaledUnits,
		"mismatch_count":   mismatch,
		"unverified_count": unverified,
		"samples":          samples,
	})
}

// ---------------------------------------------------------------------------
// Backup download (S4-GAP 运维)
// ---------------------------------------------------------------------------

// handleBackupDownload answers GET /api/admin/backup with a consistent store
// snapshot as an attachment (bbolt: bolt View+WriteTo 热备; JSON fallback: the
// serialized document). The export lands in the audit trail.
func (p *Platform) handleBackupDownload(c *fiber.Ctx) error {
	stamp := time.Now().Format("20060102-150405")
	ext := "db"
	if p.storeKind == "json" {
		ext = "json"
	}
	c.Set(fiber.HeaderContentType, "application/octet-stream")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=\"web2api-backup-%s.%s\"", stamp, ext))
	p.auditFrom(c, "", AuditActionBackupExport, "web2api."+ext, "ok")
	if err := p.store.Backup(c.Response().BodyWriter()); err != nil {
		logrus.WithError(err).Error("platform: 生成备份快照失败")
		return errInternal(c, err)
	}
	return nil
}
