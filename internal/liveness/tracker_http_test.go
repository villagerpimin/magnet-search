package liveness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/httpx"
	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
	"github.com/pigzhu/magnet-search/internal/model"
)

// testInfoHash 是测试用的 40 位十六进制 info hash。
const testInfoHash = "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"

// testHTTPClient 构造测试用 HTTP 客户端，并关闭同 host 的请求间隔限制。
func testHTTPClient(t *testing.T) *httpx.Client {
	t.Helper()
	client, err := httpx.New(httpx.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建 HTTP 客户端失败: %v", err)
	}
	client.SetMinGap(0)
	return client
}

// scrapeBody 构造 BEP 48 风格的 scrape 响应体。
func scrapeBody(t *testing.T, raw []byte, complete, incomplete int64) []byte {
	t.Helper()
	entry := bencode.NewDict(map[string]bencode.Value{
		"complete":   bencode.NewInt(complete),
		"downloaded": bencode.NewInt(0),
		"incomplete": bencode.NewInt(incomplete),
	})
	body, err := bencode.Encode(bencode.NewDict(map[string]bencode.Value{
		"files": bencode.NewDict(map[string]bencode.Value{string(raw): entry}),
	}))
	if err != nil {
		t.Fatalf("编码 scrape 响应失败: %v", err)
	}
	return body
}

func TestHTTPScrapeParsesCounts(t *testing.T) {
	raw, err := decodeInfoHash(testInfoHash)
	if err != nil {
		t.Fatalf("解析 info hash 失败: %v", err)
	}
	var gotPath string
	var gotHash []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHash = []byte(r.URL.Query().Get("info_hash"))
		_, _ = w.Write(scrapeBody(t, raw, 42, 7))
	}))
	defer srv.Close()

	tracker, err := newHTTPTracker(srv.URL, testHTTPClient(t), "-UT3550-0123456789ab")
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	seeders, leechers, err := tracker.Scrape(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("scrape 失败: %v", err)
	}
	if seeders != 42 || leechers != 7 {
		t.Fatalf("scrape 结果 = (%d, %d), 期望 (42, 7)", seeders, leechers)
	}
	if gotPath != "/scrape" {
		t.Fatalf("请求路径 = %q, 期望 /scrape", gotPath)
	}
	if string(gotHash) != string(raw) {
		t.Fatalf("info_hash 传输后 = %x, 期望原始字节 %x", gotHash, raw)
	}
}

func TestHTTPFallsBackToAnnounce(t *testing.T) {
	raw, err := decodeInfoHash(testInfoHash)
	if err != nil {
		t.Fatalf("解析 info hash 失败: %v", err)
	}
	var announceQuery string
	var announceCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/scrape":
			w.WriteHeader(http.StatusNotFound)
		case "/announce":
			announceCalled = true
			announceQuery = r.URL.RawQuery
			body, encErr := bencode.Encode(bencode.NewDict(map[string]bencode.Value{
				"complete":   bencode.NewInt(5),
				"incomplete": bencode.NewInt(3),
			}))
			if encErr != nil {
				t.Fatalf("编码 announce 响应失败: %v", encErr)
			}
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tracker, err := newHTTPTracker(srv.URL, testHTTPClient(t), "-UT3550-0123456789ab")
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	seeders, leechers, err := tracker.Scrape(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("scrape 回退失败: %v", err)
	}
	if seeders != 5 || leechers != 3 {
		t.Fatalf("announce 结果 = (%d, %d), 期望 (5, 3)", seeders, leechers)
	}
	if !announceCalled {
		t.Fatal("scrape 返回 404 后应当回退到 announce")
	}
	if strings.Contains(announceQuery, "event=") {
		t.Fatalf("announce 请求不应携带 event 参数: %s", announceQuery)
	}
	if !strings.Contains(announceQuery, "numwant=0") {
		t.Fatalf("announce 请求必须带 numwant=0: %s", announceQuery)
	}
	if !strings.Contains(announceQuery, "info_hash="+escapeInfoHash(raw)) {
		t.Fatalf("announce 请求的 info_hash 未按原始字节转义: %s", announceQuery)
	}
}

