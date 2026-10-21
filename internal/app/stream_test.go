package app

import (
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/model"
)

// TestMergerOnlyEmitsFilteredItems 校验流式产出只给出通过最终判定的条目。
// 中途展示的条目若包含最终会被过滤掉的死链，界面就会先给用户一个假结果。
func TestMergerOnlyEmitsFilteredItems(t *testing.T) {
	now := time.Now()
	m := &merger{
		seen:       make(map[string]bool),
		deadFilter: "hide",
	}

	dead := model.Result{
		Title:       "Dead One 1080p",
		InfoHash:    "1111111111111111111111111111111111111111",
		Seeders:     0,
		Leechers:    0,
		Source:      "apibay",
		PublishDate: now.Add(-72 * time.Hour),
		LastSeen:    now.Add(-72 * time.Hour),
	}
	alive := model.Result{
		Title:       "Alive One 1080p",
		InfoHash:    "2222222222222222222222222222222222222222",
		Seeders:     5,
		Leechers:    1,
		Source:      "apibay",
		PublishDate: now.Add(-time.Hour),
	}

	fresh := m.add("alive one", []model.Result{dead, alive})
	if len(fresh) != 1 {
		t.Fatalf("期望只产出 1 条（死链被过滤），实际 %d 条", len(fresh))
	}
	if fresh[0].Result.Title != "Alive One 1080p" {
		t.Errorf("产出的是 %q，应该是存活的资源", fresh[0].Result.Title)
	}
}

// TestMergerAppliesMinPeers 校验流式产出同样遵守"最少对等端"。
func TestMergerAppliesMinPeers(t *testing.T) {
	m := &merger{seen: make(map[string]bool), deadFilter: "hide", minPeers: 3}

	low := model.Result{
		Title:    "Low One 1080p",
		InfoHash: "3333333333333333333333333333333333333333",
		Seeders:  1,
		Source:   "apibay",
	}
	high := model.Result{
		Title:    "High One 1080p",
		InfoHash: "4444444444444444444444444444444444444444",
		Seeders:  9,
		Source:   "apibay",
	}

	fresh := m.add("one", []model.Result{low, high})
	if len(fresh) != 1 || fresh[0].Result.Title != "High One 1080p" {
		t.Fatalf("期望只产出对等端达标的 1 条，实际 %+v", fresh)
	}
}
