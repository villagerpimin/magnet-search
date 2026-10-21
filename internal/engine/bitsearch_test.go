package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
)

// TestBitsearchParse 用真实抓取样本校验 bitsearch 的解析结果。
func TestBitsearchParse(t *testing.T) {
	results, err := NewBitsearch(nil).parse(loadFixture(t, "bitsearch_results.html"), 0)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 20 {
		t.Fatalf("期望 20 条结果，实际 %d 条", len(results))
	}
	cases := []struct {
		name     string
		idx      int
		title    string
		hash     string
		size     int64
		seeders  int
		leechers int
		date     time.Time
		category string
		url      string
	}{
		{
			name:     "第一条",
			idx:      0,
			title:    "ubuntu-19.04-desktop-amd64.iso",
			hash:     "d540fc48eb12f2833163eed6421d449dd8f1ce1f",
			size:     2093796556,
			seeders:  28,
			leechers: 41,
			date:     time.Date(2019, 4, 18, 0, 0, 0, 0, time.UTC),
			category: "Other/DiskImage",
			url:      "https://bitsearch.eu/torrent/5cb8afc48700981f3e5b00c4",
		},
		{
			name:     "分类图标换成下载图标",
			idx:      1,
			title:    "ubuntu-22.04.2-desktop-amd64.iso",
			hash:     "a7838b75c42b612da3b6cc99beed4ecb2d04cff2",
			size:     4928474972,
			seeders:  177,
			leechers: 331,
			date:     time.Date(2023, 2, 24, 0, 0, 0, 0, time.UTC),
			category: "Softwares/Linux",
			url:      "https://bitsearch.eu/torrent/63f864e1ae697358dc80e874",
		},
		{
			name:     "第三条",
			idx:      2,
			title:    "ubuntu-23.04-desktop-amd64.iso",
			hash:     "443c7602b4fde83d1154d6d9da48808418b181b6",
			size:     4928474972,
			seeders:  163,
			leechers: 340,
			date:     time.Date(2023, 4, 21, 0, 0, 0, 0, time.UTC),
			category: "Softwares/Linux",
			url:      "https://bitsearch.eu/torrent/6442cc322e8ac2fc7fa34f36",
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
			if !got.PublishDate.Equal(tc.date) {
				t.Errorf("发布时间 = %s，期望 %s", got.PublishDate, tc.date)
			}
			if got.Category != tc.category {
				t.Errorf("分类 = %q，期望 %q", got.Category, tc.category)
			}
			if got.URL != tc.url {
				t.Errorf("详情页 = %q，期望 %q", got.URL, tc.url)
			}
			if got.Source != "bitsearch" {
				t.Errorf("来源 = %q，期望 bitsearch", got.Source)
			}
			if got.Rank != tc.idx {
				t.Errorf("排序位次 = %d，期望 %d", got.Rank, tc.idx)
			}
			if got.ListedAt.IsZero() {
				t.Error("ListedAt 不能为零值")
			}
		})
	}
}

// TestBitsearchMagnetDecoded 校验磁力链接里的 HTML 实体已被还原。
func TestBitsearchMagnetDecoded(t *testing.T) {
	results, err := NewBitsearch(nil).parse(loadFixture(t, "bitsearch_results.html"), 0)
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
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		t.Fatalf("磁力链接里解析不出 info hash: %q", uri)
	}
	if hash != results[0].InfoHash {
		t.Errorf("磁力链接里的 hash = %q，期望 %q", hash, results[0].InfoHash)
	}
}

// TestBitsearchRankOffset 校验翻页时的排序位次是连续的。
func TestBitsearchRankOffset(t *testing.T) {
	bitsearch := NewBitsearch(nil)
	results, err := bitsearch.parse(loadFixture(t, "bitsearch_results.html"), 40)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if results[0].Rank != 40 {
		t.Errorf("首条排序位次 = %d，期望 40", results[0].Rank)
	}
	if results[19].Rank != 59 {
		t.Errorf("末条排序位次 = %d，期望 59", results[19].Rank)
	}
}

// TestBitsearchSearchURL 校验查询地址与翻页规则。
func TestBitsearchSearchURL(t *testing.T) {
	bitsearch := NewBitsearch(nil)
	cases := []struct {
		name  string
		query string
		page  int
		want  string
	}{
		{"第一页", "ubuntu", 1, "https://bitsearch.eu/search?q=ubuntu&page=1"},
		{"第二页", "ubuntu", 2, "https://bitsearch.eu/search?q=ubuntu&page=2"},
		{"关键词要转义", "hello world", 1, "https://bitsearch.eu/search?q=hello+world&page=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bitsearch.searchURL(Options{Query: tc.query}, tc.page)
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
	pageCases := []struct {
		name  string
		limit int
		want  int
	}{
		{"不限条数时按上限翻页", 0, bitsearchMaxPages},
		{"少于一页", 5, 1},
		{"一页多一点", 21, 2},
		{"超过上限", 1000, bitsearchMaxPages},
	}
	for _, tc := range pageCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bitsearch.pageCount(tc.limit); got != tc.want {
				t.Errorf("翻页数 = %d，期望 %d", got, tc.want)
			}
		})
	}
}

// TestBitsearchLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestBitsearchLive(t *testing.T) {
	requireLive(t)
	results, err := NewBitsearch(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
	if err != nil {
		if isBlocked(err) {
			t.Skipf("站点拒绝了本机出口: %v", err)
		}
		t.Fatalf("搜索失败: %v", err)
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

// TestBitsearchSkipsMissingMagnet 校验没有磁力链接的卡片会被丢弃。
func TestBitsearchSkipsMissingMagnet(t *testing.T) {
	body := []byte(`<div class="torrent-result-card">
		<h3><a href="/torrent/abc">有磁力</a></h3>
		<div class="torrent-result-meta"><span><span>Other/Video</span></span><span><span>1.5 GB</span></span><span><span>1/2/2020</span></span></div>
		<div class="torrent-result-meta"><span class="text-green-600"><span class="font-medium">5</span><span>seeders</span></span><span class="text-red-600"><span class="font-medium">6</span><span>leechers</span></span></div>
		<a href="magnet:?xt=urn:btih:D540FC48EB12F2833163EED6421D449DD8F1CE1F">magnet</a>
	</div>
	<div class="torrent-result-card">
		<h3><a href="/torrent/def">没有磁力</a></h3>
		<a href="/download/torrent/1">Torrent</a>
	</div>`)
	results, err := NewBitsearch(nil).parse(body, 0)
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
	if got.Seeders != 5 || got.Leechers != 6 {
		t.Errorf("做种/下载数 = %d/%d，期望 5/6", got.Seeders, got.Leechers)
	}
	if got.Category != "Other/Video" {
		t.Errorf("分类 = %q，期望 Other/Video", got.Category)
	}
	if !got.PublishDate.Equal(time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("发布时间 = %s", got.PublishDate)
	}
}
