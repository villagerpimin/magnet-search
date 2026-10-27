package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/httpx"
)

// loadFixture 读取 testdata 目录下的真实抓取样本。
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取样本失败 %s: %v", path, err)
	}
	return body
}

// requireLive 判断是否要访问真实站点。
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MS_LIVE") != "1" {
		t.Skip("设置 MS_LIVE=1 才访问真实站点")
	}
}

// liveClient 构造访问真实站点用的 HTTP 客户端。
func liveClient(t *testing.T) ClientProvider {
	t.Helper()
	client, err := httpx.New(httpx.Options{Timeout: 20 * time.Second, Retries: 2})
	if err != nil {
		t.Fatalf("创建 HTTP 客户端失败: %v", err)
	}
	return client
}

// isBlocked 判断错误是不是站点侧的访问拦截。
func isBlocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "HTTP 403")
}

// TestRegisterEngines 校验四个引擎都注册进了引擎表。
func TestRegisterEngines(t *testing.T) {
	reg := NewRegistry()
	RegisterApibay(reg)
	RegisterTokyotosho(reg)
	RegisterRutor(reg)
	RegisterBitsearch(reg)

	cases := []struct {
		name        string
		region      Region
		description string
	}{
		{"apibay", RegionAny, "The Pirate Bay 官方 JSON API"},
		{"tokyotosho", RegionOverseas, "东京所 tokyotosho.info tracker 搜索"},
		{"rutor", RegionOverseas, "rutor.info 俄文资源搜索"},
		{"bitsearch", RegionAny, "bitsearch.eu 磁力搜索"},
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
			if spec.Name != tc.name {
				t.Errorf("引擎名 = %q，期望 %q", spec.Name, tc.name)
			}
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
			if engine.Description() == "" {
				t.Error("引擎描述不能为空")
			}
		})
	}
}

// routeClient 按 URL 返回预设响应，供需要多次请求的引擎测试使用。
type routeClient struct {
	// bodies URL 到响应体的映射
	bodies map[string][]byte
}

// Get 按 URL 返回预设响应。
func (c routeClient) Get(_ context.Context, url string) ([]byte, error) {
	body, ok := c.bodies[url]
	if !ok {
		return nil, fmt.Errorf("未预设的 URL: %s", url)
	}
	return body, nil
}

// GetString 按 URL 返回预设响应的字符串形式。
func (c routeClient) GetString(ctx context.Context, url string) (string, error) {
	body, err := c.Get(ctx, url)
	return string(body), err
}
