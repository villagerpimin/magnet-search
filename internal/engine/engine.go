// Package engine 定义搜索引擎适配器的统一接口、区域标签与并发调度。
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/model"
)

// Region 标记引擎所在网络区域，用于决定代理路由顺序。
type Region string

const (
	// RegionAny 不确定区域，默认直连优先
	RegionAny Region = "any"
	// RegionDomestic 境内直连通常更快的引擎
	RegionDomestic Region = "domestic"
	// RegionOverseas 境外引擎，通常需要代理才能访问
	RegionOverseas Region = "overseas"
)

// Options 是单次搜索的参数。
type Options struct {
	// Query 搜索关键词
	Query string
	// Limit 单引擎最多返回条数，0 表示由引擎自行决定
	Limit int
	// Category 分类过滤，如 video、audio、software，空表示全部
	Category string
	// Debug 输出引擎内部调试信息
	Debug bool
	// Timeout 单个引擎的墙钟预算，0 表示使用默认值
	Timeout time.Duration
}

// Engine 是所有搜索引擎适配器实现的接口。
type Engine interface {
	// Name 返回引擎唯一标识
	Name() string
	// Description 返回一句话描述
	Description() string
	// Search 执行搜索并返回归一化结果
	Search(ctx context.Context, opts Options) ([]model.Result, error)
}

// Factory 用给定的 HTTP 客户端构造引擎实例。
// CLI 依据引擎的区域与代理配置注入不同客户端，从而实现按引擎分流。
type Factory func(c ClientProvider) Engine

// ClientProvider 是适配器实际依赖的 HTTP 客户端能力，便于测试替换。
type ClientProvider interface {
	// Get 发起 GET 请求并返回响应体
	Get(ctx context.Context, url string) ([]byte, error)
	// GetString 发起 GET 请求并返回字符串响应体
	GetString(ctx context.Context, url string) (string, error)
}

// Spec 描述一个引擎的静态信息与构造方式。
type Spec struct {
	// Name 引擎唯一标识
	Name string
	// Description 一句话描述
	Description string
	// Region 网络区域，决定代理优先顺序
	Region Region
	// New 构造引擎实例
	New Factory
}

// Failure 记录某个引擎的失败原因。
type Failure struct {
	// Engine 引擎名
	Engine string
	// Err 失败原因
	Err error
}

// Error 实现 error 接口。
func (f Failure) Error() string {
	return fmt.Sprintf("%s: %v", f.Engine, f.Err)
}

// Registry 维护可用引擎列表。
type Registry struct {
	mu    sync.RWMutex
	specs []Spec
}

// NewRegistry 创建空的引擎注册表。
func NewRegistry() *Registry {
	return &Registry{}
}

// Add 注册一个引擎。
func (r *Registry) Add(s Spec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, s)
}

// Specs 返回全部引擎描述。
func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, len(r.specs))
	copy(out, r.specs)
	return out
}

// Names 返回全部引擎名，按字母序排列。
func (r *Registry) Names() []string {
	var out []string
	for _, s := range r.Specs() {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

// Select 按名字筛选引擎；names 为空表示全部。
func (r *Registry) Select(names []string) ([]Spec, error) {
	all := r.Specs()
	if len(names) == 0 {
		return all, nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	var out []Spec
	for _, s := range all {
		if want[s.Name] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有匹配的引擎，可用引擎: %v", r.Names())
	}
	return out, nil
}

// SearchResult 是并发搜索的汇总输出。
type SearchResult struct {
	// Results 全部引擎返回的原始结果（未去重）
	Results []model.Result
	// Failures 各引擎的失败信息
	Failures []Failure
	// PerEngine 每个引擎贡献的条数
	PerEngine map[string]int
}

// defaultEngineTimeout 是单个引擎的默认墙钟预算。
const defaultEngineTimeout = 25 * time.Second

// Search 并发调用所有引擎，任一引擎失败不影响其他引擎。
func Search(ctx context.Context, engines []Engine, opts Options) SearchResult {
	return SearchStream(ctx, engines, opts, nil)
}

// EngineCallback 在单个引擎返回时被调用。
// 回调在串行化的上下文中执行，因此可以安全地修改外部状态。
type EngineCallback func(name string, results []model.Result, err error)

// SearchStream 与 Search 相同，但每个引擎一返回就立刻回调。
// 上层据此可以边收边出结果，不必空等最慢的那个引擎。
func SearchStream(ctx context.Context, engines []Engine, opts Options, onEngine EngineCallback) SearchResult {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultEngineTimeout
	}
	var (
		mu        sync.Mutex
		results   []model.Result
		failures  []Failure
		perEngine = make(map[string]int)
		wg        sync.WaitGroup
	)

	for _, e := range engines {
		wg.Add(1)
		go func(e Engine) {
			defer wg.Done()
			engineCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			res, err := e.Search(engineCtx, opts)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, Failure{Engine: e.Name(), Err: err})
			} else {
				results = append(results, res...)
				perEngine[e.Name()] = len(res)
			}
			if onEngine != nil {
				onEngine(e.Name(), res, err)
			}
		}(e)
	}
	wg.Wait()

	return SearchResult{Results: results, Failures: failures, PerEngine: perEngine}
}
