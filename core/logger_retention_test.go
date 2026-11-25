package core

// Coverage for size rotation + retention cleanup and both log-line parsers.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotatingWriterRetentionPrunesExpiredArchives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	w, err := newRotatingFileWriterWithRetention(path, 16, 3, 1)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	defer func() { _ = w.Close() }()

	for i := 0; i < 3; i++ {
		if _, err := w.Write([]byte("[2026-11-25 18:00:00][info][some line that forces rotation]\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	rotated := path + ".1"
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
	old := time.Now().AddDate(0, 0, -10)
	if err := os.Chtimes(rotated, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if removed := w.PruneExpired(); removed < 1 {
		t.Fatalf("PruneExpired=%d, want at least 1", removed)
	}
	if _, err := os.Stat(rotated); !os.IsNotExist(err) {
		t.Fatalf("expired rotation still present: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live log removed: %v", err)
	}
}

func TestParseLogLineFormats(t *testing.T) {
	text := `[2026-11-25 18:00:00][warning][engine=baidu][request blocked]`
	entry, ok := parseLogLine(text, "logs.txt.1", LogFormatText)
	if !ok || entry.Level != "warning" || entry.Message != "request blocked" || entry.Source != "logs.txt.1" {
		t.Fatalf("text parse: %+v ok=%v", entry, ok)
	}
	if entry.Time.IsZero() {
		t.Fatalf("text timestamp not parsed: %+v", entry)
	}
	jsonLine := `{"time":"2026-11-25T18:00:00Z","level":"error","msg":"disk full"}`
	entry, ok = parseLogLine(jsonLine, "logs.txt", LogFormatJSON)
	if !ok || entry.Level != "error" || entry.Message != "disk full" {
		t.Fatalf("json parse: %+v ok=%v", entry, ok)
	}
}
