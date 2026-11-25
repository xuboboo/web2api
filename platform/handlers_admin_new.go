package platform

// Admin endpoints for console users/roles, the IP blacklist and server logs.
// All routes are registered behind jwtGuard + requireRole in RegisterAdmin.

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
	"golang.org/x/crypto/bcrypt"
)

// userMutationMu serializes the "last active admin" check with the write so
// two concurrent demotions cannot both pass the guard (bbolt is single-writer
// per transaction but the count check is a separate read).
var userMutationMu sync.Mutex

// blacklistMutationMu serializes duplicate checks and inserts.
var blacklistMutationMu sync.Mutex

// parsePageParams reads limit/offset with contract-safe bounds.
func parsePageParams(c *fiber.Ctx, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			offset = v
		}
	}
	return limit, offset
}

// ---------------------------------------------------------------------------
// Console users / roles (admin only)
// ---------------------------------------------------------------------------

func (p *Platform) handleListUsers(c *fiber.Ctx) error {
	users, err := p.store.ListUsers()
	if err != nil {
		return errInternal(c, err)
	}
	items := make([]userView, 0, len(users))
	for i := range users {
		items = append(items, newUserView(&users[i]))
	}
	return c.JSON(fiber.Map{"total": len(items), "items": items})
}

func (p *Platform) handleCreateUser(c *fiber.Ctx) error {
	userMutationMu.Lock()
	defer userMutationMu.Unlock()
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	username := normalizeUsername(req.Username)
	if len(username) < 2 || len(username) > 64 {
		return errBadRequestMsg(c, "INVALID_PARAM", "用户名长度应为 2-64 个字符")
	}
	if !validRole(req.Role) {
		return errBadRequestMsg(c, "INVALID_PARAM", "角色无效(admin/operator/viewer)")
	}
	req.Password = strings.TrimSpace(req.Password)
	if msg := p.validatePassword(req.Password); msg != "" {
		return errBadRequestMsg(c, "INVALID_PARAM", msg)
	}
	existing, err := p.store.GetUserByUsername(username)
	if err != nil {
		return errInternal(c, err)
	}
	if existing != nil {
		return errBadRequestMsg(c, "INVALID_PARAM", "用户名已存在")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return errInternal(c, err)
	}
	u := &User{
		ID: newUserID(), Username: username, PasswordHash: hash,
		Role: req.Role, Status: UserStatusActive, CreatedAt: time.Now(),
	}
	if err := p.store.PutUser(u); err != nil {
		return errInternal(c, err)
	}
	p.auditFrom(c, "", AuditActionUserCreate, u.Username, "role="+u.Role)
	return c.JSON(fiber.Map{"ok": true, "user": newUserView(u)})
}

