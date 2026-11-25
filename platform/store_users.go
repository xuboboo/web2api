package platform

// Persistence for console users/roles. Implemented for both Store backends so
// the JSON fallback keeps every admin capability of the primary bbolt store.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

func (s *boltStore) ListUsers() ([]User, error) {
	var users []User
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsers)).ForEach(func(_, v []byte) error {
			var u User
			if err := json.Unmarshal(v, &u); err != nil {
				return fmt.Errorf("corrupt user record: %w", err)
			}
			users = append(users, u)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(users, func(i, j int) bool {
		if !users[i].CreatedAt.Equal(users[j].CreatedAt) {
			return users[i].CreatedAt.After(users[j].CreatedAt)
		}
		return users[i].ID < users[j].ID
	})
	return users, nil
}

func (s *boltStore) GetUserByID(id string) (*User, error) {
	var found *User
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(boltBucketName(bucketUsers)).Get([]byte(id))
		if v == nil {
			return nil
		}
		var u User
		if err := json.Unmarshal(v, &u); err != nil {
			return fmt.Errorf("corrupt user record: %w", err)
		}
		found = &u
		return nil
	})
	return found, err
}

func (s *boltStore) GetUserByUsername(username string) (*User, error) {
	want := strings.ToLower(strings.TrimSpace(username))
	var found *User
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsers)).ForEach(func(_, v []byte) error {
			var u User
			if err := json.Unmarshal(v, &u); err != nil {
				return fmt.Errorf("corrupt user record: %w", err)
			}
			if strings.ToLower(u.Username) == want {
				found = &u
			}
			return nil
		})
	})
	return found, err
}

func (s *boltStore) PutUser(u *User) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(boltBucketName(bucketUsers)).Put([]byte(u.ID), raw)
	})
}

func (s *boltStore) DeleteUser(id string) (bool, error) {
	deleted := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(boltBucketName(bucketUsers))
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

func (s *jsonStore) ListUsers() ([]User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]User, 0, len(s.data.Users))
	for _, u := range s.data.Users {
		users = append(users, *u)
	}
	sort.Slice(users, func(i, j int) bool {
		if !users[i].CreatedAt.Equal(users[j].CreatedAt) {
			return users[i].CreatedAt.After(users[j].CreatedAt)
		}
		return users[i].ID < users[j].ID
	})
	return users, nil
}

func (s *jsonStore) GetUserByID(id string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.data.Users[id]
	if u == nil {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func (s *jsonStore) GetUserByUsername(username string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := strings.ToLower(strings.TrimSpace(username))
	for _, u := range s.data.Users {
		if strings.ToLower(u.Username) == want {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}

func (s *jsonStore) PutUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *u
	old, existed := s.data.Users[u.ID]
	s.data.Users[u.ID] = &cp
	if err := s.saveLocked(); err != nil {
		if existed {
			s.data.Users[u.ID] = old
		} else {
			delete(s.data.Users, u.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) DeleteUser(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Users[id]
	if old == nil {
		return false, nil
	}
	delete(s.data.Users, id)
	if err := s.saveLocked(); err != nil {
		s.data.Users[id] = old
		return false, err
	}
	return true, nil
}
