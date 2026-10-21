// Package server 提供磁力聚合器的本地 Web 界面与 JSON 接口。
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/aggregate"
	"github.com/pigzhu/magnet-search/internal/app"
	"github.com/pigzhu/magnet-search/internal/engine"
)

//go:embed web
var webFS embed.FS

// 分页与结果池的参数。
const (
	// maxPerPage 单页最多显示多少条
	maxPerPage = 100
	// poolPagesFactor 结果池是每页条数的多少倍
	poolPagesFactor = 5
	// maxPoolSize 结果池上限
	maxPoolSize = 200
)

// Server 是 Web 服务。
type Server struct {
	// defaults 是请求未显式指定时使用的默认参数
	defaults app.Options
	// engineSpecs 供界面展示的引擎清单
	engineSpecs []engine.Spec
	// cache 缓存整份排序结果，翻页时直接切片
	cache *searchCache
	// settings 界面上可调整的检索设置
	settings Settings
	// settingsMu 保护 settings
	settingsMu sync.RWMutex
}

// Settings 是界面上可调整的检索设置。
type Settings struct {
	// Proxy 代理地址，空表示不使用代理
	Proxy string `json:"proxy"`
	// ProxyScope 代理作用范围：region 只代理境外引擎，all 代理全部引擎
	ProxyScope string `json:"proxy_scope"`
}

// New 创建 Web 服务。
func New(defaults app.Options) *Server {
	reg := engine.NewRegistry()
	engine.RegisterAll(reg)
	return &Server{
		defaults:    defaults,
		engineSpecs: reg.Specs(),
		cache:       newSearchCache(),
		settings:    Settings{ProxyScope: app.ProxyScopeRegion},
	}
}

// Handler 返回 HTTP 处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(fmt.Sprintf("内嵌静态资源不可用: %v", err))
	}
	mux.Handle("/", http.FileServer(http.FS(assets)))

	mux.HandleFunc("/api/engines", s.handleEngines)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/check", s.handleCheck)

	return mux
}

// ListenAndServe 启动服务直到 ctx 结束。
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// engineInfo 是引擎的展示信息。
type engineInfo struct {
	// Name 引擎名
	Name string `json:"name"`
	// Region 网络区域
	Region string `json:"region"`
	// Description 一句话描述
	Description string `json:"description"`
}

// handleEngines 返回引擎清单。
func (s *Server) handleEngines(w http.ResponseWriter, r *http.Request) {
	list := make([]engineInfo, 0, len(s.engineSpecs))
	for _, spec := range s.engineSpecs {
		list = append(list, engineInfo{
			Name:        spec.Name,
			Region:      string(spec.Region),
			Description: spec.Description,
		})
	}
	writeJSON(w, http.StatusOK, list)
}

// handleSettings 读取或更新界面上的检索设置。
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.settingsMu.RLock()
		cur := s.settings
		s.settingsMu.RUnlock()
		writeJSON(w, http.StatusOK, cur)
	case http.MethodPost:
		var incoming Settings
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "设置格式不正确: " + err.Error()})
			return
		}
		if incoming.ProxyScope != app.ProxyScopeAll {
			incoming.ProxyScope = app.ProxyScopeRegion
		}
		incoming.Proxy = strings.TrimSpace(incoming.Proxy)
		s.settingsMu.Lock()
		s.settings = incoming
		s.settingsMu.Unlock()
		// 设置变了，之前的检索缓存不再适用
		s.cache.clear()
		writeJSON(w, http.StatusOK, incoming)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 GET 与 POST"})
	}
}

// engineHealth 是单个引擎的自检结果。
type engineHealth struct {
	engineInfo
	// OK 引擎是否可用
	OK bool `json:"ok"`
	// Count 冒烟查询返回的条数
	Count int `json:"count"`
	// ElapsedMS 耗时（毫秒）
	ElapsedMS int64 `json:"elapsed_ms"`
	// Error 失败原因
	Error string `json:"error,omitempty"`
}

