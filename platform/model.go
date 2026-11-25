package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	// KeyPrefix is the fixed public prefix of generated API keys: sk-w2a-<32 hex chars>.
	KeyPrefix = "sk-w2a-"
	// keyPrefixDisplayLen is how much of the full key is stored/exposed as "prefix".
	keyPrefixDisplayLen = len(KeyPrefix) + 6

	KeyStatusActive   = "active"
	KeyStatusDisabled = "disabled"
)

// DefaultEngines is the engine list used when the caller omits "engines".
var DefaultEngines = []string{"baidu", "sogou", "so360", "bing"}

// engineAliases maps accepted aliases (contract: "360") to canonical engine names.
var engineAliases = map[string]string{
	"360": "so360",
}

// APIKey is the persisted API key record. The full plaintext key is never
// stored — only a SHA-256 hash (fast hash on purpose: the key is 128 bits of
// CSPRNG output, so there is nothing for bcrypt to slow down; bcrypt is
// reserved for the low-entropy admin password).
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"key_hash"`
	Prefix     string     `json:"prefix"`
	Status     string     `json:"status"`
	DailyQuota int64      `json:"daily_quota"` // 0 = unlimited
	UsedToday  int64      `json:"used_today"`  // counter belonging to TodayDate
	UsedTotal  int64      `json:"used_total"`
	TodayDate  string     `json:"today_date"` // local YYYY-MM-DD UsedToday belongs to
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// ExpiresAt optionally caps the key's lifetime (S4-GAP 密钥增强): after
	// this instant authenticateAPIKey rejects with 401. nil = never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// AllowedIPs optionally restricts the key to a set of CIDR ranges
	// (S4-GAP 密钥增强): empty = no IP restriction; a non-empty list that
	// does not match the caller IP answers 403 forbidden. Bare IPs are
	// accepted and treated as /32 (IPv6: /128).
	AllowedIPs []string `json:"allowed_ips,omitempty"`
}

// EngineStat aggregates per-engine attempt/success counts derived from
// index/check response bodies (units with an "error" object count as failed).
type EngineStat struct {
	Calls int64 `json:"calls"`
	OK    int64 `json:"ok"`
}

// Usage outcomes (B-01 用量记账如实化). Recorded on new entries; historical
// records without an outcome ("") are treated as reached for compatibility.
const (
	OutcomeBilled       = "billed"        // reached the engine and completed with HTTP 200
	OutcomeEngineFailed = "engine_failed" // reached the engine layer but answered 5xx
	OutcomeRejected     = "rejected"      // never reached business logic (400/401/403/429)
)

// UsageRecord is one persisted entry per guarded /v1/index/check request.
type UsageRecord struct {
	Seq         int64                 `json:"seq"`
	Time        time.Time             `json:"time"`
	KeyID       string                `json:"key_id"`
	KeyName     string                `json:"key_name"`
	Endpoint    string                `json:"endpoint"`
	Engines     []string              `json:"engines,omitempty"`
	URLs        int                   `json:"urls"`
	Units       int                   `json:"units"`
	Status      int                   `json:"status"`
	TookMS      int64                 `json:"took_ms"`
	EngineStats map[string]EngineStat `json:"engine_stats,omitempty"`
	// Outcome is the billing verdict (B-01): billed / engine_failed /
	// rejected. Empty on pre-S4 records — those count as reached.
	Outcome string `json:"outcome,omitempty"`
}

// recordReached reports whether the record counted as "reached the engine /
// business logic" (P-01 有效调用口径). Rejected requests (invalid params,
// quota/rate limits, cooldown fast-fails) are excluded. Pre-S4 records carry
// no outcome and stay in the reached bucket (兼容既有数据).
func recordReached(r *UsageRecord) bool {
	return r.Outcome == "" || r.Outcome == OutcomeBilled || r.Outcome == OutcomeEngineFailed
}

// outcomeForStatus derives the billing verdict from the real HTTP status
// (B-01). 503 answers are cooldown/circuit fast-fails — they never reached
// the engine, so they count as rejected, not engine_failed.
func outcomeForStatus(status int) string {
	switch {
	case status == fiber.StatusOK:
		return OutcomeBilled
	case status >= 500 && status != fiber.StatusServiceUnavailable:
		return OutcomeEngineFailed
	default:
		return OutcomeRejected
	}
}

// ResolveEngines validates and normalizes an engine list: lower-cases, applies
// aliases ("360" → "so360"), dedupes preserving order, and falls back to all
// four engines on an empty list. ok=false means the list contained an unknown
// engine name (caller should answer 400 INVALID_PARAM).
func ResolveEngines(raw []string) ([]string, bool) {
	if len(raw) == 0 {
		return slices.Clone(DefaultEngines), true
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if mapped, ok := engineAliases[name]; ok {
			name = mapped
		}
		if !slices.Contains(DefaultEngines, name) {
			return nil, false
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// sha256Hex hashes an API key for lookup.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// generateAPIKeyMaterial returns a fresh full key (sk-w2a- + 32 hex chars) and
// its lookup hash.
func generateAPIKeyMaterial() (full string, hash string) {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	full = KeyPrefix + hex.EncodeToString(buf)
	return full, sha256Hex(full)
}

// newKeyID returns a random hex record id.
func newKeyID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// localDate formats a time as the local YYYY-MM-DD bucket used for daily
// counters and timeseries.
func localDate(t time.Time) string {
	return t.Format("2006-01-02")
}

// usedTodayUnits returns the key's units consumed today (0 after a date roll).
func usedTodayUnits(k *APIKey, now time.Time) int64 {
	if k.TodayDate == localDate(now) {
		return k.UsedToday
	}
	return 0
}

// Requests can finish in one order and acquire the storage writer in another.
// A late entry for the previous day updates the total without rolling back
// the current quota bucket or last-used timestamp.
func applyKeyUsage(key *APIKey, units int64, consume bool, at time.Time) {
	if key.LastUsedAt == nil || at.After(*key.LastUsedAt) {
		key.LastUsedAt = &at
	}
	if !consume || units <= 0 {
		return
	}
	key.UsedTotal += units
	day := localDate(at)
	switch {
	case day > key.TodayDate:
		key.TodayDate, key.UsedToday = day, units
	case day == key.TodayDate:
		key.UsedToday += units
	}
}
