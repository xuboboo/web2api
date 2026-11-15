package cmd

import (
	"strings"

	"github.com/xuboboo/web2api/core"
)

func buildEngineProxyPolicyMap() map[string]string {
	return map[string]string{
		"baidu": config.BaiduConfig.Proxy,
		"bing":  config.BingConfig.Proxy,
		"sogou": config.SogouConfig.Proxy,
		"so360": config.So360Config.Proxy,
	}
}

func buildNormalizedProxyConfig(runtime string) (core.ProxyConfig, error) {
	return core.NormalizeProxyConfig(core.ProxyConfig{
		Runtime:        runtime,
		Proxies:        config.Proxies,
		EnginePolicies: buildEngineProxyPolicyMap(),
	})
}

func resolveEngineProxyPolicy(proxyCfg core.ProxyConfig, engineName string) core.ProxyPolicy {
	engineKey := strings.ToLower(strings.TrimSpace(engineName))
	return core.ResolveEffectiveProxyPolicy(proxyCfg.Proxies.Global, proxyCfg.EnginePolicies[engineKey])
}
