package platform

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/gofiber/fiber/v2"
	bolt "go.etcd.io/bbolt"
)

// boltStore is the primary Store implementation: bbolt (etcd-io/bbolt, pure Go,
// no CGO) with the contract buckets keys / usage / settings plus the S4
// sessions and usage_daily (计数索引) buckets.
type boltStore struct {
	db *bolt.DB
}

func boltBucketName(name string) []byte {
	return []byte(name)
}

// newBoltStore opens (and creates buckets in) the bbolt database file.
func newBoltStore(path string) (*boltStore, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{bucketKeys, bucketUsage, bucketSettings, bucketSessions, bucketUsageDaily} {
			if _, err := tx.CreateBucketIfNotExists(boltBucketName(name)); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &boltStore{db: db}
	// S4-GAP 性能项(b): rebuild the usage daily-count index once at startup so
	// UsageAggregate / UsageTimeseries / ListUsagePage totals stay O(days)
	// instead of full-table scans.
	if err := s.rebuildUsageIndex(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("rebuild usage index: %w", err)
	}
	return s, nil
}

// Backup writes a consistent hot snapshot of the whole database via
// bolt.Tx.WriteTo (S4-GAP 运维: bbolt 热备). The output is a valid web2api.db.
func (s *boltStore) Backup(w io.Writer) error {
	return s.db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(w)
		return err
	})
}

func (s *boltStore) ListKeys() ([]APIKey, error) {
	var keys []APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketKeys)).ForEach(func(_, v []byte) error {
			var k APIKey
			if err := json.Unmarshal(v, &k); err != nil {
				return fmt.Errorf("corrupt key record: %w", err)
			}
			keys = append(keys, k)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sortKeysNewestFirst(keys)
	return keys, nil
}

func (s *boltStore) GetKeyByID(id string) (*APIKey, error) {
	var found *APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(boltBucketName(bucketKeys)).Get([]byte(id))
		if v == nil {
			return nil
		}
		var k APIKey
		if err := json.Unmarshal(v, &k); err != nil {
			return fmt.Errorf("corrupt key record: %w", err)
		}
		found = &k
		return nil
	})
	return found, err
}

// GetKeyByHash scans the (small) keys bucket for a matching key hash. Key count
// is single-digit for an internal platform, so a linear scan is fine.
func (s *boltStore) GetKeyByHash(keyHash string) (*APIKey, error) {
	var found *APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketKeys)).ForEach(func(_, v []byte) error {
			if found != nil {
				return nil
			}
			var k APIKey
			if err := json.Unmarshal(v, &k); err != nil {
				return fmt.Errorf("corrupt key record: %w", err)
			}
			if k.KeyHash == keyHash {
				found = &k
			}
			return nil
		})
	})
	return found, err
}

func (s *boltStore) PutKey(key *APIKey) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, err := json.Marshal(key)
		if err != nil {
			return err
		}
		return tx.Bucket(boltBucketName(bucketKeys)).Put([]byte(key.ID), raw)
	})
}

func (s *boltStore) DeleteKey(id string) (bool, error) {
	existed := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(boltBucketName(bucketKeys))
		if b.Get([]byte(id)) == nil {
			return nil
		}
		existed = true
		return b.Delete([]byte(id))
	})
	return existed, err
}

