package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/app"
)

// boolFlags 是不接受参数的开关，解析时不会吞掉后面的位置参数。
var boolFlags = map[string]bool{
	"verbose": true, "json": true, "quiet": true, "no-color": true, "no-probe": true,
}

// cliOptions 是命令行解析结果。
type cliOptions struct {
	// query 位置参数拼成的搜索关键词
	query string
	// app 检索流水线参数
	app app.Options
	// json 以 JSON 输出
	json bool
	// quiet 只输出磁力链接
	quiet bool
	// verbose 输出调试信息
	verbose bool
	// addr Web 服务监听地址
	addr string
}

// parseArgs 解析命令行参数。
// 支持 --flag=value 与 --flag value 两种写法；布尔开关不消费后续参数。
func parseArgs(args []string) (*cliOptions, error) {
	opts := &cliOptions{
		app: app.Options{
			DeadFilter:    "hide",
			RareQuota:     -1,
			RareMaxPeers:  app.DefaultRareMaxPeers,
			PerCluster:    app.DefaultPerCluster,
			ClusterKey:    "resolution",
			Probe:         true,
			Timeout:       15 * time.Second,
			EngineTimeout: 15 * time.Second,
			Retries:       2,
		},
		addr: "127.0.0.1:8080",
	}

	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		value := ""
		hasValue := false
		if idx := strings.Index(name, "="); idx >= 0 {
			name, value = name[:idx], name[idx+1:]
			hasValue = true
		}
		if !boolFlags[name] && !hasValue {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, fmt.Errorf("选项 %s 缺少取值", arg)
			}
			i++
			value = args[i]
		}

		switch name {
		case "engine":
			// 允许重复指定，累加而不是覆盖
			opts.app.Engines = append(opts.app.Engines, splitList(value)...)
		case "limit":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --limit 失败: %w", err)
			}
			opts.app.Limit = n
		case "per-engine-limit":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --per-engine-limit 失败: %w", err)
			}
			opts.app.PerEngineLimit = n
		case "category":
			opts.app.Category = value
		case "dead-filter":
			opts.app.DeadFilter = value
		case "min-peers":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --min-peers 失败: %w", err)
			}
			opts.app.MinPeers = n
		case "min-confidence":
			f, err := parseFloat(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --min-confidence 失败: %w", err)
			}
			opts.app.MinConfidence = f
		case "rare-quota":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --rare-quota 失败: %w", err)
			}
			opts.app.RareQuota = n
		case "rare-max-peers":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --rare-max-peers 失败: %w", err)
			}
			opts.app.RareMaxPeers = n
		case "per-cluster":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --per-cluster 失败: %w", err)
			}
			opts.app.PerCluster = n
		case "cluster-key":
			opts.app.ClusterKey = value
		case "no-probe":
			opts.app.Probe = false
		case "proxy":
			// 允许重复指定，累加而不是覆盖
			opts.app.Proxy = append(opts.app.Proxy, splitList(value)...)
		case "proxy-engine":
			k, v, ok := strings.Cut(value, "=")
			if !ok {
				return nil, fmt.Errorf("选项 --proxy-engine 需要 name=url 形式，收到 %q", value)
			}
			if opts.app.ProxyEngine == nil {
				opts.app.ProxyEngine = map[string]string{}
			}
			opts.app.ProxyEngine[strings.TrimSpace(k)] = strings.TrimSpace(v)
		case "timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --timeout 失败: %w", err)
			}
			opts.app.Timeout = d
		case "engine-timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --engine-timeout 失败: %w", err)
			}
			opts.app.EngineTimeout = d
		case "retries":
			n, err := parseInt(value)
			if err != nil {
				return nil, fmt.Errorf("解析 --retries 失败: %w", err)
			}
			opts.app.Retries = n
		case "addr":
			opts.addr = value
		case "json":
			opts.json = true
		case "quiet":
			opts.quiet = true
		case "verbose":
			opts.verbose = true
		case "no-color":
			// 当前输出未使用颜色，保留该选项以兼容习惯
		default:
			return nil, fmt.Errorf("未知选项: %s", arg)
		}
	}

	opts.query = strings.Join(positional, " ")
	// 关键词必须同时落到流水线的参数上，否则引擎会按空查询去搜
	opts.app.Query = opts.query
	return opts, nil
}

// splitList 按逗号切分并去除空白。
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseInt 解析整数。
func parseInt(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

// parseFloat 解析浮点数。
func parseFloat(s string) (float64, error) {
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &f); err != nil {
		return 0, err
	}
	return f, nil
}
