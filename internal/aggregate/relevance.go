package aggregate

import (
	"strings"
	"unicode"
)

// RelevanceInput 是相关度计算的输入。
type RelevanceInput struct {
	// Query 搜索关键词
	Query string
	// Title 候选标题
	Title string
	// Rank 该结果在引擎返回列表中的位置，0 为第一条；-1 表示未知
	Rank int
}

// RelevanceResult 是相关度计算结果。
type RelevanceResult struct {
	// Score 最终相关度，取值 [0,1]
	Score float64
	// Coverage 查询必需词元的加权覆盖率
	Coverage float64
	// TextRel 纯文本相关度
	TextRel float64
	// EnginePrior 引擎排序位置先验
	EnginePrior float64
	// Passed 是否通过硬门
	Passed bool
	// Reason 未通过时的原因说明
	Reason string
}

// 相关度权重。
const (
	relWeightCoverage = 0.55
	relWeightOrder    = 0.20
	relWeightPhrase   = 0.15
	relWeightPrecise  = 0.10
	relWeightText     = 0.65
	relWeightPrior    = 0.35
	// relMinCoverage 是覆盖率硬门
	relMinCoverage = 0.6
	// relMinTextRel 是配合引擎排序兜底的文本相关度下限
	relMinTextRel = 0.15
	// relMaxPoorRank 引擎排序过于靠后时放弃该结果
	relMaxPoorRank = 10
)

// 词元在覆盖率中的权重。
const (
	weightName = 1.0
	weightNum  = 1.5
)

// ComputeRelevance 计算标题对查询的相关度。
//
// 相关度由两部分组成：文本匹配分与引擎排序先验。
// 引擎已经用自己的索引（含日文/英文条目的中文别名）完成过一次匹配，
// 因此它在结果中的位置是独立于文本重叠的真实信号；
// 中文查询检索日文/英文标题的海外资源时，主要靠这一项兜底。
func ComputeRelevance(in RelevanceInput) RelevanceResult {
	qTokens := classify(in.Query, false)
	tTokens := classify(in.Title, false)

	required, anchors := splitQueryTokens(qTokens)
	weights, matches, indices := matchRequired(required, tTokens)

	// 覆盖率
	total, got := 0.0, 0.0
	for i := range required {
		total += weights[i]
		got += weights[i] * matches[i]
	}
	coverage := 1.0
	if total > 0 {
		coverage = got / total
	}

	// 锚点必须全部命中，避免"第 4 集的低码率版本"压过"第 3 集的正确版本"
	for i, tok := range anchors {
		if matches[i] <= 0 {
			return RelevanceResult{
				Coverage:    coverage,
				EnginePrior: enginePrior(in.Rank),
				Passed:      false,
				Reason:      "锚点未命中: " + tok.Text,
			}
		}
	}

	orderBonus := orderScore(indices, matches)
	phraseBonus := 0.0
	if nq := Normalize(in.Query); nq != "" && strings.Contains(Normalize(in.Title), nq) {
		phraseBonus = 1
	}
	precision := precisionScore(required, matches, tTokens)

	textRel := relWeightCoverage*coverage +
		relWeightOrder*orderBonus +
		relWeightPhrase*phraseBonus +
		relWeightPrecise*precision
	prior := enginePrior(in.Rank)
	score := relWeightText*textRel + relWeightPrior*prior

	res := RelevanceResult{
		Score:       score,
		Coverage:    coverage,
		TextRel:     textRel,
		EnginePrior: prior,
		Passed:      true,
	}

	switch {
	case coverage < relMinCoverage:
		res.Passed = false
		res.Reason = "覆盖率不足"
	case textRel < relMinTextRel && in.Rank > relMaxPoorRank:
		res.Passed = false
		res.Reason = "文本相关度过低且引擎排序靠后"
	}
	return res
}

// QueryFilters 从查询中提取画质与语言过滤条件。
// 查询里出现的画质或语言词是硬条件，不参与覆盖率计算。
func QueryFilters(query string) (quality, lang []string) {
	for _, tok := range classify(query, false) {
		switch tok.Class {
		case TokenQuality:
			quality = append(quality, tok.Text)
		case TokenLang:
			if code, ok := langTokens[tok.Text]; ok {
				lang = append(lang, code)
			}
		}
	}
	return quality, lang
}

// splitQueryTokens 把查询词元分成必需词元与锚点词元。
func splitQueryTokens(tokens []Token) (required []Token, anchors []Token) {
	for _, tok := range tokens {
		switch tok.Class {
		case TokenName, TokenNum, TokenVer:
		default:
			// 画质、语言、发布组等不参与覆盖率
			continue
		}
		if stopWords[tok.Text] {
			continue
		}
		required = append(required, tok)
		if tok.Class == TokenNum || tok.Class == TokenVer {
			anchors = append(anchors, tok)
		}
	}
	return required, anchors
}

