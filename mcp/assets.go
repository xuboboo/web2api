package mcp

import (
	_ "embed"
	"html"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// The agent-ecosystem assets are static files embedded into the single
// binary (契约 C: go:embed 静态文件). openapi.yaml is the PUBLIC contract —
// data plane (/v1/*, /{engine}/search, health/stats) plus agent plane (/mcp)
// only; admin-plane /api/* definitions live in the repo-only
// docs/internal/openapi-admin.yaml and are neither embedded nor served (审计 A-08).
// llms.txt is the agent-facing platform brief.
var (
	//go:embed openapi.yaml
	openapiSpec []byte

	//go:embed llms.txt
	llmsText []byte
)

// wantsHTML reports whether the caller is a human in a browser (Accept
// advertises text/html) and the raw variant was not explicitly requested
// via ?raw=1. Programmatic callers (Dify/n8n/curl/agents) never advertise
// text/html, so they keep receiving the raw bytes.
func wantsHTML(c *fiber.Ctx) bool {
	if c.Query("raw") == "1" {
		return false
	}
	return strings.Contains(c.Get(fiber.HeaderAccept), "text/html")
}

// assetView wraps the embedded file in a zero-dependency dark-theme document
// page (官网 V3 暗夜语言) so a browser click opens a polished, readable web
// page instead of a download (三期用户反馈:文档要网页浏览,不要下载)。
// eyebrow 区分两类资产页的眉标(openapi.yaml → OPEN API SPECIFICATION,
// llms.txt → AGENT PLATFORM BRIEF),避免简介页挂着「规范」眉标的错位组合。
// 不提供任何前端可见的「下载」入口(用户要求:防止被刷);程序化导入
// 直接 GET 本路径即可拿到原始字节(内容协商按 Accept 区分)。
func assetView(eyebrow, title, subtitle string, body []byte) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + html.EscapeString(title) + ` · Web2API 网站收录查询开放平台</title>
<link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='14' fill='%23177E5B'/%3E%3Cpath d='M18 20 L30 32 L18 44' fill='none' stroke='%23fff' stroke-width='6' stroke-linecap='round' stroke-linejoin='round'/%3E%3Cpath d='M34 44 L46 44' stroke='%233ECF8E' stroke-width='6' stroke-linecap='round'/%3E%3C/svg%3E">
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;background:#0B0B0D;color:#A1A1A6;font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;-webkit-font-smoothing:antialiased}
a{color:#3ECF8E;text-decoration:none}
a:hover{text-decoration:underline}
.topbar{height:60px;border-bottom:1px solid rgba(255,255,255,.08);background:rgba(11,11,13,.78);backdrop-filter:blur(16px);position:sticky;top:0;z-index:2}
.topbar-in{max-width:1100px;height:100%;margin:0 auto;padding:0 28px;display:flex;align-items:center;gap:12px}
.mark{width:28px;height:28px;flex:none}
.brand{color:#F5F5F7;font-weight:600;font-size:15px;letter-spacing:.2px;white-space:nowrap}
.brand span{color:#73737A;font-weight:400;margin-left:9px;font-size:13px}
.topnav{margin-left:auto;display:flex;gap:6px}
.topnav a{color:#A1A1A6;font-size:13px;padding:7px 12px;border-radius:8px}
.topnav a:hover{color:#F5F5F7;background:rgba(255,255,255,.06);text-decoration:none}
.wrap{max-width:1100px;margin:0 auto;padding:44px 28px 72px}
.eyebrow{display:inline-flex;align-items:center;gap:8px;color:#3ECF8E;font:600 11.5px/1 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;letter-spacing:1.4px;text-transform:uppercase;margin-bottom:18px}
.eyebrow::before{content:"";width:6px;height:6px;border-radius:50%;background:#3ECF8E;box-shadow:0 0 0 4px rgba(62,207,142,.16)}
h1{margin:0 0 12px;font-size:30px;line-height:1.25;color:#F5F5F7;font-weight:650;letter-spacing:-.4px}
.sub{margin:0 0 26px;color:#A1A1A6;font-size:14.5px;max-width:760px}
.card{border:1px solid rgba(255,255,255,.09);border-radius:16px;background:#17171A;overflow:hidden;box-shadow:0 8px 28px rgba(0,0,0,.22)}
.card-head{display:flex;align-items:center;gap:12px;padding:12px 18px;border-bottom:1px solid rgba(255,255,255,.07);background:#1E1E22}
.dots{display:flex;gap:6px}
.dots i{width:10px;height:10px;border-radius:50%;display:block}
.dots i:nth-child(1){background:#FF5F57}.dots i:nth-child(2){background:#FEBC2E}.dots i:nth-child(3){background:#28C840}
.fname{font:500 12px/1 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:#8A8A90}
.btn{margin-left:auto;padding:7px 14px;border-radius:8px;border:1px solid rgba(255,255,255,.14);background:rgba(255,255,255,.04);color:#F5F5F7;font:500 12.5px/1.4 inherit;cursor:pointer;transition:background .15s ease,border-color .15s ease,color .15s ease}
.btn:hover{background:#3ECF8E;border-color:#3ECF8E;color:#062B1C}
.btn:active{transform:scale(.97)}
pre{margin:0;background:transparent;padding:24px 26px;color:#D7D7DB;font:12.5px/1.75 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;overflow:auto;max-height:70vh;white-space:pre;tab-size:2}
.foot{display:flex;flex-wrap:wrap;gap:8px 22px;margin-top:20px;color:#8A8A90;font-size:12px}
.hint{margin-top:14px;padding:12px 16px;border-radius:10px;background:rgba(62,207,142,.07);border:1px solid rgba(62,207,142,.16);color:#A1A1A6;font-size:12.5px}
.hint code{font:500 12px/1 ui-monospace,monospace;color:#3ECF8E}
.quick{margin-top:22px;padding:18px 20px;border-radius:14px;border:1px solid rgba(255,255,255,.09);background:#17171A}
.quick h2{margin:0 0 12px;font-size:15px;color:#F5F5F7;font-weight:600}
.quick ol{margin:0;padding-left:20px;color:#A1A1A6;font-size:13px;line-height:1.9}
.quick code{font:500 12px/1 ui-monospace,monospace;color:#3ECF8E;background:rgba(62,207,142,.08);padding:2px 5px;border-radius:4px}
.quick a{display:inline-block;margin-top:12px;color:#3ECF8E;font-size:12.5px}
@media(max-width:640px){.topbar-in{padding:0 16px}.brand span,.topnav{display:none}.wrap{padding:28px 14px 48px}h1{font-size:24px}pre{padding:16px 14px;font-size:11.5px}.fname{font-size:11px}}
/* 细滚动条(与官网 V3 同款):本页不加载 assets/style.css,此处内联同规格 */
@supports (-moz-appearance: checkbox){html{scrollbar-width:thin;scrollbar-color:rgba(255,255,255,.22) transparent}pre{scrollbar-width:thin;scrollbar-color:rgba(255,255,255,.22) transparent}}
::-webkit-scrollbar{width:6px;height:6px}
::-webkit-scrollbar-track{background:transparent}
::-webkit-scrollbar-thumb{background:rgba(255,255,255,.22);border-radius:999px}
::-webkit-scrollbar-thumb:hover{background:rgba(255,255,255,.36)}
::-webkit-scrollbar-corner{background:transparent}
</style></head><body>
<header class="topbar"><div class="topbar-in">
<svg class="mark" viewBox="0 0 64 64" aria-hidden="true"><rect width="64" height="64" rx="14" fill="#177E5B"/><path d="M18 20 L30 32 L18 44" fill="none" stroke="#fff" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/><path d="M34 44 L46 44" stroke="#3ECF8E" stroke-width="6" stroke-linecap="round"/></svg>
<div class="brand">Web2API<span>网站收录查询开放平台</span></div>
<nav class="topnav"><a href="./">官网</a><a href="./console/">控制台</a></nav>
</div></header>
<main class="wrap">
<span class="eyebrow">` + html.EscapeString(eyebrow) + `</span>
<h1>` + html.EscapeString(title) + `</h1>
<p class="sub">` + html.EscapeString(subtitle) + `</p>
<section class="card">
<div class="card-head"><span class="dots"><i></i><i></i><i></i></span><span class="fname">` + html.EscapeString(title) + `</span><button class="btn" id="copyBtn" type="button">复制全文</button></div>
<pre id="content">` + html.EscapeString(string(body)) + `</pre>
</section>
<section class="quick">
<h2>5 分钟上手</h2>
<ol>
<li>登录:在<strong>控制台</strong>用管理员账号登录,取得 JWT(管理端点不在公开规范中列举)。</li>
<li>建钥:在控制台「API 密钥」页创建密钥 —— 完整 key 只返回一次,存好。</li>
<li>判定:<code>POST /v1/index/check</code>(带 <code>X-API-Key</code>)+ <code>{"urls":["https://example.com"],"engines":["baidu","bing"]}</code> 得到逐引擎收录结果。</li>
<li>查量:<code>GET /v1/quota</code> 查配额,用量流水在控制台查看。</li>
</ol>
<a href="./#quickstart">官网完整实例与代码示例 →</a>
</section>
<p class="hint">程序化导入(Dify / n8n / curl / Agent)直接 <code>GET</code> 本地址即可获得原始文件字节;浏览器访问自动呈现此网页视图。带真实响应样例、Python/JS/curl 代码与排错表的完整教程见仓库 <code>docs/QUICKSTART.md</code>。</p>
<div class="foot"><span>© Web2API 网站收录查询开放平台</span><a href="./">返回官网</a><a href="./console/">进入控制台</a></div>
</main>
<script>
(function(){
  var btn=document.getElementById('copyBtn');
  btn.addEventListener('click',function(){
    var text=document.getElementById('content').textContent;
    function done(){btn.textContent='已复制';setTimeout(function(){btn.textContent='复制全文';},1600);}
    function fallback(){var ta=document.createElement('textarea');ta.value=text;ta.style.position='fixed';ta.style.opacity='0';document.body.appendChild(ta);ta.select();try{document.execCommand('copy');done();}catch(e){}document.body.removeChild(ta);}
    if(navigator.clipboard&&window.isSecureContext){navigator.clipboard.writeText(text).then(done,fallback);}else{fallback();}
  });
})();
</script>
</body></html>`)
	return b.String()
}

// serveAsset answers a GET with either the raw embedded bytes (programmatic
// callers: importers, curl, agents — precise content type, short public
// cache window) or, for browsers, a zero-dependency HTML viewer so the spec
// opens as a web page instead of a download (三期用户反馈)。
func serveAsset(body []byte, contentType string, eyebrow, title, subtitle string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "public, max-age=300")
		if wantsHTML(c) {
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			return c.SendString(assetView(eyebrow, title, subtitle, body))
		}
		c.Set(fiber.HeaderContentType, contentType)
		return c.Send(body)
	}
}
