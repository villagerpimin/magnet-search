// Package aggregate 负责把多引擎结果归并、去重、聚类并排序。
package aggregate

import (
	"sort"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Verdict 是对单个磁力存活状态的判定。
type Verdict string

const (
	// VerdictAlive 有确凿证据表明当前有做种或在线对等端
	VerdictAlive Verdict = "alive"
	// VerdictCold 资源存在但当前几乎无人做种，属于冷门但仍可下载
	VerdictCold Verdict = "cold"
	// VerdictDead 有证据表明资源已死（多源一致为 0 且陈旧）
	VerdictDead Verdict = "dead"
	// VerdictUnknown 无法判定，网络探测不可用或证据不足
	VerdictUnknown Verdict = "unknown"
)

// Candidate 是聚合后的一个候选资源。
type Candidate struct {
	// Result 合并后的结果信息
	Result model.Result
	// Live 存活探测数据，网络不可用时全为 Unknown
	Live model.Liveness
	// Sources 命中该资源的全部引擎名
	Sources []string
	// Cluster 标题聚类键，用于识别同一资源的不同版本
	Cluster string
	// Relevance 标题与查询的相关度，取值 [0,1]
	Relevance float64
	// Score 最终排序分
	Score float64
	// Verdict 存活判定
	Verdict Verdict
	// Confidence 判定置信度，取值 [0,1]
	Confidence float64
}

// Merge 按 info hash 去重并合并多个引擎的结果。
// 合并原则：做种数取多源最大值（各站刷新频率不同，保守取高），
// 标题取信息量最大的一条，磁力链接合并全部 tracker 参数以提高下载成功率。
func Merge(results []model.Result) []Candidate {
	order := make([]string, 0, len(results))
	byHash := make(map[string]*Candidate, len(results))

	for _, r := range results {
		hash, ok := magnet.NormalizeInfoHash(r.InfoHash)
		if !ok || magnet.IsNullInfoHash(hash) {
			// 解析不出哈希、或哈希是零值占位符的结果无法去重，直接丢弃，
			// 避免把索引站“没有结果”的占位记录当成真实资源输出
			continue
		}
		r.InfoHash = hash
		if r.ListedAt.IsZero() {
			r.ListedAt = time.Now()
		}

		c, exists := byHash[hash]
		if !exists {
			c := &Candidate{
				Result:  r,
				Live:    model.NewLiveness(hash),
				Sources: []string{r.Source},
			}
			c.Result.MagnetURI = mergeMagnet(r.MagnetURI, r.Title, nil)
			byHash[hash] = c
			order = append(order, hash)
			continue
		}

		mergeInto(c, r)
	}

	out := make([]Candidate, 0, len(order))
	for _, h := range order {
		out = append(out, *byHash[h])
	}
	return out
}

// mergeInto 把新结果并入已有候选。
func mergeInto(c *Candidate, r model.Result) {
	if !contains(c.Sources, r.Source) {
		c.Sources = append(c.Sources, r.Source)
	}
	if r.Seeders != model.Unknown && (c.Result.Seeders == model.Unknown || r.Seeders > c.Result.Seeders) {
		c.Result.Seeders = r.Seeders
	}
	if r.Leechers != model.Unknown && (c.Result.Leechers == model.Unknown || r.Leechers > c.Result.Leechers) {
		c.Result.Leechers = r.Leechers
	}
	if r.SizeBytes > 0 {
		c.Result.SizeBytes = r.SizeBytes
	}
	// 发布时间取最早的非零值，反映资源最初进入流通的时间
	if !r.PublishDate.IsZero() && (c.Result.PublishDate.IsZero() || r.PublishDate.Before(c.Result.PublishDate)) {
		c.Result.PublishDate = r.PublishDate
	}
	if r.ListedAt.After(c.Result.ListedAt) {
		c.Result.ListedAt = r.ListedAt
	}
	// 标题取更长的一条，通常信息量更完整
	if len(r.Title) > len(c.Result.Title) {
		c.Result.Title = r.Title
	}
	if c.Result.URL == "" {
		c.Result.URL = r.URL
	}
	if c.Result.Category == "" {
		c.Result.Category = r.Category
	}
	c.Result.MagnetURI = mergeMagnet(c.Result.MagnetURI, c.Result.Title, magnet.Trackers(r.MagnetURI))
}

// mergeMagnet 合并磁力链接的 tracker 参数，保留原有 dn 等字段。
func mergeMagnet(uri, title string, extraTrackers []string) string {
	if uri == "" {
		if title == "" {
			return ""
		}
		return magnet.Build("", title, extraTrackers)
	}
	existing := magnet.Trackers(uri)
	merged := append([]string{}, existing...)
	for _, t := range extraTrackers {
		if !contains(merged, t) {
			merged = append(merged, t)
		}
	}
	if len(merged) == len(existing) {
		return uri
	}
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		return uri
	}
	return magnet.Build(hash, title, merged)
}

// contains 判断字符串是否在切片中。
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// SortByScore 按最终分数降序排序，分数相同时保持稳定顺序。
func SortByScore(cands []Candidate) {
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].Score > cands[j].Score
	})
}