func (s *boltStore) RecordUsage(rec *UsageRecord, keyID string, units int64, consume bool, at time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		settings := tx.Bucket(boltBucketName(bucketSettings))
		seq := uint64(1)
		if raw := settings.Get([]byte(settingUsageSeq)); raw != nil {
			seq = binary.BigEndian.Uint64(raw) + 1
		}
		seqBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(seqBuf, seq)
		if err := settings.Put([]byte(settingUsageSeq), seqBuf); err != nil {
			return err
		}
		rec.Seq = int64(seq)

		usage := tx.Bucket(boltBucketName(bucketUsage))
		rawRec, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := usage.Put([]byte(fmt.Sprintf("%020d", seq)), rawRec); err != nil {
			return err
		}

		// S4-GAP 性能项(b): maintain the per-day count index in the SAME
		// transaction, so aggregates never need a full-table scan.
		if err := s.indexUsageLocked(tx, rec); err != nil {
			return err
		}

		// Update the key counters in the same transaction.
		keys := tx.Bucket(boltBucketName(bucketKeys))
		if rawKey := keys.Get([]byte(keyID)); rawKey != nil {
			var k APIKey
			if err := json.Unmarshal(rawKey, &k); err != nil {
				return fmt.Errorf("corrupt key record: %w", err)
			}
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
			rawUpdated, err := json.Marshal(&k)
			if err != nil {
				return err
			}
			return keys.Put([]byte(k.ID), rawUpdated)
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// S4-GAP 性能项(b): usage daily-count index
//
// bucketUsageDaily maps YYYY-MM-DD → usageDailyBucket (JSON): per-day totals,
// per-engine stats and per-key breakdowns. bucketUsageByIndex would be a
// second index (per-key → seqs); the per-day/per-key breakdown already makes
// every aggregate and page total O(days), and page items walk the usage bucket
// newest-first with bounded early termination on the explicit time filter.
// ---------------------------------------------------------------------------

// usageKeyDayStat is the per-key breakdown of one day.
type usageKeyDayStat struct {
	Calls    int64 `json:"c"`
	Units    int64 `json:"u"`
	OK       int64 `json:"ok"`
	Rejected int64 `json:"rj"`
}

// usageDailyBucket is one day's aggregate counters.
type usageDailyBucket struct {
	Calls    int64                     `json:"c"`
	Units    int64                     `json:"u"`
	OK       int64                     `json:"ok"`
	Rejected int64                     `json:"rj"`
	ByEngine map[string]EngineStat     `json:"be,omitempty"`
	Keys     map[string]usageKeyDayStat `json:"keys,omitempty"`
}

func (b *usageDailyBucket) ensure() {
	if b.ByEngine == nil {
		b.ByEngine = map[string]EngineStat{}
	}
	if b.Keys == nil {
		b.Keys = map[string]usageKeyDayStat{}
	}
}

func (b *usageDailyBucket) add(rec *UsageRecord) {
	b.ensure()
	reached := recordReached(rec)
	ok := rec.Status == fiber.StatusOK && reached
	b.Calls++
	b.Units += int64(rec.Units)
	if ok {
		b.OK++
	}
	if !reached {
		b.Rejected++
	}
	for engine, stat := range rec.EngineStats {
		e := b.ByEngine[engine]
		e.Calls += stat.Calls
		e.OK += stat.OK
		b.ByEngine[engine] = e
	}
	ks := b.Keys[rec.KeyID]
	ks.Calls++
	ks.Units += int64(rec.Units)
	if ok {
		ks.OK++
	}
	if !reached {
		ks.Rejected++
	}
	b.Keys[rec.KeyID] = ks
}

// indexUsageLocked updates the day bucket for rec (caller inside an Update tx).
func (s *boltStore) indexUsageLocked(tx *bolt.Tx, rec *UsageRecord) error {
	day := localDate(rec.Time)
	bucket := tx.Bucket(boltBucketName(bucketUsageDaily))
	var dayStat usageDailyBucket
	if raw := bucket.Get([]byte(day)); raw != nil {
		if err := json.Unmarshal(raw, &dayStat); err != nil {
			// Corrupt index: rebuild this bucket from the usage records of the
			// day instead of failing the write.
			dayStat = usageDailyBucket{}
		}
	}
	dayStat.add(rec)
	raw, err := json.Marshal(&dayStat)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(day), raw)
}

// rebuildUsageIndex re-derives every day bucket from the usage journal.
func (s *boltStore) rebuildUsageIndex() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(boltBucketName(bucketUsageDaily)).ForEach(func(k, _ []byte) error {
			return tx.Bucket(boltBucketName(bucketUsageDaily)).Delete(k)
		}); err != nil {
			return err
		}
		return tx.Bucket(boltBucketName(bucketUsage)).ForEach(func(_, v []byte) error {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			return s.indexUsageLocked(tx, &r)
		})
	})
}

func (s *boltStore) AllUsage() ([]UsageRecord, error) {
	var records []UsageRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsage)).ForEach(func(_, v []byte) error {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			records = append(records, r)
			return nil
		})
	})
	return records, err
}

