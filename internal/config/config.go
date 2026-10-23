// Package config 负责读取启动配置文件。
// 服务的全部可调项都来自配置文件，命令行只用来指定配置文件路径。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pigzhu/magnet-search/internal/app"
)

// Database 是数据库相关配置。
type Database struct {
	// Enabled 是否启用数据库（关闭时不记录检索、也不需要登录）
	Enabled bool `yaml:"enabled"`
	// DSN MySQL 连接串
	DSN string `yaml:"dsn"`
}

// Account 是登录账号配置。
// 用户名与密码都填时，启动会创建或更新账号；之后可以留空。
type Account struct {
	// Username 登录用户名
	Username string `yaml:"username"`
	// Password 登录密码（明文写在这里，文件不要提交到仓库）
	Password string `yaml:"password"`
}

// Config 是服务的完整配置。
type Config struct {
	// Addr Web 服务监听地址
	Addr string `yaml:"addr"`
	// Proxy 全局代理地址列表，按顺序回退
	Proxy []string `yaml:"proxy"`
	// ProxyScope 代理作用范围：region 只代理境外引擎，all 代理全部引擎
	ProxyScope string `yaml:"proxy_scope"`
	// ProxyEngines 按引擎单独指定代理
	ProxyEngines map[string]string `yaml:"proxy_engines"`
	// Timeout 单次 HTTP 请求超时
	Timeout Duration `yaml:"timeout"`
	// EngineTimeout 单引擎墙钟预算
	EngineTimeout Duration `yaml:"engine_timeout"`
	// Retries 单次请求重试次数
	Retries int `yaml:"retries"`

	// Engines 默认参与的引擎，留空表示全部
	Engines []string `yaml:"engines"`
	// Limit 每页默认条数
	Limit int `yaml:"limit"`
	// PerEngineLimit 每个引擎默认取回条数
	PerEngineLimit int `yaml:"per_engine_limit"`
	// DeadFilter 死链处理策略：off / hide / mark / strict
	DeadFilter string `yaml:"dead_filter"`
	// MinPeers 最少可信对等端
	MinPeers int `yaml:"min_peers"`
	// MinConfidence 最低置信度
	MinConfidence float64 `yaml:"min_confidence"`
	// RareQuota 冷门席位，-1 表示按比例自动，0 表示关闭
	RareQuota int `yaml:"rare_quota"`
	// RareMaxPeers 判定为稀有的对等端上界
	RareMaxPeers int `yaml:"rare_max_peers"`
	// PerCluster 每个聚类最多输出条数
	PerCluster int `yaml:"per_cluster"`
	// ClusterKey 聚类粒度：resolution / full / none
	ClusterKey string `yaml:"cluster_key"`
	// Probe 是否启用 tracker/DHT 存活探测
	Probe bool `yaml:"probe"`
	// Category 分类过滤
	Category string `yaml:"category"`

	// Database 数据库配置
	Database Database `yaml:"database"`
	// Account 账号配置
	Account Account `yaml:"account"`
}

// Duration 让 YAML 里可以写 "15s" 这样的时长。
type Duration time.Duration

// UnmarshalYAML 解析 "15s"、"500ms" 形式的时长。
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("时长必须是字符串，例如 15s: %w", err)
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("无法解析时长 %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Duration 返回标准库时长。
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// defaults 返回内置默认配置。
// YAML 只覆盖文件里出现的字段，因此这里就是"省略即默认"。
func defaults() *Config {
	return &Config{
		Addr:           "127.0.0.1:8080",
		ProxyScope:     app.ProxyScopeRegion,
		Timeout:        Duration(15 * time.Second),
		EngineTimeout:  Duration(15 * time.Second),
		Retries:        2,
		Limit:          app.DefaultLimit,
		PerEngineLimit: app.DefaultPerEngineLimit,
		DeadFilter:     "hide",
		RareQuota:      -1,
		RareMaxPeers:   app.DefaultRareMaxPeers,
		PerCluster:     app.DefaultPerCluster,
		ClusterKey:     "resolution",
		Probe:          true,
	}
}

// Load 读取配置文件。
func Load(path string) (*Config, error) {
	cfg := defaults()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("找不到配置文件 %s；可以从 config.example.yaml 复制一份", path)
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate 校验取值，避免启动后才发现配置写错。
func (c *Config) validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return fmt.Errorf("addr 不能为空")
	}
	switch c.ProxyScope {
	case app.ProxyScopeRegion, app.ProxyScopeAll:
	default:
		return fmt.Errorf("proxy_scope 只能是 %q 或 %q，当前为 %q", app.ProxyScopeRegion, app.ProxyScopeAll, c.ProxyScope)
	}
	switch c.DeadFilter {
	case "off", "hide", "mark", "strict":
	default:
		return fmt.Errorf("dead_filter 只能是 off / hide / mark / strict，当前为 %q", c.DeadFilter)
	}
	switch c.ClusterKey {
	case "resolution", "full", "none":
	default:
		return fmt.Errorf("cluster_key 只能是 resolution / full / none，当前为 %q", c.ClusterKey)
	}
	if c.Limit < 0 {
		return fmt.Errorf("limit 不能为负数")
	}
	if c.PerEngineLimit < 0 {
		return fmt.Errorf("per_engine_limit 不能为负数")
	}
	if c.MinPeers < 0 {
		return fmt.Errorf("min_peers 不能为负数")
	}
	if c.MinConfidence < 0 || c.MinConfidence > 1 {
		return fmt.Errorf("min_confidence 必须在 0 到 1 之间")
	}
	if c.RareMaxPeers < 0 {
		return fmt.Errorf("rare_max_peers 不能为负数")
	}
	if c.PerCluster < 0 {
		return fmt.Errorf("per_cluster 不能为负数")
	}
	if c.Database.Enabled && strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("database.enabled 为 true 时必须填写 database.dsn")
	}
	if (c.Account.Username == "") != (c.Account.Password == "") {
		return fmt.Errorf("account.username 与 account.password 需要同时填写")
	}
	if c.Account.Password != "" && len(c.Account.Password) < 6 {
		return fmt.Errorf("account.password 至少 6 位")
	}
	return nil
}

// Options 把配置转换成检索流水线参数。
func (c *Config) Options() app.Options {
	return app.Options{
		Engines:        c.Engines,
		Limit:          c.Limit,
		PerEngineLimit: c.PerEngineLimit,
		Category:       c.Category,
		DeadFilter:     c.DeadFilter,
		MinPeers:       c.MinPeers,
		MinConfidence:  c.MinConfidence,
		RareQuota:      c.RareQuota,
		RareMaxPeers:   c.RareMaxPeers,
		PerCluster:     c.PerCluster,
		ClusterKey:     c.ClusterKey,
		Probe:          c.Probe,
		Proxy:          c.Proxy,
		ProxyScope:     c.ProxyScope,
		ProxyEngine:    c.ProxyEngines,
		Timeout:        c.Timeout.Duration(),
		EngineTimeout:  c.EngineTimeout.Duration(),
		Retries:        c.Retries,
	}
}

// DatabaseEnabled 返回是否应该连接数据库。
func (c *Config) DatabaseEnabled() bool {
	return c.Database.Enabled && strings.TrimSpace(c.Database.DSN) != ""
}

// HasAccountSetup 返回是否需要在启动时创建/更新账号。
func (c *Config) HasAccountSetup() bool {
	return c.Account.Username != "" && c.Account.Password != ""
}
