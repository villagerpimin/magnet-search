package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pigzhu/magnet-search/internal/app"
)

func newTestServer() *httptest.Server {
	s := New(app.Options{})
	ts := httptest.NewServer(s.Handler())
	return ts
}

func TestServeIndexPage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("请求首页失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首页状态码 = %d，期望 200", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), "磁力聚合搜索") {
		t.Errorf("首页未包含预期内容: %q", string(body[:n]))
	}
}

func TestEnginesAPI(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/engines")
	if err != nil {
		t.Fatalf("请求引擎列表失败: %v", err)
	}
	defer resp.Body.Close()

	var list []engineInfo
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("解析引擎列表失败: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("引擎列表不应为空")
	}
	names := map[string]bool{}
	for _, e := range list {
		names[e.Name] = true
		if e.Region == "" {
			t.Errorf("引擎 %s 缺少区域标注", e.Name)
		}
	}
	for _, want := range []string{"apibay", "bitsearch", "bt4g", "nyaa", "yts"} {
		if !names[want] {
			t.Errorf("引擎列表缺少 %s，实际: %v", want, list)
		}
	}
}

func TestSearchAPIRequiresQuery(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/search")
	if err != nil {
		t.Fatalf("请求搜索失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("缺少关键词时状态码 = %d，期望 400", resp.StatusCode)
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "-"},
		{512, "512 B"},
		{1536, "1.5 KB"},
		{3654957056, "3.4 GB"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q，期望 %q", c.in, got, c.want)
		}
	}
}
