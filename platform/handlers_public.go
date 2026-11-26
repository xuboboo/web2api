package platform

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/xuboboo/web2api/core"
)

// quotaResponse is the GET /v1/quota envelope. daily_quota=0 means unlimited.
type quotaResponse struct {
	KeyID      string `json:"key_id"`
	Name       string `json:"name"`
	DailyQuota int64  `json:"daily_quota"`
	UsedToday  int64  `json:"used_today"`
	Unlimited  bool   `json:"unlimited"`
	UsedTotal  int64  `json:"used_total"`
}

// RegisterConsoleJobs gives console users access to their own jobs and admins
// access to all jobs. JWT authorization is evaluated on every request.
func (p *Platform) RegisterConsoleJobs(app fiber.Router, h IndexJobHandlers) {
	principal := func(c *fiber.Ctx) error {
		userID, _ := c.Locals(localAdminUserID).(string)
		role, _ := c.Locals(localAdminRole).(string)
		if userID == "" {
			return errUnauthorized(c)
		}
		c.SetUserContext(core.WithConsoleJobAccess(c.UserContext(), userID, role == RoleAdmin))
		return c.Next()
	}
	limiter, auth, role := p.adminRateGuard(), p.jwtGuard(), p.requireRole(RoleViewer)
	app.Post("/api/index/check/jobs", limiter, auth, p.requireRole(RoleOperator), p.submitConsoleJob)
	app.Get("/api/index/check/jobs-retention-preview", limiter, auth, p.requireRole(RoleAdmin), func(c *fiber.Ctx) error {
		days, err := strconv.Atoi(c.Query("days"))
		if err != nil || days < 1 || days > 36500 {
			return errBadRequestMsg(c, "INVALID_PARAM", "days 须为 1–36500 的整数")
		}
		preview, ok := p.engineControl.(interface {
			IndexJobRetentionPreview(int) (int, int64, error)
		})
		if !ok {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}
		count, bytes, err := preview.IndexJobRetentionPreview(days)
		if err != nil {
			return errInternal(c, err)
		}
		return c.JSON(fiber.Map{"days": days, "jobs": count, "bytes": bytes})
	})
	if h.List != nil {
		app.Get("/api/index/check/jobs", limiter, auth, role, principal, h.List)
	}
	if h.Status != nil {
		app.Get("/api/index/check/jobs/:id", limiter, auth, role, principal, h.Status)
	}
	if h.Results != nil {
		app.Get("/api/index/check/jobs/:id/results", limiter, auth, role, principal, h.Results)
	}
	if h.Cancel != nil {
		app.Delete("/api/index/check/jobs/:id", limiter, auth, role, principal, h.Cancel)
	}
	if h.Retry != nil {
		app.Post("/api/index/check/jobs/:id/retry", limiter, auth, p.requireRole(RoleOperator), principal, func(c *fiber.Ctx) error {
			coordinator, ok := p.engineControl.(interface {
				RetryFailedIndexJob(context.Context, string) (*core.IndexJob, error)
			})
			if !ok {
				return c.SendStatus(503)
			}
			userID, _ := c.Locals(localAdminUserID).(string)
			role, _ := c.Locals(localAdminRole).(string)
			if role != RoleAdmin {
				owner, ok := p.engineControl.(interface{ ConsoleJobOwned(string, string) bool })
				if !ok {
					return c.SendStatus(503)
				}
				if !owner.ConsoleJobOwned(c.Params("id"), userID) {
					return errNotFound(c)
				}
			}
			ctx := c.UserContext()
			ctx = core.WithIndexJobSubmission(ctx, func(_ context.Context, urls, engines []string) (*core.IndexJob, error) {
				original, ok := p.engineControl.(interface {
					ConsoleJobBillingInfo(string, string) (string, bool)
					ConsoleJobBillingInfoAdmin(string) (string, bool)
					ConsoleJobOwner(string) (string, bool)
				})
				if !ok {
					return nil, jobError(503, "retry_unavailable", "任务计费归属不可用")
				}
				keyID, found := original.ConsoleJobBillingInfo(c.Params("id"), userID)
				if role == RoleAdmin && !found {
					keyID, found = original.ConsoleJobBillingInfoAdmin(c.Params("id"))
				}
				if !found {
					return nil, errNotFound(c)
				}
				if role == RoleAdmin {
					if _, ok := original.ConsoleJobOwner(c.Params("id")); !ok {
						return nil, errNotFound(c)
					}
				}
				idem := strings.TrimSpace(c.Get("Idempotency-Key"))
				if idem == "" {
					return nil, jobError(400, "invalid_param", "重试须提供 16–128 位 Idempotency-Key")
				}
				billingUser := userID
				if role == RoleAdmin {
					billingUser, _ = original.ConsoleJobOwner(c.Params("id"))
				}
				job, _, err := p.enqueueBilled(ctx, billingUser, keyID, idem, urls, engines, c.IP())
				return job, err
			})
			job, err := coordinator.RetryFailedIndexJob(ctx, c.Params("id"))
			if err != nil {
				return err
			}
			c.Status(202)
			return c.JSON(job)
		})
	}
	app.Get("/api/notices", limiter, auth, role, p.handleNotices)
	app.Post("/api/notices/:id/ack", limiter, auth, role, p.handleAcknowledgeNotice)
}

