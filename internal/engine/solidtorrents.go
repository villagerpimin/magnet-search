package engine

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Solidtorrents 适配 solidtorrents.to 的搜索结果页。
//
// 该站与 bitsearch.eu 共用同一套 Tailwind 结果模板，
// 卡片结构完全一致，因此大小、日期的匹配规则直接复用引擎包里那一份。
type Solidtorrents struct {
	Client  ClientProvider
	BaseURL string
}

// NewSolidtorrents 构造 solidtorrents 适配器。
func NewSolidtorrents(c ClientProvider) *Solidtorrents {
	return &Solidtorrents{Client: c, BaseURL: "https://solidtorrents.to"}
}

// Name 返回引擎标识。
func (a *Solidtorrents) Name() string { return "solidtorrents" }

// Description 返回引擎描述。
func (a *Solidtorrents) Description() string { return "solidtorrents.to 磁力搜索" }

// Search 执行搜索。
func (a *Solidtorrents) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := fetchWithRetry(ctx, a.Client, a.searchURL(opts))
	if err != nil {
		return nil, fmt.Errorf("solidtorrents 请求失败: %w", err)
	}
	results, err := a.parse(body)
	if err != nil {
		return nil, err
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterSolidtorrents 把 solidtorrents 注册到引擎表。
func RegisterSolidtorrents(reg *Registry) {
	reg.Add(Spec{
		Name:        "solidtorrents",
		Description: "solidtorrents.to 磁力搜索",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewSolidtorrents(c) },
	})
}

// searchURL 拼装查询地址。
func (a *Solidtorrents) searchURL(opts Options) string {
	return fmt.Sprintf("%s/search?q=%s",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
	)
}

// parse 解析结果页并归一化为结果列表。
func (a *Solidtorrents) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("solidtorrents 页面解析失败: %w", err)
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
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// parseCard 解析一张结果卡片。
func (a *Solidtorrents) parseCard(card *html.Node, now time.Time) (model.Result, bool) {
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
// 站点的分类图标并不统一（有的用 fa-video，有的用 fa-download），
// 因此按内容判断：像大小的是大小，像日期的是日期，剩下的就是分类。
func (a *Solidtorrents) parseMeta(meta *html.Node, res *model.Result) {
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

// parseSwarm 解析做种与下载那一行，人数按外层 span 的颜色类区分。
func (a *Solidtorrents) parseSwarm(meta *html.Node, res *model.Result) {
	for _, span := range childSpans(meta) {
		switch {
		case HasClass(span, "text-green-600"):
			res.Seeders = bitsearchValue(span)
		case HasClass(span, "text-red-600"):
			res.Leechers = bitsearchValue(span)
		}
	}
}
