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
	for _, table := range []string{"search_log", "app_user", "app_setting"} {
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
	if r.Query != entry.Query || r.IP != entry.IP || r.Engines != entry.Engines {
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
