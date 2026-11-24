package core

import (
	"context"
	"testing"
)

func TestTitleMatchScore(t *testing.T) {
	cases := []struct {
		name   string
		probed string
		result string
		min    float64
		max    float64
	}{
		{
			name:   "same headline, source suffix appended",
			probed: "小米物联网白皮书发布：3 大突破与 11.6 亿台设备经验开放",
			result: "小米物联网白皮书发布:3 大突破与 11.6 亿台设备经验开放_华军软件园",
			min:    0.9, max: 1.0,
		},
		{
			name:   "baidu truncates the head with an ellipsis",
			probed: "WorkBuddy 5.9.1 跨设备迁移：三类数据一键导入，任务默认暂停",
			result: "...跨设备迁移:三类数据一键导入,任务默认暂停_华军软件园",
			min:    IndexTitleMatchThreshold, max: 1.0,
		},
		{
			name:   "different article on the same beat",
			probed: "华为云开源具身世界模型：两项评测第一，80% 企业选昇腾云",
			result: "谷歌 A2UI 1.0 协议发布:6 个渲染器与 0.15 版本线_华军软件园",
			min:    0, max: IndexTitleMatchThreshold - 0.01,
		},
	}
	for _, tc := range cases {
		got := TitleMatchScore(tc.probed, tc.result)
		if got < tc.min || got > tc.max {
			t.Errorf("%s: score %.3f outside [%.2f, %.2f]", tc.name, got, tc.min, tc.max)
		}
	}
}

func TestEvaluateIndexSERPByTitleAcceptsSameHost(t *testing.T) {
	target := "https://www.example.test/article/10048594.htm"
	results := []SearchResult{
		{URL: "https://www.example.test/article/10048594.htm",
			Title: "...跨设备迁移:三类数据一键导入,任务默认暂停_华军软件园"},
	}
	verdict := EvaluateIndexSERP(context.Background(), target, results, IndexResolveOptions{
		Title:      "WorkBuddy 5.9.1 跨设备迁移：三类数据一键导入，任务默认暂停",
		TitleMatch: true,
	})
	if !verdict.Indexed || verdict.Match != IndexMatchExact {
		t.Fatalf("expected indexed verdict, got %+v", verdict)
	}
	if verdict.MatchedResultURL != "https://www.example.test/article/10048594.htm" {
		t.Errorf("matched_result_url = %q", verdict.MatchedResultURL)
	}
}

func TestEvaluateIndexSERPByTitleAcceptsMobileHostOfSameSite(t *testing.T) {
	target := "https://www.example.test/article/10047373.htm"
	results := []SearchResult{
		{URL: "https://m.example.test/article/10047373.htm",
			Title: "刚刚!豆包将正式收费!AI免费时代要结束了?!_华军软件园"},
	}
	verdict := EvaluateIndexSERP(context.Background(), target, results, IndexResolveOptions{
		Title:      "刚刚！豆包将正式收费！AI免费时代要结束了？！",
		TitleMatch: true,
	})
	if !verdict.Indexed {
		t.Fatalf("mobile host of the same publisher must count as our page: %+v", verdict)
	}
}

func TestIndexSiteKey(t *testing.T) {
	cases := []struct{ host, want string }{
		{"www.onlinedown.net", "onlinedown.net"},
		{"m.onlinedown.net", "onlinedown.net"},
		{"news.sina.com.cn", "sina.com.cn"},
		{"example.org", "example.org"},
	}
	for _, tc := range cases {
		if got := indexSiteKey(tc.host); got != tc.want {
			t.Errorf("indexSiteKey(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestEvaluateIndexSERPByTitleRejectsForeignReprint(t *testing.T) {
	target := "https://www.example.test/article/10048594.htm"
	results := []SearchResult{
		{URL: "https://reprint.example.org/copy", Title: "小米物联网白皮书发布:3 大突破与 11.6 亿台设备经验开放"},
	}
	verdict := EvaluateIndexSERP(context.Background(), target, results, IndexResolveOptions{
		Title:      "小米物联网白皮书发布：3 大突破与 11.6 亿台设备经验开放",
		TitleMatch: true,
	})
	if verdict.Indexed {
		t.Fatalf("a same-headline page on another host is not our page: %+v", verdict)
	}
}

func TestEvaluateIndexSERPByTitleWithoutTitleIsNotIndexed(t *testing.T) {
	target := "https://www.example.test/article/1.htm"
	results := []SearchResult{{URL: "https://www.example.test/article/2.htm", Title: "随便什么标题"}}
	verdict := EvaluateIndexSERP(context.Background(), target, results, IndexResolveOptions{TitleMatch: true})
	if verdict.Indexed {
		// No URL match, and the title probe cannot read the page title in a unit
		// test: nothing may be asserted as indexed.
		t.Fatalf("unknown title must not produce an indexed verdict: %+v", verdict)
	}
}

func TestPerEngineCooldownTiers(t *testing.T) {
	cfg := EngineCooldownConfig{
		StepsMinutes:      []int{10, 30, 120},
		ResetAfterMinutes: 240,
		PerEngine: map[string]EngineCooldownConfig{
			"baidu": {StepsMinutes: []int{2, 10, 30}},
		},
	}
	mgr := newEngineCooldownManager(cfg)
	if got := mgr.tier("baidu", 1); got.Minutes() != 2 {
		t.Errorf("baidu tier1 = %v, want 2m", got)
	}
	if got := mgr.tier("baidu", 3); got.Minutes() != 30 {
		t.Errorf("baidu tier3 = %v, want 30m", got)
	}
	if got := mgr.tier("sogou", 1); got.Minutes() != 10 {
		t.Errorf("sogou tier1 = %v, want 10m", got)
	}
	if got := mgr.tier("baidu", 9); got.Minutes() != 30 {
		t.Errorf("baidu last tier repeats: got %v, want 30m", got)
	}
}
