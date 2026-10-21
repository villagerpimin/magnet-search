package aggregate

import (
	"strings"
	"testing"
)

// tokenSetOf 把词元列表转成 "text:class" 集合，便于断言。
func tokenSetOf(tokens []Token) map[string]bool {
	out := map[string]bool{}
	for _, tok := range tokens {
		out[tok.Text+":"+className(tok.Class)] = true
	}
	return out
}

// className 返回词元类别的可读名。
func className(c TokenClass) string {
	switch c {
	case TokenName:
		return "name"
	case TokenNum:
		return "num"
	case TokenVer:
		return "ver"
	case TokenLang:
		return "lang"
	case TokenQuality:
		return "quality"
	case TokenEdition:
		return "edition"
	case TokenGroup:
		return "group"
	}
	return "?"
}

func TestClassifyTokensReleaseGroupAndTags(t *testing.T) {
	got := tokenSetOf(ClassifyTokens("[SubsPlease] Some Anime - 03 [1080p][x265][Multiple Subtitle][v2].mkv"))
	want := []string{
		"subsplease:group", "some:name", "anime:name", "03:num",
		"1080p:quality", "x265:quality", "multiple:lang", "v2:ver",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("缺少词元 %q，实际集合: %v", w, got)
		}
	}
}

func TestClassifyTokensTrailingGroup(t *testing.T) {
	got := tokenSetOf(ClassifyTokens("Movie.2020.1080p.BluRay-SPARKS"))
	want := []string{"2020:num", "1080p:quality", "bluray:quality", "sparks:group"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("缺少词元 %q，实际集合: %v", w, got)
		}
	}
	// 2020.1080 不能被并成版本号
	if got["2020.1080:ver"] {
		t.Errorf("年份与分辨率被错误合并成版本号: %v", got)
	}
}

func TestResolutionTierAndLangKey(t *testing.T) {
	cases := []struct {
		title string
		tier  string
		lang  string
	}{
		{"[G] Foo 2160p WEB-DL", "uhd", "any"},
		{"[G] Foo 1080p BluRay", "1080p", "any"},
		{"[G] Foo 720p x264", "720p", "any"},
		{"[G] Foo 480p", "sd", "any"},
		{"[G] Foo [Chinese]", "any", "zh"},
		{"[G] Foo [English]", "any", "en"},
		{"[G] Foo [Dual Audio]", "any", "multi"},
	}
	for _, c := range cases {
		if got := ResolutionTier(c.title); got != c.tier {
			t.Errorf("ResolutionTier(%q) = %q，期望 %q", c.title, got, c.tier)
		}
		if got := LangKey(c.title); got != c.lang {
			t.Errorf("LangKey(%q) = %q，期望 %q", c.title, got, c.lang)
		}
	}
}

func TestClusterKeySeparatesQualityGroupsGroups(t *testing.T) {
	a := "[SubsPlease] Some Anime - 01 (1080p) [x265].mkv"
	b := "[EMBER] Some Anime - 01 (1080p) [x265].mkv"
	c := "[SubsPlease] Some Anime - 01 (720p) [x264].mkv"

	if ClusterKey(a) != ClusterKey(b) {
		t.Errorf("不同发布组的同一资源应同簇:\n a=%q\n b=%q", ClusterKey(a), ClusterKey(b))
	}
	if ClusterKey(a) == ClusterKey(c) {
		t.Errorf("不同画质应不同簇: %q", ClusterKey(a))
	}
}

func TestContentKeyExcludesGroupAndQuality(t *testing.T) {
	key := ContentKey("[SubsPlease] Some Anime - 01 [1080p][x265].mkv")
	for _, unwanted := range []string{"subsplease", "1080p", "x265"} {
		if hasSubstring(key, unwanted) {
			t.Errorf("内容键不应包含 %q: %q", unwanted, key)
		}
	}
	for _, wanted := range []string{"some", "anime", "01"} {
		if !hasSubstring(key, wanted) {
			t.Errorf("内容键应包含 %q: %q", wanted, key)
		}
	}
}

func TestComputeRelevance(t *testing.T) {
	cases := []struct {
		name       string
		in         RelevanceInput
		wantPassed bool
		minScore   float64
	}{
		{
			name:       "完全命中",
			in:         RelevanceInput{Query: "ubuntu", Title: "Ubuntu 22.04.1 LTS Desktop amd64 iso", Rank: 0},
			wantPassed: true,
			minScore:   0.9,
		},
		{
			name:       "无关结果被淘汰",
			in:         RelevanceInput{Query: "ubuntu", Title: "Windows 11 Pro 24H2 Retail", Rank: 0},
			wantPassed: false,
		},
		{
			name:       "版本号前缀命中",
			in:         RelevanceInput{Query: "ubuntu 24.04", Title: "[Ubuntu 24.04.1 LTS]", Rank: 0},
			wantPassed: true,
			minScore:   0.9,
		},
		{
			name:       "集号命中",
			in:         RelevanceInput{Query: "some anime 03", Title: "[G] Some Anime - 03 [1080p]", Rank: 0},
			wantPassed: true,
			minScore:   0.8,
		},
		{
			name:       "集号不符被淘汰",
			in:         RelevanceInput{Query: "some anime 04", Title: "[G] Some Anime - 03 [1080p]", Rank: 0},
			wantPassed: false,
		},
		{
			name:       "中文查询命中日文标题",
			in:         RelevanceInput{Query: "葬送的芙莉莲", Title: "[G] 葬送のフリーレン [1080p]", Rank: 0},
			wantPassed: true,
			minScore:   0.5,
		},
		{
			name:       "跨语种不直接匹配",
			in:         RelevanceInput{Query: "哈利波特", Title: "Harry Potter 2001", Rank: 0},
			wantPassed: false,
		},
	}
	for _, c := range cases {
		got := ComputeRelevance(c.in)
		if got.Passed != c.wantPassed {
			t.Errorf("%s: Passed = %v，期望 %v（原因: %s）", c.name, got.Passed, c.wantPassed, got.Reason)
		}
		if c.minScore > 0 && got.Score < c.minScore {
			t.Errorf("%s: Score = %v，期望不小于 %v", c.name, got.Score, c.minScore)
		}
	}
}

func TestComputeRelevanceCrossScriptReliesOnEngineRank(t *testing.T) {
	// 中文查询与英文标题没有文本重叠，命中依赖引擎自身排序位置
	got := ComputeRelevance(RelevanceInput{Query: "哈利波特", Title: "Harry Potter 2001", Rank: 0})
	if got.Passed {
		t.Errorf("跨语种文本不应直接通过硬门: %+v", got)
	}
	if got.Score <= 0 {
		t.Errorf("引擎排序靠前时分数应仍为正: %+v", got)
	}
	if got.EnginePrior != 1 {
		t.Errorf("Rank=0 的引擎先验应为 1，实际 %v", got.EnginePrior)
	}
}

func TestQueryFilters(t *testing.T) {
	quality, lang := QueryFilters("ubuntu 1080p chinese")
	if len(quality) == 0 {
		t.Errorf("应提取到画质过滤条件，实际 %v", quality)
	}
	if len(lang) == 0 || lang[0] != "zh" {
		t.Errorf("应提取到语言过滤条件 zh，实际 %v", lang)
	}
}

// hasSubstring 判断字符串是否包含子串。
func hasSubstring(s, sub string) bool {
	return len(sub) > 0 && strings.Contains(s, sub)
}
