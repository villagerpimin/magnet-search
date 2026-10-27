// Package torrent 解析 .torrent 元数据，供搜索引擎与文件列表接口复用。
//
// 这里只依赖项目内的 bencode 解码器：一边用结构化解码读取文件名与文件列表，
// 一边在原始字节上定位 info 字典，按 BitTorrent 规范计算 info hash。
package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// MaxFiles 是返回给界面的文件条数上限，避免超大种子把响应撑爆。
const MaxFiles = 500

// maxScanDepth 限制扫描嵌套深度，防止畸形种子把栈压爆。
const maxScanDepth = 64

// File 是种子里的一个文件。
type File struct {
	// Path 文件路径，多文件种子用 "/" 拼接
	Path string `json:"path"`
	// SizeBytes 文件字节数
	SizeBytes int64 `json:"size_bytes"`
}

// Metainfo 是从 .torrent 中提取的展示信息。
type Metainfo struct {
	// InfoHash 40 位十六进制 info hash
	InfoHash string `json:"info_hash"`
	// Name 种子名称
	Name string `json:"name"`
	// TotalBytes 所有文件的总字节数
	TotalBytes int64 `json:"total_bytes"`
	// Files 文件列表，超过 MaxFiles 时只保留前 MaxFiles 条
	Files []File `json:"files"`
	// FileCount 文件总数（截断前）
	FileCount int `json:"file_count"`
	// Truncated 文件列表是否被截断
	Truncated bool `json:"truncated"`
	// Trackers 种子自带的 tracker 地址
	Trackers []string `json:"trackers,omitempty"`
}

// Parse 解析 .torrent 字节并提取 info hash 与文件列表。
func Parse(data []byte) (*Metainfo, error) {
	root, err := bencode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("种子文件解析失败: %w", err)
	}
	info, ok := bencode.Dict(root, "info")
	if !ok || info.Kind() != bencode.KindDict {
		return nil, errors.New("种子文件缺少 info 字典")
	}
	rawInfo, err := infoRaw(data)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(rawInfo)

	meta := &Metainfo{InfoHash: hex.EncodeToString(sum[:])}
	if name, ok := bencode.String(info, "name"); ok {
		meta.Name = strings.TrimSpace(name)
	}
	if meta.Name == "" {
		meta.Name = "未命名种子"
	}
	meta.Trackers = torrentTrackers(root)

	// 多文件种子用 files 列表；单文件种子直接给 length。
	if list, ok := bencode.List(info, "files"); ok {
		for _, item := range list {
			length, _ := bencode.Int(item, "length")
			path := filePath(item)
			if path == "" {
				path = fmt.Sprintf("文件 %d", len(meta.Files)+1)
			}
			meta.Files = append(meta.Files, File{Path: path, SizeBytes: length})
			if length > 0 {
				meta.TotalBytes += length
			}
		}
	} else if length, ok := bencode.Int(info, "length"); ok {
		meta.Files = append(meta.Files, File{Path: meta.Name, SizeBytes: length})
		if length > 0 {
			meta.TotalBytes = length
		}
	}

	meta.FileCount = len(meta.Files)
	if len(meta.Files) > MaxFiles {
		meta.Files = meta.Files[:MaxFiles]
		meta.Truncated = true
	}
	if meta.Files == nil {
		meta.Files = []File{}
	}
	return meta, nil
}

// torrentTrackers 读取种子自带的 announce 与 announce-list，去重后返回。
func torrentTrackers(root bencode.Value) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			return
		}
		seen[raw] = true
		out = append(out, raw)
	}
	if announce, ok := bencode.String(root, "announce"); ok {
		add(announce)
	}
	if tiers, ok := bencode.List(root, "announce-list"); ok {
		for _, tier := range tiers {
			urls, ok := tier.List()
			if !ok {
				continue
			}
			for _, u := range urls {
				if raw, ok := u.Bytes(); ok {
					add(string(raw))
				}
			}
		}
	}
	return out
}

// filePath 读取多文件种子里的 path 列表并拼成可读路径。
func filePath(item bencode.Value) string {
	parts, ok := bencode.List(item, "path")
	if !ok {
		return ""
	}
	var out []string
	for _, part := range parts {
		raw, ok := part.Bytes()
		if !ok {
			continue
		}
		name := strings.TrimSpace(string(raw))
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return strings.Join(out, "/")
}

// infoRaw 在原始字节中定位顶层 info 字典，返回它的完整字节区间。
//
// info hash 必须对原始编码计算，不能重新编码后再算：
// 种子里的键顺序、整数写法都是哈希的一部分，重新编码会改变结果。
func infoRaw(data []byte) ([]byte, error) {
	pos := 0
	if len(data) == 0 || data[pos] != 'd' {
		return nil, errors.New("种子文件不是 bencode 字典")
	}
	pos++
	for pos < len(data) {
		if data[pos] == 'e' {
			break
		}
		key, next, err := scanBytes(data, pos)
		if err != nil {
			return nil, err
		}
		start := next
		end, err := skipValue(data, start, 0)
		if err != nil {
			return nil, err
		}
		if string(key) == "info" {
			return data[start:end], nil
		}
		pos = end
	}
	return nil, errors.New("种子文件缺少 info 字典")
}

// scanBytes 跳过一个 bencode 字节串并返回内容与结束位置。
func scanBytes(data []byte, pos int) ([]byte, int, error) {
	start := pos
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	if pos == start || pos >= len(data) || data[pos] != ':' {
		return nil, 0, fmt.Errorf("种子文件在偏移 %d 处的字节串格式非法", start)
	}
	length, err := strconv.Atoi(string(data[start:pos]))
	if err != nil || length < 0 {
		return nil, 0, fmt.Errorf("种子文件在偏移 %d 处的字节串长度非法", start)
	}
	pos++
	if length > len(data)-pos {
		return nil, 0, fmt.Errorf("种子文件在偏移 %d 处的字节串越界", start)
	}
	return data[pos : pos+length], pos + length, nil
}

// skipValue 跳过一个 bencode 值，返回结束位置。
func skipValue(data []byte, pos, depth int) (int, error) {
	if depth > maxScanDepth {
		return 0, errors.New("种子文件嵌套层级过深")
	}
	if pos >= len(data) {
		return 0, errors.New("种子文件意外结束")
	}
	switch c := data[pos]; {
	case c == 'i':
		end := bytes.IndexByte(data[pos:], 'e')
		if end < 0 {
			return 0, fmt.Errorf("种子文件在偏移 %d 处的整数未闭合", pos)
		}
		return pos + end + 1, nil
	case c >= '0' && c <= '9':
		_, next, err := scanBytes(data, pos)
		return next, err
	case c == 'l':
		pos++
		for pos < len(data) && data[pos] != 'e' {
			next, err := skipValue(data, pos, depth+1)
			if err != nil {
				return 0, err
			}
			pos = next
		}
		if pos >= len(data) {
			return 0, fmt.Errorf("种子文件在偏移 %d 处的列表未闭合", pos)
		}
		return pos + 1, nil
	case c == 'd':
		pos++
		for pos < len(data) && data[pos] != 'e' {
			_, next, err := scanBytes(data, pos)
			if err != nil {
				return 0, err
			}
			next, err = skipValue(data, next, depth+1)
			if err != nil {
				return 0, err
			}
			pos = next
		}
		if pos >= len(data) {
			return 0, fmt.Errorf("种子文件在偏移 %d 处的字典未闭合", pos)
		}
		return pos + 1, nil
	default:
		return 0, fmt.Errorf("种子文件在偏移 %d 处出现非法类型标记 %q", pos, c)
	}
}
