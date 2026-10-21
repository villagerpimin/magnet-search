package engine

import (
	"context"
	"encoding/base32"
	"encoding/hex"
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

// Tokyotosho 适配 tokyotosho.info 的 tracker 搜索页。
type Tokyotosho struct {
	Client  ClientProvider
	BaseURL string
}

// NewTokyotosho 构造 tokyotosho 适配器。
func NewTokyotosho(c ClientProvider) *Tokyotosho {
	return &Tokyotosho{Client: c, BaseURL: "https://www.tokyotosho.info"}
}

// Name 返回引擎标识。
func (t *Tokyotosho) Name() string { return "tokyotosho" }

// Description 返回引擎描述。
func (t *Tokyotosho) Description() string { return "东京所 tokyotosho.info tracker 搜索" }

// Search 执行搜索。
// 文本搜索页常年被 Cloudflare 的挑战拦住（返回 403），
// 因此主通道失败时退到同样支持关键词的 RSS 通道。
func (t *Tokyotosho) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := t.Client.Get(ctx, t.searchURL(opts))
	if err == nil {
		results, parseErr := t.parse(body)
		if parseErr == nil {
			return limitResults(results, opts.Limit), nil
		}
		err = parseErr
	}
	rssResults, rssErr := t.searchRSS(ctx, opts)
	if rssErr != nil {
		return nil, fmt.Errorf("tokyotosho 请求失败: %w；RSS 通道也不可用: %v", err, rssErr)
	}
	return limitResults(rssResults, opts.Limit), nil
}

// RegisterTokyotosho 把 tokyotosho 注册到引擎表。
func RegisterTokyotosho(reg *Registry) {
	reg.Add(Spec{
		Name:        "tokyotosho",
		Description: "东京所 tokyotosho.info tracker 搜索",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewTokyotosho(c) },
	})
}

// searchURL 拼装查询地址。
// 站点的文本搜索只在 search.php 上生效，首页的 ?q= 参数并不过滤结果。
func (t *Tokyotosho) searchURL(opts Options) string {
	return fmt.Sprintf("%s/search.php?searchName=true&searchComment=true&terms=%s&type=0",
		strings.TrimRight(t.BaseURL, "/"),
		url.QueryEscape(opts.Query),
	)
}

// tokyotosho 列表里的大小、日期与做种数的匹配规则。
var (
	ttSizeRe  = regexp.MustCompile(`Size:\s*([0-9]+(?:\.[0-9]+)?\s*[A-Za-z]+)`)
	ttDateRe  = regexp.MustCompile(`Date:\s*([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2} UTC)`)
	ttSeedRe  = regexp.MustCompile(`S:\s*([0-9]+)`)
	ttLeechRe = regexp.MustCompile(`L:\s*([0-9]+)`)
)

