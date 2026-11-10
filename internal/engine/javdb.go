package engine

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// JavDB 适配 javdb.com：检索页按番号/片名/演员/标签找条目，再进详情页取磁力。
//
// javdb.com 全站挂在 Cloudflare 的 JS 挑战后面，普通 HTTP 客户端只会拿到
// 「Just a moment...」，因此页面统一交给 r.jina.ai 用自己的浏览器渲染成
// Markdown 再解析：检索页给番号、片名与详情页地址，详情页给该片的全部磁力。
// 查询里带番号时只保留同番号条目，否则按站点自己的相关度取前几条。
type JavDB struct {
	Client  ClientProvider
	BaseURL string // JavDB 站点地址
	Reader  string // 页面渲染服务，拼在目标地址前面

	mu       sync.Mutex
	inflight map[string]*javdbCall
}

const (
	// javdbReaderDefault 是默认的页面渲染入口
	javdbReaderDefault = "https://r.jina.ai/"
	// javdbMaxDetails 是每次检索最多解析多少条详情页：
	// 每篇详情页都要额外一次渲染请求，取太多会把整体检索拖慢
	javdbMaxDetails = 3
	// javdbDetailWorkers 是详情页的并发抓取数
	javdbDetailWorkers = 3
	// javdbTitleMaxDetails 是片名/演员检索时最多取几条详情页
	javdbTitleMaxDetails = 2
	// javdbCategory 是结果分类，聚合层里与普通影视区分开
	javdbCategory = "Adult Video"
)

// NewJavDB 构造 JavDB 适配器。
func NewJavDB(c ClientProvider) *JavDB {
	return &JavDB{Client: c, BaseURL: "https://javdb.com", Reader: javdbReaderDefault}
}

// Name 返回引擎标识。
func (a *JavDB) Name() string { return "javdb" }

// Description 返回引擎描述。
func (a *JavDB) Description() string {
	return "JavDB 番号 / 片名检索（经 r.jina.ai 渲染，详情页直接给磁力）"
}

// javdbCall 是一次进行中的检索，同一个检索词的并发调用共享它。
type javdbCall struct {
	// done 在检索结束时关闭
	done chan struct{}
	// results 检索结果
	results []model.Result
	// err 失败原因
	err error
}

// Search 执行搜索。
//
// 查询不为空就查 JavDB：检索页本身支持番号、片名、演员与标签。
// 查询里带番号时按番号严格过滤；否则按 JavDB 自己的相关度取前几条详情页。
// 一次检索要经渲染服务取 1 个检索页加最多 3 个详情页，因此详情页数量做了上限，
// 拆词检索拆出的同一个番号也会合并在途请求，只让第一个调用真正去取页面。
// 拆词变体不查：JavDB 自己有索引，碎片词（例如 ABCD-999 拆出的 999）
// 只会带回无关资源，还要多耗一次渲染额度。
func (a *JavDB) Search(ctx context.Context, opts Options) ([]model.Result, error) {
	if opts.Variant {
		return nil, nil
	}
	term, code, ok := javdbQuery(opts)
	if !ok {
		return nil, nil
	}
	// 带番号时按紧凑番号合并（SSIS-001 与 ssis001 是同一个），
	// 否则按检索词合并，避免拆词变体重复打渲染服务
	key := "t:" + term
	if code != "" {
		key = "c:" + code
	}
	call, leader := a.joinCall(key)
	if !leader {
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if call.err != nil {
			return nil, call.err
		}
		return limitResults(call.results, opts.Limit), nil
	}
	results, err := a.searchTerm(ctx, term, code)
	a.finishCall(key, results, err)
	if err != nil {
		return nil, err
	}
	return limitResults(results, opts.Limit), nil
}

// joinCall 加入某个番号的进行中请求；leader 为 true 表示由自己真正去取页面。
func (a *JavDB) joinCall(key string) (call *javdbCall, leader bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight == nil {
		a.inflight = make(map[string]*javdbCall)
	}
	if existing, ok := a.inflight[key]; ok {
		return existing, false
	}
	call = &javdbCall{done: make(chan struct{})}
	a.inflight[key] = call
	return call, true
}

// finishCall 结束一次检索并把结果交给等待者。
func (a *JavDB) finishCall(key string, results []model.Result, err error) {
	a.mu.Lock()
	call := a.inflight[key]
	delete(a.inflight, key)
	a.mu.Unlock()
	if call == nil {
		return
	}
	call.results = results
	call.err = err
	close(call.done)
}