// handleCheck 逐个测试引擎在当前网络下是否可用。
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	opts := s.defaults
	if v := r.URL.Query().Get("proxy"); v != "" {
		opts.Proxy = splitList(v)
	}

	type result struct {
		health engineHealth
	}
	results := make([]engineHealth, 0, len(s.engineSpecs))

	for _, spec := range s.engineSpecs {
		h := engineHealth{engineInfo: engineInfo{
			Name:        spec.Name,
			Region:      string(spec.Region),
			Description: spec.Description,
		}}
		e, err := buildProbeEngine(spec, opts)
		if err != nil {
			h.Error = err.Error()
			results = append(results, h)
			continue
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		res, err := e.Search(ctx, engine.Options{Query: "ubuntu", Limit: 5})
		cancel()
		h.ElapsedMS = time.Since(start).Milliseconds()
		if err != nil {
			h.Error = err.Error()
		} else {
			h.OK = true
			h.Count = len(res)
		}
		results = append(results, h)
	}

	writeJSON(w, http.StatusOK, map[string]any{"engines": results})
}

// searchResult 是一条搜索结果。
type searchResult struct {
	// Title 标题
	Title string `json:"title"`
	// InfoHash 40 位十六进制哈希
	InfoHash string `json:"info_hash"`
	// Magnet 磁力链接
	Magnet string `json:"magnet"`
	// Size 易读大小
	Size string `json:"size"`
	// SizeBytes 字节数
	SizeBytes int64 `json:"size_bytes"`
	// Seeders 引擎宣称的做种数
	Seeders int `json:"seeders"`
	// Leechers 引擎宣称的下载数
	Leechers int `json:"leechers"`
	// PublishDate 发布时间
	PublishDate string `json:"publish_date,omitempty"`
	// Sources 命中该资源的引擎
	Sources []string `json:"sources"`
	// Verdict 存活判定
	Verdict string `json:"verdict"`
	// Confidence 判定置信度
	Confidence float64 `json:"confidence"`
	// Relevance 相关度
	Relevance float64 `json:"relevance"`
	// Score 最终排序分
	Score float64 `json:"score"`
	// Peers 可信对等端数
	Peers int `json:"peers"`
	// Trackers 磁力链接中的 tracker
	Trackers []string `json:"trackers"`
	// DetailURL 详情页
	DetailURL string `json:"detail_url,omitempty"`
	// Parts 分数分项，供界面可视化
	Parts map[string]float64 `json:"parts"`
	// Rare 是否属于冷门资源
	Rare bool `json:"rare"`
}

// searchResponse 是搜索接口的响应。
type searchResponse struct {
	// Query 实际使用的关键词
	Query string `json:"query"`
	// Count 返回条数
	Count int `json:"count"`
	// Dropped 被过滤掉的条数
	Dropped int `json:"dropped"`
	// ElapsedMS 总耗时
	ElapsedMS int64 `json:"elapsed_ms"`
	// ProbeAvailable 是否启用了网络探测
	ProbeAvailable bool `json:"probe_available"`
	// ProbeReason 探测不可用时的说明
	ProbeReason string `json:"probe_reason,omitempty"`
	// PerEngine 各引擎贡献条数
	PerEngine map[string]int `json:"per_engine"`
	// Failures 引擎失败信息
	Failures []map[string]string `json:"failures,omitempty"`
	// Results 当前页的结果列表
	Results []searchResult `json:"results"`
	// Total 本次检索命中的结果总数
	Total int `json:"total"`
	// Page 当前页码，从 1 开始
	Page int `json:"page"`
	// PerPage 每页条数
	PerPage int `json:"per_page"`
	// TotalPages 总页数
	TotalPages int `json:"total_pages"`
	// PoolSize 本次检索实际取回的结果池大小
	PoolSize int `json:"pool_size"`
}

