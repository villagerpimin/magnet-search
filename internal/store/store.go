// Package store 负责把检索记录、登录账号与运行时设置持久化到 MySQL。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Config 是数据库连接配置。
type Config struct {
	// DSN MySQL 连接串；填写后优先使用，忽略下面的拆分字段
	DSN string
	// Host MySQL 主机地址
	Host string
	// Port MySQL 端口，0 表示使用默认端口 3306
	Port int
	// Username MySQL 登录用户名
	Username string
	// Password MySQL 登录密码
	Password string
	// Database MySQL 数据库名
	Database string
	// MaxOpenConns 最大连接数，0 表示使用默认值
	MaxOpenConns int
}

// AppSettings 是保存在数据库里的运行时开关。
type AppSettings struct {
	// RequireLogin 是否要求登录后才能检索
	RequireLogin bool
	// AllowRegister 是否允许自助注册账号
	AllowRegister bool
}

// ErrUserExists 表示用户名已被占用。
var ErrUserExists = errors.New("用户名已存在")

const (
	// settingRequireLogin 登录开关在 app_setting 表中的键名
	settingRequireLogin = "require_login"
	// settingAllowRegister 注册开关在 app_setting 表中的键名
	settingAllowRegister = "allow_register"
)

// SearchLog 是一条检索记录。
type SearchLog struct {
	// Query 检索关键词
	Query string
	// IP 来源 IP
	IP string
	// UserAgent 客户端标识
	UserAgent string
	// Engines 本次参与的引擎，逗号分隔
	Engines string
	// ResultCount 返回的结果条数
	ResultCount int
	// ElapsedMS 检索耗时（毫秒）
	ElapsedMS int
	// CreatedAt 记录时间
	CreatedAt time.Time
}

// Store 是 MySQL 存储。
type Store struct {
	db *sql.DB
}

// 建表语句：启动时执行一次，保证表结构存在。
const schemaSearchLog = "CREATE TABLE IF NOT EXISTS search_log (" +
	"id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT," +
	"`query` VARCHAR(512) NOT NULL," +
	"ip VARCHAR(64) NOT NULL DEFAULT ''," +
	"user_agent VARCHAR(255) NOT NULL DEFAULT ''," +
	"engines VARCHAR(255) NOT NULL DEFAULT ''," +
	"result_count INT NOT NULL DEFAULT 0," +
	"elapsed_ms INT NOT NULL DEFAULT 0," +
	"created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP," +
	"PRIMARY KEY (id)," +
	"KEY idx_created_at (created_at)," +
	"KEY idx_query (`query`(191))" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"

const schemaAppUser = "CREATE TABLE IF NOT EXISTS app_user (" +
	"id INT UNSIGNED NOT NULL AUTO_INCREMENT," +
	"username VARCHAR(64) NOT NULL," +
	"password_hash VARBINARY(255) NOT NULL," +
	"updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
	"PRIMARY KEY (id)," +
	"UNIQUE KEY uk_username (username)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"

const schemaAppSetting = "CREATE TABLE IF NOT EXISTS app_setting (" +
	"`key` VARCHAR(64) NOT NULL," +
	"`value` VARCHAR(255) NOT NULL DEFAULT ''," +
	"updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
	"PRIMARY KEY (`key`)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"

// buildDSN 生成 MySQL 连接串。
// 显式填写 DSN 时直接使用；否则由拆分字段拼接，缺少主机或库名时报错。
func buildDSN(cfg Config) (string, error) {
	if strings.TrimSpace(cfg.DSN) != "" {
		return cfg.DSN, nil
	}
	host := strings.TrimSpace(cfg.Host)
	name := strings.TrimSpace(cfg.Database)
	if host == "" || name == "" {
		return "", errors.New("未配置数据库连接：请填写 dsn，或至少填写 host 与 database")
	}
	port := cfg.Port
	if port <= 0 {
		port = 3306
	}
	mc := mysql.NewConfig()
	mc.User = cfg.Username
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	mc.DBName = name
	mc.ParseTime = true
	mc.Loc = time.Local
	mc.Params = map[string]string{"charset": "utf8mb4"}
	return mc.FormatDSN(), nil
}