// searchTerm 取检索页，再逐条进详情页取磁力。
func (a *JavDB) searchTerm(ctx context.Context, term, code string) ([]model.Result, error) {
	searchBody, err := a.fetch(ctx, a.searchURL(term))
	if err != nil {
		return nil, fmt.Errorf("javdb 检索页请求失败: %w", err)
	}
	entries := a.parseSearch(searchBody)
	if len(entries) == 0 {
		// 站点确实没这条记录时会给出固定提示，这种属于"查无结果"，不算失败；
		// 其余情况更可能是渲染服务被限流或页面结构变了，必须报出来，
		// 否则自检会把"没取到页面"误判成可用。
		if strings.Contains(string(searchBody), "No content yet") {
			return nil, nil
		}
		return nil, fmt.Errorf("javdb 检索页没有解析到条目（渲染服务可能被限流或页面结构变了）")
	}
	if code != "" {
		entries = filterJavDBEntries(entries, code)
		if len(entries) == 0 {
			// 站里有检索结果但没有这个番号：查无结果，不当作失败
			return nil, nil
		}
	}
	limit := javdbMaxDetails
	if code == "" {
		// 片名/演员检索按 JavDB 的相关度取前两条，避免一次打出太多详情页
		limit = javdbTitleMaxDetails
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}

	now := time.Now()
	outcomes := make([][]model.Result, len(entries))
	sem := make(chan struct{}, javdbDetailWorkers)
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			// 单条详情页失败不拖垮整次检索，其余条目照常解析
			body, err := a.fetch(ctx, entries[i].URL)
			if err != nil {
				return
			}
			batch := a.parseDetail(body, entries[i], now)
			if code == "" {
				// 片名/演员检索靠的是 JavDB 自己的索引，中文演员名未必出现在日文片名里。
				// 标出来让聚合层改用引擎排序判断相关性，否则会被字面匹配整批丢掉。
				for j := range batch {
					batch[j].IndexMatched = true
				}
			}
			outcomes[i] = batch
		}(i)
	}
	wg.Wait()

	var results []model.Result
	seen := make(map[string]bool)
	for _, batch := range outcomes {
		for _, res := range batch {
			if seen[res.InfoHash] {
				continue
			}
			seen[res.InfoHash] = true
			res.Rank = len(results)
			results = append(results, res)
		}
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("javdb 详情页没有解析到磁力（渲染服务可能被限流或页面结构变了）")
	}
	return results, nil
}

// RegisterJavDB 把 JavDB 注册到引擎表。
func RegisterJavDB(reg *Registry) {
	reg.Add(Spec{
		Name:        "javdb",
		Description: "JavDB 番号 / 片名检索（经 r.jina.ai 渲染，详情页直接给磁力）",
		Region:      RegionOverseas,
		ProbeQuery:  "SSIS-001",
		New:         func(c ClientProvider) Engine { return NewJavDB(c) },
	})
}

// fetch 经渲染服务取页面，返回 Markdown 正文。
func (a *JavDB) fetch(ctx context.Context, target string) ([]byte, error) {
	reader := strings.TrimSpace(a.Reader)
	if reader == "" {
		reader = javdbReaderDefault
	}
	return fetchWithRetry(ctx, a.Client, strings.TrimRight(reader, "/")+"/"+target)
}

// searchURL 拼装检索页地址。
func (a *JavDB) searchURL(term string) string {
	return strings.TrimRight(a.BaseURL, "/") + "/search?f=all&q=" + url.QueryEscape(term)
}

// javdbEntry 是检索页里的一个影片条目。
type javdbEntry struct {
	// Code 番号，例如 SSIS-001
	Code string
	// Title 片名
	Title string
	// URL 详情页地址
	URL string
}

// javdbQuery 拆出这次检索用的检索词与可选的番号。
// 查询不为空就走 JavDB：检索页支持番号、片名、演员与标签，番号只是其中一种写法。
// 番号只决定「按番号严格过滤」还是「按 JavDB 相关度取前几条」，不决定要不要检索。
func javdbQuery(opts Options) (term, code string, ok bool) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return "", "", false
	}
	if raw, compact, found := javdbCodeFromQuery(query); found {
		// 带番号时直接用番号检索，标题里夹带的其它词交给聚合层做相关性排序
		return raw, compact, true
	}
	return query, "", true
}

