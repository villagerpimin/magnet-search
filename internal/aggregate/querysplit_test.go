package aggregate

import (
	"reflect"
	"testing"
)

// TestQueryTermsFiltersAndDedupes 校验独立关键词只保留名称/数字/版本号，
// 去掉停用词、画质与语言标记，并按大小写不敏感去重。
func TestQueryTermsFiltersAndDedupes(t *testing.T) {
	got := QueryTerms("The Ubuntu UBUNTU 24.04 1080p Chinese", 0)
	want := []string{"ubuntu", "24.04"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryTerms = %v，期望 %v", got, want)
	}
}

// TestQueryTermsKeepsCJKRunsWhole 校验 CJK 连续文本不会被拆成单个汉字。
func TestQueryTermsKeepsCJKRunsWhole(t *testing.T) {
	got := QueryTerms("葬送的芙莉莲 1080p", 0)
	if len(got) != 1 || got[0] != "葬送的芙莉莲" {
		t.Fatalf("CJK 关键词应保持整体，实际 %v", got)
	}
}

// TestQueryTermsLimit 校验 max 限制关键词数量，max <= 0 表示不限制。
func TestQueryTermsLimit(t *testing.T) {
	query := "alpha beta gamma delta"
	limited := QueryTerms(query, 2)
	if !reflect.DeepEqual(limited, []string{"alpha", "beta"}) {
		t.Errorf("max=2 时 = %v，期望前两个关键词", limited)
	}
	all := QueryTerms(query, 0)
	if len(all) != 4 {
		t.Errorf("max=0 时应返回全部 4 个关键词，实际 %v", all)
	}
}

// TestQueryVariants 校验查询变体为原始查询加各独立关键词，边界情况按约定处理。
func TestQueryVariants(t *testing.T) {
	cases := []struct {
		name  string
		query string
		max   int
		want  []string
	}{
		{name: "空查询", query: "   ", want: nil},
		{name: "单个关键词只返回原查询", query: "ubuntu", want: []string{"ubuntu"}},
		{name: "多个关键词展开", query: "deepseek harness", want: []string{"deepseek harness", "deepseek", "harness"}},
		{name: "连字符也视为分隔符", query: "deepseek-harness", want: []string{"deepseek-harness", "deepseek", "harness"}},
		{name: "过滤后不足两个关键词只返回原查询", query: "deepseek 1080p", want: []string{"deepseek 1080p"}},
		{name: "限量后展开", query: "alpha beta gamma", max: 2, want: []string{"alpha beta gamma", "alpha", "beta"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := QueryVariants(c.query, c.max)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("QueryVariants(%q, %d) = %v，期望 %v", c.query, c.max, got, c.want)
			}
		})
	}
}
