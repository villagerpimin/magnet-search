package httpx

import (
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
