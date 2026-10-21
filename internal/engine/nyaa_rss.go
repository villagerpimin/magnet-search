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

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// nyaaRSSItem 是 nyaa RSS 里的一条记录。
//
// nyaa 的扩展字段带 nyaa: 前缀，而命名空间地址随站点而变
// （nyaa.si 与 sukebei.nyaa.si 就不一样），encoding/xml 的结构体标签
// 按命名空间地址匹配，因此这里改成逐 token 解析、按元素局部名取值。
type nyaaRSSItem struct {
	Title      string
	Link       string
	GUID       string
	PubDate    string
	Category   string
	CategoryID string
	Size       string
	Seeders    string
	Leechers   string
	Downloads  string
	InfoHash   string
}

// set 把元素文本追加到对应字段。
// 同一个元素可能出现多段文本，因此这里是追加而不是覆盖。
func (it *nyaaRSSItem) set(name, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch name {
	case "title":
		it.Title += value
	case "link":
		it.Link += value
	case "guid":
		it.GUID += value
	case "pubDate":
		it.PubDate += value
	case "category":
		it.Category += value
	case "categoryId":
		it.CategoryID += value
	case "size":
		it.Size += value
	case "seeders":
		it.Seeders += value
	case "leechers":
		it.Leechers += value
	case "downloads":
		it.Downloads += value
	case "infoHash":
		it.InfoHash += value
	}
}

// searchRSS 走 RSS 通道搜索。
func (a *Nyaa) searchRSS(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := fetchWithRetry(ctx, a.Client, a.rssURL(opts))
	if err != nil {
		return nil, fmt.Errorf("nyaa RSS 请求失败: %w", err)
	}
	return a.parseRSS(body)
}

// rssURL 拼装 RSS 通道的查询地址。
// nyaa.ink 不支持 RSS 参数，只有主站点用得上这个通道。
func (a *Nyaa) rssURL(opts Options) string {
	return fmt.Sprintf("%s/?f=0&c=%s&q=%s&page=rss",
		strings.TrimRight(a.BaseURL, "/"),
		nyaaCategoryCode(opts.Category),
		url.QueryEscape(opts.Query),
	)
}

// parseRSS 解析 RSS 响应并归一化为结果列表。
func (a *Nyaa) parseRSS(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	items, err := nyaaRSSItems(raw)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	results := make([]model.Result, 0, len(items))
	for _, item := range items {
		res, ok := a.rssResult(item, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// nyaaRSSItems 逐个 XML token 取出 RSS 里的 item。
func nyaaRSSItems(raw []byte) ([]nyaaRSSItem, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	// 站点偶尔会在描述里写裸 & 之类的字符，放开严格校验才能读完整份订阅
	dec.Strict = false
	var (
		items  []nyaaRSSItem
		cur    nyaaRSSItem
		elem   string
		inItem bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("nyaa RSS 解析失败: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "item":
				cur = nyaaRSSItem{}
				inItem = true
			case inItem:
				elem = t.Name.Local
			}
		case xml.CharData:
			if inItem && elem != "" {
				cur.set(elem, string(t))
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

// rssResult 把 RSS 里的一条记录转成结果。
// RSS 不直接给磁力链接，只给 info hash，因此这里自己拼一条。
func (a *Nyaa) rssResult(item nyaaRSSItem, now time.Time) (model.Result, bool) {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		return model.Result{}, false
	}
	hash, ok := magnet.NormalizeInfoHash(item.InfoHash)
	if !ok {
		return model.Result{}, false
	}
	res := model.Result{
		Title:     title,
		InfoHash:  hash,
		MagnetURI: magnet.Build(hash, title, nil),
		Seeders:   nyaaNumber(item.Seeders),
		Leechers:  nyaaNumber(item.Leechers),
		ListedAt:  now,
		Source:    a.Name(),
		Category:  strings.TrimSpace(item.Category),
	}
	if res.Category == "" {
		res.Category = nyaaCategoryGroup(item.CategoryID)
	}
	if size, ok := nyaaParseSize(item.Size); ok {
		res.SizeBytes = size
	}
	if ts, ok := parseFeedTime(item.PubDate); ok {
		res.PublishDate = ts
	}
	if guid := strings.TrimSpace(item.GUID); strings.HasPrefix(guid, "http") {
		res.URL = guid
	}
	return res, true
}

// nyaaNumber 解析 RSS 里的数值，缺失或非法时返回 model.Unknown。
func nyaaNumber(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return model.Unknown
	}
	return n
}

// nyaaCategoryGroups 是 nyaa 分类编号的大类名，用于分类名字段缺失时的兜底。
var nyaaCategoryGroups = map[string]string{
	"1": "Anime",
	"2": "Audio",
	"3": "Literature",
	"4": "Live Action",
	"5": "Pictures",
	"6": "Software",
}

// nyaaCategoryGroup 从分类编号里取大类名，例如 "1_2" 对应 Anime。
func nyaaCategoryGroup(categoryID string) string {
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return ""
	}
	return nyaaCategoryGroups[strings.SplitN(categoryID, "_", 2)[0]]
}
