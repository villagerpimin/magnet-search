// Package model 定义各层共用的数据契约：搜索结果、存活探测数据、判定状态。
package model

import "time"

// Unknown 表示某项指标在本机无法获取（网络受限、数据源未提供），
// 必须与真实的 0 区分：0 个做种者是"探测到无活种"，Unknown 是"没测到"。
const Unknown = -1

// Result 是单个搜索引擎返回的一条归一化后的结果。
type Result struct {
	// Title 资源标题，保留原始文本（可能含发布组、画质、字幕组等信息）
	Title string
	// InfoHash 40 位十六进制，聚合去重的主键
	InfoHash string
	// MagnetURI 磁力链接，可能带有 tr= tracker 参数
	MagnetURI string
	// SizeBytes 文件总大小
	SizeBytes int64
	// Seeders 引擎页面宣称的做种数，可能陈旧或造假
	Seeders int
	// Leechers 引擎页面宣称的下载者数
	Leechers int
	// PublishDate 资源发布时间，引擎未提供时为零值
	PublishDate time.Time
	// LastSeen 索引站最近一次确认该资源存在于种子网络的时间，零值表示未知。
	// 这类"最近确认"信号比页面宣称的做种数更硬，可直接作为存活证据。
	LastSeen time.Time
	// ListedAt 本次抓取到该结果的时间，用于"最近仍在列表中"的弱信号
	ListedAt time.Time
	// Source 引擎标识，例如 apibay、tokyotosho
	Source string
	// URL 结果详情页地址
	URL string
	// Category 引擎给出的分类（视频/音频/软件等），部分引擎为空
	Category string
	// Rank 该结果在引擎返回列表中的位置（0 为第一条）。
	// 引擎自身已经完成过标题匹配，这个位置是独立于文本匹配的相关度信号，
	// 对中文查询检索日文/英文标题的海外资源尤其有用。
	Rank int
}

// Liveness 是对某个 info hash 的存活探测结果。
type Liveness struct {
	// InfoHash 被探测的哈希
	InfoHash string
	// TrackerComplete 来自 HTTP/UDP tracker 的 complete 计数，Unknown 表示不可用
	TrackerComplete int
	// TrackerIncomplete 来自 tracker 的 incomplete 计数，Unknown 表示不可用
	TrackerIncomplete int
	// DHTPeers 来自 DHT get_peers 的对等端数量，Unknown 表示不可用
	DHTPeers int
	// Trackers 实际有响应的 tracker 地址
	Trackers []string
	// Errors 探测过程中的错误摘要，用于诊断而不是判定
	Errors []string
	// ProbedAt 探测时间
	ProbedAt time.Time
}

// NewLiveness 返回一个全指标不可用的 Liveness。
func NewLiveness(infoHash string) Liveness {
	return Liveness{
		InfoHash:          infoHash,
		TrackerComplete:   Unknown,
		TrackerIncomplete: Unknown,
		DHTPeers:          Unknown,
		ProbedAt:          time.Now(),
	}
}

// Seeders 返回 tracker 视角的做种数，tracker 不可用时回退到 Unknown。
func (l Liveness) Seeders() int {
	if l.TrackerComplete != Unknown {
		return l.TrackerComplete
	}
	return Unknown
}

// Peers 返回 tracker 视角的在线对等端总数（做种 + 下载）。
func (l Liveness) Peers() int {
	if l.TrackerComplete == Unknown && l.TrackerIncomplete == Unknown {
		return Unknown
	}
	seeders, leechers := 0, 0
	if l.TrackerComplete != Unknown {
		seeders = l.TrackerComplete
	}
	if l.TrackerIncomplete != Unknown {
		leechers = l.TrackerIncomplete
	}
	return seeders + leechers
}

// HasNetworkProbe 表示至少有一个网络层探测源返回过数据。
func (l Liveness) HasNetworkProbe() bool {
	return l.TrackerComplete != Unknown || l.TrackerIncomplete != Unknown || l.DHTPeers != Unknown
}
