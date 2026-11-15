package platform

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	bolt "go.etcd.io/bbolt"
)

// boltStore is the primary Store implementation: bbolt (etcd-io/bbolt, pure Go,
// no CGO) with the contract buckets keys / usage / settings.
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
		for _, name := range []string{bucketKeys, bucketUsage, bucketSettings} {
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
	return &boltStore{db: db}, nil
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

// UsageAggregate scans the usage bucket once, accumulating counters without
// retaining records, so the overview cost does not scale with allocations.
func (s *boltStore) UsageAggregate() (*UsageAggregate, error) {
	now := time.Now()
	today := localDate(now)
	window24h := now.Add(-24 * time.Hour)
	agg := &UsageAggregate{ByEngine: map[string]EngineStat{}}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsage)).ForEach(func(_, v []byte) error {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
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
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return agg, nil
}

// UsageTimeseries buckets the journal by local date for the last `days` days,
// zero-filled and ascending.
func (s *boltStore) UsageTimeseries(days int) ([]UsageDayStat, error) {
	now := time.Now()
	buckets := make(map[string]*UsageDayStat, days)
	dates := make([]string, 0, days)
	for i := days - 1; i >= 0; i-- {
		date := localDate(now.AddDate(0, 0, -i))
		buckets[date] = &UsageDayStat{Date: date}
		dates = append(dates, date)
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsage)).ForEach(func(_, v []byte) error {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			bucket, ok := buckets[localDate(r.Time)]
			if !ok {
				return nil
			}
			bucket.Calls++
			bucket.Units += r.Units
			if r.Status == fiber.StatusOK {
				bucket.OK++
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]UsageDayStat, 0, len(dates))
	for _, date := range dates {
		out = append(out, *buckets[date])
	}
	return out, nil
}

// ListUsagePage walks the usage bucket newest-first (seq keys are zero-padded
// numbers, so key order == insertion order), applying the key filter and
// offset/limit, and returns the filtered total. Only the page slice is
// materialized.
func (s *boltStore) ListUsagePage(filter UsagePageFilter) ([]UsageRecord, int, error) {
	var (
		items   []UsageRecord
		matched int
		skipped int
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(boltBucketName(bucketUsage)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var r UsageRecord
			if err := json.Unmarshal(v, &r); err != nil {
				return fmt.Errorf("corrupt usage record: %w", err)
			}
			if filter.KeyID != "" && r.KeyID != filter.KeyID {
				continue
			}
			matched++
			if skipped < filter.Offset {
				skipped++
				continue
			}
			if len(items) < filter.Limit {
				items = append(items, r)
			}
			if len(items) >= filter.Limit && filter.Limit > 0 {
				// Keep counting the rest for the pagination total.
				continue
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
	return items, matched, nil
}

// PruneUsageBefore deletes usage records older than cutoff (by record time)
// and returns the number of removed entries.
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
	return removed, err
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
