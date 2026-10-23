package store

import (
	"context"
	"os"
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
	for _, table := range []string{"search_log", "app_user"} {
		if _, err := st.db.ExecContext(ctx, "TRUNCATE TABLE "+table); err != nil {
			t.Fatalf("清空 %s 失败: %v", table, err)
		}
	}
	return st
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

func TestAccountLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 初始没有账号
	if _, _, found, err := st.Account(ctx); err != nil || found {
		t.Fatalf("初始状态应无账号，实际 found=%v err=%v", found, err)
	}

	if err := st.SetAccount(ctx, "admin", []byte("hash-1")); err != nil {
		t.Fatalf("设置账号失败: %v", err)
	}
	name, hash, found, err := st.Account(ctx)
	if err != nil || !found {
		t.Fatalf("读取账号失败: found=%v err=%v", found, err)
	}
	if name != "admin" || string(hash) != "hash-1" {
		t.Errorf("账号 = %q / %q", name, hash)
	}

	// 再次设置应替换旧账号（只保留一个）
	if err := st.SetAccount(ctx, "boss", []byte("hash-2")); err != nil {
		t.Fatalf("更新账号失败: %v", err)
	}
	name, hash, _, _ = st.Account(ctx)
	if name != "boss" || string(hash) != "hash-2" {
		t.Errorf("更新后账号 = %q / %q", name, hash)
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
