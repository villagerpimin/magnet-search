package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTemp 把内容写进临时文件并返回路径。
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	return path
}

// TestLoadDefaults 校验只写少量字段时，其余项沿用默认值。
func TestLoadDefaults(t *testing.T) {
	path := writeTemp(t, "addr: \"127.0.0.1:9244\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9244" {
		t.Errorf("addr = %q", cfg.Addr)
	}
	if cfg.DeadFilter != "hide" || cfg.ClusterKey != "resolution" {
		t.Errorf("默认值未生效: %+v", cfg)
	}
	if cfg.RareQuota != -1 {
		t.Errorf("rare_quota 默认应为 -1（自动），实际 %d", cfg.RareQuota)
	}
	if cfg.Timeout.Duration() != 15*time.Second {
		t.Errorf("timeout 默认应为 15s，实际 %v", cfg.Timeout.Duration())
	}
	if !cfg.Probe {
		t.Error("probe 默认应开启")
	}
	if !cfg.SplitQuery {
		t.Error("split_query 默认应开启")
	}
	if cfg.MaxQueryTerms != 4 {
		t.Errorf("max_query_terms 默认应为 4，实际 %d", cfg.MaxQueryTerms)
	}
	if cfg.Database.Enabled {
		t.Error("数据库默认应关闭")
	}
	if cfg.Database.Port != 3306 {
		t.Errorf("database.port 默认应为 3306，实际 %d", cfg.Database.Port)
	}
	if !cfg.Database.RequireLogin {
		t.Error("database.require_login 默认应为 true")
	}
	if cfg.Database.AllowRegister {
		t.Error("database.allow_register 默认应为 false")
	}
	if cfg.DatabaseEnabled() {
		t.Error("数据库默认应视为未启用")
	}
}

// TestLoadOverrides 校验显式写的项会覆盖默认值。
func TestLoadOverrides(t *testing.T) {
	path := writeTemp(t, `
addr: "0.0.0.0:8080"
proxy:
  - "http://127.0.0.1:7897"
  - "socks5://127.0.0.1:1080"
proxy_scope: all
proxy_engines:
  bt4g: "http://127.0.0.1:7897"
timeout: 20s
engine_timeout: 12s
retries: 3
engines: ["apibay", "knaben"]
limit: 50
dead_filter: strict
min_peers: 2
min_confidence: 0.4
rare_quota: 0
per_cluster: 3
cluster_key: full
probe: false
split_query: false
max_query_terms: 6
database:
  enabled: true
  host: "127.0.0.1"
  port: 3307
  username: root
  password: "pw:with@chars"
  database: msearch
  require_login: false
  allow_register: true
account:
  username: admin
  password: secret123
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(cfg.Proxy) != 2 || cfg.Proxy[1] != "socks5://127.0.0.1:1080" {
		t.Errorf("proxy = %v", cfg.Proxy)
	}
	if cfg.ProxyEngines["bt4g"] != "http://127.0.0.1:7897" {
		t.Errorf("proxy_engines = %v", cfg.ProxyEngines)
	}
	if cfg.Timeout.Duration() != 20*time.Second || cfg.EngineTimeout.Duration() != 12*time.Second {
		t.Errorf("时长解析不对: %v / %v", cfg.Timeout.Duration(), cfg.EngineTimeout.Duration())
	}
	if cfg.RareQuota != 0 || cfg.PerCluster != 3 || cfg.MinPeers != 2 {
		t.Errorf("数值未覆盖: %+v", cfg)
	}
	if cfg.Probe {
		t.Error("probe 应被关闭")
	}
	if cfg.SplitQuery || cfg.MaxQueryTerms != 6 {
		t.Errorf("拆词配置未覆盖: split=%v max=%d", cfg.SplitQuery, cfg.MaxQueryTerms)
	}
	if !cfg.DatabaseEnabled() || !cfg.HasAccountSetup() {
		t.Error("数据库与账号应视为已配置")
	}
	if cfg.Database.Host != "127.0.0.1" || cfg.Database.Port != 3307 ||
		cfg.Database.Username != "root" || cfg.Database.Password != "pw:with@chars" ||
		cfg.Database.Name != "msearch" {
		t.Errorf("database 拆分字段解析不对: %+v", cfg.Database)
	}
	if cfg.Database.RequireLogin {
		t.Error("require_login 应被覆盖为 false")
	}
	if !cfg.Database.AllowRegister {
		t.Error("allow_register 应被覆盖为 true")
	}

	opts := cfg.Options()
	if opts.Limit != 50 || opts.DeadFilter != "strict" || opts.ProxyScope != "all" {
		t.Errorf("Options 映射不对: %+v", opts)
	}
	if len(opts.Engines) != 2 || opts.Timeout != 20*time.Second {
		t.Errorf("Options 映射不对: %+v", opts)
	}
	if opts.SplitQuery || opts.MaxQueryTerms != 6 {
		t.Errorf("拆词参数映射不对: %+v", opts)
	}
}

// TestLoadLegacyDSN 校验旧版 dsn 仍然可用。
func TestLoadLegacyDSN(t *testing.T) {
	path := writeTemp(t, `
database:
  enabled: true
  dsn: "root:pw@tcp(127.0.0.1:3306)/msearch?parseTime=true"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("旧版 dsn 应能加载: %v", err)
	}
	if !cfg.DatabaseEnabled() {
		t.Error("只填 dsn 时数据库应视为启用")
	}
	if cfg.Database.Port != 3306 || !cfg.Database.RequireLogin || cfg.Database.AllowRegister {
		t.Errorf("旧版配置的默认值不对: %+v", cfg.Database)
	}
}

// TestLoadValidation 校验写错的配置会明确报错。
func TestLoadValidation(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"死链策略非法", "dead_filter: whatever\n"},
		{"代理范围非法", "proxy_scope: sometimes\n"},
		{"聚类粒度非法", "cluster_key: nope\n"},
		{"时长格式非法", "timeout: 15\n"},
		{"开启数据库却没填任何连接信息", "database:\n  enabled: true\n"},
		{"开启数据库却没填 host", "database:\n  enabled: true\n  database: msearch\n"},
		{"开启数据库却没填 database", "database:\n  enabled: true\n  host: 127.0.0.1\n"},
		{"端口越界", "database:\n  port: 70000\n"},
		{"拆词数量为负", "max_query_terms: -1\n"},
		{"拆词数量过大", "max_query_terms: 11\n"},
		{"端口为负", "database:\n  port: -1\n"},
		{"账号只填一半", "account:\n  username: admin\n"},
		{"密码太短", "account:\n  username: admin\n  password: 123\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, c.content)); err == nil {
				t.Fatalf("应当报错，实际通过了: %s", c.content)
			}
		})
	}
}

// TestLoadMissingFile 校验配置文件不存在时给出可操作的提示。
func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("应当报错")
	}
}
