package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// openTestStore 连接测试库。测试会清空表，因此必须指向独立的库
// （建议 msearch_test），不要指向生产库。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("MSEARCH_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 MSEARCH_TEST_DSN，跳过数据库测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := Open(ctx, Config{DSN: dsn})
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for _, table := range []string{"search_log", "search_history", "app_favorite", "app_user", "app_setting"} {
		if _, err := st.db.ExecContext(ctx, "TRUNCATE TABLE "+table); err != nil {
			t.Fatalf("清空 %s 失败: %v", table, err)
		}
	}
	return st
}

// TestBuildDSN 校验由拆分字段拼出的连接串内容，不连接数据库。
func TestBuildDSN(t *testing.T) {
	dsn, err := buildDSN(Config{
		Host:     "127.0.0.1",
		Port:     3307,
		Username: "root",
		Password: "p@ss:word",
		Database: "msearch",
	})
	if err != nil {
		t.Fatalf("拼接 DSN 失败: %v", err)
	}
	checks := []string{
		"root:p@ss:word@tcp(127.0.0.1:3307)/msearch",
		"parseTime=true",
		"charset=utf8mb4",
	}
	for _, want := range checks {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q 应包含 %q", dsn, want)
		}
	}

	// 端口为 0 时使用 3306
	dsn, err = buildDSN(Config{Host: "db.internal", Database: "msearch"})
	if err != nil {
		t.Fatalf("拼接 DSN 失败: %v", err)
	}
	if !strings.Contains(dsn, "tcp(db.internal:3306)/msearch") {
		t.Errorf("默认端口应为 3306，实际 DSN %q", dsn)
	}

	// 显式 DSN 优先
	dsn, err = buildDSN(Config{DSN: "root:pw@tcp(127.0.0.1:3306)/legacy"})
	if err != nil || dsn != "root:pw@tcp(127.0.0.1:3306)/legacy" {
		t.Errorf("显式 DSN 应原样返回: dsn=%q err=%v", dsn, err)
	}

	// 两种来源都为空时报错
	if _, err := buildDSN(Config{}); err == nil {
		t.Error("缺少连接信息时应报错")
	}
	if _, err := buildDSN(Config{Host: "127.0.0.1"}); err == nil {
		t.Error("缺少数据库名时应报错")
	}
}

func TestLogSearchRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	entry := SearchLog{
		UserID:      7,
		Query:       "葬送的芙莉莲",
		IP:          "203.0.113.7",
		UserAgent:   "Mozilla/5.0 test",
		Engines:     "apibay,bt4g",
		ResultCount: 42,
		ElapsedMS:   1234,
	}
	if err := st.LogSearch(ctx, entry); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	got, err := st.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(got))
	}
	r := got[0]
	if r.UserID != entry.UserID || r.Query != entry.Query || r.IP != entry.IP || r.Engines != entry.Engines {
		t.Errorf("记录内容不符: %+v", r)
	}
	if r.ResultCount != entry.ResultCount || r.ElapsedMS != entry.ElapsedMS {
		t.Errorf("数值不符: %+v", r)
	}
	if time.Since(r.CreatedAt) > time.Minute {
		t.Errorf("记录时间不合理: %v", r.CreatedAt)
	}
}

func TestUserLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	n, err := st.CountUsers(ctx)
	if err != nil || n != 0 {
		t.Fatalf("初始账号数应为 0，实际 n=%d err=%v", n, err)
	}

	if err := st.CreateUser(ctx, "admin", []byte("hash-1")); err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	name, hash, found, err := st.User(ctx, "admin")
	if err != nil || !found {
		t.Fatalf("读取账号失败: found=%v err=%v", found, err)
	}
	if name != "admin" || string(hash) != "hash-1" {
		t.Errorf("账号 = %q / %q", name, hash)
	}

	// 重复用户名应返回 ErrUserExists
	if err := st.CreateUser(ctx, "admin", []byte("hash-x")); !errors.Is(err, ErrUserExists) {
		t.Errorf("重复创建应返回 ErrUserExists，实际 %v", err)
	}

	// Upsert 只更新密码，不删除其他账号
	if err := st.CreateUser(ctx, "guest", []byte("hash-g")); err != nil {
		t.Fatalf("创建第二个账号失败: %v", err)
	}
	if err := st.UpsertUser(ctx, "admin", []byte("hash-2")); err != nil {
		t.Fatalf("Upsert 账号失败: %v", err)
	}
	name, hash, _, err = st.User(ctx, "admin")
	if err != nil || name != "admin" || string(hash) != "hash-2" {
		t.Errorf("Upsert 后账号 = %q / %q err=%v", name, hash, err)
	}
	if _, _, found, err := st.User(ctx, "guest"); err != nil || !found {
		t.Errorf("Upsert 不应删除其他账号: found=%v err=%v", found, err)
	}
	if n, err := st.CountUsers(ctx); err != nil || n != 2 {
		t.Errorf("账号数应为 2，实际 n=%d err=%v", n, err)
	}

	// 改名但保留密码
	if err := st.UpdateUser(ctx, "guest", "guest2", nil); err != nil {
		t.Fatalf("改名校验失败: %v", err)
	}
	if _, _, found, _ := st.User(ctx, "guest"); found {
		t.Error("旧用户名不应还能查到")
	}
	name, hash, found, err = st.User(ctx, "guest2")
	if err != nil || !found || string(hash) != "hash-g" {
		t.Errorf("改名后账号 = %q / %q found=%v err=%v", name, hash, found, err)
	}

	// 改名并改密码
	if err := st.UpdateUser(ctx, "guest2", "guest3", []byte("hash-g2")); err != nil {
		t.Fatalf("改名改密失败: %v", err)
	}
	_, hash, _, err = st.User(ctx, "guest3")
	if err != nil || string(hash) != "hash-g2" {
		t.Errorf("改名改密后哈希 = %q err=%v", hash, err)
	}

	// 改成已存在的用户名应返回 ErrUserExists
	if err := st.UpdateUser(ctx, "guest3", "admin", nil); !errors.Is(err, ErrUserExists) {
		t.Errorf("重名应返回 ErrUserExists，实际 %v", err)
	}

	// 兼容包装器：Account 返回第一个账号，SetAccount 不再清空其他账号
	if name, _, found, err := st.Account(ctx); err != nil || !found || name != "admin" {
		t.Errorf("Account 应返回第一个账号 admin: name=%q found=%v err=%v", name, found, err)
	}
	if err := st.SetAccount(ctx, "boss", []byte("hash-b")); err != nil {
		t.Fatalf("SetAccount 失败: %v", err)
	}
	if n, err := st.CountUsers(ctx); err != nil || n != 3 {
		t.Errorf("SetAccount 不应删除其他账号，期望 3 个，实际 n=%d err=%v", n, err)
	}
	if name, _, found, err := st.Account(ctx); err != nil || !found || name != "admin" {
		t.Errorf("SetAccount 不应改变第一个账号: name=%q found=%v err=%v", name, found, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 首次读取应写入并返回默认值
	got, err := st.Settings(ctx, AppSettings{RequireLogin: true, AllowRegister: false})
	if err != nil {
		t.Fatalf("读取默认设置失败: %v", err)
	}
	if !got.RequireLogin || got.AllowRegister {
		t.Errorf("默认设置 = %+v", got)
	}

	// 保存后重新读取
	want := AppSettings{RequireLogin: false, AllowRegister: true}
	if err := st.SaveSettings(ctx, want); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	got, err = st.Settings(ctx, AppSettings{RequireLogin: true, AllowRegister: false})
	if err != nil {
		t.Fatalf("重新读取设置失败: %v", err)
	}
	if got != want {
		t.Errorf("设置 = %+v，期望 %+v", got, want)
	}

	// 非法取值回退到默认值
	if _, err := st.db.ExecContext(ctx,
		"UPDATE app_setting SET `value` = 'oops' WHERE `key` = ?", settingRequireLogin); err != nil {
		t.Fatalf("写入非法值失败: %v", err)
	}
	got, err = st.Settings(ctx, AppSettings{RequireLogin: true, AllowRegister: false})
	if err != nil {
		t.Fatalf("读取设置失败: %v", err)
	}
	if !got.RequireLogin {
		t.Errorf("非法值应回退默认 true，实际 %+v", got)
	}
}

func TestSettingsNilStore(t *testing.T) {
	var st *Store
	defaults := AppSettings{RequireLogin: true, AllowRegister: false}
	got, err := st.Settings(context.Background(), defaults)
	if err != nil || got != defaults {
		t.Errorf("空存储应返回默认值: got=%+v err=%v", got, err)
	}
	if err := st.SaveSettings(context.Background(), defaults); err == nil {
		t.Error("空存储保存设置应报错")
	}
}

func TestLogSearchTruncatesLongQuery(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	long := make([]rune, 0, 700)
	for i := 0; i < 700; i++ {
		long = append(long, '字')
	}
	if err := st.LogSearch(ctx, SearchLog{Query: string(long)}); err != nil {
		t.Fatalf("超长关键词应被截断而不是报错: %v", err)
	}
	got, err := st.Recent(ctx, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("读取失败: %v", err)
	}
	if len([]rune(got[0].Query)) > 512 {
		t.Errorf("关键词未被截断: %d 字", len([]rune(got[0].Query)))
	}
}

// TestHistoryRoundTrip 校验搜索历史的去重、账号隔离与删除。
func TestHistoryRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, query := range []string{"Ubuntu", "ubuntu", "葬送的芙莉莲"} {
		if err := st.AddHistory(ctx, 1, query); err != nil {
			t.Fatalf("写入历史失败: %v", err)
		}
	}
	if err := st.AddHistory(ctx, 2, "matrix"); err != nil {
		t.Fatalf("写入另一个账号的历史失败: %v", err)
	}

	got, err := st.History(ctx, 1, 10)
	if err != nil {
		t.Fatalf("读取历史失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("大小写不同的同一关键词应去重，实际 %d 条: %+v", len(got), got)
	}
	if got[0].Query != "葬送的芙莉莲" {
		t.Errorf("最近一条历史 = %q，期望葬送的芙莉莲", got[0].Query)
	}
	other, err := st.History(ctx, 2, 10)
	if err != nil || len(other) != 1 || other[0].Query != "matrix" {
		t.Errorf("另一个账号的历史 = %+v err=%v", other, err)
	}

	removed, err := st.DeleteHistory(ctx, 1, "ubuntu")
	if err != nil || !removed {
		t.Fatalf("删除历史失败: removed=%v err=%v", removed, err)
	}
	got, _ = st.History(ctx, 1, 10)
	if len(got) != 1 {
		t.Errorf("删除后应只剩 1 条，实际 %d 条", len(got))
	}
	if err := st.ClearHistory(ctx, 1); err != nil {
		t.Fatalf("清空历史失败: %v", err)
	}
	got, _ = st.History(ctx, 1, 10)
	if len(got) != 0 {
		t.Errorf("清空后仍有 %d 条历史", len(got))
	}
}

// TestFavoriteLifecycle 校验收藏的新增、去重更新、修改与删除。
func TestFavoriteLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	f := Favorite{
		UserID:    1,
		InfoHash:  "232cd67eb3ffbd7c37bf9ec3ee887417e5ae1ee6",
		Title:     "Ubuntu Linux Bible",
		Magnet:    "magnet:?xt=urn:btih:232cd67eb3ffbd7c37bf9ec3ee887417e5ae1ee6",
		Size:      "8.2 MB",
		SizeBytes: 8554738,
		Seeders:   27,
		Leechers:  0,
		Sources:   "torrentscsv",
		Note:      "先留着",
	}
	id, err := st.AddFavorite(ctx, f)
	if err != nil || id <= 0 {
		t.Fatalf("新增收藏失败: id=%d err=%v", id, err)
	}
	// 同一账号重复收藏应更新元数据并保留备注
	f.Title = "Ubuntu Linux Bible 11ed"
	f.Seeders = 30
	again, err := st.AddFavorite(ctx, f)
	if err != nil || again != id {
		t.Fatalf("重复收藏应返回同一 ID: id=%d err=%v", again, err)
	}
	saved, err := st.FavoriteByHash(ctx, 1, f.InfoHash)
	if err != nil {
		t.Fatalf("读取收藏失败: %v", err)
	}
	if saved.Title != f.Title || saved.Seeders != 30 || saved.Note != "先留着" {
		t.Errorf("重复收藏后的记录 = %+v", saved)
	}
	// 另一个账号看不到这条收藏
	if list, err := st.Favorites(ctx, 2); err != nil || len(list) != 0 {
		t.Errorf("账号隔离失败: %+v err=%v", list, err)
	}

	updated, err := st.UpdateFavorite(ctx, 1, id, "新标题", "新备注")
	if err != nil || !updated {
		t.Fatalf("修改收藏失败: updated=%v err=%v", updated, err)
	}
	saved, _ = st.FavoriteByHash(ctx, 1, f.InfoHash)
	if saved.Title != "新标题" || saved.Note != "新备注" {
		t.Errorf("修改后的收藏 = %+v", saved)
	}
	// 不属于该账号的 ID 不能修改
	if updated, err := st.UpdateFavorite(ctx, 2, id, "越权", ""); err != nil || updated {
		t.Errorf("越权修改应失败: updated=%v err=%v", updated, err)
	}

	removed, err := st.DeleteFavorite(ctx, 1, id)
	if err != nil || !removed {
		t.Fatalf("按 ID 删除失败: removed=%v err=%v", removed, err)
	}
	if _, err := st.AddFavorite(ctx, f); err != nil {
		t.Fatalf("重新收藏失败: %v", err)
	}
	removed, err = st.DeleteFavoriteByHash(ctx, 1, f.InfoHash)
	if err != nil || !removed {
		t.Fatalf("按哈希删除失败: removed=%v err=%v", removed, err)
	}
}

