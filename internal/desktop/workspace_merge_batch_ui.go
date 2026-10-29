package desktop

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type workspaceMergeBatchItemState struct {
	Workspace      application.Workspace
	SourceRef      string
	DisabledReason string
	Selected       bool
}

type workspaceMergeBatchUIState struct {
	scope                           taskActionScope
	configJSON                      string
	TaskID, TaskPath, TaskCreatedAt string
	TaskGroupID, TaskFeatureBranch  string
	Token                           uint64
	Open                            bool
	Loading                         bool
	Error                           string
	Items                           []workspaceMergeBatchItemState
	Results                         *workspace.WorkspaceMainBranchMergeBatchResult
}

var workspaceMergeBatchToken atomic.Uint64

// taskHasMainBranchMergeBatch keeps the task-level menu available when there
// are workspaces to explain, including rows that are not currently actionable.
func (s *app) taskHasMainBranchMergeBatch(t task) bool {
	return t.id != "" && len(t.workspaces) > 0
}

func (s *app) openWorkspaceMergeBatch(t task) {
	if s.backend == nil || s.busy || t.archived || !s.workspaceMergeBatchTaskMatches(t) {
		return
	}
	state := &workspaceMergeBatchUIState{
		TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt, TaskGroupID: t.groupID, TaskFeatureBranch: t.branch, Open: true,
		Items: make([]workspaceMergeBatchItemState, 0, len(t.workspaces)),
	}
	state.scope, _ = s.taskActionScopeFor(t)
	state.configJSON = s.settingsUnit.state.configJSON
	for _, item := range t.workspaces {
		source, reason := s.workspaceMergeBatchEligibility(t, item)
		state.Items = append(state.Items, workspaceMergeBatchItemState{
			Workspace: workspaceMergeBatchSnapshot(item), SourceRef: source, DisabledReason: reason, Selected: reason == "",
		})
	}
	s.workspaceUnit.state.workspaceMergeBatch = state
}

// Confirmation owns its nested data; an in-place task update must not rewrite
// what was confirmed or make a stale async result appear current.
func workspaceMergeBatchSnapshot(item application.Workspace) application.Workspace {
	clone := func(value *string) *string {
		if value == nil {
			return nil
		}
		owned := *value
		return &owned
	}
	item.Warnings = slices.Clone(item.Warnings)
	item.Record.Warnings = slices.Clone(item.Record.Warnings)
	item.Record.OriginURL = clone(item.Record.OriginURL)
	item.Record.BaseRef = clone(item.Record.BaseRef)
	item.Record.TargetBranch = clone(item.Record.TargetBranch)
	item.Record.TagTargetRef = clone(item.Record.TagTargetRef)
	return item
}

func (s *app) workspaceMergeBatchTaskMatches(t task) bool {
	current := s.taskUnit.taskByID(t.id)
	return t.id != "" && current.id == t.id && !current.archived && current.groupID == t.groupID && current.branch == t.branch &&
		(s.page == "tasks" || s.page == "archive") && workspaceMergeTaskIdentityMatches(current, t)
}

func (s *app) workspaceMergeBatchContextCurrent(state *workspaceMergeBatchUIState) bool {
	if state == nil || !s.workspaceMergeBatchTaskMatches(workspaceMergeBatchTaskFromState(state)) {
		return false
	}
	scope, valid := s.taskActionScopeFor(workspaceMergeBatchTaskFromState(state))
	return valid && scope == state.scope && s.settingsUnit.state.configJSON == state.configJSON
}

