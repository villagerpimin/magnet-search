package aggregate

import (
	"sort"
	"strings"
)

// TokenClass 是标题词元的语义类别。
type TokenClass int

const (
	// TokenName 普通词名
	TokenName TokenClass = iota
	// TokenNum 纯数字，如集号 03、年份 2024
	TokenNum
	// TokenVer 版本号，如 24.04、v2、1.5
	TokenVer
	// TokenLang 语言或字幕标记
	TokenLang
	// TokenQuality 画质、编码、来源标记
	TokenQuality
	// TokenEdition 发行形态标记
	TokenEdition
	// TokenGroup 发布组名
	TokenGroup
)

// Token 是一个带语义类别的词元。
type Token struct {
	// Text 归一化后的词元文本（小写）
	Text string
	// Class 语义类别
	Class TokenClass
}

// ClassifyTokens 把标题或查询切分并分类。
// 中日文标题额外按字拆出次级词元，便于跨语言检索时按字匹配。
func ClassifyTokens(s string) []Token {
	return classify(s, true)
}

// classify 是内部切分实现。expandCJK 决定是否把 CJK 串按字展开。
func classify(s string, expandCJK bool) []Token {
	var out []Token
	for _, seg := range splitSegments(s) {
		out = append(out, classifySegment(seg, expandCJK)...)
	}
	return out
}

// segment 是标题被切分出的一个片段。
type segment struct {
	// text 片段文本
	text string
	// bracketed 是否为方括号或圆括号包裹的片段
	bracketed bool
}

// splitSegments 把标题切成"括号片段"与"自由文本片段"。
func splitSegments(s string) []segment {
	var out []segment
	var buf strings.Builder
	runes := []rune(s)

	flush := func() {
		if buf.Len() > 0 {
			out = append(out, segment{text: buf.String()})
			buf.Reset()
		}
	}

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '[' || c == '(' {
			closing := ']'
			if c == '(' {
				closing = ')'
			}
			if end := findRune(runes, closing, i+1); end > 0 {
				flush()
				out = append(out, segment{text: string(runes[i+1 : end]), bracketed: true})
				i = end
				continue
			}
		}
		buf.WriteRune(c)
	}
	flush()
	return out
}

// findRune 查找目标字符的下一个出现位置，找不到返回 -1。
func findRune(runes []rune, target rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// classifySegment 对单个片段做分类。
// 括号片段若含画质/语言/版本形态词则逐词分类，否则整体视为发布组名。
func classifySegment(seg segment, expandCJK bool) []Token {
	text := strings.TrimSpace(seg.text)
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)

	if seg.bracketed {
		if !hasSemanticToken(lower) {
			return []Token{{Text: lower, Class: TokenGroup}}
		}
		var out []Token
		for _, w := range splitWords(lower) {
			out = append(out, classifyWord(w, expandCJK)...)
		}
		return out
	}

	// 自由文本：先摘出结尾的 -GROUP 发布组
	body, group := extractGroupSuffix(lower)
	var out []Token
	if group != "" {
		out = append(out, Token{Text: group, Class: TokenGroup})
	}
	for _, w := range splitWords(body) {
		out = append(out, classifyWord(w, expandCJK)...)
	}
	return out
}

// hasSemanticToken 判断片段里是否出现画质/语言/版本形态词。
func hasSemanticToken(lower string) bool {
	for _, w := range splitWords(lower) {
		if _, ok := langTokens[w]; ok {
			return true
		}
		if langMarkers[w] || qualityTokens[w] || editionTokens[w] {
			return true
		}
		if isVersionLike(w) {
			return true
		}
	}
	return false
}

// extractGroupSuffix 摘出自由文本结尾的 -发布组 形式。
func extractGroupSuffix(lower string) (body, group string) {
	idx := strings.LastIndex(lower, "-")
	if idx <= 0 || idx == len(lower)-1 {
		return lower, ""
	}
	tail := lower[idx+1:]
	if strings.ContainsAny(tail, " .") {
		return lower, ""
	}
	if isAllDigits(tail) || qualityTokens[tail] || editionTokens[tail] {
		return lower, ""
	}
	if _, ok := langTokens[tail]; ok {
		return lower, ""
	}
	return lower[:idx], tail
}

