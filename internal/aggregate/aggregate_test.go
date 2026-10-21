package aggregate

import (
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

func TestMergeDedupeByInfoHash(t *testing.T) {
	now := time.Now()
	results := []model.Result{
		{
			Title:       "Ubuntu 22.04 LTS",
			InfoHash:    "2C6B6858D61DA9543D4231A71DB4B1C9264B0685",
			MagnetURI:   "magnet:?xt=urn:btih:2C6B6858D61DA9543D4231A71DB4B1C9264B0685&tr=udp%3A%2F%2Fa%2Fannounce",
			Seeders:     3,
			Leechers:    1,
			SizeBytes:   3654957056,
			Source:      "apibay",
			ListedAt:    now,
			PublishDate: now.Add(-24 * time.Hour),
		},
		{
			Title:     "Ubuntu 22.04 LTS 完整镜像版",
			InfoHash:  "2c6b6858d61da9543d4231a71db4b1c9264b0685", // 小写，同一资源
			MagnetURI: "magnet:?xt=urn:btih:2C6B6858D61DA9543D4231A71DB4B1C9264B0685&tr=udp%3A%2F%2Fb%2Fannounce",
			Seeders:   11,
			Leechers:  4,
			SizeBytes: 3654957056,
			Source:    "bitsearch",
			ListedAt:  now,
		},
		{
			Title:    "无关资源",
			InfoHash: "not-a-valid-hash",
			Source:   "broken",
			ListedAt: now,
			Seeders:  100,
		},
	}

	got := Merge(results)
	if len(got) != 1 {
		t.Fatalf("期望合并为 1 条候选，实际 %d 条", len(got))
	}
	c := got[0]
	if c.Result.InfoHash != "2c6b6858d61da9543d4231a71db4b1c9264b0685" {
		t.Errorf("info hash 未归一化为小写: %s", c.Result.InfoHash)
	}
	if c.Result.Seeders != 11 {
		t.Errorf("做种数应取多源最大值 11，实际 %d", c.Result.Seeders)
	}
	if len(c.Sources) != 2 {
		t.Errorf("应记录 2 个来源，实际 %d: %v", len(c.Sources), c.Sources)
	}
	if c.Result.Title != "Ubuntu 22.04 LTS 完整镜像版" {
		t.Errorf("标题应取信息量更大的一条，实际 %q", c.Result.Title)
	}
	if c.Result.PublishDate.IsZero() {
		t.Error("发布时间不应被覆盖为零值")
	}
	if trackers := magnetTrackers(c.Result.MagnetURI); len(trackers) != 2 {
		t.Errorf("磁力链接应合并 2 个 tracker，实际 %d", len(trackers))
	}
}

func TestClusterKeyKeepsQualitySeparatesGroups(t *testing.T) {
	a := "[SubsPlease] Some Anime - 01 (1080p) [x265].mkv"
	b := "[EMBER] Some Anime - 01 (1080p) [x265].mkv"
	c := "[SubsPlease] Some Anime - 01 (720p) [x264].mkv"

	if ClusterKey(a) != ClusterKey(b) {
		t.Errorf("不同发布组的同一资源应聚到同一簇:\n a=%q\n b=%q", ClusterKey(a), ClusterKey(b))
	}
	if ClusterKey(a) == ClusterKey(c) {
		t.Errorf("不同画质应分属不同簇: %q", ClusterKey(a))
	}
}

func TestSimilarity(t *testing.T) {
	same := Similarity(
		"[SubsPlease] Some Anime - 01 [1080p][x265].mkv",
		"[EMBER] Some Anime - 01 [1080p][x265].mkv",
	)
	diffQuality := Similarity(
		"[SubsPlease] Some Anime - 01 [1080p][x265].mkv",
		"[SubsPlease] Some Anime - 01 [720p][x264].mkv",
	)
	unrelated := Similarity(
		"[SubsPlease] Some Anime - 01 [1080p][x265].mkv",
		"Windows 11 Pro 24H2 Retail [Chinese Simplified].iso",
	)

	if same < 0.8 {
		t.Errorf("同一资源不同发布组的相似度应较高，实际 %v", same)
	}
	if diffQuality >= same {
		t.Errorf("画质差异应降低相似度: same=%v diff=%v", same, diffQuality)
	}
	if unrelated > 0.3 {
		t.Errorf("无关资源的相似度过高: %v", unrelated)
	}
}

func TestRelevance(t *testing.T) {
	cases := []struct {
		query string
		title string
		min   float64
		max   float64
	}{
		{query: "ubuntu", title: "Ubuntu 22.04.1 LTS Desktop amd64 iso", min: 0.9},
		{query: "ubuntu 24.04", title: "Ubuntu 24.04.1 LTS Desktop", min: 0.9},
		{query: "ubuntu", title: "Windows 11 Pro 24H2 Retail", max: 0.1},
		{query: "some anime", title: "[SubsPlease] Some Anime - 01 [1080p]", min: 0.9},
	}
	for _, c := range cases {
		got := Relevance(c.query, c.title)
		if got < c.min {
			t.Errorf("Relevance(%q, %q) = %v，期望不小于 %v", c.query, c.title, got, c.min)
		}
		if c.max > 0 && got > c.max {
			t.Errorf("Relevance(%q, %q) = %v，期望不大于 %v", c.query, c.title, got, c.max)
		}
	}
}

// magnetTrackers 便于测试读取磁力链接中的 tracker。
func magnetTrackers(uri string) []string {
	return magnet.Trackers(uri)
}
