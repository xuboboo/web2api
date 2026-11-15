package cmd

import (
	"context"

	"github.com/xuboboo/web2api/baidu"
	"github.com/xuboboo/web2api/bing"
	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/so360"
	"github.com/xuboboo/web2api/sogou"
)

// engineSpec is the single registry row for a search engine, driving CLI search,
// raw dispatch, serve's browserEngineSpecs, and the alias/validation strings.
// cfg points into the live config global; rawSearchFn is nil when an engine has
// no browserless mode.
type engineSpec struct {
	name        string
	aliases     []string
	factory     func(core.Browser, core.SearchEngineOptions) core.SearchEngine
	rawSearchFn func(context.Context, core.Query) ([]core.SearchResult, error)
	cfg         *EngineConfig
}

// disabled reports whether <engine>.disabled leaves the engine out of serve.
func (s engineSpec) disabled() bool {
	return s.cfg.Disabled
}

func (s engineSpec) opts() core.SearchEngineOptions {
	return s.cfg.SearchEngineOptions
}

func engineSpecs() []engineSpec {
	return []engineSpec{
		{name: "baidu", factory: newEngine(baidu.New), rawSearchFn: baidu.SearchRaw, cfg: &config.BaiduConfig},
		{name: "bing", factory: newBing, cfg: &config.BingConfig.EngineConfig},
		// Mainland-China engines: a raw HTTP channel exists (rawSearchFn), but
		// their SERPs hydrate result cards client-side, so raw output is
		// inconsistent and may not even work — serve --raw registers them with
		// that caveat, while bing stays browser-only (no rawSearchFn).
		{name: "sogou", factory: newEngine(sogou.New), rawSearchFn: sogou.SearchRaw, cfg: &config.SogouConfig},
		{name: "so360", aliases: []string{"360"}, factory: newEngine(so360.New), rawSearchFn: so360.SearchRaw, cfg: &config.So360Config},
	}
}

// newEngine adapts a concrete pkg.New (returning *Engine) to the
// core.SearchEngine-typed factory the registry stores.
func newEngine[T core.SearchEngine](ctor func(core.Browser, core.SearchEngineOptions) T) func(core.Browser, core.SearchEngineOptions) core.SearchEngine {
	return func(b core.Browser, o core.SearchEngineOptions) core.SearchEngine {
		return ctor(b, o)
	}
}

// newBing is newEngine(bing.New) plus bing.search_box.
func newBing(b core.Browser, o core.SearchEngineOptions) core.SearchEngine {
	engine := bing.New(b, o)
	engine.SearchBox = config.BingConfig.SearchBox
	return engine
}

// engineValidArgs returns every accepted engine token (canonical names +
// aliases) for cobra's OnlyValidArgs validation.
func engineValidArgs() []string {
	specs := engineSpecs()
	args := make([]string, 0, len(specs))
	for _, s := range specs {
		args = append(args, s.name)
		args = append(args, s.aliases...)
	}
	return args
}

// resolveEngineSpec returns the spec whose canonical name or alias matches raw
// (case/space already normalized by the caller), or false when unknown.
func resolveEngineSpec(raw string) (engineSpec, bool) {
	for _, s := range engineSpecs() {
		if s.name == raw {
			return s, true
		}
		for _, alias := range s.aliases {
			if alias == raw {
				return s, true
			}
		}
	}
	return engineSpec{}, false
}
