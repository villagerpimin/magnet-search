// Package app 汇总"搜索 → 合并 → 探测 → 排序"的完整流水线，供命令行与 Web 界面共用。
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/pigzhu/magnet-search/internal/aggregate"
	"github.com/pigzhu/magnet-search/internal/engine"
	"github.com/pigzhu/magnet-search/internal/httpx"
	"github.com/pigzhu/magnet-search/internal/liveness"
	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Options 是一次检索的完整参数。
type Options struct {
	// Query 搜索关键词
	Query string
	// Limit 输出条数，0 表示不限制
	Limit int
	// Engines 指定引擎名，空表示全部
	Engines []string
	// PerEngineLimit 单引擎取回条数，0 表示用默认值
	PerEngineLimit int
	// Category 分类过滤
	Category string
	// DeadFilter 死链处理：off / hide / mark / strict
	DeadFilter string
	// MinPeers 最低可信对等端数
	MinPeers int
	// MinConfidence 最低置信度
	MinConfidence float64
	// RareQuota 冷门席位，-1 表示按比例自动
	RareQuota int
	// RareMaxPeers 稀有的对等端上界
	RareMaxPeers int
	// PerCluster 每个聚类最多输出条数
	PerCluster int
	// ClusterKey 聚类粒度：resolution / full / none
	ClusterKey string
	// Probe 是否启用 tracker/DHT 存活探测
	Probe bool
	// Proxy 全局代理地址列表
	Proxy []string
	// ProxyScope 代理的作用范围：region 只代理境外引擎，all 代理全部引擎
	ProxyScope string
	// ProxyEngine 按引擎指定的代理
	ProxyEngine map[string]string
	// Timeout 单次 HTTP 请求超时
	Timeout time.Duration
	// EngineTimeout 单引擎墙钟预算
	EngineTimeout time.Duration
	// Retries 单次请求重试次数
	Retries int
	// SplitQuery 是否把查询拆成独立关键词分别检索
	SplitQuery bool
	// MaxQueryTerms 拆词检索时最多使用几个关键词，0 表示使用默认值
	MaxQueryTerms int
}

// Outcome 是一次检索的结果汇总。
type Outcome struct {
	// Query 实际使用的关键词
	Query string
	// Results 排序后的结果
	Results []aggregate.Candidate
	// Dropped 被过滤掉的数量
	Dropped int
	// PerEngine 各引擎贡献的条数
	PerEngine map[string]int
	// Failures 各引擎的失败信息
	Failures []engine.Failure
	// ProbeAvailable 是否真正启用了网络探测
	ProbeAvailable bool
	// ProbeReason 探测不可用时的说明
	ProbeReason string
	// Elapsed 总耗时
	Elapsed time.Duration
}

// 默认参数。
const (
	// DefaultLimit 默认输出条数
	DefaultLimit = 20
	// DefaultPerEngineLimit 默认每引擎取回条数
	DefaultPerEngineLimit = 50
	// DefaultRareMaxPeers 判定为稀有的对等端上界
	DefaultRareMaxPeers = 4
	// DefaultPerCluster 每个聚类默认最多输出条数
	DefaultPerCluster = 2
	// DefaultMaxQueryTerms 拆词检索默认最多使用几个关键词
	DefaultMaxQueryTerms = 4
)

// 代理作用范围。
const (
	// ProxyScopeRegion 只给境外引擎套代理，境内与通用引擎直连
	ProxyScopeRegion = "region"
	// ProxyScopeAll 给所有引擎套代理
	ProxyScopeAll = "all"
)

// withDefaults 补齐未设置的参数。
func (o *Options) withDefaults() {
	if o.Limit <= 0 {
		o.Limit = DefaultLimit
	}
	if o.PerEngineLimit <= 0 {
		o.PerEngineLimit = DefaultPerEngineLimit
	}
	if o.DeadFilter == "" {
		o.DeadFilter = "hide"
	}
	if o.RareQuota == 0 {
		o.RareQuota = -1
	}
	if o.RareMaxPeers <= 0 {
		o.RareMaxPeers = DefaultRareMaxPeers
	}
	if o.PerCluster <= 0 {
		o.PerCluster = DefaultPerCluster
	}
	if o.ClusterKey == "" {
		o.ClusterKey = "resolution"
	}
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	if o.EngineTimeout <= 0 {
		o.EngineTimeout = 25 * time.Second
	}
	if o.Retries < 0 {
		o.Retries = 0
	}
	if o.MaxQueryTerms <= 0 {
		o.MaxQueryTerms = DefaultMaxQueryTerms
	}
}

