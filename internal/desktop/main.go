package desktop

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/buildinfo"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/ui"
)

// Options controls the initial native desktop state. It is also used by the
// command line to capture stable screenshots for visual verification.
type Options struct {
	Home           string
	RestoreSession bool
	Page           string
	Dark           bool
	Width          int
	Height         int
	Tab            int
	CapturePath    string
	CaptureOnly    bool
}

// Run starts the native MyGO window and talks to the application in-process.
// It never starts a WebView, local HTTP server, or JVM.
func Run(backend application.Backend, opts Options) error {
	if backend == nil {
		return fmt.Errorf("desktop requires an application backend")
	}
	if opts.Page == "" {
		opts.Page = "tasks"
	}
	if !validPage(opts.Page) {
		return fmt.Errorf("invalid page %q", opts.Page)
	}
	if opts.Tab < 0 || opts.Tab > len(tabs) {
		return fmt.Errorf("task tab must be between 0 and %d", len(tabs))
	}
	if opts.CaptureOnly && opts.CapturePath == "" {
		return fmt.Errorf("capture-only requires a capture path")
	}
	if opts.Page == "materials" {
		opts.Page = "tasks"
		opts.Tab = 1
	}
	if opts.Width < 0 || opts.Height < 0 {
		return fmt.Errorf("window width and height must not be negative")
	}
	explicitWindowSize := opts.Width != 0 || opts.Height != 0
	s := newApp(backend, opts)
	s.materialsUnit.state.materialsGitCapture = opts.CapturePath != ""
	if opts.CapturePath != "" {
		s.readingSession = newMemoryReadingSession(opts.Home)
	}
	if opts.CapturePath == "" {
		s.restoreDesktopPreferences(opts.Home)
	}
	if opts.Width == 0 && s.desktopPreferences.store != nil {
		opts.Width = s.desktopPreferences.value.Width
	}
	if opts.Height == 0 && s.desktopPreferences.store != nil {
		opts.Height = s.desktopPreferences.value.Height
	}
	if opts.Width == 0 {
		opts.Width = 1440
	}
	if opts.Height == 0 {
		opts.Height = 940
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		return fmt.Errorf("window width and height must be positive")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位程序资源目录：%w", err)
	}
	mygo.App.SetLanguages("zh-Hans")
	mygo.App.SetPath(mygo.PathResources, buildinfo.ResourceRoot(executable))

	captureFrame := &captureFrameState{}
	mygo.App.WhenReady(func() {
		s.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     buildinfo.ProductName,
			Width:     opts.Width,
			Height:    opts.Height,
			Maximized: opts.CapturePath == "" && !explicitWindowSize && s.desktopPreferences.store != nil && s.desktopPreferences.value.Maximized,
			MinWidth:  960,
			MinHeight: 760,
			Content: ui.View(func(c *ui.Context) {
				s.view(c)
				s.observeDesktopPreferences()
				s.observeReadingSession()
				if opts.CapturePath != "" {
					width, height := c.Size()
					captureFrame.ready = width > 0 && height > 0 && s.captureReady()
				}
			}),
		})
		if err := s.win.SetIcon(applicationIcon); err != nil {
			s.lastError = fmt.Sprintf("设置应用图标失败：%v", err)
			log.Print(s.lastError)
		}
		if opts.CapturePath == "" {
			s.initializeReadingSession(opts.Home, opts.RestoreSession)
		}
		s.win.OnClosed(s.stopDesktopFeatures)
		s.win.OnFocus(func() {
			s.onMaterialsWindowFocused()
			s.onRequirementWindowFocused()
			s.onMeegleProjectPickerWindowFocused()
		})
		s.win.OnResize(s.recordDesktopWindowBounds)
		s.win.OnMaximize(s.recordDesktopWindowBounds)
		s.win.OnUnmaximize(s.recordDesktopWindowBounds)
		s.win.OnClose(func(event *mygo.CloseEvent) {
			s.recordDesktopWindowBounds()
			if s.handleSettingsAutosaveClose(event) {
				return
			}
			s.handleDocumentClose(event)
		})
		go s.loadInitialSnapshot()
		if opts.CapturePath != "" {
			go captureWindow(s, captureFrame, opts.CapturePath, opts.CaptureOnly)
		}
	})
	runErr := mygo.App.Run()
	return s.closeDesktopFeatures(runErr)
}

func (s *app) loadInitialSnapshot() {
	snapshot, err := s.backend.Snapshot(context.Background())
	if s.win == nil {
		return
	}
	s.win.Update(func() {
		s.loading = false
		if err != nil {
			s.lastError = err.Error()
			return
		}
		s.applySnapshot(snapshot)

	})
}

