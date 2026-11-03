package platform

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type releaseRequestError struct {
	reason  string
	retryAt time.Time
}

func (e *releaseRequestError) Error() string {
	if e.retryAt.IsZero() {
		return e.reason
	}
	return e.reason + "，请于 " + e.retryAt.Local().Format("2006-01-02 15:04:05") + " 后重试"
}

// HTTP 403 can also be an access policy or proxy rejection. Only report a
// quota failure when GitHub's headers or structured response establish it.
func githubReleaseResponseError(response *http.Response, body []byte, now time.Time) error {
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &detail)
		message := strings.ToLower(detail.Message)
		quota := response.Header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(message, "api rate limit exceeded")
		throttled := response.StatusCode == http.StatusTooManyRequests || strings.Contains(message, "secondary rate limit") || strings.Contains(message, "abuse detection")
		if quota || throttled {
			reason := "GitHub 请求过于频繁"
			if quota {
				reason = "GitHub 匿名请求额度已用完"
			}
			return &releaseRequestError{reason: reason, retryAt: githubReleaseRetryAt(response.Header, now, quota)}
		}
	}
	if response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("GitHub 拒绝访问（HTTP 403），请检查访问限制或代理规则")
	}
	if response.StatusCode == http.StatusProxyAuthRequired {
		return fmt.Errorf("代理认证失败（HTTP 407），请检查代理账号和密码")
	}
	return fmt.Errorf("读取 GitHub Release 失败（HTTP %d），请稍后重试", response.StatusCode)
}

func githubReleaseRetryAt(header http.Header, now time.Time, quota bool) time.Time {
	var retryAt time.Time
	if seconds, err := strconv.ParseInt(header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
		retryAt = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(header.Get("Retry-After")); err == nil && date.After(now) {
		retryAt = date
	}
	if quota {
		if seconds, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			reset := time.Unix(seconds, 0)
			if reset.After(now) && reset.After(retryAt) {
				retryAt = reset
			}
		}
	}
	if !retryAt.After(now) {
		retryAt = now.Add(time.Minute)
	}
	return retryAt
}

// The backend honors the server's pause even when the UI button is clicked
// repeatedly. No proxy switching, authentication or automatic retries occur.
type appUpdateCheckBackoff struct {
	mu       sync.Mutex
	failures map[string]*releaseRequestError
}

func (b *appUpdateCheckBackoff) pending(route string, now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if failure := b.failures[route]; failure != nil && now.Before(failure.retryAt) {
		return failure
	}
	delete(b.failures, route)
	return nil
}

func (b *appUpdateCheckBackoff) record(route string, err error) {
	failure, ok := err.(*releaseRequestError)
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures == nil {
		b.failures = make(map[string]*releaseRequestError)
	}
	for key, previous := range b.failures {
		if !previous.retryAt.After(time.Now()) {
			delete(b.failures, key)
		}
	}
	b.failures[route] = failure
}
