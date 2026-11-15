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
	"fmt"
	"os"
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
	mu       sync.Mutex
	path     string
	f        *os.File
	size     int64
	maxBytes int64
	backups  int
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
	a := &AuditLog{path: path, maxBytes: maxBytes, backups: backups}
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

// SetAuditRotation re-configures rotation bounds (called from cmd wiring).
func (p *Platform) SetAuditRotation(maxMB, backups int) {
	p.auditMu.Lock()
	defer p.auditMu.Unlock()
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
		a.rotateLocked()
	}
	n, err := a.f.Write(line)
	a.size += int64(n)
	if err != nil {
		logAuditFailure(err)
	}
}

// rotateLocked shifts audit.log.i → audit.log.i+1 (dropping the oldest) and
// reopens a fresh file. Caller holds the mutex; rotation failure keeps the
// current file (log line is still written).
func (a *AuditLog) rotateLocked() {
	if a.f != nil {
		_ = a.f.Close()
		a.f = nil
	}
	oldest := fmt.Sprintf("%s.%d", a.path, a.backups)
	_ = os.Remove(oldest)
	for i := a.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", a.path, i), fmt.Sprintf("%s.%d", a.path, i+1))
	}
	_ = os.Rename(a.path, a.path+".1")
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		logAuditFailure(err)
		return
	}
	a.f = f
	a.size = 0
}

// Close releases the audit file handle.
func (a *AuditLog) Close() error {
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
	defer f.Close()

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
	if err := scanner.Err(); err != nil {
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