// javdbCodeFromQuery 从查询里提取番号。
// 这是一条启发式规则：识别错了只会换个过滤方式，不会导致漏查。
func javdbCodeFromQuery(query string) (raw, code string, ok bool) {
	for _, m := range javdbCodeToken.FindAllStringSubmatch(query, -1) {
		compact := strings.ToUpper(m[1] + m[2] + m[3] + m[4])
		if javdbCodeStopWords[compact] {
			continue
		}
		if !javdbCodeCandidate(m[0], m[1], m[2]) {
			continue
		}
		return strings.TrimSpace(m[0]), compact, true
	}
	return "", "", false
}

// javdbCodeCandidate 过滤掉长得像番号、实际是普通词的情况。
// "ubuntu 24" 这种「小写单词 + 两位数」不算；BD1080、h264 这类由停止词表挡掉。
func javdbCodeCandidate(raw, letters, digits string) bool {
	if len(digits) >= 3 {
		return true
	}
	if strings.ContainsAny(raw, "-_") {
		return true
	}
	return letters == strings.ToUpper(letters)
}

// javdbCompactCode 去掉番号里的分隔符并统一大写，例如 ssis-001 → SSIS001。
func javdbCompactCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(s)))
}

var (
	// javdbCodeToken 从查询里抓番号片段：
	// 字母 + 可选分隔符 + 数字，再允许一段「-数字」（T28-001 这类）与字母后缀（-C、-UC）
	javdbCodeToken = regexp.MustCompile(`(?i)\b([A-Z]{1,8})[-_ ]?(\d{2,5})(?:[-_]?(\d{2,4}))?(?:[-_]?([A-Z]{1,3}))?\b`)
	// javdbCodeStopWords 是像番号但其实是分辨率/编码的词，命中就不发请求
	javdbCodeStopWords = map[string]bool{
		"BD1080": true, "BD720": true, "BD2160": true, "FHD1080": true,
		"UHD2160": true, "HD720": true, "HD1080": true, "SD480": true,
		"WEB1080": true, "WEB720": true, "UTF8": true, "UTF16": true,
		"H264": true, "H265": true, "X264": true, "X265": true, "AV1": true,
		"RTX3090": true, "RTX4090": true, "GTX1080": true, "COVID19": true,
	}
	// javdbDetailLink 匹配检索页里的详情页链接；不写死域名，BaseURL 换成镜像也能用
	javdbDetailLink = regexp.MustCompile(`\]\((https?://[^/\s)]+/v/[A-Za-z0-9]+)(?:\s+"([^"]*)")?\)`)
	// javdbCodeMark 匹配检索页里加粗的番号
	javdbCodeMark = regexp.MustCompile(`\*\*([A-Za-z0-9]+(?:-[A-Za-z0-9]+)?)\*\*`)
	// javdbMagnetLine 匹配详情页里的一条磁力：链接文字是文件名与大小，
	// 磁力之后可能还跟着 "Copy Link" 之类的标题属性，一并吃掉。
	// 链接文字里可能出现一层方括号（例如「[SSIS-001] Tsukasa ...」），因此单独放宽。
	javdbMagnetLine = regexp.MustCompile(`\[((?:[^\[\]\n]|\[[^\]\n]*\])+)\]\((magnet:\?xt=urn:btih:[0-9a-fA-F]{40}[^)\s]*)[^)\n]*\)`)
	// javdbDateLine 匹配磁力下面单独一行的发布日期
	javdbDateLine = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// javdbFileCount 匹配链接文字结尾的「, 1 file(s)」
	javdbFileCount = regexp.MustCompile(`(?i),?\s*\d+\s*file\(s\)`)
	// javdbSizeText 匹配链接文字里的大小，例如 6.33GB
	javdbSizeText = regexp.MustCompile(`(?i)\d+(?:\.\d+)?\s*(?:TB|GB|MB|KB)`)
)

