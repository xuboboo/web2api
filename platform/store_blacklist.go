package platform

// Persistence for the IP/CIDR blacklist, mirrored in both Store backends.

import (
	"encoding/json"
	"fmt"
	"sort"

	bolt "go.etcd.io/bbolt"
)

func (s *boltStore) ListBlacklist() ([]BlacklistEntry, error) {
	var entries []BlacklistEntry
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketBlacklist)).ForEach(func(_, v []byte) error {
			var e BlacklistEntry
			if err := json.Unmarshal(v, &e); err != nil {
				return fmt.Errorf("corrupt blacklist record: %w", err)
			}
			entries = append(entries, e)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

func (s *boltStore) GetBlacklist(id string) (*BlacklistEntry, error) {
	var found *BlacklistEntry
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(boltBucketName(bucketBlacklist)).Get([]byte(id))
		if v == nil {
			return nil
		}
		var e BlacklistEntry
		if err := json.Unmarshal(v, &e); err != nil {
			return fmt.Errorf("corrupt blacklist record: %w", err)
		}
		found = &e
		return nil
	})
	return found, err
}

func (s *boltStore) PutBlacklist(e *BlacklistEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketBlacklist)).Put([]byte(e.ID), raw)
	})
}

func (s *boltStore) DeleteBlacklist(id string) (bool, error) {
	deleted := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(boltBucketName(bucketBlacklist))
		if b.Get([]byte(id)) == nil {
			return nil
		}
		if err := b.Delete([]byte(id)); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	return deleted, err
}

func (s *jsonStore) ListBlacklist() ([]BlacklistEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]BlacklistEntry, 0, len(s.data.Blacklist))
	for _, e := range s.data.Blacklist {
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

func (s *jsonStore) GetBlacklist(id string) (*BlacklistEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.data.Blacklist[id]
	if e == nil {
		return nil, nil
	}
	cp := *e
	return &cp, nil
}

func (s *jsonStore) PutBlacklist(e *BlacklistEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *e
	old, existed := s.data.Blacklist[e.ID]
	s.data.Blacklist[e.ID] = &cp
	if err := s.saveLocked(); err != nil {
		if existed {
			s.data.Blacklist[e.ID] = old
		} else {
			delete(s.data.Blacklist, e.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) DeleteBlacklist(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Blacklist[id]
	if old == nil {
		return false, nil
	}
	delete(s.data.Blacklist, id)
	if err := s.saveLocked(); err != nil {
		s.data.Blacklist[id] = old
		return false, err
	}
	return true, nil
}
