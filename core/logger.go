package core

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type loggerContextKey string

const (
	requestIDContextKey loggerContextKey = "request_id"
	tenantContextKey    loggerContextKey = "tenant"
	engineContextKey    loggerContextKey = "engine"
	queryHashContextKey loggerContextKey = "query_hash"

	LogFormatJSON = "json"
	LogFormatText = "text"
)

func NormalizeLogFormat(raw string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return LogFormatText, nil
	}
	switch format {
	case LogFormatJSON, LogFormatText:
		return format, nil
	default:
		return "", fmt.Errorf("invalid logging.format %q: expected json or text", raw)
	}
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return EnsureContext(ctx)
	}
	return context.WithValue(EnsureContext(ctx), requestIDContextKey, requestID)
}

func WithTenant(ctx context.Context, tenant string) context.Context {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return EnsureContext(ctx)
	}
	return context.WithValue(EnsureContext(ctx), tenantContextKey, tenant)
}

func WithEngine(ctx context.Context, engine string) context.Context {
	engine = strings.TrimSpace(engine)
	if engine == "" {
		return EnsureContext(ctx)
	}
	return context.WithValue(EnsureContext(ctx), engineContextKey, engine)
}

func WithQueryHash(ctx context.Context, queryHash string) context.Context {
	queryHash = strings.TrimSpace(queryHash)
	if queryHash == "" {
		return EnsureContext(ctx)
	}
	return context.WithValue(EnsureContext(ctx), queryHashContextKey, queryHash)
}

func RequestIDFromContext(ctx context.Context) string {
	value, _ := EnsureContext(ctx).Value(requestIDContextKey).(string)
	return strings.TrimSpace(value)
}

func TenantFromContext(ctx context.Context) string {
	value, _ := EnsureContext(ctx).Value(tenantContextKey).(string)
	return strings.TrimSpace(value)
}

func WithRequest(ctx context.Context) *logrus.Entry {
	ctx = EnsureContext(ctx)
	fields := logrus.Fields{}

	if requestID, ok := ctx.Value(requestIDContextKey).(string); ok && strings.TrimSpace(requestID) != "" {
		fields["request_id"] = strings.TrimSpace(requestID)
	}
	if tenant, ok := ctx.Value(tenantContextKey).(string); ok && strings.TrimSpace(tenant) != "" {
		fields["tenant"] = strings.TrimSpace(tenant)
	}
	if engine, ok := ctx.Value(engineContextKey).(string); ok && strings.TrimSpace(engine) != "" {
		fields["engine"] = strings.TrimSpace(engine)
	}
	if queryHash, ok := ctx.Value(queryHashContextKey).(string); ok && strings.TrimSpace(queryHash) != "" {
		fields["query_hash"] = strings.TrimSpace(queryHash)
	}
	profileIDs := BrowserProfileIDsFromContext(ctx)
	if len(profileIDs) == 1 {
		fields["browser_profile_id"] = profileIDs[0]
	} else if len(profileIDs) > 1 {
		fields["browser_profile_ids"] = strings.Join(profileIDs, ",")
	}

	return logrus.WithFields(fields)
}

func WithRequestEngine(ctx context.Context, engine string) *logrus.Entry {
	return WithRequest(WithEngine(ctx, engine))
}

func QueryHash(raw string) string {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	if normalized == "" {
		return ""
	}
	hash := md5.Sum([]byte(normalized))
	return hex.EncodeToString(hash[:])
}

func QueryHashFromQuery(q Query) string {
	raw := strings.Join([]string{
		q.Text,
		q.Site,
		q.Filetype,
		q.LangCode,
		q.Region,
		q.DateInterval,
	}, "|")
	return QueryHash(raw)
}

func formatMessage(message string, args ...any) string {
	if len(args) == 0 {
		return message
	}
	return fmt.Sprintf(message, args...)
}

// EngineLogger provides structured logging for search engines with a fixed engine field.
type EngineLogger struct {
	engine string
	entry  *logrus.Entry
}

func NewEngineLogger(engine string) *EngineLogger {
	engine = strings.ToLower(strings.TrimSpace(engine))
	return &EngineLogger{engine: engine, entry: logrus.WithField("engine", engine)}
}

func (el *EngineLogger) WithRequest(ctx context.Context) *EngineLogger {
	return &EngineLogger{engine: el.engine, entry: WithRequestEngine(ctx, el.engine)}
}

// Fields returns a new EngineLogger with additional structured fields merged in.
func (el *EngineLogger) Fields(fields logrus.Fields) *EngineLogger {
	return &EngineLogger{engine: el.engine, entry: el.entry.WithFields(fields)}
}

func (el *EngineLogger) Debug(message string, args ...any) {
	el.entry.Debug(formatMessage(message, args...))
}

func (el *EngineLogger) Info(message string, args ...any) {
	el.entry.Info(formatMessage(message, args...))
}

func (el *EngineLogger) Warn(message string, args ...any) {
	el.entry.Warn(formatMessage(message, args...))
}

