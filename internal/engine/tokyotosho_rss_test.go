package engine

import (
	"testing"
	"time"
)

// tokyoRSSSample 是站点 RSS 通道的真实结构样本。
const tokyoRSSSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
<atom:link href="https://www.tokyotosho.info/rss.php?terms=ubuntu" rel="self" type="application/rss+xml" />
<title>ubuntu - Tokyo Toshokan</title><link>https://www.tokyotosho.info</link>
<description>Torrent Listing</description><language>en</language>
<lastBuildDate>Sat, 25 Mar 2023 02:38:59 GMT</lastBuildDate>
<ttl>15</ttl><item>
<category>Other</category>
<title>ubuntu-13.04-server-amd64.iso</title>
<link><![CDATA[http://releases.ubuntu.com/13.04/ubuntu-13.04-server-amd64.iso.torrent]]></link>
<description><![CDATA[<a href="http://releases.ubuntu.com/13.04/ubuntu-13.04-server-amd64.iso.torrent">Torrent Link</a><br />
<a href="magnet:?xt=urn:btih:4UBTDIFIJGOZL34OXVKGCE6NAIJHLSDX&tr=http://torrent.ubuntu.com:6969/announce">Magnet Link</a><br />
<a href="https://www.tokyotosho.info/details.php?id=647647">Tokyo Tosho</a><br />
Size: 1.24TB<br />
Authorized: N/A<br />
Submitter: Anonymous<br />
]]></description>
<guid><![CDATA[https://www.tokyotosho.info/details.php?id=647647]]></guid>
<pubDate>Thu, 25 Apr 2013 11:34:02 GMT</pubDate>
</item>
<item>
<category>Music</category>
<title>[Audio-4U] J-CORE 13 (flac) &amp; more</title>
<link><![CDATA[https://nyaa.si/download/1652548.torrent]]></link>
<description><![CDATA[<a href="https://nyaa.si/download/1652548.torrent">Torrent Link</a><br />
<a href="magnet:?xt=urn:btih:AXXNJJELKLSA7VEKOKODPF6H2JNNL7SV&tr=http://nyaa.tracker.wf:7777/announce">Magnet Link</a><br />
<a href="https://www.tokyotosho.info/details.php?id=1753250">Tokyo Tosho</a><br />
Size: 1.24TB<br />
Authorized: N/A<br />
Submitter: Anonymous<br />
]]></description>
<guid><![CDATA[https://www.tokyotosho.info/details.php?id=1753250]]></guid>
<pubDate>Sat, 25 Mar 2023 02:38:59 GMT</pubDate>
</item>
<item>
<category>Raws</category>
<title>没有磁力的条目</title>
<description><![CDATA[<a href="https://example.com/x.torrent">Torrent Link</a><br />Size: 1MB<br />]]></description>
<pubDate>Sat, 25 Mar 2023 02:38:59 GMT</pubDate>
</item>
</channel></rss>`

// TestTokyotoshoParseRSS 校验 RSS 通道的解析结果。
func TestTokyotoshoParseRSS(t *testing.T) {
	results, err := NewTokyotosho(nil).parseRSS([]byte(tokyoRSSSample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("期望 2 条结果，实际 %d 条", len(results))
	}
	cases := []struct {
		name     string
		idx      int
		title    string
		hash     string
		size     int64
		category string
		date     time.Time
		url      string
	}{
		{
			name:     "base32 磁力要转成十六进制",
			idx:      0,
			title:    "ubuntu-13.04-server-amd64.iso",
			hash:     "e50331a0a8499d95ef8ebd546113cd021275c877",
			size:     1363394418442,
			category: "other",
			date:     time.Date(2013, 4, 25, 11, 34, 2, 0, time.UTC),
			url:      "https://www.tokyotosho.info/details.php?id=647647",
		},
		{
			name:     "标题里的 XML 实体要还原",
			idx:      1,
			title:    "[Audio-4U] J-CORE 13 (flac) & more",
			hash:     "05eed4a48b52e40fd48a729c3797c7d25ad5fe55",
			size:     1363394418442,
			category: "music",
			date:     time.Date(2023, 3, 25, 2, 38, 59, 0, time.UTC),
			url:      "https://www.tokyotosho.info/details.php?id=1753250",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := results[tc.idx]
			if got.Title != tc.title {
				t.Errorf("标题 = %q，期望 %q", got.Title, tc.title)
			}
			if got.InfoHash != tc.hash {
				t.Errorf("info hash = %q，期望 %q", got.InfoHash, tc.hash)
			}
			if got.SizeBytes != tc.size {
				t.Errorf("大小 = %d，期望 %d", got.SizeBytes, tc.size)
			}
			if got.Category != tc.category {
				t.Errorf("分类 = %q，期望 %q", got.Category, tc.category)
			}
			if !got.PublishDate.Equal(tc.date) {
				t.Errorf("发布时间 = %s，期望 %s", got.PublishDate, tc.date)
			}
			if got.URL != tc.url {
				t.Errorf("详情页 = %q，期望 %q", got.URL, tc.url)
			}
			if got.Seeders != -1 || got.Leechers != -1 {
				t.Errorf("RSS 没有人数信息，应保持 Unknown，实际 %d/%d", got.Seeders, got.Leechers)
			}
			if got.Source != "tokyotosho" {
				t.Errorf("来源 = %q，期望 tokyotosho", got.Source)
			}
		})
	}
}

// TestTokyotoshoRSSURL 校验 RSS 通道的查询地址。
func TestTokyotoshoRSSURL(t *testing.T) {
	tokyo := NewTokyotosho(nil)
	got := tokyo.rssURL(Options{Query: "ubuntu linux"})
	want := "https://www.tokyotosho.info/rss.php?terms=ubuntu+linux"
	if got != want {
		t.Errorf("查询地址 = %q，期望 %q", got, want)
	}
}
