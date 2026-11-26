package platform

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/xuboboo/web2api/core"
)

type noticeEngineStub struct{ *engineControlFake }

func (*noticeEngineStub) JobResults(string) ([]core.IndexCheckURLResult, error) {
	return nil, nil
}

func (*noticeEngineStub) IndexJobNotificationSnapshot(id string) (*core.IndexJob, bool) {
	return &core.IndexJob{ID: id, Status: core.IndexJobFailed, Failed: 1}, true
}

func TestScheduledNoticeRecoveryConsumesMetadata(t *testing.T) {
	env := newTestEnv(t, http.StatusOK, false)
	stub := &noticeEngineStub{engineControlFake: &engineControlFake{}}
	env.p.engineControl = stub
	meta, err := json.Marshal(scheduledJobNoticeMeta{ScheduleID: "schedule-1", UserID: "operator", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.p.store.PutSetting("scheduled_job:job-1", meta); err != nil {
		t.Fatal(err)
	}
	env.p.RecoverScheduledNotices(stub)
	if value, err := env.p.store.GetSetting("scheduled_job:job-1"); err != nil || value != nil {
		t.Fatalf("scheduled metadata retained after recovery: value=%q err=%v", value, err)
	}
	notices, err := env.p.GetNotices("operator", 10)
	if err != nil || len(notices) != 1 || notices[0].Kind != "job_failed" {
		t.Fatalf("recovered notices=%+v err=%v", notices, err)
	}
}
