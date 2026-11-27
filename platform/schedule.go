package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
)

const (
	maxSchedulesTotal     = 100
	maxSchedulesPerUser   = 20
	maxScheduledURLsTotal = 10_000
	maxScheduledURLBytes  = 2048
)

type scheduledJob struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UserID    string    `json:"user_id"`
	KeyID     string    `json:"key_id"`
	URLs      []string  `json:"urls"`
	Engines   []string  `json:"engines"`
	Frequency string    `json:"frequency"`
	Enabled   bool      `json:"enabled"`
	NextAt    time.Time `json:"next_at"`
	LastCycle string    `json:"last_cycle,omitempty"`
	LastJobID string    `json:"last_job_id,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (p *Platform) scheduleSettings() (map[string]scheduledJob, error) {
	raw, err := p.store.GetSetting("scheduled_index_jobs")
	if err != nil {
		return nil, err
	}
	out := map[string]scheduledJob{}
	if len(raw) > 0 {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}

func (p *Platform) putSchedules(items map[string]scheduledJob) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return p.store.PutSetting("scheduled_index_jobs", data)
}

func (p *Platform) RegisterSchedules(app fiber.Router) {
	app.Get("/api/index/check/schedules", p.adminRateGuard(), p.jwtGuard(), p.requireRole(RoleViewer), func(c *fiber.Ctx) error {
		items, err := p.scheduleSettings()
		if err != nil {
			return errInternal(c, err)
		}
		uid, _ := c.Locals(localAdminUserID).(string)
		role, _ := c.Locals(localAdminRole).(string)
		out := make([]scheduledJob, 0, len(items))
		for _, v := range items {
			if role == RoleAdmin || v.UserID == uid {
				v.URLs = nil
				out = append(out, v)
			}
		}
		return c.JSON(fiber.Map{"items": out, "runner_enabled": p.scheduleRunning.Load()})
	})
	app.Post("/api/index/check/schedules", p.adminRateGuard(), p.jwtGuard(), p.requireRole(RoleOperator), func(c *fiber.Ctx) error {
		var v scheduledJob
		if err := json.Unmarshal(c.Body(), &v); err != nil {
			return errBadRequest(c, "INVALID_PARAM")
		}
		v.Name = strings.TrimSpace(v.Name)
		v.KeyID = strings.TrimSpace(v.KeyID)
		v.Frequency = strings.ToLower(strings.TrimSpace(v.Frequency))
		if v.Name == "" || len(v.Name) > 100 || v.KeyID == "" || len(v.URLs) == 0 || len(v.URLs) > 5000 || (v.Frequency != "daily" && v.Frequency != "weekly") {
			return errBadRequestMsg(c, "INVALID_PARAM", "名称、密钥、1–5000 个 URL 和 daily/weekly 周期必填")
		}
		urls := make([]string, 0, len(v.URLs))
		seen := map[string]bool{}
		for _, raw := range v.URLs {
			raw = strings.TrimSpace(raw)
			if len(raw) > maxScheduledURLBytes {
				return errBadRequestMsg(c, "INVALID_PARAM", "URL 长度不能超过 2048 字节")
			}
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
				return errBadRequestMsg(c, "INVALID_PARAM", "URL 须为不含凭据的绝对 HTTP(S) URL")
			}
			if !seen[raw] {
				seen[raw] = true
				urls = append(urls, raw)
			}
		}
		engines, ok := ResolveEngines(v.Engines)
		if !ok {
			return errBadRequestMsg(c, "INVALID_PARAM", "引擎无效")
		}
		v.URLs = urls
		v.Engines = engines
		uid, _ := c.Locals(localAdminUserID).(string)
		v.UserID = uid
		v.Enabled = false
		v.ID = "schedule_" + newUserID()
		v.NextAt = time.Now()
		v.UpdatedAt = time.Now()
		p.scheduleMu.Lock()
		defer p.scheduleMu.Unlock()
		items, err := p.scheduleSettings()
		if err != nil {
			return errInternal(c, err)
		}
		if key, e := p.store.GetKeyByID(v.KeyID); e != nil || key == nil || key.Status != KeyStatusActive || (key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt)) {
			return errUnauthorized(c)
		}
		if scheduleCapacityExceeded(items, uid, len(urls)) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error":   "schedule_capacity_exceeded",
				"message": "定时巡检容量已达上限（全局 100 个计划、每用户 20 个计划、总计 10000 个 URL）",
			})
		}
		items[v.ID] = v
		if err = p.putSchedules(items); err != nil {
			return errInternal(c, err)
		}
		return c.Status(201).JSON(v)
	})
	app.Patch("/api/index/check/schedules/:id", p.adminRateGuard(), p.jwtGuard(), p.requireRole(RoleOperator), func(c *fiber.Ctx) error {
		p.scheduleMu.Lock()
		defer p.scheduleMu.Unlock()
		items, err := p.scheduleSettings()
		if err != nil {
			return errInternal(c, err)
		}
		v, ok := items[c.Params("id")]
		if !ok {
			return errNotFound(c)
		}
		uid, _ := c.Locals(localAdminUserID).(string)
		role, _ := c.Locals(localAdminRole).(string)
		if role != RoleAdmin && v.UserID != uid {
			return errNotFound(c)
		}
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if json.Unmarshal(c.Body(), &req) != nil || req.Enabled == nil {
			return errBadRequest(c, "INVALID_PARAM")
		}
		v.Enabled = *req.Enabled
		if v.Enabled {
			if key, e := p.store.GetKeyByID(v.KeyID); e != nil || key == nil || key.Status != KeyStatusActive || (key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt)) {
				return errBadRequestMsg(c, "INVALID_PARAM", "计费密钥已失效")
			}
			v.NextAt = time.Now()
		} else {
			v.NextAt = time.Now().Add(schedulePeriod(v.Frequency))
		}
		v.UpdatedAt = time.Now()
		items[v.ID] = v
		if err = p.putSchedules(items); err != nil {
			return errInternal(c, err)
		}
		return c.JSON(v)
	})
	app.Delete("/api/index/check/schedules/:id", p.adminRateGuard(), p.jwtGuard(), p.requireRole(RoleOperator), func(c *fiber.Ctx) error {
		p.scheduleMu.Lock()
		defer p.scheduleMu.Unlock()
		items, err := p.scheduleSettings()
		if err != nil {
			return errInternal(c, err)
		}
		v, ok := items[c.Params("id")]
		if !ok {
			return errNotFound(c)
		}
		uid, _ := c.Locals(localAdminUserID).(string)
		role, _ := c.Locals(localAdminRole).(string)
		if role != RoleAdmin && v.UserID != uid {
			return errNotFound(c)
		}
		delete(items, v.ID)
		if err = p.putSchedules(items); err != nil {
			return errInternal(c, err)
		}
		if err = p.store.DeleteSetting("schedule_baseline:" + v.ID); err != nil {
			return errInternal(c, err)
		}
		return c.SendStatus(204)
	})
}

func scheduleCapacityExceeded(items map[string]scheduledJob, userID string, incomingURLs int) bool {
	if len(items) >= maxSchedulesTotal {
		return true
	}
	userSchedules := 0
	totalURLs := 0
	for _, item := range items {
		if item.UserID == userID {
			userSchedules++
		}
		totalURLs += len(item.URLs)
	}
	return userSchedules >= maxSchedulesPerUser || incomingURLs > maxScheduledURLsTotal-totalURLs
}

func (p *Platform) StartScheduleRunner(ctx context.Context, interval time.Duration) {
	if !p.scheduleRunning.CompareAndSwap(false, true) {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		defer p.scheduleRunning.Store(false)
		p.runDueSchedules()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.runDueSchedules()
			}
		}
	}()
}

func schedulePeriod(frequency string) time.Duration {
	if frequency == "weekly" {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

func (p *Platform) runDueSchedules() {
	now := time.Now()
	p.scheduleMu.Lock()
	defer p.scheduleMu.Unlock()
	items, err := p.scheduleSettings()
	if err != nil {
		return
	}
	changed := false
	for id, v := range items {
		if !v.Enabled || v.NextAt.After(now) {
			continue
		}
		cycle := v.NextAt.UTC().Format("2006-01-02")
		if v.Frequency == "weekly" {
			year, week := v.NextAt.UTC().ISOWeek()
			cycle = fmt.Sprintf("%04d-W%02d", year, week)
		}
		key, err := p.store.GetKeyByID(v.KeyID)
		if err != nil || key == nil || key.Status != KeyStatusActive || (key.ExpiresAt != nil && !now.Before(*key.ExpiresAt)) {
			v.Enabled = false
			v.LastError = "计费密钥已失效，计划已暂停"
			items[id] = v
			changed = true
			continue
		}
		if v.LastCycle == cycle && v.LastJobID != "" {
			v.NextAt = nextScheduleAt(v.NextAt, v.Frequency, now)
			v.UpdatedAt = now
			items[id] = v
			changed = true
			continue
		}
		idempotency := "schedule-" + id + "-" + cycle
		jobID, _ := billedJobIdentity(v.UserID, key.ID, idempotency)
		meta, _ := json.Marshal(scheduledJobNoticeMeta{ScheduleID: v.ID, UserID: v.UserID, CreatedAt: now})
		metaKey := "scheduled_job:" + jobID
		err = p.store.PutSetting(metaKey, meta)
		var job *core.IndexJob
		if err == nil {
			ctx := withScheduledBilling(core.WithJobUser(core.WithJobOwner(context.Background(), key.ID), v.UserID))
			job, _, err = p.enqueueBilled(ctx, v.UserID, key.ID, idempotency, v.URLs, v.Engines, "", false)
		}
		if err != nil {
			_ = p.store.DeleteSetting(metaKey)
			v.LastError = err.Error()
			if strings.Contains(err.Error(), "quota_exceeded") || strings.Contains(err.Error(), "密钥已失效") {
				v.Enabled = false
			} else {
				v.NextAt = now.Add(time.Hour)
			}
		} else {
			v.LastCycle = cycle
			v.LastJobID = job.ID
			v.LastError = ""
			v.NextAt = nextScheduleAt(v.NextAt, v.Frequency, now)
		}
		v.UpdatedAt = now
		items[id] = v
		changed = true
	}
	if changed {
		if err := p.putSchedules(items); err != nil {
			return
		}
	}
}

func nextScheduleAt(previous time.Time, frequency string, now time.Time) time.Time {
	next := previous.Add(schedulePeriod(frequency))
	if !next.After(now) {
		next = now.Add(schedulePeriod(frequency))
	}
	return next
}
