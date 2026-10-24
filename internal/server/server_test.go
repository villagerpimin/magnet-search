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
	"github.com/pigzhu/magnet-search/internal/store"
)

func newTestServer() *httptest.Server {
	s := New(app.Options{}, nil, store.AppSettings{})
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
	srv := New(app.Options{}, nil, store.AppSettings{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 默认没有代理，作用范围是 region；无数据库时所有开关关闭
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
	if cur.DBEnabled || cur.RequireLogin || cur.AllowRegister {
		t.Errorf("无数据库时默认开关 = %+v，期望全部关闭", cur)
	}

	// 保存后应能读回；没有数据库时请求里的开启开关会被强制关闭
	body, _ := json.Marshal(Settings{
		Proxy:         "http://127.0.0.1:7890",
		ProxyScope:    app.ProxyScopeAll,
		DBEnabled:     true,
		RequireLogin:  true,
		AllowRegister: true,
	})
	resp2, err := http.Post(ts.URL+"/api/settings", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	var saved Settings
	if err := json.NewDecoder(resp2.Body).Decode(&saved); err != nil {
		t.Fatalf("解析保存结果失败: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("保存设置状态码 = %d，期望 200", resp2.StatusCode)
	}
	if saved.DBEnabled || saved.RequireLogin || saved.AllowRegister {
		t.Errorf("无数据库时保存结果 = %+v，期望开关全部关闭", saved)
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
	if after.DBEnabled || after.RequireLogin || after.AllowRegister {
		t.Errorf("保存后读回开关 = %+v，期望全部关闭", after)
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
	srv := New(app.Options{Proxy: []string{"http://127.0.0.1:7890"}}, nil, store.AppSettings{})
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
	srv := New(app.Options{}, nil, store.AppSettings{})
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

// TestSearchOptionsPoolIndependentOfPerPage 校验结果池与每页条数解耦：
// 切换每页条数不应改变检索参数与缓存键，否则翻页会重跑网络检索。
func TestSearchOptionsPoolIndependentOfPerPage(t *testing.T) {
	srv := New(app.Options{}, nil, store.AppSettings{})
	reqA := httptest.NewRequest(http.MethodGet, "/api/search?q=ubuntu&per_page=10", nil)
	reqB := httptest.NewRequest(http.MethodGet, "/api/search?q=ubuntu&per_page=100", nil)

	optsA, perPageA, _, err := srv.searchOptions(reqA)
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	optsB, perPageB, _, err := srv.searchOptions(reqB)
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if perPageA != 10 || perPageB != 100 {
		t.Fatalf("每页条数 = %d/%d，期望 10/100", perPageA, perPageB)
	}
	if optsA.Limit != resultPoolSize || optsB.Limit != resultPoolSize {
		t.Errorf("结果池 = %d/%d，期望都等于 %d", optsA.Limit, optsB.Limit, resultPoolSize)
	}
	if searchCacheKey(optsA) != searchCacheKey(optsB) {
		t.Error("切换每页条数不应改变缓存键，否则会重新检索")
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

// TestAuthAPINoStore 校验未启用数据库时的认证状态。
func TestAuthAPINoStore(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/auth")
	if err != nil {
		t.Fatalf("请求认证状态失败: %v", err)
	}
	defer resp.Body.Close()
	var state authState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("解析认证状态失败: %v", err)
	}
	if state.DBEnabled || state.DBAvailable || state.NeedsSetup || state.LoggedIn ||
		state.RequireLogin || state.AllowRegister {
		t.Errorf("无数据库认证状态 = %+v，期望所有开关为 false", state)
	}
}

// TestRegisterAPINoStore 校验未启用数据库时注册被拒绝。
func TestRegisterAPINoStore(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := strings.NewReader(`{"username":"alice","password":"secret1"}`)
	resp, err := http.Post(ts.URL+"/api/register", "application/json", body)
	if err != nil {
		t.Fatalf("请求注册失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("无数据库注册状态码 = %d，期望 400", resp.StatusCode)
	}
}

// TestRequireAuthNoStore 校验未启用数据库时受保护接口直接放行。
func TestRequireAuthNoStore(t *testing.T) {
	srv := New(app.Options{}, nil, store.AppSettings{})
	called := false
	h := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/search?q=ubuntu", nil))
	if !called {
		t.Fatal("未启用数据库时 requireAuth 应放行")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("状态码 = %d，期望 204", rec.Code)
	}
}

// TestSettingsPartialPayload 校验只提交部分字段时布尔开关不会丢失。
func TestSettingsPartialPayload(t *testing.T) {
	srv := New(app.Options{}, nil, store.AppSettings{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := strings.NewReader(`{"proxy":"http://127.0.0.1:7890"}`)
	resp, err := http.Post(ts.URL+"/api/settings", "application/json", body)
	if err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("部分字段保存状态码 = %d，期望 200", resp.StatusCode)
	}
	var got Settings
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("解析设置失败: %v", err)
	}
	if got.Proxy != "http://127.0.0.1:7890" {
		t.Errorf("代理 = %q，期望保存的地址", got.Proxy)
	}
	if got.DBEnabled || got.RequireLogin || got.AllowRegister {
		t.Errorf("省略的布尔开关 = %+v，期望保持关闭", got)
	}
}

// TestNewAuthSettings 校验构造时按数据库可用性初始化安全开关。
func TestNewAuthSettings(t *testing.T) {
	none := New(app.Options{}, nil, store.AppSettings{RequireLogin: true, AllowRegister: true})
	none.settingsMu.RLock()
	gotNone := none.settings
	none.settingsMu.RUnlock()
	if gotNone.DBEnabled || gotNone.RequireLogin || gotNone.AllowRegister {
		t.Errorf("无数据库初始设置 = %+v，期望开关全部关闭", gotNone)
	}

	withDB := New(app.Options{}, &store.Store{}, store.AppSettings{RequireLogin: true, AllowRegister: true})
	withDB.settingsMu.RLock()
	gotDB := withDB.settings
	withDB.settingsMu.RUnlock()
	if !gotDB.DBEnabled || !gotDB.RequireLogin || !gotDB.AllowRegister {
		t.Errorf("有数据库初始设置 = %+v，期望数据库与安全开关均开启", gotDB)
	}
}

// TestRequireAuthHonorsLoginSwitch 校验关闭登录要求、或还没有账号时直接放行。
func TestRequireAuthHonorsLoginSwitch(t *testing.T) {
	cases := []struct {
		name     string
		settings store.AppSettings
	}{
		{name: "关闭登录要求时放行", settings: store.AppSettings{RequireLogin: false}},
		{name: "要求登录但还没有账号时放行", settings: store.AppSettings{RequireLogin: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := New(app.Options{}, &store.Store{}, c.settings)
			called := false
			h := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, "/api/search", nil))
			if !called {
				t.Fatalf("请求应被放行，状态码 %d", rec.Code)
			}
		})
	}
}

// TestSettingsNeedsAuth 校验安全设置变更与关闭数据库记录的登录要求。
func TestSettingsNeedsAuth(t *testing.T) {
	requireLogin := Settings{DBEnabled: true, RequireLogin: true, AllowRegister: false}
	openSearch := Settings{DBEnabled: true, RequireLogin: false, AllowRegister: false}

	cases := []struct {
		name  string
		cur   Settings
		next  Settings
		users int
		want  bool
	}{
		{"没有账号时无需登录", requireLogin, Settings{DBEnabled: true, RequireLogin: false}, 0, false},
		{"修改登录开关需要登录", requireLogin, Settings{DBEnabled: true, RequireLogin: false}, 1, true},
		{"修改注册开关需要登录", requireLogin, Settings{DBEnabled: true, RequireLogin: true, AllowRegister: true}, 1, true},
		{"要求登录时关闭数据库需要登录", requireLogin, Settings{DBEnabled: false, RequireLogin: true}, 1, true},
		{"不要求登录时关闭数据库无需登录", openSearch, Settings{DBEnabled: false, RequireLogin: false}, 1, false},
		{"只改代理无需登录", requireLogin, Settings{DBEnabled: true, RequireLogin: true, Proxy: "http://127.0.0.1:7890"}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := settingsNeedsAuth(c.cur, c.next, c.users); got != c.want {
				t.Errorf("settingsNeedsAuth = %v，期望 %v", got, c.want)
			}
		})
	}
}

// min 返回两个数里较小的。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
