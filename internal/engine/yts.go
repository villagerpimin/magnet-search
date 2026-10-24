package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// Yts 适配 yts 的 JSON 接口（yts.rs / yts.gg）。
//
// 接口只给 .torrent 下载地址与 info hash，不给磁力链接，
// 因此这里用 info hash 自己拼磁力链接。
type Yts struct {
	Client  ClientProvider
	BaseURL string
}

// NewYts 构造 yts 适配器。
func NewYts(c ClientProvider) *Yts {
	return &Yts{Client: c, BaseURL: "https://yts.rs"}
}

// Name 返回引擎标识。
func (a *Yts) Name() string { return "yts" }

// Description 返回引擎描述。
func (a *Yts) Description() string { return "YTS 电影种子搜索（JSON 接口）" }

// Search 执行搜索，主域名失败时依次回退到镜像站点。
func (a *Yts) Search(ctx context.Context, opts Options) ([]model.Result, error) {
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
	return nil, fmt.Errorf("yts 请求失败: %w", lastErr)
}

// RegisterYts 把 yts 注册到引擎表。
func RegisterYts(reg *Registry) {
	reg.Add(Spec{
		Name:        "yts",
		Description: "YTS 电影种子搜索（JSON 接口）",
		Region:      RegionOverseas,
		New:         func(c ClientProvider) Engine { return NewYts(c) },
	})
}

const (
	// ytsDefaultLimit 是接口默认拉取的电影数量。
	ytsDefaultLimit = 20
	// ytsMaxAPILimit 是接口允许的单次电影数量上限。
	ytsMaxAPILimit = 50
)

// ytsMirrors 是主域名不可用时的备用域名。
// 官方品牌已迁到 yts.gg，yts.mx 在部分网络下不可达，因此把三者都列为候选。
var ytsMirrors = []string{"https://yts.gg", "https://yts.am"}

// bases 返回依次尝试的站点地址，主地址在前、镜像在后。
func (a *Yts) bases() []string {
	primary := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if primary == "" {
		primary = "https://yts.rs"
	}
	out := []string{primary}
	for _, m := range ytsMirrors {
		if m != primary {
			out = append(out, m)
		}
	}
	return out
}

// searchURL 拼装查询地址。
// 接口的 limit 数的是电影，而结果条数是种子，
// 因此这里按电影数请求，最后再按 Options.Limit 截断种子。
func (a *Yts) searchURL(base string, opts Options) string {
	limit := opts.Limit
	if limit <= 0 {
		limit = ytsDefaultLimit
	}
	if limit > ytsMaxAPILimit {
		limit = ytsMaxAPILimit
	}
	return fmt.Sprintf("%s/api/v2/list_movies.json?query_term=%s&limit=%d",
		strings.TrimRight(base, "/"),
		url.QueryEscape(opts.Query),
		limit,
	)
}

// ytsResponse 是接口返回的顶层结构。
type ytsResponse struct {
	Data struct {
		Movies []ytsMovie `json:"movies"`
	} `json:"data"`
}

// ytsMovie 是一部电影。
type ytsMovie struct {
	ID        int          `json:"id"`
	Title     string       `json:"title"`
	TitleLong string       `json:"title_long"`
	Year      int          `json:"year"`
	URL       string       `json:"url"`
	IMDBCode  string       `json:"imdb_code"`
	Slug      string       `json:"slug"`
	Torrents  []ytsTorrent `json:"torrents"`
}

// ytsTorrent 是一个种子。
// 人数与大小用指针，是为了区分"字段缺失"与"真的是 0"。
type ytsTorrent struct {
	URL              string `json:"url"`
	Hash             string `json:"hash"`
	Quality          string `json:"quality"`
	Type             string `json:"type"`
	Seeds            *int   `json:"seeds"`
	Peers            *int   `json:"peers"`
	Size             string `json:"size"`
	SizeBytes        *int64 `json:"size_bytes"`
	DateUploaded     string `json:"date_uploaded"`
	DateUploadedUnix *int64 `json:"date_uploaded_unix"`
}

// ytsEntry 是一条待输出的结果，保留电影与种子的对应关系。
type ytsEntry struct {
	movie   ytsMovie
	torrent ytsTorrent
}

// parse 解析 JSON 响应并归一化为结果列表。
func (a *Yts) parse(body []byte) ([]model.Result, error) {
	raw, err := decodeBody(body)
	if err != nil {
		return nil, err
	}
	var resp ytsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("yts 响应解析失败: %w", err)
	}
	now := time.Now()
	entries := ytsInterleave(resp.Data.Movies)
	results := make([]model.Result, 0, len(entries))
	for _, entry := range entries {
		res, ok := a.result(entry, now)
		if !ok {
			continue
		}
		// Rank 记录引擎自己给出的先后顺序，是独立于文本匹配的相关度信号
		res.Rank = len(results)
		results = append(results, res)
	}
	return results, nil
}

// ytsInterleave 把各部电影的种子按轮次交错输出：
// 先给每部电影最好的画质，再给次好的，
// 免得结果前几条全来自同一部电影。
func ytsInterleave(movies []ytsMovie) []ytsEntry {
	var out []ytsEntry
	for round := 0; ; round++ {
		found := false
		for _, movie := range movies {
			if round < len(movie.Torrents) {
				out = append(out, ytsEntry{movie: movie, torrent: movie.Torrents[round]})
				found = true
			}
		}
		if !found {
			return out
		}
	}
}

// result 把一个种子转成结果。
func (a *Yts) result(entry ytsEntry, now time.Time) (model.Result, bool) {
	hash, ok := magnet.NormalizeInfoHash(entry.torrent.Hash)
	if !ok {
		return model.Result{}, false
	}
	title := ytsTitle(entry.movie, entry.torrent)
	if title == "" {
		return model.Result{}, false
	}
	res := model.Result{
		Title:     title,
		InfoHash:  hash,
		MagnetURI: magnet.Build(hash, title, nil),
		Seeders:   ytsCount(entry.torrent.Seeds),
		Leechers:  ytsCount(entry.torrent.Peers),
		ListedAt:  now,
		Source:    a.Name(),
		Category:  "movie",
		URL:       strings.TrimSpace(entry.movie.URL),
	}
	if entry.torrent.SizeBytes != nil {
		res.SizeBytes = *entry.torrent.SizeBytes
	} else if n := magnet.ParseSize(entry.torrent.Size); n > 0 {
		res.SizeBytes = n
	}
	if entry.torrent.DateUploadedUnix != nil && *entry.torrent.DateUploadedUnix > 0 {
		res.PublishDate = time.Unix(*entry.torrent.DateUploadedUnix, 0).UTC()
	}
	return res, true
}

// ytsTitle 拼出结果标题：电影名 + 年份 + 画质 + 来源类型。
func ytsTitle(movie ytsMovie, torrent ytsTorrent) string {
	name := strings.TrimSpace(movie.Title)
	if name == "" {
		name = strings.TrimSpace(movie.TitleLong)
	}
	parts := []string{name}
	if movie.Year > 0 {
		parts = append(parts, strconv.Itoa(movie.Year))
	}
	if q := strings.TrimSpace(torrent.Quality); q != "" {
		parts = append(parts, q)
	}
	if typ := strings.TrimSpace(torrent.Type); typ != "" {
		parts = append(parts, typ)
	}
	return strings.Join(parts, " ")
}

// ytsCount 取出人数，字段缺失时返回 model.Unknown。
func ytsCount(v *int) int {
	if v == nil {
		return model.Unknown
	}
	return *v
}
