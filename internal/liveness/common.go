package liveness

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	"github.com/pigzhu/magnet-search/internal/magnet"
	"github.com/pigzhu/magnet-search/internal/model"
)

// normalizeCount 把可选计数转成存活数据用的整型。
// 字段缺失时返回 model.Unknown，保留"没测到"与"测到是 0"的区别。
func normalizeCount(v int64, ok bool) int {
	if !ok {
		return model.Unknown
	}
	return int(v)
}

// newPeerID 生成 20 字节的 peer id，前缀模仿 Transmission 以便通过部分 tracker 的白名单。
func newPeerID() string {
	buf, err := randomBytes(6)
	if err != nil {
		// 随机数不可用属于极端情况，退化成固定后缀仍然满足 20 字节长度要求。
		return "-UT3550-000000000000"
	}
	return "-UT3550-" + fmt.Sprintf("%x", buf)
}

// shortError 把错误压成一行短摘要，避免诊断信息被超长 URL 淹没。
func shortError(err error) string {
	return truncate(err.Error(), maxShortError)
}

// truncate 按字节截断字符串，并在超出时补省略号。
// 截断点会回退到合法的 UTF-8 字符边界，避免切出半个汉字。
func truncate(msg string, limit int) string {
	if len(msg) <= limit {
		return msg
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + "…"
}

// decodeInfoHash 把 40 位十六进制（也兼容 base32）info hash 还原成 20 字节原始数据。
// 协议报文里 info hash 一定是这 20 个原始字节，不是十六进制文本。
func decodeInfoHash(hash string) ([]byte, error) {
	norm, ok := magnet.NormalizeInfoHash(hash)
	if !ok {
		return nil, fmt.Errorf("info hash 非法: %q", hash)
	}
	raw, err := hex.DecodeString(norm)
	if err != nil {
		return nil, fmt.Errorf("info hash 解码失败: %w", err)
	}
	return raw, nil
}

// randomBytes 返回 n 个密码学随机字节，用于 transaction id 与节点 id。
func randomBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("生成随机数失败: %w", err)
	}
	return buf, nil
}
