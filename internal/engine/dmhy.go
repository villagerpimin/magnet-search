package engine

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Dmhy 适配动漫花园（share.dmhy.org）的 RSS 通道。
// RSS 的 enclosure 直接给磁力链接，不需要额外请求。
type Dmhy struct {
	Client  ClientProvider
	BaseURL string
}

// NewDmhy 构造动漫花园适配器。
func NewDmhy(c ClientProvider) *Dmhy {
	return &Dmhy{Client: c, BaseURL: "https://share.dmhy.org"}
}

// Name 返回引擎标识。
func (a *Dmhy) Name() string { return "dmhy" }

// Description 返回引擎描述。
func (a *Dmhy) Description() string { return "动漫花园 RSS 磁力搜索（中文动漫资源）" }

// Search 执行搜索。
func (a *Dmhy) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := fetchWithRetry(ctx, a.Client, a.searchURL(opts))
	if err != nil {
		return nil, fmt.Errorf("dmhy 请求失败: %w", err)
	}
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	results, err := a.parse(raw)
	if err != nil {
		return nil, err
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterDmhy 把 dmhy 注册到引擎表。
func RegisterDmhy(reg *Registry) {
	reg.Add(Spec{
		Name:        "dmhy",
		Description: "动漫花园 RSS 磁力搜索（中文动漫资源）",
		Region:      RegionDomestic,
		New:         func(c ClientProvider) Engine { return NewDmhy(c) },
	})
}

// searchURL 拼装 RSS 查询地址。
func (a *Dmhy) searchURL(opts Options) string {
	return fmt.Sprintf("%s/topics/rss/rss.xml?keyword=%s",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
	)
}

// dmhyItem 是 RSS 里的一条记录。
type dmhyItem struct {
	Title    string
	Link     string
	PubDate  string
	Magnet   string
	Category string
}

// parse 解析 RSS 并归一化为结果列表。
func (a *Dmhy) parse(body []byte) ([]model.Result, error) {
	items, err := dmhyItems(body)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	results := make([]model.Result, 0, len(items))
	for _, item := range items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}
		hash, ok := magnet.InfoHashFromMagnet(item.Magnet)
		if !ok || magnet.IsNullInfoHash(hash) {
			continue
		}
		res := model.Result{
			Title:     title,
			InfoHash:  hash,
			MagnetURI: item.Magnet,
			Seeders:   model.Unknown,
			Leechers:  model.Unknown,
			ListedAt:  now,
			Source:    a.Name(),
			URL:       strings.TrimSpace(item.Link),
			Category:  strings.TrimSpace(item.Category),
		}
		if ts, ok := parseFeedTime(item.PubDate); ok {
			res.PublishDate = ts
		}
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// dmhyItems 逐个 XML token 取出 RSS 里的 item。
func dmhyItems(body []byte) ([]dmhyItem, error) {
	dec := xml.NewDecoder(bytes.NewReader(cleanXML(body)))
	dec.Strict = false
	var (
		items  []dmhyItem
		cur    dmhyItem
		elem   string
		inItem bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("dmhy RSS 解析失败: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "item":
				cur = dmhyItem{}
				inItem = true
			case inItem:
				elem = t.Name.Local
				if t.Name.Local == "enclosure" {
					if uri := strings.TrimSpace(attrOf(t, "url")); strings.HasPrefix(strings.ToLower(uri), "magnet:") {
						cur.Magnet = uri
					}
				}
			}
		case xml.CharData:
			if !inItem || elem == "" {
				continue
			}
			value := string(t)
			switch elem {
			case "title":
				cur.Title += value
			case "link":
				cur.Link += value
			case "pubDate":
				cur.PubDate += value
			case "category":
				cur.Category += value
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
	return items, nil
}

// attrOf 读取 XML 起始元素的属性值。
func attrOf(e xml.StartElement, key string) string {
	for _, a := range e.Attr {
		if a.Name.Local == key {
			return a.Value
		}
	}
	return ""
}
