package platform

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	berrors "go.etcd.io/bbolt/errors"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

// Platform owns the storage, the JWT signing secret and the admin credentials
// for the Web2API 收录查询平台.
type Platform struct {
	cfg PlatformConfig

	// store is the bbolt store, or the JSON fallback when bbolt cannot open.
	store     Store
	storeKind string

	// secret is the 32-byte JWT signing key persisted at <data_dir>/secret.key.
	secret []byte
	// adminHashP is the bcrypt hash of the admin password (kept in the settings
	// bucket; an API-changed password overrides the config default). S4-B-11:
	// accessed atomically so concurrent login/password-change cannot race.
	adminHashP  atomic.Pointer[[]byte]
	adminAuthMu sync.Mutex // serializes login, refresh and password changes

	// appVersion is injected by the serve command for GET /api/me|/api/runtime
	// (S4-B-07: 真实版本只在鉴权后的端点暴露).
	appVersion atomic.Pointer[string]

	// engineStatus supplies the engine list + circuit-breaker state snapshot
	// rendered by /v1/engines and /api/engines ({"engines": <value>}).
	engineStatus func() any

	// engineControl supplies the cooldown management surface (契约 A). It is
	// satisfied by *core.Server and injected through RegisterAdmin; nil means
	// the cooldown endpoints degrade to 503 engine_control_unavailable.
	engineControl EngineControl

	// security knobs (契约 B) + the global /api/* rate limiter.
	secMu        sync.Mutex
	sec          SecurityConfig
	adminLimiter *rate.Limiter

	// S4 rate-limit tables (B-02 per-key/unauth, B-05 per-IP admin).
	ratelimits platformRateLimiters

	quotaMu         sync.Mutex
	pendingQuota    map[string]int64
	jobSubmissionMu sync.Mutex
	scheduleMu      sync.Mutex

	// Serializes session updates and revocations across store operations.
	sessionMu sync.Mutex

	// S4-GAP 审计日志: JSON Lines writer at <data_dir>/audit.log.
	auditMu sync.Mutex
	audit   *AuditLog

	// login brute-force lockout state (契约 B), keyed by username across
	// source IPs so rotating addresses cannot reset the failure budget.
	loginMu    sync.Mutex
	loginFails map[loginAttemptKey]*loginFailState

	// started is the process-local platform start time (GET /api/runtime).
	started time.Time
}

// dummyAdminHash is the bcrypt hash every unknown-username login attempt is
// also compared against (S4-B-04 登录时延一致): both paths run one bcrypt
// comparison, so response timing no longer enumerates the admin username. The
// backing password is random per process — nothing can match it.
var dummyAdminHash = func() []byte {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	hash, err := bcrypt.GenerateFromPassword(buf, bcrypt.DefaultCost)
	if err != nil {
		// bcrypt never fails for inputs < 72 bytes; keep a static fallback.
		hash, _ = bcrypt.GenerateFromPassword([]byte("web2api-dummy-password-fallback"), bcrypt.DefaultCost)
	}
	return hash
}()