// TestFavoriteAcceptsLongFields 校验超长标题、备注、来源与磁力链接都能完整写入。
// 这些值超过旧版字段容量，旧表会报 1406；同时验证启动迁移已把字段放宽。
func TestFavoriteAcceptsLongFields(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 旧 magnet 列是 TEXT（64KB 上限），这里用 70KB 确认已放宽到 MEDIUMTEXT。
	longMagnet := "magnet:?xt=urn:btih:232cd67eb3ffbd7c37bf9ec3ee887417e5ae1ee6&dn=" + strings.Repeat("a", 70000)
	f := Favorite{
		UserID:   1,
		InfoHash: "232cd67eb3ffbd7c37bf9ec3ee887417e5ae1ee6",
		Title:    strings.Repeat("t", 700),
		Magnet:   longMagnet,
		Size:     strings.Repeat("9", 60),
		Sources:  strings.Repeat("s", 500),
		Note:     strings.Repeat("n", 900),
	}
	if _, err := st.AddFavorite(ctx, f); err != nil {
		t.Fatalf("写入长字段收藏失败: %v", err)
	}
	saved, err := st.FavoriteByHash(ctx, 1, f.InfoHash)
	if err != nil {
		t.Fatalf("读取收藏失败: %v", err)
	}
	if saved.Title != f.Title || saved.Note != f.Note || saved.Sources != f.Sources ||
		saved.Size != f.Size || saved.Magnet != f.Magnet {
		t.Errorf("长字段被截断: title=%d note=%d sources=%d size=%d magnet=%d",
			len(saved.Title), len(saved.Note), len(saved.Sources), len(saved.Size), len(saved.Magnet))
	}

	longTitle := strings.Repeat("u", 900)
	if updated, err := st.UpdateFavorite(ctx, 1, saved.ID, longTitle, f.Note); err != nil || !updated {
		t.Fatalf("修改超长标题失败: updated=%v err=%v", updated, err)
	}
	saved, _ = st.FavoriteByHash(ctx, 1, f.InfoHash)
	if saved.Title != longTitle {
		t.Errorf("修改后标题长度 = %d，期望 %d", len(saved.Title), len(longTitle))
	}
}
