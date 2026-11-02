package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/media"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

type taskFilesTestBackend struct {
	actions  []application.Action
	dispatch func(application.Action) (application.ActionResult, error)
}

func (*taskFilesTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (backend *taskFilesTestBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	backend.actions = append(backend.actions, action)
	if backend.dispatch != nil {
		return backend.dispatch(action)
	}
	return application.ActionResult{}, errors.New("unexpected task-files action")
}

func taskFilesFixture(t *testing.T) (*app, *taskFilesView, *taskFilesTestBackend) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "tasks")
	taskPath := filepath.Join(root, "fixture")
	if err := os.MkdirAll(taskPath, 0700); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{"taskRoot": root})
	if err != nil {
		t.Fatal(err)
	}
	backend := &taskFilesTestBackend{}
	s := &app{page: "tasks", tab: taskFilesTab, backend: backend, snapshot: application.Snapshot{Home: home, ConfigJSON: string(config)}}
	s.settingsUnit.state.configJSON = string(config)
	s.taskUnit.state.tasks = []task{{id: "fixture", createdAt: "2026-11-01T10:00:00Z", path: taskPath}}
	s.taskUnit.state.selected = 0
	view := newTaskFilesView(s.taskFilesIdentity())
	view.active, view.epoch, view.root = true, 1, taskPath
	view.directory("").loaded = true
	s.taskFilesUnit.views = map[taskFilesIdentity]*taskFilesView{view.identity: view}
	s.taskFilesUnit.current = view
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.taskFilesUnit.closeTaskFiles(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, view, backend
}

func seedTaskFilesPreview(s *app, view *taskFilesView, relative, kind, source string) *taskFilesPreview {
	preview := &taskFilesPreview{relative: relative, images: map[string]*taskFilesInlineImage{}, read: application.TaskFilesReadResult{
		Root: view.root, RelativePath: relative, Identity: "fixture-file", Content: documents.Content{Path: taskFilesAbsolute(view.root, relative),
			Kind: kind, Text: source, ReadOnly: true, Size: int64(len(source)), Hash: documents.Hash([]byte(source))}}}
	view.opened, view.selected, view.preview = relative, relative, preview
	view.directory("").entries = []application.TaskFileEntry{{Path: relative, Name: filepath.Base(relative), Kind: kind}}
	s.bindTaskFilesPreview(view, preview)
	return preview
}

func TestTaskFilesIdentityAndScopeAreIsolatedFromDraftSettings(t *testing.T) {
	s, view, _ := taskFilesFixture(t)
	initial := view.identity
	if !s.taskFilesCurrent(view) {
		t.Fatal("fixture is not active")
	}
	s.settingsUnit.state.configJSON = `{"taskRoot":"unsaved-root"}`
	if s.taskFilesIdentity() != initial {
		t.Fatal("an unsaved settings draft changed read ownership")
	}
	for _, change := range []struct {
		name   string
		mutate func()
	}{
		{"home", func() { s.snapshot.Home += "-other" }},
		{"id", func() { s.taskUnit.state.tasks[0].id += "-other" }},
		{"incarnation", func() { s.taskUnit.state.tasks[0].createdAt += "-new" }},
		{"path", func() { s.taskUnit.state.tasks[0].path += "-other" }},
		{"archive", func() { s.taskUnit.state.tasks[0].archived = true }},
		{"root", func() { s.snapshot.ConfigJSON = `{"taskRoot":"changed-root"}` }},
	} {
		t.Run(change.name, func(t *testing.T) {
			s.snapshot.Home = initial.home
			s.taskUnit.state.tasks[0] = task{id: initial.id, createdAt: initial.createdAt, path: initial.taskPath, archived: initial.archived}
			config, _ := json.Marshal(map[string]any{"taskRoot": initial.taskRoot})
			s.snapshot.ConfigJSON = string(config)
			change.mutate()
			if s.taskFilesCurrent(view) || s.taskFilesIdentity().documentID("a.md") == initial.documentID("a.md") {
				t.Fatal("old task owner survived identity change")
			}
		})
	}
	action := initial.action("task.files.read", "src/a.go")
	data, err := json.Marshal(action.Payload["taskFilesContext"])
	if err != nil {
		t.Fatal(err)
	}
	var decoded application.TaskFilesContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if action.TaskID != initial.id || action.Path != "src/a.go" || decoded.Home != initial.home || decoded.TaskPath != initial.taskPath ||
		decoded.TaskCreatedAt != initial.createdAt || decoded.TaskRoot != initial.taskRoot || decoded.Archived != initial.archived {
		t.Fatalf("dispatch lost task ownership: %+v %+v", action, decoded)
	}
}

