package engine

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Bt4g 适配 bt4gprx.com 的 RSS 搜索接口。
//
// 站点的 HTML 搜索页常年被 Cloudflare 的 JS 挑战拦住（返回 403），
// 但 RSS 端点不在挑战范围内，普通 HTTP 客户端可以直接拿到磁力列表。
type Bt4g struct {
	Client  ClientProvider
	BaseURL string
}

// NewBt4g 构造 bt4g 适配器。
func NewBt4g(c ClientProvider) *Bt4g {
	return &Bt4g{Client: c, BaseURL: "https://bt4gprx.com"}
}

// Name 返回引擎标识。
func (a *Bt4g) Name() string { return "bt4g" }

// Description 返回引擎描述。
func (a *Bt4g) Description() string { return "BT4G 磁力搜索（RSS 接口，绕过 Cloudflare）" }

// Search 执行搜索，主域名失败时依次回退到镜像站点。
func (a *Bt4g) Search(ctx context.Context, opts Options) ([]model.Result, error) {
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
	return nil, fmt.Errorf("bt4g 请求失败: %w", lastErr)
}

// RegisterBt4g 把 bt4g 注册到引擎表。
func RegisterBt4g(reg *Registry) {
	reg.Add(Spec{
		Name:        "bt4g",
		Description: "BT4G 磁力搜索（RSS 接口，绕过 Cloudflare）",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewBt4g(c) },
	})
}

// bt4gMirrors 是主域名不可用时的备用域名。
var bt4gMirrors = []string{"https://bt4g.link", "https://bt4g.org"}

// bases 返回依次尝试的站点地址，主地址在前、镜像在后。
func (a *Bt4g) bases() []string {
	primary := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if primary == "" {
		primary = "https://bt4gprx.com"
	}
	out := []string{primary}
	for _, m := range bt4gMirrors {
		if m != primary {
			out = append(out, m)
		}
	}
	return out
}

// searchURL 拼装 RSS 通道的查询地址。
//
// orderby=seeders 是有意为之：RSS 里没有做种与下载数，
// 只有让站点自己按做种数排序，才能把仍然活跃的资源留在列表前面。
func (a *Bt4g) searchURL(base string, opts Options) string {
	u := fmt.Sprintf("%s/search?q=%s&page=rss&orderby=seeders",
		strings.TrimRight(base, "/"),
		url.QueryEscape(opts.Query),
	)
	if cat := bt4gCategory(opts.Category); cat != "" {
		u += "&category=" + cat
	}
	return u
}

// bt4gCategory 把聚合层的分类名映射为站点支持的分类参数。
// 站点只认 all|audio|movie|doc|app|other 这几个值，认不出的分类就不加参数。
func bt4gCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "video", "movie":
		return "movie"
	case "audio", "music":
		return "audio"
	case "software", "app", "apps":
		return "app"
	case "book", "books", "doc", "documentary":
		return "doc"
	case "other":
		return "other"
	default:
		return ""
	}
}

// bt4gFeed 是站点 RSS 的最小结构。
// item 元素在 channel 下面，因此要用 channel>item 的路径写法。
type bt4gFeed struct {
	Items []bt4gItem `xml:"channel>item"`
}

// bt4gItem 是 RSS 里的一条记录。
type bt4gItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

// parse 解析 RSS 响应并归一化为结果列表。
func (a *Bt4g) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	var feed bt4gFeed
	if err := xml.Unmarshal(raw, &feed); err != nil {
		return nil, fmt.Errorf("bt4g RSS 解析失败: %w", err)
	}
	now := time.Now()
	results := make([]model.Result, 0, len(feed.Items))
	for _, item := range feed.Items {
		res, ok := a.parseItem(item, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// parseItem 解析 RSS 里的一条记录。
//
// description 里按 <br> 切开依次是：标题、大小、分类、info hash。
// RSS 不提供做种与下载数，这两项按契约保持 model.Unknown。
func (a *Bt4g) parseItem(item bt4gItem, now time.Time) (model.Result, bool) {
	title := strings.TrimSpace(item.Title)
	uri := strings.TrimSpace(item.Link)
	if title == "" || !strings.HasPrefix(strings.ToLower(uri), "magnet:") {
		return model.Result{}, false
	}
	fields := strings.Split(item.Description, "<br>")
	hash, hashOK := magnet.InfoHashFromMagnet(uri)
	if len(fields) > 3 {
		// 描述里给的哈希优先，取不到时用磁力链接里的兜底
		if h, ok := magnet.NormalizeInfoHash(strings.TrimSpace(fields[3])); ok {
			hash, hashOK = h, true
		}
	}
	if !hashOK {
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
	}
	if len(fields) > 1 {
		res.SizeBytes = magnet.ParseSize(strings.TrimSpace(fields[1]))
	}
	if len(fields) > 2 {
		res.Category = strings.TrimSpace(fields[2])
	}
	if ts, ok := parseFeedTime(item.PubDate); ok {
		res.PublishDate = ts
	}
	if guid := strings.TrimSpace(item.GUID); strings.HasPrefix(guid, "http") {
		res.URL = guid
	}
	return res, true
}
