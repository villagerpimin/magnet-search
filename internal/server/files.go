package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/httpx"
	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/torrent"
)

// torrentCacheTTL 是文件列表缓存有效期。
const torrentCacheTTL = 30 * time.Minute

// torrentCacheMax 是最多缓存多少份种子元数据。
const torrentCacheMax = 256

// maxTorrentFileSize 是允许解析的 .torrent 大小上限。
// 正常种子通常只有几十 KB；超过上限的响应不下载解析，避免异常镜像拖垮内存。
const maxTorrentFileSize = 16 << 20

// torrentCache 缓存按 info hash 拉取到的文件列表。
type torrentCache struct {
	mu      sync.Mutex
	entries map[string]*torrentCacheEntry
	order   []string
}

// torrentCacheEntry 是一份缓存的种子元数据。
type torrentCacheEntry struct {
	// meta 文件列表
	meta *torrent.Metainfo
	// created 写入时间
	created time.Time
}

// newTorrentCache 创建文件列表缓存。
func newTorrentCache() *torrentCache {
	return &torrentCache{entries: make(map[string]*torrentCacheEntry)}
}

// get 取出未过期的文件列表。
func (c *torrentCache) get(hash string) (*torrent.Metainfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[hash]
	if !ok {
		return nil, false
	}
	if time.Since(entry.created) > torrentCacheTTL {
		delete(c.entries, hash)
		c.removeOrder(hash)
		return nil, false
	}
	return entry.meta, true
}

// put 写入文件列表，超过上限时淘汰最旧的一份。
func (c *torrentCache) put(hash string, meta *torrent.Metainfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[hash]; !ok {
		c.order = append(c.order, hash)
	}
	c.entries[hash] = &torrentCacheEntry{meta: meta, created: time.Now()}
	for len(c.order) > torrentCacheMax {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// removeOrder 从顺序表里移除某个键。
func (c *torrentCache) removeOrder(hash string) {
	for i, k := range c.order {
		if k == hash {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// handleFiles 按 info hash 拉取 .torrent 并返回文件名与文件列表。
// 公共缓存里没有这份种子时返回 404，界面显示“暂无文件数据”。
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 GET"})
		return
	}
	hash, ok := magnet.NormalizeInfoHash(r.URL.Query().Get("hash"))
	if !ok || magnet.IsNullInfoHash(hash) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "info hash 不正确"})
		return
	}
	if meta, ok := s.torrents.get(hash); ok {
		writeJSON(w, http.StatusOK, meta)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, err := s.fetchTorrent(ctx, hash)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "暂无该磁力的文件数据"})
		return
	}
	meta, err := torrent.Parse(data)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "种子文件解析失败"})
		return
	}
	// 缓存服务偶尔会返回与请求哈希不一致的内容，必须核对后再展示。
	if meta.InfoHash != hash {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "种子文件与 info hash 不匹配"})
		return
	}
	s.torrents.put(hash, meta)
	writeJSON(w, http.StatusOK, meta)
}

// fetchTorrent 从公共种子缓存拉取 .torrent 文件。
// 代理按界面里的设置优先生效，失败后回退直连。
func (s *Server) fetchTorrent(ctx context.Context, hash string) ([]byte, error) {
	s.settingsMu.RLock()
	saved := s.settings
	s.settingsMu.RUnlock()

	chain := splitList(saved.Proxy)
	chain = append(chain, "")
	client, err := httpx.New(httpx.Options{
		Proxies: chain,
		Timeout: 15 * time.Second,
		Retries: 1,
	})
	if err != nil {
		return nil, err
	}

	mirrors := []string{
		"https://itorrents.net/torrent/%s.torrent",
		"https://itorrents.org/torrent/%s.torrent",
	}
	var lastErr error
	for _, base := range mirrors {
		data, err := client.Get(ctx, fmt.Sprintf(base, hash))
		if err == nil && len(data) > 0 {
			if len(data) > maxTorrentFileSize {
				lastErr = fmt.Errorf("种子文件过大: %d 字节", len(data))
				continue
			}
			if raw, err := gunzipTorrent(data); err == nil {
				return raw, nil
			}
			lastErr = fmt.Errorf("种子文件解压失败")
			continue
		}
		lastErr = err
	}
	return nil, fmt.Errorf("获取种子失败: %w", lastErr)
}

// gunzipTorrent 在响应体是 gzip 时解压，并再次限制解压后的大小。
func gunzipTorrent(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, maxTorrentFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxTorrentFileSize {
		return nil, fmt.Errorf("解压后种子文件过大: %d 字节", len(raw))
	}
	return raw, nil
}
