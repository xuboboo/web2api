package platform

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

// Error envelopes fixed by the product contract:
//
//	401 {"error":"unauthorized"}
//	429 {"error":"quota_exceeded"}            (+ Retry-After)
//	429 {"error":"too_many_attempts"}         (+ Retry-After, login lockout)
//	429 {"error":"rate_limited"}              (+ Retry-After, admin limiter)
//	400 {"error":"bad_request","code":"...","reason":"EMPTY_QUERY|TOO_MANY_URLS|INVALID_PARAM"}
//
// Every 429 carries a Retry-After header (契约 B).
// The /v1/index/check 400 envelopes are produced by the index handler
// (integration stage) via errBadRequest here; admin-side validation errors use
// the same "bad_request" shape with reason INVALID_PARAM.

func errUnauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
}

func errQuotaExceeded(c *fiber.Ctx) error {
	// Daily quotas reset at local midnight — that is the earliest a blocked
	// key can succeed again, so it is the Retry-After value (契约 B: 所有 429
	// 带 Retry-After).
	c.Set(fiber.HeaderRetryAfter, itoa(secondsUntilLocalMidnight(time.Now())))
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":               "quota_exceeded",
		"retry_after_seconds": secondsUntilLocalMidnight(time.Now()),
	})
}

// errTooManyAttempts answers the login-lockout 429: the lock window is the
// Retry-After.
func errTooManyAttempts(c *fiber.Ctx, retryAfterSeconds int) error {
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	c.Set(fiber.HeaderRetryAfter, itoa(retryAfterSeconds))
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":               "too_many_attempts",
		"retry_after_seconds": retryAfterSeconds,
	})
}

// errRateLimited answers the admin-API rate-limit 429 with Retry-After.
func errRateLimited(c *fiber.Ctx, retryAfterSeconds int) error {
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	c.Set(fiber.HeaderRetryAfter, itoa(retryAfterSeconds))
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":               "rate_limited",
		"retry_after_seconds": retryAfterSeconds,
	})
}

// errEngineControlUnavailable answers 503 when the cooldown management
// surface was not injected (engineControl == nil).
func errEngineControlUnavailable(c *fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "engine_control_unavailable"})
}

// secondsUntilLocalMidnight is how long until the local daily counters roll.
func secondsUntilLocalMidnight(now time.Time) int {
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	secs := int(math.Ceil(next.Sub(now).Seconds()))
	if secs < 1 {
		secs = 1
	}
	return secs
}

// itoa avoids importing strconv for two call sites.
func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}

// errBadRequest answers the public-API 400 envelope: error + code + reason
// (reason is one of EMPTY_QUERY / TOO_MANY_URLS / INVALID_PARAM).
func errBadRequest(c *fiber.Ctx, reason string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error":  "bad_request",
		"code":   reason,
		"reason": reason,
	})
}

func errNotFound(c *fiber.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "not_found"})
}

// errForbidden answers 403 {"error":"forbidden"} (S4-GAP 密钥增强:
// allowed_ips 不匹配的密钥; MCP origin 白名单沿用 mcp 层自己的 403 形状).
func errForbidden(c *fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
}

// errBadRequestMsg answers the admin-side 400 envelope with a natural-language
// message (S4-B-11 密码策略等校验错误的展示口径; message 不含配置键).
func errBadRequestMsg(c *fiber.Ctx, reason, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error":   "bad_request",
		"code":    reason,
		"reason":  reason,
		"message": message,
	})
}

// errInternal answers the admin-side 500 envelope (S4-B-09 去敏): the storage
// layer error string may carry file paths, so the response only returns
// {"error":"server_error","request_id":N} — the raw error goes to the server
// log with the same request id for correlation.
func errInternal(c *fiber.Ctx, err error) error {
	requestID := strings.TrimSpace(c.Get("X-Request-ID"))
	logrus.WithError(err).WithField("request_id", requestID).
		Error("platform: internal error (sanitized)")
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
		"error":      "server_error",
		"request_id": requestID,
	})
}

// Exported wrappers so other modules (the integration-stage index/check
// handler in particular) emit byte-identical contract envelopes.

// RejectBadRequest answers the public-API 400 envelope; reason is one of
// EMPTY_QUERY / TOO_MANY_URLS / INVALID_PARAM.
func RejectBadRequest(c *fiber.Ctx, reason string) error { return errBadRequest(c, reason) }

// RejectUnauthorized answers 401 {"error":"unauthorized"}.
func RejectUnauthorized(c *fiber.Ctx) error { return errUnauthorized(c) }

// RejectQuotaExceeded answers 429 {"error":"quota_exceeded"}.
func RejectQuotaExceeded(c *fiber.Ctx) error { return errQuotaExceeded(c) }
