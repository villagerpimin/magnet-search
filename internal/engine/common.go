package engine

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/pigzhu/magnet-search/internal/model"
)

// gzipMagic 是 gzip 流固定的头两个字节。
var gzipMagic = [2]byte{0x1f, 0x8b}

// decodeBody 在响应体是 gzip 压缩时先解压，返回原始字节。
//
// 共享客户端 httpx.Client 显式设置了 Accept-Encoding: gzip，
// 而 Go 的 http.Transport 只有在自己补上该请求头时才会自动解压响应体，
// 因此引擎适配器必须自己识别并解压压缩过的响应。
func decodeBody(body []byte) ([]byte, error) {
	if len(body) < len(gzipMagic) || body[0] != gzipMagic[0] || body[1] != gzipMagic[1] {
		return body, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gzip 响应解压失败: %w", err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gzip 响应解压失败: %w", err)
	}
	return raw, nil
}

// limitResults 按 Options.Limit 裁剪结果，limit 为 0 时不限制。
func limitResults(results []model.Result, limit int) []model.Result {
	if limit > 0 && len(results) > limit {
		return results[:limit]
	}
	return results
}

// resolveURL 把页面里的链接拼成绝对地址。
func resolveURL(base, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimPrefix(href, "/")
}
