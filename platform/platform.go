package platform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// adminHash is the bcrypt hash of the admin password (kept in the settings
	// bucket; an API-changed password overrides the config default).
	adminHash []byte

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

	// login brute-force lockout state (契约 B), keyed by username+IP.
	loginMu    sync.Mutex
	loginFails map[loginAttemptKey]*loginFailState
}

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

	// Primary storage: bbolt (pure Go, no CGO). Fallback per contract: the
	// equivalent JSON file store with the same interface (see store_json.go).
	boltPath := filepath.Join(cfg.DataDir, "web2api.db")
	boltStore, err := newBoltStore(boltPath)
	if err == nil {
		p.store = boltStore
		p.storeKind = "bbolt"
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
	// Startup usage-journal pruning (滚动清理上限,契约: 防止用量日志无上限增长).
	p.pruneUsage()
	return p, nil
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
		logrus.Warn("platform: secret.key 内容无效,重新生成 JWT 签名密钥")
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("platform: generate jwt secret: %w", err)
	}
	if err := os.WriteFile(secretPath, []byte(hex.EncodeToString(buf)), 0o600); err != nil {
		return fmt.Errorf("platform: write secret.key: %w", err)
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
	if err == nil && len(stored) > 0 {
		if custom, cerr := p.store.GetSetting(settingAdminPasswordCustom); cerr == nil && len(custom) > 0 {
			p.adminHash = stored
			logrus.Info("platform: 使用面板修改后的管理员密码(settings 桶),config 中的默认密码已覆盖")
			return nil
		}
		if bcrypt.CompareHashAndPassword(stored, []byte(p.cfg.AdminPassword)) == nil {
			p.adminHash = stored
			return nil
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("platform: hash admin password: %w", err)
	}
	if err := p.store.PutSetting(settingAdminHash, hash); err != nil {
		return fmt.Errorf("platform: persist admin password hash: %w", err)
	}
	p.adminHash = hash
	return nil
}

// tokenVersion reads the admin JWT token_version (契约 B). Password changes
// bump it, invalidating every previously issued token. Tokens issued before
// the mechanism existed carry tv=0, matching the initial stored version.
func (p *Platform) tokenVersion() int {
	raw, err := p.store.GetSetting(settingTokenVersion)
	if err != nil || len(raw) == 0 {
		return 0
	}
	version := 0
	for _, ch := range string(raw) {
		if ch < '0' || ch > '9' {
			return 0
		}
		version = version*10 + int(ch-'0')
		if version > 1<<30 {
			return 0
		}
	}
	return version
}

// bumpTokenVersion increments and persists the token version, returning it.
func (p *Platform) bumpTokenVersion() int {
	version := p.tokenVersion() + 1
	_ = p.store.PutSetting(settingTokenVersion, []byte(fmt.Sprintf("%d", version)))
	return version
}

// issueToken signs an admin JWT bound to the current token version.
func (p *Platform) issueToken(username string) (string, error) {
	return signJWT(p.secret, username, time.Now().Add(p.jwtTTL()), p.tokenVersion())
}

// Close releases the storage handle.
func (p *Platform) Close() error {
	if p.store == nil {
		return nil
	}
	return p.store.Close()
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
