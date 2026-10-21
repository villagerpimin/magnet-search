package server

import (
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/app"
)

// 检索结果缓存的默认参数。
const (
	// cacheTTL 缓存有效期，超时后重新检索
	cacheTTL = 5 * time.Minute
	// cacheMaxEntries 最多保留多少份结果
	cacheMaxEntries = 8
)

// searchCache 保存最近若干次检索的完整排序结果。
// 翻页时直接对缓存结果切片，避免每点一次页码就重跑一遍网络检索。
type searchCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   []string
	ttl     time.Duration
	max     int
}

// cacheEntry 是一份缓存的检索结果。
type cacheEntry struct {
	// outcome 完整排序结果
	outcome *app.Outcome
	// created 写入时间
	created time.Time
}

// newSearchCache 创建缓存。
func newSearchCache() *searchCache {
	return &searchCache{
		entries: make(map[string]*cacheEntry),
		ttl:     cacheTTL,
		max:     cacheMaxEntries,
	}
}

// get 取出未过期的缓存结果。
func (c *searchCache) get(key string) (*app.Outcome, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.created) > c.ttl {
		delete(c.entries, key)
		c.removeOrder(key)
		return nil, false
	}
	return entry.outcome, true
}

// put 写入缓存，超过上限时淘汰最旧的一份。
func (c *searchCache) put(key string, outcome *app.Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok {
		c.order = append(c.order, key)
	}
	c.entries[key] = &cacheEntry{outcome: outcome, created: time.Now()}
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// clear 清空缓存，用于设置变更后丢弃旧结果。
func (c *searchCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
	c.order = nil
}

// removeOrder 从顺序表里移除某个键。
func (c *searchCache) removeOrder(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
