package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/workspace"
)

// The file result is an immutable snapshot: the history reader replaces the
// slice when it adopts a result. Keep the sorted files and directory rows until
// that snapshot, the query, or an explicit directory toggle changes them.
type workspaceReviewFileNavigation struct {
	Query string

	source        []workspace.CompareChange
	sourceSession string
	sourceScope   string
	initialized   bool
	files         []workspace.CompareChange
	tree          *workspaceReviewTreeDirectory
	flatRows      []workspaceReviewNavigationRow
	treeRows      []workspaceReviewNavigationRow

	querySource      string
	queryKey         string
	filteredFiles    []workspace.CompareChange
	filteredTree     *workspaceReviewTreeDirectory
	filteredFlatRows []workspaceReviewNavigationRow
	filteredTreeRows []workspaceReviewNavigationRow
	filteredExpanded map[string]bool
	lists            map[string]*ui.ListState
	filteredLists    map[string]*ui.ListState
	filteredScrolls  map[string]*ui.ScrollState
}

type workspaceReviewNavigationRow struct {
	Key       string
	File      workspace.CompareChange
	Directory *workspaceReviewTreeDirectory
	Name      string
	Chain     []string
	Depth     int
	Expanded  bool
}

func workspaceReviewNavigation(state *workspaceHistoryState) *workspaceReviewFileNavigation {
	if state.FileNavigation == nil {
		state.FileNavigation = &workspaceReviewFileNavigation{}
	}
	navigation := state.FileNavigation
	scope := workspaceReviewComparisonKey(state)
	changed := !navigation.initialized || navigation.sourceSession != state.DiffSessionID || navigation.sourceScope != scope || len(navigation.source) != len(state.Files)
	if !changed && len(state.Files) > 0 {
		changed = &navigation.source[0] != &state.Files[0]
	}
	if changed {
		navigation.initialized = true
		navigation.source, navigation.sourceSession, navigation.sourceScope = state.Files, state.DiffSessionID, scope
		navigation.files = workspaceReviewSortedFiles(state.Files)
		navigation.tree = workspaceReviewTree(navigation.files)
		navigation.flatRows = workspaceReviewFlatNavigationRows(navigation.files)
		navigation.treeRows = nil
		workspaceReviewEnsureExpandedDirectories(state, state.Files)
	}
	if changed || navigation.querySource != navigation.Query {
		key := strings.Join(strings.Fields(strings.ToLower(navigation.Query)), "\x00")
		if changed || key != navigation.queryKey {
			navigation.queryKey = key
			navigation.filteredFiles, navigation.filteredTree, navigation.filteredFlatRows = navigation.files, navigation.tree, navigation.flatRows
			if key != "" {
				navigation.filteredFiles = workspaceReviewFilterFiles(navigation.files, key)
				navigation.filteredTree = workspaceReviewTree(navigation.filteredFiles)
				navigation.filteredFlatRows = workspaceReviewFlatNavigationRows(navigation.filteredFiles)
			}
			navigation.filteredTreeRows = nil
			navigation.filteredExpanded = make(map[string]bool)
			navigation.filteredLists = make(map[string]*ui.ListState)
			navigation.filteredScrolls = make(map[string]*ui.ScrollState)
		}
		navigation.querySource = navigation.Query
	}
	return navigation
}

func workspaceReviewFilterFiles(files []workspace.CompareChange, queryKey string) []workspace.CompareChange {
	if queryKey == "" {
		return files
	}
	keywords := strings.Split(queryKey, "\x00")
	result := make([]workspace.CompareChange, 0)
	for _, file := range files {
		value := strings.ToLower(strings.ReplaceAll(file.Path+"\x00"+file.OldPath, "\\", "/"))
		matches := true
		for _, keyword := range keywords {
			if !strings.Contains(value, strings.ReplaceAll(keyword, "\\", "/")) {
				matches = false
				break
			}
		}
		if matches {
			result = append(result, file)
		}
	}
	return result
}

func workspaceReviewFlatNavigationRows(files []workspace.CompareChange) []workspaceReviewNavigationRow {
	rows := make([]workspaceReviewNavigationRow, len(files))
	for i, file := range files {
		rows[i] = workspaceReviewNavigationRow{Key: "file\x00" + file.Kind + "\x00" + file.OldPath + "\x00" + file.Path, File: file}
	}
	return rows
}

func (navigation *workspaceReviewFileNavigation) rows(state *workspaceHistoryState) []workspaceReviewNavigationRow {
	if state.FileView == "flat" {
		if navigation.queryKey != "" {
			return navigation.filteredFlatRows
		}
		return navigation.flatRows
	}
	if navigation.queryKey != "" {
		if navigation.filteredTreeRows == nil {
			navigation.filteredTreeRows = workspaceReviewTreeNavigationRows(navigation.filteredTree, navigation.filteredExpanded, true)
		}
		return navigation.filteredTreeRows
	}
	if navigation.treeRows == nil {
		navigation.treeRows = workspaceReviewTreeNavigationRows(navigation.tree, state.ExpandedDirectories, false)
	}
	return navigation.treeRows
}

