package desktop

import (
	"encoding/json"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type workspaceHistoryState struct {
	TaskID, ServiceID, ModuleID, Path string
	TaskPath, TaskCreatedAt           string
	RepositorySessionKey              string
	TaskSessionKey                    string
	Mode                              string // review, history, or legacy manual-proof mode
	Open                              bool
	HistoryFocus                      ui.Handle
	HasHistoryFocus                   bool
	ExpandedCommits                   map[string]bool
	Error, SelectedPath               string
	SelectedKind                      domain.WorkspaceGitFileChangeKind
	SelectedOldPath                   string
	// Loading, AllHistory and Health remain for the existing manual-proof
	// harnesses that exercise the retired dialog flow.
	Loading                         bool
	AllHistory                      bool
	Health                          domain.WorkspaceGitHealth
	BaselineRef, BaselineDisplayRef string
	ResultBaselineRef               string
	BaseCommit, TargetCommit        string
	DiffSessionID                   string
	StartCommit, EndCommit          string
	Notice                          string
	CompareMode                     string
	CommitHashes                    []string
	IncludeWorkingTree              bool
	SimulateOnMainline              bool
	CommitPickerOpen                bool
	CommitPickerScroll              ui.ScrollState
	FileView                        string
	ExpandedDirectories             map[string]bool
	FileScrolls                     map[string]*ui.ScrollState
	ActiveComparisonKey             string
	ActiveDiffRendererKey           string
	ReviewSelections                map[string]workspaceReviewReadingState
	DiffRendererStates              map[string]*workspaceDiffRendererState
	SplitRatio                      float32
	Files                           []workspace.CompareChange
	FilesLoading, HistoryLoading    bool
	PreviewLoading                  bool
	CommitsLoaded                   bool
	ReviewGeneration                uint64
	CommitSelectionGeneration       uint64
	CommitDebounce                  *time.Timer
	InitialChange                   *domain.WorkspaceGitFileChange
	LeftWidth                       float32
	LeftCollapsed                   bool
	LeftDragging                    bool
	PreviewMode                     workspace.WorkspaceFilePreviewMode
	ChangedLinesOnly                bool
	DiffHunkIndex                   int
	DiffHunkSelected                bool
	NativeDiff                      *diffNativeViewState
	NativeDiffIdentity              *workspaceDiffIdentity
	DiffRows                        *workspaceDiffRowsView
	ContentPreview                  nativeCodePreviewState
	Commits                         []domain.WorkspaceGitCommit
	Preview                         *workspace.WorkspaceGitFilePreview
	PreviewStartedAt                time.Time
	PreviewFirstFrame               bool
	Prefetch                        *workspaceReviewPrefetchJob
	PrefetchKey                     string
	PrefetchVisible                 []workspace.CompareChange
	PrefetchFrameVisible            []workspace.CompareChange
	PrefetchViewport                ui.Rect
}

const (
	workspaceReviewMainline    = "MAINLINE"
	workspaceReviewCommits     = "COMMITS"
	workspaceReviewUncommitted = "UNCOMMITTED"
)

func (s *app) openWorkspaceHistory(task task, item application.Workspace, focus ...*ui.Handle) {
	s.dismissWorkspaceHistory()
	displayRef, localRef := s.workspaceHistoryBaseline(task, item)
	state := &workspaceHistoryState{
		TaskID: task.id, TaskPath: task.path, TaskCreatedAt: task.createdAt,
		ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		Mode: "history", Open: true, BaselineRef: localRef, BaselineDisplayRef: displayRef,
		AllHistory: false, Loading: localRef != "", HistoryLoading: localRef != "",
		ExpandedCommits: make(map[string]bool),
	}
	if len(focus) > 0 && focus[0] != nil {
		state.HistoryFocus = *focus[0]
		state.HasHistoryFocus = true
	}
	s.workspaceHistory = state
	if localRef == "" {
		state.Error = "请先在仓库配置中设置此模块的主分支。"
		return
	}
	s.act("workspace.history", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"baselineRef": localRef,
		"allHistory":  false,
	})
}

func (s *app) workspaceHistoryBaseline(task task, item application.Workspace) (displayRef, localRef string) {
	_ = task
	var sourceRef string
	for _, service := range s.snapshot.Services {
		if service.ID != item.ServiceID || task.groupID != "" && service.GroupID != task.groupID {
			continue
		}
		sourceRef = strings.TrimSpace(service.MasterBranch)
		for _, module := range service.Modules {
			if module.ID == item.ModuleID && strings.TrimSpace(module.MasterBranch) != "" {
				sourceRef = strings.TrimSpace(module.MasterBranch)
				break
			}
		}
		break
	}
	displayRef, localRef = sourceRef, sourceRef
	if sourceRef == "" || item.Record.Strategy != domain.WorkspaceIndependentClone {
		return displayRef, localRef
	}
	ref, err := workspace.ParseRemoteBranchRef(sourceRef)
	if err == nil {
		ref.Remote = "origin"
		localRef = workspace.FormatRemoteBranchRef(ref)
	}
	return displayRef, localRef
}