func (s *app) workspaceMergeBatchEligibility(t task, item application.Workspace) (string, string) {
	if t.archived {
		return "", "归档任务不能合并"
	}
	if item.Record.Health != domain.WorkspaceReady && item.Record.Health != domain.WorkspaceReadyWithWarnings {
		return "", "工作区尚未就绪"
	}
	if item.Record.TargetBranch == nil || strings.TrimSpace(*item.Record.TargetBranch) == "" || *item.Record.TargetBranch != item.Record.Branch {
		return "", "没有与当前分支匹配的任务目标分支"
	}
	if strings.TrimSpace(item.Record.Branch) == "" {
		return "", "当前功能分支为空"
	}
	config, ok := s.workspaceMergeConfig()
	if !ok {
		return "", "无法读取主分支配置"
	}
	manifest := domain.TaskManifest{GroupID: t.groupID}
	main, found, err := workspace.ResolveWorkspaceMainBranch(config, manifest, item.Record)
	if err != nil {
		return "", "主分支配置无效：" + err.Error()
	}
	if !found {
		return "", "未配置主分支"
	}
	local, err := main.Local()
	if err != nil {
		return "", "主分支配置无效：" + err.Error()
	}
	if local.Branch == item.Record.Branch {
		return main.DisplayRef, "当前分支就是主分支"
	}
	if domain.NewGitWritePolicy(config.BlockedGitWriteBranches).IsBlocked(item.Record.Branch) {
		return main.DisplayRef, "当前功能分支受 Git 写入保护"
	}
	if state := s.workspaceUnit.state.workspaceMerge; state != nil && state.running {
		return main.DisplayRef, "另一个主分支合并正在执行"
	}
	if health, exists := s.workspaceHealth(item.Path); exists {
		if health.State == domain.WorkspaceGitChecking || health.Issue == domain.WorkspaceGitIssueOperationInProgress {
			return main.DisplayRef, "工作区 Git 操作正在执行"
		}
		if health.State != domain.WorkspaceGitReady || health.ActualBranch == nil || *health.ActualBranch != item.Record.Branch {
			return main.DisplayRef, "Git 状态未确认当前功能分支"
		}
	}
	return main.DisplayRef, ""
}

func (s *app) renderWorkspaceMergeBatchDialog(c *ui.Context, p palette) {
	state := s.workspaceUnit.state.workspaceMergeBatch
	if state == nil {
		return
	}
	if !s.workspaceMergeBatchContextCurrent(state) {
		s.workspaceUnit.state.workspaceMergeBatch = nil
		return
	}
	if state.Loading && !state.Open {
		state.Open = true
	}
	if !state.Open {
		s.workspaceUnit.state.workspaceMergeBatch = nil
		return
	}
	ui.DialogBase(c.Key("workspace-merge-batch-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		width, height := c.Size()
		panel.Width(maxFloat32(360, minFloat32(900, width-40))).MaxHeight(maxFloat32(320, minFloat32(760, height-40))).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() { s.renderWorkspaceMergeBatchDialogContent(c, p, state) })
	})
	if state.Loading && !state.Open {
		state.Open = true
	}
	if !state.Open && !state.Loading {
		s.workspaceUnit.state.workspaceMergeBatch = nil
	}
}

