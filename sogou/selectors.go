package sogou

// Selectors is the single source of truth for Sogou SERP CSS selectors.
var Selectors = struct {
	Results    string
	Title      string
	Desc       []string
	Cite       string
	CiteDomain string
	AdMarkers  []string
	AdLabels   []string
	Captcha    []string
	NoResults  []string
}{
	// Results matches one organic/ad result card. Inside that card Sogou
	// renders "extended query" suggestion rows and hidden prefetched cards that
	// reuse vrwrap, so those are filtered out in the parser.
	Results: "div.vrwrap",
	Title:   "h3.vr-title a",
	// Desc covers the snippet variants Sogou ships: encyclopedia abstracts
	// (p.star-wiki), download/link cards (div.fz-mid.space-txt) and the generic
	// text layout used by news and Q&A rows.
	Desc: []string{
		"div.text-layout p.star-wiki",
		"div.fz-mid.space-txt",
		"div.space-txt",
		"div.str_info",
		"div.text-layout",
	},
	Cite:       "a.citeLinkClass",
	CiteDomain: "a.citeLinkClass span",
	AdMarkers:  []string{"[data-tuiguang]", ".ec_ad", ".ad-tag", ".vr-ad"},
	// AdLabels are the visible "广告"/"推广" tags Sogou prints on promoted rows.
	AdLabels: []string{"广告", "推广"},
	Captcha: []string{
		"#seccodeImage",
		"div.passMod_dialog-wrapper",
		"div.captcha-container",
		"div.verify-bar-area",
	},
	NoResults: []string{"#noresult_part1_container", "div.noresult", ".no-result"},
}