func TestTaskFilesLazyTreeIncludesHiddenIgnoredAndRestrictedLeaves(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	view.directory("").entries = []application.TaskFileEntry{
		{Path: "src", Name: "src", Directory: true}, {Path: ".workspace", Name: ".workspace", Directory: true},
		{Path: ".git", Name: ".git", Kind: "external"},
		{Path: "junction", Name: "junction", Directory: true, RestrictedReason: "不跟随目录链接"},
		{Path: "AGENTS.md", Name: "AGENTS.md"}, {Path: "silverwing.json", Name: "silverwing.json"}, {Path: "ignored.log", Name: "ignored.log"},
	}
	view.directory("src").entries = []application.TaskFileEntry{{Path: "src/deep.go", Name: "deep.go"}}
	view.directory("src").loaded = true
	rows := taskFilesTreeRows(view)
	if len(rows) != 7 {
		t.Fatalf("collapsed tree materialized descendants: %+v", rows)
	}
	for _, row := range rows {
		if (row.ID == ".git" || row.ID == "junction") && row.Directory {
			t.Fatal("restricted node can expand")
		}
		if row.ID == ".git" && row.Name != ".git" {
			t.Fatal("Git metadata still has a restricted suffix", row)
		}
	}
	view.expanded["src"] = true
	rows = taskFilesTreeRows(view)
	if len(rows) != 8 || rows[1].ID != "src/deep.go" || rows[1].Depth != 1 {
		t.Fatalf("expanded rows: %+v", rows)
	}
	view.directory("src").loaded = false
	view.directory("src").error = "access denied"
	rows = taskFilesTreeRows(view)
	if relative, ok := taskFilesStatusRetry(rows[1].ID); !ok || relative != "src" {
		t.Fatal("directory failure has no local retry")
	}
	s.taskFilesTreeInput(view, FileTreeInput{Kind: FileTreeActivate, Row: FileTreeRow{ID: ".git"}, ClickCount: 2})
	s.taskFilesTreeInput(view, FileTreeInput{Kind: FileTreeDrop, Row: FileTreeRow{ID: "src"}, Files: []string{"external.txt"}})
	if view.opened != ".git" || view.expanded[".git"] || len(backend.actions) != 0 || view.operationError != "" || view.preview == nil || view.preview.read.Content.Kind != "external" {
		t.Fatal("Git metadata was expanded/read or failed to show a system-open preview")
	}
}

