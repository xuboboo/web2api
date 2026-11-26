package platform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// jsonStore is the fallback Store implementation used when the bbolt database
// cannot be opened (dependency unavailable, file locked, unsupported
// filesystem). It persists the identical data model to a single JSON document
// (data_dir/web2api.json) and implements the same Store interface, per the
// product contract's fallback clause.
type jsonStore struct {
	mu   sync.Mutex
	path string
	data jsonStoreData
}

type jsonStoreData struct {
	Keys      map[string]*APIKey         `json:"keys"`
	Usage     []UsageRecord              `json:"usage"`
	Settings  map[string]string          `json:"settings"` // base64-encoded values
	Seq       uint64                     `json:"usage_seq"`
	Sessions  map[string]*AdminSession   `json:"sessions,omitempty"`  // S4-GAP 会话系统
	Users     map[string]*User           `json:"users,omitempty"`     // 控制台用户与角色
	Blacklist map[string]*BlacklistEntry `json:"blacklist,omitempty"` // IP/CIDR 黑名单
	Notices   map[string]*Notice         `json:"notices,omitempty"`
}

func newJSONStore(path string) (*jsonStore, error) {
	s := &jsonStore{
		path: path,
		data: jsonStoreData{
			Keys:      map[string]*APIKey{},
			Usage:     []UsageRecord{},
			Settings:  map[string]string{},
			Sessions:  map[string]*AdminSession{},
			Users:     map[string]*User{},
			Blacklist: map[string]*BlacklistEntry{},
			Notices:   map[string]*Notice{},
		},
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("platform: corrupt JSON fallback store %s: %w", path, err)
		}
		if s.data.Keys == nil {
			s.data.Keys = map[string]*APIKey{}
		}
		if s.data.Settings == nil {
			s.data.Settings = map[string]string{}
		}
		if s.data.Usage == nil {
			s.data.Usage = []UsageRecord{}
		}
		if s.data.Sessions == nil {
			s.data.Sessions = map[string]*AdminSession{}
		}
		if s.data.Users == nil {
			s.data.Users = map[string]*User{}
		}
		if s.data.Blacklist == nil {
			s.data.Blacklist = map[string]*BlacklistEntry{}
		}
		if s.data.Notices == nil {
			s.data.Notices = map[string]*Notice{}
		}
		return s, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("platform: read JSON fallback store %s: %w", path, err)
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// saveLocked writes the whole document atomically (temp file + rename).
func (s *jsonStore) saveLocked() error {
	raw, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Backup serializes the whole document (S4-GAP 运维: JSON fallback snapshot;
// restorable via admin-restore into the same kind of store).
func (s *jsonStore) Backup(w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func (s *jsonStore) ListKeys() ([]APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]APIKey, 0, len(s.data.Keys))
	for _, k := range s.data.Keys {
		keys = append(keys, *k)
	}
	sortKeysNewestFirst(keys)
	return keys, nil
}

func (s *jsonStore) GetKeyByID(id string) (*APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.data.Keys[id]; ok {
		cp := *k
		return &cp, nil
	}
	return nil, nil
}

func (s *jsonStore) GetKeyByHash(keyHash string) (*APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.data.Keys {
		if k.KeyHash == keyHash {
			cp := *k
			return &cp, nil
		}
	}
	return nil, nil
}

func (s *jsonStore) PutKey(key *APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *key
	old := s.data.Keys[key.ID]
	s.data.Keys[key.ID] = &cp
	if err := s.saveLocked(); err != nil {
		if old == nil {
			delete(s.data.Keys, key.ID)
		} else {
			s.data.Keys[key.ID] = old
		}
		return err
	}
	return nil
}

func (s *jsonStore) UpdateKey(id string, edit func(*APIKey)) (*APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Keys[id]
	if old == nil {
		return nil, nil
	}
	updated := *old
	edit(&updated)
	s.data.Keys[id] = &updated
	if err := s.saveLocked(); err != nil {
		s.data.Keys[id] = old
		return nil, err
	}
	result := updated
	return &result, nil
}

func (s *jsonStore) DeleteKey(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Keys[id]
	if old == nil {
		return false, nil
	}
	delete(s.data.Keys, id)
	if err := s.saveLocked(); err != nil {
		s.data.Keys[id] = old
		return false, err
	}
	return true, nil
}

func (s *jsonStore) RecordUsage(rec *UsageRecord, keyID string, units int64, consume bool, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldSeq := rec.Seq
	s.data.Seq++
	rec.Seq = int64(s.data.Seq)
	s.data.Usage = append(s.data.Usage, *rec)
	oldKey := s.data.Keys[keyID]
	if oldKey != nil {
		key := *oldKey
		applyKeyUsage(&key, units, consume, at)
		s.data.Keys[keyID] = &key
	}
	if err := s.saveLocked(); err != nil {
		s.data.Seq--
		s.data.Usage = s.data.Usage[:len(s.data.Usage)-1]
		rec.Seq = oldSeq
		if oldKey != nil {
			s.data.Keys[keyID] = oldKey
		}
		return err
	}
	return nil
}

func (s *jsonStore) CommitJobCharge(marker string, charge jobCharge, rec *UsageRecord, keyID string, units int64, at time.Time) (jobCharge, bool, error) {
	if err := validateJobCharge(marker, charge, rec, keyID, units); err != nil {
		return jobCharge{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if encoded, ok := s.data.Settings[marker]; ok {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return jobCharge{}, false, fmt.Errorf("corrupt job charge marker: %w", err)
		}
		var result jobCharge
		if err := json.Unmarshal(raw, &result); err != nil {
			return jobCharge{}, false, fmt.Errorf("corrupt job charge marker: %w", err)
		}
		if result.Fingerprint != charge.Fingerprint || result.KeyID != keyID || result.Units != units {
			return jobCharge{}, false, errJobChargeConflict
		}
		return result, true, nil
	}
	key := s.data.Keys[keyID]
	if key == nil || key.Status != KeyStatusActive || (key.ExpiresAt != nil && !at.Before(*key.ExpiresAt)) {
		return jobCharge{}, false, errJobChargeKeyUnavailable
	}
	if units <= 0 || (key.DailyQuota > 0 && units > key.DailyQuota-usedTodayUnits(key, at)) {
		return jobCharge{}, false, errJobChargeQuotaExceeded
	}
	raw, err := json.Marshal(charge)
	if err != nil {
		return jobCharge{}, false, err
	}
	oldSeq := rec.Seq
	s.data.Seq++
	rec.Seq = int64(s.data.Seq)
	s.data.Usage = append(s.data.Usage, *rec)
	updated := *key
	applyKeyUsage(&updated, units, true, at)
	s.data.Keys[keyID] = &updated
	s.data.Settings[marker] = base64.StdEncoding.EncodeToString(raw)
	if err := s.saveLocked(); err != nil {
		s.data.Seq--
		s.data.Usage = s.data.Usage[:len(s.data.Usage)-1]
		rec.Seq = oldSeq
		s.data.Keys[keyID] = key
		delete(s.data.Settings, marker)
		return jobCharge{}, false, err
	}
	return charge, false, nil
}

func (s *jsonStore) AllUsage() ([]UsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Records are appended in ascending seq order.
	out := make([]UsageRecord, len(s.data.Usage))
	copy(out, s.data.Usage)
	return out, nil
}

// UsageAggregate scans the journal once without retaining records.
func (s *jsonStore) UsageAggregate() (*UsageAggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	today := localDate(now)
	window24h := now.Add(-24 * time.Hour)
	agg := &UsageAggregate{ByEngine: map[string]EngineStat{}}
	for i := range s.data.Usage {
		r := &s.data.Usage[i]
		// B-01/P-01 有效调用口径: reached calls only in the call counters;
		// rejections surface via the additive fields.
		reached := recordReached(r)
		agg.TotalUnits += int64(r.Units)
		if reached {
			agg.TotalCalls++
		}
		if localDate(r.Time) == today {
			agg.TodayUnits += int64(r.Units)
			agg.TodayCallsAll++
			if reached {
				agg.TodayCalls++
			}
		}
		if r.Time.After(window24h) {
			if reached {
				agg.Calls24h++
				if r.Status == fiber.StatusOK {
					agg.OK24h++
				}
			} else {
				agg.Rejected24h++
			}
		}
		for engine, stat := range r.EngineStats {
			e := agg.ByEngine[engine]
			e.Calls += stat.Calls
			e.OK += stat.OK
			agg.ByEngine[engine] = e
		}
	}
	return agg, nil
}

// UsageTimeseries buckets the journal by local date, zero-filled, ascending.
func (s *jsonStore) UsageTimeseries(days int) ([]UsageDayStat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	buckets := make(map[string]*UsageDayStat, days)
	dates := make([]string, 0, days)
	for i := days - 1; i >= 0; i-- {
		date := localDate(now.AddDate(0, 0, -i))
		buckets[date] = &UsageDayStat{Date: date}
		dates = append(dates, date)
	}
	for _, r := range s.data.Usage {
		bucket, ok := buckets[localDate(r.Time)]
		if !ok {
			continue
		}
		if recordReached(&r) {
			bucket.Calls++
			bucket.Units += r.Units
			if r.Status == fiber.StatusOK {
				bucket.OK++
			}
		}
	}
	out := make([]UsageDayStat, 0, len(dates))
	for _, date := range dates {
		out = append(out, *buckets[date])
	}
	return out, nil
}

// ListUsagePage returns the filtered newest-first page plus the filtered
// total. Records are stored ascending by seq; walk backwards. S4-GAP: From/To
// bound the walk by record time (To inclusive).
func (s *jsonStore) ListUsagePage(filter UsagePageFilter) ([]UsageRecord, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := 0
	var items []UsageRecord
	skipped := 0
	for i := len(s.data.Usage) - 1; i >= 0; i-- {
		r := s.data.Usage[i]
		if !filter.To.IsZero() && r.Time.After(filter.To) {
			continue
		}
		if !filter.From.IsZero() && r.Time.Before(filter.From) {
			continue
		}
		if filter.KeyID != "" && r.KeyID != filter.KeyID {
			continue
		}
		matched++
		if filter.Limit <= 0 || len(items) >= filter.Limit {
			continue
		}
		if skipped < filter.Offset {
			skipped++
			continue
		}
		items = append(items, r)
	}
	return items, matched, nil
}

// ---------------------------------------------------------------------------
// S4-GAP 会话系统: in-document session mirror.
// ---------------------------------------------------------------------------

func (s *jsonStore) ListSessions() ([]AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AdminSession, 0, len(s.data.Sessions))
	for _, sess := range s.data.Sessions {
		out = append(out, *sess)
	}
	return out, nil
}

func (s *jsonStore) GetSession(id string) (*AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.data.Sessions[id]; ok {
		cp := *sess
		return &cp, nil
	}
	return nil, nil
}

func (s *jsonStore) PutSession(sess *AdminSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *sess
	old := s.data.Sessions[sess.ID]
	s.data.Sessions[sess.ID] = &cp
	if err := s.saveLocked(); err != nil {
		if old == nil {
			delete(s.data.Sessions, sess.ID)
		} else {
			s.data.Sessions[sess.ID] = old
		}
		return err
	}
	return nil
}

func (s *jsonStore) DeleteSession(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sessions[id]; !ok {
		return false, nil
	}
	delete(s.data.Sessions, id)
	return true, s.saveLocked()
}

func (s *jsonStore) DeleteAllSessions(keepID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id := range s.data.Sessions {
		if keepID != "" && id == keepID {
			continue
		}
		delete(s.data.Sessions, id)
		deleted++
	}
	if deleted > 0 {
		return deleted, s.saveLocked()
	}
	return deleted, nil
}

// PruneUsageBefore deletes records older than cutoff and returns the count.
func (s *jsonStore) PruneUsageBefore(cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]UsageRecord, 0, len(s.data.Usage))
	var removed int64
	for _, r := range s.data.Usage {
		if r.Time.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	if removed > 0 {
		old := s.data.Usage
		s.data.Usage = kept
		if err := s.saveLocked(); err != nil {
			s.data.Usage = old
			return removed, err
		}
	}
	return removed, nil
}

func (s *jsonStore) GetSetting(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Settings[name]
	if !ok {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(v)
}

func (s *jsonStore) PutSetting(name string, value []byte) error {
	return s.PutSettings(map[string][]byte{name: value})
}

func (s *jsonStore) PutSettings(values map[string][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Settings
	updated := make(map[string]string, len(old)+len(values))
	for name, value := range old {
		updated[name] = value
	}
	for name, value := range values {
		updated[name] = base64.StdEncoding.EncodeToString(value)
	}
	s.data.Settings = updated
	if err := s.saveLocked(); err != nil {
		s.data.Settings = old
		return err
	}
	return nil
}

func (s *jsonStore) ListSettings(prefix string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := map[string][]byte{}
	for name, raw := range s.data.Settings {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, err
		}
		values[name] = decoded
	}
	return values, nil
}

func (s *jsonStore) DeleteSetting(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.data.Settings[name]
	if !ok {
		return nil
	}
	delete(s.data.Settings, name)
	if err := s.saveLocked(); err != nil {
		s.data.Settings[name] = old
		return err
	}
	return nil
}

func (s *jsonStore) ListNotices(userID string, limit int) ([]Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []Notice{}
	for _, n := range s.data.Notices {
		if n != nil && (userID == "" || n.UserID == userID) {
			rows = append(rows, *n)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
func (s *jsonStore) PutNotice(n *Notice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Notices[n.ID]
	removed := map[string]*Notice{}
	copy := *n
	s.data.Notices[n.ID] = &copy
	if len(s.data.Notices) > 1000 {
		rows := make([]Notice, 0, len(s.data.Notices))
		for _, row := range s.data.Notices {
			if row != nil {
				rows = append(rows, *row)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
		for _, row := range rows[:len(rows)-1000] {
			removed[row.ID] = s.data.Notices[row.ID]
			delete(s.data.Notices, row.ID)
		}
	}
	if err := s.saveLocked(); err != nil {
		if old == nil {
			delete(s.data.Notices, n.ID)
		} else {
			s.data.Notices[n.ID] = old
		}
		for id, row := range removed {
			s.data.Notices[id] = row
		}
		return err
	}
	return nil
}
func (s *jsonStore) AcknowledgeNotice(userID, id string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.data.Notices[id]
	if n == nil || (userID != "" && n.UserID != userID) {
		return false, nil
	}
	old := n.AcknowledgedAt
	if old == nil {
		n.AcknowledgedAt = &at
	}
	if err := s.saveLocked(); err != nil {
		n.AcknowledgedAt = old
		return false, err
	}
	return true, nil
}

func (s *jsonStore) Close() error {
	return nil
}