func (el *EngineLogger) Error(message string, args ...any) {
	el.entry.Error(formatMessage(message, args...))
}

func (el *EngineLogger) Fatal(message string, args ...any) {
	el.entry.Fatal(formatMessage(message, args...))
}

func (el *EngineLogger) Panic(message string, args ...any) {
	el.entry.Panic(formatMessage(message, args...))
}

// bracketFormatter emits bracket-delimited fields:
//
//	[time][level][engine=..][request_id=..][query_hash=..][extra fields sorted][msg]
//
// request_id is truncated to last 8 chars; query_hash to first 12.
type bracketFormatter struct {
	TimestampFormat string
}

func (f *bracketFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	ts := entry.Time.Format(f.TimestampFormat)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%s][%s]", ts, entry.Level.String())

	// Context identity fields in fixed order, then remaining fields sorted, then msg last.
	priority := []string{"engine", "tenant", "request_id", "query_hash"}
	written := make(map[string]bool, len(entry.Data))

	for _, key := range priority {
		val, ok := entry.Data[key]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", val)
		switch key {
		case "request_id":
			if len(s) > 8 {
				s = s[len(s)-8:]
			}
		case "query_hash":
			if len(s) > 12 {
				s = s[:12]
			}
		}
		fmt.Fprintf(&buf, "[%s=%s]", key, quoteIfNeeded(s))
		written[key] = true
	}

	rest := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		if !written[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		fmt.Fprintf(&buf, "[%s=%s]", k, quoteIfNeeded(fmt.Sprintf("%v", entry.Data[k])))
	}

	// Message last so context fields are scannable without scrolling past a long msg.
	if entry.Message != "" {
		fmt.Fprintf(&buf, "[%s]", quoteIfNeeded(entry.Message))
	}

	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// quoteIfNeeded wraps s in double-quotes if it contains spaces.
func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// logFile is the open ./logs.txt handle, if any, so it can be closed.
var logFile io.WriteCloser

// rotatingFileWriter is a size-triggered rolling log writer (S4-GAP 日志轮转):
// when a write would push the file past maxBytes, the current file is renamed
// to <path>.1 (shifting .1→.2 … .N→deleted) and a fresh file is opened.
// Standard library only — no third-party dependency (单二进制约束). gzip 压缩
// 归档为可选项,当前实现只做 rename 滚动,保持归档可用 gzip -d 直接读取。
type rotatingFileWriter struct {
	mu            sync.Mutex
	path          string
	file          *os.File
	size          int64
	maxBytes      int64
	backups       int
	retentionDays int
	stopCh        chan struct{}
	stopOnce      sync.Once
}

// newRotatingFileWriter opens path for appending with size-based rotation.
// maxBytes <= 0 disables rotation (legacy infinite-append behavior); backups
// bounds how many rolled files are kept (default 5).
func newRotatingFileWriter(path string, maxBytes int64, backups int) (*rotatingFileWriter, error) {
	return newRotatingFileWriterWithRetention(path, maxBytes, backups, 0)
}

// newRotatingFileWriterWithRetention adds a time-based retention window on top
// of size rotation: rotated files older than retentionDays are removed on
// startup, after each rotation and every 12 hours thereafter. retentionDays<=0
// keeps the legacy behavior (only the backup count bounds the archive).
func newRotatingFileWriterWithRetention(path string, maxBytes int64, backups, retentionDays int) (*rotatingFileWriter, error) {
	if backups <= 0 {
		backups = 5
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	size := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	w := &rotatingFileWriter{
		path: path, file: f, size: size, maxBytes: maxBytes,
		backups: backups, retentionDays: retentionDays, stopCh: make(chan struct{}),
	}
	if retentionDays > 0 {
		// A restart may follow a long downtime, so prune before the loop starts.
		w.mu.Lock()
		w.pruneLocked()
		w.mu.Unlock()
		go w.cleanupLoop()
	}
	return w, nil
}

// cleanupLoop prunes expired rotations periodically. It never touches the live
// log file, only numeric .N rotations.
func (w *rotatingFileWriter) cleanupLoop() {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.mu.Lock()
			w.pruneLocked()
			w.mu.Unlock()
		}
	}
}

// pruneLocked removes rotated files whose mtime predates the retention window.
// Caller holds the mutex; failures go to stderr (logging through the writer
// would recurse into the same lock). Returns the number of removed files.
func (w *rotatingFileWriter) pruneLocked() int {
	if w.retentionDays <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
	matches, err := filepath.Glob(w.path + ".*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "log retention glob failed for %s: %v\n", w.path, err)
		return 0
	}
	removed := 0
	base := filepath.Base(w.path)
	for _, name := range matches {
		// Glob normalization can drop a "./" prefix; compare basenames.
		suffix := strings.TrimPrefix(filepath.Base(name), base+".")
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 {
			continue // only positive numeric rotations are managed here
		}
		info, err := os.Stat(name)
		if err != nil || info.IsDir() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(name); err != nil {
			fmt.Fprintf(os.Stderr, "log retention remove failed for %s: %v\n", name, err)
			continue
		}
		removed++
	}
	return removed
}

