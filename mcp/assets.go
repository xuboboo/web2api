package mcp

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

// The agent-ecosystem assets are static files embedded into the single
// binary (契约 C: go:embed 静态文件). openapi.yaml covers every /api/*,
// /v1/*, /mcp, /{engine}/search and health/stats endpoint; llms.txt is the
// agent-facing platform brief.
var (
	//go:embed openapi.yaml
	openapiSpec []byte

	//go:embed llms.txt
	llmsText []byte
)

// serveAsset answers a GET with the embedded bytes and a precise content
// type; a short public cache window keeps importers (Dify/Coze) fast without
// hiding updates from the operator.
func serveAsset(body []byte, contentType string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, contentType)
		c.Set(fiber.HeaderCacheControl, "public, max-age=300")
		return c.Send(body)
	}
}
