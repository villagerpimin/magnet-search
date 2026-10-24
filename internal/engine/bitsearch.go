package engine

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Bitsearch 适配 bitsearch.eu（bitsearch.to 会跳转到这里）的搜索结果页。
type Bitsearch struct {
	Client  ClientProvider
	BaseURL string
}

// NewBitsearch 构造 bitsearch 适配器。
func NewBitsearch(c ClientProvider) *Bitsearch {
	return &Bitsearch{Client: c, BaseURL: "https://bitsearch.eu"}
}

// Name 返回引擎标识。
func (a *Bitsearch) Name() string { return "bitsearch" }

// Description 返回引擎描述。
func (a *Bitsearch) Description() string { return "bitsearch.eu 磁力搜索" }

// Search 执行搜索，按需翻页直到满足 Limit。
func (a *Bitsearch) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	results := make([]model.Result, 0, bitsearchPageSize)
	for page := 1; page <= a.pageCount(opts.Limit); page++ {
		body, err := a.Client.Get(ctx, a.searchURL(opts, page))
		if err != nil {
			if page == 1 {
				return nil, fmt.Errorf("bitsearch 请求失败: %w", err)
			}
			// 后续页失败时保留已经取到的结果
			break
		}
		pageResults, err := a.parse(body, len(results))
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		if len(pageResults) == 0 {
			break
		}
		results = append(results, pageResults...)
		if opts.Limit > 0 && len(results) >= opts.Limit {
			break
		}
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterBitsearch 把 bitsearch 注册到引擎表。
func RegisterBitsearch(reg *Registry) {
	reg.Add(Spec{
		Name:        "bitsearch",
		Description: "bitsearch.eu 磁力搜索",
		Region:      RegionAny,
		New:         func(c ClientProvider) Engine { return NewBitsearch(c) },
	})
}

const (
	// bitsearchPageSize 是搜索结果每页的条目数。
	bitsearchPageSize = 20
	// bitsearchMaxPages 是最多翻取的页数，避免无上限翻页。
	bitsearchMaxPages = 8
)

// searchURL 拼装某一页的查询地址。
func (a *Bitsearch) searchURL(opts Options, page int) string {
	return fmt.Sprintf("%s/search?q=%s&page=%d",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
		page,
	)
}

// pageCount 根据 Limit 决定要翻多少页。
func (a *Bitsearch) pageCount(limit int) int {
	if limit <= 0 {
		return bitsearchMaxPages
	}
	pages := (limit + bitsearchPageSize - 1) / bitsearchPageSize
	if pages > bitsearchMaxPages {
		pages = bitsearchMaxPages
	}
	return pages
}

// bitsearch 元信息里大小与日期的匹配规则。
var (
	bsSizeRe = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?\s*(?:B|KB|MB|GB|TB)$`)
	bsDateRe = regexp.MustCompile(`^[0-9]{1,2}/[0-9]{1,2}/[0-9]{4}$`)
)

// parse 解析结果页并归一化为结果列表，rankOffset 是这一页之前已经有的结果条数。
func (a *Bitsearch) parse(body []byte, rankOffset int) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("bitsearch 页面解析失败: %w", err)
	}
	cards := FindByClass(doc, "torrent-result-card")
	now := time.Now()
	results := make([]model.Result, 0, len(cards))
	for _, card := range cards {
		res, ok := a.parseCard(card, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = rankOffset + len(results)
		results = append(results, res)
	}
	return results, nil
}

// parseCard 解析一张结果卡片。
func (a *Bitsearch) parseCard(card *html.Node, now time.Time) (model.Result, bool) {
	magnetNode := FindFirst(card, func(n *html.Node) bool {
		return n.Data == "a" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(Attr(n, "href"))), "magnet:")
	})
	if magnetNode == nil {
		return model.Result{}, false
	}
	// HTML 解析器已经把属性里的 &#x3D; 与 &amp; 还原，这里拿到的是原始磁力链接
	uri := Attr(magnetNode, "href")
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		return model.Result{}, false
	}
	head := FindFirst(card, func(n *html.Node) bool { return n.Data == "h3" })
	if head == nil {
		return model.Result{}, false
	}
	link := FindFirst(head, func(n *html.Node) bool { return n.Data == "a" })
	if link == nil {
		return model.Result{}, false
	}
	title := Text(link)
	if title == "" {
		return model.Result{}, false
	}
	res := model.Result{
		Title:     title,
		InfoHash:  hash,
		MagnetURI: uri,
		Seeders:   model.Unknown,
		Leechers:  model.Unknown,
		ListedAt:  now,
		Source:    a.Name(),
		URL:       resolveURL(a.BaseURL, Attr(link, "href")),
	}
	for _, meta := range FindByClass(card, "torrent-result-meta") {
		if FindFirst(meta, func(n *html.Node) bool { return HasClass(n, "text-green-600") }) != nil {
			a.parseSwarm(meta, &res)
			continue
		}
		a.parseMeta(meta, &res)
	}
	return res, true
}

// parseMeta 解析分类、大小与日期所在的那一行元信息。
// 卡片的分类图标并不统一（有的分类用 fa-video，有的用 fa-download），
// 因此这里按内容判断：像大小的是大小，像日期的是日期，剩下的就是分类。
func (a *Bitsearch) parseMeta(meta *html.Node, res *model.Result) {
	for _, span := range childSpans(meta) {
		text := Text(span)
		switch {
		case bsSizeRe.MatchString(text):
			res.SizeBytes = magnet.ParseSize(text)
		case bsDateRe.MatchString(text):
			if ts, err := time.Parse("1/2/2006", strings.TrimSpace(text)); err == nil {
				res.PublishDate = ts.UTC()
			}
		default:
			if res.Category == "" {
				res.Category = text
			}
		}
	}
}

// parseSwarm 解析做种/下载/下载次数那一行，做种与下载数按外层 span 的颜色类区分。
func (a *Bitsearch) parseSwarm(meta *html.Node, res *model.Result) {
	for _, span := range childSpans(meta) {
		switch {
		case HasClass(span, "text-green-600"):
			res.Seeders = bitsearchValue(span)
		case HasClass(span, "text-red-600"):
			res.Leechers = bitsearchValue(span)
		}
	}
}

// bitsearchValue 取人数：数值放在 class 为 font-medium 的内层 span 里。
func bitsearchValue(span *html.Node) int {
	inner := FindFirst(span, func(n *html.Node) bool {
		return n.Data == "span" && HasClass(n, "font-medium")
	})
	if inner != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(Text(inner))); err == nil {
			return n
		}
	}
	for _, field := range strings.Fields(Text(span)) {
		if n, err := strconv.Atoi(field); err == nil {
			return n
		}
	}
	return model.Unknown
}

// childSpans 取出元素自己的 span 子节点。
func childSpans(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "span" {
			out = append(out, c)
		}
	}
	return out
}
