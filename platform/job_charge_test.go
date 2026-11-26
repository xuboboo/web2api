package platform

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type chargeTestStore interface {
	Store
	jobChargeStore
}

func TestJobChargeAtomicAndIdempotent(t *testing.T) {
	for _, kind := range []string{"bbolt", "json"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store")
			open := func() chargeTestStore {
				if kind == "bbolt" {
					s, err := newBoltStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return s
				}
				s, err := newJSONStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			store := open()
			now := time.Now()
			if err := store.PutKey(testKey("job-key", "test", 2, now)); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for i := 0; i < cap(results); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					rec := &UsageRecord{Time: now, KeyID: "job-key", Units: 2, Status: 202}
					got, _, err := store.CommitJobCharge("test-marker", jobCharge{JobID: "job-1", Fingerprint: "request-1", KeyID: "job-key", Units: 2}, rec, "job-key", 2, now)
					if err == nil && got.JobID != "job-1" {
						err = errors.New("wrong job returned")
					}
					results <- err
				}()
			}
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatal(err)
				}
			}
			key, err := store.GetKeyByID("job-key")
			if err != nil || key.UsedTotal != 2 {
				t.Fatalf("key=%+v err=%v", key, err)
			}
			usage, err := store.AllUsage()
			if err != nil || len(usage) != 1 {
				t.Fatalf("usage=%+v err=%v", usage, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store = open()
			defer func() {
				if err := store.Close(); err != nil {
					t.Errorf("close store: %v", err)
				}
			}()
			rec := &UsageRecord{Time: now, KeyID: "job-key", Units: 2, Status: 202}
			got, replayed, err := store.CommitJobCharge("test-marker", jobCharge{JobID: "different-proposed-id", Fingerprint: "request-1", KeyID: "job-key", Units: 2}, rec, "job-key", 2, now)
			if err != nil || !replayed || got.JobID != "job-1" {
				t.Fatalf("replay=%+v replayed=%v err=%v", got, replayed, err)
			}
			_, _, err = store.CommitJobCharge("test-marker", jobCharge{JobID: "job-2", Fingerprint: "different", KeyID: "job-key", Units: 2}, rec, "job-key", 2, now)
			if !errors.Is(err, errJobChargeConflict) {
				t.Fatalf("conflict error=%v", err)
			}
			rec.Units = 1
			_, _, err = store.CommitJobCharge("new-marker", jobCharge{JobID: "job-2", Fingerprint: "request-2", KeyID: "job-key", Units: 1}, rec, "job-key", 1, now)
			if !errors.Is(err, errJobChargeQuotaExceeded) {
				t.Fatalf("quota error=%v", err)
			}
			if err := store.PutKey(testKey("limited", "limited", 1, now)); err != nil {
				t.Fatal(err)
			}
			var quotaWG sync.WaitGroup
			quotaResults := make(chan error, 8)
			for i := 0; i < cap(quotaResults); i++ {
				quotaWG.Add(1)
				go func(i int) {
					defer quotaWG.Done()
					rec := &UsageRecord{Time: now, KeyID: "limited", Units: 1, Status: 202}
					charge := jobCharge{JobID: fmt.Sprintf("job-%d", i), Fingerprint: "different", KeyID: "limited", Units: 1}
					_, _, err := store.CommitJobCharge(fmt.Sprintf("marker-%d", i), charge, rec, "limited", 1, now)
					quotaResults <- err
				}(i)
			}
			quotaWG.Wait()
			close(quotaResults)
			succeeded := 0
			for err := range quotaResults {
				if err == nil {
					succeeded++
				} else if !errors.Is(err, errJobChargeQuotaExceeded) {
					t.Fatal(err)
				}
			}
			if succeeded != 1 {
				t.Fatalf("quota concurrency accepted %d jobs, want 1", succeeded)
			}
		})
	}
}

func TestJSONJobChargeRollsBackOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	store, err := newJSONStore(filepath.Join(dir, "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.PutKey(testKey("job-key", "test", 10, now)); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(dir, "missing", "store.json")
	rec := &UsageRecord{Time: now, KeyID: "job-key", Units: 2, Status: 202}
	charge := jobCharge{JobID: "job-1", Fingerprint: "request-1", KeyID: "job-key", Units: 2}
	if _, _, err := store.CommitJobCharge("test-marker", charge, rec, "job-key", 2, now); err == nil {
		t.Fatal("write failure accepted")
	}
	key, err := store.GetKeyByID("job-key")
	if err != nil || key.UsedTotal != 0 || rec.Seq != 0 {
		t.Fatalf("counter was not rolled back: key=%+v seq=%d err=%v", key, rec.Seq, err)
	}
	marker, err := store.GetSetting("test-marker")
	if err != nil || marker != nil {
		t.Fatalf("marker survived failed commit: %q err=%v", marker, err)
	}
	usage, err := store.AllUsage()
	if err != nil || len(usage) != 0 {
		t.Fatalf("usage survived failed commit: %+v err=%v", usage, err)
	}
}