// Search 执行一次完整检索。
func Search(ctx context.Context, opts Options) (*Outcome, error) {
	opts.withDefaults()
	start := time.Now()

	reg := engine.NewRegistry()
	engine.RegisterAll(reg)
	specs, err := reg.Select(opts.Engines)
	if err != nil {
		return nil, err
	}

	engines := make([]engine.Engine, 0, len(specs))
	for _, spec := range specs {
		e, err := BuildEngine(spec, opts)
		if err != nil {
			continue
		}
		engines = append(engines, e)
	}
	if len(engines) == 0 {
		return nil, fmt.Errorf("没有可用引擎")
	}

	variants := []string{opts.Query}
	if opts.SplitQuery {
		variants = aggregate.QueryVariants(opts.Query, opts.MaxQueryTerms)
	}
	res := engine.SearchVariants(ctx, engines, engine.Options{
		Query:    opts.Query,
		Limit:    opts.PerEngineLimit,
		Category: opts.Category,
		Timeout:  opts.EngineTimeout,
	}, variants, nil)

	cands := aggregate.Merge(res.Results)

	out := &Outcome{
		Query:     opts.Query,
		PerEngine: res.PerEngine,
		Failures:  res.Failures,
	}

	if opts.Probe {
		live, reason, available := probeLiveness(ctx, opts, cands)
		out.ProbeAvailable = available
		out.ProbeReason = reason
		for i := range cands {
			if l, ok := live[cands[i].Result.InfoHash]; ok {
				cands[i].Live = l
			}
		}
	}

	ranked := aggregate.Rank(cands, aggregate.RankOptions{
		Query:         opts.Query,
		Limit:         opts.Limit,
		PerCluster:    opts.PerCluster,
		RareQuota:     opts.RareQuota,
		RareMaxPeers:  opts.RareMaxPeers,
		MinPeers:      opts.MinPeers,
		MinConfidence: opts.MinConfidence,
		DeadFilter:    opts.DeadFilter,
		ClusterKey:    opts.ClusterKey,
	})

	out.Results = ranked.Kept
	out.Dropped = len(ranked.Dropped)
	out.Elapsed = time.Since(start)
	return out, nil
}

// 事件类型。
const (
	// EventEngine 某个引擎返回了结果或失败
	EventEngine = "engine"
	// EventResult 一条已算好分数的候选结果
	EventResult = "result"
	// EventDone 全部引擎结束并完成存活探测后的最终结果
	EventDone = "done"
)

// Event 是流式检索过程中的一个事件。
type Event struct {
	// Kind 事件类型
	Kind string
	// Engine 引擎名
	Engine string
	// Count 该引擎返回的条数
	Count int
	// Err 引擎失败原因
	Err string
	// Result 单条候选结果
	Result *aggregate.Candidate
	// Outcome 全部完成后的汇总
	Outcome *Outcome
}

// SearchStream 执行检索，并在过程中逐条回调。
// 引擎一返回就把它的结果合并、打分并回调，界面可以边搜边出；
// 全部引擎结束后再做存活探测与最终排序，通过 EventDone 给出准确结果。
func SearchStream(ctx context.Context, opts Options, emit func(Event)) (*Outcome, error) {
	opts.withDefaults()
	start := time.Now()

	reg := engine.NewRegistry()
	engine.RegisterAll(reg)
	specs, err := reg.Select(opts.Engines)
	if err != nil {
		return nil, err
	}

	engines := make([]engine.Engine, 0, len(specs))
	for _, spec := range specs {
		e, err := BuildEngine(spec, opts)
		if err != nil {
			continue
		}
		engines = append(engines, e)
	}
	if len(engines) == 0 {
		return nil, fmt.Errorf("没有可用引擎")
	}

	acc := &merger{
		seen:          make(map[string]bool),
		deadFilter:    opts.DeadFilter,
		minPeers:      opts.MinPeers,
		minConfidence: opts.MinConfidence,
	}
	variants := []string{opts.Query}
	if opts.SplitQuery {
		variants = aggregate.QueryVariants(opts.Query, opts.MaxQueryTerms)
	}
	res := engine.SearchVariants(ctx, engines, engine.Options{
		Query:    opts.Query,
		Limit:    opts.PerEngineLimit,
		Category: opts.Category,
		Timeout:  opts.EngineTimeout,
	}, variants, func(name string, results []model.Result, searchErr error) {
		if searchErr != nil {
			emit(Event{Kind: EventEngine, Engine: name, Err: searchErr.Error()})
			return
		}
		emit(Event{Kind: EventEngine, Engine: name, Count: len(results)})
		for _, c := range acc.add(opts.Query, results) {
			candidate := c
			emit(Event{Kind: EventResult, Result: &candidate})
		}
	})

	cands := acc.candidates()
	out := &Outcome{
		Query:     opts.Query,
		PerEngine: res.PerEngine,
		Failures:  res.Failures,
	}

	if opts.Probe {
		live, reason, available := probeLiveness(ctx, opts, cands)
		out.ProbeAvailable = available
		out.ProbeReason = reason
		for i := range cands {
			if l, ok := live[cands[i].Result.InfoHash]; ok {
				cands[i].Live = l
			}
		}
	}

	ranked := aggregate.Rank(cands, aggregate.RankOptions{
		Query:         opts.Query,
		Limit:         opts.Limit,
		PerCluster:    opts.PerCluster,
		RareQuota:     opts.RareQuota,
		RareMaxPeers:  opts.RareMaxPeers,
		MinPeers:      opts.MinPeers,
		MinConfidence: opts.MinConfidence,
		DeadFilter:    opts.DeadFilter,
		ClusterKey:    opts.ClusterKey,
	})

	out.Results = ranked.Kept
	out.Dropped = len(ranked.Dropped)
	out.Elapsed = time.Since(start)
	return out, nil
}

