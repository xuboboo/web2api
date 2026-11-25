package platform

import (
	"io"
	"sort"
	"time"
)

// Bucket names mandated by the product contract (bbolt buckets; the JSON
// fallback mirrors them as fields of one document). usage_daily is the S4
// 计数索引 bucket (GAP-性能 b) and sessions backs the admin session store.
const (
	bucketKeys       = "keys"
	bucketUsage      = "usage"
	bucketSettings   = "settings"
	bucketSessions   = "sessions"
	bucketUsageDaily = "usage_daily"

	settingUsageSeq            = "usage_seq"
	settingAdminHash           = "admin_password_hash"
	settingAdminPasswordCustom = "admin_password_custom"
	settingTokenVersion        = "admin_token_version"
	settingFallbackNote        = "storage_fallback_note"
)

// UsagePageFilter selects one usage-journal page (newest first).
type UsagePageFilter struct {
	// KeyID optionally restricts the page to one key ("" = all keys).
	KeyID string
	// From/To optionally bound the page by record time (S4-GAP: GET /api/usage
	// from/to). Zero values = unbounded; To is inclusive.
	From time.Time
	To   time.Time
	// Offset/Limit page the filtered set, newest first.
	Offset int
	Limit  int
}

// UsageAggregate is the aggregate behind /api/stats/overview and /api/me —
// computed inside the store so handlers never materialize the whole usage
// journal. S4 口径 (B-01/P-01): TotalCalls/TodayCalls/Calls24h count REACHED
// calls only (billed + engine_failed); TodayCallsAll counts every journaled
// request and Rejected24h the 4xx/503 fast-fails (additive fields).
type UsageAggregate struct {
	TotalCalls int64
	TotalUnits int64
	TodayCalls int64
	TodayUnits int64
	Calls24h   int64
	OK24h      int64
	// TodayCallsAll counts every usage record of the local day regardless of
	// outcome (additive, P-01 有效调用口径的分母信息).
	TodayCallsAll int64
	// Rejected24h counts requests rejected before business logic (4xx/503
	// fast-fail) in the last 24h window (additive).
	Rejected24h int64
	ByEngine    map[string]EngineStat
}

// UsageDayStat is one zero-filled timeseries bucket (local date).
type UsageDayStat struct {
	Date  string `json:"date"`
	Calls int    `json:"calls"`
	Units int    `json:"units"`
	OK    int    `json:"ok"`
}

// Store is the persistence contract for the platform. The primary
// implementation is bbolt (pure Go, no CGO) at <data_dir>/web2api.db with the
// contract buckets keys/usage/settings. If bbolt cannot be opened (module
// unavailable or file lock problems), an equivalent JSON file store with the
// identical interface is used as fallback (contract-sanctioned).
type Store interface {
	ListKeys() ([]APIKey, error)
	GetKeyByID(id string) (*APIKey, error)
	GetKeyByHash(keyHash string) (*APIKey, error)
	PutKey(key *APIKey) error
	// UpdateKey edits the latest record atomically and returns nil if absent.
	UpdateKey(id string, edit func(*APIKey)) (*APIKey, error)
	DeleteKey(id string) (bool, error)

	// RecordUsage atomically appends one usage record and, when the key still
	// exists, updates its counters (LastUsedAt; UsedToday/UsedTotal rolled by
	// local date when consume && units > 0). One transaction so concurrent
	// requests cannot lose quota increments.
	RecordUsage(rec *UsageRecord, keyID string, units int64, consume bool, at time.Time) error
	AllUsage() ([]UsageRecord, error)

	// Aggregate/page push-down: admin surfaces must not load the whole usage
	// journal per request (the journal is unbounded over time).
	UsageAggregate() (*UsageAggregate, error)
	UsageTimeseries(days int) ([]UsageDayStat, error)
	ListUsagePage(filter UsagePageFilter) ([]UsageRecord, int, error)
	// PruneUsageBefore deletes usage records older than cutoff and returns
	// how many were removed (用量日志滚动清理上限).
	PruneUsageBefore(cutoff time.Time) (int64, error)

	// Backup writes a consistent snapshot of the whole store to w (S4-GAP
	// 运维: bbolt 热备 = bolt View + Tx.WriteTo; JSON store serializes its
	// document). The output of the bbolt store is a valid web2api.db file.
	Backup(w io.Writer) error

	// Admin sessions (S4-GAP 会话系统). The JSON fallback mirrors the bucket.
	ListSessions() ([]AdminSession, error)
	GetSession(id string) (*AdminSession, error)
	PutSession(s *AdminSession) error
	DeleteSession(id string) (bool, error)
	// DeleteAllSessions revokes every stored session except keepID ("" = all).
	DeleteAllSessions(keepID string) (int, error)

	GetSetting(name string) ([]byte, error)
	PutSetting(name string, value []byte) error
	PutSettings(values map[string][]byte) error

	Close() error
}

// sortKeysNewestFirst orders key listings by creation time, newest first.
func sortKeysNewestFirst(keys []APIKey) {
	sort.Slice(keys, func(i, j int) bool {
		if !keys[i].CreatedAt.Equal(keys[j].CreatedAt) {
			return keys[i].CreatedAt.After(keys[j].CreatedAt)
		}
		return keys[i].ID < keys[j].ID
	})
}
