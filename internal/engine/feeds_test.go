package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// buildFeedTorrent 拼一个单文件种子，返回种子字节与 info hash。
func buildFeedTorrent(t *testing.T, name string, length int64) ([]byte, string) {
	t.Helper()
	info := bencode.NewDict(map[string]bencode.Value{
		"name":         bencode.NewString(name),
		"length":       bencode.NewInt(length),
		"piece length": bencode.NewInt(16384),
		"pieces":       bencode.NewBytes(make([]byte, 20)),
	})
	rawInfo, err := bencode.Encode(info)
	if err != nil {
		t.Fatalf("编码 info 失败: %v", err)
	}
	data, err := bencode.Encode(bencode.NewDict(map[string]bencode.Value{
		"announce": bencode.NewString("http://tracker.example/announce"),
		"info":     info,
	}))
	if err != nil {
		t.Fatalf("编码种子失败: %v", err)
	}
	sum := sha1.Sum(rawInfo)
	return data, hex.EncodeToString(sum[:])
}

// TestDmhyParse 校验动漫花园 RSS 的磁力与标题解析。
func TestDmhyParse(t *testing.T) {
	sample := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0"><channel>
<item>
<title><![CDATA[【喵萌奶茶屋】葬送的芙莉莲 01]]></title>
<link>http://share.dmhy.org/topics/view/1.html</link>
<pubDate>Sat, 17 Oct 2026 23:42:42 +0800</pubDate>
<description><![CDATA[<p>说明</p>]]></description>
<enclosure url="magnet:?xt=urn:btih:4UBTDIFIJGOZL34OXVKGCE6NAIJHLSDX&amp;dn=x" length="1" type="application/x-bittorrent"></enclosure>
<category domain="http://share.dmhy.org/topics/list/sort_id/31"><![CDATA[季度全集]]></category>
</item>
<item><title>没有磁力的条目</title><link>http://example.com/2</link></item>
</channel></rss>`

	results, err := NewDmhy(nil).parse([]byte(sample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.Title != "【喵萌奶茶屋】葬送的芙莉莲 01" {
		t.Errorf("标题 = %q", got.Title)
	}
	if got.InfoHash != "e50331a0a8499d95ef8ebd546113cd021275c877" {
		t.Errorf("info hash = %q", got.InfoHash)
	}
	if got.Category != "季度全集" {
		t.Errorf("分类 = %q", got.Category)
	}
	if got.Source != "dmhy" || got.Rank != 0 {
		t.Errorf("来源/位次 = %q/%d", got.Source, got.Rank)
	}
	if !got.PublishDate.Equal(time.Date(2026, 10, 17, 15, 42, 42, 0, time.UTC)) {
		t.Errorf("发布时间 = %s", got.PublishDate)
	}
	if got.Seeders != model.Unknown || got.Leechers != model.Unknown {
		t.Errorf("做种/下载 = %d/%d，期望未知", got.Seeders, got.Leechers)
	}
	if got.MagnetURI == "" {
		t.Error("磁力链接不能为空")
	}
}

// TestDmhySearchURL 校验查询地址。
func TestDmhySearchURL(t *testing.T) {
	got := NewDmhy(nil).searchURL(Options{Query: "葬送 芙莉莲"})
	want := "https://share.dmhy.org/topics/rss/rss.xml?keyword=%E8%91%AC%E9%80%81+%E8%8A%99%E8%8E%89%E8%8E%B2"
	if got != want {
		t.Errorf("查询地址 = %q，期望 %q", got, want)
	}
}

// TestMikanSearch 校验蜜柑计划 RSS 与种子文件解析。
func TestMikanSearch(t *testing.T) {
	torrent, hash := buildFeedTorrent(t, "frieren-01", 4096)
	rss := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0"><channel><item>
<title>【喵萌奶茶屋】葬送的芙莉莲 01</title>
<link>https://mikanani.me/Home/Episode/abc</link>
<torrent xmlns="https://mikanani.me/0.1/">
  <link>https://mikanani.me/Home/Episode/abc</link>
  <contentLength>12345</contentLength>
  <pubDate>2026-10-17T23:42:00</pubDate>
</torrent>
<enclosure type="application/x-bittorrent" length="12345" url="https://mikanani.me/Download/20261017/abc.torrent" />
</item>
<item><title>没有种子的条目</title></item>
</channel></rss>`

	client := routeClient{bodies: map[string][]byte{
		"https://mikanani.me/RSS/Search?searchstr=%E8%91%AC%E9%80%81": []byte(rss),
		"https://mikanani.me/Download/20261017/abc.torrent":           torrent,
	}}
	results, err := NewMikan(client).Search(context.Background(), Options{Query: "葬送", Limit: 10})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.InfoHash != hash {
		t.Errorf("info hash = %q，期望 %q", got.InfoHash, hash)
	}
	if got.SizeBytes != 12345 {
		t.Errorf("大小应优先用 RSS 的 contentLength，实际 %d", got.SizeBytes)
	}
	if got.Source != "mikan" || got.Category != "anime" {
		t.Errorf("来源/分类 = %q/%q", got.Source, got.Category)
	}
	if !got.PublishDate.Equal(time.Date(2026, 10, 17, 23, 42, 0, 0, time.UTC)) {
		t.Errorf("发布时间 = %s", got.PublishDate)
	}
	if got.URL != "https://mikanani.me/Home/Episode/abc" {
		t.Errorf("详情页 = %q", got.URL)
	}
	if got.MagnetURI != magnet.Build(hash, got.Title, []string{"http://tracker.example/announce"}) {
		t.Errorf("磁力链接 = %q", got.MagnetURI)
	}
}

