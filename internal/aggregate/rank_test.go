package aggregate

import (
	"testing"
	"time"

	"github.com/pigzhu/magnet-search/internal/model"
)

// 构造一个候选：claimed 为引擎宣称的做种数，sources 为独立佐证引擎数。
func candidate(hash, title string, claimed int, sources int) Candidate {
	c := Candidate{
		Result: model.Result{
			Title:       title,
			InfoHash:    hash,
			Seeders:     claimed,
			Leechers:    model.Unknown,
			PublishDate: time.Now().Add(-24 * time.Hour),
			ListedAt:    time.Now(),
		},
		Live: model.NewLiveness(hash),
	}
	for i := 0; i < sources; i++ {
		c.Sources = append(c.Sources, string(rune('a'+i)))
	}
	return c
}

func TestJudgeStates(t *testing.T) {
	live := candidate("aa", "Foo 1080p", 0, 1)
	live.Live.TrackerComplete = 5
	live.Live.TrackerIncomplete = 2
	live.Live.Trackers = []string{"t1"}
	if v, _ := Judge(&live); v != VerdictAlive {
		t.Errorf("探测到 5 个做种应为活跃，实际 %v", v)
	}

	cold := candidate("bb", "Foo 1080p", 2, 2)
	if v, _ := Judge(&cold); v != VerdictCold {
		t.Errorf("2 个做种应为冷门，实际 %v", v)
	}

	dead := candidate("cc", "Foo 1080p", 0, 1)
	dead.Live.TrackerComplete = 0
	dead.Live.TrackerIncomplete = 0
	dead.Live.Trackers = []string{"t1"}
	if v, _ := Judge(&dead); v != VerdictDead {
		t.Errorf("探测明确返回 0 应判死链，实际 %v", v)
	}

	unknown := candidate("dd", "Foo 1080p", model.Unknown, 1)
	if v, conf := Judge(&unknown); v != VerdictUnknown || conf != 0 {
		t.Errorf("无任何证据应为未知且置信度为 0，实际 %v/%v", v, conf)
	}

	// 仅引擎宣称 0、且证据不足时不能判死
	fresh := candidate("ee", "Foo 1080p", 0, 1)
	fresh.Result.PublishDate = time.Now()
	if v, _ := Judge(&fresh); v != VerdictUnknown {
		t.Errorf("新资源宣称 0 做种不应判死，实际 %v", v)
	}

	// 多引擎一致报 0 且发布超过 7 天，才允许按元数据判死
	old := candidate("ff", "Foo 1080p", 0, 2)
	old.Result.PublishDate = time.Now().Add(-10 * 24 * time.Hour)
	if v, _ := Judge(&old); v != VerdictDead {
		t.Errorf("两引擎一致报 0 且发布 10 天应判死链，实际 %v", v)
	}
}

func TestRankColdBeatsUnverifiedPopular(t *testing.T) {
	now := time.Now()
	cold := candidate("11", "Ubuntu 24.04 Desktop", 3, 2)
	popular := candidate("22", "Ubuntu 24.04 Desktop", 500, 1)

	ranked := Rank([]Candidate{cold, popular}, RankOptions{
		Query:      "ubuntu 24.04",
		Limit:      10,
		DeadFilter: "hide",
		Now:        now,
	})
	if len(ranked.Kept) != 2 {
		t.Fatalf("应保留 2 条结果，实际 %d 条", len(ranked.Kept))
	}
	if ranked.Kept[0].Result.InfoHash != "11" {
		t.Errorf("冷门但有佐证的资源应排在未证实的热门资源之前，实际首位 %s（分数 %.4f）",
			ranked.Kept[0].Result.InfoHash, ranked.Kept[0].Score)
	}
}

func TestRankPopularBeatsColdWhenBothCorroborated(t *testing.T) {
	now := time.Now()
	cold := candidate("11", "Ubuntu 24.04 Desktop", 2, 2)
	popular := candidate("22", "Ubuntu 24.04 Desktop", 500, 2)

	ranked := Rank([]Candidate{cold, popular}, RankOptions{
		Query:      "ubuntu 24.04",
		Limit:      10,
		DeadFilter: "hide",
		Now:        now,
	})
	if ranked.Kept[0].Result.InfoHash != "22" {
		t.Errorf("同样有佐证时热门资源应领先，实际首位 %s", ranked.Kept[0].Result.InfoHash)
	}
}

