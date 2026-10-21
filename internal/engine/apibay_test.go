package engine

import (
	"context"
	"regexp"
	"testing"
	"time"
)

// TestApibayParse 用真实抓取样本校验 apibay 的解析结果。
func TestApibayParse(t *testing.T) {
	results, err := NewApibay(nil).parse(loadFixture(t, "apibay_results.json"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 100 {
		t.Fatalf("期望 100 条结果，实际 %d 条", len(results))
	}
	cases := []struct {
		name     string
		idx      int
		title    string
		hash     string
		size     int64
		seeders  int
		leechers int
		added    time.Time
		category string
	}{
		{
			name:     "普通条目",
			idx:      0,
			title:    "Ubuntu 22.04 LTS",
			hash:     "2c6b6858d61da9543d4231a71db4b1c9264b0685",
			size:     3654957056,
			seeders:  37,
			leechers: 5,
			added:    time.Date(2022, 5, 18, 12, 33, 51, 0, time.UTC),
			category: "application",
		},
		{
			name:     "标题里的 HTML 实体要还原",
			idx:      3,
			title:    "Linux Command Line (Hands-On) Ubuntu Linux & Linux Scripting",
			hash:     "c426b415d030710df9335daa38b2b12dbd0d48fb",
			size:     2464274774,
			seeders:  13,
			leechers: 3,
			added:    time.Date(2023, 8, 14, 2, 52, 16, 0, time.UTC),
			category: "other",
		},
		{
			name:     "零做种也是真实的零",
			idx:      1,
			title:    "Clinton D., Negus C. Ubuntu Linux Bible 11ed 2025",
			hash:     "232cd67eb3ffbd7c37bf9ec3ee887417e5ae1ee6",
			size:     8554738,
			seeders:  27,
			leechers: 0,
			added:    time.Date(2025, 12, 12, 8, 2, 8, 0, time.UTC),
			category: "other",
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
			if !got.PublishDate.Equal(tc.added) {
				t.Errorf("发布时间 = %s，期望 %s", got.PublishDate, tc.added)
			}
			if got.Category != tc.category {
				t.Errorf("分类 = %q，期望 %q", got.Category, tc.category)
			}
			if got.Source != "apibay" {
				t.Errorf("来源 = %q，期望 apibay", got.Source)
			}
			if got.Rank != tc.idx {
				t.Errorf("排序位次 = %d，期望 %d", got.Rank, tc.idx)
			}
			if got.ListedAt.IsZero() {
				t.Error("ListedAt 不能为零值")
			}
			wantMagnet := "magnet:?xt=urn:btih:" + tc.hash
			if !regexp.MustCompile(`^magnet:\?xt=urn:btih:[0-9a-f]{40}`).MatchString(got.MagnetURI) {
				t.Errorf("磁力链接 = %q，期望以 %s 开头", got.MagnetURI, wantMagnet)
			}
		})
	}
}

// TestApibaySearchURL 校验查询地址与分类映射。
func TestApibaySearchURL(t *testing.T) {
	a := NewApibay(nil)
	cases := []struct {
		name     string
		query    string
		category string
		want     string
	}{
		{"全部分类", "ubuntu", "", "https://apibay.org/q.php?q=ubuntu&cat=0"},
		{"视频", "ubuntu", "video", "https://apibay.org/q.php?q=ubuntu&cat=200"},
		{"音频", "ubuntu", "audio", "https://apibay.org/q.php?q=ubuntu&cat=100"},
		{"其它分类回落到全部", "ubuntu", "software", "https://apibay.org/q.php?q=ubuntu&cat=0"},
		{"关键词要转义", "hello world", "", "https://apibay.org/q.php?q=hello+world&cat=0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a.searchURL(Options{Query: tc.query, Category: tc.category})
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestApibayLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestApibayLive(t *testing.T) {
	requireLive(t)
	results, err := NewApibay(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
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
		if r.Source != "apibay" {
			t.Fatalf("来源 = %q，期望 apibay", r.Source)
		}
	}
	t.Logf("拿到 %d 条结果，例如 %s", len(results), results[0].Title)
}

// TestApibaySkipsInvalidHash 校验非法 info hash 的记录会被丢弃。
func TestApibaySkipsInvalidHash(t *testing.T) {
	body := []byte(`[
		{"id":"1","name":"好的","info_hash":"2C6B6858D61DA9543D4231A71DB4B1C9264B0685","seeders":"1","leechers":"2","size":"10","added":"1652877231"},
		{"id":"2","name":"坏的","info_hash":"not-a-hash","seeders":"1","leechers":"2","size":"10","added":"1652877231"},
		{"id":"3","name":"空的","info_hash":"","seeders":"1","leechers":"2","size":"10","added":"1652877231"}
	]`)
	results, err := NewApibay(nil).parse(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望只保留 1 条结果，实际 %d 条", len(results))
	}
	if results[0].InfoHash != "2c6b6858d61da9543d4231a71db4b1c9264b0685" {
		t.Errorf("info hash = %q", results[0].InfoHash)
	}
}

// TestApibaySkipsNullHashPlaceholder 校验接口在“没有结果”时返回的零值占位记录会被丢弃。
// 该记录的标题是 “No results returned”、info hash 全零，若不过滤会被当成真实资源输出。
func TestApibaySkipsNullHashPlaceholder(t *testing.T) {
	body := []byte(`[
		{"id":"0","name":"No results returned","info_hash":"0000000000000000000000000000000000000000","leechers":"0","seeders":"0","size":"0","added":"0","category":"0"},
		{"id":"1","name":"真的资源","info_hash":"2C6B6858D61DA9543D4231A71DB4B1C9264B0685","seeders":"3","leechers":"1","size":"1024","added":"1652877231"}
	]`)
	results, err := NewApibay(nil).parse(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望只保留 1 条结果，实际 %d 条", len(results))
	}
	if results[0].Title != "真的资源" {
		t.Errorf("保留下来的是 %q，应该是真实资源", results[0].Title)
	}
}
