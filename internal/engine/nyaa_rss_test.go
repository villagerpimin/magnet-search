package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
)

// nyaaRSSSample 是 nyaa RSS 通道的结构样本。
//
// 这份样本是按 nyaa 官方模板（nyaa/templates/rss.xml）逐字段手写的，
// 不是实时抓取：nyaa.si 在本机被 SNI 级别阻断，nyaa.ink 又不提供 RSS 通道。
// 元素名大小写敏感（pubDate、nyaa:infoHash、nyaa:categoryId），
// 解析实现必须按官方写法逐字匹配。
const nyaaRSSSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:nyaa="https://nyaa.si/xmlns/nyaa">
<channel>
<title>Nyaa - Torrent File RSS</title>
<link>https://nyaa.si/?page=rss&amp;q=ubuntu</link>
<description>Torrent File RSS</description>
<item>
<title>[Erai-raws] Anime Name - 01 [1080p][Multiple Subtitle]</title>
<guid isPermaLink="true">https://nyaa.si/view/2176774</guid>
<link>https://nyaa.si/download/2176774.torrent</link>
<pubDate>Wed, 21 Oct 2026 07:22:59 -0000</pubDate>
<nyaa:seeders>12</nyaa:seeders>
<nyaa:leechers>38</nyaa:leechers>
<nyaa:downloads>7</nyaa:downloads>
<nyaa:infoHash>cd66dda8e2492153a5e593ff1cc681587e5c10df</nyaa:infoHash>
<nyaa:categoryId>1_2</nyaa:categoryId>
<nyaa:category>Anime - English-translated</nyaa:category>
<nyaa:size>1.5 GiB</nyaa:size>
<nyaa:comments>0</nyaa:comments>
</item>
<item>
<title>Some Linux ISO</title>
<guid isPermaLink="true">https://nyaa.si/view/96659</guid>
<link>https://nyaa.si/download/96659.torrent</link>
<pubDate>Mon, 2 Feb 2026 03:04:05 -0000</pubDate>
<nyaa:seeders>3</nyaa:seeders>
<nyaa:leechers>4</nyaa:leechers>
<nyaa:downloads>0</nyaa:downloads>
<nyaa:infoHash>45008e48c8800b7d7643337b2e70a634e4c69f6a</nyaa:infoHash>
<nyaa:categoryId>6_1</nyaa:categoryId>
<nyaa:size>624.0 MiB</nyaa:size>
</item>
</channel>
</rss>`

// TestNyaaParseRSS 校验 RSS 通道的解析结果。
func TestNyaaParseRSS(t *testing.T) {
	results, err := NewNyaa(nil).parseRSS([]byte(nyaaRSSSample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("期望 2 条结果，实际 %d 条", len(results))
	}
	cases := []struct {
		name     string
		idx      int
		title    string
		hash     string
		size     int64
		seeders  int
		leechers int
		category string
		date     time.Time
		url      string
	}{
		{
			name:     "带完整分类名",
			idx:      0,
			title:    "[Erai-raws] Anime Name - 01 [1080p][Multiple Subtitle]",
			hash:     "cd66dda8e2492153a5e593ff1cc681587e5c10df",
			size:     1610612736,
			seeders:  12,
			leechers: 38,
			category: "Anime - English-translated",
			date:     time.Date(2026, 10, 21, 7, 22, 59, 0, time.UTC),
			url:      "https://nyaa.si/view/2176774",
		},
		{
			name:     "分类名缺失时用分类编号兜底",
			idx:      1,
			title:    "Some Linux ISO",
			hash:     "45008e48c8800b7d7643337b2e70a634e4c69f6a",
			size:     654311424,
			seeders:  3,
			leechers: 4,
			category: "Software",
			date:     time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC),
			url:      "https://nyaa.si/view/96659",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := results[tc.idx]
			if got.Title != tc.title {
				t.Errorf("标题 = %q，期望 %q", got.Title, tc.title)
			}
			if got.InfoHash != tc.hash {
				t.Errorf("info hash = %q，期望 %q", got.InfoHash, tc.hash)
			}
			if got.SizeBytes != tc.size {
				t.Errorf("大小 = %d，期望 %d", got.SizeBytes, tc.size)
			}
			if got.Seeders != tc.seeders {
				t.Errorf("做种数 = %d，期望 %d", got.Seeders, tc.seeders)
			}
			if got.Leechers != tc.leechers {
				t.Errorf("下载数 = %d，期望 %d", got.Leechers, tc.leechers)
			}
			if got.Category != tc.category {
				t.Errorf("分类 = %q，期望 %q", got.Category, tc.category)
			}
			if !got.PublishDate.Equal(tc.date) {
				t.Errorf("发布时间 = %s，期望 %s", got.PublishDate, tc.date)
			}
			if got.URL != tc.url {
				t.Errorf("详情页 = %q，期望 %q", got.URL, tc.url)
			}
			if got.Rank != tc.idx {
				t.Errorf("排序位次 = %d，期望 %d", got.Rank, tc.idx)
			}
			if got.Source != "nyaa" {
				t.Errorf("来源 = %q，期望 nyaa", got.Source)
			}
		})
	}
}

// TestNyaaRSSMagnetBuilt 校验 RSS 通道自己拼出的磁力链接。
func TestNyaaRSSMagnetBuilt(t *testing.T) {
	results, err := NewNyaa(nil).parseRSS([]byte(nyaaRSSSample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	uri := results[0].MagnetURI
	if !strings.HasPrefix(uri, "magnet:?xt=urn:btih:cd66dda8e2492153a5e593ff1cc681587e5c10df") {
		t.Errorf("磁力链接 = %q", uri)
	}
	if !strings.Contains(uri, "&dn=") {
		t.Errorf("磁力链接里应带上标题: %q", uri)
	}
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		t.Fatalf("磁力链接里解析不出 info hash: %q", uri)
	}
	if hash != results[0].InfoHash {
		t.Errorf("磁力链接里的 hash = %q，期望 %q", hash, results[0].InfoHash)
	}
}
