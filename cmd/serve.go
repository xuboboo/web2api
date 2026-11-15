package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/platform"
	"golang.org/x/time/rate"
)

// rawEngine implements SearchEngine interface for raw HTTP requests
type rawEngine struct {
	name string
	// searchFn overrides the registry lookup; nil in production. Test-only
	// injection point so raw-mode behavior is exercised without network.
	searchFn  func(ctx context.Context, q core.Query) ([]core.SearchResult, error)
	limiterMu sync.Mutex
	limiter   *rate.Limiter
}

func (r *rawEngine) rawSearch() (func(ctx context.Context, q core.Query) ([]core.SearchResult, error), bool) {
	if r.searchFn != nil {
		return r.searchFn, true
	}
	spec, ok := resolveEngineSpec(r.name)
	if !ok || spec.rawSearchFn == nil {
		return nil, false
	}
	return spec.rawSearchFn, true
}

func (r *rawEngine) Search(ctx context.Context, q core.Query) ([]core.SearchResult, error) {
	q.Insecure = config.Server.Insecure

	rawSearch, ok := r.rawSearch()
	if !ok {
		return nil, fmt.Errorf("unsupported engine: %s", r.name)
	}
	return rawSearch(ctx, q)
}

// CheckIndexed implements core.IndexChecker for raw mode: it pulls the raw
// SERP with the target URL itself as the query phrase through the engine's
// rawSearchFn. Candidate matching (core.EvaluateIndexSERP) and opaque
// wrapped-link resolution run afterwards in core's RunIndexCheckUnits
// orchestration, unchanged. Raw mode registers baidu/sogou/so360 only (bing
// has no raw channel), so a raw check_indexed request for bing is answered by
// SelectIndexCheckEngines with 400 INVALID_PARAM — this method never sees it.
// Parts that cannot be verified honestly surface as engine-level errors, never
// as a fake verdict.
func (r *rawEngine) CheckIndexed(ctx context.Context, targetURL string, q core.Query) ([]core.SearchResult, error) {
	rawSearch, ok := r.rawSearch()
	if !ok {
		return nil, fmt.Errorf("%w: %s has no raw search channel for index checks", core.ErrNotImplemented, r.name)
	}
	q.Insecure = config.Server.Insecure
	return rawSearch(ctx, core.IndexCheckQuery(targetURL, q))
}

func (r *rawEngine) Name() string {
	return r.name
}

func (r *rawEngine) IsInitialized() bool {
	return true
}

func (r *rawEngine) GetRateLimiter() *rate.Limiter {
	r.limiterMu.Lock()
	defer r.limiterMu.Unlock()
	if r.limiter == nil {
		// Use default rate limiter for raw requests.
		r.limiter = rate.NewLimiter(rate.Every(time.Second), 5)
	}
	return r.limiter
}

var serveCMD = &cobra.Command{
	Use:     "serve",
	Aliases: []string{"listen"},
	Short:   "Start HTTP server, to provide search engine results via API",
	Args:    cobra.MatchAll(cobra.NoArgs),
	RunE:    serve,
}

func serve(cmd *cobra.Command, args []string) error {
	corsCfg := core.DefaultCORSConfig()
	corsCfg.AllowOrigins = config.CORS.AllowOrigins
	corsCfg.AllowMethods = config.CORS.AllowMethods
	corsCfg.AllowHeaders = config.CORS.AllowHeaders
	corsCfg.MaxAge = config.CORS.MaxAge

	logSecurityPosture(corsCfg.AllowOrigins)

	captchaSolverEnabled, captchaSolverAPIKey, err := resolveCaptchaSolverConfig()
	if err != nil {
		return err
	}

	proxyRuntime := core.ProxyRuntimeBrowser
	if config.Server.IsRawRequests {
		proxyRuntime = core.ProxyRuntimeRaw
	}

	proxyCfg, err := buildNormalizedProxyConfig(proxyRuntime)
	if err != nil {
		return fmt.Errorf("invalid proxy configuration: %w", err)
	}

	fingerprintBrowserOpts := buildFingerprintBrowserOptions()

	if config.Server.IsRawRequests {
		logrus.Warn("Browserless results are very inconsistent or may not even work!")
		serverOpts := buildServerOptions(corsCfg, proxyCfg, fingerprintBrowserOpts)
		var rawEngines []core.SearchEngine
		for _, name := range enabledEngineNames("baidu", "sogou", "so360") {
			rawEngines = append(rawEngines, &rawEngine{name: name})
		}
		if len(rawEngines) == 0 {
			return fmt.Errorf("all search engines are disabled")
		}
		serv := core.NewServerWithOptions(config.Server.Host, config.Server.Port, serverOpts, rawEngines...)
		return startServerWithPlatform(serv, nil)
	}

	baseOpts := fingerprintBrowserOpts
	baseOpts.LeavePageOpen = config.App.IsLeaveHead
	baseOpts.CaptchaSolverEnabled = captchaSolverEnabled
	baseOpts.CaptchaSolverApiKey = captchaSolverAPIKey

	engines, closeBrowsers, err := buildBrowserEngines(baseOpts, proxyCfg)
	if err != nil {
		return err
	}

	serverOpts := buildServerOptions(corsCfg, proxyCfg, fingerprintBrowserOpts)
	serv := core.NewServerWithOptions(config.Server.Host, config.Server.Port, serverOpts, engines...)
	return startServerWithPlatform(serv, closeBrowsers)
}

