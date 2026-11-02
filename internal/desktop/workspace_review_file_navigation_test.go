package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func reviewNavigationTestState() *workspaceHistoryState {
	return &workspaceHistoryState{
		TaskID: "navigation-task", ServiceID: "service", ModuleID: "module", Path: `C:\synthetic\review-navigation`,
		Mode: "review", Open: true, CompareMode: workspaceReviewMainline, FileView: "flat",
		BaselineDisplayRef: "upstream/main", BaselineRef: "origin/main", ResultBaselineRef: "upstream/main",
		BaseCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), DiffSessionID: "navigation-session",
		Files: []workspace.CompareChange{
			{Path: "src/client/Shared.go", Kind: "MODIFIED"},
			{Path: "src/server/Shared.go", Kind: "MODIFIED"},
			{Path: "modern/client/Renamed.go", OldPath: "legacy/client/Shared.go", Kind: "RENAMED"},
		},
		SelectedPath: "src/server/Shared.go", SelectedKind: domain.WorkspaceGitModified,
		ExpandedDirectories: map[string]bool{"src": false, "src/client": true, "src/server": true},
		FileSelected:        func(workspace.CompareChange) {},
	}
}

func reviewNavigationTestPage(state *workspaceHistoryState, width, height int) (*app, *ui.Tester) {
	item := application.Workspace{ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path, Branch: "feature/navigation"}
	s := &app{
		page: "tasks", workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}}, taskUnit: taskController{state: taskState{tasks: []task{{id: state.TaskID, path: `C:\synthetic\navigation-task`, workspaces: []application.Workspace{item}}}}},
	}
	return s, ui.NewTester(func(c *ui.Context) { s.renderWorkspaceReviewPage(c, setTheme(c, false)) }, width, height)
}

func reviewNavigationSetFilter(t *testing.T, tester *ui.Tester, query string) {
	t.Helper()
	if err := tester.Click("筛选文件"); err != nil {
		t.Fatalf("changed-file navigation needs a filename/path filter: %v", err)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(query)
}

func TestWorkspaceReviewNavigationFlatDisambiguatesNamesAndRenamePaths(t *testing.T) {
	state := reviewNavigationTestState()
	var selected workspace.CompareChange
	state.FileSelected = func(file workspace.CompareChange) { selected = file }
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Width(360).Children(func() { s.renderWorkspaceReviewFileList(c, setTheme(c, false), state, "flat-paths") })
	}, 360, 380)
	for _, parent := range []string{"src/client", "src/server", "legacy/client → modern/client"} {
		if _, found := tester.Find(parent); !found {
			t.Errorf("flat list does not display parent path %q; texts=%v", parent, tester.Texts())
		}
	}
	if err := tester.Click("修改：src/client/Shared.go"); err != nil {
		t.Fatal(err)
	}
	if selected != state.Files[0] {
		t.Fatalf("same-basename click selected %#v, want %#v", selected, state.Files[0])
	}
	if err := tester.Click("重命名：legacy/client/Shared.go → modern/client/Renamed.go"); err != nil {
		t.Fatal(err)
	}
	if selected != state.Files[2] {
		t.Fatalf("rename click lost full old/new identity: %#v", selected)
	}
}

func TestWorkspaceReviewNavigationFilterMatchesOldPathWithoutChangingSelection(t *testing.T) {
	state := reviewNavigationTestState()
	preview := &workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "selected preview"}}
	state.Preview = preview
	selectionCalls := 0
	state.FileSelected = func(workspace.CompareChange) { selectionCalls++ }
	_, tester := reviewNavigationTestPage(state, 1040, 650)
	reviewNavigationSetFilter(t, tester, "LEGACY SHARED")
	if !tester.HasText("1 / 3") {
		t.Errorf("filter should report one of three files; texts=%v", tester.Texts())
	}
	if _, found := tester.Find("重命名：legacy/client/Shared.go → modern/client/Renamed.go"); !found {
		t.Error("case-insensitive AND filter did not match the rename's old path")
	}
	if _, found := tester.Find("修改：src/client/Shared.go"); found {
		t.Error("AND filter retained a file that only matched one keyword")
	}
	if state.SelectedPath != "src/server/Shared.go" || state.SelectedKind != domain.WorkspaceGitModified || state.Preview != preview || selectionCalls != 0 {
		t.Fatal("filter silently changed the selected file or preview")
	}
	reviewNavigationSetFilter(t, tester, "legacy absent")
	if !tester.HasText("0 / 3") || !tester.HasText("没有匹配的文件") || tester.HasText("没有差异文件") {
		t.Fatalf("no-match state was confused with no diff: %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	if !state.Open || !tester.HasText("3 / 3") || state.SelectedPath != "src/server/Shared.go" || state.Preview != preview || selectionCalls != 0 {
		t.Fatal("Escape did not clear only the file filter and preserve the reading selection")
	}
}

