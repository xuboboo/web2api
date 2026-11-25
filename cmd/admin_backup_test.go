package cmd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	bolt "go.etcd.io/bbolt"
)

func TestAdminBackupRejectsOutputThatAliasesSourceDatabase(t *testing.T) {
	tests := []struct {
		name       string
		outputPath func(t *testing.T, dbPath string) string
	}{
		{
			name: "same path",
			outputPath: func(_ *testing.T, dbPath string) string {
				return dbPath
			},
		},
		{
			name: "hard link",
			outputPath: func(t *testing.T, dbPath string) string {
				linkPath := filepath.Join(filepath.Dir(dbPath), "database-link.db")
				if err := os.Link(dbPath, linkPath); err != nil {
					t.Skipf("hard links are unavailable: %v", err)
				}
				return linkPath
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "web2api.db")
			createBackupTestDB(t, dbPath)
			before, err := os.Stat(dbPath)
			if err != nil {
				t.Fatal(err)
			}

			adminDataDir = dir
			adminBackupOut = tt.outputPath(t, dbPath)
			t.Cleanup(func() {
				adminDataDir = ""
				adminBackupOut = ""
			})

			err = runAdminBackup(&cobra.Command{}, nil)
			if err == nil {
				t.Fatal("backup must reject an output path that aliases the source database")
			}

			after, err := os.Stat(dbPath)
			if err != nil {
				t.Fatalf("source database disappeared: %v", err)
			}
			if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
				t.Fatalf("source database changed after rejected backup: before=%v after=%v", before, after)
			}
			verifyBackupTestDB(t, dbPath)
		})
	}
}

func TestAdminFullBackupAndRestoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	sourceData := filepath.Join(sourceRoot, "data")
	sourceConfig := filepath.Join(sourceRoot, "config.yaml")
	bundlePath := filepath.Join(root, "full-backup.tar.gz")
	targetRoot := filepath.Join(root, "target")
	targetData := filepath.Join(targetRoot, "data")
	targetConfig := filepath.Join(targetRoot, "config.yaml")

	for _, dir := range []string{
		filepath.Join(sourceData, "index_jobs", "job-1"),
		targetData,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	createRequiredBackupDB(t, filepath.Join(sourceData, "web2api.db"), "source-db")
	createRequiredBackupDB(t, filepath.Join(targetData, "web2api.db"), "target-db")
	writeFileForBackupTest(t, filepath.Join(sourceData, "secret.key"), "source-secret-key-0123456789")
	writeFileForBackupTest(t, filepath.Join(sourceData, "index_jobs", "job-1", "job.json"), `{"job_id":"job-1","status":"done"}`)
	writeFileForBackupTest(t, filepath.Join(sourceData, "index_jobs", "job-1", "results.jsonl"), `{"url":"https://example.com"}`+"\n")
	writeFileForBackupTest(t, filepath.Join(sourceData, "audit.log"), "login-ok\n")
	writeFileForBackupTest(t, filepath.Join(sourceData, "audit.log.1"), "audit-old\n")
	writeFileForBackupTest(t, sourceConfig, "server:\n  port: 7000\nplatform:\n  data_dir: ./data\n")
	writeFileForBackupTest(t, filepath.Join(targetData, "secret.key"), "target-secret-key-0123456789")
	writeFileForBackupTest(t, filepath.Join(targetData, "unrelated.txt"), "target-only\n")
	writeFileForBackupTest(t, targetConfig, "server:\n  port: 7999\n")

	oldIndexJobs := config.IndexJobs
	oldResolvedConfig := resolvedConfigFilePath
	t.Cleanup(func() {
		config.IndexJobs = oldIndexJobs
		resolvedConfigFilePath = oldResolvedConfig
		adminBackupFull = false
		adminBackupOut = ""
		adminDataDir = ""
		adminRestoreIn = ""
		adminRestoreConfigOut = ""
		adminRestoreKeepCurrentDB = true
	})
	config.IndexJobs.Enabled = true
	config.IndexJobs.Dir = ""
	resolvedConfigFilePath = sourceConfig
	adminBackupFull = true
	adminBackupOut = bundlePath
	adminDataDir = sourceData

	if err := runAdminBackup(&cobra.Command{}, nil); err != nil {
		t.Fatalf("full backup: %v", err)
	}
	manifest := readBundleManifestForTest(t, bundlePath)
	for _, required := range []string{
		"data/",
		"data/web2api.db",
		"data/secret.key",
		"data/index_jobs/job-1/job.json",
		"data/audit.log",
		adminBundleConfigName,
	} {
		if !manifestHasEntry(manifest, required) {
			t.Errorf("full backup missing %q", required)
		}
	}

	adminBackupFull = false
	adminBackupOut = ""
	adminDataDir = targetData
	adminRestoreIn = bundlePath
	adminRestoreConfigOut = targetConfig
	adminRestoreKeepCurrentDB = true
	if err := runAdminRestore(&cobra.Command{}, nil); err != nil {
		t.Fatalf("full restore: %v", err)
	}

	verifyRequiredBackupDB(t, filepath.Join(targetData, "web2api.db"), "source-db")
	assertFileContentForBackupTest(t, filepath.Join(targetData, "secret.key"), "source-secret-key-0123456789")
	assertFileContentForBackupTest(t, filepath.Join(targetData, "index_jobs", "job-1", "job.json"), `{"job_id":"job-1","status":"done"}`)
	assertFileContentForBackupTest(t, filepath.Join(targetData, "audit.log"), "login-ok\n")
	assertFileContentForBackupTest(t, targetConfig, "server:\n  port: 7000\nplatform:\n  data_dir: ./data\n")
	if _, err := os.Stat(filepath.Join(targetData, "unrelated.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target-only file must not survive full restore: %v", err)
	}
	preDataDirs, err := filepath.Glob(targetData + ".pre-restore-*")
	if err != nil || len(preDataDirs) != 1 {
		t.Fatalf("expected one current-data recovery copy, matches=%v err=%v", preDataDirs, err)
	}
	preConfigs, err := filepath.Glob(targetConfig + ".pre-restore-*")
	if err != nil || len(preConfigs) != 1 {
		t.Fatalf("expected one current-config recovery copy, matches=%v err=%v", preConfigs, err)
	}
	assertFileContentForBackupTest(t, filepath.Join(preDataDirs[0], "unrelated.txt"), "target-only\n")
	assertFileContentForBackupTest(t, preConfigs[0], "server:\n  port: 7999\n")
}

func TestAdminFullRestoreRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "malicious.tar.gz")
	outside := filepath.Join(root, "escaped.txt")
	writeTarGzipForBackupTest(t, archivePath, "../escaped.txt", []byte("bad"))

	adminDataDir = dataDir
	adminRestoreIn = archivePath
	adminRestoreConfigOut = filepath.Join(root, "config.yaml")
	t.Cleanup(func() {
		adminDataDir = ""
		adminRestoreIn = ""
		adminRestoreConfigOut = ""
	})
	if err := runAdminRestore(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("path traversal archive must be rejected, got %v", err)
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path traversal wrote outside staging directory: %v", err)
	}
}

func TestAdminFullRestoreRejectsManifestChecksumMismatch(t *testing.T) {
	root := t.TempDir()
	sourceData := filepath.Join(root, "source", "data")
	sourceConfig := filepath.Join(root, "source", "config.yaml")
	bundlePath := filepath.Join(root, "full-backup.tar.gz")
	if err := os.MkdirAll(filepath.Join(sourceData, "index_jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	createRequiredBackupDB(t, filepath.Join(sourceData, "web2api.db"), "source-db")
	writeFileForBackupTest(t, filepath.Join(sourceData, "secret.key"), "source-secret-key-0123456789")
	writeFileForBackupTest(t, filepath.Join(sourceData, "audit.log"), "audit\n")
	writeFileForBackupTest(t, sourceConfig, "server:\n  port: 7000\n")

	oldIndexJobs := config.IndexJobs
	oldResolvedConfig := resolvedConfigFilePath
	t.Cleanup(func() {
		config.IndexJobs = oldIndexJobs
		resolvedConfigFilePath = oldResolvedConfig
	})
	config.IndexJobs.Enabled = true
	config.IndexJobs.Dir = ""
	resolvedConfigFilePath = sourceConfig
	sources, _, err := collectAdminBundleSources(sourceData)
	if err != nil {
		t.Fatal(err)
	}
	manifest := adminBundleManifest{
		Format:     adminBundleFormat,
		Version:    adminBundleFormatVersion,
		CreatedAt:  time.Now().UTC(),
		AppVersion: version,
	}
	for _, source := range sources {
		entry := source.Entry
		if entry.Path == "data/audit.log" {
			entry.SHA256 = strings.Repeat("0", 64)
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	if err := writeAdminBundle(bundlePath, sources, manifest); err != nil {
		t.Fatal(err)
	}

	stage, err := extractAdminBundle(bundlePath, filepath.Join(root, "target", "data"))
	if err == nil || !strings.Contains(err.Error(), "校验和不一致") {
		if stage != nil {
			_ = os.RemoveAll(stage.Root)
		}
		t.Fatalf("tampered manifest must fail verification, got %v", err)
	}
}

func TestApplyStagedAdminBundleInPlacePreservesMountedVolumeContents(t *testing.T) {
	root := t.TempDir()
	targetData := filepath.Join(root, "mounted-data")
	stagedData := filepath.Join(root, "staged", "data")
	stagedConfig := filepath.Join(root, "staged", "config", "config.yaml")
	preserveRoot := filepath.Join(root, "preserve")
	targetConfig := filepath.Join(root, "restore-config", "config.yaml")

	for _, dir := range []string{targetData, filepath.Join(stagedData, "index_jobs"), filepath.Dir(targetConfig)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	createRequiredBackupDB(t, filepath.Join(targetData, "web2api.db"), "old-db")
	createRequiredBackupDB(t, filepath.Join(stagedData, "web2api.db"), "restored-db")
	writeFileForBackupTest(t, filepath.Join(targetData, "old-only.txt"), "old-volume\n")
	writeFileForBackupTest(t, filepath.Join(stagedData, "secret.key"), "restored-secret\n")
	writeFileForBackupTest(t, filepath.Join(stagedData, "index_jobs", "job.json"), `{"job_id":"restored"}`)
	writeFileForBackupTest(t, targetConfig, "server:\n  port: 7999\n")
	writeFileForBackupTest(t, stagedConfig, "server:\n  port: 7000\n")

	staged := &stagedAdminBundle{
		Root:       filepath.Dir(stagedData),
		DataDir:    stagedData,
		ConfigFile: stagedConfig,
	}
	preDataDir, preConfig, err := applyStagedAdminBundleInPlace(
		staged,
		targetData,
		targetConfig,
		preserveRoot,
		true,
	)
	if err != nil {
		t.Fatalf("in-place restore: %v", err)
	}
	if !pathWithin(preserveRoot, preDataDir) {
		t.Fatalf("pre-restore data must be stored below preserve root: %s", preDataDir)
	}

	verifyRequiredBackupDB(t, filepath.Join(targetData, "web2api.db"), "restored-db")
	assertFileContentForBackupTest(t, filepath.Join(targetData, "secret.key"), "restored-secret\n")
	assertFileContentForBackupTest(t, filepath.Join(targetData, "index_jobs", "job.json"), `{"job_id":"restored"}`)
	assertFileContentForBackupTest(t, targetConfig, "server:\n  port: 7000\n")
	verifyRequiredBackupDB(t, filepath.Join(preDataDir, "web2api.db"), "old-db")
	assertFileContentForBackupTest(t, filepath.Join(preDataDir, "old-only.txt"), "old-volume\n")
	assertFileContentForBackupTest(t, preConfig, "server:\n  port: 7999\n")
	if _, err := os.Stat(filepath.Join(targetData, "old-only.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old-volume file must not survive an in-place restore: %v", err)
	}
}

func createRequiredBackupDB(t *testing.T, dbPath, marker string) {
	t.Helper()
	db, err := bolt.Open(dbPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"keys", "usage", "settings"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return tx.Bucket([]byte("settings")).Put([]byte("test-marker"), []byte(marker))
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func verifyRequiredBackupDB(t *testing.T, dbPath, marker string) {
	t.Helper()
	db, err := bolt.Open(dbPath, 0o400, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	if err := db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("keys")) == nil || tx.Bucket([]byte("usage")) == nil {
			return errors.New("required contract bucket missing")
		}
		got := string(tx.Bucket([]byte("settings")).Get([]byte("test-marker")))
		if got != marker {
			return errors.New("database marker mismatch: got " + got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readBundleManifestForTest(t *testing.T, archivePath string) adminBundleManifest {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, file)
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, gzipReader)
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			t.Fatal("manifest not found")
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name != adminBundleManifestName {
			continue
		}
		var manifest adminBundleManifest
		if err := json.NewDecoder(tarReader).Decode(&manifest); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
}

func manifestHasEntry(manifest adminBundleManifest, name string) bool {
	for _, entry := range manifest.Entries {
		if entry.Path == name {
			return true
		}
	}
	return false
}

func writeFileForBackupTest(t *testing.T, filePath, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFileContentForBackupTest(t *testing.T, filePath, want string) {
	t.Helper()
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s content mismatch: got %q want %q", filePath, got, want)
	}
}

func writeTarGzipForBackupTest(t *testing.T, archivePath, name string, content []byte) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o600,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tarWriter.Close(), gzipWriter.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
}

func createBackupTestDB(t *testing.T, path string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("backup-test"))
		if err != nil {
			return err
		}
		return bucket.Put([]byte("key"), []byte("value"))
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func verifyBackupTestDB(t *testing.T, path string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("source database is no longer valid: %v", err)
	}
	defer closeTestResource(t, db)
	if err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("backup-test"))
		if bucket == nil || string(bucket.Get([]byte("key"))) != "value" {
			t.Fatal("source database contents changed")
		}
		return nil
	}); err != nil {
		t.Fatalf("read source database: %v", err)
	}
}
