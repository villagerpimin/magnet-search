package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
)

// TestNyaaParse 用真实抓取的 HTML 样本校验 nyaa 的解析结果。
func TestNyaaParse(t *testing.T) {
	results, err := NewNyaa(nil).parse(loadFixture(t, "nyaa_results.html"), "https://nyaa.ink")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.Title != "Koha Live CD Release 3 (3.0.4 Ubuntu 9.10 Desktop x86)" {
		t.Errorf("标题 = %q", got.Title)
	}
	if got.InfoHash != "45008e48c8800b7d7643337b2e70a634e4c69f6a" {
		t.Errorf("info hash = %q", got.InfoHash)
	}
	if got.SizeBytes != 654311424 {
		t.Errorf("大小 = %d，期望 654311424", got.SizeBytes)
	}
	if got.Seeders != 0 || got.Leechers != 0 {
		t.Errorf("做种/下载数 = %d/%d，期望 0/0", got.Seeders, got.Leechers)
	}
	if got.Category != "Software - Applications" {
		t.Errorf("分类 = %q，期望 Software - Applications", got.Category)
	}
	if !got.PublishDate.Equal(time.Unix(1257231780, 0).UTC()) {
		t.Errorf("发布时间 = %s，期望 2009-11-03 07:03 UTC", got.PublishDate)
	}
	if got.URL != "https://nyaa.ink/view/96659" {
		t.Errorf("详情页 = %q，期望 https://nyaa.ink/view/96659", got.URL)
	}
	if got.Source != "nyaa" {
		t.Errorf("来源 = %q，期望 nyaa", got.Source)
	}
	if got.Rank != 0 {
		t.Errorf("排序位次 = %d，期望 0", got.Rank)
	}
	if got.ListedAt.IsZero() {
		t.Error("ListedAt 不能为零值")
	}
}

// TestNyaaParseListing 用真实抓取的多行列表样本校验解析结果。
// 样本里既有带评论数的行，也有非零的完成下载数，
// 用来锁住两个坑：评论数链接不能当成标题，纯数字不能当成文件大小。
func TestNyaaParseListing(t *testing.T) {
	results, err := NewNyaa(nil).parse(loadFixture(t, "nyaa_listing.html"), "https://nyaa.ink")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 75 {
		t.Fatalf("期望 75 条结果，实际 %d 条", len(results))
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
			name:     "第一条",
			idx:      0,
			title:    "MOGRA ANISON MATRIX!! 2026-10-03.m4a",
			hash:     "86ead648703f843ed732468fcffe45973298234a",
			size:     877867827,
			seeders:  4,
			leechers: 0,
			category: "Audio - Lossy",
			date:     time.Date(2026, 10, 3, 20, 38, 39, 0, time.UTC),
			url:      "https://nyaa.ink/view/2169351",
		},
		{
			name:     "第二条",
			idx:      1,
			title:    "MOGRA ANISON MATRIX!! 2026-09-05.m4a",
			hash:     "e2b5b73fb4eefde86138d74a7180f06450a4a360",
			size:     899468492,
			seeders:  4,
			leechers: 1,
			category: "Audio - Lossy",
			date:     time.Date(2026, 9, 5, 20, 34, 41, 0, time.UTC),
			url:      "https://nyaa.ink/view/2156988",
		},
		{
			name:     "标题前面有评论数链接的行",
			idx:      5,
			title:    "MOGRA ANISON MATRIX!! 2026-05-02.m4a",
			hash:     "2b778debe3487991b2bdeb8c93c961f6a16506ae",
			size:     903767654,
			seeders:  2,
			leechers: 0,
			category: "Audio - Lossy",
			date:     time.Date(2026, 5, 3, 3, 1, 19, 0, time.UTC),
			url:      "https://nyaa.ink/view/2105712",
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
		})
	}
}

// TestNyaaMagnetDecoded 校验磁力链接里的 HTML 实体已被还原且 tracker 完整保留。
func TestNyaaMagnetDecoded(t *testing.T) {
	results, err := NewNyaa(nil).parse(loadFixture(t, "nyaa_results.html"), "https://nyaa.ink")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	uri := results[0].MagnetURI
	if !strings.HasPrefix(uri, "magnet:?xt=urn:btih:") {
		t.Errorf("磁力链接 = %q，期望以 magnet:?xt=urn:btih: 开头", uri)
	}
	if strings.Contains(uri, "&amp;") {
		t.Errorf("磁力链接里还留着 HTML 实体: %q", uri)
	}
	if len(magnet.Trackers(uri)) != 5 {
		t.Errorf("tracker 数量 = %d，期望 5 个", len(magnet.Trackers(uri)))
	}
}

// TestNyaaNumericTitleNotCounted 校验标题恰好是数字的条目不会被误当成人数。
func TestNyaaNumericTitleNotCounted(t *testing.T) {
	body := []byte(`<table><tbody><tr class="default">
		<td><a href="/?c=1_2" title="Anime - English"><img src="/static/img/icons/nyaa/1_2.png" alt="Anime - English" class="category-icon"></a></td>
		<td colspan="2"><a href="/view/123456" title="2026">2026</a></td>
		<td class="text-center">
			<a href="/download/123456.torrent"><i class="fa fa-fw fa-download"></i></a>
			<a href="magnet:?xt=urn:btih:45008e48c8800b7d7643337b2e70a634e4c69f6a&amp;dn=2026"><i class="fa fa-fw fa-magnet"></i></a>
		</td>
		<td class="text-center">624.0 MiB</td>
		<td class="text-center" data-timestamp="1257231780">2009-11-03 07:03</td>
		<td class="text-center">7</td>
		<td class="text-center">3</td>
		<td class="text-center">12</td>
	</tr></tbody></table>`)
	results, err := NewNyaa(nil).parse(body, "https://nyaa.ink")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.Title != "2026" {
		t.Errorf("标题 = %q，期望 2026", got.Title)
	}
	if got.SizeBytes != 654311424 {
		t.Errorf("大小 = %d，期望 654311424", got.SizeBytes)
	}
	if got.Seeders != 7 || got.Leechers != 3 {
		t.Errorf("做种/下载数 = %d/%d，期望 7/3", got.Seeders, got.Leechers)
	}
}
