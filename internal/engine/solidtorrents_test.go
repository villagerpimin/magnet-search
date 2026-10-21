package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/model"
)

// solidSample 是 solidtorrents.to 结果卡片的手写结构样本。
//
// 这份样本按该站与 bitsearch.eu 共用的 Tailwind 模板手写，
// 不是实时抓取：solidtorrents.to 在本机被 SNI 级别阻断，连不上。
// 磁力链接里故意保留 &#x3D; 与 &amp; 实体，用来验证 HTML 解析器会还原它们。
const solidSample = `<div class="torrent-result-card">
	<h3 class="font-bold text-lg mb-2">
		<a href="/torrent/D540FC48EB12F2833163EED6421D449DD8F1CE1F" class="hover:text-primary transition duration-150 ease-in-out">ubuntu-19.04-desktop-amd64.iso</a>
	</h3>
	<div class="torrent-result-meta flex flex-wrap items-center gap-4 text-sm text-gray-600 mb-3">
		<span class="inline-flex items-center space-x-1"><i class="fas fa-video text-blue-500"></i><span>Other/DiskImage</span></span>
		<span class="inline-flex items-center space-x-1"><i class="fas fa-download"></i><span>1.95 GB</span></span>
		<span class="inline-flex items-center space-x-1"><i class="fas fa-calendar"></i><span>4/18/2019</span></span>
	</div>
	<div class="torrent-result-meta flex flex-wrap items-center gap-4 text-sm">
		<span class="inline-flex items-center space-x-1 text-green-600"><i class="fas fa-arrow-up"></i><span class="font-medium">28</span><span>seeders</span></span>
		<span class="inline-flex items-center space-x-1 text-red-600"><i class="fas fa-arrow-down"></i><span class="font-medium">41</span><span>leechers</span></span>
		<span class="inline-flex items-center space-x-1 text-blue-600"><i class="fas fa-download"></i><span class="font-medium">1403</span><span>downloads</span></span>
	</div>
	<div class="hidden sm:flex flex-col space-y-2 ml-6">
		<a href="/download/torrent/D540FC48EB12F2833163EED6421D449DD8F1CE1F?title=ubuntu">Torrent</a>
		<a href="magnet:?xt&#x3D;urn:btih:D540FC48EB12F2833163EED6421D449DD8F1CE1F&amp;dn&#x3D;ubuntu-19.04-desktop-amd64.iso&amp;tr&#x3D;udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce">Magnet</a>
	</div>
</div>`

// TestSolidtorrentsParse 校验结果卡片的解析结果。
func TestSolidtorrentsParse(t *testing.T) {
	results, err := NewSolidtorrents(nil).parse([]byte(solidSample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	cases := []struct {
		name     string
		title    string
		hash     string
		size     int64
		seeders  int
		leechers int
		category string
		url      string
		source   string
		rank     int
	}{
		{
			name:     "结果卡片",
			title:    "ubuntu-19.04-desktop-amd64.iso",
			hash:     "d540fc48eb12f2833163eed6421d449dd8f1ce1f",
			size:     2093796556,
			seeders:  28,
			leechers: 41,
			category: "Other/DiskImage",
			url:      "https://solidtorrents.to/torrent/D540FC48EB12F2833163EED6421D449DD8F1CE1F",
			source:   "solidtorrents",
			rank:     0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			if got.URL != tc.url {
				t.Errorf("详情页 = %q，期望 %q", got.URL, tc.url)
			}
			if got.Source != tc.source {
				t.Errorf("来源 = %q，期望 %q", got.Source, tc.source)
			}
			if got.Rank != tc.rank {
				t.Errorf("排序位次 = %d，期望 %d", got.Rank, tc.rank)
			}
		})
	}
	if !got.PublishDate.Equal(time.Date(2019, 4, 18, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("发布时间 = %s，期望 2019-04-18", got.PublishDate)
	}
	if got.ListedAt.IsZero() {
		t.Error("ListedAt 不能为零值")
	}
}

// TestSolidtorrentsMagnetDecoded 校验磁力链接里的 HTML 实体已被还原。
func TestSolidtorrentsMagnetDecoded(t *testing.T) {
	results, err := NewSolidtorrents(nil).parse([]byte(solidSample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	uri := results[0].MagnetURI
	if !strings.HasPrefix(uri, "magnet:?xt=urn:btih:") {
		t.Errorf("磁力链接 = %q，期望以 magnet:?xt=urn:btih: 开头", uri)
	}
	if strings.Contains(uri, "&#x3D;") || strings.Contains(uri, "&amp;") {
		t.Errorf("磁力链接里还留着 HTML 实体: %q", uri)
	}
	if !strings.Contains(uri, "&tr=") {
		t.Errorf("磁力链接里应保留 tracker 参数: %q", uri)
	}
}

// TestSolidtorrentsSkipsMissingMagnet 校验没有磁力链接的卡片会被丢弃。
func TestSolidtorrentsSkipsMissingMagnet(t *testing.T) {
	body := []byte(`<div class="torrent-result-card">
		<h3><a href="/torrent/D540FC48EB12F2833163EED6421D449DD8F1CE1F">有磁力</a></h3>
		<div class="torrent-result-meta"><span class="inline-flex items-center space-x-1"><i class="fas fa-download"></i><span>1.5 GB</span></span></div>
		<div class="torrent-result-meta"><span class="inline-flex items-center space-x-1 text-green-600"><span class="font-medium">5</span><span>seeders</span></span></div>
		<a href="magnet:?xt=urn:btih:D540FC48EB12F2833163EED6421D449DD8F1CE1F">magnet</a>
	</div>
	<div class="torrent-result-card">
		<h3><a href="/torrent/abcdef">没有磁力</a></h3>
		<a href="/download/torrent/1">Torrent</a>
	</div>`)
	results, err := NewSolidtorrents(nil).parse(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望只保留 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.Title != "有磁力" {
		t.Errorf("标题 = %q，期望 有磁力", got.Title)
	}
	if got.SizeBytes != 1610612736 {
		t.Errorf("大小 = %d，期望 1610612736", got.SizeBytes)
	}
	if got.Seeders != 5 {
		t.Errorf("做种数 = %d，期望 5", got.Seeders)
	}
	if got.Leechers != model.Unknown {
		t.Errorf("没有下载数时应为 %d，实际 %d", model.Unknown, got.Leechers)
	}
}

// TestSolidtorrentsSearchURL 校验查询地址。
func TestSolidtorrentsSearchURL(t *testing.T) {
	solid := NewSolidtorrents(nil)
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"默认地址", "ubuntu", "https://solidtorrents.to/search?q=ubuntu"},
		{"关键词要转义", "hello world", "https://solidtorrents.to/search?q=hello+world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := solid.searchURL(Options{Query: tc.query})
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestSolidtorrentsLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
// solidtorrents.to 在本机不可达，网络拒绝时跳过而不是让整套测试失败。
func TestSolidtorrentsLive(t *testing.T) {
	requireLive(t)
	results, err := NewSolidtorrents(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
	if err != nil {
		skipIfUnreachable(t, err)
	}
	if len(results) == 0 {
		t.Fatal("期望至少有 1 条结果")
	}
	for _, r := range results {
		if len(r.InfoHash) != 40 {
			t.Fatalf("info hash 长度 = %d，期望 40: %q", len(r.InfoHash), r.InfoHash)
		}
	}
	t.Logf("拿到 %d 条结果，例如 %s", len(results), results[0].Title)
}