func TestRankDeadFilteredButUnknownKept(t *testing.T) {
	now := time.Now()
	dead := candidate("11", "Ubuntu 24.04 Desktop", 0, 1)
	dead.Live.TrackerComplete = 0
	dead.Live.TrackerIncomplete = 0
	dead.Live.Trackers = []string{"t1"}
	unknown := candidate("22", "Ubuntu 24.04 Desktop", model.Unknown, 1)

	ranked := Rank([]Candidate{dead, unknown}, RankOptions{
		Query:      "ubuntu 24.04",
		Limit:      10,
		DeadFilter: "hide",
		Now:        now,
	})
	if len(ranked.Kept) != 1 || ranked.Kept[0].Result.InfoHash != "22" {
		t.Fatalf("应只保留未知状态的结果，实际 %+v", ranked.Kept)
	}
	if len(ranked.Dropped) != 1 {
		t.Errorf("死链应被记录为已过滤，实际 %d 条", len(ranked.Dropped))
	}
}

func TestRankRareQuotaKeepsColdResourceInTopK(t *testing.T) {
	now := time.Now()
	cands := []Candidate{}
	// 10 条热门资源
	for i := 0; i < 10; i++ {
		cands = append(cands, candidate(string(rune('a'+i)), "Ubuntu 24.04 Desktop", 900, 1))
	}
	// 1 条冷门但确实活着的资源
	cands = append(cands, candidate("zz", "Ubuntu 24.04 Desktop", 2, 2))

	ranked := Rank(cands, RankOptions{
		Query:        "ubuntu 24.04",
		Limit:        5,
		PerCluster:   0,
		RareQuota:    -1,
		RareMaxPeers: 4,
		DeadFilter:   "hide",
		Now:          now,
	})
	found := false
	for _, c := range ranked.Kept {
		if c.Result.InfoHash == "zz" {
			found = true
		}
	}
	if !found {
		t.Errorf("冷门资源应通过配额进入前 %d 条，实际结果: %+v", 5, ranked.Kept)
	}
}

func TestRankStrictKeepsMetadataOnlyDeath(t *testing.T) {
	now := time.Now()
	// 两个引擎一致报 0、发布超过 7 天：普通模式判死，strict 模式保留
	old := candidate("11", "Ubuntu 24.04 Desktop", 0, 2)
	old.Result.PublishDate = now.Add(-10 * 24 * time.Hour)

	normal := Rank([]Candidate{old}, RankOptions{Query: "ubuntu 24.04", DeadFilter: "hide", Now: now})
	if len(normal.Kept) != 0 {
		t.Errorf("hide 模式应过滤掉元数据判死的结果，实际保留 %d 条", len(normal.Kept))
	}

	strict := Rank([]Candidate{old}, RankOptions{Query: "ubuntu 24.04", DeadFilter: "strict", Now: now})
	if len(strict.Kept) != 1 {
		t.Errorf("strict 模式只认正面证据，应保留该结果，实际 %d 条", len(strict.Kept))
	}
}

func TestJudgeLastSeenEvidence(t *testing.T) {
	now := time.Now()

	// 索引站刚确认过有人做种：冷门但活着
	fresh := candidate("11", "Foo 1080p", model.Unknown, 1)
	fresh.Result.LastSeen = now.Add(-5 * time.Minute)
	if v, conf := Judge(&fresh); v != VerdictCold || conf <= 0 {
		t.Errorf("5 分钟前确认过应判冷门且有置信度，实际 %v/%v", v, conf)
	}

	// 6 小时前确认过：仍在存活区间
	hours := candidate("22", "Foo 1080p", model.Unknown, 1)
	hours.Result.LastSeen = now.Add(-5 * time.Hour)
	if v, _ := Judge(&hours); v != VerdictCold {
		t.Errorf("5 小时前确认过应判冷门，实际 %v", v)
	}

	// 12 小时前确认过：证据不够明确，记未知
	mid := candidate("33", "Foo 1080p", model.Unknown, 1)
	mid.Result.LastSeen = now.Add(-12 * time.Hour)
	if v, _ := Judge(&mid); v != VerdictUnknown {
		t.Errorf("12 小时前确认过应记未知，实际 %v", v)
	}

	// 超过一天没人确认：正面死亡证据
	old := candidate("44", "Foo 1080p", model.Unknown, 1)
	old.Result.LastSeen = now.Add(-72 * time.Hour)
	if v, _ := Judge(&old); v != VerdictDead {
		t.Errorf("72 小时前确认过应判死链，实际 %v", v)
	}

	// 引擎宣称有做种、且索引站刚确认过：置信度应高于单靠宣称
	claim := candidate("55", "Foo 1080p", 4, 1)
	_, baseConf := Judge(&claim)
	claim2 := candidate("55", "Foo 1080p", 4, 1)
	claim2.Result.LastSeen = now.Add(-2 * time.Minute)
	_, boostedConf := Judge(&claim2)
	if boostedConf <= baseConf {
		t.Errorf("最近确认时间应提升置信度：无佐证 %v，有佐证 %v", baseConf, boostedConf)
	}
}