// New opens storage under cfg.DataDir, loads or creates the JWT signing secret
// (<data_dir>/secret.key, random 32 bytes, hex-encoded), prepares the bcrypt
// hash of the admin password in the settings bucket and stores the engineStatus
// provider used by the engines endpoints.
//
// engineStatus may be nil (the endpoints then answer {"engines": {}}).
func New(cfg PlatformConfig, engineStatus func() any) (*Platform, error) {
	cfg = cfg.normalized()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("platform: create data_dir %s: %w", cfg.DataDir, err)
	}

	p := &Platform{cfg: cfg, engineStatus: engineStatus}
	p.sec = DefaultSecurityConfig()
	p.loginFails = map[loginAttemptKey]*loginFailState{}
	p.adminLimiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(p.sec.AdminRatePerMinute)), p.sec.AdminRatePerMinute)
	p.ratelimits = newPlatformRateLimiters()
	p.pendingQuota = make(map[string]int64)

	// Primary storage: bbolt (pure Go, no CGO). S4-B-06 fail-closed: a locked
	// database (second process on the same data_dir) is a startup FAILURE, not
	// a silent JSON fallback — a split-brain instance would double-book quota
	// and usage. The JSON fallback only covers non-lock open failures.
	boltPath := filepath.Join(cfg.DataDir, "web2api.db")
	boltStore, err := newBoltStore(boltPath)
	if err == nil {
		p.store = boltStore
		p.storeKind = "bbolt"
	} else if isBoltLockError(err) {
		_ = p.closeAudit()
		return nil, fmt.Errorf("platform: bbolt 数据目录被其他进程占用(%s),拒绝启动以避免双实例分裂记账: %w", boltPath, err)
	} else if errors.Is(err, ErrBoltContentCorrupt) || isBoltCorruptOpenError(err) {
		_ = p.closeAudit()
		return nil, fmt.Errorf("platform: 数据库内容损坏(%s),拒绝静默回退到空账本;请先从备份恢复: %w", boltPath, err)
	} else {
		logrus.WithError(err).Warn("platform: bbolt 打开失败,回退到 JSON 文件存储(fallback,接口不变)")
		jsonStore, jsonErr := newJSONStore(filepath.Join(cfg.DataDir, "web2api.json"))
		if jsonErr != nil {
			return nil, fmt.Errorf("platform: open storage: bbolt=%v fallback_json=%w", err, jsonErr)
		}
		p.store = jsonStore
		p.storeKind = "json"
		_ = p.store.PutSetting(settingFallbackNote, []byte("bbolt unavailable; using JSON file fallback per contract"))
	}

	if err := p.initSecret(); err != nil {
		_ = p.store.Close()
		return nil, err
	}
	if err := p.initAdminPassword(); err != nil {
		_ = p.store.Close()
		return nil, err
	}
	// 控制台用户与角色: 首次启动把 config 管理员迁入 users 桶, 既有无损升级。
	if err := p.bootstrapAdminUser(); err != nil {
		_ = p.store.Close()
		return nil, fmt.Errorf("bootstrap admin user: %w", err)
	}
	// S4-GAP 审计日志: JSON Lines writer (rotation bounds configurable via
	// SetAuditRotation; defaults 10MB × 5).
	p.auditMu.Lock()
	p.audit = newAuditLog(cfg.DataDir, defaultAuditMaxBytes, defaultAuditBackups)
	p.auditMu.Unlock()
	// Startup usage-journal pruning (滚动清理上限,契约: 防止用量日志无上限增长).
	p.pruneUsage()
	p.started = time.Now()
	return p, nil
}

// isBoltCorruptOpenError distinguishes a damaged on-disk database from an
// environment where bbolt cannot be used at all. Only the latter may fall back
// to the JSON store.
func isBoltCorruptOpenError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"invalid database", "checksum", "file too small", "unexpected eof"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// isBoltLockError classifies bbolt open failures caused by a held file lock
// (bbolt uses a flock-style lock; the Timeout option yields ErrTimeout, and
// some platforms surface "flock"/"lock" wording for the same condition).
func isBoltLockError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, berrors.ErrTimeout) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "flock") ||
		strings.Contains(msg, "file lock") ||
		strings.Contains(msg, "could not lock")
}

