package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/app"
	"github.com/pigzhu/magnet-search/internal/engine"
	"github.com/pigzhu/magnet-search/internal/magnet"
)

// buildProbeEngine 为自检构造一个引擎实例。
func buildProbeEngine(spec engine.Spec, opts app.Options) (engine.Engine, error) {
	return app.BuildEngine(spec, opts)
}

// splitList 按逗号切分并去除空白。
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// humanSize 把字节数格式化为易读形式。
func humanSize(n int64) string {
	const unit = 1024
	if n <= 0 {
		return "-"
	}
	exp := 0
	f := float64(n)
	for f >= unit && exp < 5 {
		f /= unit
		exp++
	}
	if exp == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %cB", f, "BKMGTP"[exp])
}

// formatDate 格式化发布时间，未知时返回空串。
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// round3 保留三位小数。
func round3(f float64) float64 {
	return float64(int64(f*1000+0.5)) / 1000
}

// magnetTrackers 提取磁力链接中的 tracker 地址。
func magnetTrackers(uri string) []string {
	return magnet.Trackers(uri)
}

// parseBool 解析布尔查询参数。
func parseBool(s string) bool {
	v, _ := strconv.ParseBool(s)
	return v
}

// 自检（/api/check）的参数。
const (
	// probeTimeout 单引擎自检的墙钟预算
	probeTimeout = 25 * time.Second
	// probeLimit 自检时每个引擎最多取回的条数
	probeLimit = 5
)

// probeEngines 并发跑一遍冒烟查询，每完成一个引擎回调一次状态。
// 每个引擎用各自的探针词（Spec.ProbeTerm），因此不能直接复用 engine.SearchStream
// （它只接受一份 Options）。
func probeEngines(ctx context.Context, specs []engine.Spec, built map[string]engine.Engine, onStatus func(engineStatus)) {
	var wg sync.WaitGroup
	for _, spec := range specs {
		e, ok := built[spec.Name]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(spec engine.Spec, e engine.Engine) {
			defer wg.Done()
			started := time.Now()
			searchCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			results, err := e.Search(searchCtx, engine.Options{Query: spec.ProbeTerm(), Limit: probeLimit})
			status := engineStatus{
				Name:        spec.Name,
				Region:      string(spec.Region),
				Description: spec.Description,
				Count:       len(results),
				ElapsedMS:   time.Since(started).Milliseconds(),
			}
			if err != nil {
				status.Error = err.Error()
			} else {
				status.OK = true
			}
			onStatus(status)
		}(spec, e)
	}
	wg.Wait()
}
