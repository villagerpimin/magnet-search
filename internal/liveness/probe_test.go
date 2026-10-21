package liveness

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
	"github.com/pigzhu/magnet-search/internal/model"
)

// fakeTracker 是 tracker 的假实现，用于离线验证合并逻辑。
type fakeTracker struct {
	url      string
	seeders  int
	leechers int
	err      error
}

// Scrape 返回预置的计数或错误。
func (f *fakeTracker) Scrape(ctx context.Context, infoHash string) (int, int, error) {
	return f.seeders, f.leechers, f.err
}

// URL 返回预置的 tracker 地址。
func (f *fakeTracker) URL() string {
	return f.url
}

// fakeDHT 是 DHT 的假实现，用于离线验证合并逻辑。
type fakeDHT struct {
	peers int
	err   error
}

// Peers 返回预置的对等端数量或错误。
func (f *fakeDHT) Peers(ctx context.Context, infoHash string) (int, error) {
	return f.peers, f.err
}

// newTestProber 用假实现构造探测器。
func newTestProber(trackers []trackerProbe, dhts []dhtProbe) *Prober {
	return &Prober{
		cfg:      Config{Timeout: time.Second, MaxConcurrency: 4},
		trackers: trackers,
		dht:      dhts,
	}
}

func TestProbeMergeTakesMaxAcrossSources(t *testing.T) {
	prober := newTestProber(
		[]trackerProbe{
			&fakeTracker{url: "http://a/announce", seeders: 10, leechers: 5},
			&fakeTracker{url: "http://b/announce", seeders: 3, leechers: 20},
			&fakeTracker{url: "http://c/announce", err: errors.New("boom")},
			&fakeTracker{url: "http://d/announce", seeders: 0, leechers: 0},
		},
		[]dhtProbe{
			&fakeDHT{peers: 4},
			&fakeDHT{peers: 9},
		},
	)

	result := prober.Probe(context.Background(), []string{testInfoHash})
	live, ok := result[testInfoHash]
	if !ok {
		t.Fatalf("结果里缺少 %s", testInfoHash)
	}
	if live.TrackerComplete != 10 {
		t.Fatalf("TrackerComplete = %d, 期望 10", live.TrackerComplete)
	}
	if live.TrackerIncomplete != 20 {
		t.Fatalf("TrackerIncomplete = %d, 期望 20", live.TrackerIncomplete)
	}
	if live.DHTPeers != 9 {
		t.Fatalf("DHTPeers = %d, 期望 9", live.DHTPeers)
	}
	if len(live.Trackers) != 3 {
		t.Fatalf("Trackers = %v, 期望 3 个应答的 tracker", live.Trackers)
	}
	if len(live.Errors) != 1 {
		t.Fatalf("Errors = %v, 期望 1 条", live.Errors)
	}
}

func TestProbeKeepsZeroDistinctFromUnknown(t *testing.T) {
	prober := newTestProber(
		[]trackerProbe{&fakeTracker{url: "http://a/announce", seeders: 0, leechers: 0}},
		nil,
	)

	result := prober.Probe(context.Background(), []string{testInfoHash})
	live := result[testInfoHash]
	if live.TrackerComplete != 0 {
		t.Fatalf("TrackerComplete = %d, 期望真实的 0 而不是 Unknown", live.TrackerComplete)
	}
	if live.TrackerIncomplete != 0 {
		t.Fatalf("TrackerIncomplete = %d, 期望真实的 0 而不是 Unknown", live.TrackerIncomplete)
	}
	if live.DHTPeers != model.Unknown {
		t.Fatalf("DHTPeers = %d, 期望 Unknown(%d)", live.DHTPeers, model.Unknown)
	}
	if !live.HasNetworkProbe() {
		t.Fatal("拿到真实计数时 HasNetworkProbe 应当为 true")
	}
}

func TestProbeWithoutAnyAnswerStaysUnknown(t *testing.T) {
	prober := newTestProber(
		[]trackerProbe{
			&fakeTracker{url: "http://a/announce", err: errors.New("超时")},
			&fakeTracker{url: "http://b/announce", err: errors.New("拒绝")},
		},
		[]dhtProbe{&fakeDHT{err: errors.New("无响应")}},
	)

	result := prober.Probe(context.Background(), []string{testInfoHash})
	live := result[testInfoHash]
	if live.TrackerComplete != model.Unknown || live.TrackerIncomplete != model.Unknown {
		t.Fatalf("没有应答时计数应当保持 Unknown, 实际 (%d, %d)",
			live.TrackerComplete, live.TrackerIncomplete)
	}
	if live.DHTPeers != model.Unknown {
		t.Fatalf("DHTPeers = %d, 期望 Unknown(%d)", live.DHTPeers, model.Unknown)
	}
	if len(live.Trackers) != 0 {
		t.Fatalf("Trackers = %v, 期望为空", live.Trackers)
	}
	if len(live.Errors) == 0 {
		t.Fatal("失败原因应当记录到 Errors")
	}
	if live.HasNetworkProbe() {
		t.Fatal("没有任何来源应答时 HasNetworkProbe 应当为 false")
	}
}