// merger 跨引擎累积结果：按 info hash 去重，并为新到的结果算好分数。
type merger struct {
	// seen 已经产出过的 info hash
	seen map[string]bool
	// all 累积到的全部候选
	all []aggregate.Candidate
	// deadFilter 死链处理策略
	deadFilter string
	// minPeers 最少可信对等端
	minPeers int
	// minConfidence 最低置信度
	minConfidence float64
}

// add 合并一批结果，返回其中首次出现、需要立即产出的候选。
func (m *merger) add(query string, results []model.Result) []aggregate.Candidate {
	now := time.Now()
	var fresh []aggregate.Candidate
	for _, r := range results {
		hash, ok := magnet.NormalizeInfoHash(r.InfoHash)
		if !ok || magnet.IsNullInfoHash(hash) {
			continue
		}
		if m.seen[hash] {
			continue
		}
		m.seen[hash] = true
		r.InfoHash = hash
		if r.ListedAt.IsZero() {
			r.ListedAt = now
		}
		c := aggregate.Candidate{Result: r, Live: model.NewLiveness(hash), Sources: []string{r.Source}}
		rel := aggregate.ComputeRelevance(aggregate.RelevanceInput{
			Query: query,
			Title: r.Title,
			Rank:  r.Rank,
		})
		if !rel.Passed {
			// 相关度不达标的直接丢弃，不产出
			m.seen[hash] = false
			continue
		}
		c.Relevance = rel.Score
		c.Verdict, c.Confidence = aggregate.Judge(&c)
		c.Score = aggregate.Score(c, now)
		m.all = append(m.all, c)
		// 与最终判定保持一致：只产出通过死链、最少对等端、最低置信度过滤的条目
		if !aggregate.PassesFilters(c, m.deadFilter, m.minPeers, m.minConfidence) {
			continue
		}
		fresh = append(fresh, c)
	}
	return fresh
}

// candidates 返回累积到的全部候选。
func (m *merger) candidates() []aggregate.Candidate {
	return m.all
}

// BuildEngine 按引擎区域解析代理顺序并构造实例。
// 境外引擎优先走代理，境内或通用引擎优先直连，失败时再回退到另一条路径。
func BuildEngine(spec engine.Spec, opts Options) (engine.Engine, error) {
	chain := ProxyChain(spec, opts)
	if len(chain) == 0 {
		chain = []string{""}
	}

	client, err := httpx.New(httpx.Options{
		Proxies:        chain,
		Timeout:        opts.Timeout,
		Retries:        opts.Retries,
		ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return spec.New(client), nil
}

// ProxyChain 返回指定引擎的代理链，按优先级排列，客户端会依次尝试。
// 作用范围由 opts.ProxyScope 决定：
//   - region（默认）：境外引擎优先走代理，境内与通用引擎优先直连；
//   - all：所有引擎都优先走代理。
//
// 按引擎单独指定的代理（opts.ProxyEngine）优先级最高。
func ProxyChain(spec engine.Spec, opts Options) []string {
	chain := make([]string, 0, len(opts.Proxy)+2)
	switch {
	case opts.ProxyEngine[spec.Name] != "":
		chain = append(chain, opts.ProxyEngine[spec.Name])
		chain = append(chain, opts.Proxy...)
	case opts.ProxyScope == ProxyScopeAll:
		chain = append(chain, opts.Proxy...)
		chain = append(chain, "")
	case spec.Region == engine.RegionOverseas:
		chain = append(chain, opts.Proxy...)
		chain = append(chain, "")
	default:
		chain = append(chain, "")
		chain = append(chain, opts.Proxy...)
	}
	return chain
}

// probeLiveness 执行存活探测。
// 先实测本机出网能力：全部通道不可用时直接退回元数据判定，不浪费用户时间。
func probeLiveness(ctx context.Context, opts Options, cands []aggregate.Candidate) (map[string]model.Liveness, string, bool) {
	cfg := liveness.Config{
		HTTPTrackers:   liveness.DefaultHTTPTrackers,
		UDPTrackers:    liveness.DefaultUDPTrackers,
		DHTNodes:       liveness.DefaultDHTNodes,
		Timeout:        5 * time.Second,
		OverallTimeout: 20 * time.Second,
		MaxConcurrency: 32,
		EnableUDP:      true,
		EnableDHT:      true,
	}

	detectCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	cap := liveness.Detect(detectCtx, cfg)
	cancel()

	if !cap.TrackerHTTP && !cap.TrackerUDP && !cap.DHT {
		return nil, "本机无法访问 tracker/DHT，已退回基于引擎元数据的判定", false
	}
	cfg.EnableUDP = cap.TrackerUDP
	cfg.EnableDHT = cap.DHT
	if !cap.TrackerHTTP {
		cfg.HTTPTrackers = nil
	}

	hashes := make([]string, 0, len(cands))
	for _, c := range cands {
		hashes = append(hashes, c.Result.InfoHash)
	}
	return liveness.Probe(ctx, cfg, hashes), "", true
}
