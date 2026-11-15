package cmd

import (
	"slices"
	"testing"

	"github.com/xuboboo/web2api/bing"
	"github.com/xuboboo/web2api/core"
)

func TestDisabledEnginesAreLeftOutOfServe(t *testing.T) {
	isolateConfig(t)
	t.Setenv("WEB2API_BING_DISABLED", "true")
	if err := initializeConfig(configCommand(t, "")); err != nil {
		t.Fatal(err)
	}
	if !config.BingConfig.Disabled || config.BaiduConfig.Disabled {
		t.Fatalf("disabled: bing=%v baidu=%v, want true false", config.BingConfig.Disabled, config.BaiduConfig.Disabled)
	}

	var names []string
	for _, s := range browserEngineSpecs() {
		names = append(names, s.name)
	}
	if slices.Contains(names, "bing") || !slices.Contains(names, "baidu") || len(names) != len(engineSpecs())-1 {
		t.Fatalf("browser engines = %v, want every engine but bing", names)
	}
	if got := enabledEngineNames("baidu", "bing"); !slices.Equal(got, []string{"baidu"}) {
		t.Fatalf("enabledEngineNames = %v, want [baidu]", got)
	}
}

func TestBingSearchBoxConfig(t *testing.T) {
	isolateConfig(t)
	t.Setenv("WEB2API_BING_SEARCH_BOX", "")
	if err := initializeConfig(configCommand(t, "")); err != nil {
		t.Fatal(err)
	}
	spec, _ := resolveEngineSpec("bing")
	if engine := spec.factory(core.Browser{}, spec.opts()).(*bing.Bing); !engine.SearchBox {
		t.Fatal("search_box should default to true")
	}

	t.Setenv("WEB2API_BING_SEARCH_BOX", "false")
	if err := initializeConfig(configCommand(t, "")); err != nil {
		t.Fatal(err)
	}
	if engine := spec.factory(core.Browser{}, spec.opts()).(*bing.Bing); engine.SearchBox {
		t.Fatal("WEB2API_BING_SEARCH_BOX=false should turn the search box off")
	}
}
