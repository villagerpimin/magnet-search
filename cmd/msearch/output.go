package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/aggregate"
	"github.com/pigzhu/magnet-search/internal/app"
)

// runSearch 执行搜索并输出结果。
func runSearch(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误:", err)
		usage()
		return 2
	}
	if opts.query == "" {
		usage()
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	out, err := app.Search(ctx, opts.app)
	if err != nil {
		fmt.Fprintln(os.Stderr, "搜索失败:", err)
		return 1
	}

	if opts.verbose {
		for name, n := range out.PerEngine {
			fmt.Fprintf(os.Stderr, "引擎 %s 返回 %d 条\n", name, n)
		}
		for _, f := range out.Failures {
			fmt.Fprintf(os.Stderr, "引擎 %s 失败: %v\n", f.Engine, f.Err)
		}
		if !out.ProbeAvailable && out.ProbeReason != "" {
			fmt.Fprintln(os.Stderr, out.ProbeReason)
		}
	}

	if opts.json {
		writeJSON(out)
		return 0
	}
	printTable(out, opts.quiet)
	return 0
}

// jsonItem 是 JSON 输出的单条结构。
type jsonItem struct {
	Title       string   `json:"title"`
	InfoHash    string   `json:"info_hash"`
	Magnet      string   `json:"magnet"`
	SizeBytes   int64    `json:"size_bytes"`
	Seeders     int      `json:"seeders"`
	Leechers    int      `json:"leechers"`
	PublishDate string   `json:"publish_date,omitempty"`
	Sources     []string `json:"sources"`
	Verdict     string   `json:"verdict"`
	Confidence  float64  `json:"confidence"`
	Relevance   float64  `json:"relevance"`
	Score       float64  `json:"score"`
	Peers       int      `json:"credible_peers"`
	URL         string   `json:"url,omitempty"`
}

// writeJSON 以 JSON 输出搜索结果。
func writeJSON(out *app.Outcome) {
	items := make([]jsonItem, 0, len(out.Results))
	for _, c := range out.Results {
		item := jsonItem{
			Title:      c.Result.Title,
			InfoHash:   c.Result.InfoHash,
			Magnet:     c.Result.MagnetURI,
			SizeBytes:  c.Result.SizeBytes,
			Seeders:    c.Result.Seeders,
			Leechers:   c.Result.Leechers,
			Sources:    c.Sources,
			Verdict:    string(c.Verdict),
			Confidence: round3(c.Confidence),
			Relevance:  round3(c.Relevance),
			Score:      round3(c.Score),
			Peers:      aggregate.CrediblePeers(&c),
			URL:        c.Result.URL,
		}
		if !c.Result.PublishDate.IsZero() {
			item.PublishDate = c.Result.PublishDate.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(items)
}

// printTable 以表格输出搜索结果。
func printTable(out *app.Outcome, quiet bool) {
	if quiet {
		for _, c := range out.Results {
			fmt.Println(c.Result.MagnetURI)
		}
		return
	}
	if len(out.Results) == 0 {
		fmt.Println("没有找到符合条件的结果。")
		return
	}

	fmt.Printf("%-3s %-6s %-6s %-6s %-10s %s\n", "#", "状态", "相关", "对等", "大小", "标题")
	for i, c := range out.Results {
		fmt.Printf("%-3d %-6s %-6.2f %-6d %-10s %s\n",
			i+1,
			verdictLabel(c.Verdict),
			c.Relevance,
			aggregate.CrediblePeers(&c),
			humanSize(c.Result.SizeBytes),
			truncate(c.Result.Title, 68),
		)
		fmt.Printf("    %s  %s\n", c.Result.MagnetURI, strings.Join(c.Sources, ","))
	}
	fmt.Printf("\n共 %d 条结果", len(out.Results))
	if out.Dropped > 0 {
		fmt.Printf("，过滤 %d 条", out.Dropped)
	}
	fmt.Printf("，耗时 %.1fs\n", out.Elapsed.Seconds())
}

// verdictLabel 返回状态标签。
func verdictLabel(v aggregate.Verdict) string {
	switch v {
	case aggregate.VerdictAlive:
		return "活跃"
	case aggregate.VerdictCold:
		return "冷门"
	case aggregate.VerdictDead:
		return "死链"
	default:
		return "未知"
	}
}

// humanSize 把字节数格式化为易读形式。
func humanSize(n int64) string {
	const unit = 1024
	if n <= 0 {
		return "-"
	}
	exp := 0
	f := float64(n)
	for f >= unit && exp < 5 {
		f /= unit
		exp++
	}
	if exp == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %cB", f, "BKMGTP"[exp])
}

// truncate 截断过长的字符串。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// round3 保留三位小数。
func round3(f float64) float64 {
	return float64(int64(f*1000+0.5)) / 1000
}
