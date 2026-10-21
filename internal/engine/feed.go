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
// 因此这里先去掉逗号后的空格，再用不补零的版式解析，两种写法都能吃下。
func parseFeedTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	s = strings.Replace(s, ", ", ",", 1)
	for _, layout := range []string{
		"Mon,2 Jan 2006 15:04:05 -0700",
		time.RFC1123Z,
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}
