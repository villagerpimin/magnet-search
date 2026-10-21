package aggregate

import (
	"math"
	"sort"
	"strings"
	"time"
)

// 排序权重，来源于设计规格 docs/SPEC.md。
const (
	weightRelevance = 0.55
	weightLifeAxis  = 0.33
	weightSwarmSize = 0.05
	weightFreshness = 0.07
)

// lifeAxisPrior 是 lifeAxis 的下限，保证 Unknown 状态既不置顶也不垫底。
const lifeAxisPrior = 0.15

// saturationPeers 是 lifeAxis 饱和点：达到该对等端数后热度不再产生区分度。
const saturationPeers = 5.0

// swarmReference 是 swarmSize 归一化的参考对等端数。
const swarmReference = 201.0

// freshnessTauDays 是新鲜度衰减的时间常数（天）。
const freshnessTauDays = 45.0

// 冷门席位的准入门槛。
const (
	rareMinRelevance  = 0.55
	rareMinConfidence = 0.40
	// rareQuotaRatio 是冷门席位占输出条数的默认比例
	rareQuotaRatio = 0.2
)

// RankOptions 控制排序与过滤行为。
type RankOptions struct {
	// Query 搜索关键词，用于计算相关度
	Query string
	// Limit 输出条数上限，0 表示不限制
	Limit int
	// PerCluster 每个聚类最多保留的条数，0 表示不限制
	PerCluster int
	// RareQuota 顶部为冷门资源预留的席位数，-1 表示按比例自动计算
	RareQuota int
	// RareMaxPeers 判定为"稀有"的对等端数上界
	RareMaxPeers int
	// MinPeers 低于该可信对等端数的结果被丢弃
	MinPeers int
	// MinConfidence 低于该置信度的结果被丢弃
	MinConfidence float64
	// DeadFilter 死链处理方式：off / hide / mark / strict
	DeadFilter string
	// ClusterKey 聚类粒度：resolution / full / none
	ClusterKey string
	// Now 计算时间基准，零值时取当前时间
	Now time.Time
}

// Ranked 是排序结果。
type Ranked struct {
	// All 通过筛选的候选，已按分数降序
	All []Candidate
	// Kept 最终保留的结果，已按分数降序
	Kept []Candidate
	// Dropped 被过滤掉的结果及原因
	Dropped []Dropped
}

// Dropped 记录被过滤掉的结果及原因。
type Dropped struct {
	// Candidate 被过滤的候选
	Candidate Candidate
	// Reason 过滤原因
	Reason string
}

// Rank 完成相关度计算、存活判定、打分、过滤与排序的全流程。
func Rank(cands []Candidate, opts RankOptions) Ranked {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	kept := make([]Candidate, 0, len(cands))
	var dropped []Dropped

	for _, c := range cands {
		rel := ComputeRelevance(RelevanceInput{Query: opts.Query, Title: c.Result.Title, Rank: c.Result.Rank})
		c.Relevance = rel.Score
		if !rel.Passed {
			dropped = append(dropped, Dropped{Candidate: c, Reason: "相关度不足：" + rel.Reason})
			continue
		}

		c.Cluster = clusterKeyFor(c.Result.Title, opts.ClusterKey)
		if opts.DeadFilter == "strict" {
			// strict 模式只认正面死亡证据，元数据推断的死链不算死
			c.Verdict, c.Confidence = JudgeStrict(&c)
		} else {
			c.Verdict, c.Confidence = Judge(&c)
		}
		c.Score = score(c, now)

		if reason, ok := filterReason(c, opts); !ok {
			dropped = append(dropped, Dropped{Candidate: c, Reason: reason})
			continue
		}
		kept = append(kept, c)
	}

	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Score != kept[j].Score {
			return kept[i].Score > kept[j].Score
		}
		if kept[i].Confidence != kept[j].Confidence {
			return kept[i].Confidence > kept[j].Confidence
		}
		if kept[i].Relevance != kept[j].Relevance {
			return kept[i].Relevance > kept[j].Relevance
		}
		return kept[i].Result.InfoHash < kept[j].Result.InfoHash
	})

	selected := applyQuota(kept, opts)
	SortByScore(selected)
	return Ranked{All: kept, Kept: selected, Dropped: dropped}
}

// PassesFilters 判断候选是否通过逐条过滤：死链策略、最少可信对等端、最低置信度。
// 流式产出时用它把关，保证中途展示的条目与最终判定一致；
// 每簇条数与冷门配额属于"选哪些"的规则，由最终排序负责，不在这里判断。
func PassesFilters(c Candidate, deadFilter string, minPeers int, minConfidence float64) bool {
	_, ok := filterReason(c, RankOptions{
		DeadFilter:    deadFilter,
		MinPeers:      minPeers,
		MinConfidence: minConfidence,
	})
	return ok
}

