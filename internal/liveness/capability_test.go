package liveness

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// startSilentUDP 启动一个只收不回的 UDP 端点，用于模拟不可达的节点。
func startSilentUDP(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 UDP mock 失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, _, err := conn.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	return conn.LocalAddr().String()
}

func TestDetectWithLocalMocks(t *testing.T) {
	raw, err := decodeInfoHash(detectInfoHash)
	if err != nil {
		t.Fatalf("解析 info hash 失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(scrapeBody(t, raw, 1, 1))
	}))
	defer srv.Close()

	udpAddr := startUDPScrapeMock(t, udpMockOK, 3, 2)
	dhtAddr := startDHTMock(t, func(query []byte) []byte {
		return dhtReply(t, query, bencode.NewDict(map[string]bencode.Value{
			"id":    bencode.NewBytes(make([]byte, 20)),
			"token": bencode.NewBytes(make([]byte, 8)),
			"values": bencode.NewList([]bencode.Value{
				bencode.NewBytes(compactPeer(1, 6881)),
			}),
		}))
	})

	cfg := Config{
		HTTPTrackers: []string{srv.URL},
		UDPTrackers:  []string{"udp://" + udpAddr + "/announce"},
		DHTNodes:     []string{dhtAddr},
		Timeout:      2 * time.Second,
		Client:       testHTTPClient(t),
	}
	cap := Detect(context.Background(), cfg)
	if !cap.TrackerHTTP {
		t.Fatalf("HTTP 通道应当可用: %s", cap.TrackerHTTPReason)
	}
	if !cap.TrackerUDP {
		t.Fatalf("UDP 通道应当可用: %s", cap.TrackerUDPReason)
	}
	if !cap.DHT {
		t.Fatalf("DHT 通道应当可用: %s", cap.DHTReason)
	}
}

func TestDetectReportsUnreachableTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	silent := startSilentUDP(t)
	cfg := Config{
		HTTPTrackers: []string{srv.URL},
		UDPTrackers:  []string{"udp://" + silent + "/announce"},
		DHTNodes:     []string{silent},
		Timeout:      300 * time.Millisecond,
		Client:       testHTTPClient(t),
	}
	cap := Detect(context.Background(), cfg)
	if cap.TrackerHTTP {
		t.Fatal("返回 404 的 tracker 不应被判定为可用")
	}
	if cap.TrackerUDP {
		t.Fatal("无响应的 UDP tracker 不应被判定为可用")
	}
	if cap.DHT {
		t.Fatal("无响应的 DHT 节点不应被判定为可用")
	}
	if cap.TrackerHTTPReason == "" || cap.TrackerUDPReason == "" || cap.DHTReason == "" {
		t.Fatalf("失败时必须给出原因: %+v", cap)
	}
}

func TestDetectWithoutTargets(t *testing.T) {
	okHTTP, reasonHTTP := detectHTTP(context.Background(), Config{}, time.Second)
	if okHTTP || reasonHTTP == "" {
		t.Fatalf("未配置 tracker 时应当不可用并说明原因: %v %s", okHTTP, reasonHTTP)
	}
	okUDP, reasonUDP := detectUDP(context.Background(), Config{}, time.Second)
	if okUDP || reasonUDP == "" {
		t.Fatalf("未配置 tracker 时应当不可用并说明原因: %v %s", okUDP, reasonUDP)
	}
	okDHT, reasonDHT := detectDHT(context.Background(), Config{}, time.Second)
	if okDHT || reasonDHT == "" {
		t.Fatalf("未配置节点时应当不可用并说明原因: %v %s", okDHT, reasonDHT)
	}
}

func TestDetectHasHardTimeout(t *testing.T) {
	silent := startSilentUDP(t)
	cfg := Config{
		UDPTrackers: []string{"udp://" + silent + "/announce"},
		DHTNodes:    []string{silent},
		Timeout:     300 * time.Millisecond,
	}
	start := time.Now()
	Detect(context.Background(), cfg)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("能力探测耗时过长: %v", elapsed)
	}
}