// startServerWithPlatform mounts the platform API groups and the static panels
// (site + console) under the fixed core.BasePath, logs the site/console
// addresses and runs the graceful shutdown loop. closeResources (browser pool
// etc.) and the platform store are released together when the server drains.
func startServerWithPlatform(serv *core.Server, closeResources func() error) error {
	closePlat, err := mountPlatformAndPanel(serv)
	if err != nil {
		return err
	}
	onClose := func() error {
		var errs []error
		if closeResources != nil {
			errs = append(errs, closeResources())
		}
		if closePlat != nil {
			errs = append(errs, closePlat())
		}
		return errors.Join(errs...)
	}
	return listenWithGracefulShutdown(serv, onClose)
}

// mountPlatformAndPanel wires the running assembly onto the server's fiber app:
//
//  1. platform.enabled=true → platform.New(config.Platform, serv.EngineStatusSnapshot)
//     and the /api/* (admin) + /v1/* (public, index/check handler) groups rooted
//     at core.BasePath. The cooldown management endpoints (契约 A) ride
//     RegisterAdmin with the *core.Server injected as platform.EngineControl,
//     POST /api/playground/check is mounted behind the JWT guard, and the
//     security node (契约 B) is installed via SetSecurity;
//  2. the MCP agent plane (契约 C) rides mountMCP before the static mounts;
//  3. the static panels (三期接线): the marketing site (web/site) at
//     core.BasePath root and the admin console (web/admin) at
//     core.BasePath+"/console" (always mounted, even with the platform
//     disabled — only /api/* and /v1/* are gated by the switch).
//
// The platform closer is returned (nil when the platform is disabled).
func mountPlatformAndPanel(serv *core.Server) (func() error, error) {
	app := serv.FiberApp()

	var plat *platform.Platform
	if config.Platform.Enabled {
		var err error
		plat, err = platform.New(config.Platform, serv.EngineStatusSnapshot)
		if err != nil {
			return nil, fmt.Errorf("platform init: %w", err)
		}
		plat.SetSecurity(config.Security)
		plat.SetVersion(version)
		plat.SetAuditRotation(config.App.LogMaxMB, config.App.LogBackups)
		// B-06: /health.storage 探针字段(bbolt|json;平台关闭时省略)。
		serv.SetStorageKind(plat.StoreKind())
		dataPlaneAuth.Store(plat)
		// RegisterAdmin/RegisterPublic mount literal /api/* and /v1/* paths on
		// the given router, so the router must be rooted at BasePath (passing
		// a Group(BasePath+"/api") would double the /api segment).
		root := app.Group(core.BasePath)
		// serv (*core.Server) satisfies platform.EngineControl structurally:
		// EnginesStatusWithCooldown / ResetCooldown / ForceCooldown (契约 A).
		plat.RegisterAdmin(root, serv.IndexCheckHandler, serv)
		plat.RegisterPublic(root, serv.IndexCheckHandler)
		// Agent 平面(契约 C):POST /mcp + openapi.yaml + llms.txt,同样
		// 根植于 BasePath。必须在 mountStaticPanels 之前注册,静态通配不覆盖这些路由。
		mountMCP(app, serv, plat)
		logrus.WithFields(logrus.Fields{
			"base_path":   core.BasePath,
			"storage":     plat.StoreKind(),
			"data_dir":    config.Platform.DataDir,
			"admin_user":  config.Platform.AdminUsername,
			"public_api":  panelURL() + "v1",
			"admin_api":   panelURL() + "api",
			"site_url":    panelURL(),
			"console_url": consoleURL(),
		}).Info("收录查询平台已挂载: 官网与 API 统一部署在 " + core.BasePath + " 前缀之下, 官网地址: " + panelURL() + ", 控制台地址: " + consoleURL())
	}

	// 公开服务器状态(官网「服务器状态」看板数据源):无鉴权,仅非敏感字段
	// (引擎名 / 状态 / 冷却时间窗 / 连续拦截数),5 秒缓存头防刷。
	// A-11:注册移出 platform.Enabled 分支——platform.enabled=false 时引擎仍在
	// 运行,官网看板与状态页照常可用(与原注释承诺一致);本端点只依赖
	// serv.EnginesStatusWithCooldown,不需要 platform 实例。
	app.Get(core.BasePath+"/engines/status", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "public, max-age=5")
		return c.JSON(fiber.Map{
			"engines":      serv.EnginesStatusWithCooldown(),
			"generated_at": time.Now().Format("2006-01-02 15:04:05"),
		})
	})

	mountStaticPanels(app)

	if plat == nil {
		return nil, nil
	}
	return plat.Close, nil
}

// dataPlaneAuth holds the platform for the optional data-plane auth switch
// (security.data_plane_auth). It is populated by mountPlatformAndPanel once
// the platform is up; requests arriving before that (or with the platform
// disabled) answer 503 auth_unavailable.
var dataPlaneAuth atomic.Pointer[platform.Platform]

// dataPlaneAuthGuard is the RequestAuth validator core applies to the
// dedicated /{engine}/search and /stats/* routes when the switch is on.
// Validator contract: return nil to allow, an error to reject (no c.Next()).
func dataPlaneAuthGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		plat := dataPlaneAuth.Load()
		if plat == nil {
			return &core.APIError{
				HTTPStatus: fiber.StatusServiceUnavailable,
				ErrorCode:  "auth_unavailable",
				Message:    "security.data_plane_auth is enabled but the platform auth backend is unavailable",
			}
		}
		if !plat.ValidateDataPlaneKey(c) {
			return &core.APIError{
				HTTPStatus: fiber.StatusUnauthorized,
				ErrorCode:  "unauthorized",
				Message:    "a valid X-API-Key is required (security.data_plane_auth is enabled)",
			}
		}
		return nil
	}
}

