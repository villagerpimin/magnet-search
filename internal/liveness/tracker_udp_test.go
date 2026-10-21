package liveness

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// udpMockMode 描述模拟 UDP tracker 的行为。
type udpMockMode int

const (
	// udpMockOK 正常返回连接 id 与 scrape 计数。
	udpMockOK udpMockMode = iota
	// udpMockBadTx 返回错误的 transaction id。
	udpMockBadTx
	// udpMockError 返回动作码 3 的错误响应。
	udpMockError
)

// startUDPScrapeMock 启动一个模拟 BEP 41 的 UDP tracker：
// 先回握手连接 id，再回 scrape 计数。
func startUDPScrapeMock(t *testing.T, mode udpMockMode, seeders, leechers uint32) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 UDP mock 失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 2048)
		n, addr, err := conn.ReadFrom(buf)
		if err != nil || n < 16 {
			return
		}
		if mode == udpMockError {
			reply := make([]byte, 8)
			binary.BigEndian.PutUint32(reply[0:4], udpActionError)
			copy(reply[4:8], buf[4:8])
			body, encErr := bencode.Encode(bencode.NewString("mock tracker error"))
			if encErr != nil {
				return
			}
			_, _ = conn.WriteTo(append(reply, body...), addr)
			return
		}
		handshake := make([]byte, 16)
		binary.BigEndian.PutUint32(handshake[0:4], udpActionConnect)
		copy(handshake[4:8], buf[4:8])
		binary.BigEndian.PutUint64(handshake[8:16], 0x0102030405060708)
		if _, err := conn.WriteTo(handshake, addr); err != nil {
			return
		}

		n, addr, err = conn.ReadFrom(buf)
		if err != nil || n < 40 {
			return
		}
		reply := make([]byte, 24)
		binary.BigEndian.PutUint32(reply[0:4], udpActionScrapeResponse)
		copy(reply[4:8], buf[4:8])
		if mode == udpMockBadTx {
			binary.BigEndian.PutUint32(reply[4:8], binary.BigEndian.Uint32(buf[4:8])+1)
		}
		binary.BigEndian.PutUint32(reply[8:12], 1800)
		binary.BigEndian.PutUint32(reply[12:16], leechers)
		binary.BigEndian.PutUint32(reply[16:20], seeders)
		binary.BigEndian.PutUint32(reply[20:24], 100)
		_, _ = conn.WriteTo(reply, addr)
	}()

	return conn.LocalAddr().String()
}

func TestUDPScrapeParsesCounts(t *testing.T) {
	addr := startUDPScrapeMock(t, udpMockOK, 21, 4)
	tracker, err := newUDPTracker("udp://"+addr+"/announce", 2*time.Second)
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	seeders, leechers, err := tracker.Scrape(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("scrape 失败: %v", err)
	}
	if seeders != 21 || leechers != 4 {
		t.Fatalf("scrape 结果 = (%d, %d), 期望 (21, 4)", seeders, leechers)
	}
}

func TestUDPScrapeRejectsMismatchedTransaction(t *testing.T) {
	addr := startUDPScrapeMock(t, udpMockBadTx, 21, 4)
	tracker, err := newUDPTracker("udp://"+addr+"/announce", 2*time.Second)
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	_, _, err = tracker.Scrape(context.Background(), testInfoHash)
	if err == nil {
		t.Fatal("transaction id 不匹配时应当返回错误")
	}
	if !strings.Contains(err.Error(), "transaction id") {
		t.Fatalf("错误信息应当指出 transaction id 不匹配: %v", err)
	}
}

func TestUDPScrapeSurfacesTrackerError(t *testing.T) {
	addr := startUDPScrapeMock(t, udpMockError, 0, 0)
	tracker, err := newUDPTracker("udp://"+addr+"/announce", 2*time.Second)
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	_, _, err = tracker.Scrape(context.Background(), testInfoHash)
	if err == nil {
		t.Fatal("tracker 返回错误动作码时应当返回错误")
	}
	if !strings.Contains(err.Error(), "mock tracker error") {
		t.Fatalf("错误信息应当带上 tracker 的原始描述: %v", err)
	}
}

func TestUDPScrapeTimesOut(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 UDP mock 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// 收到请求但不回应，用于验证读超时。
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := conn.ReadFrom(buf); err != nil {
				return
			}
		}
	}()

	tracker, err := newUDPTracker("udp://"+conn.LocalAddr().String()+"/announce", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	start := time.Now()
	_, _, err = tracker.Scrape(context.Background(), testInfoHash)
	if err == nil {
		t.Fatal("无响应时应当返回错误")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("超时时间过长: %v", elapsed)
	}
}

func TestNewUDPTrackerRejectsBadAddress(t *testing.T) {
	if _, err := newUDPTracker("http://host/announce", time.Second); err == nil {
		t.Fatal("非 UDP 地址应当报错")
	}
	if _, err := newUDPTracker("udp:///announce", time.Second); err == nil {
		t.Fatal("缺少主机名的地址应当报错")
	}
}