func (s *app) openWorkspaceDirtyFiles(task task, item application.Workspace, health domain.WorkspaceGitHealth, focus ...*ui.Handle) {
	var initial *domain.WorkspaceGitFileChange
	var returnFocus *ui.Handle
	if len(focus) > 0 {
		returnFocus = focus[0]
	}
	if len(health.DirtyFiles) > 0 {
		change := health.DirtyFiles[0]
		initial = &change
	}
	s.openWorkspaceReview(task, item, workspaceReviewUncommitted, initial, returnFocus)
}

func (s *app) openWorkspaceReview(task task, item application.Workspace, mode string, initial *domain.WorkspaceGitFileChange, focus *ui.Handle) {
	s.openWorkspaceReviewWithCommits(task, item, mode, initial, focus, nil, nil)
}

func (s *app) openWorkspaceReviewWithCommits(task task, item application.Workspace, mode string, initial *domain.WorkspaceGitFileChange, focus *ui.Handle, commits []domain.WorkspaceGitCommit, selectedHashes []string) {
	s.dismissWorkspaceHistory()
	displayRef, localRef := s.workspaceHistoryBaseline(task, item)
	jobs := s.ensureWorkspaceHistoryJobs()
	jobs.pageGeneration++
	session, repositoryKey, taskKey := s.ensureWorkspaceReviewSession(task, item)
	state := &workspaceHistoryState{
		TaskID: task.id, TaskPath: task.path, TaskCreatedAt: task.createdAt,
		ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		Mode: "review", Open: true, CompareMode: mode, BaselineRef: localRef,
		BaselineDisplayRef: displayRef, InitialChange: initial, LeftWidth: 260,
		RepositorySessionKey: repositoryKey, TaskSessionKey: taskKey,
		FileView: "tree", ExpandedDirectories: make(map[string]bool), FileScrolls: make(map[string]*ui.ScrollState),
		ExpandedCommits:  make(map[string]bool),
		ReviewSelections: session.TaskReadings[taskKey], DiffRendererStates: session.RendererStates,
		SplitRatio:       session.SplitRatio,
		ReviewGeneration: jobs.pageGeneration, Commits: append([]domain.WorkspaceGitCommit(nil), commits...),
		CommitsLoaded: commits != nil, CommitHashes: append([]string(nil), selectedHashes...),
	}
	if state.SplitRatio <= 0 {
		state.SplitRatio = 0.5
	}
	if len(state.CommitHashes) == 1 {
		state.StartCommit, state.EndCommit = state.CommitHashes[0], state.CommitHashes[0]
	}
	s.workspaceHistory = state
	if focus != nil {
		s.workspaceReviewFocusReturn = &workspaceReviewFocusReturn{
			Handle: *focus, TaskID: task.id, TaskPath: task.path, TaskCreatedAt: task.createdAt, Page: s.page,
		}
	}
	if mode == workspaceReviewCommits && len(state.CommitHashes) == 0 {
		state.CommitPickerOpen = true
		s.readWorkspaceReviewCommits(state)
		return
	}
	s.readWorkspaceReviewFiles(state)
}

func (s *app) cancelWorkspaceReviewDebounce(state *workspaceHistoryState) {
	if state == nil || state.CommitDebounce == nil {
		return
	}
	state.CommitDebounce.Stop()
	state.CommitDebounce = nil
}

func (s *app) readWorkspaceReviewCommits(state *workspaceHistoryState) {
	if state == nil || state.Mode != "review" || state.CompareMode != workspaceReviewCommits || s.workspaceHistory != state {
		return
	}
	if strings.TrimSpace(state.BaselineRef) == "" {
		state.HistoryLoading = false
		state.Error = "请先在仓库配置中设置此模块的主分支。"
		return
	}
	state.HistoryLoading = true
	state.Loading = true
	state.Error = ""
	s.act("workspace.history", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"baselineRef": state.BaselineRef,
		"allHistory":  false,
	})
}

// The compatibility helpers below keep the package's focused manual-proof
// harnesses compiling while the visible UI now routes to the review page.
func (s *app) setWorkspaceHistoryScope(state *workspaceHistoryState, all bool) {
	if state == nil || state.Mode != "history" || s.workspaceHistory != state || !state.Open {
		return
	}
	s.cancelWorkspaceHistoryRead()
	state.AllHistory, state.Loading = all, true
	state.Error = ""
	baseline := state.BaselineRef
	if all {
		baseline = ""
	}
	s.act("workspace.history", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"baselineRef": baseline,
		"allHistory":  all,
	})
}

func (s *app) selectWorkspaceDirtyFile(change domain.WorkspaceGitFileChange) {
	state := s.workspaceHistory
	if state == nil || state.Mode != "dirty-files" || !state.Open {
		return
	}
	s.cancelWorkspaceHistoryRead()
	state.SelectedPath, state.SelectedKind = change.Path, change.Kind
	state.Loading, state.Error, state.Preview = true, "", nil
	s.act("workspace.file-preview", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"change": map[string]any{"path": change.Path, "kind": change.Kind},
	})
}

