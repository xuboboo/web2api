package core

// Log query / maintenance helpers shared by the admin log endpoints. The
// parsers understand both log formats this service can emit (bracket text and
// logrus JSON Lines) and never execute or interpret log content as commands.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const logFilePath = "./logs.txt"

// maxLogLines caps one admin log query so a pathologically large archive can
// never exhaust the process. This is a read-only diagnostic surface.
const maxLogLines = 200000

// LogEntry is one normalized log line from logs.txt or one of its rotations.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Source  string    `json:"source"`
}

// LogQuery filters log lines. Zero values mean "no filter"; Limit is capped by
// the caller (admin handler caps at 200).
type LogQuery struct {
	Level  string
	Text   string
	Since  time.Time
	Limit  int
	Offset int
}

// QueryLogs reads the live log and its numeric rotations, newest first, and
// returns the filtered page plus the filtered total (the scan is bounded by
// maxLogLines, so the total covers the scanned window). Rotated files are
// ordered .1 (newest archive) → .N (oldest).
func QueryLogs(q LogQuery, format string) ([]LogEntry, int, error) {
	files, err := logFilesNewestFirst()
	if err != nil {
		return nil, 0, err
	}
	level := strings.ToLower(strings.TrimSpace(q.Level))
	needle := strings.TrimSpace(q.Text)
	scanned := 0
	matched := make([]LogEntry, 0, 256)
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, err
		}
		source := filepath.Base(file)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		lines := make([]string, 0, 128)
		for scanner.Scan() {
			scanned++
			if scanned > maxLogLines {
				break
			}
			lines = append(lines, scanner.Text())
		}
		scanErr := scanner.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return nil, 0, scanErr
		}
		if closeErr != nil {
			return nil, 0, closeErr
		}
		// Each file is append-ordered (oldest → newest); walk it backwards.
		for i := len(lines) - 1; i >= 0; i-- {
			entry, ok := parseLogLine(lines[i], source, format)
			if !ok {
				continue
			}
			if level != "" && strings.ToLower(entry.Level) != level {
				continue
			}
			if !q.Since.IsZero() && entry.Time.Before(q.Since) {
				continue
			}
			if needle != "" && !strings.Contains(entry.Message, needle) && !strings.Contains(source, needle) {
				continue
			}
			matched = append(matched, entry)
		}
		if scanned > maxLogLines {
			break
		}
	}
	total := len(matched)
	start := q.Offset
	if start < 0 || start >= total {
		return []LogEntry{}, total, nil
	}
	end := start + q.Limit
	if q.Limit <= 0 || end > total {
		end = total
	}
	return matched[start:end], total, nil
}

// logFilesNewestFirst lists logs.txt followed by .1, .2 … .N (oldest last).
func logFilesNewestFirst() ([]string, error) {
	files := []string{logFilePath}
	matches, err := filepath.Glob(logFilePath + ".*")
	if err != nil {
		return nil, err
	}
	type rotation struct {
		name string
		n    int
	}
	var rotations []rotation
	for _, name := range matches {
		n, err := strconv.Atoi(strings.TrimPrefix(name, logFilePath+"."))
		if err != nil || n < 1 {
			continue
		}
		rotations = append(rotations, rotation{name: name, n: n})
	}
	sort.Slice(rotations, func(i, j int) bool { return rotations[i].n < rotations[j].n })
	for _, r := range rotations {
		files = append(files, r.name)
	}
	return files, nil
}

// parseLogLine handles both the bracket formatter and logrus JSON Lines. Time
// is best-effort: unparseable timestamps stay zero and only affect ?since=
// filtering, never line visibility.
func parseLogLine(line, source, format string) (LogEntry, bool) {
	if entry, ok := parseLineAs(line, source, format); ok {
		return entry, true
	}
	// A config change can leave older archives in the other format; fall back
	// so history stays queryable.
	other := LogFormatText
	if format == LogFormatText {
		other = LogFormatJSON
	}
	return parseLineAs(line, source, other)
}

func parseLineAs(line, source, format string) (LogEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return LogEntry{}, false
	}
	if format == LogFormatJSON {
		var obj struct {
			Time    string `json:"time"`
			Level   string `json:"level"`
			Message string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return LogEntry{}, false
		}
		ts, _ := time.Parse(time.RFC3339Nano, obj.Time)
		return LogEntry{Time: ts, Level: obj.Level, Message: obj.Message, Source: source}, true
	}
	// Bracket format: [time][level][field=..]…[message]
	if !strings.HasPrefix(line, "[") {
		return LogEntry{}, false
	}
	close1 := strings.Index(line, "]")
	if close1 < 0 {
		return LogEntry{}, false
	}
	ts, _ := time.ParseInLocation("2006-01-02 15:04:05", line[1:close1], time.Local)
	rest := strings.TrimSpace(line[close1+1:])
	level := ""
	if strings.HasPrefix(rest, "[") {
		if close2 := strings.Index(rest, "]"); close2 > 0 {
			level = rest[1:close2]
			rest = strings.TrimSpace(rest[close2+1:])
		}
	}
	// Every remaining field is a [key=value] group; the last one is the message.
	message := ""
	for strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end < 0 {
			break
		}
		message = rest[1:end]
		rest = strings.TrimSpace(rest[end+1:])
	}
	if rest != "" {
		if message != "" {
			message += " "
		}
		message += rest
	}
	return LogEntry{Time: ts, Level: level, Message: message, Source: source}, true
}

// LogConfig is the effective rotation/retention configuration of the live
// logger, exposed read-only to the admin console.
type LogConfig struct {
	MaxMB              int    `json:"max_mb"`
	Backups            int    `json:"backups"`
	RetentionDays      int    `json:"retention_days"`
	Format             string `json:"format"`
	FileLoggingEnabled bool   `json:"file_logging_enabled"`
}

var (
	logConfigMu    sync.Mutex
	logConfigState = LogConfig{Format: LogFormatText}
)

// SetLogConfig records the effective logger configuration (called from
// InitLoggerWithRetention).
func SetLogConfig(cfg LogConfig) {
	logConfigMu.Lock()
	logConfigState = cfg
	logConfigMu.Unlock()
}

// CurrentLogConfig returns a copy of the effective logger configuration.
func CurrentLogConfig() LogConfig {
	logConfigMu.Lock()
	defer logConfigMu.Unlock()
	return logConfigState
}

// PruneExpiredLogs removes expired rotations from the live rotating writer
// (0 when rotation/retention is disabled or the writer is not rotating).
func PruneExpiredLogs() int {
	w, ok := logFile.(*rotatingFileWriter)
	if !ok || w == nil {
		return 0
	}
	return w.PruneExpired()
}

// RotateLogFile forces a rotation of the live log file for callers that need
// an immediate rollover. The cleanup endpoint only prunes by retention.
func RotateLogFile() error {
	w, ok := logFile.(*rotatingFileWriter)
	if !ok || w == nil {
		return fmt.Errorf("日志文件轮转未启用")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}
