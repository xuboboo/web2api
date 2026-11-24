package platform

import (
	"time"

	"github.com/gofiber/fiber/v2"
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
