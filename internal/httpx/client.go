// Package httpx 提供聚合程序各引擎共用的 HTTP 客户端，支持多代理链与自动回退。
package httpx

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Options 控制共享客户端的行为。
type Options struct {
	// Proxies 代理地址列表，按优先级排列；空字符串表示直连。
	// 客户端会在一次请求内按顺序轮换代理，网络层失败时自动切到下一个，
	// 因此单个代理失效不会拖垮整个引擎。
	Proxies []string
	// Timeout 单次请求的总超时
	Timeout time.Duration
	// ConnectTimeout 建立连接的超时，超时后视为该代理不可用
	ConnectTimeout time.Duration
	// Retries 网络错误或 5xx 时的重试次数
	Retries int
	// UserAgent 伪装 UA，部分站点对默认 Go UA 直接拒绝
	UserAgent string
	// InsecureSkipVerify 允许自签证书的 tracker/站点
	InsecureSkipVerify bool
	// ProxyUsername 代理认证用户名；为空表示不认证
	ProxyUsername string
	// ProxyPassword 代理认证密码（仅在用户名非空时生效）
	ProxyPassword string
}

// DefaultUserAgent 是常见桌面浏览器 UA，对多数站点最不容易被拦截。
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Client 是带代理链、重试、限流的 HTTP 客户端。
type Client struct {
	hc      *http.Client
	ua      string
	retries int
	proxies []string
	direct  *http.Transport

	proxyUsername string
	proxyPassword string

	proxyMu sync.Mutex
	proxied map[string]*http.Transport

	mu       sync.Mutex
	lastCall map[string]time.Time
	// minGap 同一 host 两次请求的最小间隔，避免触发站点风控
	minGap time.Duration
}

// New 创建客户端。Proxies 为空时全部直连。
func New(opts Options) (*Client, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 8 * time.Second
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}

	c := &Client{
		ua:            opts.UserAgent,
		retries:       opts.Retries,
		proxies:       normalizeProxies(opts.Proxies),
		proxied:       make(map[string]*http.Transport),
		lastCall:      make(map[string]time.Time),
		minGap:        350 * time.Millisecond,
		proxyUsername: opts.ProxyUsername,
		proxyPassword: opts.ProxyPassword,
	}
	c.direct = newTransport(opts.InsecureSkipVerify, opts.ConnectTimeout)
	c.hc = &http.Client{Transport: c.direct, Timeout: opts.Timeout}
	return c, nil
}

// normalizeProxies 去重并过滤非法代理地址。
// 空字符串代表"直连"，必须保留：调用方用 ["代理", ""] 表达
// "先试代理、失败再直连"，过滤掉空串会让直连兜底失效。
func normalizeProxies(list []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, p := range list {
		p = strings.TrimSpace(p)
		if p != "" {
			if _, err := url.Parse(p); err != nil {
				continue
			}
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// newTransport 创建带连接超时的 transport。
func newTransport(insecure bool, connectTimeout time.Duration) *http.Transport {
	return &http.Transport{
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 3 * time.Second,
		ResponseHeaderTimeout: connectTimeout * 2,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: insecure,
		},
	}
}

// transportFor 返回指定代理对应的 transport，直连返回 direct。
func (c *Client) transportFor(proxy string) *http.Transport {
	if proxy == "" {
		return c.direct
	}
	c.proxyMu.Lock()
	defer c.proxyMu.Unlock()
	if t, ok := c.proxied[proxy]; ok {
		return t
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return c.direct
	}
	u = c.applyProxyAuth(u)
	t := newTransport(false, 8*time.Second)
	t.Proxy = http.ProxyURL(u)
	c.proxied[proxy] = t
	return t
}

// applyProxyAuth 决定代理地址最终携带的认证信息。
// 内联凭据优先：代理地址里已有的 userinfo 保持不变；否则仅在
// 配置了 ProxyUsername 时注入用户名/密码，未配置则不做任何处理（默认不认证）。
func (c *Client) applyProxyAuth(u *url.URL) *url.URL {
	if u.User != nil {
		return u
	}
	if c.proxyUsername == "" {
		return u
	}
	u.User = url.UserPassword(c.proxyUsername, c.proxyPassword)
	return u
}

// Get 发起 GET 请求并返回响应体，网络失败会轮换代理重试。
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, rawURL, nil)
}

// GetString 发起 GET 请求并按字符串返回响应体。
func (c *Client) GetString(ctx context.Context, rawURL string) (string, error) {
	b, err := c.Get(ctx, rawURL)
	return string(b), err
}

// do 执行带代理轮换与重试的请求。
// 4xx 视为硬失败（封禁或站点改版无解），5xx/429 与网络错误才触发重试。
func (c *Client) do(ctx context.Context, method, rawURL string, body io.Reader) ([]byte, error) {
	if err := c.wait(ctx, rawURL); err != nil {
		return nil, err
	}

	attempts := c.retries + 1
	if len(c.proxies) > 1 && attempts < len(c.proxies) {
		attempts = len(c.proxies)
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * 400 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		proxy := c.proxyFor(attempt)
		req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8")
		req.Header.Set("Accept-Encoding", "gzip")

		cloned := &http.Client{Transport: c.transportFor(proxy), Timeout: c.hc.Timeout}
		resp, err := cloned.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("via %s: %w", proxyLabel(proxy), err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			lastErr = fmt.Errorf("via %s: HTTP %d", proxyLabel(proxy), resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 400 {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d (%s): %s", resp.StatusCode, proxyLabel(proxy), rawURL)
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("请求失败 %s: %w", rawURL, lastErr)
}

// proxyFor 按尝试次数轮换代理。
func (c *Client) proxyFor(attempt int) string {
	if len(c.proxies) == 0 {
		return ""
	}
	return c.proxies[attempt%len(c.proxies)]
}

// proxyLabel 生成代理的可读标签，隐藏凭据。
func proxyLabel(proxy string) string {
	if proxy == "" {
		return "直连"
	}
	return RedactProxy(proxy)
}

// wait 对同一 host 做最小请求间隔控制。
func (c *Client) wait(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	host := u.Host

	c.mu.Lock()
	last, ok := c.lastCall[host]
	c.mu.Unlock()
	if ok {
		gap := time.Since(last)
		if gap < c.minGap {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.minGap - gap):
			}
		}
	}
	c.mu.Lock()
	c.lastCall[host] = time.Now()
	c.mu.Unlock()
	return nil
}

// SetMinGap 调整同 host 的最小请求间隔。
func (c *Client) SetMinGap(d time.Duration) {
	c.mu.Lock()
	c.minGap = d
	c.mu.Unlock()
}

// HasProxy 判断是否配置了真正的代理（直连不计）。
func (c *Client) HasProxy() bool {
	for _, p := range c.proxies {
		if p != "" {
			return true
		}
	}
	return false
}

// ProxyChain 返回实际生效的代理链，便于排查"为什么没走代理"。
func (c *Client) ProxyChain() []string {
	out := make([]string, len(c.proxies))
	copy(out, c.proxies)
	return out
}

// RedactProxy 去掉代理地址中的凭据，用于日志输出。
func RedactProxy(proxy string) string {
	if proxy == "" {
		return ""
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return proxy
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return strings.TrimSpace(u.String())
}
