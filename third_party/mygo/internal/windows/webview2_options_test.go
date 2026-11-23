//go:build windows && (amd64 || arm64)

package windows

import (
	"testing"
	"unsafe"
)

// TestEnvOptionsArguments checks that the options object gives the
// environment the arguments it holds, beside its language.
func TestEnvOptionsArguments(t *testing.T) {
	// ICoreWebView2EnvironmentOptions, from envOptionsVtbl.
	const (
		getAdditionalBrowserArguments = 3
		getLanguage                   = 5
	)
	for _, tt := range []struct {
		name, language, args string
	}{
		{"arguments only", "", "--remote-debugging-port=0"},
		{"language only", "fr", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options := newEnvOptions(tt.language, tt.args)
			defer release(options)
			for _, getter := range []struct {
				name  string
				index int
				want  string
			}{
				{"AdditionalBrowserArguments", getAdditionalBrowserArguments, tt.args},
				{"Language", getLanguage, tt.language},
			} {
				var p uintptr
				if hr := comCall(options, getter.index, uintptr(unsafe.Pointer(&p))); failed(hr) {
					t.Fatal(hresultError("get_"+getter.name, hr))
				}
				if getter.want == "" && p != 0 {
					t.Errorf("get_%s returned a string for no value", getter.name)
				}
				if got := takeWstr(p); got != getter.want {
					t.Errorf("get_%s = %q, want %q", getter.name, got, getter.want)
				}
			}
		})
	}
}

// TestChromiumLanguage checks the tags WebView2's browser is given for
// Chinese, which Chromium names by region.
func TestChromiumLanguage(t *testing.T) {
	for tag, want := range map[string]string{
		"zh-Hans":    "zh-CN",
		"zh-Hant":    "zh-TW",
		"zh-Hant-HK": "zh-HK",
		"zh-TW":      "zh-TW",
		"zh-Hans-SG": "zh-CN",
		"zh":         "zh-CN",
		"en-GB":      "en-GB",
		"sr-Latn":    "sr-Latn",
	} {
		if got := chromiumLanguage(tag); got != want {
			t.Errorf("chromiumLanguage(%q) = %q, want %q", tag, got, want)
		}
	}
}

// TestBrowserArguments checks the arguments WebView2's browser is given for
// the options of the first page: none for neither, and both sets for both.
func TestBrowserArguments(t *testing.T) {
	for _, tt := range []struct {
		unthrottled, autoplay bool
		want                  string
	}{
		{false, false, ""},
		{true, false, unthrottledArguments},
		{false, true, "--autoplay-policy=no-user-gesture-required"},
		{true, true, unthrottledArguments + " --autoplay-policy=no-user-gesture-required"},
	} {
		if got := browserArguments(tt.unthrottled, tt.autoplay); got != tt.want {
			t.Errorf("browserArguments(%v, %v) = %q, want %q", tt.unthrottled, tt.autoplay, got, tt.want)
		}
	}
}