// panelURL renders the externally reachable site root (the marketing site is
// served at the BasePath root), e.g. http://127.0.0.1:7000/web-tool/. Wildcard
// bind hosts are shown as 127.0.0.1.
func panelURL() string {
	host := strings.TrimSpace(config.Server.Host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d%s/", host, config.Server.Port, core.BasePath)
}

// consoleURL renders the externally reachable admin console root, e.g.
// http://127.0.0.1:7000/web-tool/console/.
func consoleURL() string {
	return panelURL() + "console/"
}

// siteDirCandidates lists where the bundled marketing site may live, relative
// to the working directory first, then to the executable. "../web/site" covers
// `go test ./cmd` which runs with the package dir as cwd.
var siteDirCandidates = []string{
	filepath.Join("web", "site"),
	filepath.Join("..", "web", "site"),
	filepath.Join(filepath.Dir(os.Args[0]), "web", "site"),
}

// adminPanelDirCandidates lists where the bundled admin panel may live,
// relative to the working directory first, then to the executable. "../web/admin"
// covers `go test ./cmd` which runs with the package dir as cwd.
var adminPanelDirCandidates = []string{
	filepath.Join("web", "admin"),
	filepath.Join("..", "web", "admin"),
	filepath.Join(filepath.Dir(os.Args[0]), "web", "admin"),
}

// findStaticIndexDir returns the first candidate directory that contains an
// index.html, or "" when none does.
func findStaticIndexDir(candidates []string) string {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// mountStaticPanels serves the two static panels (三期接线) under BasePath:
//
//	GET <BasePath>/console          → 301 <BasePath>/console/  (legacy bookmark)
//	<BasePath>/console/*            → web/admin (管理控制台, assets 相对引用解析到 console/assets/*)
//	GET <BasePath>/                 → web/site index.html (官网)
//	<BasePath>/assets/*             → web/site assets
//
// Registration order is load-bearing with fiber v2: core API routes (built at
// server construction), the platform /api/* + /v1/* groups and the MCP agent
// plane are all registered BEFORE this call, so they win over the static
// wildcards; console routes are registered before the site wildcard so the
// site mount cannot swallow /console/* paths.
func mountStaticPanels(app *fiber.App) {
	// (1) Legacy no-slash console bookmark → canonical /console/. fiber's
	// StrictRouting=false strips the trailing slash from the detection path,
	// so this exact route ALSO matches <BasePath>/console/ — the handler must
	// redirect only the bare form and fall through to the static route
	// otherwise (otherwise the canonical URL would redirect to itself forever).
	app.Get(core.BasePath+"/console", func(c *fiber.Ctx) error {
		if c.Path() != core.BasePath+"/console" {
			return c.Next()
		}
		return c.Redirect(core.BasePath+"/console/", fiber.StatusMovedPermanently)
	})

	// (2) Admin console (web/admin): GET <BasePath>/console/ serves index.html,
	// <BasePath>/console/assets/* serves the bundled resources.
	adminDir := findStaticIndexDir(adminPanelDirCandidates)
	if adminDir == "" {
		logrus.Warn("管理控制台静态资源缺失(web/admin/index.html 未找到)," + core.BasePath + "/console/ 将返回 404;API 不受影响")
	} else {
		app.Static(core.BasePath+"/console", adminDir)
		logrus.WithFields(logrus.Fields{
			"url": consoleURL(),
			"dir": adminDir,
		}).Info("管理控制台已挂载: " + consoleURL())
	}

	// (3) Marketing site (web/site) at the BasePath root — the entry users see
	// first. Registered LAST so every earlier route (API/MCP/console) wins.
	siteDir := findStaticIndexDir(siteDirCandidates)
	if siteDir == "" {
		logrus.Warn("官网静态资源缺失(web/site/index.html 未找到)," + core.BasePath + "/ 将返回 404;API 不受影响")
		return
	}
	app.Static(core.BasePath, siteDir)
	logrus.WithFields(logrus.Fields{
		"url": panelURL(),
		"dir": siteDir,
	}).Info("官网已挂载: " + panelURL())

	// (4) A-13 品牌化 404 兜底:注册在静态挂载之后,静态命中优先。
	// 用 app.All 而非 app.Get:否则 GET 通配会让未注册方法的请求(如平台关闭时
	// 的 POST /api/auth/login)从 404 变成 405(fiber 按路径命中他法路由)。
	// HTML 请求(且路径不含 API 前缀)返回官网 404 页;API 路径与其余情形
	// 返回 fiber.ErrNotFound,由 JSONErrorMiddleware 按现状输出 JSON 404
	// (API 语义不变、不泄漏内部信息)。
	notFoundPage := filepath.Join(siteDir, "404.html")
	if info, err := os.Stat(notFoundPage); err == nil && !info.IsDir() {
		app.All(core.BasePath+"/*", func(c *fiber.Ctx) error {
			p := c.Path()
			for _, prefix := range []string{"/api/", "/v1/", "/mcp", "/engines/"} {
				if strings.Contains(p, prefix) {
					return fiber.ErrNotFound
				}
			}
			if c.Accepts("html", "json") == "html" {
				return c.Status(fiber.StatusNotFound).SendFile(notFoundPage)
			}
			return fiber.ErrNotFound
		})
	} else {
		logrus.Warn("官网 404 页缺失(web/site/404.html 未找到)," + core.BasePath + "/* 未命中路径将返回 JSON 404")
	}

	// (5) A-14 根级 robots.txt / sitemap.xml:BasePath 挂载下 /web-tool/robots.txt
	// 由上面的 app.Static 自动覆盖;根路径 /robots.txt、/sitemap.xml 需在
	// BasePath 之外单独注册,读同一份文件。部署依赖(如实声明):反向代理必须
	// 把根路径转发到本实例——若代理只转发 /web-tool/*,根级文件不可达;sitemap
	// <loc> 与 og:url 同批依赖生产域名(web/site/sitemap.xml 内为占位符)。
	registerRootSEOFile(app, "/robots.txt", filepath.Join(siteDir, "robots.txt"))
	registerRootSEOFile(app, "/sitemap.xml", filepath.Join(siteDir, "sitemap.xml"))
}

// registerRootSEOFile serves one static SEO file at the fiber app root (outside
// core.BasePath) from the marketing site directory. Registration is skipped
// when the file does not exist.
func registerRootSEOFile(app *fiber.App, route, file string) {
	if info, err := os.Stat(file); err != nil || info.IsDir() {
		return
	}
	app.Get(route, func(c *fiber.Ctx) error {
		return c.SendFile(file)
	})
}

func buildFingerprintBrowserOptions() core.BrowserOpts {
	blockedResourceTypes := core.MustParseBlockedResourceTypes(config.App.BlockResources)

	opts := core.BrowserOpts{
		IsHeadless:         !config.App.IsBrowserHead,
		IsLeakless:         config.App.IsLeakless,
		Timeout:            time.Second * time.Duration(config.App.Timeout),
		BrowserPath:        config.App.BrowserPath,
		Insecure:           config.Server.Insecure,
		BlockResourceTypes: blockedResourceTypes,
		BlockTrackers:      config.App.BlockTrackers,
	}
	if config.Server.IsDebug {
		opts.IsHeadless = false
	}
	return opts
}

// cspInlineScriptHashes lists the sha256 hashes of every inline <script> this
// binary serves outside the admin console (审计 A-19:哈希法,无需 unsafe-inline).
// 已核实的内联脚本面(改动任一脚本内容必须同步更新哈希):
//   1. 官网/状态页/404 页共用的主题预置脚本(web/site/index.html、
//      web/site/status/index.html、web/site/404.html,三处逐字一致);
//   2. 状态页引擎看板脚本(web/site/status/index.html);
//   3. openapi.yaml / llms.txt 浏览器视图的复制脚本(mcp/assets.go assetView)。
// 控制台(web/admin)的内联脚本面未逐项核验,对其只下发 Report-Only,不阻断。
const cspInlineScriptHashes = "'sha256-FneMDt+RQl3mtgKESJPgeSkA7sAw8Abzx8e9WL0SZvg=' " +
	"'sha256-AGFaINW4LivGF38SmZC0cM28VIFAtmBOYt+kzvJ714k=' " +
	"'sha256-QMScITwwri5Sy12UZQ78xHdQQtmsA+TNYyrbbubwJxg='"

// contentSecurityPolicy is the site-wide CSP value. style-src keeps
// 'unsafe-inline' because the status/404 pages and the asset viewer ship
// inline <style> blocks (零构建契约,无外链化改造空间).
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' " + cspInlineScriptHashes + "; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// securityHeadersHandler sets the site-wide security response headers on every
// response (审计 A-19). S4-B-12: the console CSP graduates from Report-Only to
// enforcement — the theme pre-boot script was externalized to
// assets/theme-boot.js (S3), so the console no longer ships inline scripts.
// S4-B-14: when security.trust_proxy_headers=true and the request arrived via
// https (X-Forwarded-Proto), Strict-Transport-Security is issued — only for
// TLS-terminating reverse-proxy deployments, never for plain HTTP.
func securityHeadersHandler() fiber.Handler {
	trustProxy := config.Security.TrustProxyHeaders
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
		c.Set(fiber.HeaderReferrerPolicy, "strict-origin-when-cross-origin")
		c.Set(fiber.HeaderXFrameOptions, "DENY")
		if trustProxy && strings.EqualFold(strings.TrimSpace(c.Get("X-Forwarded-Proto")), "https") {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Set("Content-Security-Policy", contentSecurityPolicy)
		return c.Next()
	}
}

func buildServerOptions(corsCfg core.CORSConfig, proxyCfg core.ProxyConfig, fingerprintBrowserOpts core.BrowserOpts) core.ServerOptions {
	retryCfg := core.RetryConfig{
		MaxRetries:     config.Resilience.MaxRetries,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
	}
	engineTimeout := time.Duration(config.App.Timeout) * time.Second

	opts := core.ServerOptions{
		CacheTTL:               time.Duration(config.Cache.TTLSeconds) * time.Second,
		CacheMaxSize:           config.Cache.MaxSize,
		IndexCheckCacheTTL:     time.Duration(config.Cache.IndexCheckTTLSeconds) * time.Second,
		EnableCORS:             config.CORS.Enabled,
		CORS:                   corsCfg,
		MetricsToken:           config.Security.MetricsToken,
		AllowEndpointFallback:  config.Resilience.AllowEndpointFallback,
		EnableDebugEndpoints:   config.App.DebugEndpoints,
		FingerprintArtifactDir: core.DefaultFingerprintArtifactDir,
		FingerprintBrowserOpts: fingerprintBrowserOpts,
		RequestTimeout:         core.RequestTimeoutForRetries(engineTimeout, retryCfg),
		ResolveConfig:          core.DefaultURLResolveConfig(),
		ExtraMiddleware:        []fiber.Handler{securityHeadersHandler()},
		Resilience: core.ResilientConfig{
			Retry: retryCfg,
			CircuitBreaker: core.CircuitBreakerConfig{
				FailureThreshold: config.CircuitBreaker.Failures,
				RecoveryTimeout:  time.Duration(config.CircuitBreaker.RecoverySeconds) * time.Second,
				SuccessThreshold: config.CircuitBreaker.Successes,
			},
			Proxy: proxyCfg,
			// 契约 A: per-engine IP cooldown state machine knobs.
			Cooldown: config.Stability.EngineCooldown,
		},
	}
	// 数据面可选鉴权(契约): security.data_plane_auth=true 时 dedicated
	// /{engine}/search 与 /stats/* 需要 X-API-Key。默认关闭,行为与现状一致。
	if config.Security.DataPlaneAuth {
		opts.RequestAuth = dataPlaneAuthGuard()
	}
	return opts
}

const gracefulShutdownTimeout = 30 * time.Second

// logSecurityPosture prints the startup SECURITY posture (S4-B-03 出厂安全
// 姿态): one INFO line summarizing the effective posture, plus a per-condition
// SECURITY banner — 非 loopback 绑定 / 数据面无鉴权 / 允许自带代理 URL 任一即列.
// Code defaults stay compatible with existing deployments; the shipped
// config template carries the safe defaults (B-03), so a fresh deploy that
// keeps the template is clean out of the box.
func logSecurityPosture(allowOrigins string) {
	corsDisplay := strings.TrimSpace(allowOrigins)
	if corsDisplay == "" {
		corsDisplay = "*"
	}
	logrus.WithFields(logrus.Fields{
		"host":                          config.Server.Host,
		"port":                          config.Server.Port,
		"data_plane_auth":               config.Security.DataPlaneAuth,
		"cors_allow_origins":            corsDisplay,
		"allow_request_proxy_url":       config.Proxies.AllowRequestProxyURL,
		"trust_proxy_headers":           config.Security.TrustProxyHeaders,
		"api_rate_per_minute":           config.Security.APIRatePerMinute,
		"unauth_rate_per_minute":        config.Security.UnauthRatePerMinute,
		"admin_rate_per_minute":         config.Security.AdminRatePerMinute,
		"admin_rate_per_ip":             config.Security.AdminRatePerIP,
	}).Info("SECURITY POSTURE: " + fmt.Sprintf("http://%s:%d%s (数据面鉴权=%v, CORS=%s, 自带代理=%v)",
		config.Server.Host, config.Server.Port, core.BasePath,
		config.Security.DataPlaneAuth, corsDisplay, config.Proxies.AllowRequestProxyURL))

	if !isLoopbackHost(config.Server.Host) {
		logrus.Warn("SECURITY: 服务绑定在非 loopback 地址(" + config.Server.Host + ") — API 可被网络内任意主机访问;单机部署请改绑 127.0.0.1,或在前置反代上限制来源")
	}
	if !config.Security.DataPlaneAuth {
		logrus.Warn("SECURITY: 数据面鉴权未开启(security.data_plane_auth=false)—— /{engine}/search 与 /stats/* 对任何来源匿名可用,浏览器可达的算力放大器;生产环境请开启数据面鉴权")
	}
	if config.Proxies.AllowRequestProxyURL {
		logrus.Warn("SECURITY: 允许调用方自带代理 URL(proxies.allow_request_proxy_url=true)—— 调用方可驱使本机浏览器经任意代理发起请求;非必要请关闭")
	}
	// 契约 B: shout about the shipped default password on any non-loopback
	// bind — it is the only thing standing between the panel and the internet.
	if config.Platform.Enabled && config.Platform.AdminPassword == "admin123" && !isLoopbackHost(config.Server.Host) {
		logrus.Warn("SECURITY: 平台管理员密码仍为默认 admin123 且服务绑定在非 loopback 地址,请立即通过 config.platform.admin_password 或面板「系统设置」修改")
	}
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	// "" binds all interfaces and 0.0.0.0 is not loopback — both stay exposed.
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// listenWithGracefulShutdown runs the serve loop and drains traffic on the
// first stop signal. T-01(Windows 退出码): 排水成功后显式 os.Exit(0)——Windows
// 控制台以 CTRL_C/CTRL_BREAK 投递停止信号时,进程此前以 0xC000013A
// (STATUS_CONTROL_C_EXIT)收尾而非 0;signal.Notify 只保证排水流程执行,控制台
// 默认处理仍可能把退出码带偏。另注:syscall.SIGTERM 的注册仅在 Unix 可投递,
// Windows 停机请用控制台事件(Ctrl+C / taskkill 无 /F 触发 CTRL_BREAK)或服务
// 包装器(见 docs/CONFIGURATION.md 运维注记)。
func listenWithGracefulShutdown(serv *core.Server, onShutdown func() error) (err error) {
	listenErrCh := make(chan error, 1)
	go func() {
		listenErrCh <- serv.Listen()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-listenErrCh:
		// Listen itself failed (port busy etc.): release resources and surface
		// the listen error — exit code stays non-zero via the caller.
		if onShutdown != nil {
			err = errors.Join(err, onShutdown())
		}
		return err
	case sig := <-sigCh:
		logrus.WithField("signal", sig.String()).Info("Shutdown signal received, draining traffic")
	}

	serv.SetDraining(true)

	shutdownErr := serv.ShutdownWithTimeout(gracefulShutdownTimeout)
	if isServerNotRunningError(shutdownErr) {
		shutdownErr = nil
	}

	if listenErr := waitForListenExit(listenErrCh); listenErr != nil && !isExpectedListenShutdownError(listenErr) {
		shutdownErr = errors.Join(shutdownErr, listenErr)
	}

	if onShutdown != nil {
		shutdownErr = errors.Join(shutdownErr, onShutdown())
	}

	if shutdownErr != nil {
		return shutdownErr
	}

	// 排水、日志/存储关闭全部完成后统一退出码为 0(Windows: 显式退出绕开
	// 控制台事件默认处理的 STATUS_CONTROL_C_EXIT 收尾)。
	os.Exit(0)
	return nil
}

func waitForListenExit(listenErrCh <-chan error) error {
	select {
	case err := <-listenErrCh:
		return err
	case <-time.After(time.Second):
		return nil
	}
}

func isExpectedListenShutdownError(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "server closed") ||
		strings.Contains(msg, "closed network connection")
}

func isServerNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "server is not running")
}

