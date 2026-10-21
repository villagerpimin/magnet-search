package engine

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// fetchRetryDelay 是服务端报错之后等待多久再重试。
const fetchRetryDelay = 500 * time.Millisecond

// fetchWithRetry 抓取页面，服务端返回 5xx 时重试一次。
//
// yts.rs 与 nyaa.ink 都会偶发返回 5xx（实测 HTTP 500 与 502），
// 而共享客户端默认不重试，因此在适配器层面补一次重试；
// 连接层的失败（被墙、超时）重试没有意义，直接交给上层处理。
func fetchWithRetry(ctx context.Context, c ClientProvider, rawURL string) ([]byte, error) {
	body, err := c.Get(ctx, rawURL)
	if err == nil || !isServerError(err) {
		return body, err
	}
	select {
	case <-ctx.Done():
		return nil, err
	case <-time.After(fetchRetryDelay):
	}
	body, retryErr := c.Get(ctx, rawURL)
	if retryErr != nil {
		return nil, fmt.Errorf("%w；重试仍然失败: %v", err, retryErr)
	}
	return body, nil
}

// isServerError 判断错误是不是服务端 5xx。
// 共享客户端把状态码写进错误文本，这里据此区分 5xx 与 4xx、被墙等情况。
func isServerError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "HTTP 5")
}