// initSecret loads <data_dir>/secret.key (hex of the random 32-byte JWT
// signing key) or generates and persists a new one, mode 0600.
func (p *Platform) initSecret() error {
	secretPath := filepath.Join(p.cfg.DataDir, "secret.key")
	if raw, err := os.ReadFile(secretPath); err == nil {
		text := strings.TrimSpace(string(raw))
		if decoded, err := hex.DecodeString(text); err == nil && len(decoded) >= 16 {
			p.secret = decoded
			return nil
		}
		// Accept raw binary secrets written outside this package.
		if len(raw) >= 16 {
			p.secret = raw
			return nil
		}
		return fmt.Errorf("platform: invalid JWT signing secret at %s", secretPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("platform: read secret.key: %w", err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("platform: generate jwt secret: %w", err)
	}
	f, err := os.OpenFile(secretPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("platform: create secret.key: %w", err)
	}
	_, writeErr := f.WriteString(hex.EncodeToString(buf))
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return fmt.Errorf("platform: persist secret.key: %w", err)
	}
	p.secret = buf
	return nil
}

// initAdminPassword verifies the configured admin password against the stored
// bcrypt hash; on first start (or after a config password change) it re-hashes
// the configured password with bcrypt and persists the new hash.
//
// 契约 B: a password changed through POST /api/auth/password is stored in the
// settings bucket with the admin_password_custom marker and OVERRIDES the
// config default from then on (until the marker is cleared by deleting the
// settings record).
func (p *Platform) initAdminPassword() error {
	stored, err := p.store.GetSetting(settingAdminHash)
	if err != nil {
		return fmt.Errorf("platform: read admin password hash: %w", err)
	}
	custom, err := p.store.GetSetting(settingAdminPasswordCustom)
	if err != nil {
		return fmt.Errorf("platform: read admin password source: %w", err)
	}
	if len(custom) > 0 {
		if _, err := bcrypt.Cost(stored); err != nil {
			return fmt.Errorf("platform: invalid custom admin password hash: %w", err)
		}
		p.setAdminHash(stored)
		logrus.Info("platform: 使用面板修改后的管理员密码(settings 桶),config 中的默认密码已覆盖")
		return nil
	}
	if bcrypt.CompareHashAndPassword(stored, []byte(p.cfg.AdminPassword)) == nil {
		p.setAdminHash(stored)
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("platform: hash admin password: %w", err)
	}
	values := map[string][]byte{settingAdminHash: hash}
	if len(stored) > 0 {
		version, err := p.tokenVersion()
		if err != nil {
			return fmt.Errorf("platform: read admin token version: %w", err)
		}
		if version >= 1<<30 {
			return errors.New("platform: admin token version limit reached")
		}
		values[settingTokenVersion] = []byte(strconv.Itoa(version + 1))
	}
	if err := p.store.PutSettings(values); err != nil {
		return fmt.Errorf("platform: persist admin password hash: %w", err)
	}
	p.setAdminHash(hash)
	return nil
}

// adminHash returns the current bcrypt hash atomically (S4-B-11: login and
// password-change run concurrently; no data race).
func (p *Platform) adminHash() []byte {
	if h := p.adminHashP.Load(); h != nil {
		return *h
	}
	return nil
}

// setAdminHash stores the bcrypt hash atomically (S4-B-11).
func (p *Platform) setAdminHash(hash []byte) {
	cp := append([]byte(nil), hash...)
	p.adminHashP.Store(&cp)
}

// SetVersion injects the CLI version for the authenticated runtime endpoints
// (S4-B-07: 真实版本只出现在鉴权之后; empty hides it).
func (p *Platform) SetVersion(v string) {
	p.appVersion.Store(&v)
}

// Version returns the injected app version ("" when unset).
func (p *Platform) Version() string {
	if v := p.appVersion.Load(); v != nil {
		return *v
	}
	return ""
}

// auditLog returns the audit writer (nil-safe).
func (p *Platform) auditLog() *AuditLog {
	p.auditMu.Lock()
	defer p.auditMu.Unlock()
	return p.audit
}

// closeAudit releases the audit file handle.
func (p *Platform) closeAudit() error {
	p.auditMu.Lock()
	defer p.auditMu.Unlock()
	if p.audit == nil {
		return nil
	}
	return p.audit.Close()
}

// tokenVersion reads the persisted JWT version. Version 0 is reserved for
// stores created before the setting existed.
func (p *Platform) tokenVersion() (int, error) {
	raw, err := p.store.GetSetting(settingTokenVersion)
	if err != nil || len(raw) == 0 {
		return 0, err
	}
	version, err := strconv.Atoi(string(raw))
	if err != nil || version < 0 || version > 1<<30 {
		return 0, errors.New("invalid admin token version")
	}
	return version, nil
}

// issueSessionToken signs a token with the credential version authorized by
// the caller. An empty session ID preserves support for legacy JWTs.
func (p *Platform) issueSessionToken(username, sessionID string, version int) (string, error) {
	if sessionID == "" {
		return signJWT(p.secret, username, time.Now().Add(p.jwtTTL()), version)
	}
	return signJWTSession(p.secret, username, time.Now().Add(p.jwtTTL()), version, sessionID, newSessionID())
}

// issueUserToken signs a token carrying the console user id and role.
func (p *Platform) issueUserToken(u *User, sessionID string, version int) (string, error) {
	exp := time.Now().Add(p.jwtTTL())
	if sessionID == "" {
		return signJWTSessionWithUser(p.secret, u.Username, u.ID, u.Role, exp, version, "", "")
	}
	return signJWTSessionWithUser(p.secret, u.Username, u.ID, u.Role, exp, version, sessionID, newSessionID())
}

// Close releases the storage handle and the audit writer.
func (p *Platform) Close() error {
	var errs []error
	if p.store != nil {
		if err := p.store.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := p.closeAudit(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// UsesDefaultAdminPassword reports whether the effective credentials are still
// the shipped public default. A password changed through the console sets the
// custom marker and stays custom even if the default string is later restored
// in config.
func (p *Platform) UsesDefaultAdminPassword() bool {
	if p == nil || p.store == nil {
		return false
	}
	custom, err := p.store.GetSetting(settingAdminPasswordCustom)
	if err == nil && len(custom) > 0 {
		return false
	}
	return p.cfg.AdminPassword == DefaultPlatformConfig().AdminPassword
}

// StoreKind reports the active storage backend: "bbolt" or "json" (fallback).
func (p *Platform) StoreKind() string {
	return p.storeKind
}

// jwtTTL is the admin token lifetime derived from jwt_expire_hours.
func (p *Platform) jwtTTL() time.Duration {
	return time.Duration(p.cfg.JWTExpireHours) * time.Hour
}

// engineStatusValue evaluates the engine status provider (nil-safe).
func (p *Platform) engineStatusValue() any {
	if p.engineStatus == nil {
		return map[string]any{}
	}
	return p.engineStatus()
}
