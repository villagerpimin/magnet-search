// Package store 负责把检索记录与登录账号持久化到 MySQL。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Config 是数据库连接配置。
type Config struct {
	// DSN MySQL 连接串，为空表示不启用数据库
	DSN string
	// MaxOpenConns 最大连接数，0 表示使用默认值
	MaxOpenConns int
}

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

// Open 连接数据库并确保表结构存在。
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.DSN == "" {
		return nil, errors.New("未配置数据库连接串")
	}
	db, err := sql.Open("mysql", cfg.DSN)
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
	for _, stmt := range []string{schemaSearchLog, schemaAppUser} {
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

// Account 读取登录账号，没有账号时 found 为 false。
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

// SetAccount 新建或更新登录账号（只保留一个账号）。
func (s *Store) SetAccount(ctx context.Context, username string, passwordHash []byte) error {
	if s == nil || s.db == nil {
		return errors.New("数据库未启用，无法设置账号")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM app_user"); err != nil {
		return fmt.Errorf("清理旧账号失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO app_user (username, password_hash) VALUES (?, ?)",
		truncate(username, 64), passwordHash,
	); err != nil {
		return fmt.Errorf("写入账号失败: %w", err)
	}
	return tx.Commit()
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
