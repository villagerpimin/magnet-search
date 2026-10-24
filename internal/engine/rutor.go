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

// Rutor 适配 rutor.info 的俄文搜索页。
type Rutor struct {
	Client  ClientProvider
	BaseURL string
}

// NewRutor 构造 rutor 适配器。
func NewRutor(c ClientProvider) *Rutor {
	return &Rutor{Client: c, BaseURL: "https://rutor.info"}
}

// Name 返回引擎标识。
func (r *Rutor) Name() string { return "rutor" }

// Description 返回引擎描述。
func (r *Rutor) Description() string { return "rutor.info 俄文资源搜索" }

// Search 执行搜索，按需翻页直到满足 Limit。
func (r *Rutor) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	results := make([]model.Result, 0, rutorPageSize)
	for page := 0; page < rutorPageCount(opts.Limit); page++ {
		body, err := r.Client.Get(ctx, r.searchURL(opts, page))
		if err != nil {
			if page == 0 {
				return nil, fmt.Errorf("rutor 请求失败: %w", err)
			}
			break
		}
		pageResults, err := r.parse(body)
		if err != nil {
			if page == 0 {
				return nil, err
			}
			break
		}
		for i := range pageResults {
			pageResults[i].Rank = len(results) + i
		}
		results = append(results, pageResults...)
		if len(pageResults) == 0 || (opts.Limit > 0 && len(results) >= opts.Limit) {
			break
		}
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterRutor 把 rutor 注册到引擎表。
func RegisterRutor(reg *Registry) {
	reg.Add(Spec{
		Name:        "rutor",
		Description: "rutor.info 俄文资源搜索",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewRutor(c) },
	})
}

// searchURL 拼装查询地址。
//
// 站点真实的路由是 /search/{页码}/{分类}/{检索方式}{检索范围}0/{排序}/{关键词}，
// 页面里的 search_submit 脚本就按这个格式跳转；页码从 0 开始、任意分类、
// “所有词”（检索方式 1）、只搜标题（检索范围 0）、按添加时间排序。
//
// 注意：/search/word/{关键词} 这种写法会被站点当成别的参数，
// 实际搜索的是字面量 “word”，对任何关键词都返回同一批结果，因此这里不用它。
func (r *Rutor) searchURL(opts Options, page int) string {
	// 站点把关键词里的 & 当成逻辑与运算符
	query := strings.ReplaceAll(opts.Query, "&", "AND")
	return fmt.Sprintf("%s/search/%d/0/100/0/%s",
		strings.TrimRight(r.BaseURL, "/"),
		page,
		url.PathEscape(query),
	)
}

const (
	// rutorPageSize 是站点搜索结果每页的条目数。
	rutorPageSize = 100
	// rutorMaxPages 是最多翻取的页数。
	rutorMaxPages = 3
)

// rutorPageCount 根据 Limit 决定要翻多少页，页码从 0 开始。
func rutorPageCount(limit int) int {
	if limit <= 0 {
		return rutorMaxPages
	}
	pages := (limit + rutorPageSize - 1) / rutorPageSize
	if pages > rutorMaxPages {
		pages = rutorMaxPages
	}
	return pages
}

// rutor 列表里的大小与日期匹配规则。
var (
	rutorSizeRe = regexp.MustCompile(`^[0-9][0-9.,]*\s*(?:B|KB|MB|GB|TB)$`)
	rutorDateRe = regexp.MustCompile(`^[0-9]{1,2}\s+[^\s]+\s+[0-9]{2}$`)
)

// rutorMonths 是 rutor 使用的俄文月份缩写，键为小写的前三个字母。
var rutorMonths = map[string]time.Month{
	"янв": time.January, "фев": time.February, "мар": time.March,
	"апр": time.April, "май": time.May, "июн": time.June,
	"июл": time.July, "авг": time.August, "сен": time.September,
	"окт": time.October, "ноя": time.November, "дек": time.December,
}

