package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
)

const maxBilledIndexJobURLs = 5000

type consoleJobCoordinator interface {
	PrepareBilledIndexJob(context.Context, []string, []string, string, string, string, int64) (*core.IndexJob, error)
	ActivateBilledIndexJob(string, string, string) (*core.IndexJob, error)
	DiscardPreparedIndexJob(string, string, string) error
	ExistingBilledIndexJob(string, string, string) (*core.IndexJob, error)
}

func jobError(status int, code, message string) error {
	return &core.APIError{HTTPStatus: status, ErrorCode: code, Reason: strings.ToUpper(code), Message: message}
}

func (p *Platform) submitConsoleJob(c *fiber.Ctx) error {
	var body struct {
		KeyID   string   `json:"key_id"`
		URLs    []string `json:"urls"`
		Engines []string `json:"engines"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return jobError(400, "invalid_param", "请求体须为有效 JSON")
	}
	userID, _ := c.Locals(localAdminUserID).(string)
	urls, engines, valid := p.parseIndexRequest(c)
	if !valid || userID == "" {
		return jobError(400, "invalid_param", "任务参数无效")
	}
	job, replayed, err := p.enqueueBilled(c.UserContext(), userID, strings.TrimSpace(body.KeyID), c.Get("Idempotency-Key"), urls, engines, c.IP())
	if err != nil {
		return err
	}
	return c.Status(202).JSON(acceptedJob(job, replayed, "/api/index/check/jobs/"))
}

func acceptedJob(job *core.IndexJob, replayed bool, prefix string) fiber.Map {
	path := core.BasePath + prefix + job.ID
	return fiber.Map{"job_id": job.ID, "status": job.Status, "total": job.Total, "engines": job.Engines, "created_at": job.CreatedAt, "poll_url": path, "results_url": path + "/results", "csv_url": path + "/results?format=csv", "replayed": replayed}
}

// enqueueBilled is shared by console, public async requests and schedules.
// The durable task is staged, then charged atomically, then made runnable.
func (p *Platform) enqueueBilled(ctx context.Context, userID, keyID, idempotency string, urls, engines []string, ip string) (*core.IndexJob, bool, error) {
	fail := func(status int, code, msg string) (*core.IndexJob, bool, error) {
		return nil, false, jobError(status, code, msg)
	}
	if p.StoreKind() != "bbolt" {
		return fail(503, "storage_unavailable", "持久化存储不可用")
	}
	coordinator, ok := p.engineControl.(consoleJobCoordinator)
	if !ok {
		return fail(503, "jobs_unavailable", "任务服务不可用")
	}
	store, ok := p.store.(jobChargeStore)
	if !ok {
		return fail(503, "storage_unavailable", "计费存储不可用")
	}
	if len(idempotency) < 16 || len(idempotency) > 128 || strings.ContainsAny(idempotency, "\r\n\t ") {
		return fail(400, "invalid_param", "Idempotency-Key 长度须为 16–128 且不含空白")
	}
	if strings.HasPrefix(idempotency, "schedule-") {
		idempotency = sha256Hex(idempotency)
	}
	if keyID == "" || len(urls) == 0 {
		return fail(400, "invalid_param", "须指定密钥和至少一个 URL")
	}
	if len(urls) > maxBilledIndexJobURLs {
		return fail(400, "too_many_urls", fmt.Sprintf("单次任务最多接受 %d 个 URL", maxBilledIndexJobURLs))
	}
	var valid bool
	engines, valid = ResolveEngines(engines)
	if !valid {
		return fail(400, "invalid_param", "查询引擎无效")
	}
	urls = append([]string(nil), urls...)
	for i := range urls {
		urls[i] = strings.TrimSpace(urls[i])
		if urls[i] == "" {
			return fail(400, "invalid_param", "URL 不可为空")
		}
	}
	units := int64(len(urls)) * int64(len(engines))
	identity, _ := json.Marshal([]string{userID, keyID, idempotency})
	marker := "job_charge_" + sha256Hex(string(identity))
	jobID := "job_" + strings.TrimPrefix(marker, "job_charge_")[:40]
	canonical, _ := json.Marshal(struct {
		UserID  string
		KeyID   string
		URLs    []string
		Engines []string
	}{userID, keyID, urls, engines})
	fingerprint := sha256Hex(string(canonical))
	p.jobSubmissionMu.Lock()
	defer p.jobSubmissionMu.Unlock()
	key, err := p.store.GetKeyByID(keyID)
	if err != nil {
		return nil, false, err
	}
	if key == nil || key.Status != KeyStatusActive || (key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt)) {
		return fail(401, "unauthorized", "计费密钥已失效")
	}
	if !ipAllowedFor(key.AllowedIPs, ip) {
		return fail(403, "forbidden", "计费密钥 IP 限制不允许当前入口")
	}
	if !p.allowKeyRate(key.ID) {
		return fail(429, "rate_limited", "调用过于频繁，请稍后重试")
	}
	markerData, err := p.store.GetSetting(marker)
	if err != nil {
		return nil, false, err
	}
	var job *core.IndexJob
	if markerData != nil {
		var previous jobCharge
		if err = json.Unmarshal(markerData, &previous); err != nil {
			return nil, false, err
		}
		if previous.Fingerprint != fingerprint || previous.KeyID != key.ID || previous.Units != units || previous.JobID != jobID {
			return fail(409, "idempotency_conflict", "幂等键已用于其他参数")
		}
		job, err = coordinator.ExistingBilledIndexJob(jobID, marker, fingerprint)
	} else {
		job, err = coordinator.PrepareBilledIndexJob(core.WithJobUser(core.WithJobOwner(ctx, key.ID), userID), urls, engines, jobID, marker, fingerprint, units)
	}
	if err != nil {
		return nil, false, err
	}
	// Serialize with synchronous REST/MCP quota reservations through commit.
	p.quotaMu.Lock()
	defer p.quotaMu.Unlock()
	if markerData == nil {
		key, err = p.store.GetKeyByID(keyID)
		if err != nil {
			return nil, false, err
		}
		if key == nil || key.Status != KeyStatusActive || (key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt)) {
			if err = coordinator.DiscardPreparedIndexJob(job.ID, marker, fingerprint); err != nil {
				return nil, false, err
			}
			return fail(401, "unauthorized", "计费密钥已失效")
		}
		if key.DailyQuota > 0 && units > key.DailyQuota-usedTodayUnits(key, time.Now())-p.pendingQuota[keyID] {
			if err = coordinator.DiscardPreparedIndexJob(job.ID, marker, fingerprint); err != nil {
				return nil, false, err
			}
			return fail(429, "quota_exceeded", "当日配额不足")
		}
	}
	now := time.Now()
	rec := &UsageRecord{Time: now, KeyID: key.ID, KeyName: key.Name, Endpoint: "/api/index/check/jobs", Engines: engines, URLs: len(urls), Units: int(units), Status: 202, Outcome: OutcomeBilled}
	charge := jobCharge{JobID: job.ID, Fingerprint: fingerprint, KeyID: key.ID, Units: units}
	_, replayed, err := store.CommitJobCharge(marker, charge, rec, key.ID, units, now)
	if err != nil {
		if errors.Is(err, errJobChargeConflict) {
			return fail(409, "idempotency_conflict", "幂等键冲突")
		}
		if errors.Is(err, errJobChargeKeyUnavailable) || errors.Is(err, errJobChargeQuotaExceeded) {
			if discardErr := coordinator.DiscardPreparedIndexJob(job.ID, marker, fingerprint); discardErr != nil {
				return nil, false, discardErr
			}
			if errors.Is(err, errJobChargeQuotaExceeded) {
				return fail(429, "quota_exceeded", "当日配额不足")
			}
			return fail(401, "unauthorized", "计费密钥已失效")
		}
		return nil, false, fmt.Errorf("commit job charge: %w", err)
	}
	job, err = coordinator.ActivateBilledIndexJob(job.ID, marker, fingerprint)
	if err != nil {
		return fail(503, "job_activation_pending", "计费已提交，使用相同幂等键重试激活")
	}
	if !replayed {
		core.Metrics().AddUsageUnits(units)
		if userID != "" {
			p.emitNotice(Notice{UserID: userID, Kind: "job_submitted", Title: "批量任务已提交", Body: fmt.Sprintf("任务 %s 已排队，提交 %d 个 URL。", job.ID, len(urls)), JobID: job.ID})
		}
	}
	return job, replayed, nil
}
