package engine

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// knabenHashRe 是 info hash 的形态：40 位小写十六进制。
var knabenHashRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestKnabenParse 用真实抓取的结果页样本校验 knaben 的解析结果。
func TestKnabenParse(t *testing.T) {
	results, err := NewKnaben(nil).parse(loadFixture(t, "knaben_results.html"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 50 {
		t.Fatalf("期望 50 条结果，实际 %d 条", len(results))
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
			title:    "ubuntu-26.04-desktop-amd64.iso",
			hash:     "dafc8c076ca2f3ed376eeae7c76a0d6be2415c45",
			size:     6517612871,
			seeders:  136,
			leechers: 19,
			category: "PC / Unix",
			date:     time.Date(2026, 4, 25, 16, 35, 0, 0, time.UTC),
			url:      "https://knaben.xyz/thepiratebay/description.php?id=82917197",
		},
		{
			name:     "第二条",
			idx:      1,
			title:    "ubuntu-24.04.1-desktop-amd64.iso",
			hash:     "4a3f5e08bcef825718eda30637230585e3330599",
			size:     6206227742,
			seeders:  48,
			leechers: 9,
			category: "PC / Unix",
			date:     time.Date(2024, 9, 8, 0, 0, 0, 0, time.UTC),
			url:      "https://knaben.xyz/thepiratebay/description.php?id=76519964",
		},
		{
			name:     "第三条",
			idx:      2,
			title:    "ubuntu-20.04.6-desktop-amd64.iso",
			hash:     "5f5e8848426129ab63cb4db717bb54193c1c1ad7",
			size:     4348654387,
			seeders:  42,
			leechers: 27,
			category: "PC / Unix",
			date:     time.Date(2023, 10, 9, 0, 0, 0, 0, time.UTC),
			url:      "https://knaben.xyz/thepiratebay/description.php?id=72774917",
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
			if !knabenHashRe.MatchString(got.InfoHash) {
				t.Errorf("info hash = %q，不是 40 位小写十六进制", got.InfoHash)
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
			if got.Source != "knaben" {
				t.Errorf("来源 = %q，期望 knaben", got.Source)
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

// TestKnabenRowsFields 校验每一行都给出了人数、大小与哈希。
func TestKnabenRowsFields(t *testing.T) {
	results, err := NewKnaben(nil).parse(loadFixture(t, "knaben_results.html"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for i, r := range results {
		if !knabenHashRe.MatchString(r.InfoHash) {
			t.Errorf("第 %d 条 info hash = %q，不是 40 位小写十六进制", i, r.InfoHash)
		}
		if r.Seeders < 0 {
			t.Errorf("第 %d 条做种数 = %d，期望是具体数值而不是 Unknown", i, r.Seeders)
		}
		if r.Leechers < 0 {
			t.Errorf("第 %d 条下载数 = %d，期望是具体数值而不是 Unknown", i, r.Leechers)
		}
		if r.SizeBytes <= 0 {
			t.Errorf("第 %d 条大小 = %d，期望大于 0", i, r.SizeBytes)
		}
		if r.Title == "" {
			t.Errorf("第 %d 条标题为空", i)
		}
	}
}

// TestKnabenLastSeen 校验样本里的"最近确认"时间被解析出来。
func TestKnabenLastSeen(t *testing.T) {
	results, err := NewKnaben(nil).parse(loadFixture(t, "knaben_results.html"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	withLastSeen := 0
	for _, r := range results {
		if !r.LastSeen.IsZero() {
			withLastSeen++
		}
	}
	if withLastSeen == 0 {
		t.Fatal("期望至少有一条结果的 LastSeen 非零")
	}
	// 样本里有一条写的是绝对日期"2026-09-13"，应当原样解析出来
	const idx = 44
	got := results[idx]
	if got.Title != "Ubuntu 8.04.3 Desktop i386" {
		t.Fatalf("第 %d 条标题 = %q，期望 Ubuntu 8.04.3 Desktop i386", idx, got.Title)
	}
	want := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if !got.LastSeen.Equal(want) {
		t.Errorf("LastSeen = %s，期望 %s", got.LastSeen, want)
	}
}

// TestKnabenSeenTime 校验状态徽标上的说明文字都能换算成绝对时间点。
func TestKnabenSeenTime(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		text string
		want time.Time
		ok   bool
	}{
		{
			name: "刚检查过",
			text: "This torrent was checked now",
			want: now,
			ok:   true,
		},
		{
			name: "一秒前",
			text: "This torrent was checked 1 second ago",
			want: now.Add(-time.Second),
			ok:   true,
		},
		{
			name: "59 分钟前",
			text: "This torrent was checked 59 minutes ago",
			want: now.Add(-59 * time.Minute),
			ok:   true,
		},
		{
			name: "两小时前",
			text: "This torrent was checked 2 hours ago",
			want: now.Add(-2 * time.Hour),
			ok:   true,
		},
		{
			name: "三天前",
			text: "This torrent was last seen 3 days ago",
			want: now.Add(-3 * 24 * time.Hour),
			ok:   true,
		},
		{
			name: "绝对日期",
			text: "This torrent was last seen 2026-09-13",
			want: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "just now 也认",
			text: "This torrent was checked just now",
			want: now,
			ok:   true,
		},
		{
			name: "看不懂就当未知",
			text: "This torrent was checked whenever",
			want: time.Time{},
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := knabenSeenTime(tc.text, now)
			if ok != tc.ok {
				t.Fatalf("解析结果 = %v，期望 %v", ok, tc.ok)
			}
			if !ok {
				if !got.IsZero() {
					t.Errorf("解析失败时时间应为零值，实际 %s", got)
				}
				return
			}
			if !got.Equal(tc.want) {
				t.Errorf("时间 = %s，期望 %s", got, tc.want)
			}
		})
	}
}

// TestKnabenMagnetDecoded 校验磁力链接里的实体已被还原，tracker 参数完整保留。
func TestKnabenMagnetDecoded(t *testing.T) {
	results, err := NewKnaben(nil).parse(loadFixture(t, "knaben_results.html"))
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
	if len(magnet.Trackers(uri)) < 10 {
		t.Errorf("tracker 数量 = %d，期望至少 10 个", len(magnet.Trackers(uri)))
	}
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		t.Fatalf("磁力链接里解析不出 info hash: %q", uri)
	}
	if hash != results[0].InfoHash {
		t.Errorf("磁力链接里的 hash = %q，期望 %q", hash, results[0].InfoHash)
	}
}

// TestKnabenSearchURL 校验查询地址的拼装。
func TestKnabenSearchURL(t *testing.T) {
	knaben := NewKnaben(nil)
	cases := []struct {
		name  string
		base  string
		query string
		page  int
		want  string
	}{
		{
			name:  "默认地址",
			base:  "https://knaben.org",
			query: "ubuntu",
			page:  1,
			want:  "https://knaben.org/search/ubuntu/0/1/seeders?hideXXX",
		},
		{
			name:  "第二页",
			base:  "https://knaben.org",
			query: "ubuntu",
			page:  2,
			want:  "https://knaben.org/search/ubuntu/0/2/seeders?hideXXX",
		},
		{
			name:  "关键词里的空格要转义",
			base:  "https://knaben.org",
			query: "hello world",
			page:  1,
			want:  "https://knaben.org/search/hello%20world/0/1/seeders?hideXXX",
		},
		{
			name:  "结尾斜杠不重复",
			base:  "https://knaben.org/",
			query: "ubuntu",
			page:  1,
			want:  "https://knaben.org/search/ubuntu/0/1/seeders?hideXXX",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := knaben.searchURL(tc.base, Options{Query: tc.query}, tc.page)
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestKnabenPageCount 校验翻页数量按 Limit 计算且不超过上限。
func TestKnabenPageCount(t *testing.T) {
	cases := []struct {
		limit int
		want  int
	}{
		{0, knabenMaxPages},
		{1, 1},
		{knabenPageSize, 1},
		{knabenPageSize + 1, 2},
		{1000, knabenMaxPages},
	}
	for _, c := range cases {
		if got := knabenPageCount(c.limit); got != c.want {
			t.Errorf("knabenPageCount(%d) = %d，期望 %d", c.limit, got, c.want)
		}
	}
}

// TestKnabenBases 校验主域名在前。
func TestKnabenBases(t *testing.T) {
	bases := NewKnaben(nil).bases()
	if len(bases) == 0 {
		t.Fatal("候选地址不能为空")
	}
	if bases[0] != "https://knaben.org" {
		t.Errorf("首选地址 = %q，期望 https://knaben.org", bases[0])
	}
	seen := map[string]bool{}
	for _, b := range bases {
		if seen[b] {
			t.Errorf("候选地址重复: %q", b)
		}
		seen[b] = true
	}
}

// TestKnabenRegister 校验 knaben 能被注册进引擎表。
func TestKnabenRegister(t *testing.T) {
	reg := NewRegistry()
	RegisterKnaben(reg)
	specs, err := reg.Select([]string{"knaben"})
	if err != nil {
		t.Fatalf("查找引擎失败: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("期望 1 个引擎，实际 %d 个", len(specs))
	}
	spec := specs[0]
	if spec.Region != RegionOverseas {
		t.Errorf("区域 = %q，期望 %q", spec.Region, RegionOverseas)
	}
	if spec.Description != "knaben.org 磁力搜索（带最近确认时间）" {
		t.Errorf("描述 = %q", spec.Description)
	}
	engine := spec.New(nil)
	if engine.Name() != "knaben" {
		t.Errorf("构造出的引擎名 = %q，期望 knaben", engine.Name())
	}
	if engine.Description() == "" {
		t.Error("引擎描述不能为空")
	}
}

// TestKnabenLimit 校验 Limit 会裁剪结果条数。
func TestKnabenLimit(t *testing.T) {
	results, err := NewKnaben(nil).parse(loadFixture(t, "knaben_results.html"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	limited := limitResults(results, 5)
	if len(limited) != 5 {
		t.Errorf("裁剪后 = %d 条，期望 5 条", len(limited))
	}
	if limited[0].InfoHash != results[0].InfoHash {
		t.Errorf("裁剪后第一条变成了 %q", limited[0].InfoHash)
	}
	if len(limitResults(results, 0)) != len(results) {
		t.Error("Limit 为 0 时不应裁剪")
	}
}

// TestKnabenLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestKnabenLive(t *testing.T) {
	requireLive(t)
	results, err := NewKnaben(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
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
		if !knabenHashRe.MatchString(r.InfoHash) {
			t.Fatalf("info hash = %q，不是 40 位小写十六进制", r.InfoHash)
		}
		if r.Seeders == model.Unknown {
			t.Errorf("做种数应来自站点页面，实际是 Unknown: %s", r.Title)
		}
	}
	t.Logf("拿到 %d 条结果，例如 %s（最近确认 %s）", len(results), results[0].Title, results[0].LastSeen)
}
