package platform

import (
	"encoding/json"
	"fmt"

	"github.com/xuboboo/web2api/core"
)

type billedJobCoordinator interface {
	PendingBilledIndexJobs() []core.IndexJob
	ActivateBilledIndexJob(id, marker, fingerprint string) (*core.IndexJob, error)
	DiscardPreparedIndexJob(id, marker, fingerprint string) error
}

// ReconcileBilledJobs resolves interrupted submissions before accepting traffic.
// A missing marker proves the ledger did not commit. Any unreadable or
// inconsistent marker stops startup so a paid task cannot silently disappear.
func (p *Platform) ReconcileBilledJobs(coordinator billedJobCoordinator) error {
	if coordinator == nil {
		return nil
	}
	pending := coordinator.PendingBilledIndexJobs()
	if len(pending) > 0 && p.StoreKind() != "bbolt" {
		return fmt.Errorf("cannot reconcile billed jobs while the primary ledger is unavailable")
	}
	for _, job := range pending {
		if job.ID == "" || job.BillingMarker == "" || job.BillingFingerprint == "" || job.OwnerKeyID == "" || job.BillingUnits <= 0 {
			return fmt.Errorf("incomplete pending billing metadata for job %q", job.ID)
		}
		raw, err := p.store.GetSetting(job.BillingMarker)
		if err != nil {
			return fmt.Errorf("read pending job charge %q: %w", job.ID, err)
		}
		if raw == nil {
			if err := coordinator.DiscardPreparedIndexJob(job.ID, job.BillingMarker, job.BillingFingerprint); err != nil {
				return fmt.Errorf("discard uncharged job %q: %w", job.ID, err)
			}
			continue
		}
		var charge jobCharge
		if err := json.Unmarshal(raw, &charge); err != nil {
			return fmt.Errorf("decode pending job charge %q: %w", job.ID, err)
		}
		if charge.JobID != job.ID || charge.Fingerprint != job.BillingFingerprint || charge.KeyID != job.OwnerKeyID || charge.Units != job.BillingUnits {
			return fmt.Errorf("pending job charge %q does not match task metadata", job.ID)
		}
		if _, err := coordinator.ActivateBilledIndexJob(job.ID, job.BillingMarker, job.BillingFingerprint); err != nil {
			return fmt.Errorf("activate paid job %q: %w", job.ID, err)
		}
	}
	return nil
}