func TestHTTPFallsBackWhenScrapeIsNotBencode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/scrape" {
			_, _ = w.Write([]byte("<html>not bencode</html>"))
			return
		}
		body, err := bencode.Encode(bencode.NewDict(map[string]bencode.Value{
			"complete":   bencode.NewInt(11),
			"incomplete": bencode.NewInt(2),
		}))
		if err != nil {
			t.Fatalf("编码 announce 响应失败: %v", err)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	tracker, err := newHTTPTracker(srv.URL, testHTTPClient(t), "-UT3550-0123456789ab")
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	seeders, leechers, err := tracker.Scrape(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("scrape 回退失败: %v", err)
	}
	if seeders != 11 || leechers != 2 {
		t.Fatalf("announce 结果 = (%d, %d), 期望 (11, 2)", seeders, leechers)
	}
}

func TestHTTPScrapeKeepsMissingCountUnknown(t *testing.T) {
	raw, err := decodeInfoHash(testInfoHash)
	if err != nil {
		t.Fatalf("解析 info hash 失败: %v", err)
	}
	entry := bencode.NewDict(map[string]bencode.Value{"complete": bencode.NewInt(42)})
	body, err := bencode.Encode(bencode.NewDict(map[string]bencode.Value{
		"files": bencode.NewDict(map[string]bencode.Value{string(raw): entry}),
	}))
	if err != nil {
		t.Fatalf("编码 scrape 响应失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	tracker, err := newHTTPTracker(srv.URL, testHTTPClient(t), "-UT3550-0123456789ab")
	if err != nil {
		t.Fatalf("构造 tracker 失败: %v", err)
	}
	seeders, leechers, err := tracker.Scrape(context.Background(), testInfoHash)
	if err != nil {
		t.Fatalf("scrape 失败: %v", err)
	}
	if seeders != 42 {
		t.Fatalf("做种数 = %d, 期望 42", seeders)
	}
	if leechers != model.Unknown {
		t.Fatalf("缺失的下载数 = %d, 期望 Unknown(%d)", leechers, model.Unknown)
	}
}

func TestTrackerEndpoints(t *testing.T) {
	cases := []struct {
		base     string
		announce string
		scrape   string
		wantErr  bool
	}{
		{base: "http://host", announce: "http://host/announce", scrape: "http://host/scrape"},
		{base: "http://host/", announce: "http://host/announce", scrape: "http://host/scrape"},
		{base: "http://host/announce", announce: "http://host/announce", scrape: "http://host/scrape"},
		{base: "https://host:8443/announce", announce: "https://host:8443/announce", scrape: "https://host:8443/scrape"},
		{base: "http://host/prefix", announce: "http://host/prefix/announce", scrape: "http://host/prefix/scrape"},
		{base: "udp://host:1337/announce", wantErr: true},
		{base: "not a url", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			announce, scrape, err := trackerEndpoints(tc.base)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("地址 %q 应当报错", tc.base)
				}
				return
			}
			if err != nil {
				t.Fatalf("地址 %q 解析失败: %v", tc.base, err)
			}
			if announce != tc.announce || scrape != tc.scrape {
				t.Fatalf("得到 announce=%q scrape=%q, 期望 announce=%q scrape=%q",
					announce, scrape, tc.announce, tc.scrape)
			}
		})
	}
}

func TestEscapeInfoHashEncodesSpaceAsPercent20(t *testing.T) {
	raw := []byte{0x20, 0x2b, 0x26, 0x3d, 0x00, 0xff}
	escaped := escapeInfoHash(raw)
	if strings.Contains(escaped, "+") {
		t.Fatalf("转义结果不能含 '+': %s", escaped)
	}
	if !strings.Contains(escaped, "%20") {
		t.Fatalf("空格字节必须转义成 %%20: %s", escaped)
	}
	if !strings.Contains(escaped, "%2B") || !strings.Contains(escaped, "%26") {
		t.Fatalf("保留字符必须被转义: %s", escaped)
	}
}
