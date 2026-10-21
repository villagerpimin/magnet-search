package liveness

import (
	"sync"

	"github.com/pigzhu/magnet-search/internal/model"
)

// accumulator 汇总单个 info hash 的多来源探测结果。
type accumulator struct {
	mu         sync.Mutex
	complete   int
	incomplete int
	dhtPeers   int
	trackers   []string
	errors     []string
}

// newAccumulator 建立一个全部指标不可用的累加器。
func newAccumulator() *accumulator {
	return &accumulator{
		complete:   model.Unknown,
		incomplete: model.Unknown,
		dhtPeers:   model.Unknown,
	}
}

// addTracker 合并一个 tracker 的结果。
//
// 合并规则（关键）：
//   - 多个 tracker 描述的是同一个 swarm 的不同视图，各自刷新频率不同，
//     因此取各来源报告值的最大值：最大的那个最接近"当前最活跃"的视图。
//   - 只有真正应答的 tracker 才会写进 Trackers，失败的只进 Errors。
func (a *accumulator) addTracker(url string, seeders, leechers int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if seeders > a.complete {
		a.complete = seeders
	}
	if leechers > a.incomplete {
		a.incomplete = leechers
	}
	for _, u := range a.trackers {
		if u == url {
			return
		}
	}
	a.trackers = append(a.trackers, url)
}

// addDHT 合并 DHT 观测到的对等端数量，同样取最大值。
func (a *accumulator) addDHT(peers int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if peers > a.dhtPeers {
		a.dhtPeers = peers
	}
}

// addError 记录一条错误摘要，最多保留 maxErrorEntries 条，过长的原文会被截断。
func (a *accumulator) addError(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.errors) >= maxErrorEntries {
		return
	}
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength] + "…"
	}
	a.errors = append(a.errors, msg)
}

// snapshot 导出累加结果。
// 没有任何来源应答时三个指标保持 model.Unknown，Trackers 为空，
// 也就是"网络没告诉我们"，而不是"网络说它是 0"。
func (a *accumulator) snapshot(infoHash string) model.Liveness {
	a.mu.Lock()
	defer a.mu.Unlock()
	live := model.NewLiveness(infoHash)
	live.TrackerComplete = a.complete
	live.TrackerIncomplete = a.incomplete
	live.DHTPeers = a.dhtPeers
	if len(a.trackers) > 0 {
		live.Trackers = append([]string(nil), a.trackers...)
	}
	if len(a.errors) > 0 {
		live.Errors = append([]string(nil), a.errors...)
	}
	return live
}