func (p *Platform) handleUpdateUser(c *fiber.Ctx) error {
	userMutationMu.Lock()
	defer userMutationMu.Unlock()
	u, err := p.store.GetUserByID(c.Params("id"))
	if err != nil {
		return errInternal(c, err)
	}
	if u == nil {
		return errNotFound(c)
	}
	var req struct {
		Role   *string `json:"role"`
		Status *string `json:"status"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	current, _ := p.currentRequestUser(c)
	if current != nil && current.ID == u.ID {
		if (req.Role != nil && *req.Role != u.Role) || (req.Status != nil && *req.Status != UserStatusActive) {
			return errBadRequestMsg(c, "INVALID_PARAM", "不能修改当前登录用户的角色或状态")
		}
	}
	if req.Role != nil {
		if !validRole(*req.Role) {
			return errBadRequestMsg(c, "INVALID_PARAM", "角色无效(admin/operator/viewer)")
		}
		if u.Role == RoleAdmin && *req.Role != RoleAdmin && p.activeAdminCount() <= 1 {
			return errBadRequestMsg(c, "INVALID_PARAM", "至少需要保留一名管理员")
		}
		u.Role = *req.Role
	}
	if req.Status != nil {
		if !validUserStatus(*req.Status) {
			return errBadRequestMsg(c, "INVALID_PARAM", "状态无效(active/disabled)")
		}
		if u.Role == RoleAdmin && *req.Status != UserStatusActive && p.activeAdminCount() <= 1 {
			return errBadRequestMsg(c, "INVALID_PARAM", "至少需要保留一名管理员")
		}
		u.Status = *req.Status
	}
	if err := p.store.PutUser(u); err != nil {
		return errInternal(c, err)
	}
	revoked, _ := p.revokeUserSessions(u.ID)
	p.auditFrom(c, "", AuditActionUserUpdate, u.Username, "role="+u.Role+",status="+u.Status+",revoked="+itoa(revoked))
	return c.JSON(fiber.Map{"ok": true, "user": newUserView(u)})
}

func (p *Platform) handleDeleteUser(c *fiber.Ctx) error {
	userMutationMu.Lock()
	defer userMutationMu.Unlock()
	u, err := p.store.GetUserByID(c.Params("id"))
	if err != nil {
		return errInternal(c, err)
	}
	if u == nil {
		return errNotFound(c)
	}
	current, _ := p.currentRequestUser(c)
	if current != nil && current.ID == u.ID {
		return errBadRequestMsg(c, "INVALID_PARAM", "不能删除当前登录用户")
	}
	if u.Role == RoleAdmin && p.activeAdminCount() <= 1 {
		return errBadRequestMsg(c, "INVALID_PARAM", "至少需要保留一名管理员")
	}
	deleted, err := p.store.DeleteUser(u.ID)
	if err != nil {
		return errInternal(c, err)
	}
	if !deleted {
		return errNotFound(c)
	}
	revoked, _ := p.revokeUserSessions(u.ID)
	p.auditFrom(c, "", AuditActionUserDelete, u.Username, "ok,revoked="+itoa(revoked))
	return c.JSON(fiber.Map{"ok": true})
}

func (p *Platform) handleResetUserPassword(c *fiber.Ctx) error {
	userMutationMu.Lock()
	defer userMutationMu.Unlock()
	u, err := p.store.GetUserByID(c.Params("id"))
	if err != nil {
		return errInternal(c, err)
	}
	if u == nil {
		return errNotFound(c)
	}
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	req.NewPassword = strings.TrimSpace(req.NewPassword)
	if msg := p.validatePassword(req.NewPassword); msg != "" {
		return errBadRequestMsg(c, "INVALID_PARAM", msg)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return errInternal(c, err)
	}
	// Resetting the configured bootstrap admin through the user admin API must
	// follow the same global credential path as self-service password change;
	// otherwise startup re-syncs the old settings hash and silently restores it.
	revoked := 0
	if u.Username == normalizeUsername(p.cfg.AdminUsername) {
		version, err := p.tokenVersion()
		if err != nil {
			return errInternal(c, err)
		}
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
		revoked, _ = p.revokeAllSessions()
	} else {
		revoked, _ = p.revokeUserSessions(u.ID)
	}
	u.PasswordHash = hash
	if err := p.store.PutUser(u); err != nil {
		return errInternal(c, err)
	}
	p.auditFrom(c, "", AuditActionUserPassword, u.Username, "reset,revoked="+itoa(revoked))
	return c.JSON(fiber.Map{"ok": true})
}

func (p *Platform) activeAdminCount() int {
	users, err := p.store.ListUsers()
	if err != nil {
		return 1 // fail-closed: never remove the last admin on a store error
	}
	n := 0
	for _, u := range users {
		if u.Role == RoleAdmin && u.Status == UserStatusActive {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// IP / CIDR blacklist (admin + operator)
// ---------------------------------------------------------------------------

func (p *Platform) handleListBlacklist(c *fiber.Ctx) error {
	entries, err := p.store.ListBlacklist()
	if err != nil {
		return errInternal(c, err)
	}
	now := time.Now()
	items := make([]blacklistView, 0, len(entries))
	for _, e := range entries {
		items = append(items, newBlacklistView(e, now))
	}
	return c.JSON(fiber.Map{"total": len(items), "items": items})
}

func (p *Platform) handleCreateBlacklist(c *fiber.Ctx) error {
	blacklistMutationMu.Lock()
	defer blacklistMutationMu.Unlock()
	var req struct {
		Kind      string  `json:"kind"`
		Value     string  `json:"value"`
		Reason    string  `json:"reason"`
		ExpiresAt *string `json:"expires_at"`
		Force     bool    `json:"force"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	value, kind, reason := parseBlacklistValue(req.Kind, req.Value)
	if reason != "" {
		return errBadRequestMsg(c, "INVALID_PARAM", reason)
	}
	if !req.Force {
		own := net.ParseIP(c.IP())
		blocked := false
		if own != nil {
			if kind == BlacklistKindIP && own.Equal(net.ParseIP(value)) {
				blocked = true
			}
			if kind == BlacklistKindCIDR {
				if _, network, err := net.ParseCIDR(value); err == nil && network.Contains(own) {
					blocked = true
				}
			}
		}
		if blocked {
			return errBadRequestMsg(c, "INVALID_PARAM", "不能屏蔽当前请求来源 IP;如确需封禁自身请显式设置 force:true")
		}
	}
	existing, err := p.store.ListBlacklist()
	if err != nil {
		return errInternal(c, err)
	}
	for _, e := range existing {
		if e.Kind == kind && e.Value == value {
			return errBadRequestMsg(c, "INVALID_PARAM", "该 IP/CIDR 已在黑名单中")
		}
	}
	entry := &BlacklistEntry{
		ID: newKeyID(), Kind: kind, Value: value,
		Reason: strings.TrimSpace(req.Reason), CreatedAt: time.Now(),
	}
	if actor, ok := c.Locals(localAdminUsername).(string); ok {
		entry.CreatedBy = actor
	}
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		ts, err := parseTimeParam(*req.ExpiresAt)
		if err != nil || !ts.After(time.Now()) {
			return errBadRequestMsg(c, "INVALID_PARAM", "过期时间须为 RFC3339 且晚于当前时间")
		}
		entry.ExpiresAt = &ts
	}
	if err := p.store.PutBlacklist(entry); err != nil {
		return errInternal(c, err)
	}
	p.auditFrom(c, "", AuditActionBlacklistAdd, entry.Value, "kind="+kind)
	return c.JSON(fiber.Map{"ok": true, "entry": newBlacklistView(*entry, time.Now())})
}

