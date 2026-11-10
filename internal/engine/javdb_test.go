package engine

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// TestJavDBQuery 校验检索词与番号识别：查询不为空就查 JavDB，番号只决定过滤方式。
func TestJavDBQuery(t *testing.T) {
	cases := []struct {
		name  string
		query string
		term  string
		code  string
		ok    bool
	}{
		{name: "标准番号", query: "SSIS-001", term: "SSIS-001", code: "SSIS001", ok: true},
		{name: "无连字符", query: "ssis001", term: "ssis001", code: "SSIS001", ok: true},
		{name: "番号带后缀关键词", query: "SSIS-001 中文字幕", term: "SSIS-001", code: "SSIS001", ok: true},
		{name: "番号带标点", query: "SSIS-001,", term: "SSIS-001", code: "SSIS001", ok: true},
		{name: "番号带版本后缀", query: "SSIS-001-C", term: "SSIS-001-C", code: "SSIS001C", ok: true},
		{name: "小写带后缀", query: "ssis001c", term: "ssis001c", code: "SSIS001C", ok: true},
		{name: "番号后紧跟中文", query: "SSIS-001中文字幕", term: "SSIS-001", code: "SSIS001", ok: true},
		{name: "两段数字番号", query: "T28-001", term: "T28-001", code: "T28001", ok: true},
		{name: "片名按原词查", query: "葵司", term: "葵司", ok: true},
		{name: "英文关键词也查", query: "hello world", term: "hello world", ok: true},
		{name: "普通关键词不误判番号", query: "ubuntu 24.04", term: "ubuntu 24.04", ok: true},
		{name: "分辨率不算番号", query: "1080p", term: "1080p", ok: true},
		{name: "BD 分辨率不算番号", query: "BD1080", term: "BD1080", ok: true},
		{name: "编码不算番号", query: "h264", term: "h264", ok: true},
		{name: "空查询不查", query: "   ", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, code, ok := javdbQuery(Options{Query: tc.query})
			if ok != tc.ok {
				t.Fatalf("是否检索 = %v，期望 %v", ok, tc.ok)
			}
			if term != tc.term || code != tc.code {
				t.Errorf("检索词/番号 = %q/%q，期望 %q/%q", term, code, tc.term, tc.code)
			}
		})
	}
}

// TestJavDBParseSearch 校验检索页的条目解析与番号过滤。
func TestJavDBParseSearch(t *testing.T) {
	entries := NewJavDB(nil).parseSearch(loadFixture(t, "javdb_search.md"))
	if len(entries) != 5 {
		t.Fatalf("期望 5 个条目，实际 %d 个", len(entries))
	}
	first := entries[0]
	if first.Code != "SSIS-001" {
		t.Errorf("番号 = %q，期望 SSIS-001", first.Code)
	}
	if first.URL != "https://javdb.com/v/ZY5eq" {
		t.Errorf("详情页 = %q", first.URL)
	}
	if !strings.Contains(first.Title, "一ヶ月間の禁欲") {
		t.Errorf("片名 = %q，应从链接标题属性里取到", first.Title)
	}

	filtered := filterJavDBEntries(entries, "SSIS001")
	if len(filtered) != 1 {
		t.Fatalf("按番号过滤后期望 1 个条目，实际 %d 个", len(filtered))
	}
	if filtered[0].URL != "https://javdb.com/v/ZY5eq" {
		t.Errorf("过滤后详情页 = %q", filtered[0].URL)
	}

	// 页面有番号但都不匹配时不做回退，避免把无关影片的磁力混进来
	if got := filterJavDBEntries(entries, "ABCD999"); len(got) != 0 {
		t.Errorf("无匹配时不该回退，实际 %d 个", len(got))
	}
	// 查 SSIS-001 时，同番号带后缀的条目也要一起保留
	withSuffix := append(append([]javdbEntry{}, entries...), javdbEntry{Code: "SSIS-001-UC", URL: "https://javdb.com/v/uc"})
	if got := filterJavDBEntries(withSuffix, "SSIS001"); len(got) != 2 {
		t.Errorf("带后缀的同番号条目应一起保留，实际 %d 个", len(got))
	}
	// 页面完全没标番号（站点改版）时才退回按相关度取前几条
	noCodes := []javdbEntry{{URL: "https://javdb.com/v/a"}, {URL: "https://javdb.com/v/b"}}
	if got := filterJavDBEntries(noCodes, "ABCD999"); len(got) != len(noCodes) {
		t.Errorf("页面没标番号时应退回全部条目，实际 %d 个", len(got))
	}
}

