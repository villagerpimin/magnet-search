package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/store"
)

// historyItem 是搜索历史的一条记录。
type historyItem struct {
	// Query 关键词
	Query string `json:"query"`
	// UpdatedAt 最近一次搜索时间
	UpdatedAt string `json:"updated_at"`
}

// historyResponse 是搜索历史接口的响应。
type historyResponse struct {
	// DBEnabled 数据库是否启用
	DBEnabled bool `json:"db_enabled"`
	// Items 去重后的历史列表
	Items []historyItem `json:"items"`
}

// handleHistory 读取或删除当前账号的搜索历史。
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if !s.dbEnabled() {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, historyResponse{Items: []historyItem{}})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据库未启用，无法使用搜索历史"})
		return
	}
	userID := s.currentUserID(r)
	switch r.Method {
	case http.MethodGet:
		entries, err := s.store.History(r.Context(), userID, 50)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		items := make([]historyItem, 0, len(entries))
		for _, e := range entries {
			items = append(items, historyItem{Query: e.Query, UpdatedAt: e.UpdatedAt.Format(time.RFC3339)})
		}
		writeJSON(w, http.StatusOK, historyResponse{DBEnabled: true, Items: items})
	case http.MethodDelete:
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		if query == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少要删除的关键词"})
			return
		}
		removed, err := s.store.DeleteHistory(r.Context(), userID, query)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 GET 与 DELETE"})
	}
}

// handleHistoryClear 清空当前账号的全部搜索历史。
func (s *Server) handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	if !s.dbEnabled() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据库未启用，无法清空搜索历史"})
		return
	}
	if err := s.store.ClearHistory(r.Context(), s.currentUserID(r)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// favoriteItem 是收藏接口返回的一条记录。
type favoriteItem struct {
	// ID 收藏主键
	ID int64 `json:"id"`
	// InfoHash 40 位十六进制哈希
	InfoHash string `json:"info_hash"`
	// Title 资源标题
	Title string `json:"title"`
	// Magnet 磁力链接
	Magnet string `json:"magnet"`
	// Size 易读大小
	Size string `json:"size"`
	// SizeBytes 字节数
	SizeBytes int64 `json:"size_bytes"`
	// Seeders 做种数，-1 表示未知
	Seeders int `json:"seeders"`
	// Leechers 下载数，-1 表示未知
	Leechers int `json:"leechers"`
	// Sources 来源引擎
	Sources []string `json:"sources"`
	// Note 自定义备注
	Note string `json:"note"`
	// CreatedAt 收藏时间
	CreatedAt string `json:"created_at"`
	// UpdatedAt 最近更新时间
	UpdatedAt string `json:"updated_at"`
}

// favoritesResponse 是收藏列表接口的响应。
type favoritesResponse struct {
	// DBEnabled 数据库是否启用
	DBEnabled bool `json:"db_enabled"`
	// Items 收藏列表
	Items []favoriteItem `json:"items"`
}

// favoriteRequest 是新增收藏的请求体。
type favoriteRequest struct {
	InfoHash  string   `json:"info_hash"`
	Title     string   `json:"title"`
	Magnet    string   `json:"magnet"`
	Size      string   `json:"size"`
	SizeBytes int64    `json:"size_bytes"`
	Seeders   int      `json:"seeders"`
	Leechers  int      `json:"leechers"`
	Sources   []string `json:"sources"`
	Note      string   `json:"note"`
}

// favoriteUpdateRequest 是修改收藏的请求体。
type favoriteUpdateRequest struct {
	Title string `json:"title"`
	Note  string `json:"note"`
}

// handleFavorites 列出、新增或按哈希删除收藏。
func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	if !s.dbEnabled() {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, favoritesResponse{Items: []favoriteItem{}})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据库未启用，无法使用收藏"})
		return
	}
	userID := s.currentUserID(r)
	switch r.Method {
	case http.MethodGet:
		items, err := s.listFavorites(r, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, favoritesResponse{DBEnabled: true, Items: items})
	case http.MethodPost:
		var in favoriteRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不正确"})
			return
		}
		hash, ok := magnet.NormalizeInfoHash(in.InfoHash)
		if !ok || magnet.IsNullInfoHash(hash) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "info hash 不正确"})
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少资源标题"})
			return
		}
		uri := strings.TrimSpace(in.Magnet)
		if uri == "" {
			uri = magnet.Build(hash, title, nil)
		}
		if !strings.HasPrefix(strings.ToLower(uri), "magnet:") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "磁力链接格式不正确"})
			return
		}
		size := strings.TrimSpace(in.Size)
		if size == "" && in.SizeBytes > 0 {
			size = humanSize(in.SizeBytes)
		}
		if _, err := s.store.AddFavorite(r.Context(), store.Favorite{
			UserID:    userID,
			InfoHash:  hash,
			Title:     title,
			Magnet:    uri,
			Size:      size,
			SizeBytes: in.SizeBytes,
			Seeders:   in.Seeders,
			Leechers:  in.Leechers,
			Sources:   strings.Join(in.Sources, ","),
			Note:      strings.TrimSpace(in.Note),
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		saved, err := s.store.FavoriteByHash(r.Context(), userID, hash)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, newFavoriteItem(saved))
	case http.MethodDelete:
		hash, ok := magnet.NormalizeInfoHash(r.URL.Query().Get("hash"))
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "info hash 不正确"})
			return
		}
		removed, err := s.store.DeleteFavoriteByHash(r.Context(), userID, hash)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 GET、POST 与 DELETE"})
	}
}

// handleFavoriteItem 修改或删除单条收藏。
func (s *Server) handleFavoriteItem(w http.ResponseWriter, r *http.Request) {
	if !s.dbEnabled() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据库未启用，无法使用收藏"})
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/favorites/"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "收藏 ID 不正确"})
		return
	}
	userID := s.currentUserID(r)
	switch r.Method {
	case http.MethodPut:
		var in favoriteUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不正确"})
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "标题不能为空"})
			return
		}
		updated, err := s.store.UpdateFavorite(r.Context(), userID, id, title, strings.TrimSpace(in.Note))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !updated {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "收藏不存在"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "title": title, "note": strings.TrimSpace(in.Note)})
	case http.MethodDelete:
		removed, err := s.store.DeleteFavorite(r.Context(), userID, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !removed {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "收藏不存在"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 PUT 与 DELETE"})
	}
}

// listFavorites 读取收藏并转换成接口结构。
func (s *Server) listFavorites(r *http.Request, userID int64) ([]favoriteItem, error) {
	list, err := s.store.Favorites(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	items := make([]favoriteItem, 0, len(list))
	for _, f := range list {
		items = append(items, newFavoriteItem(f))
	}
	return items, nil
}

// newFavoriteItem 把存储层记录转成接口结构。
func newFavoriteItem(f store.Favorite) favoriteItem {
	sources := []string{}
	for _, s := range strings.Split(f.Sources, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			sources = append(sources, s)
		}
	}
	return favoriteItem{
		ID:        f.ID,
		InfoHash:  f.InfoHash,
		Title:     f.Title,
		Magnet:    f.Magnet,
		Size:      f.Size,
		SizeBytes: f.SizeBytes,
		Seeders:   f.Seeders,
		Leechers:  f.Leechers,
		Sources:   sources,
		Note:      f.Note,
		CreatedAt: f.CreatedAt.Format(time.RFC3339),
		UpdatedAt: f.UpdatedAt.Format(time.RFC3339),
	}
}
