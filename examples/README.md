# Web2API Examples

Short, copy-pasteable examples for using Web2API from JavaScript and Python.

> **Legacy assets**: several examples still use retired engines (`google` / `ecosia` / `duckduckgo`) and the `@web2api/*` / `web2api` packages below are not published. Treat them as reference only; use `/web-tool/v1/index/check` and the console-generated API key for live integrations.

## Start a server

Most examples expect Web2API at `https://radar.ydsdoa.cn/web-tool/` (console: `https://radar.ydsdoa.cn/web-tool/console/`); for a local instance use `http://127.0.0.1:7000/web-tool/`.

```bash
# Docker
docker run --init -p 127.0.0.1:7000:7000 -it xuboboo/web2api serve -a 0.0.0.0 -p 7000

# Or from source
go build -o web2api . && ./web2api serve
```

## Try it without code

A search is a single HTTP GET, so you can check the server with `curl`:

```bash
# One engine
curl "https://radar.ydsdoa.cn/web-tool/bing/search?text=open+source+search+api&limit=10"

# Several engines at once
curl "https://radar.ydsdoa.cn/web-tool/v1/index/check?url=https%3A%2F%2Fexample.com&engines=bing,baidu"
```

## SDKs and integrations

| Tool                    | Package                                                                                      | Install                         |
| ----------------------- | -------------------------------------------------------------------------------------------- | ------------------------------- |
| JavaScript / TypeScript | `@web2api/sdk` (未发布)                                                                    | 使用仓库内示例或 OpenAPI       |
| Python                  | `web2api` (PyPI 上为同名无关包，版本不满足要求)                                            | 使用仓库内示例或 OpenAPI       |
| MCP server (AI agents)  | 内置 MCP（`/web-tool/mcp`，见 Agent 集成章节）                                             | 内置，无需 npm                 |
| n8n community node      | `@web2api/n8n-nodes-web2api` (未发布)                                                      | 用 OpenAPI 导入 n8n            |

## Examples by question

### Getting started

- **How do I run a search from code?** - [JavaScript](quickstart/js-basic-search) · [Python](quickstart/python-basic-search)
- **How do I compare results across several engines?** - [JavaScript](search/js-multi-engine-compare)
- **How do I search a list of keywords and export them?** - [Python](search/python-keyword-csv)

### AI and LLM grounding

- **How do I ground an LLM answer in fresh search results?** - [JavaScript](ai/js-rag-context-builder)
- **How do I give a search tool to an agent?** - [Python](ai/python-agent-tool)
- **I want this inside Claude Code, Cursor, or another MCP client.** - [Use with AI agents](../README.md#6-agent-integrations-mcp--openapi)

### SEO

- **Who are my real competitors for a set of keywords?** - [JavaScript](seo/js-competitor-overlap)
- **Where does my domain rank for each keyword?** - [Python](seo/python-rank-tracker)

### Content extraction

- **How do I search and read the page content in one call?** - [JavaScript](content/js-search-with-extract)
- **How do I turn a URL into clean Markdown?** - [Python](content/python-extract-markdown)

### Images

- **How do I search images and preview them?** - [JavaScript](media/js-image-gallery)

### Automation

- **I want to wire Web2API into a no-code workflow.** - [`@web2api/n8n-nodes-web2api`](https://www.npmjs.com/package/@web2api/n8n-nodes-web2api)