// matchRequired 为每个必需词元找到标题中的最佳匹配。
// 返回权重、匹配强度与命中的标题词元下标。
func matchRequired(required []Token, title []Token) (weights, matches []float64, indices []int) {
	weights = make([]float64, len(required))
	matches = make([]float64, len(required))
	indices = make([]int, len(required))

	for i, q := range required {
		weights[i] = weightName
		if q.Class == TokenNum || q.Class == TokenVer {
			weights[i] = weightNum
		}
		indices[i] = -1
		best := 0.0
		for j, t := range title {
			if w := matchWeight(q, t); w > best {
				best = w
				indices[i] = j
			}
		}
		matches[i] = best
	}
	return weights, matches, indices
}

// orderScore 计算词序奖励：查询词在标题中按同样顺序出现得满分，顺序错乱得半分。
func orderScore(indices []int, matches []float64) float64 {
	if len(indices) == 0 {
		return 0
	}
	last := -1
	for i, idx := range indices {
		if matches[i] <= 0 || idx <= last {
			return 0
		}
		last = idx
	}
	return 1
}

// precisionScore 计算精确度：命中的查询词占标题实词的比例。
func precisionScore(required []Token, matches []float64, title []Token) float64 {
	nameCount := 0
	for _, t := range title {
		if t.Class == TokenName {
			nameCount++
		}
	}
	if nameCount == 0 {
		return 0
	}
	hits := 0
	for _, m := range matches {
		if m > 0 {
			hits++
		}
	}
	p := float64(hits) / float64(nameCount)
	if p > 1 {
		p = 1
	}
	return p
}

// enginePrior 由引擎返回位置得到先验，越靠前越可信。
func enginePrior(rank int) float64 {
	if rank < 0 {
		return 0.5
	}
	return 1 / (1 + float64(rank))
}

// matchWeight 计算查询词元与标题词元的匹配强度。
func matchWeight(q, t Token) float64 {
	if q.Text == t.Text {
		return 1
	}
	qCJK, tCJK := isCJKString(q.Text), isCJKString(t.Text)
	if qCJK || tCJK {
		if !qCJK || !tCJK {
			// 跨语种不直接匹配，中文查询与日文标题的关联由引擎排序兜底
			return 0
		}
		if strings.Contains(t.Text, q.Text) || strings.Contains(q.Text, t.Text) {
			return 0.9
		}
		if commonSubstringLen(q.Text, t.Text) >= 2 {
			return 0.9
		}
		return 0
	}
	if isVersionLike(q.Text) && isVersionLike(t.Text) {
		qp, tp := versionParts(q.Text), versionParts(t.Text)
		if equalParts(qp, tp) {
			return 1
		}
		if len(qp) < len(tp) && equalParts(qp, tp[:len(qp)]) {
			// 查询 24.04 可以命中标题 24.04.1
			return 0.9
		}
		return 0
	}
	if isAllDigits(q.Text) && isAllDigits(t.Text) {
		if strings.TrimLeft(q.Text, "0") == strings.TrimLeft(t.Text, "0") {
			return 1
		}
		return 0
	}
	if pluralMatch(q.Text, t.Text) {
		return 0.95
	}
	if q.Class == TokenName && len([]rune(q.Text)) >= 3 && strings.HasPrefix(t.Text, q.Text) {
		return 0.8
	}
	return 0
}

// pluralMatch 判断两个词是否为单复数关系。
func pluralMatch(q, t string) bool {
	shorter, longer := q, t
	if len(longer) < len(shorter) {
		shorter, longer = longer, shorter
	}
	if len(shorter) < 3 || len(longer) != len(shorter)+1 {
		return false
	}
	return strings.HasPrefix(longer, shorter) && strings.HasSuffix(longer, "s")
}

// commonSubstringLen 返回两个字符串最长公共子串的长度。
func commonSubstringLen(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 || len(br) == 0 {
		return 0
	}
	prev := make([]int, len(br)+1)
	best := 0
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		for j := 1; j <= len(br); j++ {
			if ar[i-1] == br[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			}
		}
		prev = cur
	}
	return best
}

// versionParts 把版本号拆成数值段，去掉前导零与 v 前缀。
func versionParts(s string) []string {
	parts := strings.Split(s, ".")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		if i == 0 {
			p = strings.TrimPrefix(p, "v")
		}
		out = append(out, strings.TrimLeft(p, "0"))
	}
	return out
}

// equalParts 比较两段版本号是否相同。
func equalParts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isAllDigits 判断字符串是否只含数字。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// isVersionLikeNum 判断是否形如 24、v2 的纯数字词（可带小数点）。
func isVersionLikeNum(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "v") {
		return isAllDigits(s[1:])
	}
	return isAllDigits(s)
}

// isVersionLike 判断词元是否为版本号。
func isVersionLike(s string) bool {
	if strings.HasPrefix(s, "v") && len(s) > 1 && isAllDigits(s[1:]) {
		return true
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if !isAllDigits(p) {
			return false
		}
	}
	return true
}

// isASCIIAlnum 判断是否为 ASCII 字母或数字。
func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// isCJKRune 判断是否为中日文字符。
func isCJKRune(r rune) bool {
	return IsCJK(r)
}

// isCJKString 判断字符串是否全部由中日文字符组成。
func isCJKString(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !IsCJK(r) {
			return false
		}
	}
	return true
}
