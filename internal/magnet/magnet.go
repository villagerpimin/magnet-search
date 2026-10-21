// Package magnet 负责磁力链接与 info hash 的解析、归一化与构造。
package magnet

import (
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// InfoHashFromMagnet 从磁力链接中提取 40 位十六进制的 info hash。
// 支持 urn:btih: 后接 40 位 hex 或 32 位 base32 两种形式。
func InfoHashFromMagnet(uri string) (string, bool) {
	lower := strings.ToLower(uri)
	idx := strings.Index(lower, "urn:btih:")
	if idx < 0 {
		return "", false
	}
	rest := uri[idx+len("urn:btih:"):]
	end := strings.IndexAny(rest, "&#?")
	if end >= 0 {
		rest = rest[:end]
	}
	return NormalizeInfoHash(rest)
}

// NormalizeInfoHash 把 hex40 或 base32 形式的哈希统一为小写 hex40。
// base32 必须按大写解码：Go 的 base32 解码器只接受标准大写字母表，
// 先小写化再解码会让 base32 分支恒定失败。
func NormalizeInfoHash(s string) (string, bool) {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 40:
		lower := strings.ToLower(s)
		if _, err := hex.DecodeString(lower); err != nil {
			return "", false
		}
		return lower, true
	case 32:
		raw, err := b32.DecodeString(strings.ToUpper(s))
		if err != nil || len(raw) != 20 {
			return "", false
		}
		return hex.EncodeToString(raw), true
	default:
		return "", false
	}
}

// Trackers 提取磁力链接中的 tr 参数（去重、保持顺序）。
func Trackers(uri string) []string {
	u, err := url.Parse(uri)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, tr := range u.Query()["tr"] {
		tr = strings.TrimSpace(tr)
		if tr == "" || seen[tr] {
			continue
		}
		seen[tr] = true
		out = append(out, tr)
	}
	return out
}

// Build 按 BitTorrent 规范构造磁力链接，参数按字典序排列以保证可复现。
func Build(infoHash, title string, trackers []string) string {
	xt := "urn:btih:" + strings.ToLower(infoHash)
	var b strings.Builder
	b.WriteString("magnet:?xt=")
	b.WriteString(xt)
	if title != "" {
		b.WriteString("&dn=")
		b.WriteString(url.QueryEscape(title))
	}
	for _, tr := range trackers {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

// IsValid 判断字符串是否是一个形态合法的 info hash。
func IsValid(s string) bool {
	_, ok := NormalizeInfoHash(s)
	return ok
}

// ParseSize 解析人类可读的大小字符串，返回字节数；无法解析时返回 0。
func ParseSize(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "TB"):
		multiplier, s = 1<<40, strings.TrimSuffix(s, "TB")
	case strings.HasSuffix(s, "GB"):
		multiplier, s = 1<<30, strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		multiplier, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KIB"):
		multiplier, s = 1<<10, strings.TrimSuffix(s, "KIB")
	case strings.HasSuffix(s, "KB"):
		multiplier, s = 1<<10, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		multiplier, s = 1, strings.TrimSuffix(s, "B")
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0
	}
	// 形如 "1.5 GB"
	if dot := strings.Index(s, "."); dot >= 0 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return int64(f * float64(multiplier))
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n * multiplier
}
