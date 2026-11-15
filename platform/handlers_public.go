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
func (p *Platform) RegisterPublic(app fiber.Router, indexCheck fiber.Handler) {
	guard := p.APIKeyGuard()
	app.Post("/v1/index/check", guard, indexCheck)
	app.Get("/v1/index/check", guard, indexCheck)
	app.Get("/v1/quota", guard, p.handleQuota)
	app.Get("/v1/engines", guard, p.handleEngines)
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
