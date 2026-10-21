package liveness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// dhtProber 通过 BEP 5 的 get_peers 查询 DHT 中某个 info hash 的对等端数量。
type dhtProber struct {
	nodes   []string
	timeout time.Duration
}

// newDHTProber 构造 DHT 探测器。
func newDHTProber(nodes []string, timeout time.Duration) *dhtProber {
	return &dhtProber{nodes: nodes, timeout: timeout}
}

// Peers 查询 DHT 中该 info hash 的对等端数量。
// 多个引导节点的结果取最大值；全部节点无响应时才返回错误。
func (d *dhtProber) Peers(ctx context.Context, infoHash string) (int, error) {
	raw, err := decodeInfoHash(infoHash)
	if err != nil {
		return 0, err
	}
	if len(d.nodes) == 0 {
		return 0, errors.New("未配置 DHT 引导节点")
	}
	type nodeResult struct {
		peers int
		err   error
	}
	results := make([]nodeResult, len(d.nodes))
	var wg sync.WaitGroup
	for i, node := range d.nodes {
		wg.Add(1)
		go func(i int, node string) {
			defer wg.Done()
			peers, err := d.queryNode(ctx, node, raw)
			results[i] = nodeResult{peers: peers, err: err}
		}(i, node)
	}
	wg.Wait()

	best := 0
	answered := false
	var errs []string
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err.Error())
			continue
		}
		answered = true
		if r.peers > best {
			best = r.peers
		}
	}
	if !answered {
		return 0, fmt.Errorf("DHT 节点均无响应: %s", strings.Join(errs, "; "))
	}
	return best, nil
}

// queryNode 向单个 DHT 节点发送 get_peers 查询并解析响应。
func (d *dhtProber) queryNode(ctx context.Context, addr string, raw []byte) (int, error) {
	tx, err := randomBytes(2)
	if err != nil {
		return 0, err
	}
	nodeID, err := randomBytes(20)
	if err != nil {
		return 0, err
	}
	query := bencode.NewDict(map[string]bencode.Value{
		"t": bencode.NewBytes(tx),
		"y": bencode.NewString("q"),
		"q": bencode.NewString("get_peers"),
		"a": bencode.NewDict(map[string]bencode.Value{
			"id":        bencode.NewBytes(nodeID),
			"info_hash": bencode.NewBytes(raw),
		}),
	})
	payload, err := bencode.Encode(query)
	if err != nil {
		return 0, fmt.Errorf("编码 get_peers 查询失败: %w", err)
	}

	dialer := net.Dialer{Timeout: d.timeout}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return 0, fmt.Errorf("连接 DHT 节点 %s 失败: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	stop := closeOnCancel(ctx, conn)
	defer stop()

	deadline := time.Now().Add(d.timeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(deadline) {
		deadline = cd
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, fmt.Errorf("设置 DHT 超时失败: %w", err)
	}
	if _, err := conn.Write(payload); err != nil {
		return 0, fmt.Errorf("发送 get_peers 查询失败: %w", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		// ctx 取消时连接会被主动关闭，此时应报告超时而不是"连接已关闭"。
		var netErr net.Error
		if ctx.Err() != nil || (errors.As(err, &netErr) && netErr.Timeout()) {
			return 0, fmt.Errorf("DHT 节点 %s 响应超时", addr)
		}
		return 0, fmt.Errorf("读取 DHT 响应失败: %w", err)
	}
	return parseDHTResponse(buf[:n], tx)
}

// parseDHTResponse 解析 get_peers 的响应并统计对等端数量。
func parseDHTResponse(payload []byte, tx []byte) (int, error) {
	root, err := bencode.Decode(payload)
	if err != nil {
		return 0, fmt.Errorf("DHT 响应不是合法 bencode: %w", err)
	}
	if got, ok := bencode.Bytes(root, "t"); ok && !bytes.Equal(got, tx) {
		return 0, fmt.Errorf("DHT 响应 transaction id 不匹配: 期望 %x 收到 %x", tx, got)
	}
	kind, _ := bencode.String(root, "y")
	switch kind {
	case "r":
		r, ok := bencode.Dict(root, "r")
		if !ok {
			return 0, fmt.Errorf("DHT 响应缺少 r 字段")
		}
		if values, ok := bencode.List(r, "values"); ok {
			return countCompactPeers(values), nil
		}
		if _, ok := bencode.Bytes(r, "nodes"); ok {
			// 引导节点通常只回路由表（紧凑节点信息）而不是 peer 列表。
			// 这种响应说明节点活着但没有直接的 peer 信息，按 0 计且不视为错误。
			return 0, nil
		}
		return 0, nil
	case "e":
		if list, ok := bencode.List(root, "e"); ok && len(list) == 2 {
			if msg, ok := list[1].Bytes(); ok {
				return 0, fmt.Errorf("DHT 返回错误: %s", msg)
			}
		}
		return 0, fmt.Errorf("DHT 返回错误")
	default:
		return 0, fmt.Errorf("未知的 DHT 响应类型 %q", kind)
	}
}

// countCompactPeers 统计紧凑格式的 peer 列表长度，每个 peer 占 6 字节（4 字节 IP + 2 字节端口）。
func countCompactPeers(values []bencode.Value) int {
	count := 0
	for _, v := range values {
		if b, ok := v.Bytes(); ok && len(b) == 6 {
			count++
		}
	}
	return count
}
