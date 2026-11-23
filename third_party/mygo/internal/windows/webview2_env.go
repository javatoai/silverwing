//go:build windows && (amd64 || arm64) && !mygo_nowebview

package windows

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/webview2"
)

// webViewBuilt tells that the backend has WebView2: apps built with the tag
// mygo_nowebview have none (webview2_env_off.go), and the compiler leaves
// out what creates web views behind it.
const webViewBuilt = true

// backendName is the backend's Name.
const backendName = "windows/webview2"

// createEnvironment starts creating the WebView2 environment, in language,
// the app's (envOptions), or the system's for "", with the browser's
// additional arguments args; done runs on the main thread with it, and
// whether it has args, or with an error. An official loader next to the
// executable reads WebView2's environment variables itself. Without one,
// the runtime's client library does not, so WEBVIEW2_USER_DATA_FOLDER
// replaces the user data folder and WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS
// are appended to args. A runtime that fails to create one with the
// options is asked for one without them.
func createEnvironment(userData, language, args string, done func(env uintptr, withArgs bool, err error)) error {
	// An official loader next to the executable wins, and reads WebView2's
	// environment variables itself; the runtime's client library does not.
	loader := loaderProc()
	if loader == nil {
		var extra string
		userData, extra = loaderVariables(os.Getenv, executableDir, userData)
		switch {
		case args == "":
			args = extra
		case extra != "":
			args += " " + extra
		}
	}
	options := language != "" || args != ""
	without := func(env uintptr, err error) { done(env, false, err) }
	err := createEnvironmentWith(loader, userData, language, args, func(env uintptr, err error) {
		if err != nil && options {
			// A runtime that takes no options of ours: one without them,
			// in the language of the system.
			log.Printf("%v; creating it again without its language and arguments", err)
			if err := createEnvironmentWith(loader, userData, "", "", without); err != nil {
				without(0, err)
			}
			return
		}
		done(env, args != "", err)
	})
	if err != nil && options {
		return createEnvironmentWith(loader, userData, "", "", without)
	}
	return err
}

// createEnvironmentWith creates the environment as createEnvironment does,
// through loader, or the runtime's client library for nil, with the options
// of language and args, or none when both are "".
func createEnvironmentWith(loader *syscall.LazyProc, userData, language, args string, done func(env uintptr, err error)) error {
	completed := func(hr, env uintptr) {
		if failed(hr) || env == 0 {
			done(0, hresultError("creating the WebView2 environment", hr))
			return
		}
		addRef(env)
		done(env, nil)
	}
	dataDir := u16(userData)
	var options uintptr
	if language != "" || args != "" {
		options = newEnvOptions(language, args)
		defer release(options) // the environment takes its own reference
	}

	// An official loader next to the executable wins.
	if loader != nil {
		hr := withHandler(completed, func(h uintptr) uintptr {
			r, _, _ := loader.Call(0, uintptr(unsafe.Pointer(dataDir)), options, h)
			return r
		})
		if failed(hr) {
			return hresultError("CreateCoreWebView2EnvironmentWithOptions", hr)
		}
		return nil
	}

	// Otherwise the runtime's client library, as the loader does: a fixed
	// version runtime named by WEBVIEW2_BROWSER_EXECUTABLE_FOLDER, else the
	// Evergreen runtime.
	dir, fixed, err := webview2.Find()
	if err != nil {
		return fmt.Errorf("mygo: %w", err)
	}
	client, runtimeType := webview2.ClientDLL(dir), uintptr(0)
	if fixed {
		runtimeType = 1
	}
	dll := syscall.NewLazyDLL(client)
	proc := dll.NewProc("CreateWebViewEnvironmentWithOptionsInternal")
	if err := proc.Find(); err != nil {
		return fmt.Errorf("mygo: unsupported WebView2 Runtime at %s: %w", client, err)
	}
	hr := withHandler(completed, func(h uintptr) uintptr {
		r, _, _ := proc.Call(1, runtimeType, uintptr(unsafe.Pointer(dataDir)), options, h)
		return r
	})
	if failed(hr) {
		return hresultError("creating the WebView2 environment", hr)
	}
	return nil
}

