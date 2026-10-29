package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

const (
	nativeRemoteImageTimeout   = 20 * time.Second
	nativeRemoteImageMaxBytes  = media.DefaultMaxImageBytes
	nativeRemoteImageRedirects = 10
)

// requestNativeImagePreview decodes a validated local image on a worker
// goroutine. The caller must verify that absolutePath resolves to a regular
// file inside the active materials root before calling; this helper enforces
// media byte/pixel limits and never changes the active document. It calls done
// from the worker goroutine only while ctx remains live.
func requestNativeImagePreview(ctx context.Context, absolutePath string, maxWidthPx int, done func(*ui.Bitmap, error)) {
	if done == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		bitmap, err := decodeNativeImagePreview(ctx, absolutePath, maxWidthPx)
		if ctx.Err() == nil {
			done(bitmap, err)
		}
	}()
}

// requestNativeRemoteImagePreview preserves Source's HTTP(S) image support
// without using a WebView. It accepts only image data through the bounded
// native media decoder, follows only HTTP(S) redirects, and removes its
// temporary staging file on every exit path. The callback runs off the UI
// thread and is omitted after cancellation.
func requestNativeRemoteImagePreview(ctx context.Context, rawURL string, maxWidthPx int, done func(*ui.Bitmap, error)) {
	requestNativeRemoteImagePreviewWithTempRoot(ctx, rawURL, maxWidthPx, "", done)
}

func requestNativeRemoteImagePreviewWithTempRoot(ctx context.Context, rawURL string, maxWidthPx int, tempRoot string, done func(*ui.Bitmap, error)) {
	if done == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		requestCtx, cancel := context.WithTimeout(ctx, nativeRemoteImageTimeout)
		defer cancel()
		bitmap, err := fetchNativeRemoteImagePreviewIn(requestCtx, rawURL, maxWidthPx, tempRoot)
		if ctx.Err() == nil {
			done(bitmap, err)
		}
	}()
}

func decodeNativeImagePreview(ctx context.Context, path string, maxWidthPx int) (*ui.Bitmap, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("图片路径为空")
	}
	if maxWidthPx < 0 {
		return nil, errors.New("图片预览宽度不能为负数")
	}
	document, err := media.OpenImage(ctx, path, media.Options{MaxImageBytes: media.DefaultMaxImageBytes})
	if err != nil {
		return nil, err
	}
	defer document.Close()
	rendered, err := document.RenderRGBA(ctx, maxWidthPx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ui.NewBitmap(rendered.RGBA), nil
}

func fetchNativeRemoteImagePreview(ctx context.Context, rawURL string, maxWidthPx int) (*ui.Bitmap, error) {
	return fetchNativeRemoteImagePreviewIn(ctx, rawURL, maxWidthPx, "")
}

func fetchNativeRemoteImagePreviewIn(ctx context.Context, rawURL string, maxWidthPx int, tempRoot string) (*ui.Bitmap, error) {
	if maxWidthPx < 0 {
		return nil, errors.New("图片预览宽度不能为负数")
	}
	parsed, err := parseNativeRemoteImageURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: nativeRemoteImageTimeout,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			return validateNativeRemoteImageRedirect(request.URL, previous)
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建远程图片请求失败：%w", err)
	}
	request.Header.Set("Accept", "image/*")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("读取远程图片失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("远程图片返回 HTTP %d", response.StatusCode)
	}
	if response.ContentLength > nativeRemoteImageMaxBytes {
		return nil, fmt.Errorf("远程图片超过 %d 字节限制", nativeRemoteImageMaxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tempRoot != "" {
		info, err := os.Stat(tempRoot)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("远程图片临时目录无效：%s", tempRoot)
		}
	}
	staging, err := os.CreateTemp(tempRoot, "silverwing-remote-image-*.png")
	if err != nil {
		return nil, fmt.Errorf("创建远程图片临时文件失败：%w", err)
	}
	stagingPath := staging.Name()
	defer os.Remove(stagingPath)
	written, copyErr := io.Copy(staging, io.LimitReader(response.Body, nativeRemoteImageMaxBytes+1))
	closeErr := staging.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("保存远程图片失败：%w", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("关闭远程图片临时文件失败：%w", closeErr)
	}
	if written > nativeRemoteImageMaxBytes {
		return nil, fmt.Errorf("远程图片超过 %d 字节限制", nativeRemoteImageMaxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decodeNativeImagePreview(ctx, stagingPath, maxWidthPx)
}

func parseNativeRemoteImageURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("远程图片地址无效：%w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("远程图片仅支持不含凭据的 HTTP(S) 地址")
	}
	return parsed, nil
}

func validateNativeRemoteImageRedirect(next *url.URL, previous []*http.Request) error {
	if len(previous) >= nativeRemoteImageRedirects {
		return errors.New("远程图片重定向次数过多")
	}
	if next == nil {
		return errors.New("远程图片重定向目标无效")
	}
	if _, err := parseNativeRemoteImageURL(next.String()); err != nil {
		return fmt.Errorf("远程图片重定向目标无效：%w", err)
	}
	if len(previous) > 0 && previous[0] != nil && previous[0].URL != nil &&
		strings.EqualFold(previous[0].URL.Scheme, "https") && !strings.EqualFold(next.Scheme, "https") {
		return errors.New("远程图片不允许从 HTTPS 降级到 HTTP")
	}
	return nil
}
