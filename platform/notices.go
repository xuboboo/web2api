package platform

import (
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
	"time"
)

type scheduledJobResults interface {
	JobResults(string) ([]core.IndexCheckURLResult, error)
}

type Notice struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	Kind           string     `json:"kind"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	JobID          string     `json:"job_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

func (p *Platform) GetNotices(userID string, limit int) ([]Notice, error) {
	return p.store.ListNotices(userID, limit)
}
func (p *Platform) handleNotices(c *fiber.Ctx) error {
	uid, _ := c.Locals(localAdminUserID).(string)
	role, _ := c.Locals(localAdminRole).(string)
	if role == RoleAdmin {
		uid = ""
	}
	rows, err := p.store.ListNotices(uid, 100)
	if err != nil {
		return errInternal(c, err)
	}
	return c.JSON(fiber.Map{"items": rows})
}
func (p *Platform) handleAcknowledgeNotice(c *fiber.Ctx) error {
	uid, _ := c.Locals(localAdminUserID).(string)
	role, _ := c.Locals(localAdminRole).(string)
	if role == RoleAdmin {
		uid = ""
	}
	ok, err := p.store.AcknowledgeNotice(uid, c.Params("id"), time.Now())
	if err != nil {
		return errInternal(c, err)
	}
	if !ok {
		return errNotFound(c)
	}
	return c.JSON(fiber.Map{"ok": true})
}
func (p *Platform) emitNotice(n Notice) {
	if n.ID == "" {
		n.ID = "notice_" + newUserID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	_ = p.store.PutNotice(&n)
}

func (p *Platform) completeNotice(jobID string, failed bool) {
	raw, err := p.store.GetSetting("scheduled_job:" + jobID)
	if err != nil || len(raw) == 0 {
		return
	}
	var schedule scheduledJob
	if json.Unmarshal(raw, &schedule) != nil {
		return
	}
	provider, ok := p.engineControl.(scheduledJobResults)
	if !ok {
		return
	}
	rows, err := provider.JobResults(jobID)
	if err != nil {
		return
	}
	previous := map[string]core.IndexCheckURLResult{}
	if raw, e := p.store.GetSetting("schedule_baseline:" + schedule.ID); e == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &previous)
	}
	changed := 0
	for _, row := range rows {
		if old, ok := previous[row.URL]; ok && core.JobVerdictChanged(old, row) {
			changed++
		}
		if !rowFailedNotice(row) {
			previous[row.URL] = row
		}
	}
	baseline, _ := json.Marshal(previous)
	_ = p.store.PutSetting("schedule_baseline:"+schedule.ID, baseline)
	if changed > 0 || failed {
		kind, title, body := "verdict_changed", "巡检结论变化", fmt.Sprintf("%d 个 URL 的有效收录结论发生变化。", changed)
		if failed {
			kind = "job_failed"
			title = "定时巡检失败"
			body = fmt.Sprintf("任务 %s 执行失败或存在错误，结果已保留。", jobID)
		}
		p.emitNotice(Notice{UserID: schedule.UserID, Kind: kind, Title: title, Body: body, JobID: jobID})
	}
}
func rowFailedNotice(row core.IndexCheckURLResult) bool {
	if len(row.Engines) == 0 {
		return true
	}
	for _, o := range row.Engines {
		if s, e := o.Verdict(); s != nil && e == nil {
			return false
		}
	}
	return true
}
func (p *Platform) NotifyScheduledJobFinished(jobID string, failed bool) {
	p.completeNotice(jobID, failed)
}