func workspaceReviewTreeNavigationRows(root *workspaceReviewTreeDirectory, expanded map[string]bool, defaultExpanded bool) []workspaceReviewNavigationRow {
	rows := make([]workspaceReviewNavigationRow, 0)
	var appendFiles func(*workspaceReviewTreeDirectory, int)
	var appendDirectory func(*workspaceReviewTreeDirectory, int)
	appendFiles = func(directory *workspaceReviewTreeDirectory, depth int) {
		for _, file := range directory.Files {
			rows = append(rows, workspaceReviewNavigationRow{
				Key: "file\x00" + file.Kind + "\x00" + file.OldPath + "\x00" + file.Path, File: file, Depth: depth,
			})
		}
		for _, child := range workspaceReviewSortedDirectories(directory) {
			appendDirectory(child, depth)
		}
	}
	appendDirectory = func(directory *workspaceReviewTreeDirectory, depth int) {
		terminal := directory
		chain := []string{directory.Path}
		names := []string{directory.Name}
		for len(terminal.Files) == 0 && len(terminal.Directories) == 1 {
			terminal = workspaceReviewSortedDirectories(terminal)[0]
			chain = append(chain, terminal.Path)
			names = append(names, terminal.Name)
		}
		open := true
		for _, path := range chain {
			value, exists := expanded[path]
			if exists && !value || !exists && !defaultExpanded {
				open = false
			}
		}
		rows = append(rows, workspaceReviewNavigationRow{
			Key: "directory\x00" + terminal.Path, Directory: terminal, Name: strings.Join(names, "/"), Chain: chain, Depth: depth, Expanded: open,
		})
		if open {
			appendFiles(terminal, depth+1)
		}
	}
	appendFiles(root, 0)
	return rows
}

func (navigation *workspaceReviewFileNavigation) toggleDirectory(state *workspaceHistoryState, row workspaceReviewNavigationRow) {
	expanded := state.ExpandedDirectories
	if navigation.queryKey != "" {
		expanded = navigation.filteredExpanded
		navigation.filteredTreeRows = nil
	} else {
		navigation.treeRows = nil
	}
	for _, path := range row.Chain {
		expanded[path] = !row.Expanded
	}
}

func (navigation *workspaceReviewFileNavigation) listState(state *workspaceHistoryState) (*ui.ListState, *ui.ScrollState, string) {
	view := firstNonEmpty(state.FileView, "tree")
	if navigation.lists == nil {
		navigation.lists = make(map[string]*ui.ListState)
	}
	if state.FileScrolls == nil {
		state.FileScrolls = make(map[string]*ui.ScrollState)
	}
	lists, scrolls, suffix := navigation.lists, state.FileScrolls, view
	if navigation.queryKey != "" {
		lists, scrolls, suffix = navigation.filteredLists, navigation.filteredScrolls, view+"-filtered"
	}
	if lists[view] == nil {
		lists[view] = &ui.ListState{}
	}
	if scrolls[view] == nil {
		scrolls[view] = &ui.ScrollState{}
	}
	return lists[view], scrolls[view], suffix
}

func workspaceReviewParentPath(value string) string {
	segments := workspaceReviewPathSegments(value)
	if len(segments) < 2 {
		return "."
	}
	return strings.Join(segments[:len(segments)-1], "/")
}

func workspaceReviewComparisonSummary(state *workspaceHistoryState) (display, tooltip string) {
	baseline := firstNonEmpty(state.BaselineDisplayRef, state.BaselineRef, "主分支")
	ready := !state.FilesLoading && state.Error == "" && state.TargetCommit != "" &&
		(state.ActiveComparisonKey == "" || state.ActiveComparisonKey == workspaceReviewComparisonKey(state))
	base, target := "", ""
	if ready {
		base = " " + shortCommitID(state.BaseCommit)
		target = " " + shortCommitID(state.TargetCommit)
	}
	switch state.CompareMode {
	case workspaceReviewUncommitted:
		display = "HEAD" + base + " → 工作区"
	case workspaceReviewCommits:
		if len(state.CommitHashes) == 0 {
			return "请选择提交范围", "选择起点和终点后，比较起点的第一父版本与终点；根提交以空树为起点。"
		}
		if state.SimulateOnMainline {
			display = "模拟：" + baseline + base + " → 模拟结果"
			if ready {
				display += " " + shortCommitID(strings.TrimPrefix(state.TargetCommit, "tree:"))
			}
		} else {
			start := "起点第一父版本" + base
			if ready && state.BaseCommit == "" {
				start = "空树（根提交）"
			}
			display = start + " → 终点" + target
		}
	default:
		display = baseline + " 共同祖先" + base + " → HEAD"
		if ready {
			head := state.TargetCommit
			if strings.HasPrefix(head, "worktree:") {
				head = strings.Split(strings.TrimPrefix(head, "worktree:"), ":")[0]
			}
			display += " " + shortCommitID(head)
		}
		if state.IncludeWorkingTree {
			display += " · 含本地未提交"
		}
	}
	tooltip = display
	if !ready {
		status := "范围未就绪"
		if state.FilesLoading {
			status = "正在读取"
		}
		display += " · " + status
		tooltip = display
	} else {
		tooltip += "\n基准快照：" + firstNonEmpty(state.BaseCommit, "空树") + "\n目标快照：" + state.TargetCommit
		if state.CompareMode == workspaceReviewMainline || state.SimulateOnMainline {
			tooltip += "\n配置主分支：" + baseline + "\n本地引用：" + state.BaselineRef + "\n结果主分支：" + state.ResultBaselineRef
		}
		if state.CompareMode == workspaceReviewCommits {
			tooltip += "\n所选提交：" + state.StartCommit + " → " + state.EndCommit
		}
		if state.Notice != "" {
			tooltip += "\n" + state.Notice
		}
	}
	return display, tooltip
}
