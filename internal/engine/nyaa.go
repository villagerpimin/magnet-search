package engine

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Nyaa 适配 nyaa.si（镜像 nyaa.ink）的搜索。
//
// 站点同时提供 HTML 列表与 RSS 两个通道：RSS 额外带做种、下载与完成下载数，
// 因此优先走 RSS，RSS 不可用时再退回 HTML 列表。
type Nyaa struct {
	Client  ClientProvider
	BaseURL string
}

// NewNyaa 构造 nyaa 适配器。
func NewNyaa(c ClientProvider) *Nyaa {
	return &Nyaa{Client: c, BaseURL: "https://nyaa.si"}
}

// Name 返回引擎标识。
func (a *Nyaa) Name() string { return "nyaa" }

// Description 返回引擎描述。
func (a *Nyaa) Description() string { return "nyaa.si 动漫音频资源搜索（RSS 优先）" }

// Search 执行搜索。
// RSS 通道不可用时依次退回各站点的 HTML 列表。
func (a *Nyaa) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	rssResults, rssErr := a.searchRSS(ctx, opts)
	if rssErr == nil {
		return limitResults(rssResults, opts.Limit), nil
	}
	var htmlErr error
	for _, base := range a.bases() {
		body, err := fetchWithRetry(ctx, a.Client, a.searchURL(base, opts))
		if err != nil {
			htmlErr = err
			continue
		}
		results, err := a.parse(body, base)
		if err != nil {
			htmlErr = err
			continue
		}
		return limitResults(results, opts.Limit), nil
	}
	return nil, fmt.Errorf("nyaa 请求失败: RSS 通道 %v；HTML 通道 %v", rssErr, htmlErr)
}

// RegisterNyaa 把 nyaa 注册到引擎表。
func RegisterNyaa(reg *Registry) {
	reg.Add(Spec{
		Name:        "nyaa",
		Description: "nyaa.si 动漫音频资源搜索（RSS 优先）",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewNyaa(c) },
	})
}

// nyaaMirrors 是主站点不可用时的备用站点。
// nyaa.ink 提供同样的 HTML 列表，但不支持 RSS 参数。
var nyaaMirrors = []string{"https://nyaa.ink"}

// bases 返回依次尝试的站点地址。
func (a *Nyaa) bases() []string {
	primary := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if primary == "" {
		primary = "https://nyaa.si"
	}
	out := []string{primary}
	for _, m := range nyaaMirrors {
		if m != primary {
			out = append(out, m)
		}
	}
	return out
}

// searchURL 拼装 HTML 列表的查询地址。
func (a *Nyaa) searchURL(base string, opts Options) string {
	return fmt.Sprintf("%s/?f=0&c=%s&q=%s",
		strings.TrimRight(base, "/"),
		nyaaCategoryCode(opts.Category),
		url.QueryEscape(opts.Query),
	)
}

// nyaaCategoryCode 把聚合层的分类名映射为 nyaa 的分类参数。
// 站点的分类形如 1_2，前一位是大类、后一位是子类。
// 站点把视频拆成动画与真人影视两类，单个参数覆盖不了，
// 因此 video 不做过滤，免得漏掉一半结果。
func nyaaCategoryCode(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "anime":
		return "1_0"
	case "audio", "music":
		return "2_0"
	case "book", "books", "literature":
		return "3_0"
	case "movie", "live-action", "liveaction":
		return "4_0"
	case "picture", "pictures", "image", "images":
		return "5_0"
	case "software", "app", "apps":
		return "6_0"
	default:
		return "0_0"
	}
}