// TestAcgRipSearch 校验 ACG.RIP RSS 与种子文件解析。
func TestAcgRipSearch(t *testing.T) {
	torrent, hash := buildFeedTorrent(t, "frieren-bd", 8192)
	rss := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torrent="http://xmlns.ezrss.it/0.1/" xmlns:media="http://search.yahoo.com/mrss/">
<channel><item>
<title>【喵萌奶茶屋】葬送的芙莉莲 合集</title>
<description>搬运</description>
<pubDate>Sat, 17 Oct 2026 08:42:45 -0700</pubDate>
<link>https://acg.rip/t/365461</link>
<guid>https://acg.rip/t/365461</guid>
<enclosure url="https://acg.rip/t/365461.torrent" type="application/x-bittorrent"/>
<torrent:contentLength>8198506496</torrent:contentLength>
<media:content url="https://acg.rip/t/365461.torrent" fileSize="8198506496"/>
</item></channel></rss>`

	client := routeClient{bodies: map[string][]byte{
		"https://acg.rip/.xml?term=%E8%91%AC%E9%80%81": []byte(rss),
		"https://acg.rip/t/365461.torrent":             torrent,
	}}
	results, err := NewAcgRip(client).Search(context.Background(), Options{Query: "葬送", Limit: 10})
	if err != nil {
		t.Fatalf("检索失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.InfoHash != hash {
		t.Errorf("info hash = %q，期望 %q", got.InfoHash, hash)
	}
	if got.SizeBytes != 8198506496 {
		t.Errorf("大小 = %d，期望 8198506496", got.SizeBytes)
	}
	if got.Source != "acgrip" || got.Category != "anime" {
		t.Errorf("来源/分类 = %q/%q", got.Source, got.Category)
	}
	if !got.PublishDate.Equal(time.Date(2026, 10, 17, 15, 42, 45, 0, time.UTC)) {
		t.Errorf("发布时间 = %s", got.PublishDate)
	}
}

// TestTorrentsCsvParse 校验 JSON 接口的字段映射。
func TestTorrentsCsvParse(t *testing.T) {
	sample := `{"torrents":[
		{"infohash":"232CD67EB3FFBD7C37BF9EC3EE887417E5AE1EE6","name":"Ubuntu Linux Bible","size_bytes":8554738,"created_unix":1784214068,"seeders":27,"leechers":0},
		{"infohash":"0000000000000000000000000000000000000000","name":"空哈希","size_bytes":1},
		{"infohash":"bad","name":"坏哈希","size_bytes":1}
	]}`
	results, err := NewTorrentsCsv(nil).parse([]byte(sample))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条有效结果，实际 %d 条", len(results))
	}
	got := results[0]
	if got.InfoHash != "232cd67eb3ffbd7c37bf9ec3ee887417e5ae1ee6" {
		t.Errorf("info hash = %q", got.InfoHash)
	}
	if got.Title != "Ubuntu Linux Bible" || got.SizeBytes != 8554738 {
		t.Errorf("标题/大小 = %q/%d", got.Title, got.SizeBytes)
	}
	if got.Seeders != 27 || got.Leechers != 0 {
		t.Errorf("做种/下载 = %d/%d", got.Seeders, got.Leechers)
	}
	if got.Source != "torrentscsv" || got.Rank != 0 {
		t.Errorf("来源/位次 = %q/%d", got.Source, got.Rank)
	}
}

// TestTorrentsCsvSearchURL 校验条数上限与关键词转义。
func TestTorrentsCsvSearchURL(t *testing.T) {
	a := NewTorrentsCsv(nil)
	cases := []struct {
		limit int
		want  string
	}{
		{0, "https://torrents-csv.com/service/search?q=hello+world&size=50"},
		{20, "https://torrents-csv.com/service/search?q=hello+world&size=20"},
		{500, "https://torrents-csv.com/service/search?q=hello+world&size=100"},
	}
	for _, c := range cases {
		if got := a.searchURL(Options{Query: "hello world", Limit: c.limit}); got != c.want {
			t.Errorf("limit=%d 查询地址 = %q，期望 %q", c.limit, got, c.want)
		}
	}
}

// TestNewEnginesRegistered 校验新增引擎都进了注册表。
func TestNewEnginesRegistered(t *testing.T) {
	reg := NewRegistry()
	RegisterAll(reg)
	names := map[string]bool{}
	for _, spec := range reg.Specs() {
		names[spec.Name] = true
	}
	for _, want := range []string{"dmhy", "mikan", "acgrip", "torrentscsv"} {
		if !names[want] {
			t.Errorf("注册表缺少引擎 %s", want)
		}
	}
}
