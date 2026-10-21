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

// Knaben 适配 knaben.org 的搜索结果页。
//
// 该站每行结果带一个状态徽标，写着索引站最近一次确认该资源还存在于种子网络的时间
// （"This torrent was checked 59 minutes ago"），比页面上宣称的做种数更可靠，
// 因此这里把它解析进 model.Result.LastSeen。
type Knaben struct {
	Client  ClientProvider
	BaseURL string
}

// NewKnaben 构造 knaben 适配器。
func NewKnaben(c ClientProvider) *Knaben {
	return &Knaben{Client: c, BaseURL: "https://knaben.org"}
}

// Name 返回引擎标识。
func (a *Knaben) Name() string { return "knaben" }

// Description 返回引擎描述。
func (a *Knaben) Description() string { return "knaben.org 磁力搜索（带最近确认时间）" }

// Search 执行搜索，主域名失败时依次回退到镜像站点。
func (a *Knaben) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	var lastErr error
	for _, base := range a.bases() {
		body, err := fetchWithRetry(ctx, a.Client, a.searchURL(base, opts))
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", base, err)
			continue
		}
		results, err := a.parse(body)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", base, err)
			continue
		}
		return limitResults(results, opts.Limit), nil
	}
	return nil, fmt.Errorf("knaben 请求失败: %w", lastErr)
}

// RegisterKnaben 把 knaben 注册到引擎表。
func RegisterKnaben(reg *Registry) {
	reg.Add(Spec{
		Name:        "knaben",
		Description: "knaben.org 磁力搜索（带最近确认时间）",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewKnaben(c) },
	})
}

// bases 返回依次尝试的站点地址，主地址在前、镜像在后。
//
// 目前只有 knaben.org 提供搜索页：knaben.xyz 只镜像 The Pirate Bay 的详情页，
// 对 /search 路径一律返回 403，因此暂时没有可用的备用域名。
func (a *Knaben) bases() []string {
	primary := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if primary == "" {
		primary = "https://knaben.org"
	}
	return []string{primary}
}

// searchURL 拼装查询地址。
//
// 站点路径形如 /search/{关键词}/{分类}/{页码}/{排序}：
// 分类 0 表示全部分类，页码从 1 开始，排序用 seeders 表示按做种数从多到少。
// hideXXX 参数用来屏蔽成人内容，聚合层默认不展示这类资源。
func (a *Knaben) searchURL(base string, opts Options) string {
	return fmt.Sprintf("%s/search/%s/0/1/seeders?hideXXX",
		strings.TrimRight(base, "/"),
		url.PathEscape(opts.Query),
	)
}

// parse 解析搜索结果页并归一化为结果列表。
func (a *Knaben) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("knaben 页面解析失败: %w", err)
	}
	// 结果行的标识是行上的 data-id，表头里的 tr 没有这个属性
	rows := FindAll(doc, func(n *html.Node) bool { return n.Data == "tr" && Attr(n, "data-id") != "" })
	now := time.Now()
	results := make([]model.Result, 0, len(rows))
	for _, row := range rows {
		res, ok := a.parseRow(row, now)
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
//
// 单元格顺序与表头一致：分类、标题、大小、日期、做种数、下载数、来源。
func (a *Knaben) parseRow(row *html.Node, now time.Time) (model.Result, bool) {
	cells := knabenCells(row)
	if len(cells) < 6 {
		return model.Result{}, false
	}
	anchor := knabenTitleAnchor(cells[1])
	if anchor == nil {
		return model.Result{}, false
	}
	// HTML 解析器已经把属性里的实体还原，这里拿到的是原始磁力链接
	uri := strings.TrimSpace(Attr(anchor, "href"))
	hash, ok := knabenInfoHash(row, uri)
	if !ok {
		return model.Result{}, false
	}
	title := strings.TrimSpace(Attr(anchor, "title"))
	if title == "" {
		title = Text(anchor)
	}
	if title == "" {
		return model.Result{}, false
	}
	res := model.Result{
		Title:     title,
		InfoHash:  hash,
		MagnetURI: uri,
		SizeBytes: magnet.ParseSize(Text(cells[2])),
		Seeders:   knabenCount(Text(cells[4])),
		Leechers:  knabenCount(Text(cells[5])),
		ListedAt:  now,
		Source:    a.Name(),
		Category:  knabenCategory(cells[0]),
	}
	if ts, ok := knabenDate(Attr(cells[3], "title")); ok {
		res.PublishDate = ts
	}
	if ts, ok := knabenLastSeen(cells[0], now); ok {
		res.LastSeen = ts
	}
	// 来源列给的是该条目在索引站上的详情页
	if len(cells) > 6 {
		for _, a := range FindByTag(cells[6], "a") {
			if href := strings.TrimSpace(Attr(a, "href")); strings.HasPrefix(href, "http") {
				res.URL = href
				break
			}
		}
	}
	return res, true
}

// knabenCells 取出一行结果里的各个单元格（<tr> 的直接 <td> 子节点）。
func knabenCells(row *html.Node) []*html.Node {
	var out []*html.Node
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "td" {
			out = append(out, c)
		}
	}
	return out
}