func TestTaskFilesSinglePreviewKeepsRepeatedClicksAndReleasesReplacedFile(t *testing.T) {
	s, view, _ := taskFilesFixture(t)
	first := seedTaskFilesPreview(s, view, "one.md", "markdown", strings.Repeat("原文\n", 50))
	reader := first.reader
	want := markdownui.ReaderViewState{DocumentID: reader.Document().ID, Mode: markdownui.Source, Zoom: 1.2, ScrollY: 90, SourceScrollY: 140}
	if err := reader.RestoreViewState(want); err != nil {
		t.Fatal(err)
	}
	view.directory("").entries = append(view.directory("").entries, application.TaskFileEntry{Path: "two.txt", Name: "two.txt"})
	activate := func(relative string, count int) {
		s.taskFilesTreeInput(view, FileTreeInput{Kind: FileTreeActivate, Row: FileTreeRow{ID: relative}, ClickCount: count})
	}
	for _, count := range []int{1, 2, 1} {
		activate("one.md", count)
		if view.preview != first || reader.Context().Err() != nil || reader.GetViewState() != want {
			t.Fatal("repeated click reloaded the file or changed its reading position", count)
		}
	}
	activate("two.txt", 1)
	if view.opened != "two.txt" || view.selected != "two.txt" || view.preview == first || view.preview.relative != "two.txt" {
		t.Fatal("selection did not replace the single preview")
	}
	if reader.Context().Err() == nil || view.position("one.md").reader != want {
		t.Fatal("replacing a file did not release its reader and save its position")
	}
	second := view.preview
	activate("two.txt", 2)
	if view.preview != second {
		t.Fatal("double click reloaded the current preview")
	}
	s.openTaskFilePreview(view, "two.txt", true)
	if view.preview == second || view.opened != "two.txt" {
		t.Fatal("explicit refresh failed to reread the current file")
	}
}

func TestTaskFilesGitShowsOnlySystemOpenWithoutReading(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	view.directory("").entries = []application.TaskFileEntry{{Path: ".git", Name: ".git", Kind: "external"}}
	view.operationError = "old restriction message"
	s.taskFilesTreeInput(view, FileTreeInput{Kind: FileTreeActivate, Row: FileTreeRow{ID: ".git"}, ClickCount: 1})
	preview := view.preview
	if preview == nil || preview.loading || preview.reader != nil || preview.code.view != nil || view.operationError != "" || len(backend.actions) != 0 {
		t.Fatal("Git selection read contents, created an editor, or retained a warning")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskFiles(c, setTheme(c, false)) }, 1000, 650)
	if _, ok := tester.Find("使用系统应用打开"); !ok || tester.HasText("Git 内部元数据，不支持展开或预览") || tester.HasText("只读") {
		t.Fatal("Git fallback still displays a restriction or readonly badge")
	}
	s.taskFilesTreeInput(view, FileTreeInput{Kind: FileTreeActivate, Row: FileTreeRow{ID: ".git"}, ClickCount: 2})
	if view.preview != preview || view.expanded[".git"] || len(backend.actions) != 0 {
		t.Fatal("Git double click expanded/read/reloaded its contents")
	}
	for _, count := range []int{1, 2} {
		view.operationError, view.operationMessage = "old junction warning", "old operation"
		s.taskFilesTreeInput(view, FileTreeInput{Kind: FileTreeActivate, Row: FileTreeRow{ID: ".git"}, ClickCount: count})
		if view.operationError != "" || view.operationMessage != "" || view.preview != preview || len(backend.actions) != 0 {
			t.Fatal("returning to Git retained an old message or reloaded its preview", count)
		}
	}
}

func TestTaskFilesReadonlyCodeCopiesOriginalAndIgnoresTypeSaveDrop(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			s, view, backend := taskFilesFixture(t)
			const source = "\ufeffpackage main\r\n\t// 中文  原文\nvar value = 1\r\n"
			preview := seedTaskFilesPreview(s, view, "main.go", "text", source)
			if err := os.WriteFile(preview.read.Content.Path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			tester := ui.NewTester(func(c *ui.Context) { s.renderTaskFiles(c, setTheme(c, dark)) }, 1000, 650)
			if err := tester.Click(preview.read.Content.Path); err != nil {
				t.Fatal(err)
			}
			preview.code.view.Select(0, len(preview.code.view.Buffer.String()))
			tester.Frame()
			tester.Command("copy")
			if tester.Clipboard() != source {
				t.Fatalf("copy changed original source: %q", tester.Clipboard())
			}
			tester.Type("must not edit")
			tester.SetClipboard("must not paste")
			tester.Key(ui.Ctrl, ui.KeyV)
			tester.Key(0, ui.KeyBackspace)
			tester.Key(ui.Ctrl, ui.KeyS)
			bounds, ok := tester.Find(preview.read.Content.Path)
			if !ok {
				t.Fatal("native code view missing")
			}
			tester.DropFiles(bounds.X+bounds.W/2, bounds.Y+bounds.H/2, filepath.Join(t.TempDir(), "external.txt"))
			bytes, err := os.ReadFile(preview.read.Content.Path)
			if err != nil {
				t.Fatal(err)
			}
			if !preview.code.view.ReadOnly || preview.code.view.Buffer.OriginalText() != source || string(bytes) != source || len(backend.actions) != 0 {
				t.Fatal("read-only code input changed source/disk or dispatched an action")
			}
			if s.materialsUnit.state.markdown != nil || s.materialsUnit.state.documentPath != "" {
				t.Fatal("task files adopted a materials owner")
			}
		})
	}
}

