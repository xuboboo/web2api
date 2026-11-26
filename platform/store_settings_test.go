package platform

import (
	"path/filepath"
	"testing"
)

func TestStoreListsAndDeletesSettingsByPrefix(t *testing.T) {
	bolt, err := newBoltStore(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	jsonDB, err := newJSONStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	stores := []struct {
		name  string
		store Store
	}{{"bbolt", bolt}, {"json", jsonDB}}
	for _, tc := range stores {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { _ = tc.store.Close() })
			if err := tc.store.PutSettings(map[string][]byte{
				"scheduled_job:one": []byte("one"),
				"scheduled_job:two": []byte("two"),
				"unrelated":         []byte("keep"),
			}); err != nil {
				t.Fatal(err)
			}
			items, err := tc.store.ListSettings("scheduled_job:")
			if err != nil || len(items) != 2 || string(items["scheduled_job:one"]) != "one" {
				t.Fatalf("ListSettings: items=%v err=%v", items, err)
			}
			if err := tc.store.DeleteSetting("scheduled_job:one"); err != nil {
				t.Fatal(err)
			}
			items, err = tc.store.ListSettings("scheduled_job:")
			if err != nil || len(items) != 1 {
				t.Fatalf("after DeleteSetting: items=%v err=%v", items, err)
			}
		})
	}
}
