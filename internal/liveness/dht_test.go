package liveness

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// startDHTMock 启动一个模拟 BEP 5 的 DHT 节点。
// respond 收到查询报文后返回要回送的响应体，返回 nil 表示不回应。
func startDHTMock(t *testing.T, respond func(query []byte) []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 DHT mock 失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 4096)
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		payload := respond(buf[:n])
		if payload == nil {
			return
		}
		_, _ = conn.WriteTo(payload, addr)
	}()

	return conn.LocalAddr().String()
}

// dhtReply 构造 get_peers 的成功响应。
func dhtReply(t *testing.T, query []byte, r bencode.Value) []byte {
	t.Helper()
	req, err := bencode.Decode(query)
	if err != nil {
		return nil
	}
	tx, ok := bencode.Bytes(req, "t")
	if !ok {
		return nil
	}
	payload, err := bencode.Encode(bencode.NewDict(map[string]bencode.Value{
		"t": bencode.NewBytes(tx),
		"y": bencode.NewString("r"),
		"r": r,
	}))
	if err != nil {
		return nil
	}
	return payload
}

// compactPeer 生成紧凑格式的 peer：4 字节 IP + 2 字节端口。
func compactPeer(ip byte, port uint16) []byte {
	peer := make([]byte, 6)
	peer[0] = 127
	peer[1] = 0
	peer[2] = 0
	peer[3] = ip
	peer[4] = byte(port >> 8)
	peer[5] = byte(port)
	return peer
}

func TestDHTGetPeersCountsPeers(t *testing.T) {
	addr := startDHTMock(t, func(query []byte) []byte {
		return dhtReply(t, query, bencode.NewDict(map[string]bencode.Value{
			"id":    bencode.NewBytes(make([]byte, 20)),
			"token": bencode.NewBytes(make([]byte, 8)),
			"values": bencode.NewList([]bencode.Value{
				bencode.NewBytes(compactPeer(1, 6881)),
				bencode.NewBytes(compactPeer(2, 6882)),
			}),
		}))
	})

	prober := newDHTProber([]string{addr}, 2*time.Second)
	peers, err := prober.Peers(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("get_peers 失败: %v", err)
	}
	if peers != 2 {
		t.Fatalf("对等端数量 = %d, 期望 2", peers)
	}
}

func TestDHTNodesResponseIsNotAnError(t *testing.T) {
	addr := startDHTMock(t, func(query []byte) []byte {
		return dhtReply(t, query, bencode.NewDict(map[string]bencode.Value{
			"id":    bencode.NewBytes(make([]byte, 20)),
			"token": bencode.NewBytes(make([]byte, 8)),
			"nodes": bencode.NewBytes(make([]byte, 26)),
		}))
	})

	prober := newDHTProber([]string{addr}, 2*time.Second)
	peers, err := prober.Peers(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("nodes 响应不应视为错误: %v", err)
	}
	if peers != 0 {
		t.Fatalf("对等端数量 = %d, 期望 0", peers)
	}
}

func TestDHTPeersFailsWhenNodeSilent(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 UDP mock 失败: %v", err)
	}
	defer func() { _ = conn.Close() }()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, _, err := conn.ReadFrom(buf); err != nil {
				return
			}
		}
	}()

	prober := newDHTProber([]string{conn.LocalAddr().String()}, 300*time.Millisecond)
	start := time.Now()
	_, err = prober.Peers(context.Background(), testInfoHash)
	if err == nil {
		t.Fatal("节点无响应时应当返回错误")
	}
	if !strings.Contains(err.Error(), "无响应") {
		t.Fatalf("错误信息应当说明节点无响应: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("超时时间过长: %v", elapsed)
	}
}

func TestDHTRejectsInvalidInfoHash(t *testing.T) {
	prober := newDHTProber([]string{"127.0.0.1:6881"}, time.Second)
	if _, err := prober.Peers(context.Background(), "不是哈希"); err == nil {
		t.Fatal("非法 info hash 应当报错")
	}
}

func TestDHTWithoutNodes(t *testing.T) {
	prober := newDHTProber(nil, time.Second)
	if _, err := prober.Peers(context.Background(), testInfoHash); err == nil {
		t.Fatal("没有引导节点时应当报错")
	}
}