// parse 解析搜索页并归一化为结果列表。
func (r *Rutor) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("rutor 页面解析失败: %w", err)
	}
	rows := FindByTag(doc, "tr")
	now := time.Now()
	results := make([]model.Result, 0, len(rows))
	for _, row := range rows {
		res, ok := r.parseRow(row, now)
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
// 站点的结果行是 tr.gai 与 tr.tum 交替出现的斑马纹，两种行结构完全一致。
func (r *Rutor) parseRow(row *html.Node, now time.Time) (model.Result, bool) {
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
		return n.Data == "a" && strings.HasPrefix(Attr(n, "href"), "/torrent/")
	})
	if titleNode == nil {
		return model.Result{}, false
	}
	res := model.Result{
		Title:     Text(titleNode),
		InfoHash:  hash,
		MagnetURI: uri,
		Seeders:   model.Unknown,
		Leechers:  model.Unknown,
		ListedAt:  now,
		Source:    r.Name(),
		URL:       resolveURL(r.BaseURL, Attr(titleNode, "href")),
	}
	for _, cell := range rutorCells(row) {
		if cell == magnetNode.Parent {
			// 标题单元格，标题本身也可能长得像日期或大小
			continue
		}
		text := Text(cell)
		switch {
		case rutorIsPeersCell(cell):
			// “Пиры” 单元格里做种数放在 green 样式、下载数放在 red 样式；
			// 万一只有一个合并数字，则按做种数处理，下载数保持 Unknown。
			green := FindFirst(cell, func(x *html.Node) bool { return HasClass(x, "green") })
			red := FindFirst(cell, func(x *html.Node) bool { return HasClass(x, "red") })
			if n, ok := rutorPeers(green); ok {
				res.Seeders = n
			}
			if n, ok := rutorPeers(red); ok {
				res.Leechers = n
			}
			if green == nil && red == nil {
				if n, ok := rutorSingleNumber(text); ok {
					res.Seeders = n
				}
			}
		case rutorDateRe.MatchString(text):
			if ts, ok := rutorDate(text); ok {
				res.PublishDate = ts
			}
		case rutorSizeRe.MatchString(text):
			res.SizeBytes = magnet.ParseSize(text)
		}
	}
	return res, true
}

// rutorCells 取出行自己的单元格，不含嵌套表格的单元格。
func rutorCells(row *html.Node) []*html.Node {
	var out []*html.Node
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "td" {
			out = append(out, c)
		}
	}
	return out
}

// rutorIsPeersCell 判断单元格是不是“Пиры”（在线人数）单元格。
// 该单元格带有 alt="S"（做种）与 alt="L"（下载）的小箭头图标，
// 评论区那种 alt="C" 的图标不算。
func rutorIsPeersCell(cell *html.Node) bool {
	arrow := FindFirst(cell, func(n *html.Node) bool {
		if n.Data != "img" {
			return false
		}
		alt := strings.ToUpper(strings.TrimSpace(Attr(n, "alt")))
		return alt == "S" || alt == "L"
	})
	if arrow != nil {
		return true
	}
	return FindFirst(cell, func(n *html.Node) bool {
		return HasClass(n, "green") || HasClass(n, "red")
	}) != nil
}

// rutorSingleNumber 从文本里取唯一的数字，用于只有一个合并数值的场景。
func rutorSingleNumber(text string) (int, bool) {
	var found []int
	for _, field := range strings.Fields(text) {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil {
			continue
		}
		found = append(found, n)
	}
	if len(found) != 1 {
		return 0, false
	}
	return found[0], true
}

// rutorPeers 从人数样式里解析数字，无法解析时返回 false。
func rutorPeers(node *html.Node) (int, bool) {
	if node == nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(Text(node)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// rutorDate 解析 “15 Сен 26” 形式的俄文日期，站点只给两位年份。
func rutorDate(text string) (time.Time, bool) {
	fields := strings.Fields(text)
	if len(fields) != 3 {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(fields[0])
	if err != nil {
		return time.Time{}, false
	}
	month, ok := rutorMonths[rutorMonthKey(fields[1])]
	if !ok {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(fields[2])
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(2000+year, month, day, 0, 0, 0, 0, time.UTC), true
}

// rutorMonthKey 取月份词的前三个字母，兼容 “Сентябрь” 这类完整写法。
func rutorMonthKey(s string) string {
	runes := []rune(strings.ToLower(strings.TrimSpace(s)))
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes)
}
