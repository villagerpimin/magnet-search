package liveness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/httpx"
)

const (
	// detectTimeout 是能力探测的单次尝试超时。
	detectTimeout = 3 * time.Second
	// detectMaxTargets 是每种通道最多尝试的目标数量，避免探测本身变成一次长等待。
	detectMaxTargets = 3
)

// detectInfoHash 是能力探测使用的 info hash。
// 只需要一个格式合法的哈希，用它换取 tracker 或 DHT 节点的应答即可。
const detectInfoHash = "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"

// Capability 描述当前机器上各探测通道的可用性。
// 上层据此决定是走真实网络探测，还是退回基于元数据的启发式判定。
type Capability struct {
	// TrackerHTTP 表示 HTTP/HTTPS tracker 探测可用。
	TrackerHTTP bool
	// TrackerUDP 表示 UDP tracker 探测可用。
	TrackerUDP bool
	// DHT 表示 DHT 探测可用。
	DHT bool
	// TrackerHTTPReason 说明 HTTP 通道的结论。
	TrackerHTTPReason string
	// TrackerUDPReason 说明 UDP 通道的结论。
	TrackerUDPReason string
	// DHTReason 说明 DHT 通道的结论。
	DHTReason string
}

// Detect 实测一次网络探测，判断各通道是否真的可用。
// 三个通道并行探测，每个目标都有硬性超时，因此本函数不会长时间挂起。
// 在被本地 TUN 代理拦截出网的环境里，通常会如实报告"全部不可用"。
func Detect(ctx context.Context, cfg Config) Capability {
	// 能力探测关心的是"这台机器能不能出网"，因此未指定目标时用内置的公共 tracker。
	if len(cfg.HTTPTrackers) == 0 {
		cfg.HTTPTrackers = DefaultHTTPTrackers
	}
	if len(cfg.UDPTrackers) == 0 {
		cfg.UDPTrackers = DefaultUDPTrackers
	}
	if len(cfg.DHTNodes) == 0 {
		cfg.DHTNodes = DefaultDHTNodes
	}

	timeout := detectTimeout
	if cfg.Timeout > 0 && cfg.Timeout < timeout {
		timeout = cfg.Timeout
	}

	var (
		cap Capability
		wg  sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		cap.TrackerHTTP, cap.TrackerHTTPReason = detectHTTP(ctx, cfg, timeout)
	}()
	go func() {
		defer wg.Done()
		cap.TrackerUDP, cap.TrackerUDPReason = detectUDP(ctx, cfg, timeout)
	}()
	go func() {
		defer wg.Done()
		cap.DHT, cap.DHTReason = detectDHT(ctx, cfg, timeout)
	}()
	wg.Wait()
	return cap
}

// detectHTTP 检测 HTTP/HTTPS tracker 是否可达。
func detectHTTP(ctx context.Context, cfg Config, timeout time.Duration) (bool, string) {
	targets := cfg.HTTPTrackers
	if len(targets) == 0 {
		return false, "未配置 HTTP tracker"
	}
	client := cfg.Client
	if client == nil {
		c, err := httpx.New(httpx.Options{Timeout: timeout})
		if err != nil {
			return false, fmt.Sprintf("创建 HTTP 客户端失败: %v", err)
		}
		client = c
	}
	peerID := cfg.PeerID
	if peerID == "" {
		peerID = newPeerID()
	}

	var errs []string
	for i, base := range targets {
		if i >= detectMaxTargets {
			break
		}
		tracker, err := newHTTPTracker(base, client, peerID)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		err = tracker.reachable(probeCtx)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", base, shortError(err)))
			continue
		}
		return true, fmt.Sprintf("tracker %s 可达", base)
	}
	return false, "所有 HTTP tracker 均不可达: " + strings.Join(errs, "; ")
}

// detectUDP 检测 UDP tracker 是否可达。
func detectUDP(ctx context.Context, cfg Config, timeout time.Duration) (bool, string) {
	targets := cfg.UDPTrackers
	if len(targets) == 0 {
		return false, "未配置 UDP tracker"
	}

	var errs []string
	for i, base := range targets {
		if i >= detectMaxTargets {
			break
		}
		tracker, err := newUDPTracker(base, timeout)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		_, _, err = tracker.Scrape(probeCtx, detectInfoHash)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", base, shortError(err)))
			continue
		}
		return true, fmt.Sprintf("tracker %s 可达", base)
	}
	return false, "所有 UDP tracker 均不可达: " + strings.Join(errs, "; ")
}

// detectDHT 检测 DHT 引导节点是否可达。
func detectDHT(ctx context.Context, cfg Config, timeout time.Duration) (bool, string) {
	nodes := cfg.DHTNodes
	if len(nodes) == 0 {
		return false, "未配置 DHT 引导节点"
	}
	prober := newDHTProber(nodes[:min(len(nodes), detectMaxTargets)], timeout)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := prober.Peers(probeCtx, detectInfoHash); err != nil {
		return false, fmt.Sprintf("DHT 节点不可达: %s", shortError(err))
	}
	return true, "DHT 节点可达"
}