func TestTaskFilesPublicMarkdownReaderRemainsReadonlyAcrossSourceMode(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	const source = "# 只读标题\r\n\r\n原始正文\r\n"
	preview := seedTaskFilesPreview(s, view, "guide.md", "markdown", source)
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskFiles(c, setTheme(c, false)) }, 1000, 650)
	if !tester.HasText("原始正文") || preview.reader == nil {
		t.Fatal("public reader is not reachable")
	}
	if err := tester.RightClick("原始正文"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tester.Menu(), "实时编辑") {
		t.Fatal("readonly reader exposed editing")
	}
	if err := tester.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	if preview.reader.Mode() != markdownui.Source {
		t.Fatal("source mode was not reachable")
	}
	tester.Type("must not edit")
	tester.Key(ui.Ctrl, ui.KeyS)
	if preview.reader.Snapshot().Source != source || len(backend.actions) != 0 || s.materialsUnit.state.markdown != nil {
		t.Fatal("readonly markdown wrote or adopted materials state")
	}
}

func TestTaskFilesReleasePreservesPerFilePresentationAndTaskTree(t *testing.T) {
	s, view, _ := taskFilesFixture(t)
	preview := seedTaskFilesPreview(s, view, "guide.md", "markdown", strings.Repeat("原文\n", 150))
	old := preview.reader
	want := markdownui.ReaderViewState{DocumentID: old.Document().ID, Mode: markdownui.Source, Zoom: 1.4, ScrollX: 7, ScrollY: 120, SourceScrollX: 9, SourceScrollY: 240}
	if err := old.RestoreViewState(want); err != nil {
		t.Fatal(err)
	}
	view.expanded["src"] = true
	view.tree.scroll.Y = 66
	s.leaveTaskFiles()
	if old.Context().Err() == nil || view.preview != nil || view.active || !view.expanded["src"] || view.tree.scroll.Y != 66 || view.opened != "guide.md" || view.selected != "guide.md" {
		t.Fatal("leaving did not release resources while preserving view state")
	}
	view.active = true
	next := &taskFilesPreview{relative: "guide.md", read: application.TaskFilesReadResult{Content: documents.Content{Kind: "markdown", Path: taskFilesAbsolute(view.root, "guide.md"), Text: strings.Repeat("原文\n", 150)}}, images: map[string]*taskFilesInlineImage{}}
	view.preview = next
	s.bindTaskFilesPreview(view, next)
	if got := next.reader.GetViewState(); got != want {
		t.Fatalf("presentation restore: got %+v want %+v", got, want)
	}
	if view.position("another.go").code != (code.ScrollPosition{}) {
		t.Fatal("scroll leaked into another file")
	}
}

