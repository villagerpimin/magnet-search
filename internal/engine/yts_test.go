package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// TestYtsParse 用真实抓取的 JSON 样本校验 yts 的解析结果。
func TestYtsParse(t *testing.T) {
	results, err := NewYts(nil).parse(loadFixture(t, "yts_results.json"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("期望 4 条结果，实际 %d 条", len(results))
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
			name:     "第一部电影的第一个画质",
			idx:      0,
			title:    "Matrix: Generation 2024 720p web",
			hash:     "937c8886c8fd31240898b0de40de9e104a926f7e",
			size:     511568773,
			seeders:  3,
			leechers: 4,
			date:     time.Date(2024, 1, 22, 21, 45, 44, 0, time.UTC),
			url:      "https://yts.rs/movies/matrix-generation-2024",
		},
		{
			name:     "不同电影交错输出",
			idx:      1,
			title:    "Hope Frozen 2019 720p web",
			hash:     "2c926a3afb9e85b8005bbdb10743d21e538e3683",
			size:     778011935,
			seeders:  0,
			leechers: 2,
			date:     time.Date(2022, 3, 5, 23, 40, 58, 0, time.UTC),
			url:      "https://yts.rs/movies/hope-frozen-2019",
		},
		{
			name:     "同一部电影的下一个画质",
			idx:      2,
			title:    "Matrix: Generation 2024 1080p web",
			hash:     "47c3a66a7d04065607f97aab5cbfa651c136d90a",
			size:     948174848,
			seeders:  20,
			leechers: 1,
			date:     time.Date(2024, 1, 22, 22, 19, 9, 0, time.UTC),
			url:      "https://yts.rs/movies/matrix-generation-2024",
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
			if got.Category != "movie" {
				t.Errorf("分类 = %q，期望 movie", got.Category)
			}
			if got.Source != "yts" {
				t.Errorf("来源 = %q，期望 yts", got.Source)
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

// TestYtsMagnetBuilt 校验磁力链接是按 info hash 自己拼出来的。
func TestYtsMagnetBuilt(t *testing.T) {
	results, err := NewYts(nil).parse(loadFixture(t, "yts_results.json"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	uri := results[0].MagnetURI
	if !strings.HasPrefix(uri, "magnet:?xt=urn:btih:937c8886c8fd31240898b0de40de9e104a926f7e") {
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

// TestYtsSearchURL 校验查询地址与默认条数。
func TestYtsSearchURL(t *testing.T) {
	yts := NewYts(nil)
	cases := []struct {
		name  string
		query string
		limit int
		want  string
	}{
		{
			name:  "默认条数",
			query: "matrix",
			want:  "https://yts.rs/api/v2/list_movies.json?query_term=matrix&limit=20",
		},
		{
			name:  "指定条数",
			query: "matrix",
			limit: 2,
			want:  "https://yts.rs/api/v2/list_movies.json?query_term=matrix&limit=2",
		},
		{
			name:  "关键词要转义",
			query: "the matrix",
			limit: 1,
			want:  "https://yts.rs/api/v2/list_movies.json?query_term=the+matrix&limit=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := yts.searchURL(yts.BaseURL, Options{Query: tc.query, Limit: tc.limit})
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestYtsMissingCounts 校验人数字段缺失时按 Unknown 处理，而不是当成 0。
func TestYtsMissingCounts(t *testing.T) {
	body := []byte(`{"data":{"movies":[{"id":1,"title":"No Counts","year":2020,"url":"https://yts.rs/movies/x",
		"torrents":[{"hash":"937C8886C8FD31240898B0DE40DE9E104A926F7E","quality":"720p","type":"web"}]}]}}`)
	results, err := NewYts(nil).parse(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	if results[0].Seeders != model.Unknown || results[0].Leechers != model.Unknown {
		t.Errorf("缺失的人数 = %d/%d，期望 %d/%d",
			results[0].Seeders, results[0].Leechers, model.Unknown, model.Unknown)
	}
	if results[0].SizeBytes != 0 {
		t.Errorf("缺失的大小 = %d，期望 0", results[0].SizeBytes)
	}
}

// TestYtsSkipsBadHash 校验哈希非法的种子会被丢弃。
func TestYtsSkipsBadHash(t *testing.T) {
	body := []byte(`{"data":{"movies":[{"id":1,"title":"Broken","year":2020,"torrents":[
		{"hash":"not-a-hash","quality":"720p","type":"web"},
		{"hash":"937C8886C8FD31240898B0DE40DE9E104A926F7E","quality":"1080p","type":"web"}]}]}}`)
	results, err := NewYts(nil).parse(body)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望只保留 1 条结果，实际 %d 条", len(results))
	}
	if results[0].Title != "Broken 2020 1080p web" {
		t.Errorf("标题 = %q", results[0].Title)
	}
}

// TestYtsLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestYtsLive(t *testing.T) {
	requireLive(t)
	results, err := NewYts(liveClient(t)).Search(context.Background(), Options{Query: "matrix", Limit: 3})
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
	}
	t.Logf("拿到 %d 条结果，例如 %s", len(results), results[0].Title)
}

// TestYtsBasesIncludeMirrors 校验主域名之后还有可用镜像，
// 主域名不可达时能自动回退到镜像站点。
func TestYtsBasesIncludeMirrors(t *testing.T) {
	yts := NewYts(nil)
	bases := yts.bases()
	if len(bases) < 2 {
		t.Fatalf("期望至少包含一个镜像，实际只有 %v", bases)
	}
	if bases[0] != "https://yts.rs" {
		t.Errorf("首个地址应是主域名，实际 %q", bases[0])
	}
	found := false
	for _, b := range bases {
		if b == "https://yts.gg" {
			found = true
		}
	}
	if !found {
		t.Errorf("镜像列表里应包含 yts.gg，实际 %v", bases)
	}
}
