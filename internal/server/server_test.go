package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
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

func TestPaginate(t *testing.T) {
	cases := []struct {
		total, perPage, page         int
		wantStart, wantEnd, wantPage int
		wantPages                    int
	}{
		{total: 100, perPage: 20, page: 1, wantStart: 0, wantEnd: 20, wantPage: 1, wantPages: 5},
		{total: 100, perPage: 20, page: 3, wantStart: 40, wantEnd: 60, wantPage: 3, wantPages: 5},
		{total: 100, perPage: 20, page: 5, wantStart: 80, wantEnd: 100, wantPage: 5, wantPages: 5},
		// 越界页码收敛到最后一页
		{total: 100, perPage: 20, page: 9, wantStart: 80, wantEnd: 100, wantPage: 5, wantPages: 5},
		// 不足一页
		{total: 7, perPage: 20, page: 1, wantStart: 0, wantEnd: 7, wantPage: 1, wantPages: 1},
		// 空结果
		{total: 0, perPage: 20, page: 1, wantStart: 0, wantEnd: 0, wantPage: 1, wantPages: 1},
	}
	for _, c := range cases {
		start, end, page, pages := paginate(c.total, c.perPage, c.page)
		if start != c.wantStart || end != c.wantEnd || page != c.wantPage || pages != c.wantPages {
			t.Errorf("paginate(%d,%d,%d) = (%d,%d,%d,%d)，期望 (%d,%d,%d,%d)",
				c.total, c.perPage, c.page, start, end, page, pages,
				c.wantStart, c.wantEnd, c.wantPage, c.wantPages)
		}
	}
}

func TestSettingsAPI(t *testing.T) {
	srv := New(app.Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 默认没有代理，作用范围是 region
	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	var cur Settings
	if err := json.NewDecoder(resp.Body).Decode(&cur); err != nil {
		t.Fatalf("解析设置失败: %v", err)
	}
	resp.Body.Close()
	if cur.Proxy != "" || cur.ProxyScope != app.ProxyScopeRegion {
		t.Errorf("默认设置 = %+v，期望空代理 + region", cur)
	}

	// 保存后应能读回
	body, _ := json.Marshal(Settings{Proxy: "http://127.0.0.1:7890", ProxyScope: app.ProxyScopeAll})
	resp2, err := http.Post(ts.URL+"/api/settings", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("保存设置状态码 = %d，期望 200", resp2.StatusCode)
	}

	resp3, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	var after Settings
	if err := json.NewDecoder(resp3.Body).Decode(&after); err != nil {
		t.Fatalf("解析设置失败: %v", err)
	}
	resp3.Body.Close()
	if after.Proxy != "http://127.0.0.1:7890" || after.ProxyScope != app.ProxyScopeAll {
		t.Errorf("保存后读回 = %+v，期望代理与 all 范围", after)
	}
}

func TestSSEWriterFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	sse, err := newSSEWriter(rec)
	if err != nil {
		t.Fatalf("创建 SSE 写入器失败: %v", err)
	}
	sse.send("result", map[string]any{"title": "x"})
	got := rec.Body.String()
	if !strings.HasPrefix(got, "event: result\ndata: ") {
		t.Errorf("SSE 事件格式不对: %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("SSE 事件应以空行结尾: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q，应为 text/event-stream", ct)
	}
}

func TestSettingsPreferCommandLineProxy(t *testing.T) {
	// 命令行传入的代理应当作为界面的初始设置，界面打开即可见
	srv := New(app.Options{Proxy: []string{"http://127.0.0.1:7890"}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/settings")
	if err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	defer resp.Body.Close()
	var got Settings
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("解析设置失败: %v", err)
	}
	if got.Proxy != "http://127.0.0.1:7890" {
		t.Errorf("初始代理 = %q，期望 %q", got.Proxy, "http://127.0.0.1:7890")
	}
	if got.ProxyScope != app.ProxyScopeRegion {
		t.Errorf("初始作用范围 = %q，期望 %q", got.ProxyScope, app.ProxyScopeRegion)
	}
}

func TestSearchOptionsUseSavedProxy(t *testing.T) {
	srv := New(app.Options{})
	srv.settings.Proxy = "http://127.0.0.1:7890"
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=ubuntu", nil)
	opts, _, _, err := srv.searchOptions(req)
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if len(opts.Proxy) != 1 || opts.Proxy[0] != "http://127.0.0.1:7890" {
		t.Errorf("检索参数里的代理 = %v，期望使用界面保存的代理", opts.Proxy)
	}
}

// TestFilterLabelsHaveTips 校验每个筛选标签都带得上像样的提示文本。
// 这条断言是回归网：曾经把提示文本和标签文字写反，导致解释被当成正文渲染、布局塌掉。
func TestFilterLabelsHaveTips(t *testing.T) {
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("读取内嵌页面失败: %v", err)
	}
	html := string(raw)

	labels := 0
	for _, m := range regexp.MustCompile(`<label class="tip" data-tip="([^"]*)">([^<]*)<span class="q">\?</span></label>`).FindAllStringSubmatch(html, -1) {
		labels++
		tip, name := m[1], strings.TrimSpace(m[2])
		if name == "" {
			t.Errorf("第 %d 个提示标签没有可见文字", labels)
		}
		if len([]rune(tip)) < 10 {
			t.Errorf("标签 %q 的提示文本太短: %q", name, tip)
		}
		if !strings.Contains(tip, name) && !strings.Contains(name, tip[:min(len(tip), len(name))]) {
			// 提示文本里不必出现标签名，但也不该是标签名本身
			if tip == name {
				t.Errorf("标签 %q 的提示文本与标签名相同，疑似写反", name)
			}
		}
	}
	if labels < 5 {
		t.Errorf("带提示的筛选项只有 %d 个，应至少覆盖引擎、死链、最少对等端、冷门席位、每簇最多、每页、代理", labels)
	}
}

// min 返回两个数里较小的。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