// pooledBrowser is one Chrome process in the pool, dedicated to a single proxy
// auth identity (or to the shared no-auth/unauth path when launchProxyURL=="").
type pooledBrowser struct {
	browser        *core.Browser
	launchProxyURL string
	lastUsedAt     time.Time
}

// browserPool keeps a bounded set of Chrome processes keyed by proxy auth
// identity (scheme+host+port+username). Each entry was launched with its own
// `l.Proxy(...)` so Chrome handles 407 natively for the main document AND all
// subresources. Direct and unauthenticated proxies share one entry whose Chrome
// was launched without a process-level proxy; per-BrowserContext ProxyServer is
// applied at request time for unauthenticated request-URL proxies.
type browserPool struct {
	mu        sync.Mutex
	base      core.BrowserOpts
	laneStore *core.LaneStore

	maxProcesses int
	idleTTL      time.Duration

	browsers map[string]*pooledBrowser

	evictedLRU  int
	evictedIdle int

	stopSweeper chan struct{}
	sweeperDone chan struct{}
}

const directBrowserKey = "direct"

func newBrowserPool(base core.BrowserOpts, defaultLaunchProxyURL string, laneStore *core.LaneStore, maxProcesses int, idleTTL time.Duration) *browserPool {
	base.ProxyLaneStore = laneStore
	if maxProcesses <= 0 {
		maxProcesses = 4
	}
	pool := &browserPool{
		base:         base,
		laneStore:    laneStore,
		maxProcesses: maxProcesses,
		idleTTL:      idleTTL,
		browsers:     map[string]*pooledBrowser{},
		stopSweeper:  make(chan struct{}),
		sweeperDone:  make(chan struct{}),
	}
	if launchURL := strings.TrimSpace(defaultLaunchProxyURL); launchURL != "" {
		if key := browserPoolKey(launchURL); key != directBrowserKey {
			pool.browsers[key] = &pooledBrowser{
				launchProxyURL: launchURL,
				lastUsedAt:     time.Now(),
			}
		}
	}
	if idleTTL > 0 {
		go pool.sweepIdle()
	} else {
		close(pool.sweeperDone)
	}
	return pool
}