func TestProbeCapsErrorEntries(t *testing.T) {
	trackers := make([]trackerProbe, 0, 7)
	for i := 0; i < 7; i++ {
		trackers = append(trackers, &fakeTracker{url: "http://a/announce", err: errors.New("失败")})
	}
	prober := newTestProber(trackers, nil)

	result := prober.Probe(context.Background(), []string{testInfoHash})
	live := result[testInfoHash]
	if len(live.Errors) != maxErrorEntries {
		t.Fatalf("Errors 条数 = %d, 期望上限 %d", len(live.Errors), maxErrorEntries)
	}
}

func TestProbeKeepsInvalidHashVisible(t *testing.T) {
	prober := newTestProber(nil, nil)

	result := prober.Probe(context.Background(), []string{"不是哈希"})
	live, ok := result["不是哈希"]
	if !ok {
		t.Fatal("非法哈希也应当出现在结果里")
	}
	if live.TrackerComplete != model.Unknown {
		t.Fatalf("TrackerComplete = %d, 期望 Unknown(%d)", live.TrackerComplete, model.Unknown)
	}
	if len(live.Errors) == 0 {
		t.Fatal("非法哈希应当记录错误原因")
	}
}

func TestProbeNormalizesHashKeys(t *testing.T) {
	prober := newTestProber(
		[]trackerProbe{&fakeTracker{url: "http://a/announce", seeders: 1, leechers: 1}},
		nil,
	)

	upper := "DD8255ECDC7CA55FB0BBF81323D87062DB1F6D1C"
	result := prober.Probe(context.Background(), []string{upper})
	if _, ok := result[testInfoHash]; !ok {
		t.Fatalf("结果应当用小写 hex 归一化, 实际键: %v", result)
	}
}

func TestProbeRespectsContextCancellation(t *testing.T) {
	prober := newTestProber(
		[]trackerProbe{&fakeTracker{url: "http://a/announce", err: errors.New("取消")}},
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := prober.Probe(ctx, []string{testInfoHash})
	live := result[testInfoHash]
	if live.TrackerComplete != model.Unknown {
		t.Fatalf("ctx 取消后计数应当保持 Unknown, 实际 %d", live.TrackerComplete)
	}
}

func TestProbeAgainstLocalMocks(t *testing.T) {
	raw, err := decodeInfoHash(testInfoHash)
	if err != nil {
		t.Fatalf("解析 info hash 失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(scrapeBody(t, raw, 42, 7))
	}))
	defer srv.Close()
	udpAddr := startUDPScrapeMock(t, udpMockOK, 21, 4)
	dhtAddr := startDHTMock(t, func(query []byte) []byte {
		return dhtReply(t, query, bencode.NewDict(map[string]bencode.Value{
			"id":    bencode.NewBytes(make([]byte, 20)),
			"token": bencode.NewBytes(make([]byte, 8)),
			"values": bencode.NewList([]bencode.Value{
				bencode.NewBytes(compactPeer(1, 6881)),
				bencode.NewBytes(compactPeer(2, 6882)),
			}),
		}))
	})

	cfg := Config{
		HTTPTrackers: []string{srv.URL},
		UDPTrackers:  []string{"udp://" + udpAddr + "/announce"},
		DHTNodes:     []string{dhtAddr},
		EnableUDP:    true,
		EnableDHT:    true,
		Timeout:      2 * time.Second,
		Client:       testHTTPClient(t),
	}
	result := NewProber(cfg).Probe(context.Background(), []string{testInfoHash})
	live := result[testInfoHash]
	if live.TrackerComplete != 42 {
		t.Fatalf("TrackerComplete = %d, 期望 42", live.TrackerComplete)
	}
	if live.TrackerIncomplete != 7 {
		t.Fatalf("TrackerIncomplete = %d, 期望 7", live.TrackerIncomplete)
	}
	if live.DHTPeers != 2 {
		t.Fatalf("DHTPeers = %d, 期望 2", live.DHTPeers)
	}
	if len(live.Trackers) != 2 {
		t.Fatalf("Trackers = %v, 期望 HTTP 与 UDP 各一个", live.Trackers)
	}
}

func TestTruncateKeepsRuneBoundary(t *testing.T) {
	got := truncate("读取 tracker 响应超时这是一个很长很长的中文错误", 10)
	if !utf8.ValidString(got) {
		t.Fatalf("截断结果必须是合法 UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断结果应当带省略号: %q", got)
	}
	if truncate("short", 10) != "short" {
		t.Fatal("未超长的字符串不应被截断")
	}
}
