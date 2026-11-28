package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestPasswordRotationCommitsOrRollsBack(t *testing.T) {
	for _, backend := range []string{"bbolt", "json"} {
		for _, bootstrap := range []bool{false, true} {
			name := backend + "/account"
			if bootstrap {
				name = backend + "/bootstrap"
			}
			t.Run(name, func(t *testing.T) {
				store, prepareFailure := openPasswordRotationStore(t, backend)
				user := User{ID: "user-1", Username: "operator", PasswordHash: []byte("old-hash")}
				if err := store.PutUser(&user); err != nil {
					t.Fatal(err)
				}
				if err := store.PutSettings(map[string][]byte{
					settingAdminHash:    []byte("old-admin-hash"),
					settingTokenVersion: []byte("3"),
				}); err != nil {
					t.Fatal(err)
				}
				for id, userID := range map[string]string{"old": user.ID, "other": "user-2"} {
					if err := store.PutSession(&AdminSession{ID: id, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				rotation, _, err := planPasswordRotation(&user, []byte("new-hash"), 3, bootstrap)
				if err != nil {
					t.Fatal(err)
				}
				rotation.replacement = &AdminSession{ID: "new", UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}

				clearFailure := prepareFailure(t)
				if revoked, err := store.RotatePassword(rotation); err == nil || revoked != 0 {
					t.Fatalf("failed rotation = (%d, %v), want error and zero committed revocations", revoked, err)
				}
				clearFailure(t)
				assertPasswordRotationState(t, store, "old-hash", "old-admin-hash", "3", false, false, false)

				wantRevocations := 1
				globalHash, version := "old-admin-hash", "3"
				if bootstrap {
					wantRevocations = 2
					globalHash, version = "new-hash", "4"
				}
				if revoked, err := store.RotatePassword(rotation); err != nil || revoked != wantRevocations {
					t.Fatalf("successful rotation = (%d, %v), want (%d, nil)", revoked, err, wantRevocations)
				}
				assertPasswordRotationState(t, store, "new-hash", globalHash, version, true, bootstrap, true)
			})
		}
	}
}

func openPasswordRotationStore(t *testing.T, backend string) (Store, func(*testing.T) func(*testing.T)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rotation."+backend)
	if backend == "bbolt" {
		store, err := newBoltStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store, func(t *testing.T) func(*testing.T) {
			t.Helper()
			// Force a session decode error after the transaction has written
			// the new user and bootstrap settings.
			if err := store.db.Update(func(tx *bolt.Tx) error {
				return tx.Bucket(boltBucketName(bucketSessions)).Put([]byte("corrupt"), []byte("{"))
			}); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T) {
				t.Helper()
				if _, err := store.DeleteSession("corrupt"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	store, err := newJSONStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, func(t *testing.T) func(*testing.T) {
		t.Helper()
		tmp := path + ".tmp"
		if err := os.Mkdir(tmp, 0o700); err != nil {
			t.Fatal(err)
		}
		return func(t *testing.T) {
			t.Helper()
			if err := os.Remove(tmp); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func assertPasswordRotationState(t *testing.T, store Store, hash, adminHash, version string, oldRevoked, otherRevoked, replacement bool) {
	t.Helper()
	user, err := store.GetUserByID("user-1")
	if err != nil || user == nil || !bytes.Equal(user.PasswordHash, []byte(hash)) {
		t.Fatalf("user credential did not match committed state: %v", err)
	}
	for key, want := range map[string]string{
		settingAdminHash:    adminHash,
		settingTokenVersion: version,
	} {
		got, err := store.GetSetting(key)
		if err != nil || string(got) != want {
			t.Fatalf("setting %s = %q, %v; want %q", key, got, err, want)
		}
	}
	for id, wantRevoked := range map[string]bool{"old": oldRevoked, "other": otherRevoked} {
		session, err := store.GetSession(id)
		if err != nil || session == nil || session.Revoked != wantRevoked {
			t.Fatalf("session %s = %+v, %v; want revoked=%t", id, session, err, wantRevoked)
		}
	}
	session, err := store.GetSession("new")
	if err != nil || (session != nil) != replacement || (session != nil && session.Revoked) {
		t.Fatalf("replacement session = %+v, %v; want active=%t", session, err, replacement)
	}
}
