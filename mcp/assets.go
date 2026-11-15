package mcp

import (
	_ "embed"
	"html"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// The agent-ecosystem assets are static files embedded into the single
// binary (契约 C: go:embed 静态文件). openapi.yaml covers every /api/*,
// /v1/*, /{engine}/search and health/stats endpoint; llms.txt is the
// agent-facing platform brief.
var (
	//go:embed openapi.yaml
	openapiSpec []byte

	//go:embed llms.txt
	llmsText []byte
)

// wantsHTML reports whether the caller is a human in a browser (Accept
// advertises text/html) and the raw variant was not explicitly requested
// via ?raw=1. Programmatic callers (Dify/Coze/curl/agents) never advertise
// text/html, so they keep receiving the raw bytes.
func wantsHTML(c *fiber.Ctx) bool {
	if c.Query("raw") == "1" {
		return false
	}
	return strings.Contains(c.Get(fiber.HeaderAccept), "text/html")
}

// assetView wraps the embedded file in a zero-dependency dark-window page
// (site 暗窗语言) so a browser click opens a readable web page instead of
// triggering a download (三期用户反馈:文档要网页浏览,不要下载)。
func assetView(title, subtitle, path string, body []byte) string {
	raw := html.EscapeString(path) + "?raw=1"
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + html.EscapeString(title) + ` · Web2API 网站收录查询开放平台</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;background:#0A0A0A;color:rgba(255,255,255,.5);font:14px/1.6 "Geist Mono","JetBrains Mono",ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
.wrap{max-width:1056px;margin:0 auto;padding:48px 24px 64px}
.head{display:flex;flex-wrap:wrap;gap:12px;align-items:baseline;justify-content:space-between;margin-bottom:8px}
h1{margin:0;font-size:22px;color:#fff;font-weight:600;letter-spacing:.2px}
.sub{margin:0 0 20px;color:rgba(255,255,255,.5)}
.bar{display:flex;gap:10px;margin-bottom:20px;flex-wrap:wrap}
.btn{display:inline-block;padding:8px 16px;border-radius:8px;border:.8px solid rgba(255,255,255,.14);background:rgba(255,255,255,.04);color:rgba(255,255,255,.72);font:500 13px/1.4 inherit;cursor:pointer;text-decoration:none;transition:background .15s ease,color .15s ease,border-color .15s ease}
.btn:hover{background:#5B8DEF;border-color:#5B8DEF;color:#0A0A0A}
pre{margin:0;background:#161615;border:.8px solid rgba(255,255,255,.06);border-radius:16px;padding:28px 24px;color:rgba(255,255,255,.72);font-size:13px;line-height:1.7;overflow:auto;max-height:72vh;white-space:pre;tab-size:2}
.foot{margin-top:16px;color:rgba(255,255,255,.35);font-size:12px}
.foot a{color:#5B8DEF;text-decoration:none}
@media(max-width:640px){.wrap{padding:24px 14px 40px}pre{padding:16px 12px}}
</style></head><body><div class="wrap">
<div class="head"><h1>` + html.EscapeString(title) + `</h1><div class="bar"><button class="btn" id="copyBtn" type="button">复制全文</button><a class="btn" href="` + raw + `">下载原始文件</a></div></div>
<p class="sub">` + html.EscapeString(subtitle) + ` · 程序化导入(Dify/Coze/curl)请用「下载原始文件」,或直接 GET 本地址且不带浏览器 Accept。</p>
<pre id="content">` + html.EscapeString(string(body)) + `</pre>
<p class="foot">Web2API 网站收录查询开放平台 · <a href="./">返回官网</a> · <a href="./console/">进入控制台</a></p>
</div>
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
func serveAsset(body []byte, contentType string, title, subtitle string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "public, max-age=300")
		if wantsHTML(c) {
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			return c.SendString(assetView(title, subtitle, c.Path(), body))
		}
		c.Set(fiber.HeaderContentType, contentType)
		return c.Send(body)
	}
}