func TestWorkspaceReviewNavigationFilterRestoresCollapsedDirectoryAndViewScroll(t *testing.T) {
	state := reviewNavigationTestState()
	state.FileView = "tree"
	state.FileScrolls = map[string]*ui.ScrollState{"tree": {}, "flat": {Y: 73}}
	flatScroll := state.FileScrolls["flat"]
	_, tester := reviewNavigationTestPage(state, 1040, 650)
	if _, found := tester.Find("修改：src/client/Shared.go"); found {
		t.Fatal("fixture's src directory should start collapsed")
	}
	reviewNavigationSetFilter(t, tester, "src/client")
	if _, found := tester.Find("修改：src/client/Shared.go"); !found {
		t.Fatal("filter did not temporarily reveal its matching file inside a collapsed directory")
	}
	if state.ExpandedDirectories["src"] {
		t.Fatal("filter overwrote the user's collapsed-directory state")
	}
	tester.Key(0, ui.KeyEscape)
	if _, found := tester.Find("修改：src/client/Shared.go"); found || state.ExpandedDirectories["src"] {
		t.Fatal("clearing the filter did not restore normal directory expansion")
	}
	if state.FileScrolls["flat"] != flatScroll || flatScroll.Y != 73 {
		t.Fatal("filtering the tree changed the flat view's independent scroll position")
	}
}

func TestWorkspaceReviewNavigationVirtualizesRowsAndKeepsClickAndPrefetchIdentity(t *testing.T) {
	for _, view := range []string{"flat", "tree"} {
		t.Run(view, func(t *testing.T) {
			state := reviewNavigationTestState()
			state.FileView, state.SelectedPath = view, "src/file_0000.go"
			state.ExpandedDirectories = map[string]bool{"src": true}
			state.Files = make([]workspace.CompareChange, 1200)
			for i := range state.Files {
				state.Files[i] = workspace.CompareChange{Path: fmt.Sprintf("src/file_%04d.go", i), Kind: "MODIFIED"}
			}
			var selected workspace.CompareChange
			state.FileSelected = func(file workspace.CompareChange) { selected = file }
			s := &app{}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.renderWorkspaceReviewFileList(c, setTheme(c, false), state, "virtual-files") })
			}, 340, 420)
			countRows := func() int {
				n := 0
				for _, text := range tester.Texts() {
					if strings.HasPrefix(text, "修改：src/file_") {
						n++
					}
				}
				return n
			}
			if n := countRows(); n == 0 || n > 60 {
				t.Errorf("viewport built %d file rows for a 1200-file list; want 1..60", n)
			}
			if state.FileNavigation == nil {
				t.Fatal("file navigation did not retain its cached model between frames")
			}
			cached := state.FileNavigation
			tester.Frame()
			if state.FileNavigation != cached {
				t.Fatal("an unchanged frame replaced the navigation cache")
			}
			scroll := state.FileScrolls[view]
			if scroll == nil {
				t.Fatal("virtualized list did not retain the view's scroll state")
			}
			scroll.Y = 18000
			tester.Frame()
			if n := countRows(); n == 0 || n > 60 {
				t.Fatalf("scrolled viewport built %d file rows; want 1..60", n)
			}
			var clickedPath string
			for _, text := range tester.Texts() {
				if !strings.HasPrefix(text, "修改：src/file_") {
					continue
				}
				box, found := tester.Find(text)
				if found && box.W > 0 && box.H > 0 {
					clickedPath = strings.TrimPrefix(text, "修改：")
					if err := tester.Click(text); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if clickedPath == "" || selected.Path != clickedPath || selected.Kind != "MODIFIED" || selected.OldPath != "" {
				t.Fatalf("virtualized click identity = %#v, rendered path = %q", selected, clickedPath)
			}
			if len(state.PrefetchFrameVisible) == 0 || len(state.PrefetchFrameVisible) > 16 {
				t.Fatalf("prefetch visibility should contain 1..16 on-screen files, got %d", len(state.PrefetchFrameVisible))
			}
			for _, file := range state.PrefetchFrameVisible {
				box, found := tester.Find("修改：" + file.Path)
				if !found || box.W <= 0 || box.H <= 0 {
					t.Errorf("prefetch visibility included an off-screen or unbuilt file %q", file.Path)
				}
			}
		})
	}
}

