package liveness

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/pigzhu/magnet-search/internal/httpx"
	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// httpTracker 通过 HTTP/HTTPS 协议探测单个 tracker 的 swarm 规模。
type httpTracker struct {
	base        string
	announceURL string
	scrapeURL   string
	client      *httpx.Client
	peerID      string
}

// newHTTPTracker 解析 tracker 基础地址并构造 HTTP 探测器。
func newHTTPTracker(base string, client *httpx.Client, peerID string) (*httpTracker, error) {
	announce, scrape, err := trackerEndpoints(base)
	if err != nil {
		return nil, err
	}
	return &httpTracker{
		base:        base,
		announceURL: announce,
		scrapeURL:   scrape,
		client:      client,
		peerID:      peerID,
	}, nil
}

// trackerEndpoints 由 tracker 基础地址推导 announce 与 scrape 两个端点。
// 形如 http://host/announce 的地址直接替换路径，形如 http://host 的地址补全路径。
func trackerEndpoints(base string) (announce, scrape string, err error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", "", fmt.Errorf("tracker 地址非法 %q: %w", base, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("tracker 地址 %q 不是 HTTP(S) 协议", base)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("tracker 地址 %q 缺少主机名", base)
	}
	root := u.Scheme + "://" + u.Host
	path := strings.TrimSuffix(u.Path, "/")
	if strings.HasSuffix(path, "/announce") {
		return root + path, root + strings.TrimSuffix(path, "/announce") + "/scrape", nil
	}
	return root + path + "/announce", root + path + "/scrape", nil
}

// URL 返回 tracker 地址，用于记录哪些 tracker 真正响应过。
func (t *httpTracker) URL() string {
	return t.base
}

// Scrape 查询 info hash 的做种数与下载数。
// 优先使用 BEP 48 的 scrape 接口，不支持时回退到 announce 查询。
func (t *httpTracker) Scrape(ctx context.Context, infoHash string) (int, int, error) {
	raw, err := decodeInfoHash(infoHash)
	if err != nil {
		return 0, 0, err
	}
	seeders, leechers, scrapeErr := t.scrape(ctx, raw)
	if scrapeErr == nil {
		return seeders, leechers, nil
	}
	// scrape 是 BEP 48 的可选扩展，多数 tracker 只实现 announce，因此回退。
	seeders, leechers, announceErr := t.announce(ctx, raw)
	if announceErr != nil {
		return 0, 0, fmt.Errorf("scrape 失败: %v; announce 失败: %w", scrapeErr, announceErr)
	}
	return seeders, leechers, nil
}

// scrape 调用 tracker 的 scrape 接口读取 swarm 计数。
func (t *httpTracker) scrape(ctx context.Context, raw []byte) (int, int, error) {
	if t.client == nil {
		return 0, 0, errors.New("未配置 HTTP 客户端")
	}
	target := t.scrapeURL + "?info_hash=" + escapeInfoHash(raw)
	body, err := t.client.Get(ctx, target)
	if err != nil {
		return 0, 0, fmt.Errorf("scrape 请求失败: %w", err)
	}
	body, err = decodeTrackerBody(body)
	if err != nil {
		return 0, 0, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return 0, 0, errors.New("scrape 响应为空")
	}
	root, err := bencode.Decode(body)
	if err != nil {
		return 0, 0, fmt.Errorf("scrape 响应不是合法 bencode: %w", err)
	}
	files, ok := bencode.Dict(root, "files")
	if !ok {
		return 0, 0, errors.New("scrape 响应缺少 files 字段")
	}
	// scrape 的字典键是 20 字节原始 info hash。
	entry, ok := bencode.DictBytes(files, raw)
	if !ok {
		return 0, 0, errors.New("scrape 响应中没有该 info hash 的条目")
	}
	seeders, ok1 := bencode.Int(entry, "complete")
	leechers, ok2 := bencode.Int(entry, "incomplete")
	if !ok1 && !ok2 {
		return 0, 0, errors.New("scrape 条目缺少 complete/incomplete")
	}
	// 缺失的计数不能当成 0（0 表示"确实没有"），保持 Unknown 更诚实。
	return normalizeCount(seeders, ok1), normalizeCount(leechers, ok2), nil
}

