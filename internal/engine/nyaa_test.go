package engine

import (
	"context"
	"testing"

	"github.com/pigzhu/magnet-search/internal/model"
)

// TestNyaaSearchURL 校验 HTML 与 RSS 两个通道的查询地址与分类映射。
func TestNyaaSearchURL(t *testing.T) {
	nyaa := NewNyaa(nil)
	cases := []struct {
		name     string
		base     string
		query    string
		category string
		want     string
	}{
		{
			name:  "默认地址",
			base:  "https://nyaa.si",
			query: "ubuntu",
			want:  "https://nyaa.si/?f=0&c=0_0&q=ubuntu",
		},
		{
			name:  "关键词要转义",
			base:  "https://nyaa.si",
			query: "hello world",
			want:  "https://nyaa.si/?f=0&c=0_0&q=hello+world",
		},
		{
			name:     "音频分类",
			base:     "https://nyaa.si",
			query:    "flac",
			category: "audio",
			want:     "https://nyaa.si/?f=0&c=2_0&q=flac",
		},
		{
			name:     "软件分类",
			base:     "https://nyaa.si",
			query:    "ubuntu",
			category: "software",
			want:     "https://nyaa.si/?f=0&c=6_0&q=ubuntu",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nyaa.searchURL(tc.base, Options{Query: tc.query, Category: tc.category})
			if got != tc.want {
				t.Errorf("查询地址 = %q，期望 %q", got, tc.want)
			}
		})
	}
	rssWant := "https://nyaa.si/?f=0&c=0_0&q=ubuntu&page=rss"
	if got := nyaa.rssURL(Options{Query: "ubuntu"}); got != rssWant {
		t.Errorf("RSS 地址 = %q，期望 %q", got, rssWant)
	}
}

// TestNyaaVideoCategoryKeepsAll 校验 video 分类不做过滤，
// 因为站点把视频拆成动画与真人影视两类，过滤会漏掉一半结果。
func TestNyaaVideoCategoryKeepsAll(t *testing.T) {
	if got := nyaaCategoryCode("video"); got != "0_0" {
		t.Errorf("分类参数 = %q，期望 0_0", got)
	}
}

// TestNyaaBases 校验主站点在前、镜像在后。
func TestNyaaBases(t *testing.T) {
	bases := NewNyaa(nil).bases()
	if len(bases) != 2 {
		t.Fatalf("期望 2 个候选地址，实际 %d 个: %v", len(bases), bases)
	}
	if bases[0] != "https://nyaa.si" || bases[1] != "https://nyaa.ink" {
		t.Errorf("候选地址 = %v，期望 [https://nyaa.si https://nyaa.ink]", bases)
	}
}

// TestNyaaUnknownCounts 校验缺失的人数按 Unknown 处理，而不是当成 0。
func TestNyaaUnknownCounts(t *testing.T) {
	if got := nyaaNumber(""); got != model.Unknown {
		t.Errorf("缺失的人数 = %d，期望 %d", got, model.Unknown)
	}
	if got := nyaaNumber("abc"); got != model.Unknown {
		t.Errorf("非法的人数 = %d，期望 %d", got, model.Unknown)
	}
	if got := nyaaNumber(" 12 "); got != 12 {
		t.Errorf("人数 = %d，期望 12", got)
	}
}

// TestNyaaLive 访问真实站点做冒烟测试，需要 MS_LIVE=1。
func TestNyaaLive(t *testing.T) {
	requireLive(t)
	results, err := NewNyaa(liveClient(t)).Search(context.Background(), Options{Query: "ubuntu", Limit: 5})
	if err != nil {
		skipIfUnreachable(t, err)
	}
	if len(results) == 0 {
		t.Fatal("期望至少有 1 条结果")
	}
	for _, r := range results {
		if len(r.InfoHash) != 40 {
			t.Fatalf("info hash 长度 = %d，期望 40: %q", len(r.InfoHash), r.InfoHash)
		}
	}
	t.Logf("拿到 %d 条结果，例如 %s", len(results), results[0].Title)
}