func validPage(page string) bool {
	switch page {
	case "tasks", "materials", "tools", "settings", "requirements", "skills", "archive":
		return true
	default:
		return false
	}
}

// Only the UI thread reads or writes this state. CapturePage reuses MyGO's last
// scene, so readiness must include a frame built after asynchronous reads settle.
type captureFrameState struct {
	ready bool
}

func captureWindow(s *app, frame *captureFrameState, path string, captureOnly bool) {
	defer func() {
		if captureOnly {
			mygo.App.Quit()
		}
	}()
	// A launcher can request a hidden initial window. On Windows that request
	// overrides the first Show performed by MyGO; a hidden surface receives no
	// paint frames, so view-triggered data reads cannot start. Show it without
	// taking keyboard focus before waiting for the page to finish loading.
	if s.win == nil || s.win.IsDestroyed() {
		return
	}
	s.win.ShowInactive()
	deadline := time.Now().Add(30 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if s.win == nil || s.win.IsDestroyed() {
			return
		}
		readyResult := make(chan bool, 1)
		s.win.Update(func() { readyResult <- s.captureReady() && frame.ready })
		select {
		case ready = <-readyResult:
		case <-time.After(500 * time.Millisecond):
			ready = false
		}
		if ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		task := s.taskUnit.currentTask()
		log.Printf("capture readiness timed out for %s; page=%s tab=%d loading=%t busy=%t task=%q materialsPath=%q documentsLoaded=%t documentsRequested=%t documentReadPending=%t document=%q kind=%s; no screenshot written",
			path, s.page, s.tab, s.loading, s.busy, task.id, task.materialsPath, s.materialsUnit.state.documentsLoaded,
			s.materialsUnit.state.documentsRequested, s.materialsUnit.state.documentReadPending, s.materialsUnit.state.documentPath, s.materialsUnit.state.documentKind)
		return
	}

	png, err := s.win.CapturePage()
	if err != nil {
		log.Printf("capture native window: %v", err)
		return
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("create capture directory: %v", err)
			return
		}
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		log.Printf("write capture %q: %v", path, err)
		return
	}
	log.Printf("captured native window to %s", path)
}

func (s *app) captureReady() bool {
	if s == nil || s.loading || s.busy {
		return false
	}
	if s.page == "tasks" || s.page == "archive" {
		task := s.taskUnit.currentTask()

		if task.id != "" && domain.ParseFeishuWorkItemLink(task.requirementURL) != nil {
			metadata := s.taskRequirementMetadataFor(task)
			if metadata.Status == requirementMetadataNotLoaded {
				s.requestTaskRequirementMetadata(task, false)
				return false
			}
			if metadata.Status == requirementMetadataLoading {
				return false
			}
		}
	}
	if s.page == "settings" && s.settingsUnit.state.settingsLoaded == "" {
		return false
	}
	if s.localSkillsViewActive() {
		state := s.settingsUnit.state.skillsBrowser
		if state == nil || state.loadingCatalog || (!state.catalogLoaded && state.error == "") {
			return false
		}
		if state.loadingFiles || state.loadingPreview || (state.selectedPath != "" && !state.previewLoaded && state.fileError == "") {
			return false
		}
	}
	if (s.page == "tasks" || s.page == "archive") && s.tab == 1 {
		// The task detail pane can be fully rendered without loading a document
		// tree when no task is selected or its materials have not been linked.
		// In those states materials() renders an actionable empty state instead
		// of dispatching document.list, so waiting for documentsLoaded would
		// never settle.
		task := s.taskUnit.currentTask()
		if task.id == "" || !task.materialsReady() {
			return true
		}
		if !s.materialsUnit.state.documentsLoaded || s.materialsUnit.state.documentsRequested || s.materialsUnit.state.documentReadPending {
			return false
		}
		if s.materialsUnit.state.documentPath != "" && (s.materialsUnit.state.documentKind == "pdf" || s.materialsUnit.state.documentKind == "image") {
			mediaState := s.materialsUnit.state.nativeMedia
			if mediaState == nil {
				return false
			}
			mediaState.mu.Lock()
			ready := !mediaState.openLoading && !mediaState.renderLoading && (mediaState.bitmap != nil || mediaState.previewError != "")
			if s.materialsUnit.state.documentKind == "pdf" {
				visible := max(0, min(mediaState.page, mediaState.pdfInfo.PageCount-1))
				page := mediaState.pdfPageBitmaps[visible]
				ready = !mediaState.openLoading && (page.bitmap != nil || mediaState.pdfPageErrors[visible] != "" || mediaState.previewError != "")
			}
			mediaState.mu.Unlock()
			return ready
		}
	}
	return s.captureTextEditorsReady()
}