// filterReason 判断候选是否应被过滤，返回原因与是否通过。
func filterReason(c Candidate, opts RankOptions) (string, bool) {
	switch opts.DeadFilter {
	case "off", "mark":
		// 保留死链，由输出层标记
	default:
		if c.Verdict == VerdictDead {
			return "死链（已过滤）", false
		}
	}
	if opts.MinPeers > 0 && CrediblePeers(&c) < opts.MinPeers {
		return "可信对等端数不足", false
	}
	if opts.MinConfidence > 0 && c.Confidence < opts.MinConfidence {
		return "置信度不足", false
	}
	return "", true
}

// applyQuota 在保留结果中为冷门资源预留席位，并施加聚类条数上限。
func applyQuota(sorted []Candidate, opts RankOptions) []Candidate {
	limit := opts.Limit
	if limit <= 0 || limit > len(sorted) {
		limit = len(sorted)
	}

	quota := opts.RareQuota
	if quota < 0 {
		quota = int(math.Ceil(rareQuotaRatio * float64(limit)))
	}
	if quota > limit {
		quota = limit
	}

	clusterCount := map[string]int{}
	selected := make([]Candidate, 0, limit)
	chosen := map[string]bool{}

	// 第一步：为冷门资源预留席位，保证稀有但确实活着的资源能进入输出
	if quota > 0 {
		for _, c := range sorted {
			if len(selected) >= quota {
				break
			}
			if !isRare(c, opts) {
				continue
			}
			if opts.PerCluster > 0 && clusterCount[c.Cluster] >= opts.PerCluster {
				continue
			}
			selected = append(selected, c)
			chosen[c.Result.InfoHash] = true
			clusterCount[c.Cluster]++
		}
	}

	// 第二步：按分数填充剩余席位
	for _, c := range sorted {
		if len(selected) >= limit {
			break
		}
		if chosen[c.Result.InfoHash] {
			continue
		}
		if opts.PerCluster > 0 && clusterCount[c.Cluster] >= opts.PerCluster {
			continue
		}
		selected = append(selected, c)
		chosen[c.Result.InfoHash] = true
		clusterCount[c.Cluster]++
	}
	return selected
}

// isRare 判断候选是否属于应被优先暴露的冷门资源。
func isRare(c Candidate, opts RankOptions) bool {
	if c.Verdict != VerdictCold {
		return false
	}
	if CrediblePeers(&c) > opts.RareMaxPeers {
		return false
	}
	return c.Relevance >= rareMinRelevance && c.Confidence >= rareMinConfidence
}

// score 按设计规格计算最终排序分。
func score(c Candidate, now time.Time) float64 {
	relevance, lifeAxis, swarmSize, freshness := ScoreParts(c, now)
	return weightRelevance*relevance +
		weightLifeAxis*lifeAxis +
		weightSwarmSize*swarmSize +
		weightFreshness*freshness
}

// Score 计算候选的排序分，供流式产出时逐条打分。
func Score(c Candidate, now time.Time) float64 {
	return score(c, now)
}

// ScoreParts 返回排序分的各分项，供界面把分数拆开显示。
func ScoreParts(c Candidate, now time.Time) (relevance, lifeAxis, swarmSize, freshness float64) {
	peers := float64(CrediblePeers(&c))
	conf := c.Confidence
	return c.Relevance,
		lifeAxisPrior + (1-lifeAxisPrior)*math.Pow(conf, 1.6)*sat(peers),
		math.Min(1, math.Log2(1+peers)/math.Log2(swarmReference)) * conf,
		freshnessOf(c, now)
}

// sat 是对数饱和函数，在 saturationPeers 处取到 1。
func sat(p float64) float64 {
	if p <= 0 {
		return 0
	}
	v := math.Log2(1+p) / math.Log2(saturationPeers)
	if v > 1 {
		return 1
	}
	return v
}

// freshnessOf 返回资源新鲜度，发布日期未知时取中性偏低的值。
func freshnessOf(c Candidate, now time.Time) float64 {
	age := ageInDays(&c, now)
	if age < 0 {
		return 0.30
	}
	return math.Exp(-age / freshnessTauDays)
}

// clusterKeyFor 按指定粒度生成聚类键。
func clusterKeyFor(title, mode string) string {
	switch mode {
	case "none":
		return ""
	case "full":
		return ResolutionTier(title) + "|" + LangKey(title) + "|" + sourceDetail(title) + "|" + ContentKey(title)
	default:
		return ClusterKey(title)
	}
}

// sourceDetail 提取画质之外的来源特征（蓝光/压制源/编码），用于 full 粒度聚类。
func sourceDetail(title string) string {
	var parts []string
	for _, tok := range ClassifyTokens(title) {
		if tok.Class != TokenQuality || isResolutionToken(tok.Text) {
			continue
		}
		parts = append(parts, tok.Text)
	}
	sort.Strings(parts)
	return strings.Join(parts, ".")
}

// isResolutionToken 判断词元是否为分辨率标记。
func isResolutionToken(text string) bool {
	switch text {
	case "2160p", "1080p", "1080i", "720p", "576p", "480p", "4k", "8k", "uhd":
		return true
	}
	return false
}
