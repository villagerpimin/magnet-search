package liveness

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

const (
	// udpProtocolID 是 BEP 41 规定的握手协议标识 0x41727101980。
	udpProtocolID uint64 = 0x41727101980
	// udpActionConnect 是 UDP tracker 的连接请求动作码。
	udpActionConnect uint32 = 0
	// udpActionError 是 UDP tracker 的错误响应动作码。
	udpActionError uint32 = 3
	// udpActionScrape 是 UDP tracker 的 scrape 请求动作码。
	udpActionScrape uint32 = 2
	// udpActionScrapeResponse 是 UDP tracker 的 scrape 响应动作码。
	udpActionScrapeResponse uint32 = 1
)

// udpTracker 通过 BEP 41 的 UDP 协议探测单个 tracker。
type udpTracker struct {
	base    string
	addr    string
	timeout time.Duration
}

// newUDPTracker 解析 udp:// tracker 地址并构造探测器。
func newUDPTracker(base string, timeout time.Duration) (*udpTracker, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return nil, fmt.Errorf("tracker 地址非法 %q: %w", base, err)
	}
	if u.Scheme != "udp" {
		return nil, fmt.Errorf("tracker 地址 %q 不是 udp 协议", base)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("tracker 地址 %q 缺少主机名", base)
	}
	return &udpTracker{base: base, addr: u.Host, timeout: timeout}, nil
}

// URL 返回 tracker 地址，用于记录哪些 tracker 真正响应过。
func (t *udpTracker) URL() string {
	return t.base
}

// Scrape 通过 BEP 41 的 connect + scrape 两步握手查询 swarm 计数。
func (t *udpTracker) Scrape(ctx context.Context, infoHash string) (int, int, error) {
	raw, err := decodeInfoHash(infoHash)
	if err != nil {
		return 0, 0, err
	}
	conn, err := t.dial(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = conn.Close() }()
	stop := closeOnCancel(ctx, conn)
	defer stop()

	connID, err := t.connect(ctx, conn)
	if err != nil {
		return 0, 0, err
	}
	seeders, leechers, err := t.scrape(ctx, conn, connID, raw)
	if err != nil {
		return 0, 0, err
	}
	return seeders, leechers, nil
}

// dial 建立 UDP 连接，并挂上 ctx 取消时的关闭逻辑，避免读操作一直阻塞到超时。
func (t *udpTracker) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: t.timeout}
	conn, err := d.DialContext(ctx, "udp", t.addr)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", t.addr, err)
	}
	return conn, nil
}

// connect 发送握手请求并返回连接 id。
func (t *udpTracker) connect(ctx context.Context, conn net.Conn) (uint64, error) {
	txID, err := randomBytes(4)
	if err != nil {
		return 0, err
	}
	req := make([]byte, 16)
	binary.BigEndian.PutUint32(req[0:4], udpActionConnect)
	copy(req[4:8], txID)
	binary.BigEndian.PutUint64(req[8:16], udpProtocolID)
	if _, err := conn.Write(req); err != nil {
		return 0, fmt.Errorf("发送 connect 请求失败: %w", err)
	}
	pkt, err := t.readPacket(ctx, conn)
	if err != nil {
		return 0, err
	}
	if len(pkt) < 8 {
		return 0, fmt.Errorf("connect 响应过短: %d 字节", len(pkt))
	}
	action := binary.BigEndian.Uint32(pkt[0:4])
	if !bytes.Equal(pkt[4:8], txID) {
		return 0, fmt.Errorf("connect 响应 transaction id 不匹配: 期望 %x 收到 %x", txID, pkt[4:8])
	}
	switch action {
	case udpActionConnect:
		if len(pkt) < 16 {
			return 0, fmt.Errorf("connect 响应过短: %d 字节", len(pkt))
		}
		return binary.BigEndian.Uint64(pkt[8:16]), nil
	case udpActionError:
		return 0, fmt.Errorf("tracker 返回错误: %s", errorMessage(pkt[8:]))
	default:
		return 0, fmt.Errorf("未知的 connect 响应动作码 %d", action)
	}
}

// scrape 发送 scrape 请求并解析响应。
// 响应布局为 action(4) transaction_id(4) interval(4) leechers(4) seeders(4) completed(4)。
func (t *udpTracker) scrape(ctx context.Context, conn net.Conn, connID uint64, raw []byte) (int, int, error) {
	txID, err := randomBytes(4)
	if err != nil {
		return 0, 0, err
	}
	req := make([]byte, 40)
	binary.BigEndian.PutUint32(req[0:4], udpActionScrape)
	copy(req[4:8], txID)
	binary.BigEndian.PutUint64(req[8:16], connID)
	copy(req[16:36], raw)
	// num_want 置 0：只查询计数，不要 peer 列表。
	binary.BigEndian.PutUint32(req[36:40], 0)

	if _, err := conn.Write(req); err != nil {
		return 0, 0, fmt.Errorf("发送 scrape 请求失败: %w", err)
	}
	pkt, err := t.readPacket(ctx, conn)
	if err != nil {
		return 0, 0, err
	}
	if len(pkt) < 8 {
		return 0, 0, fmt.Errorf("scrape 响应过短: %d 字节", len(pkt))
	}
	action := binary.BigEndian.Uint32(pkt[0:4])
	if !bytes.Equal(pkt[4:8], txID) {
		return 0, 0, fmt.Errorf("scrape 响应 transaction id 不匹配: 期望 %x 收到 %x", txID, pkt[4:8])
	}
	switch action {
	case udpActionScrapeResponse:
		if len(pkt) < 20 {
			return 0, 0, fmt.Errorf("scrape 响应过短: %d 字节", len(pkt))
		}
		leechers := binary.BigEndian.Uint32(pkt[12:16])
		seeders := binary.BigEndian.Uint32(pkt[16:20])
		return int(seeders), int(leechers), nil
	case udpActionError:
		return 0, 0, fmt.Errorf("tracker 返回错误: %s", errorMessage(pkt[8:]))
	default:
		return 0, 0, fmt.Errorf("未知的 scrape 响应动作码 %d", action)
	}
}

// readPacket 读取一个 UDP 报文并设置硬性读超时。
func (t *udpTracker) readPacket(ctx context.Context, conn net.Conn) ([]byte, error) {
	deadline := time.Now().Add(t.timeout)
	if cd, ok := ctx.Deadline(); ok && cd.Before(deadline) {
		deadline = cd
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("设置读超时失败: %w", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		// ctx 取消时连接会被主动关闭，此时应报告超时而不是"连接已关闭"。
		var netErr net.Error
		if ctx.Err() != nil || (errors.As(err, &netErr) && netErr.Timeout()) {
			return nil, fmt.Errorf("读取 tracker 响应超时(%s)", t.addr)
		}
		return nil, fmt.Errorf("读取 tracker 响应失败: %w", err)
	}
	return buf[:n], nil
}

// errorMessage 解析错误响应里的 bencode 字符串。
func errorMessage(payload []byte) string {
	if len(payload) == 0 {
		return "未知错误"
	}
	v, err := bencode.Decode(payload)
	if err != nil {
		return "未知错误"
	}
	s, ok := v.Bytes()
	if !ok {
		return "未知错误"
	}
	return string(s)
}

// closeOnCancel 在 ctx 取消时关闭连接，使阻塞中的读操作立刻返回。
// 返回的函数用于正常流程结束时解除监听。
func closeOnCancel(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}