func (p *Platform) handleUpdateBlacklist(c *fiber.Ctx) error {
	entry, err := p.store.GetBlacklist(c.Params("id"))
	if err != nil {
		return errInternal(c, err)
	}
	if entry == nil {
		return errNotFound(c)
	}
	var req struct {
		Reason    *string `json:"reason"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return errBadRequest(c, "INVALID_PARAM")
	}
	if req.Reason != nil {
		entry.Reason = strings.TrimSpace(*req.Reason)
	}
	if req.ExpiresAt != nil {
		if strings.TrimSpace(*req.ExpiresAt) == "" {
			entry.ExpiresAt = nil // permanent
		} else {
			ts, err := parseTimeParam(*req.ExpiresAt)
			if err != nil || !ts.After(time.Now()) {
				return errBadRequestMsg(c, "INVALID_PARAM", "过期时间须为 RFC3339 且晚于当前时间")
			}
			entry.ExpiresAt = &ts
		}
	}
	if err := p.store.PutBlacklist(entry); err != nil {
		return errInternal(c, err)
	}
	p.auditFrom(c, "", AuditActionBlacklistUpdate, entry.Value, "ok")
	return c.JSON(fiber.Map{"ok": true, "entry": newBlacklistView(*entry, time.Now())})
}

func (p *Platform) handleDeleteBlacklist(c *fiber.Ctx) error {
	entry, err := p.store.GetBlacklist(c.Params("id"))
	if err != nil {
		return errInternal(c, err)
	}
	if entry == nil {
		return errNotFound(c)
	}
	deleted, err := p.store.DeleteBlacklist(entry.ID)
	if err != nil {
		return errInternal(c, err)
	}
	if !deleted {
		return errNotFound(c)
	}
	p.auditFrom(c, "", AuditActionBlacklistDelete, entry.Value, "ok")
	return c.JSON(fiber.Map{"ok": true})
}

// ---------------------------------------------------------------------------
// Server logs (admin for cleanup, viewer for query)
// ---------------------------------------------------------------------------

func (p *Platform) handleLogs(c *fiber.Ctx) error {
	limit, offset := parsePageParams(c, 50, 200)
	q := core.LogQuery{
		Level:  strings.TrimSpace(c.Query("level")),
		Text:   strings.TrimSpace(c.Query("q")),
		Limit:  limit,
		Offset: offset,
	}
	if raw := strings.TrimSpace(c.Query("since")); raw != "" {
		ts, err := parseTimeParam(raw)
		if err != nil {
			return errBadRequestMsg(c, "INVALID_PARAM", "since 须为 RFC3339 时间")
		}
		q.Since = ts
	}
	cfg := core.CurrentLogConfig()
	items, total, err := core.QueryLogs(q, cfg.Format)
	if err != nil {
		return errInternal(c, err)
	}
	if items == nil {
		items = []core.LogEntry{}
	}
	return c.JSON(fiber.Map{"total": total, "items": items})
}

func (p *Platform) handleLogsConfig(c *fiber.Ctx) error {
	cfg := core.CurrentLogConfig()
	auditDays := 0
	if audit := p.auditLog(); audit != nil {
		auditDays = audit.RetentionDays()
	}
	return c.JSON(fiber.Map{
		"max_mb": cfg.MaxMB, "backups": cfg.Backups,
		"retention_days": cfg.RetentionDays, "format": cfg.Format,
		"file_logging_enabled": cfg.FileLoggingEnabled,
		"audit_retention_days": auditDays,
	})
}

func (p *Platform) handleLogsCleanup(c *fiber.Ctx) error {
	removedServe := core.PruneExpiredLogs()
	removedAudit := 0
	if audit := p.auditLog(); audit != nil {
		removedAudit = audit.PruneExpired()
	}
	logrus.WithFields(logrus.Fields{
		"serve": removedServe, "audit": removedAudit,
	}).Info("platform: 手动清理过期日志完成")
	p.auditFrom(c, "", AuditActionLogsCleanup, "logs", "serve="+itoa(removedServe)+",audit="+itoa(removedAudit))
	return c.JSON(fiber.Map{
		"ok": true, "removed_serve": removedServe, "removed_audit": removedAudit,
	})
}
