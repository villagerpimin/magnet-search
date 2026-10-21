package aggregate

import (
	"math"
	"time"

	"github.com/pigzhu/magnet-search/internal/model"
)

// 存活判定与置信度所用的参数。
const (
	// alivePeersThreshold 达到该做种数即视为活跃
	alivePeersThreshold = 3
	// metadataDeathAgeDays 纯元数据判定为死链所需的最少天数
	metadataDeathAgeDays = 7
	// metadataDeathMinSources 纯元数据判定为死链所需的最少独立引擎数
	metadataDeathMinSources = 2
	// maxConfidence 置信度上限，永不声称完全确定
	maxConfidence = 0.95
)

// 证据来源的半衰期：超过该时长未刷新，证据对"当下是否仍有人做种"的支撑力减半。
const (
	// halfLifeTracker tracker 计数的半衰期
	halfLifeTracker = 30 * time.Minute
	// halfLifeDHT DHT 对等端计数的半衰期
	halfLifeDHT = 15 * time.Minute
	// halfLifeEngine 引擎宣称做种数的半衰期
	halfLifeEngine = 6 * time.Hour
	// durabilityTauDays 资源存续衰减的时间常数（天）
	durabilityTauDays = 120
)

// Judge 依据网络探测与引擎宣称，给出存活判定与置信度。
//
// 判定原则：只有"确实测到没有对等端"才允许判为死链；
// "测不到"（探测不可用或无数据）一律记为 Unknown，绝不等同于死。
func Judge(c *Candidate) (Verdict, float64) {
	return judge(c, true)
}

// JudgeStrict 只认正面死亡证据：即使多引擎一致报 0，也不据此判死。
// 用于 --dead-filter strict 模式，避免误杀只在私有 tracker 上活跃的资源。
func JudgeStrict(c *Candidate) (Verdict, float64) {
	return judge(c, false)
}

// judge 是判定实现，allowMetadataDeath 控制是否允许用元数据判死。
func judge(c *Candidate, allowMetadataDeath bool) (Verdict, float64) {
	now := time.Now()
	s, l, source, ok := peerEvidence(c)
	if !ok {
		return VerdictUnknown, 0
	}

	base, halfLife, observedAt := sourceConfidence(c, source, now)
	if base <= 0 {
		return VerdictUnknown, 0
	}

	// 引擎宣称与探测结果互相矛盾时降低置信度
	conflict := 1.0
	if hasConflict(c, source, s) {
		conflict = 0.8
	}

	lag := now.Sub(observedAt)
	if lag < 0 {
		lag = 0
	}
	freshness := math.Pow(2, -lag.Seconds()/halfLife.Seconds())

	durability := durabilityFactor(c, now)

	conf := clamp(base*freshness*durability*conflict, 0, maxConfidence)

	if source == evidenceLastSeen {
		// 只有"最近确认时间"这一条证据时，按它的新旧决定冷门还是死链
		age := now.Sub(c.Result.LastSeen)
		switch {
		case age <= lastSeenAliveWithin:
			// 最近还确认过有人做种：冷门但活着
			return VerdictCold, conf
		case age <= lastSeenDeadAfter:
			return VerdictUnknown, conf * 0.6
		default:
			// 超过一天没有任何人做种：正面死亡证据
			return VerdictDead, conf * 0.9
		}
	}

	switch {
	case s >= alivePeersThreshold:
		return VerdictAlive, conf
	case s >= 1:
		return VerdictCold, conf
	case l >= 1:
		// 有下载者但无做种者：种子已失效，勉强可下但随时可能彻底失败
		return VerdictCold, conf * 0.9
	}

	// 走到这里说明做种与下载者都为 0
	if source == evidenceProbe {
		// 探测成功且明确返回 0：这是死亡的正面证据
		return VerdictDead, conf
	}
	// 仅靠引擎宣称的 0：需要多引擎一致且资源足够老，才敢判死
	if allowMetadataDeath {
		ageDays := ageInDays(c, now)
		if ageDays >= metadataDeathAgeDays && len(c.Sources) >= metadataDeathMinSources {
			return VerdictDead, conf * 0.85
		}
	}
	return VerdictUnknown, conf * 0.5
}

// 证据来源类型。
type evidenceKind int

const (
	// evidenceNone 没有任何可用证据
	evidenceNone evidenceKind = iota
	// evidenceProbe 来自 tracker 或 DHT 的网络探测
	evidenceProbe
	// evidenceEngine 仅来自搜索引擎页面宣称的计数
	evidenceEngine
	// evidenceLastSeen 来自索引站的"最近确认时间"
	evidenceLastSeen
)

// 索引站"最近确认时间"的判定阈值。
const (
	// lastSeenFreshWithin 多久之内算"刚刚还活着"
	lastSeenFreshWithin = 30 * time.Minute
	// lastSeenAliveWithin 多久之内还算活着（冷门但没死）
	lastSeenAliveWithin = 6 * time.Hour
	// lastSeenDeadAfter 超过这个时长没有任何确认，才判死
	lastSeenDeadAfter = 24 * time.Hour
)

