package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Tool names (contract C — the four tools; naming must stay in sync with
// docs/AGENT-README.md, the admin panel Agent page and skills/).
const (
	toolWebSearch    = "web_search"
	toolCheckIndexed = "check_indexed"
	toolGetQuota     = "get_quota"
	toolListEngines  = "list_engines"
)

// engineOptions is the closed engine set of the contract enum
// "baidu|sogou|so360|bing" plus the "360" public slug alias served by the
// data plane (/web-tool/360/search).
var engineOptions = []string{"baidu", "sogou", "so360", "bing", "360"}

// contractEngines renders the contract enum for descriptions.
const contractEngines = "baidu|sogou|so360|bing"

// defaultSearchLimit / maxSearchLimit mirror the contract default (10) and the
// data plane's MaxQueryLimit (100).
const (
	defaultSearchLimit = 10
	maxSearchLimit     = 100
)

// maxCheckURLs mirrors the platform contract maximum for one check batch.
const maxCheckURLs = 20

func isKnownEngine(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, e := range engineOptions {
		if e == name {
			return true
		}
	}
	return false
}

// toolSpecs returns the tools/list payload: name + description + JSON Schema
// inputSchema per tool.
func toolSpecs() []map[string]any {
	return []map[string]any{
		{
			"name": toolWebSearch,
			"description": "国内搜索引擎 SERP 查询。返回 {results:[{rank,title,url,snippet}]}。" +
				"计费:每次调用计 1 次。引擎冷却期调用会返回 engine_cooldown 错误。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "搜索关键词,例如 \"golang fiber 教程\"",
					},
					"engine": map[string]any{
						"type":        "string",
						"enum":        engineOptions,
						"default":     "baidu",
						"description": "搜索引擎(" + contractEngines + ",360 为 so360 的别名),默认 baidu",
					},
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     maxSearchLimit,
						"default":     defaultSearchLimit,
						"description": "返回条数,默认 10,最大 100",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			"name": toolCheckIndexed,
			"description": "检测 URL 是否被百度/搜狗/360/必应收录(收录判定)。返回与 GET /v1/index/check 相同的" +
				" {id,created_at,results:[{url,engines:{...}}],meta} 结构。计费:urls × engines 次(URL 数 × 引擎数)。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"urls": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "minLength": 1},
						"minItems":    1,
						"maxItems":    maxCheckURLs,
						"description": "待检测的绝对 URL 列表(1..20 条)",
					},
					"engines": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": engineOptions},
						"description": "可选引擎列表(" + contractEngines + "),缺省=全部四引擎",
					},
				},
				"required": []string{"urls"},
			},
		},
		{
			"name":        toolGetQuota,
			"description": "查询当前调用方的配额与用量。X-API-Key 计费方返回配额对象(等价 GET /v1/quota);管理员 JWT 不计费。",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        toolListEngines,
			"description": "列出四个引擎及其运行状态(启用/冷却/熔断),用于决定优先调用哪个引擎。",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// rawArgs is a parsed arguments object.
type rawArgs map[string]json.RawMessage

// parseWebSearchArgs validates web_search arguments:
//
//	query  required, non-empty string
//	engine optional, one of the contract enum (case-insensitive), default baidu
//	limit  optional integer 1..100, default 10
func parseWebSearchArgs(args rawArgs) (engine, query string, limit int, err error) {
	engine = "baidu"
	limit = defaultSearchLimit

	raw, ok := args["query"]
	if !ok {
		return "", "", 0, fmt.Errorf("query is required")
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return "", "", 0, fmt.Errorf("query must be a string")
	}
	if strings.TrimSpace(query) == "" {
		return "", "", 0, fmt.Errorf("query must not be empty")
	}

	if raw, ok := args["engine"]; ok {
		if err := json.Unmarshal(raw, &engine); err != nil {
			return "", "", 0, fmt.Errorf("engine must be a string")
		}
		engine = strings.ToLower(strings.TrimSpace(engine))
		if !isKnownEngine(engine) {
			return "", "", 0, fmt.Errorf("engine must be one of %s (got %q)", contractEngines, engine)
		}
	}

	if raw, ok := args["limit"]; ok {
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return "", "", 0, fmt.Errorf("limit must be an integer")
		}
		if f != math.Trunc(f) {
			return "", "", 0, fmt.Errorf("limit must be an integer")
		}
		limit = int(f)
		if limit < 1 || limit > maxSearchLimit {
			return "", "", 0, fmt.Errorf("limit must be between 1 and %d", maxSearchLimit)
		}
	}
	return engine, query, limit, nil
}

// parseCheckIndexedArgs validates check_indexed arguments:
//
//	urls    required, 1..20 non-empty strings
//	engines optional, array of contract engine names; nil ⇒ all four engines
func parseCheckIndexedArgs(args rawArgs) (urls []string, engines []string, err error) {
	raw, ok := args["urls"]
	if !ok {
		return nil, nil, fmt.Errorf("urls is required")
	}
	if err := json.Unmarshal(raw, &urls); err != nil {
		return nil, nil, fmt.Errorf("urls must be an array of strings")
	}
	if len(urls) == 0 {
		return nil, nil, fmt.Errorf("urls must contain 1..%d entries", maxCheckURLs)
	}
	if len(urls) > maxCheckURLs {
		return nil, nil, fmt.Errorf("urls accepts at most %d entries, got %d", maxCheckURLs, len(urls))
	}
	for i, u := range urls {
		if strings.TrimSpace(u) == "" {
			return nil, nil, fmt.Errorf("urls[%d] must not be empty", i)
		}
	}

	if raw, ok := args["engines"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &engines); err != nil {
			return nil, nil, fmt.Errorf("engines must be an array of strings")
		}
		for i, e := range engines {
			if !isKnownEngine(e) {
				return nil, nil, fmt.Errorf("engines[%d] must be one of %s (got %q)", i, contractEngines, e)
			}
			engines[i] = strings.ToLower(strings.TrimSpace(e))
		}
		if len(engines) == 0 {
			return nil, nil, fmt.Errorf("engines must not be empty when provided")
		}
	}
	return urls, engines, nil
}
