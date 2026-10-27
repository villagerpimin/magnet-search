package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/aggregate"
	"github.com/pigzhu/magnet-search/internal/app"
	"github.com/pigzhu/magnet-search/internal/engine"
	"github.com/pigzhu/magnet-search/internal/model"
)

// sseWriter 以 text/event-stream 推送事件。
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// newSSEWriter 准备一个流式响应。
func newSSEWriter(w http.ResponseWriter) (*sseWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("当前连接不支持流式响应")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// 关闭反向代理的缓冲，否则事件会被攒住一次性下发
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &sseWriter{w: w, flusher: flusher}, nil
}

// send 推送一个事件并立即冲刷。
func (s *sseWriter) send(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload)
	s.flusher.Flush()
}

// newSearchResult 把候选结果转成界面用的结构。
func newSearchResult(c aggregate.Candidate, now time.Time) searchResult {
	relevance, lifeAxis, swarm, freshness := aggregate.ScoreParts(c, now)
	return searchResult{
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
	}
}

// handleSearchStream 以 SSE 推送检索过程：引擎一条条返回，结果一条条产出。
func (s *Server) handleSearchStream(w http.ResponseWriter, r *http.Request) {
	opts, perPage, page, err := s.searchOptions(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sse, err := newSSEWriter(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	// 同一组参数的检索结果已经缓存时直接返回，避免翻页或换每页条数时重跑网络检索
	key := searchCacheKey(opts)
	if out, ok := s.cache.get(key); ok {
		if page == 1 {
			s.recordHistory(r, opts.Query)
		}
		sse.send("done", buildSearchResponse(out, perPage, page, time.Now()))
		return
	}

	now := time.Now()
	out, searchErr := app.SearchStream(ctx, opts, func(ev app.Event) {
		switch ev.Kind {
		case app.EventEngine:
			sse.send("engine", map[string]any{
				"engine": ev.Engine,
				"count":  ev.Count,
				"error":  ev.Err,
			})
		case app.EventResult:
			if ev.Result == nil {
				return
			}
			sse.send("result", newSearchResult(*ev.Result, now))
		}
	})
	if searchErr != nil {
		sse.send("error", map[string]string{"error": searchErr.Error()})
		return
	}

	// 客户端中途断开时结果可能不完整，不能写进缓存
	if r.Context().Err() == nil {
		s.cache.put(key, out)
	}

	// 全部引擎结束并完成存活探测后，给出最终的排序与分页信息。
	// 先记录检索再发 done，前端收到 done 后立刻刷新搜索历史时才能读到这一条。
	final := buildSearchResponse(out, perPage, page, time.Now())
	s.logSearch(r, opts, len(out.Results), out.Elapsed)
	sse.send("done", final)
}

// engineStatus 是单个引擎的自检状态。
type engineStatus struct {
	// Name 引擎名
	Name string `json:"name"`
	// Region 网络区域
	Region string `json:"region"`
	// Description 一句话描述
	Description string `json:"description"`
	// OK 是否可用
	OK bool `json:"ok"`
	// Count 冒烟查询返回的条数
	Count int `json:"count"`
	// ElapsedMS 耗时（毫秒）
	ElapsedMS int64 `json:"elapsed_ms"`
	// Error 失败原因
	Error string `json:"error,omitempty"`
}

// handleCheckStream 以 SSE 推送引擎自检：哪个引擎先出结果就先显示哪个。
func (s *Server) handleCheckStream(w http.ResponseWriter, r *http.Request) {
	sse, err := newSSEWriter(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	opts := s.defaults
	if v := r.URL.Query().Get("proxy"); v != "" {
		opts.Proxy = splitList(v)
	}
	s.settingsMu.RLock()
	saved := s.settings
	s.settingsMu.RUnlock()
	if len(opts.Proxy) == 0 && saved.Proxy != "" {
		opts.Proxy = splitList(saved.Proxy)
	}

	reg := engine.NewRegistry()
	engine.RegisterAll(reg)
	specs, err := reg.Select(opts.Engines)
	if err != nil {
		sse.send("error", map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	var (
		mu      sync.Mutex
		okCount int
		total   = len(specs)
	)

	engines := make([]engine.Engine, 0, len(specs))
	meta := make(map[string]engine.Spec, len(specs))
	for _, spec := range specs {
		e, err := app.BuildEngine(spec, opts)
		if err != nil {
			sse.send("engine", engineStatus{
				Name:        spec.Name,
				Region:      string(spec.Region),
				Description: spec.Description,
				Error:       err.Error(),
			})
			continue
		}
		engines = append(engines, e)
		meta[spec.Name] = spec
	}

	engine.SearchStream(ctx, engines, engine.Options{Query: "ubuntu", Limit: 5}, func(name string, results []model.Result, err error) {
		spec := meta[name]
		status := engineStatus{
			Name:        name,
			Region:      string(spec.Region),
			Description: spec.Description,
			Count:       len(results),
		}
		if err != nil {
			status.Error = err.Error()
		} else {
			status.OK = true
		}
		mu.Lock()
		if status.OK {
			okCount++
		}
		mu.Unlock()
		sse.send("engine", status)
	})

	sse.send("done", map[string]any{"ok": okCount, "total": total})
}
