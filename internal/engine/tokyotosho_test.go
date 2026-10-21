package engine

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestTokyotoshoParse 用真实抓取样本校验 tokyotosho 的解析结果。
func TestTokyotoshoParse(t *testing.T) {
	results, err := NewTokyotosho(nil).parse(loadFixture(t, "tokyotosho_listing.html"))
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
		date     time.Time
		category string
		url      string
	}{
		{
			name:     "base32 磁力要转成十六进制",
			idx:      0,
			title:    "(同人誌) [りゅうかくさんのどあめ (極太眉毛)] 私だけの先生…EX (ブルーアーカイブ) [DL版].zip",
			hash:     "991a71d8d7a881ebc806913ba492fad7e9a41903",
			size:     38702940,
			seeders:  0,
			leechers: 0,
			date:     time.Date(2026, 10, 21, 7, 7, 0, 0, time.UTC),
			category: "hmanga",
			url:      "https://www.tokyotosho.info/details.php?id=2128323",
		},
		{
			name:     "有做种与下载数",
			idx:      1,
			title:    "[neoDESU] That Time I Got Reincarnated as a Slime the Movie - Tears of the Azure Sea [BD 1080p AV1 OPUS AAC] [Dual Audio]",
			hash:     "604439f7eced73a452e1623c77b0fd38c80f34e8",
			size:     1438814044,
			seeders:  36,
			leechers: 4,
			date:     time.Date(2026, 10, 21, 6, 44, 0, 0, time.UTC),
			category: "anime",
			url:      "https://www.tokyotosho.info/details.php?id=2128322",
		},
		{
			name:     "标题里的 HTML 实体要还原",
			idx:      3,
			title:    "(C100) [HEARTS & CRUSTS (七名菜奈)] SAPPHIRE color edition (からかい上手の高木さん).zip",
			hash:     "0a2aeb700e564d36f6f07056eedce0c0c18009ec",
			size:     12121538,
			seeders:  0,
			leechers: 0,
			date:     time.Date(2026, 10, 21, 6, 37, 0, 0, time.UTC),
			category: "hmanga",
			url:      "https://www.tokyotosho.info/details.php?id=2128320",
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
			if got.Source != "tokyotosho" {
				t.Errorf("来源 = %q，期望 tokyotosho", got.Source)
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

// TestTokyotoshoInfoHash 校验 base32 与十六进制两种磁力都能归一化。
func TestTokyotoshoInfoHash(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
		ok   bool
	}{
		{
			name: "base32 形式",
			uri:  "magnet:?xt=urn:btih:TENHDWGXVCA6XSAGSE52JEX227U2IGID&tr=http%3A%2F%2Fehtracker.org%2F4270917%2Fannounce",
			want: "991a71d8d7a881ebc806913ba492fad7e9a41903",
			ok:   true,
		},
		{
			name: "十六进制形式",
			uri:  "magnet:?xt=urn:btih:D540FC48EB12F2833163EED6421D449DD8F1CE1F&dn=x",
			want: "d540fc48eb12f2833163eed6421d449dd8f1ce1f",
			ok:   true,
		},
		{
			name: "长度不对要判失败",
			uri:  "magnet:?xt=urn:btih:DEADBEEF",
			want: "",
			ok:   false,
		},
		{
			name: "没有 btih 要判失败",
			uri:  "magnet:?dn=x",
			want: "",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tokyoInfoHash(tc.uri)
			if ok != tc.ok {
				t.Fatalf("解析成功 = %v，期望 %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("info hash = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestTokyotoshoSearchURL 校验查询地址用的是 search.php。
func TestTokyotoshoSearchURL(t *testing.T) {
	tokyo := NewTokyotosho(nil)
	got := tokyo.searchURL(Options{Query: "ubuntu linux"})
	want := "https://www.tokyotosho.info/search.php?searchName=true&searchComment=true&terms=ubuntu+linux&type=0"
	if got != want {
		t.Errorf("查询地址 = %q，期望 %q", got, want)
	}
	if strings.Contains(got, "?q=") {
		t.Error("不能用不过滤的 ?q= 列表地址")
	}
}

// TestTokyotoshoSkipsMissingMagnet 校验没有磁力链接的条目会被丢弃。
func TestTokyotoshoSkipsMissingMagnet(t *testing.T) {
	body := []byte(`<table class="listing">
		<tr><td class="desc-top"><a href="magnet:?xt=urn:btih:TENHDWGXVCA6XSAGSE52JEX227U2IGID">m</a> <a type="application/x-bittorrent" href="https://x/y.torrent">有磁力</a></td></tr>
		<tr><td class="desc-bot">Size: 1MB | Date: 2026-10-21 07:07 UTC</td><td class="stats">S: 1 L: 2 C: 3 ID: 1</td></tr>
		<tr><td class="desc-top"><a type="application/x-bittorrent" href="https://x/z.torrent">没有磁力</a></td></tr>
		<tr><td class="desc-bot">Size: 2MB | Date: 2026-10-21 07:08 UTC</td><td class="stats">S: 4 L: 5 C: 6 ID: 2</td></tr>
	</table>`)
	results, err := NewTokyotosho(nil).parse(body)
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
	if got.InfoHash != "991a71d8d7a881ebc806913ba492fad7e9a41903" {
		t.Errorf("info hash = %q", got.InfoHash)
	}
	if got.SizeBytes != 1048576 {
		t.Errorf("大小 = %d，期望 1048576", got.SizeBytes)
	}
	if got.Seeders != 1 || got.Leechers != 2 {
		t.Errorf("做种/下载数 = %d/%d，期望 1/2", got.Seeders, got.Leechers)
	}
}

// TestTokyotoshoLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestTokyotoshoLive(t *testing.T) {
	requireLive(t)
	results, err := NewTokyotosho(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("期望至少有 1 条结果")
	}
	for _, r := range results {
		if len(r.InfoHash) != 40 {
			t.Fatalf("info hash 长度 = %d，期望 40: %q", len(r.InfoHash), r.InfoHash)
		}
		if !strings.HasPrefix(r.MagnetURI, "magnet:?xt=urn:btih:") {
			t.Fatalf("磁力链接格式不对: %q", r.MagnetURI)
		}
	}
	t.Logf("拿到 %d 条结果，例如 %s", len(results), results[0].Title)
}