func TestWorkspaceReviewNavigationComparisonSummaryTracksCurrentScope(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		change func(*workspaceHistoryState)
		want   []string
		absent []string
	}{
		{name: "mainline", want: []string{"共同祖先", "upstream/main", "HEAD", "aaaaaaaa", "bbbbbbbb"}},
		{name: "mainline_with_worktree", change: func(s *workspaceHistoryState) {
			s.IncludeWorkingTree = true
			s.TargetCommit = "worktree:" + strings.Repeat("b", 40) + ":" + strings.Repeat("c", 40)
		}, want: []string{"共同祖先", "upstream/main", "HEAD", "含本地未提交"}},
		{name: "uncommitted", change: func(s *workspaceHistoryState) {
			s.CompareMode, s.ResultBaselineRef = workspaceReviewUncommitted, "HEAD"
			s.TargetCommit = "worktree:" + strings.Repeat("b", 40) + ":" + strings.Repeat("c", 40)
		}, want: []string{"HEAD", "工作区"}, absent: []string{"共同祖先"}},
		{name: "commits", change: func(s *workspaceHistoryState) {
			s.CompareMode, s.ResultBaselineRef = workspaceReviewCommits, "提交范围"
			s.StartCommit, s.EndCommit, s.CommitHashes = "start001", "end00002", []string{"start001", "end00002"}
		}, want: []string{"第一父版本", "aaaaaaaa", "bbbbbbbb"}, absent: []string{"共同祖先"}},
		{name: "simulation", change: func(s *workspaceHistoryState) {
			s.CompareMode, s.SimulateOnMainline = workspaceReviewCommits, true
			s.StartCommit, s.EndCommit, s.CommitHashes = "start001", "end00002", []string{"start001", "end00002"}
			s.TargetCommit = "tree:" + strings.Repeat("c", 40)
		}, want: []string{"模拟", "upstream/main", "模拟结果"}, absent: []string{"共同祖先"}},
		{name: "loading_hides_previous_snapshot", change: func(s *workspaceHistoryState) {
			s.FilesLoading, s.IncludeWorkingTree = true, true
			s.ResultBaselineRef, s.BaseCommit, s.TargetCommit = "obsolete-main", "obsoletebase", "obsoletetarget"
		}, want: []string{"共同祖先", "upstream/main", "正在读取"}, absent: []string{"obsolete"}},
		{name: "error_hides_previous_snapshot", change: func(s *workspaceHistoryState) {
			s.Error = "读取新范围失败"
			s.ResultBaselineRef, s.BaseCommit, s.TargetCommit = "obsolete-main", "obsoletebase", "obsoletetarget"
		}, want: []string{"共同祖先", "upstream/main", "范围未就绪"}, absent: []string{"obsolete"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := reviewNavigationTestState()
			if testCase.change != nil {
				testCase.change(state)
			}
			_, tester := reviewNavigationTestPage(state, 1040, 650)
			if _, found := tester.Find("比较范围"); !found {
				t.Fatal("review has no persistent, accessible comparison-scope summary")
			}
			for _, value := range testCase.want {
				if !tester.HasText(value) {
					t.Errorf("summary is missing %q: %v", value, tester.Texts())
				}
			}
			for _, value := range testCase.absent {
				if tester.HasText(value) {
					t.Errorf("summary presented stale or wrong scope %q", value)
				}
			}
		})
	}
}

