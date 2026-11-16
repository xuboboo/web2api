package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

type JSONErrorResponse struct {
	Error     string                 `json:"error"`
	Code      int                    `json:"code"`
	RequestID string                 `json:"request_id,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
	// RetryAfterSeconds is set (>0) on 429/503 errors that carry a Retry-After
	// header (engine cooldown, quota, rate limiting).
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
}

type CORSConfig struct {
	AllowOrigins string
	AllowMethods string
	AllowHeaders string
	MaxAge       int
}

const browserProfileIDHeader = "X-Browser-Profile-Id"
const useProfileHeader = "X-Use-Profile"

// Mcp-Session-Id is exposed so browser-based MCP clients can read the session
// id from the initialize response (streamable HTTP clients need it for
// follow-up requests / session termination).
const exposedResponseHeaders = "X-Request-ID, X-Cache, X-Fallback-Engine, X-Proxy-Mode, X-Proxy-Tag, X-Proxy-Used, X-Proxy-Attempts, X-Network-Bytes, Mcp-Session-Id, " + browserProfileIDHeader

// DefaultCORSAllowHeaders is the single source of truth for the CORS
// allow-headers default, shared by DefaultCORSConfig and the viper defaults
// in cmd/root.go so the two cannot drift.
// X-API-Key is the data-plane credential; Mcp-Session-Id is returned by MCP
// initialize. Both are needed for browser-based SDKs/clients to call the API
// and MCP endpoints cross-origin (otherwise the CORS preflight rejects them).
const DefaultCORSAllowHeaders = "Origin, Content-Type, Accept, Authorization, X-API-Key, Mcp-Session-Id, X-Use-Proxy, X-Proxy-URL, X-Proxy-Country, X-Proxy-Class, X-Proxy-Provider, X-Proxy-Session-ID, X-Request-ID, X-Tenant, X-Use-Profile"

func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins: "*",
		AllowMethods: "GET, POST, OPTIONS",
		AllowHeaders: DefaultCORSAllowHeaders,
		MaxAge:       86400,
	}
}

func RequestContextMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		requestID := strings.TrimSpace(c.Get("X-Request-ID"))
		if requestID == "" {
			id, err := uuid.NewV7()
			if err != nil {
				requestID = uuid.NewString()
			} else {
				requestID = id.String()
			}
		}

		requestCtx := WithRequestID(c.UserContext(), requestID)
		requestCtx = WithTenant(requestCtx, strings.TrimSpace(c.Get("X-Tenant")))
		requestCtx = WithQueryHash(requestCtx, QueryHash(c.Query("text")))
		c.SetUserContext(requestCtx)

		c.Set("X-Request-ID", requestID)
		return c.Next()
	}
}

// RequestTimeoutMiddleware bounds wall-clock time per request by attaching a
// deadline to the user context, which fasthttp never cancels on client
// disconnect.
func RequestTimeoutMiddleware(timeout time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), timeout)
		defer cancel()
		c.SetUserContext(ctx)
		return c.Next()
	}
}

func CORSMiddleware(cfg CORSConfig) fiber.Handler {
	cfg = normalizeCORSConfig(cfg)

	// B-13: allow_origins accepts a comma-separated list. "*" keeps the
	// wildcard; a list echoes back only the matching origin and sends no ACAO
	// otherwise. The admin plane (/api/*) never receives CORS headers — the
	// panel is same-origin, so cross-origin reads of the management API are
	// simply not needed and would only widen the browser attack surface.
	wildcard := strings.TrimSpace(cfg.AllowOrigins) == "*"
	allowedOrigins := map[string]bool{}
	if !wildcard {
		for _, entry := range strings.Split(cfg.AllowOrigins, ",") {
			if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" {
				allowedOrigins[entry] = true
			}
		}
	}

	return func(c *fiber.Ctx) error {
		isAdminPlane := strings.HasPrefix(c.Path(), BasePath+"/api/") || c.Path() == BasePath+"/api"
		if isAdminPlane {
			// No ACAO for the management plane (B-13). Preflights answer 204
			// without allow headers so browsers stop early.
			if c.Method() == "OPTIONS" {
				return c.SendStatus(fiber.StatusNoContent)
			}
			return c.Next()
		}
		origin := strings.TrimSpace(c.Get(fiber.HeaderOrigin))
		switch {
		case wildcard:
			c.Set("Access-Control-Allow-Origin", cfg.AllowOrigins)
		case origin != "" && allowedOrigins[strings.ToLower(origin)]:
			// Echo the matched entry so credentials/origin binding stay exact.
			c.Set("Access-Control-Allow-Origin", origin)
		case c.Method() == "OPTIONS":
			// Non-matching preflight: no ACAO, browser blocks the request.
			return c.SendStatus(fiber.StatusNoContent)
		default:
			return c.Next()
		}
		c.Set("Access-Control-Allow-Methods", cfg.AllowMethods)
		c.Set("Access-Control-Allow-Headers", cfg.AllowHeaders)
		c.Set("Access-Control-Max-Age", fmt.Sprintf("%d", cfg.MaxAge))
		c.Set("Access-Control-Expose-Headers", exposedResponseHeaders)

		if c.Method() == "OPTIONS" {
			return c.SendStatus(fiber.StatusNoContent)
		}
		return c.Next()
	}
}

// normalizeCORSConfig keeps CORS behavior predictable when config provides partial values.
func normalizeCORSConfig(cfg CORSConfig) CORSConfig {
	defaults := DefaultCORSConfig()

	if strings.TrimSpace(cfg.AllowOrigins) == "" {
		cfg.AllowOrigins = defaults.AllowOrigins
	}
	if strings.TrimSpace(cfg.AllowMethods) == "" {
		cfg.AllowMethods = defaults.AllowMethods
	}
	if strings.TrimSpace(cfg.AllowHeaders) == "" {
		cfg.AllowHeaders = defaults.AllowHeaders
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = defaults.MaxAge
	}

	return cfg
}

func RequestLoggerMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		latency := time.Since(start)
		status := c.Response().StatusCode()
		if err != nil {
			if e, ok := err.(*fiber.Error); ok {
				status = e.Code
			} else if apiErr, ok := err.(*APIError); ok {
				status = apiErr.HTTPStatus
			} else {
				status = fiber.StatusInternalServerError
			}
		}

		logFields := logrus.Fields{
			"method": c.Method(),
			"path":   c.Path(),
			"status": status,
			"ip":     c.IP(),
		}
		logFields["latency_ms"] = latency.Milliseconds()
		if query := c.Query("text"); query != "" {
			logFields["query_hash"] = QueryHash(query)
		}
		addProxyLogFields(c, logFields)

		entry := WithRequest(c.UserContext()).WithFields(logFields)
		if status >= 500 {
			entry.Error("request failed")
		} else if status == fiber.StatusTooManyRequests {
			// 429 限速逐条 warn 会刷爆日志(档 50 限速 40s 内滚出 3 个 10MB 段)
			// 并淹没真实告警;聚合计数已由 Metrics().IncRateLimited →
			// /web-tool/metrics rate_limited_total 承担,逐条请求日志降为 debug。
			entry.Debug("request rate limited")
		} else if status >= 400 {
			entry.Warn("request error")
		} else {
			entry.Info("request completed")
		}

		// S4-GAP 指标: per-route/status counters + sparse duration histogram.
		route := c.Route().Path
		if route == "" || route == "/" {
			route = "unmatched"
		}
		Metrics().IncHTTP(route, status)
		Metrics().ObserveRequestDuration(latency.Seconds())

		return err
	}
}

func addProxyLogFields(c *fiber.Ctx, fields logrus.Fields) {
	if country := strings.ToLower(strings.TrimSpace(c.Get("X-Proxy-Country"))); country != "" {
		fields["proxy_country"] = country
	}
	if class := strings.ToLower(strings.TrimSpace(c.Get("X-Proxy-Class"))); class != "" {
		fields["proxy_class"] = class
	}
	if provider := strings.ToLower(strings.TrimSpace(c.Get("X-Proxy-Provider"))); provider != "" {
		fields["proxy_provider"] = provider
	}

	if sessionID := strings.TrimSpace(c.Get("X-Proxy-Session-ID")); sessionID != "" {
		fields["proxy_session_id"] = sessionID
	}

	if proxyURL := strings.TrimSpace(c.Get("X-Proxy-URL")); proxyURL != "" {
		fields["proxy_used"] = MaskProxyURL(proxyURL)
	}

	if laneKey := proxyLaneKeyFromContext(c.UserContext()); !laneKey.Empty() {
		fields["lane_id"] = laneKey.ID()
	}
}

func JSONErrorMiddleware() fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		code := fiber.StatusInternalServerError
		errorCode := ""
		reason := ""
		var meta map[string]interface{}

		if e, ok := err.(*fiber.Error); ok {
			code = e.Code
		}
		isAPIError := false
		var apiErr *APIError
		if e, ok := err.(*APIError); ok {
			apiErr = e
			code = apiErr.HTTPStatus
			errorCode = apiErr.ErrorCode
			reason = apiErr.Reason
			meta = apiErr.Meta
			isAPIError = true
		}
		if errorCode == "" {
			errorCode = statusText(code)
		}
		requestID := RequestIDFromContext(c.UserContext())
		if requestID == "" {
			requestID = strings.TrimSpace(c.Get("X-Request-ID"))
		}

		// Keep error details out of 5xx bodies: unknown internal errors
		// (library panics, proxy URLs) are logged in full server-side and
		// returned to the client as a generic message. APIError responses
		// have already been sanitized by their producers; fiber.NewError
		// texts keep their detail but credential-bearing URLs get masked.
		message := err.Error()
		if code >= 500 && !isAPIError {
			if _, isFiberErr := err.(*fiber.Error); !isFiberErr {
				logrus.WithError(err).WithField("request_id", requestID).
					Error("Unhandled internal error")
				message = "internal server error"
			} else {
				message = maskCredentialedURLs(message)
			}
		}

		resp := JSONErrorResponse{
			Error:     errorCode,
			Code:      code,
			RequestID: requestID,
			Message:   message,
			Reason:    reason,
			Meta:      meta,
		}
		if isAPIError {
			if apiErr.RetryAfterSeconds > 0 {
				c.Set(fiber.HeaderRetryAfter, strconv.Itoa(apiErr.RetryAfterSeconds))
				resp.RetryAfterSeconds = apiErr.RetryAfterSeconds
			}
		}

		c.Set("Content-Type", "application/json")
		return c.Status(code).JSON(resp)
	}
}

func statusText(code int) string {
	switch {
	case code == 400:
		return "bad_request"
	case code == 401:
		return "unauthorized"
	case code == 403:
		return "forbidden"
	case code == 404:
		return "not_found"
	case code == 405:
		return "method_not_allowed"
	case code == 429:
		return "rate_limited"
	case code == 502:
		return "bad_gateway"
	case code == 503:
		return "service_unavailable"
	case code == 504:
		return "gateway_timeout"
	case code >= 400 && code < 500:
		return "client_error"
	case code >= 500:
		return "server_error"
	default:
		return "error"
	}
}
