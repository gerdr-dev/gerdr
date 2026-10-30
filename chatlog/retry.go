package chatlog

import (
	"strings"
	"time"
)

// RetryWaits：一轮因为网络出错失败时自动重试的间隔（agent 自己的重试已经用完了才会走到这里）。
// 指数回退：5 秒起翻倍，单次最多 1 分钟，所有等待加起来不超过 5 分钟 —— 偶发的 403 / 过载常常要一两分钟才缓过来，
// 原来固定三次（5/15/30 秒）不到一分钟就放弃了；再往后多半是真坏了，别让人一直等。
var RetryWaits = backoff(5*time.Second, time.Minute, 5*time.Minute)

func backoff(first, max, total time.Duration) []time.Duration {
	var out []time.Duration
	for d, sum := first, time.Duration(0); sum < total; d *= 2 {
		d = min(d, max, total-sum)
		out = append(out, d)
		sum += d
	}
	return out
}

// Retryable：值得自动重来的错误 —— 网络 / 过载 / agent 进程半路死了。额度用完、参数错这类不重试
// （重试也一样，还白白烧钱）。
func Retryable(msg string) bool {
	m := strings.ToLower(msg)
	// 「API Error: 403 Request not allowed」：API 那边偶发的拒绝（前面还挂着一句 Failed to authenticate），
	// 不是登录失效，过一会儿就好（用户报的）—— 先于下面的 403 判掉。
	if strings.Contains(m, "request not allowed") {
		return true
	}
	for _, k := range []string{"rate limit", "usage limit", "quota", "invalid", "unauthorized", "401", "403", "credit"} {
		if strings.Contains(m, k) {
			return false
		}
	}
	for _, k := range []string{"econnreset", "etimedout", "enotfound", "eai_again", "econnrefused", "socket hang up", "network", "connection",
		"timeout", "timed out", "overloaded", "529", "502", "503", "504", "stream disconnected", "fetch failed", "进程意外退出", "进程退出了"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}