func workspaceReviewComparePayload(state *workspaceHistoryState) map[string]any {
	payload := map[string]any{"mode": state.CompareMode}
	switch state.CompareMode {
	case workspaceReviewMainline:
		payload["includeWorkingTree"] = state.IncludeWorkingTree
	case workspaceReviewCommits:
		payload["commitHashes"] = append([]string(nil), state.CommitHashes...)
		payload["startCommit"], payload["endCommit"] = state.StartCommit, state.EndCommit
		payload["simulateOnMainline"] = state.SimulateOnMainline
	}
	return payload
}

func (s *app) readWorkspaceReviewFiles(state *workspaceHistoryState) {
	if state == nil || state.Mode != "review" || state.CompareMode == workspaceReviewCommits && len(state.CommitHashes) == 0 || s.workspaceHistory != state {
		return
	}
	s.cancelWorkspaceReviewDebounce(state)
	s.cancelWorkspaceReviewPrefetch(state)
	comparisonKey := workspaceReviewComparisonKey(state)
	if state.ActiveComparisonKey != comparisonKey {
		s.releaseWorkspaceReviewDiffSession(state)
		state.ActiveComparisonKey = comparisonKey
		state.Files = nil
		state.Preview = nil
		state.BaseCommit, state.TargetCommit = "", ""
		state.SelectedPath, state.SelectedOldPath, state.SelectedKind = "", "", ""
		if saved, ok := state.ReviewSelections[comparisonKey]; ok {
			state.SelectedPath, state.SelectedOldPath = saved.Path, saved.OldPath
			state.SelectedKind = saved.Kind
			state.PreviewMode, state.ChangedLinesOnly = saved.PreviewMode, saved.ChangedOnly
			state.FileView = firstNonEmpty(saved.FileView, "tree")
			state.ExpandedDirectories = cloneBoolMap(saved.ExpandedDirectories)
			state.FileScrolls = cloneFileScrollMap(saved.FileScrolls)
		}
		state.resetWorkspaceDiffView()
		state.DiffRows = nil
		state.resetWorkspaceContentPreview()
	}
	state.FilesLoading = true
	state.Error = ""
	s.act("workspace.diff-files", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"compare": workspaceReviewComparePayload(state),
	})
}

func (s *app) selectWorkspaceReviewFile(state *workspaceHistoryState, change workspace.CompareChange) {
	if state == nil || s.workspaceHistory != state || state.Mode != "review" || !state.Open {
		return
	}
	if state.SelectedPath == change.Path && state.SelectedOldPath == change.OldPath && string(state.SelectedKind) == change.Kind && state.PreviewLoading {
		return
	}
	s.cancelWorkspaceReviewPrefetch(state)
	s.cancelWorkspaceHistoryRead()
	s.saveWorkspaceReviewSelection(state)
	state.SelectedPath, state.SelectedOldPath = change.Path, change.OldPath
	state.SelectedKind = domain.WorkspaceGitFileChangeKind(change.Kind)
	state.Preview = nil
	state.PreviewMode = ""
	state.ChangedLinesOnly = false
	state.resetWorkspaceDiffView()
	state.DiffRows = nil
	state.resetWorkspaceContentPreview()
	state.Error = ""
	state.PreviewLoading = true
	state.PreviewStartedAt = time.Now()
	state.PreviewFirstFrame = false
	compare := workspaceReviewComparePayload(state)
	if !workspaceReviewSnapshotsReady(state) {
		state.PreviewLoading = false
		state.Error = "比较快照尚未就绪，请刷新文件列表后重试。"
		return
	}
	compare["expectedBaseCommit"] = state.BaseCommit
	compare["expectedTargetCommit"] = state.TargetCommit
	s.act("workspace.diff-preview", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"sessionId": state.DiffSessionID,
		"compare":   compare,
		"change":    map[string]any{"path": change.Path, "oldPath": change.OldPath, "kind": change.Kind},
	})
}

// A root commit has no base commit ID: its diff is anchored to the empty tree.
// TargetCommit is the immutable snapshot identity required to safely request
// the preview; an empty BaseCommit is a valid value and must be sent as-is.
func workspaceReviewSnapshotsReady(state *workspaceHistoryState) bool {
	return state != nil && strings.TrimSpace(state.DiffSessionID) != "" && strings.TrimSpace(state.TargetCommit) != ""
}