// Open 连接数据库并确保表结构存在。
func Open(ctx context.Context, cfg Config) (*Store, error) {
	dsn, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	} else {
		db.SetMaxOpenConns(8)
	}
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	s := &Store{db: db}
	if err := s.ensureSchema(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// ensureSchema 建表，已存在则跳过。
func (s *Store) ensureSchema(ctx context.Context) error {
	for _, stmt := range []string{schemaSearchLog, schemaAppUser, schemaAppSetting} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("初始化表结构失败: %w", err)
		}
	}
	return nil
}

// Close 关闭连接池。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// LogSearch 写入一条检索记录。
func (s *Store) LogSearch(ctx context.Context, e SearchLog) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO search_log (`query`, ip, user_agent, engines, result_count, elapsed_ms) VALUES (?, ?, ?, ?, ?, ?)",
		truncate(e.Query, 512), truncate(e.IP, 64), truncate(e.UserAgent, 255),
		truncate(e.Engines, 255), e.ResultCount, e.ElapsedMS,
	)
	if err != nil {
		return fmt.Errorf("写入检索记录失败: %w", err)
	}
	return nil
}

// Recent 读取最近的检索记录，按时间倒序。
func (s *Store) Recent(ctx context.Context, limit int) ([]SearchLog, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT `query`, ip, user_agent, engines, result_count, elapsed_ms, created_at FROM search_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("读取检索记录失败: %w", err)
	}
	defer rows.Close()

	var out []SearchLog
	for rows.Next() {
		var e SearchLog
		if err := rows.Scan(&e.Query, &e.IP, &e.UserAgent, &e.Engines, &e.ResultCount, &e.ElapsedMS, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("解析检索记录失败: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Settings 读取运行时开关。
// 先把 defaults 作为缺失键的初始值写入，再读取实际值；
// 键缺失或取值非法时回退到 defaults。
func (s *Store) Settings(ctx context.Context, defaults AppSettings) (AppSettings, error) {
	if s == nil || s.db == nil {
		return defaults, nil
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT IGNORE INTO app_setting (`key`, `value`) VALUES (?, ?), (?, ?)",
		settingRequireLogin, boolText(defaults.RequireLogin),
		settingAllowRegister, boolText(defaults.AllowRegister),
	); err != nil {
		return defaults, fmt.Errorf("初始化设置失败: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT `key`, `value` FROM app_setting WHERE `key` IN (?, ?)",
		settingRequireLogin, settingAllowRegister)
	if err != nil {
		return defaults, fmt.Errorf("读取设置失败: %w", err)
	}
	defer rows.Close()

	out := defaults
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return defaults, fmt.Errorf("解析设置失败: %w", err)
		}
		switch key {
		case settingRequireLogin:
			if v, ok := parseBoolText(value); ok {
				out.RequireLogin = v
			}
		case settingAllowRegister:
			if v, ok := parseBoolText(value); ok {
				out.AllowRegister = v
			}
		}
	}
	if err := rows.Err(); err != nil {
		return defaults, fmt.Errorf("读取设置失败: %w", err)
	}
	return out, nil
}

// SaveSettings 保存两个运行时开关，同一事务内写入。
func (s *Store) SaveSettings(ctx context.Context, v AppSettings) error {
	if s == nil || s.db == nil {
		return errors.New("数据库未启用，无法保存设置")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	for _, item := range []struct {
		key   string
		value string
	}{
		{settingRequireLogin, boolText(v.RequireLogin)},
		{settingAllowRegister, boolText(v.AllowRegister)},
	} {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO app_setting (`key`, `value`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `value` = VALUES(`value`)",
			item.key, item.value,
		); err != nil {
			return fmt.Errorf("保存设置失败: %w", err)
		}
	}
	return tx.Commit()
}

// CountUsers 返回账号总数。
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_user").Scan(&n); err != nil {
		return 0, fmt.Errorf("统计账号失败: %w", err)
	}
	return n, nil
}

// User 按用户名读取账号，返回库中规范化的用户名与密码哈希。
func (s *Store) User(ctx context.Context, username string) (name string, hash []byte, found bool, err error) {
	if s == nil || s.db == nil {
		return "", nil, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		"SELECT username, password_hash FROM app_user WHERE username = ? LIMIT 1", truncate(username, 64))
	switch e := row.Scan(&name, &hash); {
	case errors.Is(e, sql.ErrNoRows):
		return "", nil, false, nil
	case e != nil:
		return "", nil, false, fmt.Errorf("读取账号失败: %w", e)
	}
	return name, hash, true, nil
}

// CreateUser 新建账号，用户名重复时返回 ErrUserExists。
func (s *Store) CreateUser(ctx context.Context, username string, passwordHash []byte) error {
	if s == nil || s.db == nil {
		return errors.New("数据库未启用，无法创建账号")
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO app_user (username, password_hash) VALUES (?, ?)",
		truncate(username, 64), passwordHash,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrUserExists
		}
		return fmt.Errorf("创建账号失败: %w", err)
	}
	return nil
}

