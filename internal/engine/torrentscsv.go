package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// TorrentsCsv 适配 torrents-csv.com 的 JSON 搜索接口。
// 接口直接给 info hash、大小与做种数，不需要再拼详情页。
type TorrentsCsv struct {
	Client  ClientProvider
	BaseURL string
}

// NewTorrentsCsv 构造 torrents-csv 适配器。
func NewTorrentsCsv(c ClientProvider) *TorrentsCsv {
	return &TorrentsCsv{Client: c, BaseURL: "https://torrents-csv.com"}
}

// Name 返回引擎标识。
func (a *TorrentsCsv) Name() string { return "torrentscsv" }

// Description 返回引擎描述。
func (a *TorrentsCsv) Description() string { return "torrents-csv.com 磁力索引（JSON 接口）" }

// Search 执行搜索。
func (a *TorrentsCsv) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := fetchWithRetry(ctx, a.Client, a.searchURL(opts))
	if err != nil {
		return nil, fmt.Errorf("torrentscsv 请求失败: %w", err)
	}
	results, err := a.parse(body)
	if err != nil {
		return nil, err
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterTorrentsCsv 把 torrentscsv 注册到引擎表。
func RegisterTorrentsCsv(reg *Registry) {
	reg.Add(Spec{
		Name:        "torrentscsv",
		Description: "torrents-csv.com 磁力索引（JSON 接口）",
		Region:      RegionAny,
		New:         func(c ClientProvider) Engine { return NewTorrentsCsv(c) },
	})
}

const (
	// torrentsCsvDefaultSize 是接口默认返回条数
	torrentsCsvDefaultSize = 50
	// torrentsCsvMaxSize 是接口允许的单次返回上限
	torrentsCsvMaxSize = 100
)

// searchURL 拼装查询地址。
func (a *TorrentsCsv) searchURL(opts Options) string {
	size := opts.Limit
	if size <= 0 {
		size = torrentsCsvDefaultSize
	}
	if size > torrentsCsvMaxSize {
		size = torrentsCsvMaxSize
	}
	return fmt.Sprintf("%s/service/search?q=%s&size=%d",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
		size,
	)
}

// torrentsCsvResponse 是接口返回的顶层结构。
type torrentsCsvResponse struct {
	Torrents []torrentsCsvItem `json:"torrents"`
}

// torrentsCsvItem 是接口返回的一条记录。
type torrentsCsvItem struct {
	InfoHash    string `json:"infohash"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedUnix int64  `json:"created_unix"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
}

// parse 解析 JSON 响应并归一化为结果列表。
func (a *TorrentsCsv) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	var resp torrentsCsvResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("torrentscsv 响应解析失败: %w", err)
	}
	now := time.Now()
	results := make([]model.Result, 0, len(resp.Torrents))
	for _, item := range resp.Torrents {
		hash, ok := magnet.NormalizeInfoHash(item.InfoHash)
		if !ok || magnet.IsNullInfoHash(hash) {
			continue
		}
		title := strings.TrimSpace(item.Name)
		if title == "" {
			continue
		}
		res := model.Result{
			Title:     title,
			InfoHash:  hash,
			MagnetURI: magnet.Build(hash, title, nil),
			SizeBytes: item.SizeBytes,
			Seeders:   item.Seeders,
			Leechers:  item.Leechers,
			ListedAt:  now,
			Source:    a.Name(),
		}
		if item.CreatedUnix > 0 {
			res.PublishDate = time.Unix(item.CreatedUnix, 0).UTC()
		}
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}
