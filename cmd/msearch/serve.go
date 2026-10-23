package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/pigzhu/magnet-search/internal/app"
	"github.com/pigzhu/magnet-search/internal/engine"
	"github.com/pigzhu/magnet-search/internal/server"
	"github.com/pigzhu/magnet-search/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// runCheck 逐个测试引擎在当前网络下的可用性。
func runCheck(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误:", err)
		return 2
	}
	if opts.app.Timeout <= 0 {
		opts.app.Timeout = 12 * time.Second
	}

	reg := engine.NewRegistry()
	engine.RegisterAll(reg)
	specs, err := reg.Select(opts.app.Engines)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	type report struct {
		name    string
		region  string
		ok      bool
		count   int
		elapsed time.Duration
		err     string
	}
	var reports []report
	okCount := 0

	for _, spec := range specs {
		e, err := app.BuildEngine(spec, opts.app)
		if err != nil {
			reports = append(reports, report{name: spec.Name, region: string(spec.Region), err: err.Error()})
			continue
		}
		start := time.Now()
		engCtx, engCancel := context.WithTimeout(ctx, 30*time.Second)
		results, err := e.Search(engCtx, engine.Options{Query: "ubuntu", Limit: 5})
		engCancel()
		rep := report{name: spec.Name, region: string(spec.Region), elapsed: time.Since(start), count: len(results)}
		if err != nil {
			rep.err = err.Error()
		} else {
			rep.ok = true
			okCount++
		}
		reports = append(reports, rep)
	}

	sort.Slice(reports, func(i, j int) bool { return reports[i].name < reports[j].name })
	fmt.Printf("%-14s %-10s %-8s %-8s %s\n", "引擎", "区域", "结果", "耗时", "状态")
	for _, r := range reports {
		status := "可用"
		if !r.ok {
			status = "不可用: " + truncate(r.err, 60)
		}
		fmt.Printf("%-14s %-10s %-8d %-8s %s\n", r.name, r.region, r.count, r.elapsed.Round(time.Millisecond), status)
	}
	fmt.Printf("\n%d/%d 个引擎可用\n", okCount, len(reports))
	if okCount == 0 {
		return 2
	}
	if okCount < len(reports) {
		return 1
	}
	return 0
}

// runEngines 列出全部引擎。
func runEngines() int {
	reg := engine.NewRegistry()
	engine.RegisterAll(reg)
	specs := reg.Specs()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	for _, s := range specs {
		fmt.Printf("%-14s %-10s %s\n", s.Name, s.Region, s.Description)
	}
	return 0
}

// runServe 启动本地 Web 界面。
func runServe(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误:", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 连接数据库；连接失败时降级为"不启用数据库"，服务仍然可用
	var st *store.Store
	if opts.dbDSN != "" {
		opened, err := store.Open(ctx, store.Config{DSN: opts.dbDSN})
		if err != nil {
			fmt.Fprintln(os.Stderr, "警告：数据库不可用，已按未启用数据库运行：", err)
		} else {
			st = opened
			defer st.Close()
			fmt.Println("数据库：已连接")
		}
	} else {
		fmt.Println("数据库：未启用（不记录检索、不需要登录）")
	}

	// 启动时按参数设置账号，方便首次初始化或改密
	if st != nil && opts.username != "" && opts.password != "" {
		if err := setAccount(ctx, st, opts.username, opts.password); err != nil {
			fmt.Fprintln(os.Stderr, "设置账号失败:", err)
		} else {
			fmt.Printf("登录账号：%s（已更新）\n", opts.username)
		}
	}

	srv := server.New(opts.app, st)
	fmt.Printf("磁力聚合搜索界面已启动： http://%s\n", opts.addr)
	fmt.Printf("代理： %v   引擎： %v\n", opts.app.Proxy, opts.app.Engines)
	if !isLoopbackAddr(opts.addr) {
		fmt.Println("提示：监听地址不是本机回环，服务会对网络上的其他主机开放，请自行确认访问控制。")
	}
	fmt.Println("按 Ctrl+C 停止。")

	if err := srv.ListenAndServe(ctx, opts.addr); err != nil {
		fmt.Fprintln(os.Stderr, "服务异常退出:", err)
		return 1
	}
	return 0
}

// isLoopbackAddr 判断监听地址是否只对本机开放。
func isLoopbackAddr(addr string) bool {
	host, _, err := splitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// splitHostPort 拆分 host:port。
func splitHostPort(addr string) (string, string, error) {
	return net.SplitHostPort(addr)
}

// setAccount 把账号写入数据库（启动时按参数初始化或改密）。
func setAccount(ctx context.Context, st *store.Store, username, password string) error {
	if len(password) < 6 {
		return fmt.Errorf("密码至少 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return st.SetAccount(ctx, username, hash)
}