// RegisterPublic mounts the contract's public /v1/* endpoints on the given
// router (pass the root fiber app; paths are registered verbatim):
//
//	POST /v1/index/check   (APIKeyGuard + indexCheck)
//	GET  /v1/index/check   (APIKeyGuard + indexCheck; ?url=...&engines=baidu,bing)
//	GET  /v1/quota         (APIKeyGuard)
//	GET  /v1/engines       (APIKeyGuard → {"engines": engineStatus()})
//
// indexCheck is the index module's handler (func(c *fiber.Ctx) error); it runs
// behind APIKeyGuard and can read the platform_units/platform_urls/
// platform_engines locals injected by the guard.
//
// T-04: 未注册的 HTTP 方法在每个数据面路径上显式回答 405 method_not_allowed
// (与 MCP 面 GET /mcp 的先例一致),不再落入静态 404 兜底返回
// {"error":"not_found"}。OPTIONS 留空给 CORS 预检,不在此注册。
func (p *Platform) RegisterPublic(app fiber.Router, indexCheck fiber.Handler) {
	guard := p.APIKeyGuard()
	app.Post("/v1/index/check", guard, indexCheck)
	app.Get("/v1/index/check", guard, indexCheck)
	app.Get("/v1/quota", guard, p.handleQuota)
	app.Get("/v1/engines", guard, p.handleEngines)

	// 数据面路径上未注册方法的显式 405(契约口径: 405 优先于 404 兜底)。
	methodNotAllowed := func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusMethodNotAllowed, "Method Not Allowed")
	}
	registerMethodNotAllowed := func(path string, exclude ...string) {
		excluded := make(map[string]bool, len(exclude))
		for _, m := range exclude {
			excluded[m] = true
		}
		for _, m := range []string{fiber.MethodPost, fiber.MethodPut, fiber.MethodDelete, fiber.MethodPatch} {
			if !excluded[m] {
				app.Add(m, path, methodNotAllowed)
			}
		}
	}
	registerMethodNotAllowed("/v1/index/check", fiber.MethodPost)
	registerMethodNotAllowed("/v1/quota", fiber.MethodGet)
	registerMethodNotAllowed("/v1/engines", fiber.MethodGet)
}

// IndexJobHandlers bundles the asynchronous index-check job handlers mounted
// under /v1/index/check/jobs. Each runs behind APIKeyGuard.
type IndexJobHandlers struct {
	List    fiber.Handler
	Status  fiber.Handler
	Results fiber.Handler
	Cancel  fiber.Handler
	Retry   fiber.Handler
}

// RegisterPublicJobs mounts the async job routes:
//
//	GET    /v1/index/check/jobs
//	GET    /v1/index/check/jobs/:id
//	GET    /v1/index/check/jobs/:id/results[?format=csv]
//	DELETE /v1/index/check/jobs/:id
//
// It is separate from RegisterPublic so deployments that keep index jobs
// disabled (no job directory) do not advertise the routes.
func (p *Platform) RegisterPublicJobs(app fiber.Router, h IndexJobHandlers) {
	guard := p.APIKeyGuard()
	if h.List != nil {
		app.Get("/v1/index/check/jobs", guard, h.List)
	}
	if h.Status != nil {
		app.Get("/v1/index/check/jobs/:id", guard, h.Status)
	}
	if h.Results != nil {
		app.Get("/v1/index/check/jobs/:id/results", guard, h.Results)
	}
	if h.Cancel != nil {
		app.Delete("/v1/index/check/jobs/:id", guard, h.Cancel)
	}
}

// handleQuota answers GET /v1/quota for the authenticated key.
func (p *Platform) handleQuota(c *fiber.Ctx) error {
	key, ok := c.Locals(localKeyName).(APIKey)
	if !ok {
		return errUnauthorized(c)
	}
	return c.JSON(quotaResponse{
		KeyID:      key.ID,
		Name:       key.Name,
		DailyQuota: key.DailyQuota,
		UsedToday:  usedTodayUnits(&key, time.Now()),
		Unlimited:  key.DailyQuota == 0,
		UsedTotal:  key.UsedTotal,
	})
}

// handleEngines answers GET /v1/engines with the engine/circuit-breaker
// snapshot supplied at construction time.
func (p *Platform) handleEngines(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"engines": p.engineStatusValue()})
}