func TestTaskFilesRequestsRejectReplacedCancelledAndPreviousPageReceipts(t *testing.T) {
	view := newTaskFilesView(taskFilesIdentity{id: "fixture"})
	view.active = true
	view.epoch = 3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := &taskFilesRequest{view: view, epoch: view.epoch, key: "preview:read", ctx: ctx, cancel: cancel}
	jobs := &taskFilesJobs{current: map[string]*taskFilesRequest{request.key: request}}
	if !jobs.accepts(request, view) {
		t.Fatal("current request rejected")
	}
	view.epoch++
	if jobs.accepts(request, view) {
		t.Fatal("previous page epoch accepted")
	}
	view.epoch--
	view.active = false
	if jobs.accepts(request, view) {
		t.Fatal("inactive page accepted")
	}
	view.active = true
	jobs.current[request.key] = &taskFilesRequest{}
	if jobs.accepts(request, view) {
		t.Fatal("replaced request accepted")
	}
	jobs.current[request.key] = request
	cancel()
	if jobs.accepts(request, view) {
		t.Fatal("cancelled request accepted")
	}
	listCtx, listCancel := context.WithCancel(context.Background())
	defer listCancel()
	jobs.current["list:src"] = &taskFilesRequest{key: "list:src", view: view, ctx: listCtx, cancel: listCancel}
	jobs.cancelPreview()
	if listCtx.Err() != nil || jobs.current["list:src"] == nil {
		t.Fatal("changing preview cancelled an independent directory read")
	}
}

func TestTaskFilesClearingSearchRestoresTreeAndRejectsLateResults(t *testing.T) {
	for _, state := range []string{"completed", "pending", "failed"} {
		t.Run(state, func(t *testing.T) {
			s, view, backend := taskFilesFixture(t)
			view.query, view.searchQuery = "main.go", "main.go"
			view.searchDone, view.searching = state == "completed", state == "pending"
			if state == "failed" {
				view.searchError = "access denied"
			}
			view.search = application.TaskFilesSearchResult{Root: view.root, Query: "main.go", Entries: []application.TaskFileEntry{{Path: "src/main.go", Name: "main.go"}}}
			root := view.directory("")
			view.expanded["src"] = true
			view.selected, view.opened = "note.md", "note.md"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := &taskFilesRequest{view: view, epoch: view.epoch, key: "search", ctx: ctx, cancel: cancel}
			jobs := &s.taskFilesUnit.jobs
			jobs.current = map[string]*taskFilesRequest{"search": request}

			// A nonempty draft continues to use the existing explicit Enter/search
			// action. Typing must not unexpectedly issue a new traversal.
			view.query = "other.go"
			s.changeTaskFilesSearchQuery(view)
			if ctx.Err() != nil || view.searchQuery != "main.go" || !jobs.accepts(request, view) {
				t.Fatal("nonempty input prematurely changed the committed search")
			}

			view.query = ""
			s.changeTaskFilesSearchQuery(view)
			if view.searching || view.searchDone || view.searchError != "" || view.searchQuery != "" || len(view.search.Entries) != 0 {
				t.Fatal("cleared input retained the search presentation", view.search)
			}
			if ctx.Err() != context.Canceled || jobs.accepts(request, view) || jobs.current["search"] != nil {
				t.Fatal("a late search receipt could restore old results")
			}
			if view.directory("") != root || !root.loaded || !view.expanded["src"] || view.selected != "note.md" || view.opened != "note.md" {
				t.Fatal("clearing search discarded the tree, selection or open file")
			}
			if len(backend.actions) != 0 {
				t.Fatal("clearing search dispatched a new filesystem operation", backend.actions)
			}
		})
	}
}

