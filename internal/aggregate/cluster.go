package aggregate

import (
	"regexp"
	"strings"
	"unicode"
)

// 归一化标题时需要剥离的噪声词。
// 这些词不携带资源身份信息，进入聚类键只会让不同版本被错误地合并。
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true,
	"of": true, "in": true, "to": true, "for": true, "with": true,
	"from": true, "by": true, "on": true, "at": true, "is": true,
	"www": true, "com": true, "net": true, "org": true, "co": true,
	"www2": true, "full": true, "final": true, "new": true,
}

// 描述画质与来源的关键词，必须保留在聚类键中：
// 1080p 与 720p 是不同文件，x264 与 x265 也是不同编码。
var qualityWords = map[string]bool{
	"2160p": true, "1080p": true, "1080i": true, "720p": true, "480p": true, "360p": true,
	"4k": true, "8k": true, "uhd": true, "hd": true, "sd": true,
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true, "avc": true,
	"xvid": true, "divx": true, "av1": true, "vp9": true,
	"bluray": true, "blu": true, "bdrip": true, "brrip": true, "remux": true,
	"webrip": true, "web": true, "webdl": true, "hdtv": true, "pdtv": true,
	"dvdrip": true, "dvd": true, "hddvd": true, "cam": true, "ts": true, "tc": true, "r5": true,
	"proper": true, "repack": true, "remastered": true, "extended": true, "unrated": true,
	"internal": true, "limited": true, "imax": true, "hdr": true, "dv": true, "sdr": true,
	"atmos": true, "truehd": true, "dts": true, "ddp": true, "dd": true, "aac": true,
	"flac": true, "opus": true, "mp3": true, "m4a": true,
	"multi": true, "dual": true, "subbed": true, "dubbed": true, "vostfr": true, "vf": true,
	"eng": true, "jpn": true, "kor": true, "chs": true, "cht": true, "gb": true,
	"简": true, "繁": true, "中": true, "英": true, "日": true,
}

// 分隔符：把标题切成词元。
var separatorRe = regexp.MustCompile(`[\s._\-–—+·:;,()\[\]{}<>!?"'|/\\]+`)

// Normalize 把标题归一化为小写、去符号的字符串。
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = separatorRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// Tokenize 把标题切分为词元列表。
func Tokenize(s string) []string {
	norm := Normalize(s)
	if norm == "" {
		return nil
	}
	return strings.Fields(norm)
}

// bracketContentRe 提取标题中的方括号与圆括号片段。
var bracketContentRe = regexp.MustCompile(`[\[\(]([^\[\]\(\)]+)[\]\)]`)

// Similarity 计算两个标题的相似度，返回 [0,1]。
// 使用加权 Jaccard：出现在两个标题中的词元贡献交集权重，
// 画质词权重更高（1080p 与 720p 不应被判定为同一资源）。
func Similarity(a, b string) float64 {
	ca, cb := significantTokens(a), significantTokens(b)
	if len(ca) == 0 || len(cb) == 0 {
		return 0
	}

	inter := 0.0
	union := 0.0
	for tok, w := range ca {
		union += w
		if _, ok := cb[tok]; ok {
			inter += w
		}
	}
	for tok, w := range cb {
		if _, ok := ca[tok]; !ok {
			union += w
		}
	}
	if union == 0 {
		return 0
	}
	return inter / union
}

// significantTokens 返回参与相似度计算的词元及其权重。
func significantTokens(title string) map[string]float64 {
	out := map[string]float64{}
	for _, tok := range Tokenize(title) {
		if stopWords[tok] {
			continue
		}
		w := 1.0
		if qualityWords[tok] {
			w = 2.0
		}
		out[tok] += w
	}
	return out
}

// Relevance 计算标题对查询的相关度，返回 [0,1]。
// 规则：逐个查询词匹配标题词元——完全相等记 1 分，前缀匹配记 0.8 分，
// 子串匹配记 0.5 分；查询整串出现在标题中额外加分。
func Relevance(query, title string) float64 {
	qTerms := Tokenize(query)
	if len(qTerms) == 0 {
		return 0
	}
	titleTokens := Tokenize(title)
	titleSet := make(map[string]bool, len(titleTokens))
	for _, t := range titleTokens {
		titleSet[t] = true
	}
	normTitle := Normalize(title)

	total := 0.0
	for _, q := range qTerms {
		switch {
		case titleSet[q]:
			total += 1
		case hasPrefixToken(titleTokens, q):
			total += 0.8
		case len(q) >= 3 && strings.Contains(normTitle, q):
			total += 0.5
		}
	}
	score := total / float64(len(qTerms))

	// 整串命中说明标题整体就是该关键词，给出额外权重
	if nq := Normalize(query); nq != "" && strings.Contains(normTitle, nq) && score < 1 {
		score += 0.15
	}
	if score > 1 {
		score = 1
	}
	return score
}

// hasPrefixToken 判断标题词元中是否存在以 q 为前缀的词。
func hasPrefixToken(tokens []string, q string) bool {
	if len(q) < 3 {
		return false
	}
	for _, t := range tokens {
		if strings.HasPrefix(t, q) {
			return true
		}
	}
	return false
}

// IsCJK 判断字符是否属于中日韩文字。
func IsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}
