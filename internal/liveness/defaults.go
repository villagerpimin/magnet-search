package liveness

// 默认的 tracker 与 DHT 引导节点，配置未指定时使用。
var (
	// DefaultHTTPTrackers 是常用的 HTTP/HTTPS tracker。
	DefaultHTTPTrackers = []string{
		"https://tracker.torrentbay.net/announce",
		"https://tracker.gbitt.info/announce",
		"https://tracker.torrent.eu.org/announce",
		"http://tracker.opentrackr.org:1337/announce",
		"http://tracker.openbittorrent.com/announce",
	}
	// DefaultUDPTrackers 是常用的 UDP tracker。
	DefaultUDPTrackers = []string{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.demonii.com:1337/announce",
		"udp://tracker.openbittorrent.com:6969/announce",
		"udp://tracker.torrent.eu.org:451/announce",
		"udp://explodie.org:6969/announce",
	}
	// DefaultDHTNodes 是常用的 DHT 引导节点，格式为 host:port。
	DefaultDHTNodes = []string{
		"dht.transmissionbt.com:6881",
		"router.bittorrent.com:6881",
		"router.utorrent.com:6881",
		"dht.libtorrent.org:25401",
		"router.bitcomet.com:6881",
	}
)