func (s *app) setWorkspaceReviewMode(state *workspaceHistoryState, mode string) {
	if state == nil || state != s.workspaceHistory || !state.Open || state.Mode != "review" || state.CompareMode == mode {
		return
	}
	s.cancelWorkspaceReviewDebounce(state)
	s.cancelWorkspaceHistoryRead()
	s.saveWorkspaceReviewSelection(state)
	state.CompareMode = mode
	state.FilesLoading, state.HistoryLoading, state.PreviewLoading = false, false, false
	state.Error = ""
	state.Notice = ""
	if mode == workspaceReviewCommits {
		if len(state.CommitHashes) == 0 {
			state.CommitPickerOpen = true
			state.StartCommit, state.EndCommit = "", ""
			s.clearWorkspaceReviewResult(state)
		}
		if !state.CommitsLoaded {
			s.readWorkspaceReviewCommits(state)
		} else if len(state.CommitHashes) > 0 {
			s.readWorkspaceReviewFiles(state)
		}
		return
	}
	state.CommitPickerOpen = false
	s.readWorkspaceReviewFiles(state)
}

func (s *app) selectWorkspaceReviewCommit(state *workspaceHistoryState, hash string) {
	if state == nil || state != s.workspaceHistory || !state.Open || state.CompareMode != workspaceReviewCommits {
		return
	}
	start, end, hashes, err := workspaceReviewNextCommitRange(state.Commits, state.StartCommit, state.EndCommit, hash, state.CommitHashes)
	if err != nil {
		state.Error = err.Error()
		return
	}
	s.saveWorkspaceReviewSelection(state)
	state.StartCommit, state.EndCommit = start, end
	state.CommitHashes = hashes
	state.CommitSelectionGeneration++
	generation := state.CommitSelectionGeneration
	s.cancelWorkspaceReviewDebounce(state)
	s.cancelWorkspaceHistoryRead()
	state.FilesLoading, state.PreviewLoading = false, false
	state.Error = ""
	if len(state.CommitHashes) == 0 {
		state.CommitPickerOpen = true
		s.clearWorkspaceReviewResult(state)
		return
	}
	// A new range must never label the previous range's code as its result,
	// including the short debounce interval before the request is sent.
	s.clearWorkspaceReviewResult(state)
	state.ActiveComparisonKey = ""
	state.FilesLoading = true
	state.CommitDebounce = time.AfterFunc(250*time.Millisecond, func() {
		if s.win == nil || s.win.IsDestroyed() {
			return
		}
		s.win.Update(func() {
			if s.workspaceHistory != state || state.ReviewGeneration != s.workspaceHistory.ReviewGeneration ||
				!s.workspaceReviewPageVisible() || state.CommitSelectionGeneration != generation ||
				state.CompareMode != workspaceReviewCommits || len(state.CommitHashes) == 0 {
				return
			}
			state.CommitDebounce = nil
			s.readWorkspaceReviewFiles(state)
		})
	})
}

func (s *app) clearWorkspaceReviewResult(state *workspaceHistoryState) {
	if state == nil {
		return
	}
	s.releaseWorkspaceReviewDiffSession(state)
	state.resetWorkspaceDiffView()
	state.DiffRows = nil
	state.resetWorkspaceContentPreview()
	state.Files = nil
	state.Preview = nil
	state.SelectedPath, state.SelectedOldPath, state.SelectedKind = "", "", ""
	state.BaseCommit, state.TargetCommit = "", ""
	state.FilesLoading, state.PreviewLoading = false, false
	state.ActiveComparisonKey = workspaceReviewComparisonKey(state)
}

// dismissWorkspaceHistory cancels the visible review and invalidates its session.
func (s *app) dismissWorkspaceHistory() {
	s.cancelWorkspaceHistoryRead()
	state := s.workspaceHistory
	if state != nil {
		s.releaseWorkspaceReviewDiffSession(state)
		if state.Mode == "review" {
			s.saveWorkspaceReviewSession(state)
		}
		s.cancelWorkspaceReviewDebounce(state)
		state.Open = false
		state.releaseWorkspaceReviewRenderData()
		state.ReviewGeneration++
	}
	if jobs := s.workspaceHistoryJobs; jobs != nil {
		jobs.pageGeneration++
	}
	s.workspaceHistory = nil
	s.workspaceReviewFocusReturn = nil
}

func (s *app) backWorkspaceReview(state *workspaceHistoryState) {
	if state == nil || s.workspaceHistory != state {
		return
	}
	s.cancelWorkspaceHistoryRead()
	s.cancelWorkspaceReviewDebounce(state)
	s.releaseWorkspaceReviewDiffSession(state)
	state.Open = false
	s.saveWorkspaceReviewSession(state)
	if s.workspaceReviewFocusReturn != nil {
		s.workspaceReviewFocusReturn.deferOneFrame = true
	}
	state.releaseWorkspaceReviewRenderData()
	state.ReviewGeneration++
	if s.currentTask().id == state.TaskID {
		s.requestWorkspaceGitStatus(true)
	}
}

func (state *workspaceHistoryState) releaseWorkspaceReviewRenderData() {
	state.resetWorkspaceDiffView()
	state.DiffRows = nil
	state.resetWorkspaceContentPreview()
	state.Preview = nil
}

