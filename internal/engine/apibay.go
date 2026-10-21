package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Apibay 适配 The Pirate Bay 的 JSON API。
type Apibay struct {
	Client  ClientProvider
	BaseURL string
}

// NewApibay 构造 apibay 适配器。
func NewApibay(c ClientProvider) *Apibay {
	return &Apibay{Client: c, BaseURL: "https://apibay.org"}
}

// Name 返回引擎标识。
func (a *Apibay) Name() string { return "apibay" }

// Description 返回引擎描述。
func (a *Apibay) Description() string { return "The Pirate Bay 官方 JSON API" }

// Search 执行搜索。
func (a *Apibay) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := a.Client.Get(ctx, a.searchURL(opts))
	if err != nil {
		return nil, fmt.Errorf("apibay 请求失败: %w", err)
	}
	results, err := a.parse(body)
	if err != nil {
		return nil, err
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterApibay 把 apibay 注册到引擎表。
func RegisterApibay(reg *Registry) {
	reg.Add(Spec{
		Name:        "apibay",
		Description: "The Pirate Bay 官方 JSON API",
		Region:      RegionAny,
		New:         func(c ClientProvider) Engine { return NewApibay(c) },
	})
}

// searchURL 拼装查询地址；cat=0 表示全部分类。
func (a *Apibay) searchURL(opts Options) string {
	return fmt.Sprintf("%s/q.php?q=%s&cat=%d",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
		apibayCategoryCode(opts.Category),
	)
}

// apibayCategoryCode 把聚合层的分类名映射为 The Pirate Bay 的分类码，0 代表全部。
func apibayCategoryCode(category string) int {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "video":
		return 200
	case "audio":
		return 100
	default:
		return 0
	}
}

// apibayCategoryLabel 把接口返回的分类码映射为可读的一级分类名。
func apibayCategoryLabel(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	switch code[0] {
	case '1':
		return "audio"
	case '2':
		return "video"
	case '3':
		return "application"
	case '4':
		return "game"
	case '5':
		return "porn"
	case '6':
		return "other"
	}
	return ""
}

// apibayItem 是接口返回的单条记录，所有数值字段都是字符串。
type apibayItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	InfoHash string `json:"info_hash"`
	Leechers string `json:"leechers"`
	Seeders  string `json:"seeders"`
	Size     string `json:"size"`
	Added    string `json:"added"`
	Category string `json:"category"`
}

// parse 解析 JSON 响应并归一化为结果列表。
func (a *Apibay) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	var items []apibayItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("apibay 响应解析失败: %w", err)
	}
	now := time.Now()
	results := make([]model.Result, 0, len(items))
	for _, it := range items {
		hash, ok := magnet.NormalizeInfoHash(it.InfoHash)
		if !ok || magnet.IsNullInfoHash(hash) {
			// info hash 非法、缺失或是零值哈希的记录直接丢弃。
			// 零值哈希是接口在“没有匹配结果”时返回的占位符（标题写作
			// “No results returned”），当成真实资源会污染搜索结果。
			continue
		}
		// 接口对标题做了 HTML 转义，需要还原成原始文本
		title := html.UnescapeString(it.Name)
		res := model.Result{
			Title:     title,
			InfoHash:  hash,
			MagnetURI: magnet.Build(hash, title, nil),
			SizeBytes: apibayInt64(it.Size),
			Seeders:   apibayInt(it.Seeders),
			Leechers:  apibayInt(it.Leechers),
			ListedAt:  now,
			Source:    a.Name(),
			Category:  apibayCategoryLabel(it.Category),
		}
		if added := apibayInt64(it.Added); added > 0 {
			res.PublishDate = time.Unix(added, 0).UTC()
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		// 接口只给 id，不给详情页地址，而 apibay.org 本身只提供 JSON 接口，
		// 因此这里不拼造一个会 404 的详情页链接。
		results = append(results, res)
	}
	return results, nil
}

// apibayInt 解析字符串型数值，缺失或非法时返回 model.Unknown。
func apibayInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return model.Unknown
	}
	return n
}

// apibayInt64 解析字符串型数值，缺失或非法时返回 0。
func apibayInt64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
