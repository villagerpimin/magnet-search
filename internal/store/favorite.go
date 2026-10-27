package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// schemaAppFavorite 保存账号的磁力收藏。
const schemaAppFavorite = "CREATE TABLE IF NOT EXISTS app_favorite (" +
	"id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT," +
	"user_id BIGINT UNSIGNED NOT NULL DEFAULT 0," +
	"info_hash CHAR(40) NOT NULL," +
	"title VARCHAR(512) NOT NULL," +
	"magnet TEXT NOT NULL," +
	"size VARCHAR(32) NOT NULL DEFAULT ''," +
	"size_bytes BIGINT NOT NULL DEFAULT 0," +
	"seeders INT NOT NULL DEFAULT -1," +
	"leechers INT NOT NULL DEFAULT -1," +
	"sources VARCHAR(255) NOT NULL DEFAULT ''," +
	"note VARCHAR(255) NOT NULL DEFAULT ''," +
	"created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP," +
	"updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
	"PRIMARY KEY (id)," +
	"UNIQUE KEY uk_user_hash (user_id, info_hash)," +
	"KEY idx_user_updated (user_id, updated_at)" +
	") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"

// Favorite 是一条收藏记录。
type Favorite struct {
	// ID 收藏主键
	ID int64
	// UserID 所属账号 ID，0 表示未登录时的匿名账号
	UserID int64
	// InfoHash 40 位十六进制哈希
	InfoHash string
	// Title 资源标题
	Title string
	// Magnet 磁力链接
	Magnet string
	// Size 易读大小
	Size string
	// SizeBytes 字节数
	SizeBytes int64
	// Seeders 做种数，-1 表示未知
	Seeders int
	// Leechers 下载数，-1 表示未知
	Leechers int
	// Sources 来源引擎，逗号分隔
	Sources string
	// Note 自定义备注
	Note string
	// CreatedAt 收藏时间
	CreatedAt time.Time
	// UpdatedAt 最近更新时间
	UpdatedAt time.Time
}

// Favorites 读取某个账号的全部收藏，最近更新的在前。
func (s *Store) Favorites(ctx context.Context, userID int64) ([]Favorite, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if userID < 0 {
		userID = 0
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, user_id, info_hash, title, magnet, size, size_bytes, seeders, leechers, sources, note, created_at, updated_at "+
			"FROM app_favorite WHERE user_id = ? ORDER BY updated_at DESC, id DESC LIMIT 500",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("读取收藏失败: %w", err)
	}
	defer rows.Close()

	var out []Favorite
	for rows.Next() {
		var f Favorite
		if err := rows.Scan(&f.ID, &f.UserID, &f.InfoHash, &f.Title, &f.Magnet, &f.Size, &f.SizeBytes,
			&f.Seeders, &f.Leechers, &f.Sources, &f.Note, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("解析收藏失败: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AddFavorite 新增收藏；同一账号重复收藏同一哈希时更新资源元数据并保留备注。
// 返回收藏 ID。
func (s *Store) AddFavorite(ctx context.Context, f Favorite) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("数据库未启用，无法收藏")
	}
	if f.UserID < 0 {
		f.UserID = 0
	}
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO app_favorite (user_id, info_hash, title, magnet, size, size_bytes, seeders, leechers, sources, note) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE title = VALUES(title), magnet = VALUES(magnet), size = VALUES(size), "+
			"size_bytes = VALUES(size_bytes), seeders = VALUES(seeders), leechers = VALUES(leechers), "+
			"sources = VALUES(sources), updated_at = CURRENT_TIMESTAMP, id = LAST_INSERT_ID(id)",
		f.UserID, f.InfoHash, truncate(f.Title, 512), f.Magnet, truncate(f.Size, 32),
		f.SizeBytes, f.Seeders, f.Leechers, truncate(f.Sources, 255), truncate(f.Note, 255),
	)
	if err != nil {
		return 0, fmt.Errorf("写入收藏失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("读取收藏 ID 失败: %w", err)
	}
	return id, nil
}

// UpdateFavorite 修改收藏的标题与备注，返回是否命中了该账号的收藏。
func (s *Store) UpdateFavorite(ctx context.Context, userID, id int64, title, note string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("数据库未启用，无法修改收藏")
	}
	if userID < 0 {
		userID = 0
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE app_favorite SET title = ?, note = ? WHERE id = ? AND user_id = ?",
		truncate(title, 512), truncate(note, 255), id, userID,
	)
	if err != nil {
		return false, fmt.Errorf("修改收藏失败: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return true, nil
	}
	// MySQL 在字段值没有变化时 RowsAffected 为 0，这里再确认一次记录是否存在。
	var count int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM app_favorite WHERE id = ? AND user_id = ?", id, userID,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("确认收藏失败: %w", err)
	}
	return count > 0, nil
}

// DeleteFavorite 按 ID 删除收藏。
func (s *Store) DeleteFavorite(ctx context.Context, userID, id int64) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("数据库未启用，无法删除收藏")
	}
	if userID < 0 {
		userID = 0
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM app_favorite WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return false, fmt.Errorf("删除收藏失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("删除收藏失败: %w", err)
	}
	return n > 0, nil
}

// DeleteFavoriteByHash 按 info hash 删除收藏，供搜索结果里的星标取消使用。
func (s *Store) DeleteFavoriteByHash(ctx context.Context, userID int64, infoHash string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("数据库未启用，无法删除收藏")
	}
	if userID < 0 {
		userID = 0
	}
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM app_favorite WHERE user_id = ? AND info_hash = ?",
		userID, strings.ToLower(strings.TrimSpace(infoHash)),
	)
	if err != nil {
		return false, fmt.Errorf("删除收藏失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("删除收藏失败: %w", err)
	}
	return n > 0, nil
}

// FavoriteByHash 读取单条收藏，未找到时返回 sql.ErrNoRows。
func (s *Store) FavoriteByHash(ctx context.Context, userID int64, infoHash string) (Favorite, error) {
	if s == nil || s.db == nil {
		return Favorite{}, sql.ErrNoRows
	}
	if userID < 0 {
		userID = 0
	}
	var f Favorite
	err := s.db.QueryRowContext(ctx,
		"SELECT id, user_id, info_hash, title, magnet, size, size_bytes, seeders, leechers, sources, note, created_at, updated_at "+
			"FROM app_favorite WHERE user_id = ? AND info_hash = ? LIMIT 1",
		userID, strings.ToLower(strings.TrimSpace(infoHash)),
	).Scan(&f.ID, &f.UserID, &f.InfoHash, &f.Title, &f.Magnet, &f.Size, &f.SizeBytes,
		&f.Seeders, &f.Leechers, &f.Sources, &f.Note, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}
