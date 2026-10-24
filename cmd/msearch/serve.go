package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pigzhu/magnet-search/internal/app"
	"github.com/pigzhu/magnet-search/internal/config"
	"github.com/pigzhu/magnet-search/internal/engine"
	"github.com/pigzhu/magnet-search/internal/server"
	"github.com/pigzhu/magnet-search/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// defaultConfigPath 是默认的配置文件路径。
const defaultConfigPath = "config.yaml"

// parseConfigPath 从参数里取出配置文件路径。
// serve 只接受 --config，其余配置项一律写在配置文件里。
func parseConfigPath(args []string) (string, error) {
	path := defaultConfigPath
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--config" || arg == "-c":
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s 缺少配置文件路径", arg)
			}
			i++
			path = args[i]
		case strings.HasPrefix(arg, "--config="):
			path = strings.TrimPrefix(arg, "--config=")
		case arg == "--help" || arg == "-h":
			usage()
			os.Exit(0)
		default:
			return "", fmt.Errorf("serve 只接受 --config <路径>：其他配置项请写进配置文件（%s）", defaultConfigPath)
		}
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("配置文件路径不能为空")
	}
	return path, nil
}

// runServe 读取配置文件并启动 Web 服务。
func runServe(args []string) int {
	path, err := parseConfigPath(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "参数错误:", err)
		return 2
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 连接数据库；失败时降级为"不启用数据库"，服务仍然可用
	var st *store.Store
	dbSettings := store.AppSettings{
		RequireLogin:  cfg.Database.RequireLogin,
		AllowRegister: cfg.Database.AllowRegister,
	}
	if cfg.DatabaseEnabled() {
		opened, err := store.Open(ctx, store.Config{
			DSN:      cfg.Database.DSN,
			Host:     cfg.Database.Host,
			Port:     cfg.Database.Port,
			Username: cfg.Database.Username,
			Password: cfg.Database.Password,
			Database: cfg.Database.Name,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "警告：数据库不可用，已按未启用数据库运行：", err)
		} else {
			st = opened
			defer st.Close()
			fmt.Println("数据库：已连接")
			// 配置里的开关只作为首次写入的默认值，之后以库里的设置为准
			loaded, err := st.Settings(ctx, dbSettings)
			if err != nil {
				fmt.Fprintln(os.Stderr, "警告：读取数据库设置失败，使用配置默认值：", err)
			} else {
				dbSettings = loaded
			}
		}
	} else {
		fmt.Println("数据库：未启用（不记录检索、不需要登录）")
	}

	// 配置文件里给了账号就创建/更新（首次初始化或改密）
	if st != nil && cfg.HasAccountSetup() {
		if err := setAccount(ctx, st, cfg.Account.Username, cfg.Account.Password); err != nil {
			fmt.Fprintln(os.Stderr, "设置账号失败:", err)
		} else {
			fmt.Printf("登录账号：%s（已更新）\n", cfg.Account.Username)
		}
	}

	srv := server.New(cfg.Options(), st, dbSettings)
	fmt.Printf("配置文件：%s\n", path)
	fmt.Printf("磁力聚合搜索界面已启动： http://%s\n", cfg.Addr)
	fmt.Printf("代理： %v（作用范围 %s）\n", cfg.Proxy, cfg.ProxyScope)
	fmt.Printf("引擎： %v\n", cfg.Engines)
	if st != nil {
		fmt.Printf("登录要求：%s；账号注册：%s\n", onOff(dbSettings.RequireLogin), onOff(dbSettings.AllowRegister))
	}
	if !isLoopbackAddr(cfg.Addr) {
		fmt.Println("提示：监听地址不是本机回环，服务会对网络上的其他主机开放，请自行确认访问控制。")
	}
	fmt.Println("按 Ctrl+C 停止。")

	if err := srv.ListenAndServe(ctx, cfg.Addr); err != nil {
		fmt.Fprintln(os.Stderr, "服务异常退出:", err)
		return 1
	}
	return 0
}

// setAccount 把账号写入数据库（启动时按配置初始化或改密）。
func setAccount(ctx context.Context, st *store.Store, username, password string) error {
	if len(password) < 6 {
		return fmt.Errorf("密码至少 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return st.UpsertUser(ctx, username, hash)
}

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

// onOff 把布尔开关转成启动信息里的中文状态。
func onOff(v bool) string {
	if v {
		return "开启"
	}
	return "关闭"
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