// knabenTitleAnchor 在标题单元格里挑出最合适的磁力链接锚点。
//
// 行里有两个磁力链接：标题文本本身与右侧的"用磁力下载"按钮。
// 标题锚点带 title 属性（完整标题），按钮锚点只有图标，因此优先挑带 title 的那个。
func knabenTitleAnchor(cell *html.Node) *html.Node {
	var first *html.Node
	for _, a := range FindByTag(cell, "a") {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(Attr(a, "href"))), "magnet:") {
			continue
		}
		if first == nil {
			first = a
		}
		if strings.TrimSpace(Attr(a, "title")) != "" {
			return a
		}
	}
	return first
}

// knabenInfoHash 取出该行的 info hash。
//
// 行上的 data-id 是站点内部的行标识，与磁力链接里的 btih 并不总是同一个值，
// 因此以磁力链接里的哈希为准，只有磁力链接解析不出哈希时才退回 data-id。
func knabenInfoHash(row *html.Node, uri string) (string, bool) {
	if hash, ok := magnet.InfoHashFromMagnet(uri); ok {
		return hash, true
	}
	return magnet.NormalizeInfoHash(Attr(row, "data-id"))
}

// knabenCategory 取出分类面包屑。
//
// 分类单元格里除了面包屑还有一个状态徽标，它的数字会混进整格文本，
// 因此只取指向 /browse/ 的分类链接，再用 " / " 连接。
func knabenCategory(cell *html.Node) string {
	var parts []string
	for _, a := range FindByTag(cell, "a") {
		if !strings.HasPrefix(Attr(a, "href"), "/browse/") {
			continue
		}
		if text := Text(a); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " / ")
}

// knabenCount 解析做种数或下载数，站点没给出数值时返回 model.Unknown。
func knabenCount(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return model.Unknown
	}
	return n
}

// knabenDate 解析日期单元格 title 上的时间戳（形如 2026-04-25 16:35:00）。
// 站点不带时区标记，这里统一按 UTC 解释。
func knabenDate(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if ts, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

// knabenLastSeen 解析"最近确认"时间。
//
// 状态徽标的 tooltip 写着 "This torrent was checked 59 minutes ago"
// 或 "This torrent was last seen 2026-09-13"，前者是相对时长、后者是绝对日期，
// 都要换算成绝对时间点；解析不了就当作未知。
func knabenLastSeen(cell *html.Node, now time.Time) (time.Time, bool) {
	node := FindFirst(cell, func(x *html.Node) bool {
		return strings.TrimSpace(Attr(x, "data-bs-original-title")) != ""
	})
	if node == nil {
		return time.Time{}, false
	}
	return knabenSeenTime(Attr(node, "data-bs-original-title"), now)
}

// knabenSeenTime 把 tooltip 里的说明文字换算成绝对时间点。
func knabenSeenTime(text string, now time.Time) (time.Time, bool) {
	s := strings.TrimSpace(text)
	for _, prefix := range []string{"This torrent was ", "checked ", "last seen "} {
		s = strings.TrimPrefix(s, prefix)
	}
	switch strings.ToLower(s) {
	case "now", "just now":
		return now, true
	}
	if ts, err := time.Parse("2006-01-02", s); err == nil {
		return ts.UTC(), true
	}
	d, ok := knabenAgo(s)
	if !ok {
		return time.Time{}, false
	}
	return now.Add(-d), true
}

// knabenAgoUnits 是相对时长里各时间单位的长度。
// 月按 30 天、年按 365 天折算，这是站点只给相对时长时的近似值。
var knabenAgoUnits = map[string]time.Duration{
	"second": time.Second, "seconds": time.Second,
	"minute": time.Minute, "minutes": time.Minute,
	"hour": time.Hour, "hours": time.Hour,
	"day": 24 * time.Hour, "days": 24 * time.Hour,
	"week": 7 * 24 * time.Hour, "weeks": 7 * 24 * time.Hour,
	"month": 30 * 24 * time.Hour, "months": 30 * 24 * time.Hour,
	"year": 365 * 24 * time.Hour, "years": 365 * 24 * time.Hour,
}

// knabenAgo 解析 "3 days ago" 这类相对时长。
func knabenAgo(s string) (time.Duration, bool) {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) != 3 || fields[2] != "ago" {
		return 0, false
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 0 {
		return 0, false
	}
	unit, ok := knabenAgoUnits[fields[1]]
	if !ok {
		return 0, false
	}
	return time.Duration(n) * unit, true
}
