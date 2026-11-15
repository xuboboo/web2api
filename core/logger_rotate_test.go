package core

// S4-GAP 日志轮转: size-triggered rolling writer (standard library only).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFileWriterRollsOnSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.txt")
	w, err := newRotatingFileWriter(path, 100, 3)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}

	line := strings.Repeat("x", 39) + "\n" // 40 bytes
	for i := 0; i < 6; i++ {                 // 6 × 40 = 240B → rolls twice+
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	// .1 must exist (the newest roll); total kept rolls ≤ backups.
	rolled := 0
	for i := 1; i <= 5; i++ {
		if _, err := os.Stat(path + "." + string(rune('0'+i))); err == nil {
			rolled++
		}
	}
	if rolled < 2 || rolled > 3 {
		t.Fatalf("expected 2..3 rolled files, got %d", rolled)
	}
	// The current file is fresh (smaller than one line + one write).
	data, _ := os.ReadFile(path)
	if len(data) > 80 {
		t.Fatalf("current file not rotated: %d bytes", len(data))
	}
}

func TestRotatingFileWriterZeroDisablesRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.txt")
	w, err := newRotatingFileWriter(path, 0, 3)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for i := 0; i < 50; i++ {
		_, _ = w.Write([]byte("0123456789\n"))
	}
	_ = w.Close()
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("rotation must be disabled with maxBytes=0")
	}
}