// parseSearch 解析检索页 Markdown，按页面顺序返回影片条目。
// 封面图与标题可能各给一个指向同一详情页的链接，这里合并成一个条目并补齐字段。
func (a *JavDB) parseSearch(body []byte) []javdbEntry {
	text := string(body)
	matches := javdbDetailLink.FindAllStringSubmatchIndex(text, -1)
	entries := make([]javdbEntry, 0, len(matches))
	index := make(map[string]int, len(matches))
	for _, m := range matches {
		link := text[m[2]:m[3]]
		title := ""
		if m[4] >= 0 {
			title = strings.TrimSpace(text[m[4]:m[5]])
		}
		// 番号只写在同一行的链接文字里，所以按行截取后再匹配
		line := javdbLineAt(text, m[0], m[1])
		code := ""
		if cm := javdbCodeMark.FindStringSubmatch(line); cm != nil {
			code = strings.ToUpper(strings.TrimSpace(cm[1]))
		}
		if i, ok := index[link]; ok {
			if entries[i].Title == "" {
				entries[i].Title = title
			}
			if entries[i].Code == "" {
				entries[i].Code = code
			}
			continue
		}
		index[link] = len(entries)
		entries = append(entries, javdbEntry{Code: code, Title: title, URL: link})
	}
	return entries
}

// javdbLineAt 返回 text 中覆盖 [start,end) 的那一行。
func javdbLineAt(text string, start, end int) string {
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	lineEnd := len(text)
	if i := strings.IndexByte(text[end:], '\n'); i >= 0 {
		lineEnd = end + i
	}
	return text[lineStart:lineEnd]
}

// filterJavDBEntries 只留下番号与查询一致的条目。
// 检索页会顺带列出同系列的其它番号，不过滤的话会把无关影片的磁力混进来。
func filterJavDBEntries(entries []javdbEntry, code string) []javdbEntry {
	out := make([]javdbEntry, 0, len(entries))
	allCodesEmpty := true
	for _, e := range entries {
		if e.Code == "" {
			continue
		}
		allCodesEmpty = false
		entryCode := javdbCompactCode(e.Code)
		// 两个方向都算匹配：查 SSIS-001 要收 SSIS-001-UC，查 SSIS-001-UC 也要收 SSIS-001
		if entryCode == code || strings.HasPrefix(entryCode, code) || strings.HasPrefix(code, entryCode) {
			out = append(out, e)
		}
	}
	if len(out) == 0 && allCodesEmpty {
		// 页面根本没标番号（站点改版）时退回按相关度取前几条，总比一条不出好
		return entries
	}
	return out
}

// parseDetail 解析详情页 Markdown，取出磁力列表。
func (a *JavDB) parseDetail(body []byte, entry javdbEntry, now time.Time) []model.Result {
	lines := strings.Split(string(body), "\n")
	results := make([]model.Result, 0, len(lines))
	for i, line := range lines {
		m := javdbMagnetLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		label := strings.TrimSpace(m[1])
		uri := strings.TrimSpace(m[2])
		hash, ok := magnet.InfoHashFromMagnet(uri)
		if !ok {
			continue
		}
		tag := strings.TrimSpace(javdbFileCount.ReplaceAllString(label, ""))
		title := tag
		if movie := strings.TrimSpace(entry.Title); movie != "" {
			// 带上检索页给的片名：JavDB 的相关性来自片名/演员，
			// 只留磁力文件名的话，片名检索的结果会被聚合层判成不相关
			title = strings.TrimSpace(movie + " " + tag)
		}
		// 磁力自己带番号的不再补，否则前面加上，便于在结果里认片
		if entry.Code != "" && !strings.Contains(javdbCompactCode(title), javdbCompactCode(entry.Code)) {
			title = strings.TrimSpace(entry.Code + " " + title)
		}
		res := model.Result{
			Title:     title,
			InfoHash:  hash,
			MagnetURI: uri,
			SizeBytes: javdbParseSize(label),
			Seeders:   model.Unknown,
			Leechers:  model.Unknown,
			ListedAt:  now,
			Source:    a.Name(),
			URL:       entry.URL,
			Category:  javdbCategory,
		}
		// 磁力下面紧跟发布日期；中间可能夹着空行与下载按钮，往后看几行
		for j := i + 1; j < len(lines) && j <= i+4; j++ {
			date := strings.TrimSpace(lines[j])
			if !javdbDateLine.MatchString(date) {
				continue
			}
			if ts, err := time.Parse("2006-01-02", date); err == nil {
				res.PublishDate = ts
			}
			break
		}
		results = append(results, res)
	}
	return results
}

// javdbParseSize 从磁力链接文字里取文件大小，取不到时返回 0。
func javdbParseSize(label string) int64 {
	match := javdbSizeText.FindString(label)
	if match == "" {
		return 0
	}
	return magnet.ParseSize(match)
}