// UsageAggregate builds the overview aggregate from the per-day index
// (O(days), S4-GAP 性能项 b) plus a bounded newest-first scan for the 24h
// window (records are seq-ordered, so the scan stops at the window edge).
func (s *boltStore) UsageAggregate() (*UsageAggregate, error) {
	now := time.Now()
	today := localDate(now)
	window24h := now.Add(-24 * time.Hour)
	agg := &UsageAggregate{ByEngine: map[string]EngineStat{}}

	// Day-index derived counters: totals, today, all-time per-engine.
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsageDaily)).ForEach(func(k, v []byte) error {
			var day usageDailyBucket
			if err := json.Unmarshal(v, &day); err != nil {
				return fmt.Errorf("corrupt usage day bucket: %w", err)
			}
			// P-01 有效调用口径: TotalCalls/TodayCalls count reached calls
			// only (billed + engine_failed); TodayCallsAll/Rejected24h expose
			// the full journal (additive).
			reached := day.Calls - day.Rejected
			if reached < 0 {
				reached = 0
			}
			agg.TotalCalls += reached
			agg.TotalUnits += day.Units
			if string(k) == today {
				agg.TodayCallsAll += day.Calls
				agg.TodayUnits += day.Units
				agg.TodayCalls += reached
			}
			for engine, stat := range day.ByEngine {
				e := agg.ByEngine[engine]
				e.Calls += stat.Calls
				e.OK += stat.OK
				agg.ByEngine[engine] = e
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	// 24h window: bounded backward scan over the usage bucket (seq keys are
	// insertion-ordered and traffic is time-ascending; the scan stops once a
	// record falls out of the window).
	err = s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(boltBucketName(bucketUsage)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			if r.Time.Before(window24h) {
				return nil
			}
			if recordReached(&r) {
				agg.Calls24h++
				if r.Status == fiber.StatusOK {
					agg.OK24h++
				}
			} else {
				agg.Rejected24h++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return agg, nil
}

// UsageTimeseries reads the last `days` day buckets (O(days), zero-filled,
// ascending). Reached-calls semantics: rejected requests are excluded from
// Calls (P-01 口径), matching the overview counters.
func (s *boltStore) UsageTimeseries(days int) ([]UsageDayStat, error) {
	now := time.Now()
	out := make([]UsageDayStat, 0, days)
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(boltBucketName(bucketUsageDaily))
		for i := days - 1; i >= 0; i-- {
			date := localDate(now.AddDate(0, 0, -i))
			stat := UsageDayStat{Date: date}
			if raw := bucket.Get([]byte(date)); raw != nil {
				var day usageDailyBucket
				if err := json.Unmarshal(raw, &day); err != nil {
					return fmt.Errorf("corrupt usage day bucket: %w", err)
				}
				stat.Calls = int(day.Calls - day.Rejected)
				if stat.Calls < 0 {
					stat.Calls = 0
				}
				stat.Units = int(day.Units)
				stat.OK = int(day.OK)
			}
			out = append(out, stat)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListUsagePage walks the usage bucket newest-first (seq keys are zero-padded
// numbers, so key order == insertion order), applying the key/time filters and
// offset/limit, and returns the filtered total (O(days) via the day index).
// The page walk terminates early once the requested page slice is complete and
// the explicit From bound is passed.
func (s *boltStore) ListUsagePage(filter UsagePageFilter) ([]UsageRecord, int, error) {
	// Filtered total from the day index: per-key or day-level counters.
	total, totalErr := s.usageFilteredTotal(filter)
	if totalErr != nil {
		return nil, 0, totalErr
	}

	var (
		items   []UsageRecord
		skipped int
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(boltBucketName(bucketUsage)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			if !filter.To.IsZero() && r.Time.After(filter.To) {
				continue
			}
			if !filter.From.IsZero() && r.Time.Before(filter.From) {
				// Records are insertion-ordered (time ascending for live
				// traffic); below the explicit lower bound we can stop.
				return nil
			}
			if filter.KeyID != "" && r.KeyID != filter.KeyID {
				continue
			}
			if skipped < filter.Offset {
				skipped++
				continue
			}
			if len(items) < filter.Limit {
				items = append(items, r)
			}
			if len(items) >= filter.Limit && filter.Limit > 0 {
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if filter.Limit <= 0 {
		items = nil
	}
	return items, total, nil
}

// usageFilteredTotal computes the matched-record count for the filter from the
// day index (O(days) bucket reads, no record unmarshal).
func (s *boltStore) usageFilteredTotal(filter UsagePageFilter) (int, error) {
	total := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsageDaily)).ForEach(func(k, v []byte) error {
			date := string(k)
			if !filter.From.IsZero() && dateBefore(date, localDate(filter.From)) {
				return nil
			}
			if !filter.To.IsZero() && dateBefore(localDate(filter.To), date) {
				return nil
			}
			var day usageDailyBucket
			if err := json.Unmarshal(v, &day); err != nil {
				return fmt.Errorf("corrupt usage day bucket: %w", err)
			}
			if filter.KeyID != "" {
				if ks, ok := day.Keys[filter.KeyID]; ok {
					total += int(ks.Calls)
				}
				return nil
			}
			total += int(day.Calls)
			return nil
		})
	})
	return total, err
}

// dateBefore compares two YYYY-MM-DD strings lexically (same calendar format).
func dateBefore(a, b string) bool { return a < b }

// PruneUsageBefore deletes usage records older than cutoff (by record time)
// and returns the number of removed entries. The day index is rebuilt after
// the delete so aggregate counters stay exact.
func (s *boltStore) PruneUsageBefore(cutoff time.Time) (int64, error) {
	var removed int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(boltBucketName(bucketUsage))
		var keys [][]byte
		if err := b.ForEach(func(k, v []byte) error {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			if r.Time.Before(cutoff) {
				keys = append(keys, append([]byte(nil), k...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	if err == nil && removed > 0 {
		if rbErr := s.rebuildUsageIndex(); rbErr != nil {
			return removed, rbErr
		}
	}
	return removed, err
}

// ---------------------------------------------------------------------------
// S4-GAP 会话系统: sessions bucket CRUD (JSON records, id-keyed).
// ---------------------------------------------------------------------------

func (s *boltStore) ListSessions() ([]AdminSession, error) {
	var sessions []AdminSession
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketSessions)).ForEach(func(_, v []byte) error {
			var sess AdminSession
			if err := json.Unmarshal(v, &sess); err != nil {
				return fmt.Errorf("corrupt session record: %w", err)
			}
			sessions = append(sessions, sess)
			return nil
		})
	})
	return sessions, err
}

func (s *boltStore) GetSession(id string) (*AdminSession, error) {
	var found *AdminSession
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(boltBucketName(bucketSessions)).Get([]byte(id))
		if v == nil {
			return nil
		}
		var sess AdminSession
		if err := json.Unmarshal(v, &sess); err != nil {
			return fmt.Errorf("corrupt session record: %w", err)
		}
		found = &sess
		return nil
	})
	return found, err
}

func (s *boltStore) PutSession(sess *AdminSession) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, err := json.Marshal(sess)
		if err != nil {
			return err
		}
		return tx.Bucket(boltBucketName(bucketSessions)).Put([]byte(sess.ID), raw)
	})
}

func (s *boltStore) DeleteSession(id string) (bool, error) {
	existed := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(boltBucketName(bucketSessions))
		if b.Get([]byte(id)) == nil {
			return nil
		}
		existed = true
		return b.Delete([]byte(id))
	})
	return existed, err
}

func (s *boltStore) DeleteAllSessions(keepID string) (int, error) {
	deleted := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(boltBucketName(bucketSessions))
		var ids [][]byte
		if err := b.ForEach(func(k, _ []byte) error {
			if keepID != "" && string(k) == keepID {
				return nil
			}
			ids = append(ids, append([]byte(nil), k...))
			return nil
		}); err != nil {
			return err
		}
		for _, id := range ids {
			if err := b.Delete(id); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return deleted, err
}

func (s *boltStore) GetSetting(name string) ([]byte, error) {
	var value []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(boltBucketName(bucketSettings)).Get([]byte(name)); v != nil {
			value = append([]byte(nil), v...)
		}
		return nil
	})
	return value, err
}

func (s *boltStore) PutSetting(name string, value []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketSettings)).Put([]byte(name), value)
	})
}

func (s *boltStore) Close() error {
	return s.db.Close()
}