// loaderVariables returns the user data folder and the browser arguments
// that Microsoft's loader takes from WebView2's environment variables and
// the runtime's client library does not, so that MyGo applies them when it
// loads the runtime itself: WEBVIEW2_USER_DATA_FOLDER replaces userData,
// relative to the directory exeDir returns, and
// WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS are appended to the arguments of the
// options, after the browser's own (browserArguments).
func loaderVariables(getenv func(string) string, exeDir func() string, userData string) (dir, args string) {
	dir = getenv("WEBVIEW2_USER_DATA_FOLDER")
	switch {
	case dir == "":
		dir = userData
	case !filepath.IsAbs(dir):
		dir = filepath.Join(exeDir(), dir)
	}
	return dir, getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")
}

// executableDir returns the directory of the executable, which a relative
// WEBVIEW2_USER_DATA_FOLDER is relative to, or "" when it is unknown.
func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// loaderProc returns the official WebView2 loader next to the executable and
// its CreateCoreWebView2EnvironmentWithOptions, or nil when there is no
// loader to use. createEnvironment calls what it returns, and
// WebViewAvailable asks for it, so the two cannot disagree: a DLL of that
// name with no such export is no loader, and a runtime is looked for
// instead.
func loaderProc() *syscall.LazyProc {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	path := filepath.Join(filepath.Dir(exe), "WebView2Loader.dll")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	proc := syscall.NewLazyDLL(path).NewProc("CreateCoreWebView2EnvironmentWithOptions")
	if proc.Find() != nil {
		return nil
	}
	return proc
}

// WebViewAvailable reports whether a window showing a web page can have
// one: the WebView2 Runtime is installed, or a loader next to the
// executable loads a fixed version runtime or the installed one itself. It
// starts nothing; once a window showing a page created the environment, it
// says whether that worked. It reads the registry and the disk only, so any
// goroutine may call it, before Init too.
func (b *Backend) WebViewAvailable() bool {
	switch b.envResult.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	if loaderProc() != nil {
		return true
	}
	_, _, err := webview2.Find()
	return err == nil
}

// startEnvironment starts creating the WebView2 environment, the first time
// a window that shows a web page is created, and returns why it cannot,
// such as a missing WebView2 Runtime. Windows that show native UI need
// none: an app whose windows all do runs without the runtime. o are the
// options of the page that needs it, nil for none.
//
// The environment's browser runs with unthrottledArguments when that first
// page asked not to be throttled, and with autoplayArguments when it asked
// to play without a user gesture: the arguments are the browser's, which
// all the app's pages share, and a page that asks once it runs is too
// late, which the log tells.
func (b *Backend) startEnvironment(o *platform.WindowOptions) error {
	unthrottled := o != nil && o.NoBackgroundThrottling
	autoplay := o != nil && o.Autoplay
	if b.envStarted {
		if unthrottled && !b.envUnthrottled && !b.envWarned {
			b.envWarned = true
			log.Print("mygo: ThrottlingOff of a page shown after the first one: WebView2's browser already runs without the arguments that keep hidden pages running; give the option to the first window or web view that shows a page")
		}
		if autoplay && !b.envAutoplay && !b.envAutoplayWarned {
			b.envAutoplayWarned = true
			log.Print("mygo: AutoplayAllow of a page shown after the first one: WebView2's browser already runs without the argument that lets pages play sound without a user gesture; give the option to the first window or web view that shows a page")
		}
		return nil
	}
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	userData := filepath.Join(dir, sanitize(b.name), "WebView2")
	_ = os.MkdirAll(userData, 0o755)
	language := ""
	if len(b.languages) > 0 {
		language = b.languages[0]
	}
	// What the first page asked for counts from now, so that a page shown
	// while the environment is being made isn't told it is too late.
	b.envUnthrottled, b.envAutoplay = unthrottled, autoplay
	err := createEnvironment(userData, language, browserArguments(unthrottled, autoplay), func(env uintptr, withArgs bool, err error) {
		b.env, b.envErr = env, err
		if err != nil || !withArgs {
			b.envUnthrottled, b.envAutoplay = false, false
		}
		if err != nil {
			log.Print(err)
			b.envResult.Store(2)
		} else {
			b.envResult.Store(1)
		}
		waiters := b.envWaiters
		b.envWaiters = nil
		for _, fn := range waiters {
			fn()
		}
	})
	// A runtime installed later is found by the next window.
	b.envStarted = err == nil
	return err
}

// sanitize makes name, the app's, a folder's name.
func sanitize(name string) string {
	out := []rune{}
	for _, r := range name {
		if r < ' ' || r == '<' || r == '>' || r == ':' || r == '"' || r == '/' || r == '\\' || r == '|' || r == '?' || r == '*' {
			r = '_'
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return "MyGo"
	}
	return string(out)
}