// splitWords 把片段切成词：连续的 ASCII 字母数字算一个词，
// 连续的 CJK 字符算一个词；形如 24.04 的版本号会保留小数点。
func splitWords(s string) []string {
	runes := []rune(s)
	var out []string

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case isASCIIAlnum(c):
			start := i
			for i < len(runes) && isASCIIAlnum(runes[i]) {
				i++
			}
			word := string(runes[start:i])
			i--
			// 版本号合并：把 24.04 这类被点号隔开的数字并回同一个词元。
			// 只吸收 1-2 位数字的小数段，避免把 2020.1080p 误并成版本号。
			if isVersionLikeNum(word) {
				for i+2 < len(runes) && runes[i+1] == '.' && isDigit(runes[i+2]) {
					j := i + 2
					for j < len(runes) && isDigit(runes[j]) {
						j++
					}
					if j-(i+2) > 2 {
						break
					}
					word += string(runes[i+1 : j])
					i = j - 1
				}
			}
			out = append(out, word)
		case isCJKRune(c):
			j := i
			for j < len(runes) && isCJKRune(runes[j]) {
				j++
			}
			out = append(out, string(runes[i:j]))
			i = j - 1
		}
	}
	return out
}

// isDigit 判断字符是否为数字。
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// classifyWord 判定单个词的语义类别。
func classifyWord(w string, expandCJK bool) []Token {
	if w == "" {
		return nil
	}
	if _, ok := langTokens[w]; ok {
		return []Token{{Text: w, Class: TokenLang}}
	}
	if langMarkers[w] {
		return []Token{{Text: w, Class: TokenLang}}
	}
	if qualityTokens[w] {
		return []Token{{Text: w, Class: TokenQuality}}
	}
	if editionTokens[w] {
		return []Token{{Text: w, Class: TokenEdition}}
	}
	if isAllDigits(w) {
		return []Token{{Text: w, Class: TokenNum}}
	}
	if isVersionLike(w) {
		return []Token{{Text: w, Class: TokenVer}}
	}
	if isCJKString(w) {
		out := []Token{{Text: w, Class: TokenName}}
		if expandCJK {
			for _, r := range w {
				out = append(out, Token{Text: string(r), Class: TokenName})
			}
		}
		return out
	}
	return []Token{{Text: w, Class: TokenName}}
}

// ResolutionTier 返回标题的画质档位：uhd / 1080p / 720p / sd / any。
func ResolutionTier(title string) string {
	for _, tok := range classify(title, false) {
		if tok.Class != TokenQuality {
			continue
		}
		if tier, ok := resolutionTiers[tok.Text]; ok {
			return tier
		}
	}
	return "any"
}

// LangKey 返回标题的语言标识。
// 没有语言标记返回 any；出现多种语言返回 multi。
func LangKey(title string) string {
	seen := map[string]bool{}
	for _, tok := range classify(title, false) {
		if tok.Class != TokenLang {
			continue
		}
		if code, ok := langTokens[tok.Text]; ok {
			seen[code] = true
		}
	}
	if len(seen) == 0 {
		return "any"
	}
	if len(seen) == 1 {
		for code := range seen {
			return code
		}
	}
	return "multi"
}

// ContentKey 返回去掉画质、语言、发布组、版本形态词元后的内容词元集合。
func ContentKey(title string) string {
	seen := map[string]bool{}
	for _, tok := range classify(title, false) {
		switch tok.Class {
		case TokenName, TokenNum, TokenVer:
		default:
			continue
		}
		if stopWords[tok.Text] {
			continue
		}
		seen[tok.Text] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ".")
}

// ClusterKey 返回聚类键，格式为 "画质档位|语言|内容词元"。
// 画质档位进入键，使 1080p 与 720p 分属不同簇；
// 发布组名不进入键，使不同发布组的同一资源聚在一起。
func ClusterKey(title string) string {
	return ResolutionTier(title) + "|" + LangKey(title) + "|" + ContentKey(title)
}
