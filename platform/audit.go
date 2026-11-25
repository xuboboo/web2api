package platform

// S4-GAP 审计日志: structured JSON Lines audit trail at <data_dir>/audit.log.
//
//   - One JSON object per line: {ts, actor, ip, action, target, result, request_id}.
//   - Size-triggered rolling (same mechanics as the serve log writer, standard
//     library only — 单二进制约束): audit.log → audit.log.1 → audit.log.2 …
//   - 查询: GET /api/audit?limit&offset (JWT) reads the file, newest first.
//   - 脱敏红线沿用: keys are logged by name/prefix only, never key material;
//     passwords never enter the trail.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// AuditEntry is one audit trail line.
type AuditEntry struct {
	TS        time.Time `json:"ts"`
	Actor     string    `json:"actor"`
	IP        string    `json:"ip"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	RequestID string    `json:"request_id,omitempty"`
}

// Audit log rotation defaults (matched to the serve-log defaults; cmd
// overrides via SetAuditRotation with app.log_max_mb / app.log_backups).
const (
	defaultAuditMaxBytes = int64(10 * 1024 * 1024)
	defaultAuditBackups  = 5
)

// AuditLog is the append-only JSON Lines audit writer + query reader.
type AuditLog struct {
	mu               sync.Mutex
	path             string
	f                *os.File
	size             int64
	maxBytes         int64
	backups          int
	retentionDays    int
	retentionStarted bool
	stopCh           chan struct{}
	stopOnce         sync.Once
}

// newAuditLog opens <dataDir>/audit.log for appending with size rolling.
func newAuditLog(dataDir string, maxBytes int64, backups int) *AuditLog {
	if maxBytes <= 0 {
		maxBytes = defaultAuditMaxBytes
	}
	if backups <= 0 {
		backups = defaultAuditBackups
	}
	path := dataDir + string(os.PathSeparator) + "audit.log"
	a := &AuditLog{path: path, maxBytes: maxBytes, backups: backups, stopCh: make(chan struct{})} // retention is installed by SetAuditRetention during serve wiring
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		// Audit is best-effort at startup: a broken path must not take the
		// platform down; Append retries the open.
		return a
	}
	if info, err := f.Stat(); err == nil {
		a.size = info.Size()
	}
	a.f = f
	return a
}

// SetAuditRetention sets the audit archive retention window. It prunes once
// immediately (startup after downtime) and starts the periodic loop on first
// enable. Disabling keeps the writer but stops future cleanup ticks.
func (p *Platform) SetAuditRetention(days int) {
	p.auditMu.Lock()
	defer p.auditMu.Unlock()
	if p.audit == nil {
		return
	}
	p.audit.mu.Lock()
	p.audit.retentionDays = days
	start := days > 0 && !p.audit.retentionStarted
	if start {
		p.audit.retentionStarted = true
	}
	removed := p.audit.pruneLocked()
	p.audit.mu.Unlock()
	if start {
		go p.audit.cleanupLoop()
	}
	if removed > 0 {
		logrus.WithField("removed", removed).Info("platform: 已清理过期审计日志归档")
	}
}

// SetAuditRotation re-configures rotation bounds (called from cmd wiring).
func (p *Platform) SetAuditRotation(maxMB, backups int) {
	p.auditMu.Lock()
	defer p.auditMu.Unlock()
	p.audit.mu.Lock()
	defer p.audit.mu.Unlock()
	if maxMB > 0 {
		p.audit.maxBytes = int64(maxMB) * 1024 * 1024
	}
	if backups > 0 {
		p.audit.backups = backups
	}
}

// Append writes one audit entry as a JSON line (rotation on size). Writes are
// serialized; a failed append logs to the server log but never fails the
// request that triggered it.
func (a *AuditLog) Append(e AuditEntry) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	line = append(line, '\n')

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		f, ferr := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if ferr != nil {
			logAuditFailure(ferr)
			return
		}
		a.f = f
	}
	if a.size > 0 && a.size+int64(len(line)) > a.maxBytes {
		if err := a.rotateLocked(); err != nil {
			logAuditFailure(err)
		} else {
			a.pruneLocked()
		}
	}
	if a.f == nil {
		f, ferr := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if ferr != nil {
			logAuditFailure(ferr)
			return
		}
		a.f = f
	}
	n, err := a.f.Write(line)
	a.size += int64(n)
	if err != nil {
		logAuditFailure(err)
	}
}

// rotateLocked shifts audit.log.i → audit.log.i+1 (dropping the oldest) and
// reopens a fresh file. Caller holds the mutex. On any failure the current
// handle is kept and the file is NOT truncated, so the audit trail survives.
func (a *AuditLog) rotateLocked() error {
	if a.f == nil {
		a.restoreAppendLocked()
	}
	if a.f != nil {
		if err := a.f.Close(); err != nil {
			a.f = nil
			return err
		}
	}
	a.f = nil
	oldest := fmt.Sprintf("%s.%d", a.path, a.backups)
	_ = os.Remove(oldest)
	for i := a.backups - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", a.path, i), fmt.Sprintf("%s.%d", a.path, i+1)); err != nil && !os.IsNotExist(err) {
			a.restoreAppendLocked()
			return err
		}
	}
	if err := os.Rename(a.path, a.path+".1"); err != nil && !os.IsNotExist(err) {
		a.restoreAppendLocked()
		return err
	}
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		a.restoreAppendLocked()
		return err
	}
	a.f = f
	a.size = 0
	now := time.Now()
	_ = os.Chtimes(a.path+".1", now, now)
	return nil
}

// restoreAppendLocked reopens the audit file after a failed rotation.
func (a *AuditLog) restoreAppendLocked() {
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	a.f = f
	a.size = 0
	if info, err := f.Stat(); err == nil {
		a.size = info.Size()
	}
}

// cleanupLoop prunes expired audit rotations every 12 hours.
func (a *AuditLog) cleanupLoop() {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.mu.Lock()
			a.pruneLocked()
			a.mu.Unlock()
		}
	}
}

// pruneLocked deletes audit.log.N files older than the retention window.
// Caller holds the mutex. Returns the number of removed files.
func (a *AuditLog) pruneLocked() int {
	if a.retentionDays <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -a.retentionDays)
	matches, err := filepath.Glob(a.path + ".*")
	if err != nil {
		logAuditFailure(err)
		return 0
	}
	removed := 0
	base := filepath.Base(a.path)
	for _, name := range matches {
		suffix := strings.TrimPrefix(filepath.Base(name), base+".")
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 {
			continue
		}
		info, err := os.Stat(name)
		if err != nil || info.IsDir() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(name); err != nil {
			logAuditFailure(err)
			continue
		}
		removed++
	}
	return removed
}

// PruneExpired removes expired audit rotations on demand (admin cleanup API).
func (a *AuditLog) PruneExpired() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pruneLocked()
}

// RetentionDays reports the effective retention window (0 = disabled).
func (a *AuditLog) RetentionDays() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.retentionDays
}

// Close releases the audit file handle.
func (a *AuditLog) Close() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

// Query returns up to `limit` entries newest first, with the unfiltered total.
// Corrupt lines are skipped; the audit trail is append-only JSON Lines.
func (a *AuditLog) Query(limit, offset int) ([]AuditEntry, int, error) {
	a.mu.Lock()
	path := a.path
	a.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []AuditEntry{}, 0, nil
		}
		return nil, 0, err
	}
	var entries []AuditEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip corrupt line, keep the trail readable
		}
		entries = append(entries, e)
	}
	scanErr := scanner.Err()
	closeErr := f.Close()
	if err := errors.Join(scanErr, closeErr); err != nil {
		return nil, 0, err
	}
	total := len(entries)
	// Newest first: the file is append-ordered (oldest → newest).
	start := total - 1 - offset
	if start < 0 {
		return []AuditEntry{}, total, nil
	}
	out := make([]AuditEntry, 0, limit)
	for i := start; i >= 0 && len(out) < limit; i-- {
		out = append(out, entries[i])
	}
	return out, total, nil
}

func logAuditFailure(err error) {
	logrus.WithError(err).Warn("platform: 写入审计日志失败")
}
