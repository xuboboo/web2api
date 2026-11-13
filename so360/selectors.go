package so360

// Selectors is the single source of truth for 360 Search SERP CSS selectors.
var Selectors = struct {
	Results    string
	Title      string
	Desc       string
	Cite       string
	AdMarkers  []string
	AdTypes    []string
	AdLabels   []string
	Captcha    []string
	NoResults  []string
}{
	Results: "li.res-list",
	Title:   "h3.res-title a",
	Desc:    "p.res-desc",
	Cite:    "p.g-linkinfo cite a, p.g-linkinfo cite",
	// AdMarkers are the class/id markers 360 puts on promoted cards.
	AdMarkers: []string{"[data-result-type='ad']", "[data-ad]", ".res-list-ad", ".ec_ad"},
	// AdTypes are data-result-type values reserved for ads.
	AdTypes: []string{"ad", "ads", "adv", "advertisement"},
	AdLabels: []string{"广告", "推广", "广告·推广"},
	Captcha: []string{
		"#captcha",
		"div.verify-bar-area",
		"div.captcha-container",
		"div.s-verify",
	},
	NoResults: []string{".no-result", "#no-result", "div.noresult"},
}