// browserPoolKey derives the pool key from a request's proxy URL. Authenticated
// HTTP/HTTPS proxies get their own Chrome keyed by scheme+host+port+username
// plus a short auth hash, so provider session tokens do not share one Chrome.
// Empty/unauthenticated/SOCKS request URLs fall through to the shared
// "direct" Chrome.
func browserPoolKey(requestProxyURL string) string {
	requestProxyURL = strings.TrimSpace(requestProxyURL)
	if requestProxyURL == "" {
		return directBrowserKey
	}
	normalized, err := core.NormalizeProxyURL(requestProxyURL)
	if err != nil || normalized == "" {
		return directBrowserKey
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return directBrowserKey
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		// Authenticated SOCKS is rejected upstream; unauthenticated SOCKS goes
		// through the per-context proxy path on the shared Chrome.
		return directBrowserKey
	}
	if parsed.User == nil {
		return directBrowserKey
	}
	// Hash the full userinfo so a rotating password gets its own Chrome without
	// leaking credentials into the key. Scheme+host+username stay readable.
	sum := sha256.Sum256([]byte(parsed.User.String()))
	return fmt.Sprintf("%s|%s|%s|%s", parsed.Scheme, parsed.Host, parsed.User.Username(), hex.EncodeToString(sum[:])[:16])
}

