package platform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

type passwordRotation struct {
	user        User
	settings    map[string][]byte // Only the bootstrap credential changes global settings.
	replacement *AdminSession
}

func planPasswordRotation(user *User, hash []byte, version int, bootstrap bool) (passwordRotation, int, error) {
	rotation := passwordRotation{user: *user}
	rotation.user.PasswordHash = hash
	if rotation.user.ID == "" {
		rotation.user.ID = newUserID()
	}
	if bootstrap {
		if version >= 1<<30 {
			return passwordRotation{}, 0, fmt.Errorf("admin token version limit reached")
		}
		version++
		rotation.settings = map[string][]byte{
			settingAdminHash:           hash,
			settingAdminPasswordCustom: []byte("1"),
			settingTokenVersion:        []byte(strconv.Itoa(version)),
		}
	}
	return rotation, version, nil
}

// commitPasswordRotation runs with adminAuthMu already held. sessionMu keeps a
// concurrent session touch from overwriting a revocation with an older record.
func (p *Platform) commitPasswordRotation(rotation passwordRotation) (int, error) {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	revoked, err := p.store.RotatePassword(rotation)
	if err != nil {
		return 0, err
	}
	if rotation.settings != nil {
		p.setAdminHash(rotation.user.PasswordHash)
	}
	return revoked, nil
}

func (r passwordRotation) revokes(session *AdminSession, now time.Time) bool {
	if session.Revoked {
		return false
	}
	if r.settings != nil {
		return now.Before(session.ExpiresAt)
	}
	return session.UserID == r.user.ID
}

func (s *boltStore) RotatePassword(rotation passwordRotation) (int, error) {
	user, err := json.Marshal(&rotation.user)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var revoked []AdminSession
	err = s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(boltBucketName(bucketUsers)).Put([]byte(rotation.user.ID), user); err != nil {
			return err
		}
		if err := putSettingsTx(tx, rotation.settings); err != nil {
			return err
		}
		sessions := tx.Bucket(boltBucketName(bucketSessions))
		if err := sessions.ForEach(func(_, raw []byte) error {
			var session AdminSession
			if err := json.Unmarshal(raw, &session); err != nil {
				return err
			}
			if rotation.revokes(&session, now) {
				session.Revoked = true
				revoked = append(revoked, session)
			}
			return nil
		}); err != nil {
			return err
		}
		updated := revoked
		if rotation.replacement != nil {
			updated = append(updated, *rotation.replacement)
		}
		return putRotatedSessions(sessions, updated)
	})
	if err != nil {
		return 0, err
	}
	return len(revoked), nil
}

func putRotatedSessions(bucket *bolt.Bucket, sessions []AdminSession) error {
	for _, session := range sessions {
		raw, err := json.Marshal(session)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(session.ID), raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *jsonStore) RotatePassword(rotation passwordRotation) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.data
	s.data.Users = maps.Clone(before.Users)
	s.data.Users[rotation.user.ID] = &rotation.user

	if rotation.settings != nil {
		s.data.Settings = maps.Clone(before.Settings)
		for name, value := range rotation.settings {
			s.data.Settings[name] = base64.StdEncoding.EncodeToString(value)
		}
	}

	now := time.Now()
	revoked := 0
	s.data.Sessions = maps.Clone(before.Sessions)
	for id, session := range before.Sessions {
		if session != nil && rotation.revokes(session, now) {
			updated := *session
			updated.Revoked = true
			s.data.Sessions[id] = &updated
			revoked++
		}
	}
	if rotation.replacement != nil {
		replacement := *rotation.replacement
		s.data.Sessions[replacement.ID] = &replacement
	}
	if err := s.saveLocked(); err != nil {
		s.data = before
		return 0, err
	}
	return revoked, nil
}