func TestTaskFilesShutdownWaitsForDisposalAndHonorsDeadline(t *testing.T) {
	var jobs taskFilesJobs
	started, release := make(chan struct{}), make(chan struct{})
	jobs.dispose(func() { close(started); <-release })
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := jobs.close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup wait ignored cancellation: %v", err)
	}
	close(release)
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := jobs.close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTaskFilesMarkdownImagesRejectEscapesAndKeepRelativeLocalPaths(t *testing.T) {
	for _, raw := range []string{"../../outside.png", "%2e%2e/%2e%2e/outside.png", "/absolute.png", "file:///C:/outside.png", `..\outside.png`, "//server/share.png", "data:image/png;base64,a"} {
		if _, _, err := taskFilesImageTarget("docs/guide.md", raw); err == nil {
			t.Fatalf("unsafe image accepted: %q", raw)
		}
	}
	for raw, want := range map[string]string{"../images/a%20b.png": "images/a b.png", "./local.png": "docs/local.png"} {
		rel, remote, err := taskFilesImageTarget("docs/guide.md", raw)
		if err != nil || remote != "" || rel != want {
			t.Fatalf("relative image %q: %q %q %v", raw, rel, remote, err)
		}
	}
	if _, remote, err := taskFilesImageTarget("docs/guide.md", "https://example.test/a.png"); err != nil || remote == "" {
		t.Fatal("HTTP image lost native URL adapter")
	}
	width := taskFilesImageWidth(media.ImageInfo{Width: 1000, Height: 20000}, 1200, 2_000_000)
	if width <= 0 || int64(width)*int64(width)*20 > 2_000_000 {
		t.Fatal("tall image escaped inline raster budget")
	}
}

func TestTaskFilesNativeMediaRefusesFileReplacementAfterDecode(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	filename := taskFilesAbsolute(view.root, "image.png")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	bitmap := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	bitmap.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	encodeErr := png.Encode(file, bitmap)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		t.Fatalf("fixture: %v %v", encodeErr, closeErr)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	expected := application.TaskFilesReadResult{Root: view.root, RelativePath: "image.png", Identity: "original-file", Content: documents.Content{Path: filename, Kind: "image", ReadOnly: true, Size: info.Size(), Modified: info.ModTime().String()}}
	snapshotBytes, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	backend.dispatch = func(action application.Action) (application.ActionResult, error) {
		actual := expected
		if action.Payload["media"] == true {
			actual.MediaBytes, actual.MediaHash = snapshotBytes, documents.Hash(snapshotBytes)
		}
		if len(backend.actions) > 1 {
			actual.Identity = "replacement-file"
		}
		return application.ActionResult{Data: actual}, nil
	}
	asset, err := loadTaskFilesMedia(context.Background(), s.backend, view.identity, expected, 0, 600)
	if err == nil || asset != nil || len(backend.actions) != 2 {
		t.Fatalf("replacement pixels adopted: asset=%v err=%v reads=%d", asset, err, len(backend.actions))
	}
	for _, action := range backend.actions {
		if action.Kind != "task.files.read" || action.Path != "image.png" {
			t.Fatalf("media bypassed task backend: %+v", action)
		}
	}
}