// browserLaunchURL returns the URL to pass to launcher.Proxy for a given
// request URL, or "" when the launch should be unproxied (direct + unauth).
func browserLaunchURL(requestProxyURL string) string {
	requestProxyURL = strings.TrimSpace(requestProxyURL)
	if requestProxyURL == "" {
		return ""
	}
	normalized, err := core.NormalizeProxyURL(requestProxyURL)
	if err != nil || normalized == "" {
		return ""
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if parsed.User == nil {
		return ""
	}
	return normalized
}

// get returns a Chrome that can route the supplied request URL. For
// authenticated HTTP(S) proxies it returns the dedicated Chrome (launching one
// if needed). For everything else it returns the shared "direct" Chrome.
func (p *browserPool) get(requestProxyURL string) (*core.Browser, error) {
	key := browserPoolKey(requestProxyURL)
	launchURL := ""
	if key != directBrowserKey {
		launchURL = browserLaunchURL(requestProxyURL)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.browsers[key]; ok && entry.browser != nil {
		entry.lastUsedAt = time.Now()
		return entry.browser, nil
	}

	// Use any pre-bound launchProxyURL on the existing entry (e.g. legacy
	// global proxy) when the caller didn't supply one.
	if entry, ok := p.browsers[key]; ok && entry.browser == nil {
		if launchURL == "" {
			launchURL = entry.launchProxyURL
		}
	}

	opts := p.base
	opts.ProxyURL = launchURL
	browser, err := core.NewBrowser(opts)
	if err != nil {
		return nil, err
	}

	p.browsers[key] = &pooledBrowser{
		browser:        browser,
		launchProxyURL: launchURL,
		lastUsedAt:     time.Now(),
	}
	p.evictLRULocked()
	return browser, nil
}

func (p *browserPool) evictLRULocked() {
	for len(p.browsers) > p.maxProcesses {
		var (
			oldestKey string
			oldest    time.Time
			found     bool
		)
		for key, entry := range p.browsers {
			if !found || entry.lastUsedAt.Before(oldest) {
				oldestKey = key
				oldest = entry.lastUsedAt
				found = true
			}
		}
		if !found {
			return
		}
		entry := p.browsers[oldestKey]
		delete(p.browsers, oldestKey)
		p.evictedLRU++
		go closePooledBrowser(entry, "lru")
	}
}

func (p *browserPool) sweepIdle() {
	defer close(p.sweeperDone)
	interval := p.idleTTL / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopSweeper:
			return
		case now := <-ticker.C:
			p.mu.Lock()
			for key, entry := range p.browsers {
				if entry.browser == nil {
					continue
				}
				if now.Sub(entry.lastUsedAt) < p.idleTTL {
					continue
				}
				delete(p.browsers, key)
				p.evictedIdle++
				go closePooledBrowser(entry, "idle")
			}
			p.mu.Unlock()
		}
	}
}

