# Web2API Examples

Short, copy-pasteable examples for using Web2API from JavaScript and Python.
Each one runs against a local server.

## Start a server

Most examples expect Web2API running on `https://radar.ydsdoa.cn`.

```bash
# Docker
docker run --init -p radar.ydsdoa.cn:7000 -it xuboboo/web2api serve -a 0.0.0.0 -p 7000

# Or from source
go build -o web2api . && ./web2api serve
```

## Try it without code

A search is a single HTTP GET, so you can check the server with `curl`:

```bash
# One engine
curl "https://radar.ydsdoa.cn/google/search?text=open+source+search+api&limit=10"

# Several engines at once
curl "https://radar.ydsdoa.cn/mega/search?text=open+source+search+api&engines=bing,duckduckgo&limit=10"
```

## SDKs and integrations

| Tool                    | Package                                                                                      | Install                         |
| ----------------------- | -------------------------------------------------------------------------------------------- | ------------------------------- |
| JavaScript / TypeScript | [`@web2api/sdk`](https://www.npmjs.com/package/@web2api/sdk)                               | `npm install @web2api/sdk`     |
| Python                  | [`web2api`](https://pypi.org/project/web2api/)                                             | `pip install web2api`          |
| MCP server (AI agents)  | [`@web2api/mcp`](https://www.npmjs.com/package/@web2api/mcp)                               | `npx @web2api/mcp`             |
| n8n community node      | [`@web2api/n8n-nodes-web2api`](https://www.npmjs.com/package/@web2api/n8n-nodes-web2api) | Install via n8n community nodes |

## Examples by question

### Getting started

- **How do I run a search from code?** - [JavaScript](quickstart/js-basic-search) · [Python](quickstart/python-basic-search)
- **How do I compare results across several engines?** - [JavaScript](search/js-multi-engine-compare)
- **How do I search a list of keywords and export them?** - [Python](search/python-keyword-csv)

### AI and LLM grounding

- **How do I ground an LLM answer in fresh search results?** - [JavaScript](ai/js-rag-context-builder)
- **How do I give a search tool to an agent?** - [Python](ai/python-agent-tool)
- **I want this inside Claude Code, Cursor, or another MCP client.** - [Use with AI agents](../README.md#use-with-ai-agents)

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
