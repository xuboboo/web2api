package platform

import (
	"encoding/json"
	"testing"

	"github.com/xuboboo/web2api/core"
)

type recoveryCoordinatorFake struct {
	jobs      []core.IndexJob
	activated []string
	discarded []string
}

func (f *recoveryCoordinatorFake) PendingBilledIndexJobs() []core.IndexJob { return f.jobs }
func (f *recoveryCoordinatorFake) ActivateBilledIndexJob(id, marker, fingerprint string) (*core.IndexJob, error) {
	f.activated = append(f.activated, id)
	return &core.IndexJob{ID: id}, nil
}
func (f *recoveryCoordinatorFake) DiscardPreparedIndexJob(id, marker, fingerprint string) error {
	f.discarded = append(f.discarded, id)
	return nil
}

func TestReconcileBilledJobs(t *testing.T) {
	p := mustNewPlatform(t, t.TempDir(), nil)
	paid := core.IndexJob{ID: "paid", OwnerKeyID: "key", BillingMarker: "paid-marker", BillingFingerprint: "hash", BillingUnits: 2}
	unpaid := core.IndexJob{ID: "unpaid", OwnerKeyID: "key", BillingMarker: "unpaid-marker", BillingFingerprint: "hash2", BillingUnits: 1}
	raw, err := json.Marshal(jobCharge{JobID: paid.ID, Fingerprint: paid.BillingFingerprint, KeyID: paid.OwnerKeyID, Units: paid.BillingUnits})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.store.PutSetting(paid.BillingMarker, raw); err != nil {
		t.Fatal(err)
	}
	fake := &recoveryCoordinatorFake{jobs: []core.IndexJob{paid, unpaid}}
	if err := p.ReconcileBilledJobs(fake); err != nil {
		t.Fatal(err)
	}
	if len(fake.activated) != 1 || fake.activated[0] != paid.ID || len(fake.discarded) != 1 || fake.discarded[0] != unpaid.ID {
		t.Fatalf("recovery activated=%v discarded=%v", fake.activated, fake.discarded)
	}
	if err := p.store.PutSetting(paid.BillingMarker, []byte(`{"job_id":"other"}`)); err != nil {
		t.Fatal(err)
	}
	fake = &recoveryCoordinatorFake{jobs: []core.IndexJob{paid}}
	if err := p.ReconcileBilledJobs(fake); err == nil || len(fake.activated) != 0 || len(fake.discarded) != 0 {
		t.Fatalf("mismatched charge should stop recovery: %v %+v", err, fake)
	}
	p.storeKind = "json"
	if err := p.ReconcileBilledJobs(fake); err == nil {
		t.Fatal("fallback ledger accepted")
	}
}
