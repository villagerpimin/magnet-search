package engine

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/model"
)

// Mikan 适配蜜柑计划（mikanani.me）的 RSS 搜索。
// RSS 只给 .torrent 地址，需要下载种子文件后才能得到 info hash 与磁力链接。
type Mikan struct {
	Client  ClientProvider
	BaseURL string
}

// NewMikan 构造蜜柑计划适配器。
func NewMikan(c ClientProvider) *Mikan {
	return &Mikan{Client: c, BaseURL: "https://mikanani.me"}
}

// Name 返回引擎标识。
func (a *Mikan) Name() string { return "mikan" }

// Description 返回引擎描述。
func (a *Mikan) Description() string { return "蜜柑计划 RSS 磁力搜索（中文动漫资源）" }

// Search 执行搜索。
func (a *Mikan) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := fetchWithRetry(ctx, a.Client, a.searchURL(opts))
	if err != nil {
		return nil, fmt.Errorf("mikan 请求失败: %w", err)
	}
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	items, err := mikanItems(raw)
	if err != nil {
		return nil, err
	}
	feed := make([]torrentFeedItem, 0, len(items))
	for _, item := range items {
		if item.TorrentURL == "" {
			continue
		}
		feed = append(feed, torrentFeedItem{
			Title:       item.Title,
			Link:        item.Link,
			TorrentURL:  item.TorrentURL,
			SizeBytes:   item.SizeBytes,
			PublishDate: item.PublishDate,
		})
		if len(feed) >= maxTorrentFeedItems {
			break
		}
	}
	return resolveTorrentFeed(ctx, a.Client, feed, a.Name(), "anime", opts.Limit), nil
}

// RegisterMikan 把 mikan 注册到引擎表。
func RegisterMikan(reg *Registry) {
	reg.Add(Spec{
		Name:        "mikan",
		Description: "蜜柑计划 RSS 磁力搜索（中文动漫资源）",
		Region:      RegionDomestic,
		New:         func(c ClientProvider) Engine { return NewMikan(c) },
	})
}

// searchURL 拼装 RSS 查询地址。
func (a *Mikan) searchURL(opts Options) string {
	return fmt.Sprintf("%s/RSS/Search?searchstr=%s",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
	)
}

// mikanRSSItem 是蜜柑计划 RSS 里的一条记录。
type mikanRSSItem struct {
	Title       string
	Link        string
	TorrentURL  string
	SizeBytes   int64
	PublishDate time.Time
	// pubRaw 是解析过程中的原始时间文本
	pubRaw string
}

// mikanItems 逐个 XML token 取出 RSS 里的 item。
func mikanItems(body []byte) ([]mikanRSSItem, error) {
	dec := xml.NewDecoder(bytes.NewReader(cleanXML(body)))
	dec.Strict = false
	var (
		items  []mikanRSSItem
		cur    mikanRSSItem
		elem   string
		inItem bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mikan RSS 解析失败: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "item":
				cur = mikanRSSItem{}
				inItem = true
			case inItem:
				elem = t.Name.Local
				if t.Name.Local == "enclosure" {
					uri := strings.TrimSpace(attrOf(t, "url"))
					if strings.HasSuffix(strings.ToLower(uri), ".torrent") {
						cur.TorrentURL = uri
					}
				}
			}
		case xml.CharData:
			if !inItem || elem == "" {
				continue
			}
			value := strings.TrimSpace(string(t))
			switch elem {
			case "title":
				cur.Title += string(t)
			case "link":
				cur.Link = value
			case "pubDate":
				cur.pubRaw = value
			case "contentLength":
				if n, err := strconv.ParseInt(value, 10, 64); err == nil {
					cur.SizeBytes = n
				}
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == "item":
				items = append(items, cur)
				inItem = false
				elem = ""
			case inItem:
				elem = ""
			}
		}
	}
	for i := range items {
		if ts, ok := parseFeedTime(items[i].pubRaw); ok {
			items[i].PublishDate = ts
		}
	}
	return items, nil
}
