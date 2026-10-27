package torrent

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"

	"github.com/pigzhu/magnet-search/internal/liveness/bencode"
)

// buildTorrent 用 bencode 编码器拼一个种子文件，并返回预期的 info hash。
// 编码器对字典键排序，因此单独编码 info 字典得到的字节就是文件里的原样区间。
func buildTorrent(t *testing.T, info bencode.Value) (data []byte, infoHash string) {
	t.Helper()
	rawInfo, err := bencode.Encode(info)
	if err != nil {
		t.Fatalf("编码 info 字典失败: %v", err)
	}
	root := bencode.NewDict(map[string]bencode.Value{
		"announce": bencode.NewString("http://tracker.example/announce"),
		"info":     info,
	})
	data, err = bencode.Encode(root)
	if err != nil {
		t.Fatalf("编码种子失败: %v", err)
	}
	sum := sha1.Sum(rawInfo)
	return data, hex.EncodeToString(sum[:])
}

// TestParseSingleFile 校验单文件种子的名称、大小与哈希。
func TestParseSingleFile(t *testing.T) {
	info := bencode.NewDict(map[string]bencode.Value{
		"name":         bencode.NewString("ubuntu.iso"),
		"length":       bencode.NewInt(4096),
		"piece length": bencode.NewInt(16384),
		"pieces":       bencode.NewBytes(make([]byte, 20)),
	})
	data, wantHash := buildTorrent(t, info)

	meta, err := Parse(data)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if meta.InfoHash != wantHash {
		t.Errorf("info hash = %q，期望 %q", meta.InfoHash, wantHash)
	}
	if meta.Name != "ubuntu.iso" {
		t.Errorf("名称 = %q，期望 ubuntu.iso", meta.Name)
	}
	if meta.TotalBytes != 4096 {
		t.Errorf("总大小 = %d，期望 4096", meta.TotalBytes)
	}
	if len(meta.Files) != 1 || meta.Files[0].Path != "ubuntu.iso" || meta.Files[0].SizeBytes != 4096 {
		t.Errorf("文件列表 = %+v", meta.Files)
	}
	if meta.FileCount != 1 || meta.Truncated {
		t.Errorf("文件计数 = %d，截断 = %v", meta.FileCount, meta.Truncated)
	}
	if len(meta.Trackers) != 1 || meta.Trackers[0] != "http://tracker.example/announce" {
		t.Errorf("tracker 列表 = %v", meta.Trackers)
	}
}

// TestParseMultiFile 校验多文件种子的路径拼接与总大小。
func TestParseMultiFile(t *testing.T) {
	info := bencode.NewDict(map[string]bencode.Value{
		"name": bencode.NewString("collection"),
		"files": bencode.NewList([]bencode.Value{
			bencode.NewDict(map[string]bencode.Value{
				"length": bencode.NewInt(100),
				"path": bencode.NewList([]bencode.Value{
					bencode.NewString("第一季"),
					bencode.NewString("01.mp4"),
				}),
			}),
			bencode.NewDict(map[string]bencode.Value{
				"length": bencode.NewInt(200),
				"path": bencode.NewList([]bencode.Value{
					bencode.NewString("第一季"),
					bencode.NewString("02.mp4"),
				}),
			}),
			bencode.NewDict(map[string]bencode.Value{
				"length": bencode.NewInt(300),
				"path":   bencode.NewList([]bencode.Value{}),
			}),
		}),
		"piece length": bencode.NewInt(16384),
		"pieces":       bencode.NewBytes(make([]byte, 20)),
	})
	data, wantHash := buildTorrent(t, info)

	meta, err := Parse(data)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if meta.InfoHash != wantHash {
		t.Errorf("info hash = %q，期望 %q", meta.InfoHash, wantHash)
	}
	if meta.TotalBytes != 600 {
		t.Errorf("总大小 = %d，期望 600", meta.TotalBytes)
	}
	if meta.FileCount != 3 {
		t.Fatalf("文件数 = %d，期望 3", meta.FileCount)
	}
	if meta.Files[0].Path != "第一季/01.mp4" || meta.Files[0].SizeBytes != 100 {
		t.Errorf("第一个文件 = %+v", meta.Files[0])
	}
	if meta.Files[1].Path != "第一季/02.mp4" {
		t.Errorf("第二个文件路径 = %q", meta.Files[1].Path)
	}
	if meta.Files[2].Path != "文件 3" {
		t.Errorf("空路径应生成兜底名称，实际 %q", meta.Files[2].Path)
	}
}

// TestParseRejectsInvalid 校验非法输入返回错误而不是 panic。
func TestParseRejectsInvalid(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("not-bencode"),
		[]byte("d8:announce3:fooe"), // 缺少 info
		[]byte("d4:infoi1ee"),       // info 不是字典
	}
	for _, data := range cases {
		if _, err := Parse(data); err == nil {
			t.Errorf("Parse(%q) 应返回错误", data)
		}
	}
}