// UpdateUser 修改账号：用户名改到 next；passwordHash 非空时同时更新密码。
// 用户名重复时返回 ErrUserExists。
func (s *Store) UpdateUser(ctx context.Context, current, next string, passwordHash []byte) error {
	if s == nil || s.db == nil {
		return errors.New("数据库未启用，无法更新账号")
	}
	if strings.TrimSpace(next) == "" {
		next = current
	}
	var err error
	if len(passwordHash) > 0 {
		_, err = s.db.ExecContext(ctx,
			"UPDATE app_user SET username = ?, password_hash = ? WHERE username = ?",
			truncate(next, 64), passwordHash, truncate(current, 64),
		)
	} else {
		_, err = s.db.ExecContext(ctx,
			"UPDATE app_user SET username = ? WHERE username = ?",
			truncate(next, 64), truncate(current, 64),
		)
	}
	if err != nil {
		if isDuplicateKey(err) {
			return ErrUserExists
		}
		return fmt.Errorf("更新账号失败: %w", err)
	}
	return nil
}

// UpsertUser 新建或更新账号密码，不会删除其他账号。
// 用于把配置文件里的账号同步进库。
func (s *Store) UpsertUser(ctx context.Context, username string, passwordHash []byte) error {
	if s == nil || s.db == nil {
		return errors.New("数据库未启用，无法设置账号")
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO app_user (username, password_hash) VALUES (?, ?) "+
			"ON DUPLICATE KEY UPDATE password_hash = VALUES(password_hash)",
		truncate(username, 64), passwordHash,
	)
	if err != nil {
		return fmt.Errorf("写入账号失败: %w", err)
	}
	return nil
}

// Account 读取第一个账号，保留给旧调用方；新代码请使用 User。
func (s *Store) Account(ctx context.Context) (username string, hash []byte, found bool, err error) {
	if s == nil || s.db == nil {
		return "", nil, false, nil
	}
	row := s.db.QueryRowContext(ctx, "SELECT username, password_hash FROM app_user ORDER BY id LIMIT 1")
	switch e := row.Scan(&username, &hash); {
	case errors.Is(e, sql.ErrNoRows):
		return "", nil, false, nil
	case e != nil:
		return "", nil, false, fmt.Errorf("读取账号失败: %w", e)
	}
	return username, hash, true, nil
}

// SetAccount 新建或更新账号，保留给旧调用方；新代码请使用 UpsertUser。
// 与旧版不同，这里不再删除其他账号。
func (s *Store) SetAccount(ctx context.Context, username string, passwordHash []byte) error {
	return s.UpsertUser(ctx, username, passwordHash)
}

// boolText 把布尔值转成数据库里保存的 "1"/"0"。
func boolText(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// parseBoolText 解析 "1"/"0"，其他取值返回 ok=false。
func parseBoolText(s string) (value bool, ok bool) {
	switch s {
	case "1":
		return true, true
	case "0":
		return false, true
	default:
		return false, false
	}
}

// isDuplicateKey 判断是否为 MySQL 的唯一键冲突（错误号 1062）。
func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// truncate 按字节截断，避免超长字段导致写入失败。
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	// 按 rune 边界回退，避免把多字节字符截断成乱码
	cut := limit
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