func closePooledBrowser(entry *pooledBrowser, reason string) {
	if entry == nil || entry.browser == nil {
		return
	}
	if err := entry.browser.Close(); err != nil {
		logrus.WithError(err).WithField("evict_reason", reason).Debug("Browser pool: close evicted browser failed")
	}
}

func (p *browserPool) dropLaneCookies(ctx context.Context, engineName string, q core.Query) {
	if p == nil || p.laneStore == nil {
		return
	}
	laneKey := core.ProxyLaneKeyForTenant(engineName, core.TenantFromContext(ctx), q, q.ProxyURL)
	p.laneStore.DropCookies(laneKey)
}

func (p *browserPool) laneStats() core.LaneStats {
	if p == nil || p.laneStore == nil {
		return core.LaneStats{}
	}
	return p.laneStore.Stats()
}

func (p *browserPool) browserStats() core.BrowserPoolStats {
	if p == nil {
		return core.BrowserPoolStats{}
	}
	p.mu.Lock()
	active := 0
	for _, entry := range p.browsers {
		if entry.browser != nil {
			active++
		}
	}
	stats := core.BrowserPoolStats{
		Active:      active,
		Max:         p.maxProcesses,
		EvictedLRU:  p.evictedLRU,
		EvictedIdle: p.evictedIdle,
	}
	p.mu.Unlock()
	return stats
}