func (s *app) renderWorkspaceMergeBatchDialogContent(c *ui.Context, p palette, state *workspaceMergeBatchUIState) {
	label(c, "批量合并主分支", 17, p.text).FontWeight(600).Shrink(0)
	target := s.taskUnit.taskByID(state.TaskID)
	label(c, firstNonEmpty(target.title, state.TaskID), 13, p.muted).Shrink(0).FillWidth().SingleLine().Tooltip(target.title)
	if state.Results != nil {
		s.renderWorkspaceMergeBatchResults(c, p, state)
		return
	}
	selected, eligible := 0, 0
	for _, item := range state.Items {
		if item.DisabledReason == "" {
			eligible++
			if item.Selected {
				selected++
			}
		}
	}
	label(c, fmt.Sprintf("选择要合并的工作区 · %d / %d 个可操作", selected, eligible), 11, p.muted).Shrink(0)
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(180).Gap(7).Children(func() {
		for index := range state.Items {
			s.renderWorkspaceMergeBatchSelectionRow(c, p, state, index)
		}
	})
	if state.Error != "" {
		label(c, state.Error, 11, p.amber).FillWidth().Wrap().Shrink(0)
	}
	ui.Row(c).Shrink(0).Gap(8).Justify(ui.End).Children(func() {
		if state.Loading {
			ownsOperation := s.workspaceMergeBatchOwnsActiveOperation(state)
			cancelPending := ownsOperation && s.operationCancelling
			cancelEnabled := ownsOperation && s.canCancelOperation()
			cancelLabel := "取消合并"
			if cancelPending {
				cancelLabel = "正在取消…"
			} else if !ownsOperation {
				cancelLabel = "等待当前操作…"
			}
			button := ui.ButtonBase(c.Key("workspace-merge-batch-cancel")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label(cancelLabel).Tooltip(cancelLabel).
				Border(1, p.line).Background(p.surface).Disabled(!cancelEnabled)
			button.Children(func() {
				if cancelPending {
					ui.Spinner(c).Size(16, 16).TextColor(p.muted).Label(cancelLabel)
				} else {
					icon(c, "close", 16, p.text)
				}
				label(c, cancelLabel, 13, p.text).NoWrap()
			})
			if button.Clicked() && cancelEnabled {
				s.cancelAction()
			}
		} else if action(c, "workspace-merge-batch-cancel", "close", "取消", p, false) {
			state.Open = false
		}
		button := ui.ButtonBase(c.Key("workspace-merge-batch-submit")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label("开始合并").Tooltip("开始合并")
		button.Background(p.blue).Disabled(state.Loading || s.busy || selected == 0).Children(func() {
			text := "开始合并"
			if state.Loading {
				text = "正在合并…"
			}
			label(c, text, 13, p.surface).NoWrap()
		})
		if button.Clicked() && !state.Loading && !s.busy && selected > 0 {
			s.submitWorkspaceMergeBatch(state)
		}
	})
}

func (s *app) workspaceMergeBatchOwnsActiveOperation(state *workspaceMergeBatchUIState) bool {
	return state != nil && state == s.workspaceUnit.state.workspaceMergeBatch && state.Loading && s.busy &&
		s.operationKind == "workspace.merge-batch" && s.workspaceMergeBatchContextCurrent(state)
}

func (s *app) workspaceMergeBatchCanRequestCancellation(state *workspaceMergeBatchUIState) bool {
	return s.workspaceMergeBatchOwnsActiveOperation(state) && s.canCancelOperation()
}

func (s *app) renderWorkspaceMergeBatchSelectionRow(c *ui.Context, p palette, state *workspaceMergeBatchUIState, index int) {
	item := &state.Items[index]
	workspace := item.Workspace
	selected := item.Selected
	background := p.soft
	if selected {
		background = p.selected
	}
	ui.Column(c.Key("workspace-merge-batch-item-"+workspaceMergeBatchItemKey(workspace))).FillWidth().MinWidth(0).Padding(9, 11).Gap(5).Background(background).Border(1, p.line).Radius(9).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("workspace-merge-batch-check-"+workspaceMergeBatchItemKey(workspace)), &selected, "").Label("选择工作区 " + workspace.Name).
				Disabled(state.Loading || s.busy || item.DisabledReason != "").OnChange(func() { item.Selected = selected }).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				name := workspace.Name
				if module := strings.TrimSpace(workspace.Record.ModuleName); module != "" && module != name {
					name += " · " + module
				}
				label(c, name, 12, p.text).FontWeight(600).FillWidth().Wrap()
				label(c, item.SourceRef+" → "+workspace.Record.Branch, 11, p.muted).FillWidth().Wrap().Selectable()
				label(c, workspace.Path, 10, p.muted).SingleLine().Tooltip(workspace.Path)
			})
		})
		if item.DisabledReason != "" {
			label(c, item.DisabledReason, 11, p.amber).FillWidth().Wrap()
		}
	})
}

func (s *app) renderWorkspaceMergeBatchResults(c *ui.Context, p palette, state *workspaceMergeBatchUIState) {
	results := state.Results.Items
	succeeded, conflicts, failed := 0, 0, 0
	for _, item := range results {
		switch item.Outcome {
		case workspace.MainBranchConflict:
			conflicts++
		case workspace.MainBranchFailed:
			failed++
		default:
			succeeded++
		}
	}
	label(c, fmt.Sprintf("处理 %d 个工作区 · 成功 %d · 冲突 %d · 失败 %d", len(results), succeeded, conflicts, failed), 11, p.muted).Shrink(0)
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(180).Gap(7).Children(func() {
		for index, result := range results {
			s.renderWorkspaceMergeBatchResultRow(c, p, index, result)
		}
	})
	ui.Row(c).Shrink(0).Justify(ui.End).Children(func() {
		if action(c, "workspace-merge-batch-results-close", "close", "关闭", p, false) {
			state.Open = false
		}
	})
}

