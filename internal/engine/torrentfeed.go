package engine

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
	"github.com/pigzhu/magnet-search/internal/torrent"
)

// maxTorrentFeedItems 是每次检索最多为 RSS 条目下载多少份 .torrent。
// 这类站点只给 .torrent 地址、不给 info hash，必须下载后才能拼磁力，
// 因此要限制数量，避免一次检索打出几十个额外请求。
const maxTorrentFeedItems = 24

// torrentFeedWorkers 是下载种子文件的并发数。
const torrentFeedWorkers = 4

// torrentFeedItem 是 RSS 里一条待解析种子的记录。
type torrentFeedItem struct {
	// Title 标题
	Title string
	// Link 详情页地址
	Link string
	// TorrentURL .torrent 下载地址
	TorrentURL string
	// SizeBytes RSS 给出的大小，0 表示未知
	SizeBytes int64
	// PublishDate 发布时间
	PublishDate time.Time
}

// resolveTorrentFeed 并发下载 .torrent、解析 info hash，并构建结果列表。
// 解析失败的条目直接跳过，返回顺序与输入顺序一致。
func resolveTorrentFeed(ctx context.Context, client ClientProvider, items []torrentFeedItem, source, category string, limit int) []model.Result {
	if limit <= 0 || limit > maxTorrentFeedItems {
		limit = maxTorrentFeedItems
	}
	if len(items) > limit {
		items = items[:limit]
	}
	if len(items) == 0 {
		return nil
	}
	// 每个引擎使用独立的 HTTP 客户端，放宽同 host 间隔，避免串行下载拖慢检索。
	if setter, ok := client.(interface{ SetMinGap(time.Duration) }); ok {
		setter.SetMinGap(20 * time.Millisecond)
	}

	type outcome struct {
		result model.Result
		ok     bool
	}
	out := make([]outcome, len(items))
	sem := make(chan struct{}, torrentFeedWorkers)
	var wg sync.WaitGroup

	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			data, err := client.Get(ctx, items[i].TorrentURL)
			if err != nil {
				return
			}
			data, err = decodeBody(data)
			if err != nil {
				return
			}
			meta, err := torrent.Parse(data)
			if err != nil {
				return
			}
			title := strings.TrimSpace(items[i].Title)
			if title == "" {
				title = meta.Name
			}
			size := items[i].SizeBytes
			if size <= 0 {
				size = meta.TotalBytes
			}
			out[i] = outcome{
				result: model.Result{
					Title:       title,
					InfoHash:    meta.InfoHash,
					MagnetURI:   magnet.Build(meta.InfoHash, title, meta.Trackers),
					SizeBytes:   size,
					Seeders:     model.Unknown,
					Leechers:    model.Unknown,
					PublishDate: items[i].PublishDate,
					ListedAt:    time.Now(),
					Source:      source,
					URL:         items[i].Link,
					Category:    category,
				},
				ok: true,
			}
		}(i)
	}
	wg.Wait()

	results := make([]model.Result, 0, len(items))
	for i := range out {
		if !out[i].ok {
			continue
		}
		res := out[i].result
		res.Rank = len(results)
		results = append(results, res)
	}
	return results
}