// TestJavDBParseDetail 校验详情页磁力解析。
func TestJavDBParseDetail(t *testing.T) {
	entry := javdbEntry{Code: "SSIS-001", Title: "サンプル作品", URL: "https://javdb.com/v/ZY5eq"}
	results := NewJavDB(nil).parseDetail(loadFixture(t, "javdb_detail.md"), entry, time.Now())
	if len(results) != 3 {
		t.Fatalf("期望 3 条磁力，实际 %d 条", len(results))
	}
	first := results[0]
	if first.InfoHash != "fcef4eb87ac04a85c5285f8377b2856e9e40b9f1" {
		t.Errorf("info hash = %q", first.InfoHash)
	}
	if first.Source != "javdb" {
		t.Errorf("来源 = %q，期望 javdb", first.Source)
	}
	if first.Category != javdbCategory {
		t.Errorf("分类 = %q，期望 %q", first.Category, javdbCategory)
	}
	if first.URL != entry.URL {
		t.Errorf("详情页 = %q，期望 %q", first.URL, entry.URL)
	}
	// 链接文字本身带番号时不该再重复加前缀
	if strings.HasPrefix(first.Title, "SSIS-001 SSIS-001") {
		t.Errorf("标题重复了几次番号: %q", first.Title)
	}
	if !strings.Contains(first.Title, "无码破解") {
		t.Errorf("标题 = %q，应保留磁力自带的文件名", first.Title)
	}
	if !strings.Contains(first.Title, "サンプル作品") {
		t.Errorf("标题 = %q，应带上检索页的片名", first.Title)
	}
	if first.SizeBytes != magnet.ParseSize("6.33GB") {
		t.Errorf("大小 = %d，期望 %d", first.SizeBytes, magnet.ParseSize("6.33GB"))
	}
	if got := first.PublishDate.Format("2006-01-02"); got != "2023-11-18" {
		t.Errorf("发布时间 = %q，期望 2023-11-18", got)
	}
	if got := results[1].PublishDate.Format("2006-01-02"); got != "2021-02-21" {
		t.Errorf("第二条发布时间 = %q，期望 2021-02-21", got)
	}
	if strings.Contains(results[2].Title, "file(s)") {
		t.Errorf("标题里残留文件数: %q", results[2].Title)
	}
}

// TestJavDBSearchThroughReader 校验整条链路：检索页 → 详情页 → 结果。
func TestJavDBSearchThroughReader(t *testing.T) {
	searchURL := "https://r.jina.ai/https://javdb.com/search?f=all&q=SSIS-001"
	detailURL := "https://r.jina.ai/https://javdb.com/v/ZY5eq"
	client := routeClient{bodies: map[string][]byte{
		searchURL: loadFixture(t, "javdb_search.md"),
		detailURL: loadFixture(t, "javdb_detail.md"),
	}}
	results, err := NewJavDB(client).Search(context.Background(), Options{Query: "SSIS-001"})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("期望 3 条结果，实际 %d 条", len(results))
	}
	if results[0].Rank != 0 || results[2].Rank != 2 {
		t.Errorf("排序位次 = %d/%d，期望 0/2", results[0].Rank, results[2].Rank)
	}
	if results[0].IndexMatched {
		t.Error("番号检索的结果不该标成引擎索引匹配")
	}
}