func (s *app) renderWorkspaceMergeBatchResultRow(c *ui.Context, p palette, index int, result workspace.WorkspaceMainBranchMergeResult) {
	color, status := workspaceMergeBatchOutcomeStyle(p, result.Outcome)
	ui.Column(c.Key(fmt.Sprintf("workspace-merge-batch-result-%d", index))).FillWidth().MinWidth(0).Padding(10, 11).Gap(5).Background(p.soft).Border(1, p.line).Radius(9).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				name := result.ServiceName
				if result.ModuleName != "" && result.ModuleName != result.ServiceName {
					name += " · " + result.ModuleName
				}
				label(c, name, 12, p.text).FontWeight(600).FillWidth().Wrap()
				label(c, result.SourceRef+" → "+result.TargetBranch, 11, p.muted).FillWidth().Wrap().Selectable()
				label(c, result.WorkspacePath, 10, p.muted).SingleLine().Tooltip(result.WorkspacePath)
			})
			label(c, status, 11, color).Shrink(0)
			if result.Outcome == workspace.MainBranchConflict && iconButton(c, fmt.Sprintf("workspace-merge-batch-copy-%d", index), "copy", "复制冲突信息", p).Clicked() {
				s.copyTextToClipboard(c.Services(), workspaceMergeBatchCopyText(result), "冲突信息已复制")
			}
		})
		label(c, result.Message, 11, color).FillWidth().Wrap().Selectable()
		if result.Outcome == workspace.MainBranchConflict && len(result.ConflictFiles) > 0 {
			label(c, "冲突文件："+strings.Join(result.ConflictFiles, "、"), 11, color).FillWidth().Wrap().Selectable()
		}
	})
}

func workspaceMergeBatchOutcomeStyle(p palette, outcome workspace.WorkspaceMainBranchMergeOutcome) (ui.Color, string) {
	switch outcome {
	case workspace.MainBranchUpToDate:
		return p.muted, "已是最新"
	case workspace.MainBranchFastForwarded:
		return p.blue, "快进完成"
	case workspace.MainBranchMerged:
		return p.green, "合并成功"
	case workspace.MainBranchConflict:
		return p.amber, "存在冲突"
	default:
		return p.amber, "合并失败"
	}
}

func workspaceMergeBatchCopyText(result workspace.WorkspaceMainBranchMergeResult) string {
	var text strings.Builder
	text.WriteString(result.Message)
	text.WriteString("\n合并方向：")
	text.WriteString(result.SourceRef)
	text.WriteString(" → ")
	text.WriteString(result.TargetBranch)
	text.WriteString("\n工作区：")
	text.WriteString(result.WorkspacePath)
	if len(result.ConflictFiles) > 0 {
		text.WriteString("\n冲突文件：\n")
		text.WriteString(strings.Join(result.ConflictFiles, "\n"))
	}
	return strings.TrimSpace(text.String())
}

func (s *app) submitWorkspaceMergeBatch(state *workspaceMergeBatchUIState) {
	if state == nil || state != s.workspaceUnit.state.workspaceMergeBatch || state.Loading || s.busy {
		return
	}
	if !s.workspaceMergeBatchStateCurrent(state) {
		s.retireStaleWorkspaceMergeBatch(state)
		return
	}
	t := s.taskUnit.taskByID(state.TaskID)
	keys := make([]string, 0, len(state.Items))
	for index := range state.Items {
		item := &state.Items[index]
		if !item.Selected {
			continue
		}
		source, reason := s.workspaceMergeBatchEligibility(t, item.Workspace)
		item.DisabledReason = reason
		if reason != "" {
			state.Error = "所选工作区状态已变化，请检查后重试"
			return
		}
		if source != item.SourceRef {
			state.Error = "主分支配置已变化，请关闭弹框后重新打开并确认合并方向"
			return
		}
		keys = append(keys, item.Workspace.Path)
	}
	if len(keys) == 0 {
		return
	}
	confirmed := make([]workspace.WorkspaceMainBranchMergeBatchConfirmedWorkspace, 0, len(keys))
	for index := range state.Items {
		item := &state.Items[index]
		if item.Selected {
			confirmed = append(confirmed, workspace.WorkspaceMainBranchMergeBatchConfirmedWorkspace{Record: item.Workspace.Record, SourceRef: item.SourceRef})
		}
	}
	token := workspaceMergeBatchToken.Add(1)
	if token == 0 {
		token = workspaceMergeBatchToken.Add(1)
	}
	state.Token, state.Loading, state.Error, state.Results = token, true, "", nil
	confirmation := workspace.WorkspaceMainBranchMergeBatchConfirmation{
		TaskDirectoryName: t.id,
		CreatedAt:         t.createdAt,
		GroupID:           t.groupID,
		FeatureBranch:     t.branch,
		Workspaces:        confirmed,
	}
	s.act("workspace.merge-batch", t.id, "", "", t.path, "", map[string]any{"selectionKeys": keys, "batchToken": token, "confirmation": confirmation})
}