// peerEvidence 提取可信的对等端计数。
// 优先级：tracker 探测 > DHT 探测 > 引擎宣称。
func peerEvidence(c *Candidate) (seeders, leechers int, source evidenceKind, ok bool) {
	if c.Live.TrackerComplete != model.Unknown {
		s := c.Live.TrackerComplete
		l := 0
		if c.Live.TrackerIncomplete != model.Unknown {
			l = c.Live.TrackerIncomplete
		}
		return s, l, evidenceProbe, true
	}
	if c.Live.DHTPeers != model.Unknown {
		return c.Live.DHTPeers, 0, evidenceProbe, true
	}
	if c.Result.Seeders != model.Unknown && c.Result.Seeders > 0 {
		l := 0
		if c.Result.Leechers != model.Unknown {
			l = c.Result.Leechers
		}
		return c.Result.Seeders, l, evidenceEngine, true
	}
	// 引擎没给做种数或宣称是 0 时，看索引站的"最近确认时间"：
	// 它比宣称做种数更硬——它说明那时确实有人在做种。
	if !c.Result.LastSeen.IsZero() {
		return 1, 0, evidenceLastSeen, true
	}
	if c.Result.Seeders == 0 {
		return 0, 0, evidenceEngine, true
	}
	return 0, 0, evidenceNone, false
}

// sourceConfidence 给出证据来源的基础置信度、半衰期与观测时间。
func sourceConfidence(c *Candidate, source evidenceKind, now time.Time) (base float64, halfLife time.Duration, observedAt time.Time) {
	switch source {
	case evidenceProbe:
		observedAt = c.Live.ProbedAt
		if observedAt.IsZero() {
			observedAt = now
		}
		if c.Live.TrackerComplete != model.Unknown {
			halfLife = halfLifeTracker
			base = 0.90
			// 多个 tracker 同时确认，置信度更高
			if len(c.Live.Trackers) >= 2 {
				base = 0.95
			}
			return base, halfLife, observedAt
		}
		halfLife = halfLifeDHT
		base = 0.60
		return base, halfLife, observedAt
	case evidenceLastSeen:
		// 最近确认时间的观测点就是它本身，衰减交给下面的分支按年龄处理
		observedAt = now
		halfLife = halfLifeEngine
		age := now.Sub(c.Result.LastSeen)
		switch {
		case age <= lastSeenFreshWithin:
			base = 0.70
		case age <= lastSeenAliveWithin:
			base = 0.55
		case age <= lastSeenDeadAfter:
			base = 0.40
		default:
			base = 0.60
		}
		return base, halfLife, observedAt
	case evidenceEngine:
		observedAt = c.Result.ListedAt
		if observedAt.IsZero() {
			observedAt = now
		}
		halfLife = halfLifeEngine
		// 多引擎独立佐证比单一引擎的宣称可信得多
		switch k := len(c.Sources); {
		case k >= 4:
			base = 0.80
		case k == 3:
			base = 0.75
		case k == 2:
			base = 0.65
		default:
			base = 0.45
		}
		// 索引站刚刚确认过，等于又多一份独立佐证
		if !c.Result.LastSeen.IsZero() && now.Sub(c.Result.LastSeen) <= lastSeenFreshWithin {
			base += 0.15
			if base > 0.90 {
				base = 0.90
			}
		}
		return base, halfLife, observedAt
	default:
		return 0, 0, now
	}
}

// hasConflict 判断引擎宣称与探测结果是否互相矛盾。
func hasConflict(c *Candidate, source evidenceKind, probePeers int) bool {
	if source != evidenceProbe {
		return false
	}
	if c.Result.Seeders == model.Unknown {
		return false
	}
	claimed := c.Result.Seeders
	if claimed > 0 && probePeers == 0 {
		return true
	}
	if claimed == 0 && probePeers > 0 {
		return true
	}
	return false
}

// durabilityFactor 资源存续因子：越老的资源越可能已经失去对等端。
func durabilityFactor(c *Candidate, now time.Time) float64 {
	age := ageInDays(c, now)
	if age < 0 {
		// 发布时间未知，不做过度惩罚
		return 0.75
	}
	return 0.55 + 0.45*math.Exp(-age/durabilityTauDays)
}

// ageInDays 返回资源发布至今的天数，未知时返回 -1。
func ageInDays(c *Candidate, now time.Time) float64 {
	if c.Result.PublishDate.IsZero() {
		return -1
	}
	return now.Sub(c.Result.PublishDate).Hours() / 24
}

// CrediblePeers 返回用于排序的可信对等端数量。
// 无做种者但有下载者时记为 1，表示"勉强还活着"。
func CrediblePeers(c *Candidate) int {
	s, l, _, ok := peerEvidence(c)
	if !ok {
		return 0
	}
	if s > 0 {
		return s
	}
	if l > 0 {
		return 1
	}
	return 0
}

// clamp 把数值限制到 [lo, hi] 区间。
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
