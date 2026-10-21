package engine

import (
	"context"
	"testing"
	"time"
)

// TestRutorParse 用真实抓取样本校验 rutor 的解析结果。
func TestRutorParse(t *testing.T) {
	results, err := NewRutor(nil).parse(loadFixture(t, "rutor_results.html"))
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
		date     time.Time
		url      string
	}{
		{
			name:     "普通条目",
			idx:      0,
			title:    "Andrew Latimer - One Word (2026) MP3",
			hash:     "97af02cdf9afdfc5127d67236f893f6db2164f3e",
			size:     75549900,
			seeders:  0,
			leechers: 0,
			date:     time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC),
			url:      "https://rutor.info/torrent/1106288/andrew-latimer-one-word-2026-mp3",
		},
		{
			name:     "多出来的评论数列不能当成大小",
			idx:      2,
			title:    "Василий Леонов - Просто о Word. Руководство для начинающих (2026) PDF",
			hash:     "0ed7eebee9350e757d95b16647369751e4063ea9",
			size:     33397145,
			seeders:  17,
			leechers: 0,
			date:     time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC),
			url:      "https://rutor.info/torrent/1090637/vasilij-leonov-prosto-o-word-rukovodstvo-dlja-nachinajuwih-2026-pdf",
		},
		{
			name:     "下载数大于零",
			idx:      3,
			title:    "Облачный владыка / Клятва мастера / Jun you yun / Word of Honor [S02] (2025) WEB-DL 1080p | P",
			hash:     "71ce7223c716846d42eeccf5cc206438599594bb",
			size:     19874961162,
			seeders:  0,
			leechers: 1,
			date:     time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
			url:      "https://rutor.info/torrent/1087733/oblachnyj-vladyka_kljatva-mastera_jun-you-yun_word-of-honor-s02-2025-web-dl-1080p-p",
		},
		{
			name:     "西里尔字母与 HTML 实体要还原",
			idx:      10,
			title:    "Не говори ни слова / Don't Say a Word (2001) BDRip 1080p | D, P, P2, A",
			hash:     "72249c5ddad83bb1d15e84521f276f959068e544",
			size:     16718160199,
			seeders:  4,
			leechers: 0,
			date:     time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
			url:      "https://rutor.info/torrent/285942/ne-govori-ni-slova_dont-say-a-word-2001-bdrip-1080p-d-p-p2-a",
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
			if got.URL != tc.url {
				t.Errorf("详情页 = %q，期望 %q", got.URL, tc.url)
			}
			if got.Source != "rutor" {
				t.Errorf("来源 = %q，期望 rutor", got.Source)
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

// TestRutorDate 校验俄文日期的解析。
func TestRutorDate(t *testing.T) {
	cases := []struct {
		name string
		text string
		want time.Time
		ok   bool
	}{
		{"俄文缩写", "15 Сен 26", time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), true},
		{"三字母月份", "23 Июл 26", time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC), true},
		{"五月", "12 Май 26", time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC), true},
		{"一月", "05 Янв 25", time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC), true},
		{"十二月", "31 Дек 24", time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC), true},
		{"完整月份名", "15 Сентябрь 26", time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), true},
		{"认不出来的月份", "15 Фыва 26", time.Time{}, false},
		{"格式不对", "2026-09-15", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rutorDate(tc.text)
			if ok != tc.ok {
				t.Fatalf("解析成功 = %v，期望 %v", ok, tc.ok)
			}
			if !got.Equal(tc.want) {
				t.Errorf("日期 = %s，期望 %s", got, tc.want)
			}
		})
	}
}

// TestRutorSearchURL 校验查询地址用的是站点真实的路由。
func TestRutorSearchURL(t *testing.T) {
	rutor := NewRutor(nil)
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"单词", "ubuntu", "https://rutor.info/search/0/0/100/0/ubuntu"},
		{"多词", "ubuntu linux", "https://rutor.info/search/0/0/100/0/ubuntu%20linux"},
		{"和号按站点规则换成 AND", "rock & roll", "https://rutor.info/search/0/0/100/0/rock%20AND%20roll"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rutor.searchURL(Options{Query: tc.query})
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestRutorLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestRutorLive(t *testing.T) {
	requireLive(t)
	results, err := NewRutor(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
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

// TestRutorSkipsMissingMagnet 校验没有磁力链接的条目会被丢弃，
// 并且只有一个合并人数时下载数保持 Unknown。
func TestRutorSkipsMissingMagnet(t *testing.T) {
	body := []byte(`<table>
		<tr class="gai"><td>15 Сен 26</td><td><a href="magnet:?xt=urn:btih:97af02cdf9afdfc5127d67236f893f6db2164f3e">m</a><a href="/torrent/1/x">有磁力</a></td><td align="right">1 MB</td><td align="center"><span class="green"><img src="x" alt="S" />&nbsp;3</span><img src="x" alt="L" /><span class="red">&nbsp;4</span></td></tr>
		<tr class="tum"><td>16 Сен 26</td><td><a href="/torrent/2/y">没有磁力</a></td><td align="right">2 MB</td><td align="center"><img src="x" alt="S" />&nbsp;7</td></tr>
	</table>`)
	results, err := NewRutor(nil).parse(body)
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
	if got.Seeders != 3 || got.Leechers != 4 {
		t.Errorf("做种/下载数 = %d/%d，期望 3/4", got.Seeders, got.Leechers)
	}
	if !got.PublishDate.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("发布时间 = %s", got.PublishDate)
	}
}