func (s *app) applyWorkspaceMergeBatchResult(action application.Action, result application.ActionResult) {
	state := s.workspaceUnit.state.workspaceMergeBatch
	if !workspaceMergeBatchActionMatches(state, action) {
		return
	}
	if !s.workspaceMergeBatchStateCurrent(state) {
		s.retireStaleWorkspaceMergeBatch(state)
		return
	}
	var batch workspace.WorkspaceMainBranchMergeBatchResult
	if err := decodeWorkspaceValue(result.Data, &batch); err != nil {
		state.Loading = false
		state.Error = "批量合并结果格式错误：" + err.Error()
		return
	}
	if !workspaceMergeBatchResultsMatch(state, batch.Items) {
		state.Loading = false
		state.Error = "批量合并结果与所选工作区不一致，已丢弃结果"
		return
	}
	state.Loading = false
	state.Results = &batch
}

func (s *app) applyWorkspaceMergeBatchFailure(action application.Action, err error) {
	state := s.workspaceUnit.state.workspaceMergeBatch
	if !workspaceMergeBatchActionMatches(state, action) {
		return
	}
	if !s.workspaceMergeBatchStateCurrent(state) {
		s.retireStaleWorkspaceMergeBatch(state)
		return
	}
	state.Loading = false
	state.Error = err.Error()
}

func (s *app) retireStaleWorkspaceMergeBatch(state *workspaceMergeBatchUIState) {
	if state == nil || s.workspaceUnit.state.workspaceMergeBatch != state {
		return
	}
	state.Loading = false
	state.Results = nil
	if s.workspaceMergeBatchContextCurrent(state) {
		state.Open = true
		state.Error = "工作区信息已变化，本次合并结果已忽略。请关闭弹框后重新打开。"
		return
	}
	s.workspaceUnit.state.workspaceMergeBatch = nil
}

func (s *app) reconcileWorkspaceMergeBatchAfterSnapshot() {
	state := s.workspaceUnit.state.workspaceMergeBatch
	if state != nil && !state.Loading && state.Results != nil && !s.workspaceMergeBatchStateCurrent(state) {
		s.retireStaleWorkspaceMergeBatch(state)
	}
}

func (s *app) workspaceMergeBatchStateCurrent(state *workspaceMergeBatchUIState) bool {
	if !s.workspaceMergeBatchContextCurrent(state) {
		return false
	}
	current := s.taskUnit.taskByID(state.TaskID)
	for _, expected := range state.Items {
		if !expected.Selected {
			continue
		}
		found := false
		for _, item := range current.workspaces {
			if item.ServiceID == expected.Workspace.ServiceID && item.ModuleID == expected.Workspace.ModuleID &&
				workspaceHealthPathKey(item.Path) == workspaceHealthPathKey(expected.Workspace.Path) && reflect.DeepEqual(item.Record, expected.Workspace.Record) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func workspaceMergeBatchTaskFromState(state *workspaceMergeBatchUIState) task {
	if state == nil {
		return task{}
	}
	return task{
		id: state.TaskID, path: state.TaskPath, createdAt: state.TaskCreatedAt,
		groupID: state.TaskGroupID, branch: state.TaskFeatureBranch,
	}
}

func workspaceMergeBatchResultsMatch(state *workspaceMergeBatchUIState, results []workspace.WorkspaceMainBranchMergeResult) bool {
	if state == nil {
		return false
	}
	expected := make(map[string]application.Workspace, len(state.Items))
	for _, item := range state.Items {
		if item.Selected {
			expected[workspaceHealthPathKey(item.Workspace.Path)] = item.Workspace
		}
	}
	if len(expected) != len(results) {
		return false
	}
	seen := make(map[string]bool, len(results))
	for _, result := range results {
		key := workspaceHealthPathKey(result.WorkspacePath)
		item, ok := expected[key]
		if !ok || seen[key] || result.ServiceName != item.Record.ServiceName || result.ModuleName != item.Record.ModuleName || result.TargetBranch != item.Record.Branch {
			return false
		}
		seen[key] = true
	}
	return true
}

func workspaceMergeBatchActionMatches(state *workspaceMergeBatchUIState, action application.Action) bool {
	if state == nil || !state.Loading || action.Kind != "workspace.merge-batch" || action.TaskID != state.TaskID ||
		workspaceHealthPathKey(action.Path) != workspaceHealthPathKey(state.TaskPath) {
		return false
	}
	token, ok := action.Payload["batchToken"].(uint64)
	if !ok {
		if value, numeric := action.Payload["batchToken"].(float64); numeric {
			token, ok = uint64(value), value >= 0 && value == float64(uint64(value))
		}
	}
	return ok && token == state.Token
}

func workspaceMergeBatchItemKey(item application.Workspace) string {
	return item.ServiceID + "-" + item.ModuleID
}
