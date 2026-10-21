package engine

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// tokyoFeed 是站点 RSS 通道的最小结构。
// item 元素在 channel 下面，因此要用 channel>item 的路径写法。
type tokyoFeed struct {
	Items []tokyoFeedItem `xml:"channel>item"`
}

// tokyoFeedItem 是 RSS 里的一条记录。
type tokyoFeedItem struct {
	Category string `xml:"category"`
	Title    string `xml:"title"`
	Desc     string `xml:"description"`
	PubDate  string `xml:"pubDate"`
}

// RSS 通道里磁力、详情页与大小的匹配规则。
var (
	rssMagnetRe = regexp.MustCompile(`href="(magnet:[^"]+)"`)
	rssDetailRe = regexp.MustCompile(`href="([^"]*details\.php\?id=\d+)"`)
	rssSizeRe   = regexp.MustCompile(`Size:\s*([0-9]+(?:\.[0-9]+)?\s*[A-Za-z]+)`)
)

// rssURL 拼装 RSS 通道的查询地址。
// rss.php 同样支持 terms 关键词参数，因此它是文本搜索页被 Cloudflare 拦住时的备用通道。
func (t *Tokyotosho) rssURL(opts Options) string {
	return fmt.Sprintf("%s/rss.php?terms=%s",
		strings.TrimRight(t.BaseURL, "/"),
		url.QueryEscape(opts.Query),
	)
}

// searchRSS 走 RSS 通道搜索。
func (t *Tokyotosho) searchRSS(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := t.Client.Get(ctx, t.rssURL(opts))
	if err != nil {
		return nil, fmt.Errorf("tokyotosho RSS 请求失败: %w", err)
	}
	return t.parseRSS(body)
}

// parseRSS 解析 RSS 响应并归一化为结果列表。
// RSS 不提供做种与下载数，这两项按契约保持 model.Unknown。
func (t *Tokyotosho) parseRSS(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	var feed tokyoFeed
	if err := xml.Unmarshal(raw, &feed); err != nil {
		return nil, fmt.Errorf("tokyotosho RSS 解析失败: %w", err)
	}
	now := time.Now()
	results := make([]model.Result, 0, len(feed.Items))
	for _, item := range feed.Items {
		res, ok := t.parseRSSItem(item, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// parseRSSItem 解析 RSS 里的一条记录。
func (t *Tokyotosho) parseRSSItem(item tokyoFeedItem, now time.Time) (model.Result, bool) {
	m := rssMagnetRe.FindStringSubmatch(item.Desc)
	if m == nil {
		return model.Result{}, false
	}
	uri := m[1]
	hash, ok := tokyoInfoHash(uri)
	if !ok {
		return model.Result{}, false
	}
	res := model.Result{
		Title:     strings.TrimSpace(item.Title),
		InfoHash:  hash,
		MagnetURI: uri,
		Seeders:   model.Unknown,
		Leechers:  model.Unknown,
		ListedAt:  now,
		Source:    t.Name(),
		Category:  strings.ToLower(strings.TrimSpace(item.Category)),
	}
	if res.Title == "" {
		return model.Result{}, false
	}
	if m := rssSizeRe.FindStringSubmatch(item.Desc); m != nil {
		res.SizeBytes = magnet.ParseSize(m[1])
	}
	if m := rssDetailRe.FindStringSubmatch(item.Desc); m != nil {
		res.URL = m[1]
	}
	if ts, err := time.Parse(time.RFC1123, strings.TrimSpace(item.PubDate)); err == nil {
		res.PublishDate = ts.UTC()
	}
	return res, true
}