// parse 解析 HTML 列表页并归一化为结果列表，base 是实际提供页面的站点地址。
func (a *Nyaa) parse(body []byte, base string) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("nyaa 页面解析失败: %w", err)
	}
	rows := FindByTag(doc, "tr")
	now := time.Now()
	results := make([]model.Result, 0, len(rows))
	for _, row := range rows {
		res, ok := a.parseRow(row, base, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// parseRow 解析一行结果。
// 一行里依次是分类图标、标题、下载与磁力、大小、日期、做种数、下载数、完成下载数。
func (a *Nyaa) parseRow(row *html.Node, base string, now time.Time) (model.Result, bool) {
	magnetNode := FindFirst(row, func(n *html.Node) bool {
		return n.Data == "a" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(Attr(n, "href"))), "magnet:")
	})
	if magnetNode == nil {
		return model.Result{}, false
	}
	uri := Attr(magnetNode, "href")
	hash, ok := magnet.InfoHashFromMagnet(uri)
	if !ok {
		return model.Result{}, false
	}
	titleNode := FindFirst(row, func(n *html.Node) bool {
		if n.Data != "a" {
			return false
		}
		href := Attr(n, "href")
		// 标题前面还有一个指向同一资源、只带评论数的链接，
		// 靠 URL 里的 #comments 片段把两者区分开
		return strings.HasPrefix(href, "/view/") && !strings.Contains(href, "#")
	})
	if titleNode == nil {
		return model.Result{}, false
	}
	title := Text(titleNode)
	if title == "" {
		title = strings.TrimSpace(Attr(titleNode, "title"))
	}
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
		Category:  nyaaCategoryName(row),
		URL:       resolveURL(base, Attr(titleNode, "href")),
	}
	// 末尾三个纯数字单元格依次是做种数、下载数与完成下载数，
	// 结果模型里没有完成下载数的位置，因此只取前两个。
	// 只有排在大小单元格之后的纯数字才算人数，
	// 否则标题里恰好是数字的条目会被误当成人数。
	sizeSeen := false
	var numbers []int
	for _, cell := range nyaaCells(row) {
		text := Text(cell)
		if ts := strings.TrimSpace(Attr(cell, "data-timestamp")); ts != "" {
			if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
				res.PublishDate = time.Unix(n, 0).UTC()
			}
			continue
		}
		if size, ok := nyaaParseSize(text); ok {
			res.SizeBytes = size
			sizeSeen = true
			continue
		}
		if !sizeSeen {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			numbers = append(numbers, n)
		}
	}
	if len(numbers) >= 2 {
		res.Seeders = numbers[0]
		res.Leechers = numbers[1]
	}
	return res, true
}

// nyaaCells 取出行自己的单元格，不含嵌套表格的单元格。
func nyaaCells(row *html.Node) []*html.Node {
	var out []*html.Node
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "td" {
			out = append(out, c)
		}
	}
	return out
}

// nyaaCategoryName 从分类图标里取分类名，图标缺失时退回分类链接的 title。
func nyaaCategoryName(row *html.Node) string {
	img := FindFirst(row, func(n *html.Node) bool {
		return n.Data == "img" && HasClass(n, "category-icon")
	})
	if img != nil {
		if alt := strings.TrimSpace(Attr(img, "alt")); alt != "" {
			return alt
		}
	}
	link := FindFirst(row, func(n *html.Node) bool {
		return n.Data == "a" && strings.HasPrefix(Attr(n, "href"), "/?c=")
	})
	if link != nil {
		return strings.TrimSpace(Attr(link, "title"))
	}
	return ""
}

// nyaaSizeUnits 是 nyaa 使用的大小单位，都是二进制倍数。
var nyaaSizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"TIB", 1 << 40},
	{"GIB", 1 << 30},
	{"MIB", 1 << 20},
	{"KIB", 1 << 10},
}

// nyaaParseSize 解析 nyaa 给出的二进制单位大小，例如 "624.0 MiB"。
// magnet.ParseSize 只认 KB/MB/GB 这类写法，遇到 MiB 会直接解析失败，
// 因此这里先处理二进制单位，其余情况再交给它。
// 必须带单位才算大小：行尾还有做种、下载、完成下载三个纯数字单元格，
// 没有单位限制的话它们会被当成大小，把真正的文件大小顶掉。
func nyaaParseSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	for _, u := range nyaaSizeUnits {
		if !strings.HasSuffix(upper, u.suffix) {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(upper, u.suffix)), 64)
		if err != nil {
			return 0, false
		}
		return int64(f * float64(u.mult)), true
	}
	if !strings.HasSuffix(upper, "B") {
		return 0, false
	}
	if n := magnet.ParseSize(s); n > 0 {
		return n, true
	}
	return 0, false
}
