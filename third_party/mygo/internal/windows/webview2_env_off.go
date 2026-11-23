//go:build windows && (amd64 || arm64) && mygo_nowebview

package windows

import (
	"errors"

	"github.com/egoist/mygo/internal/platform"
)

// The tag mygo_nowebview builds the backend without WebView2, for apps whose
// windows all show native UI (WindowOptions.Content): no loader, no lookup
// of the runtime, and none of the code that creates web views, which the
// callers of createWebView leave out behind webViewBuilt. webview2_env.go
// has the real environment.
const webViewBuilt = false

// backendName is the backend's Name.
const backendName = "windows"

var errBuiltWithoutWebView = errors.New("mygo: built with the tag mygo_nowebview, the app shows native UI only (WindowOptions.Content), not web pages")

// startEnvironment fails: a window or web view that shows a page, and a
// cookie call, report errBuiltWithoutWebView (NewWindow panics with it).
func (b *Backend) startEnvironment(*platform.WindowOptions) error {
	return errBuiltWithoutWebView
}

// WebViewAvailable reports false: the app was built without WebView2.
func (b *Backend) WebViewAvailable() bool { return false }