// applyWorkspaceHistoryResult applies legacy history metadata and the new
// selected-comparison file/preview responses to the current review session.
func (s *app) applyWorkspaceHistoryResult(action application.Action, result application.ActionResult) {
	state := s.workspaceHistory
	if state == nil || !state.matches(action) {
		return
	}
	switch action.Kind {
	case "workspace.history":
		var commits []domain.WorkspaceGitCommit
		if err := decodeWorkspaceSlice(result.Data, &commits); err != nil {
			state.HistoryLoading = false
			state.Loading = false
			state.Error = "提交历史格式错误：" + err.Error()
			return
		}
		state.Commits = commits
		state.CommitsLoaded = true
		state.HistoryLoading = false
		state.Loading = false
		state.Error = ""
		if state.Mode == "review" && state.CompareMode == workspaceReviewCommits && len(state.CommitHashes) > 0 {
			if len(state.CommitHashes) == 1 {
				state.StartCommit, state.EndCommit = state.CommitHashes[0], state.CommitHashes[0]
			}
			_, hasStart := workspaceReviewCommitByHash(state.Commits, state.StartCommit)
			_, hasEnd := workspaceReviewCommitByHash(state.Commits, state.EndCommit)
			if !hasStart || !hasEnd {
				state.StartCommit, state.EndCommit, state.CommitHashes = "", "", nil
				state.CommitPickerOpen = true
				s.clearWorkspaceReviewResult(state)
				state.Error = "所选提交已不在当前分支的新增提交中，请重新选择。"
				return
			}
			s.readWorkspaceReviewFiles(state)
		}
	case "workspace.file-preview":
		var preview workspace.WorkspaceGitFilePreview
		if err := decodeWorkspaceValue(result.Data, &preview); err != nil {
			state.Loading = false
			state.Error = "文件差异格式错误：" + err.Error()
			return
		}
		state.Preview = &preview
		state.PreviewMode = preview.DefaultMode()
		state.Loading = false
		state.Error = ""
	case "workspace.diff-files":
		var files workspace.CompareFilesResult
		if err := decodeWorkspaceValue(result.Data, &files); err != nil {
			s.releaseWorkspaceDiffSessionResult(result)
			state.FilesLoading = false
			state.Error = "差异文件列表格式错误：" + err.Error()
			return
		}
		if strings.TrimSpace(files.SessionID) == "" {
			s.releaseWorkspaceDiffSessionResult(result)
			s.releaseWorkspaceReviewDiffSession(state)
			state.FilesLoading = false
			state.Error = "Git 差异会话未建立，请重新读取文件列表。"
			return
		}
		previousFiles := append([]workspace.CompareChange(nil), state.Files...)
		previousBase, previousTarget := state.BaseCommit, state.TargetCommit
		previousPath, previousOldPath, previousKind := state.SelectedPath, state.SelectedOldPath, state.SelectedKind
		hadPreview := state.Preview != nil
		state.Files = files.Files
		previousSessionID := state.DiffSessionID
		if len(files.Files) == 0 {
			state.DiffSessionID = ""
			s.releaseWorkspaceGitDiffSession(files.SessionID)
			s.releaseWorkspaceGitDiffSession(previousSessionID)
		} else {
			state.DiffSessionID = files.SessionID
			if previousSessionID != files.SessionID {
				s.releaseWorkspaceGitDiffSession(previousSessionID)
			}
		}
		state.ResultBaselineRef = files.BaselineRef
		state.BaseCommit, state.TargetCommit = files.BaseCommit, files.TargetCommit
		state.Notice = files.Notice
		state.FilesLoading = false
		state.Error = ""
		if change, ok := workspaceReviewInitialSelection(state.Files, state.InitialChange); ok {
			state.InitialChange = nil
			s.selectWorkspaceReviewFile(state, change)
		} else if selected, ok := workspaceReviewSelection(state.Files, state.SelectedPath, state.SelectedOldPath, state.SelectedKind); ok {
			if hadPreview && previousBase == state.BaseCommit && previousTarget == state.TargetCommit &&
				previousPath == selected.Path && previousOldPath == selected.OldPath && previousKind == domain.WorkspaceGitFileChangeKind(selected.Kind) {
				state.SelectedPath, state.SelectedOldPath = selected.Path, selected.OldPath
				state.SelectedKind = domain.WorkspaceGitFileChangeKind(selected.Kind)
				s.saveWorkspaceReviewSelection(state)
			} else {
				s.selectWorkspaceReviewFile(state, selected)
			}
		} else if len(state.Files) > 0 {
			state.InitialChange = nil
			index := workspaceReviewPreviousSelectionIndex(previousFiles, previousPath, previousOldPath, previousKind)
			if index >= len(state.Files) {
				index = len(state.Files) - 1
			}
			s.selectWorkspaceReviewFile(state, state.Files[index])
		} else {
			state.resetWorkspaceDiffView()
			state.DiffRows = nil
			state.resetWorkspaceContentPreview()
			state.SelectedPath, state.SelectedOldPath, state.SelectedKind = "", "", ""
			state.Preview = nil
			state.PreviewLoading = false
		}
	case "workspace.diff-preview":
		var preview workspace.WorkspaceGitFilePreview
		if err := decodeWorkspaceValue(result.Data, &preview); err != nil {
			state.PreviewLoading = false
			state.Error = "文件差异格式错误：" + err.Error()
			return
		}
		state.Preview = &preview
		state.PreviewMode = preview.DefaultMode()
		state.ChangedLinesOnly = false
		state.resetWorkspaceDiffView()
		state.DiffRows = nil
		state.resetWorkspaceContentPreview()
		state.PreviewLoading = false
		s.saveWorkspaceReviewSelection(state)
		s.updateWorkspaceReviewPrefetch(state, nil)
	}
}

