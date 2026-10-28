package httpx

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestProxyChainKeepsDirectFallback 校验代理链里的"直连"兜底不会被过滤掉。
// 这条断言是回归网：曾经把空串当无效代理丢掉，导致代理不通时永远不会回退直连。
func TestProxyChainKeepsDirectFallback(t *testing.T) {
	c, err := New(Options{
		Proxies: []string{"http://127.0.0.1:7897", ""},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	chain := c.ProxyChain()
	if len(chain) != 2 {
		t.Fatalf("代理链 = %v，期望包含代理与直连两项", chain)
	}
	if chain[0] != "http://127.0.0.1:7897" || chain[1] != "" {
		t.Errorf("代理链 = %v，期望 [代理 直连]", chain)
	}
	if get := c.proxyFor(1); get != "" {
		t.Errorf("第二次尝试应回退直连，实际 %q", get)
	}
	if !c.HasProxy() {
		t.Error("配置了代理时 HasProxy 应为 true")
	}
}

// TestProxyChainDedupes 校验重复的代理与直连只保留一份。
func TestProxyChainDedupes(t *testing.T) {
	c, err := New(Options{
		Proxies: []string{"http://a:1", "http://a:1", "", ""},
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if chain := c.ProxyChain(); len(chain) != 2 {
		t.Fatalf("代理链 = %v，期望去重为 2 项", chain)
	}
}

// TestNoProxyMeansDirect 校验不配代理时就是直连。
func TestNoProxyMeansDirect(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if c.HasProxy() {
		t.Error("未配置代理时 HasProxy 应为 false")
	}
	if got := c.proxyFor(0); got != "" {
		t.Errorf("proxyFor(0) = %q，期望直连", got)
	}
}

// mustParse 解析 URL，失败直接 panic，仅用于测试。
func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// TestProxyAuthInjected 校验配置的用户名/密码被注入到 transport 的代理 URL。
func TestProxyAuthInjected(t *testing.T) {
	c, err := New(Options{
		Proxies:       []string{"http://127.0.0.1:7897"},
		ProxyUsername: "alice",
		ProxyPassword: "s3cret",
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	tr := c.transportFor("http://127.0.0.1:7897")
	req := &http.Request{URL: mustParse("https://example.com")}
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("获取代理地址失败: %v", err)
	}
	if proxyURL == nil || proxyURL.User == nil {
		t.Fatalf("代理地址应包含认证信息，实际 %v", proxyURL)
	}
	if got := proxyURL.User.Username(); got != "alice" {
		t.Errorf("代理用户名 = %q，期望 alice", got)
	}
	if p, _ := proxyURL.User.Password(); p != "s3cret" {
		t.Errorf("代理密码 = %q，期望 s3cret", p)
	}
}

// TestProxyAuthInlineWins 校验代理地址内的内联凭据优先于 Options 里的认证配置。
func TestProxyAuthInlineWins(t *testing.T) {
	c, err := New(Options{
		Proxies:       []string{"http://bob:inline@127.0.0.1:7897"},
		ProxyUsername: "alice",
		ProxyPassword: "s3cret",
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	tr := c.transportFor("http://bob:inline@127.0.0.1:7897")
	req := &http.Request{URL: mustParse("https://example.com")}
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("获取代理地址失败: %v", err)
	}
	if proxyURL == nil || proxyURL.User == nil {
		t.Fatalf("代理地址应包含内联凭据，实际 %v", proxyURL)
	}
	if got := proxyURL.User.Username(); got != "bob" {
		t.Errorf("代理用户名 = %q，期望 bob", got)
	}
	if p, _ := proxyURL.User.Password(); p != "inline" {
		t.Errorf("代理密码 = %q，期望 inline", p)
	}
}

// TestProxyAuthDefaultNone 校验未配置认证信息时默认不发送 Proxy-Authorization。
func TestProxyAuthDefaultNone(t *testing.T) {
	c, err := New(Options{
		Proxies: []string{"http://127.0.0.1:7897"},
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	tr := c.transportFor("http://127.0.0.1:7897")
	req := &http.Request{URL: mustParse("https://example.com")}
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("获取代理地址失败: %v", err)
	}
	if proxyURL == nil {
		t.Fatalf("应返回代理地址，实际为 nil")
	}
	if proxyURL.User != nil {
		t.Errorf("未配置认证时代理地址不应包含 userinfo，实际 %v", proxyURL.User)
	}
}

// TestProxyAuthHeaderSent 起一个本地 HTTP 代理，验证客户端真的按配置
// 发出了 Proxy-Authorization: Basic 头（而不是只在 URL 里挂了 userinfo）。
func TestProxyAuthHeaderSent(t *testing.T) {
	var gotAuth string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "ok")
	}))
	defer proxy.Close()

	c, err := New(Options{
		Proxies:       []string{proxy.URL},
		ProxyUsername: "alice",
		ProxyPassword: "s3cret",
		Retries:       0,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if _, err := c.Get(context.Background(), "http://example.invalid/"); err != nil {
		t.Fatalf("经代理请求失败: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if gotAuth != want {
		t.Errorf("Proxy-Authorization = %q，期望 %q", gotAuth, want)
	}
}

// TestProxyAuthHeaderAbsentByDefault 校验默认不认证时不会带 Proxy-Authorization 头。
func TestProxyAuthHeaderAbsentByDefault(t *testing.T) {
	var gotAuth string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer proxy.Close()

	c, err := New(Options{
		Proxies: []string{proxy.URL},
		Retries: 0,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if _, err := c.Get(context.Background(), "http://example.invalid/"); err != nil {
		t.Fatalf("经代理请求失败: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("默认不应发送 Proxy-Authorization，实际 %q", gotAuth)
	}
}