// parse 解析列表页并归一化为结果列表。
// 一条结果占两行 tr：第一行是 desc-top（磁力与标题），第二行是 desc-bot 与 stats（大小、日期、做种数）。
func (t *Tokyotosho) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	doc, err := ParseDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("tokyotosho 页面解析失败: %w", err)
	}
	tables := FindByClass(doc, "listing")
	if len(tables) == 0 {
		return nil, fmt.Errorf("tokyotosho 页面结构变化：找不到 listing 表格")
	}
	rows := FindByTag(tables[0], "tr")
	now := time.Now()
	results := make([]model.Result, 0, len(rows)/2)
	for i, row := range rows {
		// 一条结果占两行：第一行是 desc-top，第二行是 desc-bot 与 stats
		var next *html.Node
		if i+1 < len(rows) {
			next = rows[i+1]
		}
		res, ok := t.parseRow(row, next, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// parseRow 解析一条结果：top 是带磁力与标题的那一行，bot 是带大小与做种数的那一行。
func (t *Tokyotosho) parseRow(top, bot *html.Node, now time.Time) (model.Result, bool) {
	descTop := FindFirst(top, func(n *html.Node) bool { return HasClass(n, "desc-top") })
	if descTop == nil {
		return model.Result{}, false
	}
	descBot := FindFirst(bot, func(n *html.Node) bool { return HasClass(n, "desc-bot") })
	stats := FindFirst(bot, func(n *html.Node) bool { return HasClass(n, "stats") })
	magnetNode := FindFirst(descTop, func(n *html.Node) bool {
		return n.Data == "a" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(Attr(n, "href"))), "magnet:")
	})
	if magnetNode == nil {
		return model.Result{}, false
	}
	uri := Attr(magnetNode, "href")
	hash, ok := tokyoInfoHash(uri)
	if !ok {
		return model.Result{}, false
	}
	titleNode := FindFirst(descTop, func(n *html.Node) bool {
		if n.Data != "a" {
			return false
		}
		if Attr(n, "type") == "application/x-bittorrent" {
			return true
		}
		// 磁力链接里的 tracker 域名也可能带 .torrent，所以只能按结尾判断
		return strings.HasSuffix(strings.ToLower(strings.TrimSpace(Attr(n, "href"))), ".torrent")
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
		Source:    t.Name(),
		Category:  tokyoCategory(top),
		URL:       t.detailURL(top),
	}
	if descBot != nil {
		text := Text(descBot)
		if m := ttSizeRe.FindStringSubmatch(text); m != nil {
			res.SizeBytes = magnet.ParseSize(m[1])
		}
		if m := ttDateRe.FindStringSubmatch(text); m != nil {
			if ts, err := time.Parse("2006-01-02 15:04 MST", m[1]); err == nil {
				res.PublishDate = ts.UTC()
			}
		}
	}
	if stats != nil {
		text := Text(stats)
		if m := ttSeedRe.FindStringSubmatch(text); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				res.Seeders = n
			}
		}
		if m := ttLeechRe.FindStringSubmatch(text); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				res.Leechers = n
			}
		}
	}
	return res, true
}

// detailURL 拼出结果的详情页地址。
func (t *Tokyotosho) detailURL(row *html.Node) string {
	node := FindFirst(row, func(n *html.Node) bool {
		return n.Data == "a" && strings.Contains(Attr(n, "href"), "details.php")
	})
	if node == nil {
		return ""
	}
	return resolveURL(t.BaseURL, Attr(node, "href"))
}

// tokyoCategory 从分类图标里取分类名，sprite_cat-hmanga 对应 hmanga。
func tokyoCategory(row *html.Node) string {
	node := FindFirst(row, func(n *html.Node) bool {
		for _, class := range strings.Fields(Attr(n, "class")) {
			if strings.HasPrefix(class, "sprite_cat-") {
				return true
			}
		}
		return false
	})
	if node == nil {
		return ""
	}
	for _, class := range strings.Fields(Attr(node, "class")) {
		if strings.HasPrefix(class, "sprite_cat-") {
			return strings.TrimPrefix(class, "sprite_cat-")
		}
	}
	return ""
}

// tokyoInfoHash 从磁力链接里取 info hash。
// 站点的 btih 是 32 位 base32，而共享的 magnet.InfoHashFromMagnet 最终走
// magnet.NormalizeInfoHash —— 它先把输入小写化，Go 的 base32 解码器却只接受大写字母，
// 于是 base32 分支恒定失败。因此这里自己截取原始 btih 再分派。
func tokyoInfoHash(uri string) (string, bool) {
	lower := strings.ToLower(uri)
	idx := strings.Index(lower, "urn:btih:")
	if idx < 0 {
		return "", false
	}
	rest := uri[idx+len("urn:btih:"):]
	if end := strings.IndexAny(rest, "&#?"); end >= 0 {
		rest = rest[:end]
	}
	return tokyoNormalizeHash(rest)
}

// tokyoNormalizeHash 把 40 位 hex 或 32 位 base32 的哈希归一化为小写 hex40。
func tokyoNormalizeHash(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if hash, ok := magnet.NormalizeInfoHash(s); ok {
		return hash, true
	}
	if len(s) != 32 {
		return "", false
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(s))
	if err != nil || len(raw) != 20 {
		return "", false
	}
	return magnet.NormalizeInfoHash(hex.EncodeToString(raw))
}
