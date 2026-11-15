package cmd

import (
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/mcp"
	"github.com/xuboboo/web2api/platform"
)

// mountMCP wires the MCP Agent plane (二期契约 C) under core.BasePath:
//
//	POST /web-tool/mcp          MCP streamable HTTP(JSON-RPC 2.0)
//	GET  /web-tool/openapi.yaml OpenAPI 3.1 全端点规范(go:embed,公开)
//	GET  /web-tool/llms.txt     agent 友好平台说明(go:embed,公开)
//
// 终态接线(接缝已全部落地):
//
//  1. auth = plat.AuthAny():X-API-Key → key 校验 + 配额预检 + 按 JSON-RPC body
//     计费(web_search=1 unit,check_indexed=urls×engines units,get_quota/
//     list_engines/initialize 等不收费);Bearer JWT → 管理员不计费。
//  2. Deps = core 新接缝的直接方法引用:web_search 走 serv.SearchViaEngine
//     (现役 search 管道:重试/熔断/代理/冷却门),check_indexed 走
//     serv.CheckIndexBatch(与 /v1/index/check 完全同一编排,响应结构一致),
//     list_engines 走 serv.EnginesStatusWithCooldown(含冷却状态)。
//     计费全部由 AuthAny + ChargeUsage 承担,MCP 层不感知。
func mountMCP(app *fiber.App, serv *core.Server, plat *platform.Platform) {
	if plat == nil || serv == nil {
		// 没有平台层就没有鉴权层;Agent 平面不得无鉴权暴露。
		return
	}
	root := app.Group(core.BasePath)
	mcp.Version = version

	deps := mcp.Deps{
		Search:          serv.SearchViaEngine,
		CheckIndexed:    serv.CheckIndexBatch,
		EnginesStatus:   serv.EnginesStatusWithCooldown,
		OriginAllowlist: config.Security.MCPAllowedOrigins,
	}
	if err := mcp.Register(root, plat.AuthAny(), deps); err != nil {
		logrus.WithError(err).Error("MCP Agent 平面挂载失败(已跳过;其余 API 不受影响)")
		return
	}
	logrus.WithFields(logrus.Fields{
		"mcp":     panelURL() + "mcp",
		"openapi": panelURL() + "openapi.yaml",
		"llms":    panelURL() + "llms.txt",
	}).Info("MCP Agent 平面已挂载: " + panelURL() + "mcp")
}