// announce 用 announce 协议读取 swarm 计数。
//
// 权衡说明：announce 语义上会把请求方登记成一个 peer，而 scrape 不会。
// 为了不污染被测资源的 swarm 计数，这里传 numwant=0（明确表示不要 peer 列表）
// 并省略 event 参数（不发 started/completed/stopped 事件，绝大多数 tracker 因此不会
// 把我们写进 peer 表）。仍有少数 tracker 会把这次 announce 计入做种数，
// 误差方向是"偏大"，不会把死链误判成活链。
func (t *httpTracker) announce(ctx context.Context, raw []byte) (int, int, error) {
	if t.client == nil {
		return 0, 0, errors.New("未配置 HTTP 客户端")
	}
	query := "info_hash=" + escapeInfoHash(raw) +
		"&peer_id=" + url.QueryEscape(t.peerID) +
		"&port=6881&uploaded=0&downloaded=0&left=0&compact=1&numwant=0"
	body, err := t.client.Get(ctx, t.announceURL+"?"+query)
	if err != nil {
		return 0, 0, fmt.Errorf("announce 请求失败: %w", err)
	}
	body, err = decodeTrackerBody(body)
	if err != nil {
		return 0, 0, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return 0, 0, errors.New("announce 响应为空")
	}
	root, err := bencode.Decode(body)
	if err != nil {
		return 0, 0, fmt.Errorf("announce 响应不是合法 bencode: %w", err)
	}
	if reason, ok := bencode.String(root, "failure reason"); ok {
		return 0, 0, fmt.Errorf("tracker 拒绝请求: %s", reason)
	}
	seeders, ok1 := bencode.Int(root, "complete")
	leechers, ok2 := bencode.Int(root, "incomplete")
	if !ok1 && !ok2 {
		return 0, 0, errors.New("announce 响应缺少 complete/incomplete")
	}
	return normalizeCount(seeders, ok1), normalizeCount(leechers, ok2), nil
}

// reachable 判断 tracker 是否可达，只要求能返回合法 bencode。
// 能力探测关心的是网络通不通，而不是目标哈希是否被 tracker 收录，
// 因此这里不要求响应里含有所查询的哈希条目。
func (t *httpTracker) reachable(ctx context.Context) error {
	if t.client == nil {
		return errors.New("未配置 HTTP 客户端")
	}
	raw, err := decodeInfoHash(detectInfoHash)
	if err != nil {
		return err
	}
	scrapeBody, scrapeErr := t.client.Get(ctx, t.scrapeURL+"?info_hash="+escapeInfoHash(raw))
	scrapeReason := "响应不是合法 bencode"
	if scrapeErr != nil {
		scrapeReason = scrapeErr.Error()
	} else if decoded, decErr := decodeTrackerBody(scrapeBody); decErr != nil {
		scrapeReason = decErr.Error()
	} else if isBencode(decoded) {
		return nil
	}

	announceBody, announceErr := t.client.Get(ctx, t.announceURL+"?"+
		"info_hash="+escapeInfoHash(raw)+
		"&peer_id="+url.QueryEscape(t.peerID)+
		"&port=6881&uploaded=0&downloaded=0&left=0&compact=1&numwant=0")
	announceReason := "响应不是合法 bencode"
	if announceErr != nil {
		announceReason = announceErr.Error()
	} else if decoded, decErr := decodeTrackerBody(announceBody); decErr != nil {
		announceReason = decErr.Error()
	} else if isBencode(decoded) {
		return nil
	}
	return fmt.Errorf("scrape: %s; announce: %s", scrapeReason, announceReason)
}

// decodeTrackerBody 解析 tracker 响应体。
// 共享 HTTP 客户端显式设置了 Accept-Encoding: gzip，服务端若据此压缩响应，
// Go 的 Transport 便不会再自动解压，因此这里按 gzip 魔数判断并自行解压。
func decodeTrackerBody(body []byte) ([]byte, error) {
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解压 tracker 响应失败: %w", err)
	}
	defer func() { _ = zr.Close() }()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("解压 tracker 响应失败: %w", err)
	}
	return out, nil
}

// isBencode 判断响应体是否是可解析的 bencode 数据。
func isBencode(body []byte) bool {
	if len(bytes.TrimSpace(body)) == 0 {
		return false
	}
	_, err := bencode.Decode(body)
	return err == nil
}

// escapeInfoHash 把 20 字节原始 info hash 编码成查询串可用的百分号转义形式。
//
// 不能直接用 url.QueryEscape：它会把 0x20 这个字节转义成 '+'，
// 而 '+' 在查询串里是"空格"的别名，服务端解码后会得到 0x20 以外的值，
// 极端情况下会篡改哈希。因此这里把 '+' 进一步改写成 %20。
func escapeInfoHash(raw []byte) string {
	return strings.ReplaceAll(url.QueryEscape(string(raw)), "+", "%20")
}