// PruneExpired removes expired rotated files on demand (admin cleanup API).
func (w *rotatingFileWriter) PruneExpired() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pruneLocked()
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.maxBytes > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			// Rotation failure must not lose the log line: fall through and
			// keep appending to the current file.
			fmt.Fprintf(os.Stderr, "log rotation failed for %s: %v\n", w.path, err)
		} else {
			w.pruneLocked()
		}
	}
	if w.file == nil {
		w.restoreAppendLocked()
		if w.file == nil {
			return 0, fmt.Errorf("log writer unavailable: %s", w.path)
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotateLocked shifts <path>.i → <path>.i+1 (dropping the oldest), then starts
// a fresh file. Caller holds the mutex.
func (w *rotatingFileWriter) rotateLocked() error {
	if w.file == nil {
		w.restoreAppendLocked()
		if w.file == nil {
			return fmt.Errorf("log writer has no open file")
		}
	}
	// Windows cannot rename an open file, so the live fd must be closed before
	// shifting the chain. Any failure restores an append handle instead of
	// leaving w.file nil (which previously silently dropped every later line).
	if err := w.file.Close(); err != nil {
		w.file = nil
		return err
	}
	w.file = nil
	oldest := fmt.Sprintf("%s.%d", w.path, w.backups)
	_ = os.Remove(oldest)
	for i := w.backups - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)); err != nil && !os.IsNotExist(err) {
			w.restoreAppendLocked()
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		w.restoreAppendLocked()
		return err
	}
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		w.restoreAppendLocked()
		return err
	}
	w.file = f
	w.size = 0
	// Refresh the archive mtime: retention is measured from rotation time,
	// not from the last write to the now-archived file.
	now := time.Now()
	_ = os.Chtimes(w.path+".1", now, now)
	return nil
}

// restoreAppendLocked reopens the live path in append mode after a failed
// rotation so subsequent writes keep working. Caller holds the mutex.
func (w *rotatingFileWriter) restoreAppendLocked() {
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	w.file = f
	w.size = 0
	if info, err := f.Stat(); err == nil {
		w.size = info.Size()
	}
}

func (w *rotatingFileWriter) Close() error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// InitLogger configures the global logrus sink. When logMaxMB > 0 the
// ./logs.txt file output is wrapped in a size-triggered rotating writer
// (app.log_max_mb / app.log_backups, S4-GAP 日志轮转); 0 keeps the legacy
// infinite-append file.
func InitLogger(isVerbose, isDebug, isQuiet, saveLog bool, format string, logMaxMB, logBackups int) {
	InitLoggerWithRetention(isVerbose, isDebug, isQuiet, saveLog, format, logMaxMB, logBackups, 0)
}

// InitLoggerWithRetention is InitLogger plus a retention window for rotated
// files (app.log_retention_days). The legacy signature above stays intact for
// existing callers and tests.
func InitLoggerWithRetention(isVerbose, isDebug, isQuiet, saveLog bool, format string, logMaxMB, logBackups, retentionDays int) {
	SetLogConfig(LogConfig{MaxMB: logMaxMB, Backups: logBackups, RetentionDays: retentionDays, Format: format, FileLoggingEnabled: saveLog && !isQuiet && !isDebug})
	switch format {
	case LogFormatText:
		logrus.SetFormatter(&bracketFormatter{TimestampFormat: "2006-01-02 15:04:05"})
	case LogFormatJSON:
		logrus.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339Nano,
		})
	}

	// Logs go to stderr (+ optional file) so stdout carries only the payload.
	switch {
	case isDebug:
		logrus.SetOutput(io.MultiWriter(os.Stderr))
		logrus.SetReportCaller(true)
	case isQuiet || !saveLog:
		// One-shot CLI default: stderr only, no ./logs.txt in the user's CWD.
		logrus.SetOutput(os.Stderr)
		logrus.SetReportCaller(false)
	default:
		// Always use the rotating writer: maxBytes=0 only disables size
		// triggering, time-based retention stays active and is independent.
		maxBytes := int64(0)
		if logMaxMB > 0 {
			maxBytes = int64(logMaxMB) * 1024 * 1024
		}
		w, err := newRotatingFileWriterWithRetention("./logs.txt", maxBytes, logBackups, retentionDays)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open logs file ./logs.txt: %v\n", err)
			logrus.SetOutput(io.MultiWriter(os.Stderr))
		} else {
			logFile = w
			logrus.SetOutput(io.MultiWriter(w, os.Stderr))
		}
		logrus.SetReportCaller(false)
	}

	level := logrus.InfoLevel
	if isVerbose {
		level = logrus.DebugLevel
	}
	if isDebug {
		level = logrus.TraceLevel
	}
	if isQuiet && !isVerbose && !isDebug {
		// Quiet keeps only warnings/errors on stderr.
		level = logrus.WarnLevel
	}
	logrus.SetLevel(level)
}
