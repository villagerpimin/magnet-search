package aggregate

import "strings"

// normalizeQuerySeparators 把查询里的连字符视为普通分隔符。
// 标题解析会把结尾的 -word 当成发布组，但查询里的 - 通常只是连接词。
func normalizeQuerySeparators(query string) string {
	return strings.ReplaceAll(query, "-", " ")
}

// QueryTerms 提取用于引擎查询的独立关键词（只保留名称、数字、版本号，去掉停用词/画质/语言等）。
// 关键词按首次出现的顺序返回，并按大小写不敏感去重；max > 0 时最多返回 max 个，
// max <= 0 表示不限制。CJK 连续文本会作为一个整体关键词保留，不按字拆开。
func QueryTerms(query string, max int) []string {
	query = normalizeQuerySeparators(query)
	seen := make(map[string]bool)
	var terms []string
	for _, tok := range classify(query, false) {
		switch tok.Class {
		case TokenName, TokenNum, TokenVer:
		default:
			// 画质、语言、发布组等不适合作为独立引擎查询
			continue
		}
		if stopWords[tok.Text] {
			continue
		}
		key := strings.ToLower(tok.Text)
		if seen[key] {
			continue
		}
		seen[key] = true
		terms = append(terms, tok.Text)
		if max > 0 && len(terms) >= max {
			break
		}
	}
	return terms
}

// QueryVariants 返回引擎查询变体：原始查询 + 各独立关键词。
// 查询为空时返回 nil；独立关键词少于两个时只返回原始查询；
// 与整条查询完全相同的关键词会被跳过，避免重复检索。
func QueryVariants(query string, max int) []string {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	terms := QueryTerms(q, max)
	if len(terms) < 2 {
		return []string{q}
	}
	variants := []string{q}
	for _, term := range terms {
		if strings.EqualFold(term, q) {
			continue
		}
		variants = append(variants, term)
	}
	return variants
}