// TestJavDBPlainQuerySearchesPage 校验普通关键词也会查检索页，只是站点给出查无结果。
func TestJavDBPlainQuerySearchesPage(t *testing.T) {
	searchURL := "https://r.jina.ai/https://javdb.com/search?f=all&q=" + url.QueryEscape("ubuntu 24.04")
	// 只预设检索页：万一代码去抓详情页，routeClient 会因未预设的 URL 报错
	client := routeClient{bodies: map[string][]byte{
		searchURL: []byte("## Keywords ubuntu 24.04 Result\n\nNo content yet\n"),
	}}
	results, err := NewJavDB(client).Search(context.Background(), Options{Query: "ubuntu 24.04"})
	if err != nil {
		t.Fatalf("查无结果时不该报错: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("查无结果应返回 0 条，实际 %d 条", len(results))
	}
}

// TestJavDBTitleQuery 校验片名检索：检索页 → 前两条详情页 → 结果，标题带上片名。
func TestJavDBTitleQuery(t *testing.T) {
	searchURL := "https://r.jina.ai/https://javdb.com/search?f=all&q=" + url.QueryEscape("葵司")
	detail := loadFixture(t, "javdb_detail.md")
	// 片名模式按相关度只取前两条，因此只需预设前两个详情页
	client := routeClient{bodies: map[string][]byte{
		searchURL: loadFixture(t, "javdb_search.md"),
		"https://r.jina.ai/https://javdb.com/v/ZY5eq": detail,
		"https://r.jina.ai/https://javdb.com/v/4vQJb": detail,
	}}
	results, err := NewJavDB(client).Search(context.Background(), Options{Query: "葵司"})
	if err != nil {
		t.Fatalf("片名检索失败: %v", err)
	}
	// 两份详情页给的是同一批磁力，去重后应剩 3 条
	if len(results) != 3 {
		t.Fatalf("期望 3 条结果，实际 %d 条", len(results))
	}
	if !strings.Contains(results[0].Title, "一ヶ月間の禁欲") {
		t.Errorf("标题 = %q，应带上检索页的片名", results[0].Title)
	}
	// 片名检索靠的是 JavDB 自己的索引，需要标记出来让聚合层别按字面词丢掉
	if !results[0].IndexMatched {
		t.Error("片名检索的结果应标记为引擎索引匹配")
	}
}

// TestJavDBRegister 校验注册信息。
func TestJavDBRegister(t *testing.T) {
	reg := NewRegistry()
	RegisterJavDB(reg)
	specs := reg.Specs()
	if len(specs) != 1 {
		t.Fatalf("期望 1 个引擎，实际 %d 个", len(specs))
	}
	spec := specs[0]
	if spec.Name != "javdb" {
		t.Errorf("引擎名 = %q，期望 javdb", spec.Name)
	}
	if spec.Region != RegionOverseas {
		t.Errorf("区域 = %q，期望 overseas（需要代理）", spec.Region)
	}
	engine := spec.New(nil)
	if engine.Name() != "javdb" || engine.Description() == "" {
		t.Errorf("构造出的引擎 = %q / %q", engine.Name(), engine.Description())
	}
}

// TestJavDBJoinsInflightCalls 校验拆词变体并发调用同一番号时只发一次请求。
func TestJavDBJoinsInflightCalls(t *testing.T) {
	engine := NewJavDB(nil)
	call, leader := engine.joinCall("SSIS-001")
	if !leader {
		t.Fatal("第一个调用应该是 leader")
	}
	again, leaderAgain := engine.joinCall("SSIS-001")
	if leaderAgain {
		t.Fatal("同一检索词的后续调用不该再去取页面")
	}
	if again != call {
		t.Fatal("同一检索词应共享同一份进行中请求")
	}
	// 不同检索词互不影响
	if _, ok := engine.joinCall("SSIS091"); !ok {
		t.Fatal("不同检索词应各自发起请求")
	}

	engine.finishCall("SSIS-001", []model.Result{{Title: "SSIS-001 test"}}, nil)
	<-call.done
	if call.err != nil || len(call.results) != 1 {
		t.Fatalf("等待者拿到的结果 = %+v / %v", call.results, call.err)
	}
	// 上一轮结束后，同一检索词可以重新发起
	if _, leader := engine.joinCall("SSIS-001"); !leader {
		t.Fatal("上一次结束后应能重新发起")
	}
}

// TestSpecProbeTerm 校验自检查询词的默认值与覆盖规则。
func TestSpecProbeTerm(t *testing.T) {
	if got := (Spec{Name: "plain"}).ProbeTerm(); got != DefaultProbeQuery {
		t.Errorf("默认探针词 = %q，期望 %q", got, DefaultProbeQuery)
	}
	if got := (Spec{Name: "javdb", ProbeQuery: "SSIS-001"}).ProbeTerm(); got != "SSIS-001" {
		t.Errorf("自定义探针词 = %q，期望 SSIS-001", got)
	}
}

// TestJavDBSearchEmptyPage 校验检索页没解析到条目时不会静默成功。
func TestJavDBSearchEmptyPage(t *testing.T) {
	searchURL := "https://r.jina.ai/https://javdb.com/search?f=all&q=SSIS-001"
	blocked := routeClient{bodies: map[string][]byte{searchURL: []byte("Just a moment...")}}
	if _, err := NewJavDB(blocked).Search(context.Background(), Options{Query: "SSIS-001"}); err == nil {
		t.Fatal("解析不到条目时应报错，否则自检会把被限流当成可用")
	}

	empty := routeClient{bodies: map[string][]byte{searchURL: []byte("## Keywords SSIS-001 Result\n\nNo content yet\n")}}
	results, err := NewJavDB(empty).Search(context.Background(), Options{Query: "SSIS-001"})
	if err != nil {
		t.Fatalf("站点明确查无结果时不该报错: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("查无结果应返回 0 条，实际 %d 条", len(results))
	}
}

// TestJavDBParseSearchMirror 校验详情页链接不写死域名，换镜像也能解析。
func TestJavDBParseSearchMirror(t *testing.T) {
	body := []byte("[Playable **SSIS-001** 片名](https://javdb580.com/v/ZY5eq \"片名\")\n")
	entries := NewJavDB(nil).parseSearch(body)
	if len(entries) != 1 {
		t.Fatalf("期望 1 个条目，实际 %d 个", len(entries))
	}
	if entries[0].URL != "https://javdb580.com/v/ZY5eq" {
		t.Errorf("详情页 = %q，应接受镜像域名", entries[0].URL)
	}
	if entries[0].Code != "SSIS-001" {
		t.Errorf("番号 = %q，期望 SSIS-001", entries[0].Code)
	}
}

// TestJavDBVariantSkipped 校验拆词变体不请求 JavDB，避免碎片词带回无关结果。
func TestJavDBVariantSkipped(t *testing.T) {
	// routeClient 没预设任何 URL，只要发了请求这里就会失败
	results, err := NewJavDB(routeClient{bodies: map[string][]byte{}}).
		Search(context.Background(), Options{Query: "999", Variant: true})
	if err != nil {
		t.Fatalf("拆词变体不该发请求: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("拆词变体应返回 0 条，实际 %d 条", len(results))
	}
}