// handleSearch 执行搜索并返回 JSON。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := s.defaults
	opts.Query = strings.TrimSpace(q.Get("q"))
	if opts.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少搜索关键词"})
		return
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Limit = n
		}
	}
	if v := q.Get("engines"); v != "" {
		opts.Engines = splitList(v)
	}
	if v := q.Get("dead_filter"); v != "" {
		opts.DeadFilter = v
	}
	if v := q.Get("min_peers"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.MinPeers = n
		}
	}
	if v := q.Get("min_confidence"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			opts.MinConfidence = f
		}
	}
	if v := q.Get("rare_quota"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.RareQuota = n
		}
	}
	if v := q.Get("per_cluster"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.PerCluster = n
		}
	}
	if v := q.Get("cluster_key"); v != "" {
		opts.ClusterKey = v
	}
	if v := q.Get("category"); v != "" {
		opts.Category = v
	}
	// 请求里没显式给代理时，用界面上保存的设置
	s.settingsMu.RLock()
	saved := s.settings
	s.settingsMu.RUnlock()
	if v := q.Get("proxy"); v != "" {
		opts.Proxy = splitList(v)
	} else if saved.Proxy != "" {
		opts.Proxy = splitList(saved.Proxy)
	}
	if v := q.Get("proxy_scope"); v != "" {
		opts.ProxyScope = v
	} else if saved.ProxyScope != "" {
		opts.ProxyScope = saved.ProxyScope
	}
	if v := q.Get("probe"); v != "" {
		opts.Probe = v == "1" || v == "true"
	}

	perPage := 20
	if v := q.Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			perPage = n
		}
	}
	if perPage < 1 {
		perPage = 1
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	page := 1
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}

	// 结果池取每页条数的若干倍，够翻几页；翻到池子尽头再自动扩大重取。
	poolSize := perPage * poolPagesFactor
	if poolSize > maxPoolSize {
		poolSize = maxPoolSize
	}
	opts.Limit = poolSize
	opts.PerEngineLimit = poolSize

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	key := searchCacheKey(opts)
	out, cached := s.cache.get(key)
	if !cached {
		result, searchErr := app.Search(ctx, opts)
		if searchErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": searchErr.Error()})
			return
		}
		out = result
		s.cache.put(key, out)
	}

	total := len(out.Results)
	start, end, page, totalPages := paginate(total, perPage, page)
	pageItems := out.Results[start:end]

	resp := searchResponse{
		Query:          out.Query,
		Count:          len(pageItems),
		Dropped:        out.Dropped,
		ElapsedMS:      out.Elapsed.Milliseconds(),
		ProbeAvailable: out.ProbeAvailable,
		ProbeReason:    out.ProbeReason,
		PerEngine:      out.PerEngine,
		Results:        make([]searchResult, 0, len(pageItems)),
		Total:          total,
		Page:           page,
		PerPage:        perPage,
		TotalPages:     totalPages,
		PoolSize:       poolSize,
	}
	for _, f := range out.Failures {
		resp.Failures = append(resp.Failures, map[string]string{"engine": f.Engine, "error": f.Err.Error()})
	}

	now := time.Now()
	for _, c := range pageItems {
		relevance, lifeAxis, swarm, freshness := aggregate.ScoreParts(c, now)
		resp.Results = append(resp.Results, searchResult{
			Title:       c.Result.Title,
			InfoHash:    c.Result.InfoHash,
			Magnet:      c.Result.MagnetURI,
			Size:        humanSize(c.Result.SizeBytes),
			SizeBytes:   c.Result.SizeBytes,
			Seeders:     c.Result.Seeders,
			Leechers:    c.Result.Leechers,
			PublishDate: formatDate(c.Result.PublishDate),
			Sources:     c.Sources,
			Verdict:     string(c.Verdict),
			Confidence:  round3(c.Confidence),
			Relevance:   round3(c.Relevance),
			Score:       round3(c.Score),
			Peers:       aggregate.CrediblePeers(&c),
			Trackers:    magnetTrackers(c.Result.MagnetURI),
			DetailURL:   c.Result.URL,
			Parts: map[string]float64{
				"relevance": round3(relevance),
				"life":      round3(lifeAxis),
				"swarm":     round3(swarm),
				"freshness": round3(freshness),
			},
			Rare: c.Verdict == aggregate.VerdictCold,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// paginate 计算分页切片区间，并把越界的页码收敛到最后一页。
func paginate(total, perPage, page int) (start, end, curPage, totalPages int) {
	if perPage < 1 {
		perPage = 1
	}
	totalPages = (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start = (page - 1) * perPage
	if start > total {
		start = total
	}
	end = start + perPage
	if end > total {
		end = total
	}
	return start, end, page, totalPages
}

// searchCacheKey 依据影响结果的参数生成缓存键。
// 翻页与切换每页条数不改变键，因此翻页会命中同一份缓存。
func searchCacheKey(opts app.Options) string {
	return strings.Join([]string{
		opts.Query,
		strings.Join(opts.Engines, ","),
		opts.DeadFilter,
		opts.Category,
		opts.ClusterKey,
		strconv.Itoa(opts.MinPeers),
		strconv.FormatFloat(opts.MinConfidence, 'f', 3, 64),
		strconv.Itoa(opts.RareQuota),
		strconv.Itoa(opts.RareMaxPeers),
		strconv.Itoa(opts.PerCluster),
		strconv.FormatBool(opts.Probe),
	}, "|")
}

// writeJSON 写出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