func TestWorkspaceReviewNavigationSummaryAndFilterFitNarrowPage(t *testing.T) {
	state := reviewNavigationTestState()
	state.BaselineDisplayRef = "upstream/" + strings.Repeat("long-module-name/", 8) + "main"
	_, tester := reviewNavigationTestPage(state, 540, 620)
	for _, label := range []string{"比较范围", "筛选文件"} {
		box, found := tester.Find(label)
		if !found || box.W <= 0 || box.H <= 0 || box.X < 0 || box.X+box.W > 540 {
			t.Errorf("%q does not fit the narrow page: found=%v, bounds=%+v", label, found, box)
		}
	}
}

func TestWorkspaceReviewNavigationCacheChangesOnlyWithSnapshotOrNavigation(t *testing.T) {
	state := reviewNavigationTestState()
	state.FileView = "tree"
	navigation := workspaceReviewNavigation(state)
	tree, flatRows := navigation.tree, &navigation.flatRows[0]
	rows := navigation.rows(state)
	for i := 0; i < 3; i++ {
		if next := workspaceReviewNavigation(state); next.tree != tree || &next.flatRows[0] != flatRows || &next.rows(state)[0] != &rows[0] {
			t.Fatal("an unchanged frame rebuilt the tree, sorting, or flattened navigation rows")
		}
	}
	var src workspaceReviewNavigationRow
	for _, row := range rows {
		if row.Directory != nil && row.Directory.Path == "src" {
			src = row
		}
	}
	if src.Directory == nil || src.Expanded {
		t.Fatal("fixture's collapsed src directory was not retained")
	}
	navigation.toggleDirectory(state, src)
	expandedRows := navigation.rows(state)
	if len(expandedRows) <= len(rows) || navigation.tree != tree || &navigation.flatRows[0] != flatRows {
		t.Fatal("directory expansion should rebuild visible tree rows and reuse the file snapshot")
	}
	state.Files = []workspace.CompareChange{
		{Path: "new/replacement.go", Kind: "ADDED"},
		{Path: "new/second.go", Kind: "MODIFIED"},
		{Path: "new/third.go", Kind: "DELETED"},
	}
	refreshed := workspaceReviewNavigation(state)
	if refreshed.tree == tree || len(refreshed.flatRows) != 3 || refreshed.flatRows[0].File.Path != "new/replacement.go" {
		t.Fatal("same-size refreshed file snapshot reused stale navigation rows")
	}
}

func TestWorkspaceReviewNavigationSummaryTooltipUsesCompleteSnapshotIdentity(t *testing.T) {
	state := reviewNavigationTestState()
	state.CompareMode, state.ResultBaselineRef = workspaceReviewCommits, "提交范围"
	state.StartCommit, state.EndCommit = strings.Repeat("1", 40), strings.Repeat("2", 40)
	state.CommitHashes = []string{state.StartCommit, state.EndCommit}
	_, tooltip := workspaceReviewComparisonSummary(state)
	for _, identity := range []string{state.BaseCommit, state.TargetCommit, state.StartCommit, state.EndCommit} {
		if !strings.Contains(tooltip, identity) {
			t.Errorf("scope tooltip lost full identity %q", identity)
		}
	}
	state.BaseCommit = ""
	display, tooltip := workspaceReviewComparisonSummary(state)
	if !strings.Contains(display, "空树") || strings.Contains(display, "第一父版本 ") || !strings.Contains(tooltip, "基准快照：空树") {
		t.Fatalf("root commit was not compared with the empty tree: display=%q, tooltip=%q", display, tooltip)
	}
	state.ActiveComparisonKey = "previous-comparison"
	display, tooltip = workspaceReviewComparisonSummary(state)
	if !strings.Contains(display, "范围未就绪") || strings.Contains(tooltip, state.TargetCommit) {
		t.Fatal("a pending different range was labeled with the previous loaded snapshot")
	}
}
