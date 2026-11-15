package platform

// Usage-journal retention (滚动清理上限) and the shared RecordUsage wrapper.

import (
	"time"

	"github.com/sirupsen/logrus"
)

// usagePruneInterval spreads the prune scan over the insert stream: prune
// once per N recorded calls (startup prunes too). Keeps the constant-size
// scan amortized and invisible on the request path.
const usagePruneInterval = int64(256)

// recordUsage is the single choke point every billed write goes through:
// it appends the record (and key counters) atomically, then triggers the
// rolling prune every usagePruneInterval inserts when a retention window is
// configured. rec.Seq is set by the store implementation.
func (p *Platform) recordUsage(rec *UsageRecord, keyID string, units int64, consume bool, at time.Time) error {
	if err := p.store.RecordUsage(rec, keyID, units, consume, at); err != nil {
		return err
	}
	if rec.Seq > 0 && rec.Seq%usagePruneInterval == 0 && p.usageRetentionDays() > 0 {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logrus.WithField("panic", r).Warn("platform: usage prune panicked")
				}
			}()
			p.pruneUsage()
		}()
	}
	return nil
}

// usageRetentionDays returns the configured retention window in days
// (0 = keep everything).
func (p *Platform) usageRetentionDays() int {
	if p.cfg.UsageRetentionDays < 0 {
		return 0
	}
	return p.cfg.UsageRetentionDays
}

// pruneUsage deletes usage records older than the retention window.
func (p *Platform) pruneUsage() {
	days := p.usageRetentionDays()
	if days <= 0 || p.store == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	removed, err := p.store.PruneUsageBefore(cutoff)
	if err != nil {
		logrus.WithError(err).Warn("platform: 清理用量日志失败")
		return
	}
	if removed > 0 {
		logrus.WithField("removed", removed).WithField("retention_days", days).Info("platform: 已清理过期用量日志")
	}
}
