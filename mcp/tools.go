package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/xuboboo/web2api/core"
)

// Tool names (contract C — the four tools; naming must stay in sync with
// docs/AGENT-README.md, the admin panel Agent page and skills/). The two job
// tools extend check_indexed for batches too large for one synchronous call.
const (
	toolWebSearch      = "web_search"
	toolCheckIndexed   = "check_indexed"
	toolGetIndexJob    = "get_index_job"
	toolCancelIndexJob = "cancel_index_job"
	toolGetQuota       = "get_quota"
	toolListEngines    = "list_engines"
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

// maxCheckURLs mirrors the platform contract maximum for one synchronous
// check batch; larger batches become an async job.
const maxCheckURLs = core.MaxIndexCheckURLs

// maxJobURLs mirrors index_jobs.max_urls (the per-job ceiling).
const maxJobURLs = 5000

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
			"description": "检测 URL 是否被百度/搜狗/360/必应收录(收录判定)。≤20 条同步返回与 GET /v1/index/check 相同的" +
				" {id,created_at,results:[{url,engines:{...}}],meta} 结构;超过 20 条则进入服务端队列,立即返回" +
				" {job_id,status,total,...},随后用 get_index_job 查进度/结果(整张表一次提交即可)。" +
				"计费:urls × engines 次(URL 数 × 引擎数)。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"urls": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "minLength": 1},
						"minItems":    1,
						"maxItems":    maxJobURLs,
						"description": "待检测的绝对 URL 列表(1..5000;>20 条自动转为异步任务)",
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
			"name": toolGetIndexJob,
			"description": "查询异步收录任务:默认返回进度(status/progress/eta_seconds/各计数);" +
				"include_results=true 时附带已完成的结果行(与 check_indexed 同步结果同结构,可用 offset/limit 分页)。" +
				"不计费。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "check_indexed 返回的 job_id",
					},
					"include_results": map[string]any{
						"type":        "boolean",
						"default":     false,
						"description": "true = 一并返回结果行",
					},
					"offset": map[string]any{
						"type":        "integer",
						"minimum":     0,
						"default":     0,
						"description": "结果行起点(仅 include_results=true 时生效)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     maxJobURLs,
						"description": "返回多少行(缺省=全部剩余)",
					},
				},
				"required": []string{"job_id"},
			},
		},
		{
			"name":        toolCancelIndexJob,
			"description": "取消一个排队中或运行中的收录任务;已结束的任务返回错误。不计费。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "check_indexed 返回的 job_id",
					},
				},
				"required": []string{"job_id"},
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

func parseToolCallParams(raw json.RawMessage) (string, rawArgs, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return "", nil, fmt.Errorf("params must be an object with name and arguments")
		}
	}
	if strings.TrimSpace(params.Name) == "" {
		return "", nil, fmt.Errorf("name is required")
	}
	var args rawArgs
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return "", nil, fmt.Errorf("arguments must be an object")
		}
	}
	return params.Name, args, nil
}

// ToolQuotaRequest describes a validated call that may reserve API key quota.
type ToolQuotaRequest struct {
	Search   bool
	URLCount int
	Engines  []string
	Async    bool
}

// ParseToolQuotaRequest leaves invalid and unbilled calls for the RPC handler.
func ParseToolQuotaRequest(body []byte) ToolQuotaRequest {
	var req rpcRequest
	if json.Unmarshal(body, &req) != nil || req.Method != "tools/call" {
		return ToolQuotaRequest{}
	}
	if (req.JSONRPC != "" && req.JSONRPC != "2.0") || len(req.ID) == 0 || string(req.ID) == "null" {
		return ToolQuotaRequest{}
	}
	name, args, err := parseToolCallParams(req.Params)
	if err != nil {
		return ToolQuotaRequest{}
	}
	switch name {
	case toolWebSearch:
		if _, _, _, err := parseWebSearchArgs(args); err == nil {
			return ToolQuotaRequest{Search: true}
		}
	case toolCheckIndexed:
		if urls, engines, err := parseCheckIndexedArgs(args); err == nil {
			return ToolQuotaRequest{URLCount: len(urls), Engines: engines, Async: len(urls) > maxCheckURLs}
		}
	}
	return ToolQuotaRequest{}
}

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
//	urls    required, 1..maxJobURLs non-empty strings (the handler turns >20
//	        into an async job when the deployment wires one)
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
		return nil, nil, fmt.Errorf("urls must contain 1..%d entries", maxJobURLs)
	}
	if len(urls) > maxJobURLs {
		return nil, nil, fmt.Errorf("urls accepts at most %d entries, got %d", maxJobURLs, len(urls))
	}
	for i, u := range urls {
		if strings.TrimSpace(u) == "" {
			return nil, nil, fmt.Errorf("urls[%d] must not be empty", i)
		}
	}
	if len(urls) <= maxCheckURLs {
		if _, validationErr := core.ValidateIndexCheckURLs(urls); validationErr != nil {
			return nil, nil, fmt.Errorf("%s", validationErr.Message)
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

// parseGetIndexJobArgs validates get_index_job arguments.
func parseGetIndexJobArgs(args rawArgs) (jobID string, includeResults bool, offset, limit int, err error) {
	raw, ok := args["job_id"]
	if !ok {
		return "", false, 0, 0, fmt.Errorf("job_id is required")
	}
	if err := json.Unmarshal(raw, &jobID); err != nil {
		return "", false, 0, 0, fmt.Errorf("job_id must be a string")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", false, 0, 0, fmt.Errorf("job_id must not be empty")
	}
	if raw, ok := args["include_results"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &includeResults); err != nil {
			return "", false, 0, 0, fmt.Errorf("include_results must be a boolean")
		}
	}
	if raw, ok := args["offset"]; ok && string(raw) != "null" {
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || f != math.Trunc(f) || f < 0 {
			return "", false, 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = int(f)
	}
	if raw, ok := args["limit"]; ok && string(raw) != "null" {
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || f != math.Trunc(f) {
			return "", false, 0, 0, fmt.Errorf("limit must be an integer")
		}
		limit = int(f)
		if limit < 1 || limit > maxJobURLs {
			return "", false, 0, 0, fmt.Errorf("limit must be between 1 and %d", maxJobURLs)
		}
	}
	return jobID, includeResults, offset, limit, nil
}

// parseJobIDArg validates the single-job_id tools (cancel_index_job).
func parseJobIDArg(args rawArgs) (string, error) {
	raw, ok := args["job_id"]
	if !ok {
		return "", fmt.Errorf("job_id is required")
	}
	var jobID string
	if err := json.Unmarshal(raw, &jobID); err != nil {
		return "", fmt.Errorf("job_id must be a string")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", fmt.Errorf("job_id must not be empty")
	}
	return jobID, nil
}
