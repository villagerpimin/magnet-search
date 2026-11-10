package engine

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/pigzhu/magnet-search/internal/model"
)

// Sukebei 适配 sukebei.nyaa.si：nyaa 的成人分站，番号与成人资源的磁力都从这里出。
//
// 站点把番号、片名和字幕标记直接写在标题里，RSS 通道又带 info hash、分类、
// 做种数与大小，因此不需要再解析详情页，一次请求就能拿到完整结果。
type Sukebei struct {
	Client  ClientProvider
	BaseURL string
}

// NewSukebei 构造 sukebei 适配器。
func NewSukebei(c ClientProvider) *Sukebei {
	return &Sukebei{Client: c, BaseURL: "https://sukebei.nyaa.si"}
}

// Name 返回引擎标识。
func (a *Sukebei) Name() string { return "sukebei" }

// Description 返回引擎描述。
func (a *Sukebei) Description() string { return "sukebei.nyaa.si 成人 / 番号磁力（RSS）" }

// Search 执行搜索。
func (a *Sukebei) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	body, err := fetchWithRetry(ctx, a.Client, a.rssURL(opts))
	if err != nil {
		return nil, fmt.Errorf("sukebei RSS 请求失败: %w", err)
	}
	results, err := a.parseRSS(body)
	if err != nil {
		return nil, err
	}
	return limitResults(results, opts.Limit), nil
}

// RegisterSukebei 把 sukebei 注册到引擎表。
func RegisterSukebei(reg *Registry) {
	reg.Add(Spec{
		Name:        "sukebei",
		Description: "sukebei.nyaa.si 成人 / 番号磁力（RSS）",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewSukebei(c) },
	})
}

// rssURL 拼装 RSS 通道的查询地址。
//
// 分类固定 0_0：分站的所有分类本来就是成人向，按分类过滤只会把结果切窄。
func (a *Sukebei) rssURL(opts Options) string {
	return fmt.Sprintf("%s/?f=0&c=0_0&q=%s&page=rss",
		strings.TrimRight(a.BaseURL, "/"),
		url.QueryEscape(opts.Query),
	)
}

// parseRSS 解析 RSS 响应。
//
// 分站的分类编号与主站含义不同（2_x 是真人视频而不是音频），
// 因此不套用 nyaa 的分类名兜底表：RSS 里没给分类名时宁可留空。
func (a *Sukebei) parseRSS(body []byte) ([]model.Result, error) {
	return nyaaRSSResults(body, a.Name(), nil)
}
