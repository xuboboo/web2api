package platform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
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
	Keys     map[string]*APIKey `json:"keys"`
	Usage    []UsageRecord      `json:"usage"`
	Settings map[string]string  `json:"settings"` // base64-encoded values
	Seq      uint64             `json:"usage_seq"`
}

func newJSONStore(path string) (*jsonStore, error) {
	s := &jsonStore{
		path: path,
		data: jsonStoreData{
			Keys:     map[string]*APIKey{},
			Usage:    []UsageRecord{},
			Settings: map[string]string{},
		},
	}
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) > 0 {
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
		return s, nil
	}
	// Missing file (or unreadable) — start fresh. If the file existed but was
	// unreadable the caller has already been warned by New().
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
	s.data.Keys[key.ID] = &cp
	return s.saveLocked()
}

func (s *jsonStore) DeleteKey(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Keys[id]; !ok {
		return false, nil
	}
	delete(s.data.Keys, id)
	return true, s.saveLocked()
}

func (s *jsonStore) RecordUsage(rec *UsageRecord, keyID string, units int64, consume bool, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Seq++
	rec.Seq = int64(s.data.Seq)
	s.data.Usage = append(s.data.Usage, *rec)
	if k, ok := s.data.Keys[keyID]; ok {
		atCopy := at
		k.LastUsedAt = &atCopy
		if consume && units > 0 {
			today := localDate(at)
			if k.TodayDate != today {
				k.TodayDate = today
				k.UsedToday = units
			} else {
				k.UsedToday += units
			}
			k.UsedTotal += units
		}
	}
	return s.saveLocked()
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
	for _, r := range s.data.Usage {
		agg.TotalCalls++
		agg.TotalUnits += int64(r.Units)
		if localDate(r.Time) == today {
			agg.TodayCalls++
			agg.TodayUnits += int64(r.Units)
		}
		if r.Time.After(window24h) {
			agg.Calls24h++
			if r.Status == fiber.StatusOK {
				agg.OK24h++
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
		bucket.Calls++
		bucket.Units += r.Units
		if r.Status == fiber.StatusOK {
			bucket.OK++
		}
	}
	out := make([]UsageDayStat, 0, len(dates))
	for _, date := range dates {
		out = append(out, *buckets[date])
	}
	return out, nil
}

// ListUsagePage returns the filtered newest-first page plus the filtered
// total. Records are stored ascending by seq; walk backwards.
func (s *jsonStore) ListUsagePage(filter UsagePageFilter) ([]UsageRecord, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := 0
	var items []UsageRecord
	skipped := 0
	for i := len(s.data.Usage) - 1; i >= 0; i-- {
		r := s.data.Usage[i]
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

// PruneUsageBefore deletes records older than cutoff and returns the count.
func (s *jsonStore) PruneUsageBefore(cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.data.Usage[:0]
	var removed int64
	for _, r := range s.data.Usage {
		if r.Time.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	s.data.Usage = kept
	if removed > 0 {
		if err := s.saveLocked(); err != nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Settings[name] = base64.StdEncoding.EncodeToString(value)
	return s.saveLocked()
}

func (s *jsonStore) Close() error {
	return nil
}
