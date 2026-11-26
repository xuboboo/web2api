package platform

// Usage-journal retention (滚动清理上限) and the shared RecordUsage wrapper.

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

const usagePrunePeriod = 6 * time.Hour

// recordUsage is the single choke point every billed write goes through.
// Retention runs on a timer so low request volume does not extend the policy.
func (p *Platform) recordUsage(rec *UsageRecord, keyID string, units int64, consume bool, at time.Time) error {
	if err := p.store.RecordUsage(rec, keyID, units, consume, at); err != nil {
		return err
	}
	return nil
}

func (p *Platform) startUsagePruner() {
	p.startUsagePrunerWithPeriod(usagePrunePeriod)
}

func (p *Platform) startUsagePrunerWithPeriod(period time.Duration) {
	if p.usageRetentionDays() <= 0 {
		return
	}
	if period <= 0 {
		period = usagePrunePeriod
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.usagePruneCancel = cancel
	p.usagePruneWG.Add(1)
	go func() {
		defer p.usagePruneWG.Done()
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.pruneUsage()
			}
		}
	}()
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
	p.usagePruneMu.Lock()
	defer p.usagePruneMu.Unlock()
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
