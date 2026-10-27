package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pigzhu/magnet-search/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// 会话相关参数。
const (
	// sessionCookie 是会话 cookie 名
	sessionCookie = "msearch_session"
	// sessionTTL 会话有效期
	sessionTTL = 7 * 24 * time.Hour
	// minPasswordLen 密码最短长度
	minPasswordLen = 6
	// minUsernameLen 用户名最短长度
	minUsernameLen = 2
)

// authState 是给界面的认证状态。
type authState struct {
	// DBEnabled 数据库是否启用（关闭时不需要登录、也不记录）
	DBEnabled bool `json:"db_enabled"`
	// DBAvailable 服务端是否配置了数据库连接（用于决定界面是否显示开关）
	DBAvailable bool `json:"db_available"`
	// NeedsSetup 还没有账号，需要先设置
	NeedsSetup bool `json:"needs_setup"`
	// LoggedIn 当前是否已登录
	LoggedIn bool `json:"logged_in"`
	// Username 当前登录的用户名
	Username string `json:"username,omitempty"`
	// RequireLogin 是否要求登录后才能检索
	RequireLogin bool `json:"require_login"`
	// AllowRegister 是否允许自助注册
	AllowRegister bool `json:"allow_register"`
}

// sessionIdentity 是会话里携带的账号信息。
type sessionIdentity struct {
	// UserID 账号 ID，未登录时不会出现在会话里
	UserID int64
	// Username 用户名
	Username string
}

// dbEnabled 判断数据库是否启用。
func (s *Server) dbEnabled() bool {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.store != nil && s.settings.DBEnabled
}

// requireLogin 判断是否要求登录后才能检索。
func (s *Server) requireLogin() bool {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.store != nil && s.settings.RequireLogin
}

// allowRegister 判断是否允许自助注册账号。
func (s *Server) allowRegister() bool {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.store != nil && s.settings.AllowRegister
}

// handleAuth 返回当前认证状态。
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	s.settingsMu.RLock()
	cur := s.settings
	s.settingsMu.RUnlock()

	state := authState{
		DBEnabled:     s.store != nil && cur.DBEnabled,
		DBAvailable:   s.store != nil,
		RequireLogin:  s.store != nil && cur.RequireLogin,
		AllowRegister: s.store != nil && cur.AllowRegister,
	}
	if !state.DBEnabled {
		writeJSON(w, http.StatusOK, state)
		return
	}
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	state.NeedsSetup = count == 0
	if name, ok := s.sessionUser(r); ok {
		state.LoggedIn = true
		state.Username = name
	}
	writeJSON(w, http.StatusOK, state)
}

// credentials 是登录与设置账号的请求体。
type credentials struct {
	// Username 用户名
	Username string `json:"username"`
	// Password 密码（仅用于校验或写入哈希）
	Password string `json:"password"`
}

// handleLogin 校验账号密码并下发会话 cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	if !s.dbEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "db_enabled": false})
		return
	}

	var in credentials
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不正确"})
		return
	}

	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if count == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "还没有设置账号，请先设置"})
		return
	}

	user, found, err := s.store.LookupUser(r.Context(), strings.TrimSpace(in.Username))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !found || bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(in.Password)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码不正确"})
		return
	}

	s.setSession(w, user.ID, user.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": user.Username})
}

// handleRegister 处理自助注册。
// 必须先启用数据库并打开注册开关，注册成功后直接登录。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	if !s.dbEnabled() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据库未启用，无法注册"})
		return
	}
	if !s.allowRegister() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "未开放注册"})
		return
	}

	var in credentials
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不正确"})
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if len([]rune(in.Username)) < minUsernameLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("用户名至少 %d 个字符", minUsernameLen)})
		return
	}
	if len(in.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("密码至少 %d 位", minPasswordLen)})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "密码处理失败"})
		return
	}
	if err := s.store.CreateUser(r.Context(), in.Username, hash); err != nil {
		if errors.Is(err, store.ErrUserExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "用户名已存在"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	user, found, err := s.store.LookupUser(r.Context(), in.Username)
	if err != nil || !found {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "注册成功但读取账号失败，请重新登录"})
		return
	}

	s.setSession(w, user.ID, user.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": user.Username})
}

