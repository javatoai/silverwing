package desktop

import (
	"reflect"
	"strings"
	"sync/atomic"
	"unicode"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type workspaceMergeUIState struct {
	taskID        string
	taskPath      string
	taskCreatedAt string
	workspace     domain.ServiceWorkspace
	token         uint64
	running       bool
	result        *workspace.WorkspaceMainBranchMergeResult
}

var workspaceMergeToken atomic.Uint64

func (s *app) workspaceMainBranchMergeActionLabel(t task, w application.Workspace) string {
	if source, ok := s.workspaceMainBranch(t, w); ok {
		return "合并远程主分支：" + source.DisplayRef + " → " + w.Record.Branch
	}
	return "合并远程主分支（未配置主分支）"
}

func (s *app) workspaceMainBranchMergeRunning(t task, w application.Workspace) bool {
	state := s.workspaceMergeStateFor(t, w)
	return state != nil && state.running
}

func (s *app) workspaceMainBranchMergeEnabled(t task, w application.Workspace) bool {
	if s.busy || t.archived || w.Record.Health != domain.WorkspaceReady && w.Record.Health != domain.WorkspaceReadyWithWarnings {
		return false
	}
	if w.Record.TargetBranch == nil || *w.Record.TargetBranch != w.Record.Branch {
		return false
	}
	config, ok := s.workspaceMergeConfig()
	if !ok {
		return false
	}
	source, ok := s.workspaceMainBranchWithConfig(config, t, w)
	if !ok || w.Record.Branch == source.LocalBranch {
		return false
	}
	if domain.NewGitWritePolicy(config.BlockedGitWriteBranches).IsBlocked(w.Record.Branch) {
		return false
	}
	if state := s.workspaceMerge; state != nil && state.running {
		return false
	}
	health, exists := s.workspaceHealth(w.Path)
	if !exists || health.State == domain.WorkspaceGitChecking || health.Issue == domain.WorkspaceGitIssueOperationInProgress {
		return true
	}
	return health.State == domain.WorkspaceGitReady && health.ActualBranch != nil && *health.ActualBranch == w.Record.Branch
}

// beginWorkspaceMerge owns the token and starts the existing backend action.
// A zero token means the current card no longer satisfies the source guards.
func (s *app) beginWorkspaceMerge(t task, w application.Workspace) uint64 {
	if s.backend == nil || !s.workspaceMainBranchMergeEnabled(t, w) || !s.workspaceMergeSelectionMatches(t, w) {
		return 0
	}
	token := workspaceMergeToken.Add(1)
	if token == 0 {
		token = workspaceMergeToken.Add(1)
	}
	s.workspaceMerge = &workspaceMergeUIState{
		taskID: t.id, taskPath: t.path, taskCreatedAt: t.createdAt,
		workspace: w.Record, token: token, running: true,
	}
	s.act("workspace.merge", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{"mergeToken": token})
	return token
}

func (s *app) applyWorkspaceMergeResult(action application.Action, result application.ActionResult) {
	state := s.workspaceMerge
	if !workspaceMergeActionMatches(state, action) {
		return
	}
	if !s.workspaceMergeStateIsCurrent(state) {
		s.workspaceMerge = nil
		return
	}
	var merged workspace.WorkspaceMainBranchMergeResult
	if err := decodeWorkspaceValue(result.Data, &merged); err != nil {
		s.workspaceMerge = nil
		s.lastError = "主分支合并结果格式错误：" + err.Error()
		return
	}
	if workspaceHealthPathKey(merged.WorkspacePath) != workspaceHealthPathKey(state.workspace.WorktreePath) ||
		merged.ServiceName != state.workspace.ServiceName || merged.ModuleName != state.workspace.ModuleName ||
		merged.TargetBranch != state.workspace.Branch {
		s.workspaceMerge = nil
		s.lastError = "主分支合并结果与发起操作的工作区身份不一致，已丢弃结果"
		return
	}
	if merged.ConflictFiles == nil {
		merged.ConflictFiles = []string{}
	}
	state.running = false
	state.result = &merged
}

// Backend errors and cancellation follow the source controller's onFailure / onCancelled
// paths: discard the card state instead of synthesizing a FAILED merge result.
func (s *app) applyWorkspaceMergeFailure(action application.Action, _ error) {
	state := s.workspaceMerge
	if workspaceMergeActionMatches(state, action) && s.workspaceMergeStateIsCurrent(state) {
		s.workspaceMerge = nil
	}
	s.requestWorkspaceGitStatus(true)
}

func (s *app) clearWorkspaceMergeSelection() {
	s.workspaceMerge = nil
}

func (s *app) dismissWorkspaceMergeResult(t task, w application.Workspace) {
	state := s.workspaceMergeStateFor(t, w)
	if state != nil && !state.running {
		s.workspaceMerge = nil
	}
}

func (s *app) renderWorkspaceMergePanel(c *ui.Context, p palette, t task, w application.Workspace) {
	state := s.workspaceMergeStateFor(t, w)
	if state == nil || state.running || state.result == nil {
		return
	}
	result := state.result
	conflict := result.Outcome == workspace.MainBranchConflict
	failure := result.Outcome == workspace.MainBranchFailed
	color := p.blue
	if conflict || failure {
		color = p.amber
	}
	direction := result.SourceRef + " → " + result.TargetBranch

	ui.Row(c).FillWidth().Padding(10, 12).Gap(8).AlignItems(ui.Start).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			label(c, result.Message, 12, color).FillWidth().Wrap().Selectable()
			label(c, "合并方向："+direction, 10, p.muted).FillWidth().Wrap().Selectable()
			if conflict {
				label(c, "冲突文件："+strings.Join(result.ConflictFiles, "、"), 11, color).FillWidth().Wrap().Selectable()
				label(c, "请在当前工作区解决冲突并完成合并，或自行中止后再试。", 11, p.text).FillWidth().Wrap()
			}
		})
		copyLabel := "复制合并结果"
		if conflict {
			copyLabel = "复制冲突信息"
		}
		if iconButton(c, "workspace-merge-copy-"+workspaceMergePanelKey(t, w), "copy", copyLabel, p).Clicked() {
			s.copyTextToClipboard(c.Services(), workspaceMergeCopyText(*result), "合并信息已复制")
		}
		if iconButton(c, "workspace-merge-close-"+workspaceMergePanelKey(t, w), "close", "关闭合并结果", p).Clicked() {
			s.dismissWorkspaceMergeResult(t, w)
		}
	})
}

