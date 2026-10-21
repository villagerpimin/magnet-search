package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// skipIfUnreachable 在站点被本机网络拦截时跳过测试。
//
// nyaa.si 与 solidtorrents.to 在本机是 SNI 级别阻断，
// 表现为 TLS 握手被重置或连接超时，而不是 HTTP 状态码，
// 因此这里按网络错误特征判断，而不是只看 403。
func skipIfUnreachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	// 解析失败说明站点是通的，是我们的实现有问题，不能当成网络问题跳过
	if strings.Contains(msg, "解析失败") {
		t.Fatalf("解析失败: %v", err)
	}
	for _, mark := range []string{
		"HTTP 403",
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"context deadline exceeded",
		"TLS handshake",
		"x509",
		"proxyconnect",
		"unsupported protocol scheme",
		"server gave HTTP response to HTTPS client",
		// SNI 级别阻断时 TLS 握手被掐断，Go 报的就是一个裸 EOF
		"EOF",
	} {
		if strings.Contains(msg, mark) {
			t.Skipf("站点在本机不可达，跳过冒烟测试: %v", err)
		}
	}
	t.Fatalf("搜索失败: %v", err)
}

// TestOverseasEnginesRegistered 校验四个境外引擎都注册进了引擎表。
func TestOverseasEnginesRegistered(t *testing.T) {
	reg := NewRegistry()
	RegisterBt4g(reg)
	RegisterNyaa(reg)
	RegisterYts(reg)
	RegisterSolidtorrents(reg)

	cases := []struct {
		name        string
		region      Region
		description string
	}{
		{"bt4g", RegionOverseas, "BT4G 磁力搜索（RSS 接口，绕过 Cloudflare）"},
		{"nyaa", RegionOverseas, "nyaa.si 动漫音频资源搜索（RSS 优先）"},
		{"yts", RegionOverseas, "YTS 电影种子搜索（JSON 接口）"},
		{"solidtorrents", RegionOverseas, "solidtorrents.to 磁力搜索"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specs, err := reg.Select([]string{tc.name})
			if err != nil {
				t.Fatalf("查找引擎失败: %v", err)
			}
			if len(specs) != 1 {
				t.Fatalf("期望 1 个引擎，实际 %d 个", len(specs))
			}
			spec := specs[0]
			if spec.Region != tc.region {
				t.Errorf("区域 = %q，期望 %q", spec.Region, tc.region)
			}
			if spec.Description != tc.description {
				t.Errorf("描述 = %q，期望 %q", spec.Description, tc.description)
			}
			engine := spec.New(nil)
			if engine.Name() != tc.name {
				t.Errorf("构造出的引擎名 = %q，期望 %q", engine.Name(), tc.name)
			}
			if engine.Description() != tc.description {
				t.Errorf("引擎描述 = %q，期望 %q", engine.Description(), tc.description)
			}
		})
	}
}

// fakeClient 是可编排的假客户端，用来验证重试行为。
type fakeClient struct {
	// bodies 依次返回的响应体
	bodies [][]byte
	// errs 依次返回的错误
	errs []error
	// calls 已经发起的请求次数
	calls int
}

// Get 返回编排好的下一次响应。
func (f *fakeClient) Get(_ context.Context, _ string) ([]byte, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.bodies) {
		return f.bodies[i], nil
	}
	return nil, fmt.Errorf("没有更多响应")
}

// GetString 返回编排好的下一次响应的字符串形式。
func (f *fakeClient) GetString(_ context.Context, _ string) (string, error) {
	b, err := f.Get(context.Background(), "")
	return string(b), err
}

// TestFetchWithRetry 校验 5xx 会重试、其它错误不重试。
func TestFetchWithRetry(t *testing.T) {
	cases := []struct {
		name      string
		errs      []error
		bodies    [][]byte
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "首次成功不重试",
			bodies:    [][]byte{[]byte("ok")},
			wantCalls: 1,
		},
		{
			name:      "5xx 重试一次后成功",
			errs:      []error{fmt.Errorf("请求失败: via 直连: HTTP 500")},
			bodies:    [][]byte{nil, []byte("ok")},
			wantCalls: 2,
		},
		{
			name:      "5xx 重试仍然失败",
			errs:      []error{fmt.Errorf("请求失败: via 直连: HTTP 500"), fmt.Errorf("请求失败: via 直连: HTTP 502")},
			wantCalls: 2,
			wantErr:   true,
		},
		{
			name:      "连接失败不重试",
			errs:      []error{fmt.Errorf(`Get "https://example.com": EOF`)},
			wantCalls: 1,
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{errs: tc.errs, bodies: tc.bodies}
			body, err := fetchWithRetry(context.Background(), client, "https://example.com")
			if tc.wantErr && err == nil {
				t.Fatal("期望出错，实际成功")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望成功，实际出错: %v", err)
			}
			if !tc.wantErr && string(body) != "ok" {
				t.Errorf("响应体 = %q，期望 ok", body)
			}
			if client.calls != tc.wantCalls {
				t.Errorf("请求次数 = %d，期望 %d", client.calls, tc.wantCalls)
			}
		})
	}
}

// TestIsServerError 校验 5xx 与 4xx 的区分。
func TestIsServerError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"500 是服务端错误", fmt.Errorf("via 直连: HTTP 500"), true},
		{"502 是服务端错误", fmt.Errorf("via 直连: HTTP 502"), true},
		{"403 不是服务端错误", fmt.Errorf("HTTP 403 (直连): https://x"), false},
		{"连接错误不算", fmt.Errorf(`Get "https://x": EOF`), false},
		{"没有错误不算", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isServerError(tc.err); got != tc.want {
				t.Errorf("isServerError = %v，期望 %v", got, tc.want)
			}
		})
	}
}
