package platform

// Console users and role-based access control. The config admin is migrated
// into the users bucket on first start; after that the store is the source of
// truth. Roles are fixed in code (no custom-role editor) and checked on every
// request from the persisted user record, so a role/status change takes effect
// immediately even for an already-issued JWT.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

// User is one console account. Only the bcrypt hash is persisted — never the
// plaintext password. PasswordHash is []byte so bbolt's JSON encoding stores a
// compact base64 string.
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash []byte     `json:"password_hash"`
	Role         string     `json:"role"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// userView is the public (non-secret) projection returned by /api/users.
type userView struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

func newUserView(u *User) userView {
	return userView{
		ID: u.ID, Username: u.Username, Role: u.Role, Status: u.Status,
		CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
	}
}

func validRole(role string) bool {
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer:
		return true
	}
	return false
}

func validUserStatus(status string) bool {
	return status == UserStatusActive || status == UserStatusDisabled
}

// roleRank orders roles for the "minimum role" guard. A missing/unknown role
// ranks below viewer and is denied (fail-closed).
var roleRank = map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

func roleAtLeast(role, minimum string) bool {
	return roleRank[role] >= roleRank[minimum] && roleRank[minimum] > 0
}

func newUserID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf)
}

// normalizeUsername canonicalizes console account names to lowercase so
// duplicate detection and login lookup stay case-insensitive.
func normalizeUsername(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// bootstrapAdminUser migrates the configured admin into the users bucket on
// first start. Existing installations keep their accounts; if every account
// was somehow disabled/demoted we restore one active admin to avoid a locked
// console. The password hash comes from initAdminPassword (config or the
// settings-bucket override), so changing the password via the panel keeps
// working across restarts.
func (p *Platform) bootstrapAdminUser() error {
	users, err := p.store.ListUsers()
	if err != nil {
		return err
	}
	if len(users) == 0 {
		hash := p.adminHash()
		if len(hash) == 0 {
			return nil // storage without credentials cannot happen in practice
		}
		u := &User{
			ID:           newUserID(),
			Username:     normalizeUsername(p.cfg.AdminUsername),
			PasswordHash: append([]byte(nil), hash...),
			Role:         RoleAdmin,
			Status:       UserStatusActive,
			CreatedAt:    time.Now(),
		}
		return p.store.PutUser(u)
	}
	// The config admin credential may have been changed in config.yaml or via
	// the offline admin-reset CLI (both write the settings hash). Keep the
	// persisted user hash in sync so login actually accepts the new password.
	configName := normalizeUsername(p.cfg.AdminUsername)
	for i := range users {
		if users[i].Username != configName || users[i].Role != RoleAdmin {
			continue
		}
		if hash := p.adminHash(); len(hash) > 0 && !bytes.Equal(users[i].PasswordHash, hash) {
			users[i].PasswordHash = append([]byte(nil), hash...)
			if err := p.store.PutUser(&users[i]); err != nil {
				return err
			}
		}
		break
	}
	for _, u := range users {
		if u.Role == RoleAdmin && u.Status == UserStatusActive {
			return nil
		}
	}
	// Safety net: never leave the console without an administrator.
	for i := range users {
		if users[i].Status != UserStatusDisabled {
			users[i].Role = RoleAdmin
			users[i].Status = UserStatusActive
			return p.store.PutUser(&users[i])
		}
	}
	users[0].Role, users[0].Status = RoleAdmin, UserStatusActive
	return p.store.PutUser(&users[0])
}

// legacyAdminUser allows the configured admin to authenticate when the users
// bucket is completely empty (e.g. a store replaced at runtime or a legacy
// database opened before bootstrap). It never bypasses a populated user table.
func (p *Platform) legacyAdminUser(username string) *User {
	if p.store == nil {
		return nil
	}
	users, err := p.store.ListUsers()
	if err != nil || len(users) != 0 {
		return nil
	}
	if normalizeUsername(username) != normalizeUsername(p.cfg.AdminUsername) {
		return nil
	}
	return &User{
		ID: "", Username: p.cfg.AdminUsername,
		PasswordHash: append([]byte(nil), p.adminHash()...),
		Role:         RoleAdmin, Status: UserStatusActive, CreatedAt: time.Now(),
	}
}

// resolveRequestUser returns the persisted user for the JWT subject. Legacy
// tokens (no UID) map to the bootstrap admin for backward compatibility.
func (p *Platform) resolveRequestUser(username, uid string) (*User, bool) {
	if uid != "" {
		u, err := p.store.GetUserByID(uid)
		if err != nil || u == nil || u.Status != UserStatusActive {
			return nil, false
		}
		return u, true
	}
	u, err := p.store.GetUserByUsername(username)
	if err != nil {
		return nil, false
	}
	if u == nil {
		u = p.legacyAdminUser(username)
	}
	if u == nil {
		return nil, false
	}
	if u.Status != UserStatusActive {
		return nil, false
	}
	return u, true
}

// currentRequestUser resolves the persisted user from jwtGuard locals.
func (p *Platform) currentRequestUser(c *fiber.Ctx) (*User, bool) {
	uid, _ := c.Locals(localAdminUserID).(string)
	username, _ := c.Locals(localAdminUsername).(string)
	return p.resolveRequestUser(username, uid)
}

// requireRole is a route guard: the persisted role must rank at least minimum.
// 401 is handled by jwtGuard; here an authenticated but under-privileged caller
// receives 403 with an explicit code for the console to render a hint.
func (p *Platform) requireRole(minimum string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals(localAdminRole).(string)
		if !roleAtLeast(role, minimum) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "forbidden", "code": "insufficient_role",
			})
		}
		return c.Next()
	}
}

// verifyUserPassword applies bcrypt in a constant-shape path shared by login
// and password reset helpers (callers decide what to do on mismatch).
func verifyUserPassword(u *User, password string) bool {
	if u == nil || len(u.PasswordHash) == 0 {
		return false
	}
	return bcrypt.CompareHashAndPassword(u.PasswordHash, []byte(password)) == nil
}