func workspaceReviewPreviousSelectionIndex(files []workspace.CompareChange, path, oldPath string, kind domain.WorkspaceGitFileChangeKind) int {
	for index, file := range files {
		if file.Path == path && file.OldPath == oldPath && file.Kind == string(kind) {
			return index
		}
	}
	return 0
}

func workspaceReviewInitialSelection(files []workspace.CompareChange, initial *domain.WorkspaceGitFileChange) (workspace.CompareChange, bool) {
	if initial == nil {
		return workspace.CompareChange{}, false
	}
	for _, file := range files {
		if file.Path == initial.Path && file.Kind == string(initial.Kind) {
			return file, true
		}
	}
	return workspace.CompareChange{}, false
}

// applyWorkspaceHistoryFailure leaves the failed request visible and never
// replaces another page, task, mode or selected file.
func (s *app) applyWorkspaceHistoryFailure(action application.Action, err error) {
	state := s.workspaceHistory
	if state == nil || !state.matches(action) {
		return
	}
	switch action.Kind {
	case "workspace.history":
		state.Loading = false
		if state.HistoryLoading {
			state.HistoryLoading = false
			state.Error = err.Error()
		}
	case "workspace.diff-files":
		if state.FilesLoading {
			state.FilesLoading = false
			state.Error = err.Error()
		}
	case "workspace.diff-preview":
		if state.PreviewLoading {
			state.PreviewLoading = false
			state.Error = err.Error()
		}
	case "workspace.file-preview":
		state.Loading = false
		state.Error = err.Error()
	}
}

func (state *workspaceHistoryState) matches(action application.Action) bool {
	if state == nil || action.TaskID != state.TaskID || action.ServiceID != state.ServiceID ||
		action.ModuleID != state.ModuleID || workspaceHealthPathKey(action.Path) != workspaceHealthPathKey(state.Path) {
		return false
	}
	switch action.Kind {
	case "workspace.history":
		baseline, _ := action.Payload["baselineRef"].(string)
		if state.Mode == "review" {
			return state.CompareMode == workspaceReviewCommits && state.HistoryLoading && baseline == state.BaselineRef && !workspacePayloadBool(action.Payload, "allHistory")
		}
		return state.Mode == "history" && state.Loading && workspaceHistoryAllScope(action) == state.AllHistory && baseline == state.BaselineRef
	case "workspace.file-preview":
		return state.Mode == "dirty-files" && state.Loading &&
			nestedPayloadString(action.Payload, "change", "path") == state.SelectedPath &&
			nestedPayloadString(action.Payload, "change", "kind") == string(state.SelectedKind)
	case "workspace.diff-files":
		return state.Mode == "review" && state.Open && state.FilesLoading && workspaceComparePayloadMatches(state, action.Payload)
	case "workspace.diff-preview":
		return state.Mode == "review" && state.Open && state.PreviewLoading && workspaceComparePayloadMatches(state, action.Payload) &&
			stringPayloadValue(action.Payload["sessionId"]) == state.DiffSessionID &&
			nestedPayloadString(action.Payload, "change", "path") == state.SelectedPath &&
			nestedPayloadString(action.Payload, "change", "oldPath") == state.SelectedOldPath &&
			nestedPayloadString(action.Payload, "change", "kind") == string(state.SelectedKind)
	default:
		return false
	}
}

func workspaceComparePayloadMatches(state *workspaceHistoryState, payload map[string]any) bool {
	compare, ok := payload["compare"].(map[string]any)
	if !ok || stringPayloadValue(compare["mode"]) != state.CompareMode {
		return false
	}
	if state.CompareMode == workspaceReviewCommits {
		hashes := workspacePayloadStrings(compare["commitHashes"])
		if len(hashes) != len(state.CommitHashes) {
			return false
		}
		for index := range hashes {
			if hashes[index] != state.CommitHashes[index] {
				return false
			}
		}
	}
	if state.CompareMode == workspaceReviewCommits &&
		(stringPayloadValue(compare["startCommit"]) != state.StartCommit || stringPayloadValue(compare["endCommit"]) != state.EndCommit ||
			workspacePayloadBool(compare, "simulateOnMainline") != state.SimulateOnMainline) {
		return false
	}
	if state.CompareMode == workspaceReviewMainline && workspacePayloadBool(compare, "includeWorkingTree") != state.IncludeWorkingTree {
		return false
	}
	if _, hasExpectedBase := compare["expectedBaseCommit"]; hasExpectedBase && stringPayloadValue(compare["expectedBaseCommit"]) != state.BaseCommit {
		return false
	}
	if _, hasExpectedTarget := compare["expectedTargetCommit"]; hasExpectedTarget && stringPayloadValue(compare["expectedTargetCommit"]) != state.TargetCommit {
		return false
	}
	return true
}

func workspacePayloadStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func stringPayloadValue(value any) string {
	text, _ := value.(string)
	return text
}

func workspacePayloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func nestedPayloadString(payload map[string]any, key, child string) string {
	value, ok := payload[key].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := value[child].(string)
	return text
}

func (s *app) renderWorkspaceFilePreview(c *ui.Context, p palette, state *workspaceHistoryState, preview *workspace.WorkspaceGitFilePreview) {
	if preview == nil {
		return
	}
	if preview.UnavailableReason != "" {
		label(c, preview.UnavailableReason, 11, p.amber).Wrap()
	}
	if preview.Notice != "" {
		label(c, preview.Notice, 11, p.muted).Wrap()
	}
	comparison := preview.Comparison
	hasComparison := comparison != nil && len(comparison.Rows) > 0
	s.workspaceNativeDiffView(state, comparison)
	hasContent := preview.Content != nil
	// An actual added file has one meaningful source. Keep the user's normal
	// comparison/content preference untouched while showing its full code.
	if state.SelectedKind == domain.WorkspaceGitAdded {
		mode := workspace.PreviewContent
		if hasComparison {
			mode = workspace.PreviewComparison
		}
		if message := workspaceFilePreviewTruncationMessage(preview, mode); message != "" {
			label(c, message, 11, p.amber).Wrap()
		}
		if hasComparison {
			s.renderWorkspaceComparison(c, p, state, comparison, false)
		} else if hasContent {
			renderWorkspaceFileContentPreview(c, p, state, preview).Grow(1)
		}
		return
	}
	if state.PreviewMode == "" {
		state.PreviewMode = preview.DefaultMode()
	}
	state.PreviewMode = workspacePreviewDisplayedMode(preview, state.PreviewMode)
	if hasComparison || hasContent {
		ui.Row(c).FillWidth().Gap(5).Children(func() {
			if hasComparison && hasContent {
				if workspaceHistoryChoice(c, "workspace-preview-comparison", "左右对比", state.PreviewMode == workspace.PreviewComparison, p).Clicked() {
					state.PreviewMode = workspace.PreviewComparison
				}
				contentLabel := "文件内容"
				switch preview.Content.Origin {
				case workspace.PreviewOriginHead:
					contentLabel = "HEAD 内容"
				case workspace.PreviewOriginMainline, workspace.PreviewOriginBaseline:
					contentLabel = "主分支基线"
				case workspace.PreviewOriginSelectedCommits:
					contentLabel = "选中提交内容"
				}
				if workspaceHistoryChoice(c, "workspace-preview-content", contentLabel, state.PreviewMode == workspace.PreviewContent, p).Clicked() {
					state.PreviewMode = workspace.PreviewContent
				}
			}
			ui.Spacer(c)
			if hasComparison && state.PreviewMode == workspace.PreviewComparison {
				label := "仅看改动"
				if state.ChangedLinesOnly {
					label = "查看全部"
				}
				if workspaceHistoryChoice(c, "workspace-preview-lines", label, state.ChangedLinesOnly, p).Clicked() {
					state.ChangedLinesOnly = !state.ChangedLinesOnly
				}
			}
		})
	}
	if message := workspaceFilePreviewTruncationMessage(preview, state.PreviewMode); message != "" {
		label(c, message, 11, p.amber).Wrap()
	}
	if state.PreviewMode == workspace.PreviewComparison && hasComparison {
		s.renderWorkspaceComparison(c, p, state, comparison, state.ChangedLinesOnly)
		return
	}
	if state.PreviewMode == workspace.PreviewContent && hasContent {
		renderWorkspaceFileContentPreview(c, p, state, preview).Grow(1)
		return
	}
	if !hasComparison && !hasContent && preview.UnavailableReason != "" {
		label(c, preview.UnavailableReason, 11, p.muted).Wrap()
	}
}

