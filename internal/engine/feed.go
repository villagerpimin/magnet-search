package engine

import (
	"strings"
	"time"
)

// parseFeedTime 解析 RSS 通道的 pubDate。
//
// 各站点的写法并不统一：bt4gprx 逗号后没有空格（"Wed,21 Oct 2026 05:27:30 -0000"），
// nyaa 走的是标准 RFC1123Z（"Wed, 21 Oct 2026 07:22:59 -0000"）。
// RFC1123Z 还要求日补零，而有些订阅写成 "Mon, 2 Feb 2026 ..."，
// 蜜柑计划则直接给 RFC3339（"2026-10-17T23:42:00"）。
// 因此这里先去掉逗号后的空格，再依次尝试多种版式。
func parseFeedTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	s = strings.Replace(s, ", ", ",", 1)
	for _, layout := range []string{
		"Mon,2 Jan 2006 15:04:05 -0700",
		time.RFC1123Z,
		time.RFC3339,
		"2006-01-02T15:04:05",
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

// cleanXML 去掉 XML 不允许的控制字符。
// 部分订阅源会在标题或描述里夹带 U+001F 这类控制字符，
// encoding/xml 即使关闭严格模式也会直接报错，导致整份订阅解析失败。
func cleanXML(raw []byte) []byte {
	valid := strings.ToValidUTF8(string(raw), "")
	var b strings.Builder
	b.Grow(len(valid))
	for _, r := range valid {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
			b.WriteRune(r)
		}
	}
	return []byte(b.String())
}
