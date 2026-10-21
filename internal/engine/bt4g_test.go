package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// TestBt4gParse 用真实抓取的 RSS 样本校验 bt4g 的解析结果。
func TestBt4gParse(t *testing.T) {
	results, err := NewBt4g(nil).parse(loadFixture(t, "bt4g_rss.xml"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 15 {
		t.Fatalf("期望 15 条结果，实际 %d 条", len(results))
	}
	cases := []struct {
		name     string
		idx      int
		title    string
		hash     string
		size     int64
		category string
		date     time.Time
		url      string
	}{
		{
			name:     "第一条",
			idx:      0,
			title:    "ubuntu-26.04.1-desktop-amd64.iso",
			hash:     "7574471b5b0358644446267cb4089cc4831d72e4",
			size:     6485400616,
			category: "Archive file",
			date:     time.Date(2026, 10, 21, 5, 27, 30, 0, time.UTC),
			url:      "https://bt4gprx.com/magnet/oHkbCFMgWTDu00lqhBp20vE6yKHAAAAA",
		},
		{
			name:     "第二条",
			idx:      1,
			title:    "ubuntu-mini-iso-26.10-mini-iso-amd64.iso",
			hash:     "29a68f1fe8e534afeb13810a0463a050d4352934",
			size:     130568683,
			category: "Archive file",
			date:     time.Date(2026, 10, 17, 0, 3, 51, 0, time.UTC),
			url:      "https://bt4gprx.com/magnet/4ohRVUFv9SozTiQBqyPPH9EIg5CAAAAA",
		},
		{
			name:     "第三条",
			idx:      2,
			title:    "ubuntu-unity-26.10-desktop-amd64.iso",
			hash:     "6658692b845d7c1ba695ee658131d8060b3f7ddc",
			size:     4681514352,
			category: "Archive file",
			date:     time.Date(2026, 10, 16, 4, 13, 46, 0, time.UTC),
			url:      "https://bt4gprx.com/magnet/Q7ETKPAxvHQo87ISegWCXZPj9MMAAAAAA",
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
			if got.Category != tc.category {
				t.Errorf("分类 = %q，期望 %q", got.Category, tc.category)
			}
			if !got.PublishDate.Equal(tc.date) {
				t.Errorf("发布时间 = %s，期望 %s", got.PublishDate, tc.date)
			}
			if got.URL != tc.url {
				t.Errorf("详情页 = %q，期望 %q", got.URL, tc.url)
			}
			if got.Seeders != model.Unknown || got.Leechers != model.Unknown {
				t.Errorf("RSS 没有人数信息，应保持 Unknown，实际 %d/%d", got.Seeders, got.Leechers)
			}
			if got.Source != "bt4g" {
				t.Errorf("来源 = %q，期望 bt4g", got.Source)
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

// TestBt4gMagnetDecoded 校验磁力链接里的 XML 实体已被还原，tracker 参数完整保留。
func TestBt4gMagnetDecoded(t *testing.T) {
	results, err := NewBt4g(nil).parse(loadFixture(t, "bt4g_rss.xml"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	uri := results[0].MagnetURI
	if !strings.HasPrefix(uri, "magnet:?xt=urn:btih:") {
		t.Errorf("磁力链接 = %q，期望以 magnet:?xt=urn:btih: 开头", uri)
	}
	if strings.Contains(uri, "&amp;") {
		t.Errorf("磁力链接里还留着 XML 实体: %q", uri)
	}
	trackers := magnet.Trackers(uri)
	if len(trackers) < 10 {
		t.Errorf("tracker 数量 = %d，期望至少 10 个", len(trackers))
	}
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		t.Fatalf("磁力链接里解析不出 info hash: %q", uri)
	}
	if hash != results[0].InfoHash {
		t.Errorf("磁力链接里的 hash = %q，期望 %q", hash, results[0].InfoHash)
	}
}

// TestBt4gSearchURL 校验 RSS 查询地址与分类映射。
func TestBt4gSearchURL(t *testing.T) {
	bt4g := NewBt4g(nil)
	cases := []struct {
		name     string
		base     string
		query    string
		category string
		want     string
	}{
		{
			name:  "默认地址",
			base:  "https://bt4gprx.com",
			query: "ubuntu",
			want:  "https://bt4gprx.com/search?q=ubuntu&page=rss&orderby=seeders",
		},
		{
			name:  "关键词要转义",
			base:  "https://bt4gprx.com",
			query: "hello world",
			want:  "https://bt4gprx.com/search?q=hello+world&page=rss&orderby=seeders",
		},
		{
			name:     "视频分类映射成 movie",
			base:     "https://bt4gprx.com",
			query:    "matrix",
			category: "video",
			want:     "https://bt4gprx.com/search?q=matrix&page=rss&orderby=seeders&category=movie",
		},
		{
			name:     "软件分类映射成 app",
			base:     "https://bt4gprx.com",
			query:    "ubuntu",
			category: "software",
			want:     "https://bt4gprx.com/search?q=ubuntu&page=rss&orderby=seeders&category=app",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bt4g.searchURL(tc.base, Options{Query: tc.query, Category: tc.category})
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestBt4gBases 校验主域名在前、镜像在后。
func TestBt4gBases(t *testing.T) {
	bases := NewBt4g(nil).bases()
	if len(bases) != 3 {
		t.Fatalf("期望 3 个候选地址，实际 %d 个: %v", len(bases), bases)
	}
	if bases[0] != "https://bt4gprx.com" {
		t.Errorf("首选地址 = %q，期望 https://bt4gprx.com", bases[0])
	}
	seen := map[string]bool{}
	for _, b := range bases {
		if seen[b] {
			t.Errorf("候选地址重复: %q", b)
		}
		seen[b] = true
	}
}

// TestBt4gTimeLayouts 校验 pubDate 的两种写法都能解析。
func TestBt4gTimeLayouts(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Time
	}{
		{
			name:  "逗号后没有空格",
			value: "Wed,21 Oct 2026 05:27:30 -0000",
			want:  time.Date(2026, 10, 21, 5, 27, 30, 0, time.UTC),
		},
		{
			name:  "逗号后有空格",
			value: "Wed, 21 Oct 2026 05:27:30 -0000",
			want:  time.Date(2026, 10, 21, 5, 27, 30, 0, time.UTC),
		},
		{
			name:  "单位数日期",
			value: "Fri,2 Jan 2026 03:04:05 -0000",
			want:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseFeedTime(tc.value)
			if !ok {
				t.Fatalf("解析失败: %q", tc.value)
			}
			if !got.Equal(tc.want) {
				t.Errorf("时间 = %s，期望 %s", got, tc.want)
			}
		})
	}
}

// TestBt4gLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestBt4gLive(t *testing.T) {
	requireLive(t)
	results, err := NewBt4g(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
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