func (p *browserPool) close() error {
	if p == nil {
		return nil
	}
	close(p.stopSweeper)
	<-p.sweeperDone

	p.mu.Lock()
	entries := make([]*pooledBrowser, 0, len(p.browsers))
	for key, entry := range p.browsers {
		entries = append(entries, entry)
		delete(p.browsers, key)
	}
	p.mu.Unlock()

	var closeErr error
	for _, entry := range entries {
		if entry == nil || entry.browser == nil {
			continue
		}
		if err := entry.browser.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

type pooledBrowserEngine struct {
	name    string
	opts    core.SearchEngineOptions
	factory func(core.Browser, core.SearchEngineOptions) core.SearchEngine
	pool    *browserPool

	reportLaneStats bool
}

func (e *pooledBrowserEngine) Search(ctx context.Context, q core.Query) ([]core.SearchResult, error) {
	engine, err := e.resolveEngine(q)
	if err != nil {
		return nil, err
	}
	return engine.Search(ctx, q)
}

// CheckIndexed forwards core.IndexChecker to the engine resolved from the pool
// (the wrapper itself is replaced per browser, so the capability check must
// happen on the inner engine). Without this forwarding, /v1/index/check would
// answer not_implemented for every browser-mode engine.
func (e *pooledBrowserEngine) CheckIndexed(ctx context.Context, targetURL string, q core.Query) ([]core.SearchResult, error) {
	engine, err := e.resolveEngine(q)
	if err != nil {
		return nil, err
	}
	checker, ok := engine.(core.IndexChecker)
	if !ok {
		return nil, fmt.Errorf("%w: %s does not support index checks", core.ErrNotImplemented, engine.Name())
	}
	return checker.CheckIndexed(ctx, targetURL, q)
}

func (e *pooledBrowserEngine) IsInitialized() bool {
	return true
}

func (e *pooledBrowserEngine) Name() string {
	return e.name
}

func (e *pooledBrowserEngine) GetRateLimiter() *rate.Limiter {
	return e.opts.GetRateLimiter()
}

func (e *pooledBrowserEngine) DropProxyLaneCookies(ctx context.Context, q core.Query) {
	e.pool.dropLaneCookies(ctx, e.name, q)
}

func (e *pooledBrowserEngine) ProxyLaneStats() core.LaneStats {
	if !e.reportLaneStats {
		return core.LaneStats{}
	}
	return e.pool.laneStats()
}

func (e *pooledBrowserEngine) BrowserPoolStats() core.BrowserPoolStats {
	if !e.reportLaneStats {
		return core.BrowserPoolStats{}
	}
	return e.pool.browserStats()
}

// Resolve per call because eviction can replace the Browser behind a pool key.
func (e *pooledBrowserEngine) resolveEngine(q core.Query) (core.SearchEngine, error) {
	browser, err := e.pool.get(q.ProxyURL)
	if err != nil {
		return nil, err
	}
	return e.factory(*browser, e.opts), nil
}

type browserEngineSpec struct {
	name    string
	opts    core.SearchEngineOptions
	factory func(core.Browser, core.SearchEngineOptions) core.SearchEngine
}

func browserEngineSpecs() []browserEngineSpec {
	specs := engineSpecs()
	out := make([]browserEngineSpec, 0, len(specs))
	for _, s := range specs {
		if s.disabled() {
			logrus.Infof("Engine %s is disabled in config", s.name)
			continue
		}
		out = append(out, browserEngineSpec{
			name:    s.name,
			opts:    s.opts(),
			factory: s.factory,
		})
	}
	return out
}

func buildBrowserEngines(baseOpts core.BrowserOpts, proxyCfg core.ProxyConfig) ([]core.SearchEngine, func() error, error) {
	specs := browserEngineSpecs()
	if len(specs) == 0 {
		return nil, nil, fmt.Errorf("all search engines are disabled")
	}
	// Validate before the pool starts its sweeper, so failed startup leaks nothing.
	for _, spec := range specs {
		policy := resolveEngineProxyPolicy(proxyCfg, spec.name)
		if err := validateBrowserProxyPolicy(proxyCfg, policy); err != nil {
			return nil, nil, fmt.Errorf("browser proxy validation failed for engine %s: %w", spec.name, err)
		}
	}
	launchProxyURL := ""
	if strings.TrimSpace(proxyCfg.Proxies.Global) != "" && !proxyCfg.Proxies.AllowRequestProxyURL {
		launchProxyURL = proxyCfg.Proxies.Global
	}
	var laneStore *core.LaneStore
	if proxyCfg.Proxies.Lanes.Enabled {
		laneStore = core.NewLaneStore(proxyCfg.Proxies.Lanes.MaxLanes)
	}
	pool := newBrowserPool(baseOpts, launchProxyURL, laneStore, config.App.MaxProcesses, config.App.IdleTTL)

	engines := make([]core.SearchEngine, 0, len(specs))
	for idx, spec := range specs {
		opts := spec.opts
		opts.Init()
		base := &pooledBrowserEngine{
			name:            spec.name,
			opts:            opts,
			factory:         spec.factory,
			pool:            pool,
			reportLaneStats: idx == 0,
		}
		engines = append(engines, base)
	}

	return engines, pool.close, nil
}

func validateBrowserProxyPolicy(proxyCfg core.ProxyConfig, policy core.ProxyPolicy) error {
	if policy.Mode != core.ProxyModeTagPool {
		return nil
	}

	proxyURL := strings.TrimSpace(proxyCfg.Proxies.Global)
	if proxyURL != "" {
		return validateBrowserProxyURL(proxyURL)
	}

	for _, entry := range proxyCfg.Proxies.Entries {
		if !entryHasTag(entry, policy.Tag) {
			continue
		}
		if err := validateBrowserProxyURL(entry.URL); err != nil {
			return err
		}
	}

	return nil
}

func validateBrowserProxyURL(proxyURL string) error {
	// Browser startup must stop immediately on authenticated SOCKS because Chrome
	// cannot use that proxy shape reliably and retrying a different proxy hides the misconfiguration.
	if core.IsAuthenticatedSocksProxyURL(proxyURL) {
		return fmt.Errorf(
			"%w: browser runtime does not support authenticated SOCKS proxy %s",
			core.ErrProxyUnavailable,
			core.MaskProxyURL(proxyURL),
		)
	}
	return nil
}

func entryHasTag(entry core.ProxyEntryConfig, tag string) bool {
	tag = strings.TrimSpace(strings.ToLower(tag))
	if tag == "" {
		return false
	}
	for _, entryTag := range entry.Tags {
		if strings.TrimSpace(strings.ToLower(entryTag)) == tag {
			return true
		}
	}
	return false
}

func init() {
	RootCmd.AddCommand(serveCMD)
}

// enabledEngineNames drops the engines whose <engine>.disabled is set.
func enabledEngineNames(names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if spec, ok := resolveEngineSpec(name); ok && spec.disabled() {
			logrus.Infof("Engine %s is disabled in config", name)
			continue
		}
		out = append(out, name)
	}
	return out
}