func TestTaskFilesMediaSnapshotDoesNotReopenReplacedAncestor(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	original := filepath.Join(view.root, "sub")
	parked := filepath.Join(view.root, "parked-original")
	alternate := filepath.Join(t.TempDir(), "outside")
	writePNG := func(directory string, width, height int) []byte {
		t.Helper()
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "image.png"))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, image.NewNRGBA(image.Rect(0, 0, width, height)))
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatal(encodeErr, closeErr)
		}
		data, err := os.ReadFile(filepath.Join(directory, "image.png"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	data := writePNG(original, 2, 2)
	writePNG(alternate, 17, 11)
	info, err := os.Stat(filepath.Join(original, "image.png"))
	if err != nil {
		t.Fatal(err)
	}
	expected := application.TaskFilesReadResult{Root: view.root, RelativePath: "sub/image.png", Identity: "original-file",
		Content: documents.Content{Path: filepath.Join(original, "image.png"), Kind: "image", ReadOnly: true, Size: info.Size(), Modified: info.ModTime().String()}}
	backend.dispatch = func(action application.Action) (application.ActionResult, error) {
		actual := expected
		if action.Payload["media"] == true {
			// Return the already scoped snapshot, then replace its entire ancestor
			// before decoding. The old pathname decoder would see 17x11 pixels.
			actual.MediaBytes, actual.MediaHash = data, documents.Hash(data)
			if err := os.Rename(original, parked); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(alternate, original); err != nil {
				t.Fatal(err)
			}
		} else {
			// Restore the exact original inode before the metadata postcheck,
			// reproducing the ABA that two pathname checks cannot distinguish.
			if err := os.Rename(original, alternate); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(parked, original); err != nil {
				t.Fatal(err)
			}
		}
		return application.ActionResult{Data: actual}, nil
	}
	asset, err := loadTaskFilesMedia(context.Background(), s.backend, view.identity, expected, 0, 600)
	if err != nil {
		t.Fatal(err)
	}
	defer asset.dispose()
	width, height := asset.bitmap.Size()
	if width != 2 || height != 2 || len(backend.actions) != 2 {
		t.Fatalf("decoder reopened the ambient replacement path: %dx%d reads=%d", width, height, len(backend.actions))
	}
	if current, err := os.Stat(expected.Content.Path); err != nil || !os.SameFile(info, current) {
		t.Fatal("ABA fixture did not restore the original identity", err)
	}
}

func TestTaskFilesMediaSnapshotRejectsTamperedAndUnboundedPayload(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	expected := application.TaskFilesReadResult{Root: view.root, RelativePath: "image.png", Identity: "stable",
		Content: documents.Content{Path: taskFilesAbsolute(view.root, "image.png"), Kind: "image", Size: 4, Modified: "unchanged", ReadOnly: true}}
	for _, test := range []string{"missing", "hash", "length", "fallback"} {
		t.Run(test, func(t *testing.T) {
			read := expected
			read.MediaBytes, read.MediaHash = []byte{1, 2, 3, 4}, documents.Hash([]byte{1, 2, 3, 4})
			switch test {
			case "missing":
				read.MediaBytes = nil
			case "hash":
				read.MediaBytes[0] = 9
			case "length":
				read.MediaBytes = []byte{1, 2, 3}
				read.MediaHash = documents.Hash(read.MediaBytes)
			case "fallback":
				read.FallbackReason = "oversized media"
			}
			backend.dispatch = func(application.Action) (application.ActionResult, error) {
				return application.ActionResult{Data: read}, nil
			}
			if _, err := taskFilesReadMedia(context.Background(), s.backend, view.identity, expected); err == nil {
				t.Fatal("untrusted media payload reached a native decoder", test)
			}
		})
	}
}

func TestTaskFilesResultBoundaryRejectsWrongRootAndMutableDocuments(t *testing.T) {
	s, view, backend := taskFilesFixture(t)
	read := application.TaskFilesReadResult{Root: view.root, RelativePath: "a.txt", Identity: "file", Content: documents.Content{Path: taskFilesAbsolute(view.root, "a.txt"), Kind: "text", Text: "original", ReadOnly: true}}
	backend.dispatch = func(application.Action) (application.ActionResult, error) {
		return application.ActionResult{Data: read}, nil
	}
	if _, err := taskFilesRead(context.Background(), s.backend, view.identity, "a.txt"); err != nil {
		t.Fatal(err)
	}
	read.Content.ReadOnly = false
	if _, err := taskFilesRead(context.Background(), s.backend, view.identity, "a.txt"); err == nil {
		t.Fatal("mutable document accepted")
	}
	read.Content.ReadOnly = true
	read.Root = filepath.Dir(view.root)
	if _, err := taskFilesRead(context.Background(), s.backend, view.identity, "a.txt"); err == nil {
		t.Fatal("other root accepted")
	}
	bad := application.TaskFilesDirectory{Root: view.root, Path: "", Entries: []application.TaskFileEntry{{Path: "src/a.go", Name: "a.go"}}}
	if err := validateTaskFilesDirectory(bad, ""); err == nil {
		t.Fatal("directory returned recursive descendants as direct children")
	}
	before := view.root
	if err := view.acceptRoot(filepath.Dir(view.root)); err == nil || !reflect.DeepEqual(before, view.root) {
		t.Fatal("root failure changed established owner")
	}
}
