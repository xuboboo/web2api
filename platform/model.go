package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
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
}

// EngineStat aggregates per-engine attempt/success counts derived from
// index/check response bodies (units with an "error" object count as failed).
type EngineStat struct {
	Calls int64 `json:"calls"`
	OK    int64 `json:"ok"`
}

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
