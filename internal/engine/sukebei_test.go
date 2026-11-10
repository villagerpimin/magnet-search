package engine

import (
	"strings"
	"testing"

	"github.com/pigzhu/magnet-search/internal/model"
)

// TestSukebeiRSSURL 校验 RSS 查询地址的拼装与转义。
func TestSukebeiRSSURL(t *testing.T) {
	engine := NewSukebei(nil)
	cases := []struct {
		name  string
		base  string
		query string
		want  string
	}{
		{
			name:  "默认地址",
			query: "SSIS-001",
			want:  "https://sukebei.nyaa.si/?f=0&c=0_0&q=SSIS-001&page=rss",
		},
		{
			name:  "关键词要转义",
			query: "hello world",
			want:  "https://sukebei.nyaa.si/?f=0&c=0_0&q=hello+world&page=rss",
		},
		{
			name:  "结尾斜杠不重复",
			base:  "https://sukebei.nyaa.si/",
			query: "SSIS-001",
			want:  "https://sukebei.nyaa.si/?f=0&c=0_0&q=SSIS-001&page=rss",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine.BaseURL = "https://sukebei.nyaa.si"
			if tc.base != "" {
				engine.BaseURL = tc.base
			}
			if got := engine.rssURL(Options{Query: tc.query}); got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestSukebeiParseRSS 校验真实样本的解析结果。
func TestSukebeiParseRSS(t *testing.T) {
	results, err := NewSukebei(nil).parseRSS(loadFixture(t, "sukebei_rss.xml"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("期望 2 条结果，实际 %d 条", len(results))
	}
	first := results[0]
	if !strings.Contains(first.Title, "SSIS-001") {
		t.Errorf("标题 = %q，应含番号", first.Title)
	}
	if first.Source != "sukebei" {
		t.Errorf("来源 = %q，期望 sukebei", first.Source)
	}
	if first.InfoHash != "30291c52bb72d46affc1574ec01a4e16fc28a292" {
		t.Errorf("info hash = %q", first.InfoHash)
	}
	if !strings.HasPrefix(first.MagnetURI, "magnet:?xt=urn:btih:30291c52bb72d46affc1574ec01a4e16fc28a292") {
		t.Errorf("磁力链接 = %q", first.MagnetURI)
	}
	if first.SizeBytes <= 0 {
		t.Errorf("大小 = %d，应为正数", first.SizeBytes)
	}
	if first.Category != "Real Life - Videos" {
		t.Errorf("分类 = %q，期望 RSS 里给出的 Real Life - Videos", first.Category)
	}
	if first.Seeders != 0 || first.Leechers != 1 {
		t.Errorf("人数 = %d/%d，期望 0/1", first.Seeders, first.Leechers)
	}
	if first.Rank != 0 || results[1].Rank != 1 {
		t.Errorf("排序位次 = %d/%d，期望 0/1", first.Rank, results[1].Rank)
	}
	if first.URL != "https://sukebei.nyaa.si/view/4419939" {
		t.Errorf("详情页 = %q", first.URL)
	}
}

// TestSukebeiRegister 校验注册信息。
func TestSukebeiRegister(t *testing.T) {
	reg := NewRegistry()
	RegisterSukebei(reg)
	specs := reg.Specs()
	if len(specs) != 1 {
		t.Fatalf("期望 1 个引擎，实际 %d 个", len(specs))
	}
	spec := specs[0]
	if spec.Name != "sukebei" {
		t.Errorf("引擎名 = %q，期望 sukebei", spec.Name)
	}
	if spec.Region != RegionOverseas {
		t.Errorf("区域 = %q，期望 overseas（需要代理）", spec.Region)
	}
	engine := spec.New(nil)
	if engine.Name() != "sukebei" || engine.Description() == "" {
		t.Errorf("构造出的引擎 = %q / %q", engine.Name(), engine.Description())
	}
}

// TestSukebeiMissingCategoryStaysEmpty 校验 sukebei 不套用 nyaa 的分类兜底表。
// 分站的 2_x 是真人视频，按主站语义会被误标成 Audio，因此宁可不填。
func TestSukebeiMissingCategoryStaysEmpty(t *testing.T) {
	const sample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:nyaa="https://sukebei.nyaa.si/xmlns/nyaa">
<channel>
<item>
<title>SSIS-001 无分类样本</title>
<nyaa:infoHash>30291c52bb72d46affc1574ec01a4e16fc28a292</nyaa:infoHash>
<nyaa:categoryId>2_2</nyaa:categoryId>
<nyaa:size>1.0 GiB</nyaa:size>
</item>
</channel>
</rss>`
	results, err := NewSukebei(nil).parseRSS([]byte(sample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	if results[0].Category != "" {
		t.Errorf("分类 = %q，sukebei 不该套用 nyaa 的分类名兜底", results[0].Category)
	}
	if results[0].Seeders != model.Unknown {
		t.Errorf("做种数 = %d，RSS 没给时应记成未知 %d", results[0].Seeders, model.Unknown)
	}
}