// renderWorkspaceCommitHistory and renderWorkspaceDirtyFilePreview are retained
// for the existing manual-proof harnesses. The normal UI now uses the full-page
// review experience below.
func (s *app) renderWorkspaceCommitHistory(c *ui.Context, p palette, state *workspaceHistoryState) {
	if state == nil {
		return
	}
	if state.Loading {
		label(c, "正在读取提交历史…", 12, p.muted)
		return
	}
	if len(state.Commits) == 0 {
		label(c, "相对服务基线没有新增提交。", 12, p.muted).Wrap()
		return
	}
	ui.Scroll(c).Fill().Gap(8).Children(func() {
		for _, commit := range state.Commits {
			ui.Column(c.Key("workspace-legacy-commit-"+commit.FullHash)).FillWidth().Gap(5).Padding(10).Background(p.surface).Border(1, p.line).Radius(9).Children(func() {
				label(c, commit.FullHash, 11, p.muted).Wrap()
				label(c, commit.Message, 13, p.text).Wrap()
				committerName := strings.TrimSpace(commit.CommitterName)
				if committerName == "" {
					committerName = "未知提交人"
				}
				if strings.TrimSpace(commit.CommitterEmail) != "" {
					label(c, "提交者邮箱："+commit.CommitterEmail, 11, p.muted).Wrap()
				} else {
					label(c, committerName, 11, p.muted).Wrap()
				}
				authorName, authorEmail := strings.TrimSpace(commit.AuthorName), strings.TrimSpace(commit.AuthorEmail)
				if authorName == "" {
					authorName = "未知"
				}
				if authorName != strings.TrimSpace(commit.CommitterName) || authorEmail != strings.TrimSpace(commit.CommitterEmail) {
					author := "作者：" + authorName
					if authorEmail != "" {
						author += " <" + authorEmail + ">"
					}
					label(c, author, 11, p.muted).Wrap()
				}
				if commit.FullHash != "" {
					if action(c, "workspace-legacy-commit-copy-"+commit.FullHash, "copy", "复制完整提交 SHA", p, false) {
						s.copyTextToClipboard(c.Services(), commit.FullHash, "提交 SHA 已复制")
					}
				}
			})
		}
	})
}

func (s *app) renderWorkspaceDirtyFilePreview(c *ui.Context, p palette, state *workspaceHistoryState) {
	if state == nil {
		return
	}
	ui.Column(c).Fill().MinWidth(0).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, strings.TrimSpace(state.SelectedPath), 12, p.text).Wrap().Grow(1)
			if state.SelectedPath != "" && action(c, "workspace-legacy-file-copy-path", "copy", "复制相对路径", p, false) {
				s.copyTextToClipboard(c.Services(), state.SelectedPath, "相对路径已复制")
			}
		})
		if state.Loading {
			label(c, "正在读取文件差异…", 12, p.muted)
			return
		}
		if state.Error != "" {
			label(c, state.Error, 12, p.danger).Wrap()
			return
		}
		if state.Preview != nil {
			s.renderWorkspaceFilePreview(c, p, state, state.Preview)
		}
	})
}

func (s *app) renderWorkspaceComparison(c *ui.Context, p palette, state *workspaceHistoryState, comparison *workspace.WorkspaceFileComparison, changedOnly bool) {
	if state == nil || comparison == nil {
		return
	}
	if native := s.workspaceNativeDiffView(state, comparison); native != nil {
		s.renderWorkspaceNativeComparison(c, p, state, native)
		return
	}
	renderWorkspaceDiffRows(c, p, state, comparison, changedOnly)
}

func workspaceComparisonRowChanged(row workspace.WorkspaceFileComparisonRow) bool {
	return row.OldLine == nil || row.OldLine.Kind != workspace.PreviewContext ||
		row.NewLine == nil || row.NewLine.Kind != workspace.PreviewContext
}

func workspaceHistoryChoice(c *ui.Context, key, text string, selected bool, p palette) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Row().Padding(7, 10).Gap(5).Radius(7).Label(text)
	if selected {
		button.Background(p.selected)
	} else {
		button.Background(p.surface)
	}
	button.Children(func() { label(c, text, 10, p.text).NoWrap() })
	return button
}

func workspaceChangeKindLabel(kind domain.WorkspaceGitFileChangeKind) string {
	switch kind {
	case domain.WorkspaceGitModified:
		return "修改"
	case domain.WorkspaceGitAdded:
		return "新增"
	case domain.WorkspaceGitDeleted:
		return "删除"
	case domain.WorkspaceGitRenamed:
		return "重命名"
	case domain.WorkspaceGitCopied:
		return "复制"
	case domain.WorkspaceGitTypeChanged:
		return "类型变更"
	case domain.WorkspaceGitConflicted:
		return "冲突"
	case domain.WorkspaceGitUntracked:
		return "未跟踪"
	default:
		return string(kind)
	}
}

func decodeWorkspaceSlice[T any](value any, target *[]T) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if string(content) == "null" {
		*target = []T{}
		return nil
	}
	return json.Unmarshal(content, target)
}

// The review page is rendered by taskWorkspace; this global hook owns only the
// commit-history modal so it cannot render the full review page twice.
func (s *app) renderWorkspaceHistoryDialog(c *ui.Context, p palette) {
	state := s.workspaceHistory
	if state == nil || state.Mode != "history" {
		return
	}
	s.renderWorkspaceCommitHistoryDialog(c, p, state)
}