func workspaceMergeCopyText(result workspace.WorkspaceMainBranchMergeResult) string {
	var text strings.Builder
	text.WriteString(result.Message)
	text.WriteString("\n合并方向：")
	text.WriteString(result.SourceRef)
	text.WriteString(" → ")
	text.WriteString(result.TargetBranch)
	text.WriteString("\n工作区：")
	text.WriteString(result.WorkspacePath)
	if result.Outcome == workspace.MainBranchConflict {
		text.WriteString("\n冲突文件：\n")
		for _, path := range result.ConflictFiles {
			text.WriteString(path)
			text.WriteByte('\n')
		}
		text.WriteString("请在当前任务工作区解决冲突并完成合并，或自行中止本次合并。")
	}
	return strings.TrimRightFunc(text.String(), unicode.IsSpace)
}

type workspaceMainBranchSource struct {
	DisplayRef  string
	LocalBranch string
}

func (s *app) workspaceMainBranch(t task, w application.Workspace) (workspaceMainBranchSource, bool) {
	config, ok := s.workspaceMergeConfig()
	if !ok {
		return workspaceMainBranchSource{}, false
	}
	return s.workspaceMainBranchWithConfig(config, t, w)
}

func (s *app) workspaceMainBranchWithConfig(config domain.AppConfig, t task, w application.Workspace) (workspaceMainBranchSource, bool) {
	manifest := domain.TaskManifest{GroupID: t.groupID}
	resolved, ok, err := workspace.ResolveWorkspaceMainBranch(config, manifest, w.Record)
	if err != nil || !ok {
		return workspaceMainBranchSource{}, false
	}
	ref, err := resolved.Local()
	if err != nil {
		return workspaceMainBranchSource{}, false
	}
	return workspaceMainBranchSource{DisplayRef: resolved.DisplayRef, LocalBranch: ref.Branch}, true
}

func (s *app) workspaceMergeConfig() (domain.AppConfig, bool) {
	config, err := s.readCurrentAppConfig()
	if err != nil {
		return domain.AppConfig{}, false
	}
	return config, true
}

func (s *app) workspaceMergeSelectionMatches(t task, w application.Workspace) bool {
	selected := s.currentTask()
	if selected.path == "" || !workspaceMergeTaskIdentityMatches(selected, t) {
		return false
	}
	for _, current := range selected.workspaces {
		if reflect.DeepEqual(current.Record, w.Record) && current.ServiceID == w.ServiceID && current.ModuleID == w.ModuleID && workspaceHealthPathKey(current.Path) == workspaceHealthPathKey(w.Path) {
			return true
		}
	}
	return false
}

func (s *app) workspaceMergeStateFor(t task, w application.Workspace) *workspaceMergeUIState {
	state := s.workspaceMerge
	if state == nil || state.taskPath != t.path || state.taskCreatedAt != t.createdAt || !reflect.DeepEqual(state.workspace, w.Record) {
		return nil
	}
	return state
}

func (s *app) workspaceMergeStateIsCurrent(state *workspaceMergeUIState) bool {
	if state == nil {
		return false
	}
	t := s.currentTask()
	if t.path != state.taskPath || t.createdAt != state.taskCreatedAt || t.id != state.taskID {
		return false
	}
	for _, item := range t.workspaces {
		if reflect.DeepEqual(item.Record, state.workspace) {
			return true
		}
	}
	return false
}

func workspaceMergeActionMatches(state *workspaceMergeUIState, action application.Action) bool {
	if state == nil || action.Kind != "workspace.merge" || action.TaskID != state.taskID ||
		action.ServiceID != state.workspace.GroupServiceID || action.ModuleID != state.workspace.ModuleID ||
		workspaceHealthPathKey(action.Path) != workspaceHealthPathKey(state.workspace.WorktreePath) {
		return false
	}
	token, ok := action.Payload["mergeToken"].(uint64)
	if !ok {
		if value, numeric := action.Payload["mergeToken"].(float64); numeric {
			token, ok = uint64(value), value >= 0 && value == float64(uint64(value))
		}
	}
	return ok && token == state.token
}

func workspaceMergeTaskIdentityMatches(left, right task) bool {
	return left.path != "" && workspaceHealthPathKey(left.path) == workspaceHealthPathKey(right.path) && left.createdAt == right.createdAt
}

func workspaceMergePanelKey(t task, w application.Workspace) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(t.id + "-" + w.ServiceID + "-" + w.ModuleID)
}