// handleLogout 清除会话。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAccount 设置或修改登录账号。
// 还没有账号时允许直接设置（首次初始化）；已有账号时只能修改当前登录账号。
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 POST"})
		return
	}
	if !s.dbEnabled() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "数据库未启用，无法设置账号"})
		return
	}

	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var current string
	if count > 0 {
		name, ok := s.sessionUser(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录再修改账号"})
			return
		}
		current = name
	}

	var in credentials
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不正确"})
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if len([]rune(in.Username)) < minUsernameLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("用户名至少 %d 个字符", minUsernameLen)})
		return
	}

	// 首次设置必须给密码；已有账号时留空表示保持原密码
	var hash []byte
	if count == 0 || in.Password != "" {
		if len(in.Password) < minPasswordLen {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("密码至少 %d 位", minPasswordLen)})
			return
		}
		hash, err = bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "密码处理失败"})
			return
		}
	}

	if count == 0 {
		err = s.store.CreateUser(r.Context(), in.Username, hash)
	} else {
		err = s.store.UpdateUser(r.Context(), current, in.Username, hash)
	}
	if err != nil {
		if errors.Is(err, store.ErrUserExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "用户名已存在"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	user, found, err := s.store.LookupUser(r.Context(), in.Username)
	if err != nil || !found {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "账号已保存但读取失败，请重新登录"})
		return
	}

	s.setSession(w, user.ID, user.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": user.Username})
}

// requireAuth 在启用数据库、要求登录且已有账号时，要求请求带上有效会话。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.dbEnabled() {
			next(w, r)
			return
		}
		if !s.requireLogin() {
			next(w, r)
			return
		}
		count, err := s.store.CountUsers(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// 还没有账号时不拦，让界面先完成首次设置
		if count == 0 {
			next(w, r)
			return
		}
		if _, ok := s.sessionUser(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
			return
		}
		next(w, r)
	}
}

// setSession 下发签名会话 cookie，载荷里带上账号 ID 与用户名。
func (s *Server) setSession(w http.ResponseWriter, userID int64, username string) {
	expires := time.Now().Add(sessionTTL).Unix()
	payload := strconv.FormatInt(userID, 10) + "|" +
		strconv.FormatInt(expires, 10) + "|" +
		base64.RawURLEncoding.EncodeToString([]byte(username))
	sig := s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// sessionIdentity 校验会话 cookie，返回账号 ID 与用户名。
func (s *Server) sessionIdentity(r *http.Request) (sessionIdentity, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return sessionIdentity{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return sessionIdentity{}, false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return sessionIdentity{}, false
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || userID < 0 {
		return sessionIdentity{}, false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return sessionIdentity{}, false
	}
	// 先校验签名，再校验有效期，避免伪造请求触发额外解析
	signed := strings.Join(parts[:3], "|")
	if subtle.ConstantTimeCompare([]byte(parts[3]), []byte(s.sign(signed))) != 1 {
		return sessionIdentity{}, false
	}
	if time.Now().Unix() > expires {
		return sessionIdentity{}, false
	}
	nameRaw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return sessionIdentity{}, false
	}
	name := string(nameRaw)
	if name == "" {
		return sessionIdentity{}, false
	}
	return sessionIdentity{UserID: userID, Username: name}, true
}

// sessionUser 校验会话 cookie，返回登录用户名。
func (s *Server) sessionUser(r *http.Request) (string, bool) {
	identity, ok := s.sessionIdentity(r)
	if !ok {
		return "", false
	}
	return identity.Username, true
}

// currentUserID 返回当前账号 ID，未登录时为 0。
func (s *Server) currentUserID(r *http.Request) int64 {
	identity, ok := s.sessionIdentity(r)
	if !ok {
		return 0
	}
	return identity.UserID
}

// sign 用进程内的密钥对载荷做 HMAC-SHA256。
func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.authSecret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// newAuthSecret 生成进程内会话密钥（重启后旧会话失效）。
func newAuthSecret() []byte {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下退回固定长度的时间派生值，保证不 panic
		return []byte(strconv.FormatInt(time.Now().UnixNano(), 10))
	}
	return buf
}

// clientIP 取请求来源 IP，优先使用反向代理传来的真实地址。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return strings.TrimSpace(rip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
