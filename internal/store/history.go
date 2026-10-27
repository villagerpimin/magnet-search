package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// schemaSearchHistory 保存按账号去重后的搜索历史。
// query_hash 让长短关键词都能稳定去重，避免用前缀索引做唯一键时误合并。
const schemaSearchHistory = "CREATE TABLE IF NOT EXISTS search_history (" +
	"id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT," +
	"user_id BIGINT UNSIGNED NOT NULL DEFAULT 0," +
	"`query` VARCHAR(512) NOT NULL," +
	"query_hash CHAR(64) NOT NULL," +
	"created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP," +
	"updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
	"PRIMARY KEY (id)," +
	"UNIQUE KEY uk_user_query (user_id, query_hash)," +
	"KEY idx_user_updated (user_id, updated_at)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"

// HistoryEntry 是一条去重后的搜索历史。
type HistoryEntry struct {
	// Query 关键词
	Query string
	// UpdatedAt 最近一次搜索时间
	UpdatedAt time.Time
}

// AddHistory 记录一次搜索；同一账号下重复关键词只更新时间。
func (s *Store) AddHistory(ctx context.Context, userID int64, query string) error {
	query = strings.TrimSpace(query)
	if query == "" || s == nil || s.db == nil {
		return nil
	}
	if userID < 0 {
		userID = 0
	}
	stored := truncate(query, 512)
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO search_history (user_id, `query`, query_hash) VALUES (?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE `query` = VALUES(`query`), updated_at = CURRENT_TIMESTAMP",
		userID, stored, historyHash(stored),
	)
	if err != nil {
		return fmt.Errorf("写入搜索历史失败: %w", err)
	}
	return nil
}

// History 读取某个账号最近的搜索历史，按最近使用时间倒序。
func (s *Store) History(ctx context.Context, userID int64, limit int) ([]HistoryEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if userID < 0 {
		userID = 0
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT `query`, updated_at FROM search_history WHERE user_id = ? ORDER BY updated_at DESC, id DESC LIMIT ?",
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("读取搜索历史失败: %w", err)
	}
	defer rows.Close()

	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.Query, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("解析搜索历史失败: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteHistory 删除某个账号的一条搜索历史。
func (s *Store) DeleteHistory(ctx context.Context, userID int64, query string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	if userID < 0 {
		userID = 0
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return false, nil
	}
	stored := truncate(query, 512)
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM search_history WHERE user_id = ? AND query_hash = ?",
		userID, historyHash(stored),
	)
	if err != nil {
		return false, fmt.Errorf("删除搜索历史失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("删除搜索历史失败: %w", err)
	}
	return n > 0, nil
}

// ClearHistory 清空某个账号的全部搜索历史。
func (s *Store) ClearHistory(ctx context.Context, userID int64) error {
	if s == nil || s.db == nil {
		return nil
	}
	if userID < 0 {
		userID = 0
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM search_history WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("清空搜索历史失败: %w", err)
	}
	return nil
}

// historyHash 用规范化后的小写关键词生成去重哈希。
func historyHash(query string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(query))))
	return hex.EncodeToString(sum[:])
}
