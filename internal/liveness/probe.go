// Package liveness 实现磁力链的存活探测：用真实的 tracker 与 DHT 报文判断资源是否还活着。
//
// 这个包只产出原始探测数据（做种数、下载数、DHT 对等端数），
// 不做存活判定、不打分、不排序，判定由上层消费 model.Liveness 完成。
// 最重要的一条约定是：测不到（model.Unknown，-1）与测到是 0 严格区分。
package liveness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/httpx"
	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

const (
	// defaultTimeout 是单次探测的默认超时。
	defaultTimeout = 5 * time.Second
	// defaultMaxConcurrency 是默认的同时在途探测数。
	defaultMaxConcurrency = 32
	// maxErrorEntries 是单个 info hash 最多保留的错误条数。
	maxErrorEntries = 5
	// maxErrorLength 是单条错误摘要的最大长度，错误原文可能带很长的 URL。
	maxErrorLength = 200
	// maxShortError 是能力探测原因里单条错误的截断长度。
	maxShortError = 120
)

// Config 是存活探测的配置。
// 三个来源列表都只认显式配置：留空表示不启用该通道，不会偷偷去访问内置的公共 tracker。
type Config struct {
	// HTTPTrackers HTTP/HTTPS tracker 地址列表。
	HTTPTrackers []string
	// UDPTrackers UDP tracker 地址列表，形如 udp://host:port/announce。
	UDPTrackers []string
	// DHTNodes DHT 引导节点列表，格式为 host:port。
	DHTNodes []string
	// Timeout 单次探测的超时时间，0 表示使用默认值。
	Timeout time.Duration
	// OverallTimeout 整体超时，0 表示完全由调用方的 ctx 决定。
	OverallTimeout time.Duration
	// MaxConcurrency 同时在途的探测数上限，0 表示使用默认值。
	MaxConcurrency int
	// EnableUDP 是否启用 UDP tracker 探测。
	EnableUDP bool
	// EnableDHT 是否启用 DHT 探测。
	EnableDHT bool
	// Client 共享的 HTTP 客户端，为 nil 时内部自建直连客户端。
	// 注意共享客户端对同一 host 有最小请求间隔，批量探测前可以调低这个间隔。
	Client *httpx.Client
	// PeerID announce 使用的 peer id，为空时自动生成。
	PeerID string
}

// trackerProbe 是单个 tracker 的探测能力抽象，便于测试注入假实现。
type trackerProbe interface {
	// Scrape 查询 info hash 的做种数与下载数。
	Scrape(ctx context.Context, infoHash string) (seeders, leechers int, err error)
	// URL 返回 tracker 地址。
	URL() string
}

// dhtProbe 是 DHT 探测能力抽象，便于测试注入假实现。
type dhtProbe interface {
	// Peers 查询 info hash 的对等端数量。
	Peers(ctx context.Context, infoHash string) (int, error)
}

// Prober 负责并发探测多个 info hash 的存活状态。
// 建议构造一次后重复使用，内部的 tracker 连接与客户端都可复用。
type Prober struct {
	cfg      Config
	trackers []trackerProbe
	dht      []dhtProbe
}

// NewProber 按配置创建探测器。
// 非法地址的 tracker 会被跳过，不会让整个探测器创建失败。
func NewProber(cfg Config) *Prober {
	p := &Prober{cfg: cfg}
	p.trackers, p.dht = buildProbes(cfg)
	return p
}

// buildProbes 依据配置构造各路探测器。
func buildProbes(cfg Config) ([]trackerProbe, []dhtProbe) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	peerID := cfg.PeerID
	if peerID == "" {
		peerID = newPeerID()
	}
	client := cfg.Client
	if client == nil {
		c, err := httpx.New(httpx.Options{Timeout: timeout})
		if err == nil {
			client = c
		}
	}

	var trackers []trackerProbe
	for _, base := range cfg.HTTPTrackers {
		t, err := newHTTPTracker(base, client, peerID)
		if err != nil {
			continue
		}
		trackers = append(trackers, t)
	}
	if cfg.EnableUDP {
		for _, base := range cfg.UDPTrackers {
			t, err := newUDPTracker(base, timeout)
			if err != nil {
				continue
			}
			trackers = append(trackers, t)
		}
	}

	var dhts []dhtProbe
	if cfg.EnableDHT {
		dhts = append(dhts, newDHTProber(cfg.DHTNodes, timeout))
	}
	return trackers, dhts
}

// Probe 用默认配置探测一批 info hash，返回按 info hash 索引的存活数据。
func Probe(ctx context.Context, cfg Config, hashes []string) map[string]model.Liveness {
	return NewProber(cfg).Probe(ctx, hashes)
}

// Probe 并发探测所有 info hash，返回按 info hash 索引的存活数据。
// 单个 tracker 或 DHT 节点失败只会被记录进 Errors，不会影响其它来源，也不会让整轮失败。
func (p *Prober) Probe(ctx context.Context, hashes []string) map[string]model.Liveness {
	if p.cfg.OverallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.OverallTimeout)
		defer cancel()
	}

	accs, order := p.newAccumulators(hashes)
	tasks := p.buildTasks(order)

	concurrency := p.cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = defaultMaxConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, t := range tasks {
		wg.Add(1)
		go func(t probeTask) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			p.runTask(ctx, accs[t.hash], t)
		}(t)
	}
	wg.Wait()

	result := make(map[string]model.Liveness, len(order))
	for _, h := range order {
		result[h] = accs[h].snapshot(h)
	}
	return result
}

// newAccumulators 为每个 info hash 建立累加器，并归一化哈希格式。
func (p *Prober) newAccumulators(hashes []string) (map[string]*accumulator, []string) {
	accs := make(map[string]*accumulator, len(hashes))
	order := make([]string, 0, len(hashes))
	for _, h := range hashes {
		norm, ok := magnet.NormalizeInfoHash(h)
		if !ok {
			norm = strings.ToLower(strings.TrimSpace(h))
		}
		if _, exists := accs[norm]; exists {
			continue
		}
		acc := newAccumulator()
		if !ok {
			acc.addError(fmt.Sprintf("info hash 非法: %s", h))
		}
		accs[norm] = acc
		order = append(order, norm)
	}
	return accs, order
}

// probeTask 是一次具体的探测任务。
type probeTask struct {
	hash    string
	tracker trackerProbe
	dht     dhtProbe
}

// buildTasks 展开 hash × 来源的任务矩阵。
func (p *Prober) buildTasks(order []string) []probeTask {
	tasks := make([]probeTask, 0, len(order)*(len(p.trackers)+len(p.dht)))
	for _, h := range order {
		for _, tr := range p.trackers {
			tasks = append(tasks, probeTask{hash: h, tracker: tr})
		}
		for _, d := range p.dht {
			tasks = append(tasks, probeTask{hash: h, dht: d})
		}
	}
	return tasks
}

// runTask 执行一次探测并把结果合并进累加器。
func (p *Prober) runTask(ctx context.Context, acc *accumulator, t probeTask) {
	timeout := p.cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if t.dht != nil {
		peers, err := t.dht.Peers(probeCtx, t.hash)
		if err != nil {
			acc.addError(fmt.Sprintf("DHT: %v", err))
			return
		}
		acc.addDHT(peers)
		return
	}

	seeders, leechers, err := t.tracker.Scrape(probeCtx, t.hash)
	if err != nil {
		acc.addError(fmt.Sprintf("tracker %s: %v", t.tracker.URL(), err))
		return
	}
	acc.addTracker(t.tracker.URL(), seeders, leechers)
}
