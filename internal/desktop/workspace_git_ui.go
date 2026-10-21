package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

const (
	workspaceGitModeCommit        = "COMMIT"
	workspaceGitModePush          = "PUSH"
	workspaceGitModeCommitAndPush = "COMMIT_AND_PUSH"
)

type workspaceGitFlowState struct {
	TaskID, TaskKey   string
	Scope             string // batch or single
	Mode              string
	Open              bool
	ResultOpen        bool
	Loading           bool
	PreviewLoaded     bool
	PreviewGeneration uint64
	PreviewCancel     context.CancelFunc
	Error             string
	Single            application.Workspace
	Selected          map[string]bool
	Messages          map[string]string
	Previews          map[string]workspace.WorkspaceGitChangePreview
	Result            *workspace.WorkspaceGitBatchResult
}

var (
	workspaceRequirementDetailNumber = regexp.MustCompile(`(?i)/detail/([0-9]{4,})`)
	workspaceRequirementNumber       = regexp.MustCompile(`[0-9]{4,}`)
	workspaceCommitHorizontalSpace   = regexp.MustCompile(`[ \t]+`)
	workspaceCommitNewlineSpace      = regexp.MustCompile(` *\n *`)
)

func (s *app) openWorkspaceGitBatch(task task, mode string, initialSelection []string) {
	if s.busy || task.id == "" || len(taskPhysicalGitWorkspaces(task)) == 0 {
		return
	}
	s.cancelWorkspaceGitFlow()
	flow := &workspaceGitFlowState{
		TaskID: task.id, TaskKey: workspaceHealthTaskKey(task), Scope: "batch", Mode: mode,
		Open: true, Selected: make(map[string]bool), Messages: make(map[string]string),
		Previews: make(map[string]workspace.WorkspaceGitChangePreview),
	}
	initial := make(map[string]bool, len(initialSelection))
	for _, path := range initialSelection {
		initial[workspaceHealthPathKey(path)] = true
	}
	for _, item := range taskPhysicalGitWorkspaces(task) {
		key := workspaceGitUIKey(item.Path)
		flow.Messages[key] = s.defaultWorkspaceCommitMessage(task, item)
		if initial[key] {
			flow.Selected[key] = true
		}
	}
	s.ensureWorkspaceGitState().flow = flow
	s.requestWorkspaceGitPreview()
}

func (s *app) openWorkspaceGitSingle(task task, item application.Workspace, mode string) {
	if s.busy || task.id == "" || strings.TrimSpace(item.Path) == "" {
		return
	}
	if mode != workspaceGitModeCommit && mode != workspaceGitModeCommitAndPush {
		return
	}
	s.cancelWorkspaceGitFlow()
	key := workspaceGitUIKey(item.Path)
	flow := &workspaceGitFlowState{
		TaskID: task.id, TaskKey: workspaceHealthTaskKey(task), Scope: "single", Mode: mode,
		Open: true, Single: item, Selected: map[string]bool{key: true},
		Messages: map[string]string{key: s.defaultWorkspaceCommitMessage(task, item)},
		Previews: make(map[string]workspace.WorkspaceGitChangePreview),
	}
	s.ensureWorkspaceGitState().flow = flow
	s.requestWorkspaceGitPreview()
}

func (s *app) cancelWorkspaceGitFlow() {
	if state := s.workspaceGit; state != nil && state.flow != nil {
		if state.flow.PreviewCancel != nil {
			state.flow.PreviewCancel()
			state.flow.PreviewCancel = nil
		}
		state.flow.PreviewGeneration++
		state.flow = nil
	}
}

func (s *app) requestWorkspaceGitPreview() {
	state := s.ensureWorkspaceGitState()
	flow := state.flow
	if flow == nil || s.backend == nil || s.win == nil {
		if flow != nil {
			flow.Error = "应用后端不可用，无法读取 Git 预览。"
		}
		return
	}
	if flow.PreviewCancel != nil {
		flow.PreviewCancel()
	}
	flow.PreviewGeneration++
	generation := flow.PreviewGeneration
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	flow.PreviewCancel = cancel
	flow.Loading = true
	flow.PreviewLoaded = false
	flow.Error = ""
	flow.Previews = make(map[string]workspace.WorkspaceGitChangePreview)
	action := application.Action{TaskID: flow.TaskID}
	if flow.Scope == "single" {
		action.Kind = "workspace.preview"
		action.ServiceID = flow.Single.ServiceID
		action.ModuleID = flow.Single.ModuleID
		action.Path = flow.Single.Path
		action.Payload = map[string]any{"selectionKeys": []string{flow.Single.Path}}
	} else {
		action.Kind = "workspace.previews"
	}
	win := s.win
	taskKey := flow.TaskKey
	go func() {
		defer cancel()
		err := s.acquireBackend(ctx)
		var result application.ActionResult
		if err == nil {
			result, err = s.backend.Dispatch(ctx, action)
			s.releaseBackend()
		}
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			current := s.workspaceGit
			if current == nil || current.flow != flow || flow.PreviewGeneration != generation ||
				flow.TaskID != s.currentTask().id || flow.TaskKey != taskKey || workspaceHealthTaskKey(s.currentTask()) != taskKey {
				return
			}
			flow.PreviewCancel = nil
			flow.Loading = false
			if err != nil {
				flow.Error = err.Error()
				s.win.Invalidate()
				return
			}
			if flow.Scope == "single" {
				var preview workspace.WorkspaceGitChangePreview
				if decodeWorkspaceValue(result.Data, &preview) != nil || workspaceGitUIKey(preview.WorkspacePath) != workspaceGitUIKey(flow.Single.Path) {
					flow.Error = "提交预览与当前工作区不匹配，请重新读取。"
				} else {
					flow.Previews[workspaceGitUIKey(preview.WorkspacePath)] = preview
					flow.PreviewLoaded = true
				}
			} else {
				var previews map[string]workspace.WorkspaceGitChangePreview
				if err := decodeWorkspaceValue(result.Data, &previews); err != nil {
					flow.Error = "Git 预览格式错误：" + err.Error()
				} else {
					for _, preview := range previews {
						flow.Previews[workspaceGitUIKey(preview.WorkspacePath)] = preview
					}
					flow.PreviewLoaded = true
				}
			}
			s.win.Invalidate()
		})
	}()
}

func (s *app) submitWorkspaceGitFlow() {
	flow := s.currentWorkspaceGitFlow()
	if flow == nil || s.busy || flow.Loading || !flow.PreviewLoaded {
		return
	}
	if flow.Scope == "single" {
		key := workspaceGitUIKey(flow.Single.Path)
		preview, ok := flow.Previews[key]
		if !ok || strings.TrimSpace(flow.Messages[key]) == "" {
			flow.Error = "请先读取预览并填写提交信息。"
			return
		}
		kind := "workspace.commit"
		if flow.Mode == workspaceGitModeCommitAndPush {
			kind = "workspace.commit-push"
		}
		flow.Loading = true
		flow.Error = ""
		s.act(kind, flow.TaskID, flow.Single.ServiceID, flow.Single.ModuleID, flow.Single.Path, "", map[string]any{
			"selectionKeys":       []string{flow.Single.Path},
			"message":             flow.Messages[key],
			"fingerprint":         preview.Fingerprint,
			"expectedFingerprint": preview.Fingerprint,
		})
		return
	}
	selectionKeys := make([]string, 0, len(flow.Selected))
	messages := make(map[string]string)
	fingerprints := make(map[string]string)
	for _, item := range taskPhysicalGitWorkspaces(s.currentTask()) {
		key := workspaceGitUIKey(item.Path)
		if !flow.Selected[key] {
			continue
		}
		preview, ok := flow.Previews[key]
		if !ok {
			flow.Error = "所选工作区的预览尚未读取完成，请重新读取后重试。"
			return
		}
		if flow.Mode != workspaceGitModePush && len(preview.Files) > 0 && strings.TrimSpace(flow.Messages[key]) == "" {
			flow.Error = "存在本地变更的工作区必须填写提交信息。"
			return
		}
		selectionKeys = append(selectionKeys, item.Path)
		payloadKey := workspaceGitPayloadKey(item.Path)
		messages[payloadKey] = flow.Messages[key]
		fingerprints[payloadKey] = preview.Fingerprint
	}
	if len(selectionKeys) == 0 {
		flow.Error = "请至少选择一个工作区。"
		return
	}
	flow.Loading = true
	flow.Error = ""
	s.act("workspace.batch", flow.TaskID, "", "", "", "", map[string]any{
		"mode": flow.Mode, "selectionKeys": selectionKeys, "messages": messages, "fingerprints": fingerprints,
	})
}

func (s *app) applyWorkspaceGitFlowResult(action application.Action, result application.ActionResult) {
	flow := s.currentWorkspaceGitFlow()
	if action.Kind == "workspace.push" && flow == nil {
		message := result.Message
		if message == "" {
			var operation workspace.WorkspaceGitOperationResult
			if decodeWorkspaceValue(result.Data, &operation) == nil {
				message = operation.Message
			}
		}
		s.lastMessage = firstNonEmpty(message, "工作区推送已完成")
		return
	}
	if !workspaceGitFlowMatches(flow, action) {
		return
	}
	flow.Loading = false
	flow.Error = ""
	switch action.Kind {
	case "workspace.batch":
		var batch workspace.WorkspaceGitBatchResult
		if err := decodeWorkspaceValue(result.Data, &batch); err != nil {
			flow.Error = "批量 Git 结果格式错误：" + err.Error()
			flow.Open = true
			return
		}
		if batch.Items == nil {
			batch.Items = []workspace.WorkspaceGitBatchItemResult{}
		}
		flow.Result = &batch
		flow.Open = false
		flow.ResultOpen = true
	case "workspace.commit", "workspace.commit-push":
		var operation workspace.WorkspaceGitOperationResult
		if err := decodeWorkspaceValue(result.Data, &operation); err != nil {
			flow.Error = "Git 操作结果格式错误：" + err.Error()
			flow.Open = true
			return
		}
		s.lastMessage = firstNonEmpty(operation.Message, result.Message, "Git 操作已完成")
		s.cancelWorkspaceGitFlow()
	case "workspace.push":
		message := result.Message
		var operation workspace.WorkspaceGitOperationResult
		if decodeWorkspaceValue(result.Data, &operation) == nil {
			message = firstNonEmpty(operation.Message, message)
		}
		s.lastMessage = firstNonEmpty(message, "工作区推送已完成")
	}
}

func (s *app) applyWorkspaceGitFlowFailure(action application.Action, err error) {
	if err == nil {
		return
	}
	flow := s.currentWorkspaceGitFlow()
	if !workspaceGitFlowMatches(flow, action) {
		if action.Kind == "workspace.push" {
			s.lastError = err.Error()
		}
		return
	}
	flow.Loading = false
	flow.Error = err.Error()
	flow.Open = true
	flow.ResultOpen = false
}

func workspaceGitFlowMatches(flow *workspaceGitFlowState, action application.Action) bool {
	if flow == nil || action.TaskID != flow.TaskID {
		return false
	}
	if flow.Scope == "batch" {
		return action.Kind == "workspace.batch"
	}
	if flow.Scope != "single" || action.ServiceID != flow.Single.ServiceID || action.ModuleID != flow.Single.ModuleID ||
		workspaceGitUIKey(action.Path) != workspaceGitUIKey(flow.Single.Path) {
		return false
	}
	return action.Kind == "workspace.commit" && flow.Mode == workspaceGitModeCommit ||
		action.Kind == "workspace.commit-push" && flow.Mode == workspaceGitModeCommitAndPush
}

func (s *app) currentWorkspaceGitFlow() *workspaceGitFlowState {
	if s.workspaceGit == nil {
		return nil
	}
	return s.workspaceGit.flow
}

func (s *app) renderWorkspaceGitDialogs(c *ui.Context, p palette) {
	flow := s.currentWorkspaceGitFlow()
	if flow == nil {
		return
	}
	if flow.Open {
		ui.DialogBase(c.Key("workspace-git-flow-dialog"), &flow.Open, func(backdrop, panel ui.Element) {
			backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
			w, h := c.Size()
			panel.Width(max(360, min(820, w-48))).MaxHeight(max(320, min(700, h-48))).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(16)
			panel.Children(func() {
				if flow.Scope == "single" {
					s.renderSingleWorkspaceGitDialog(c, p, flow)
				} else {
					s.renderBatchWorkspaceGitDialog(c, p, flow)
				}
			})
		})
		if !flow.Open && !flow.ResultOpen {
			s.cancelWorkspaceGitFlow()
		}
	}
	if flow.ResultOpen {
		ui.DialogBase(c.Key("workspace-git-result-dialog"), &flow.ResultOpen, func(backdrop, panel ui.Element) {
			backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
			w, h := c.Size()
			panel.Width(max(360, min(820, w-48))).MaxHeight(max(320, min(680, h-48))).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(16)
			panel.Children(func() { s.renderBatchWorkspaceGitResult(c, p, flow) })
		})
		if !flow.ResultOpen {
			s.cancelWorkspaceGitFlow()
		}
	}
}

func (s *app) renderSingleWorkspaceGitDialog(c *ui.Context, p palette, flow *workspaceGitFlowState) {
	title := "提交"
	if flow.Mode == workspaceGitModeCommitAndPush {
		title = "提交并推送"
	}
	label(c, title, 17, p.text).FontWeight(600)
	label(c, flow.Single.Name+" · "+flow.Single.Branch, 12, p.muted).SingleLine().Tooltip(flow.Single.Path)
	key := workspaceGitUIKey(flow.Single.Path)
	message := flow.Messages[key]
	ui.TextArea(c.Key("workspace-git-single-message"), &message).Label("提交信息 "+flow.Single.Name).
		OnChange(func() { flow.Messages[key] = message }).FillWidth().Height(86).Padding(10).Background(p.surface).Border(1, p.line).Radius(8)
	s.renderWorkspaceGitPreviewState(c, p, flow, key)
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if action(c, "workspace-git-preview-retry", "refresh", "重新读取预览", p, false) && !flow.Loading && !s.busy {
			s.requestWorkspaceGitPreview()
		}
		if action(c, "workspace-git-single-cancel", "close", "取消", p, false) {
			flow.Open = false
			s.cancelWorkspaceGitFlow()
		}
		canSubmit := flow.PreviewLoaded && !flow.Loading && !s.busy && flow.Error == "" && strings.TrimSpace(message) != "" && hasWorkspaceGitPreview(flow, key)
		button := ui.ButtonBase(c.Key("workspace-git-single-submit")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label("确认").Tooltip("确认")
		button.Background(p.blue).Disabled(!canSubmit).Children(func() {
			label(c, title, 13, p.surface).NoWrap()
		})
		if button.Clicked() && canSubmit {
			s.submitWorkspaceGitFlow()
		}
	})
}

func (s *app) renderBatchWorkspaceGitDialog(c *ui.Context, p palette, flow *workspaceGitFlowState) {
	title := workspaceGitModeTitle(flow.Mode)
	label(c, title, 17, p.text).FontWeight(600)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, "确认后仅预检并操作所选物理工作区。", 11, p.muted).Grow(1).Wrap()
		if action(c, "workspace-git-select-all", "check", "全选", p, false) {
			for _, item := range taskPhysicalGitWorkspaces(s.currentTask()) {
				flow.Selected[workspaceGitUIKey(item.Path)] = true
			}
		}
		if action(c, "workspace-git-select-none", "close", "全不选", p, false) {
			flow.Selected = make(map[string]bool)
		}
	})
	workspaces := taskPhysicalGitWorkspaces(s.currentTask())
	ui.Scroll(c).Grow(1).MinHeight(150).Padding(2).Gap(8).Children(func() {
		for index, item := range workspaces {
			key := workspaceGitUIKey(item.Path)
			selected := flow.Selected[key]
			preview, hasPreview := flow.Previews[key]
			ui.Column(c).FillWidth().Padding(9, 10).Gap(6).Background(p.surface).Border(1, p.line).Radius(10).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key(fmt.Sprintf("workspace-git-select-%d", index)), &selected, "").Label("选择工作区 " + item.Name).
						Disabled(s.busy).OnChange(func() { flow.Selected[key] = selected }).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						name, module := s.workspaceNames(s.taskByID(flow.TaskID).groupID, item)
						if module != "" && module != name {
							name += " · " + module
						}
						label(c, name, 12, p.text).FontWeight(600).SingleLine()
						branch := item.Branch
						if health, ok := s.workspaceHealth(item.Path); ok && health.ActualBranch != nil && strings.TrimSpace(*health.ActualBranch) != "" {
							branch = *health.ActualBranch
						}
						identity := branch
						if strings.TrimSpace(item.PushRemote) != "" {
							identity += " · " + item.PushRemote
						}
						identity += " · " + item.Path
						label(c, identity, 10, p.muted).SingleLine().Tooltip(item.Path)
					})
					if selected {
						s.renderWorkspaceGitDisposition(c, p, flow, key, hasPreview, preview)
					}
				})
				if selected && hasPreview && flow.Mode != workspaceGitModePush && len(preview.Files) > 0 {
					message := flow.Messages[key]
					ui.TextArea(c.Key("workspace-git-message-"+fmt.Sprint(index)), &message).Label("提交信息 "+item.Name).
						OnChange(func() { flow.Messages[key] = message }).FillWidth().Height(66).Padding(8).Placeholder("提交信息").Background(p.surface).Border(1, p.line).Radius(7)
				}
				if selected && hasPreview {
					s.renderWorkspaceGitPreviewFiles(c, p, preview)
				}
			})
		}
	})
	s.renderWorkspaceGitPreviewState(c, p, flow, "")
	if flow.Mode == workspaceGitModeCommitAndPush && s.workspaceGitBatchOnlyPushes(flow) {
		ui.Box(c).FillWidth().Padding(8, 10).Radius(8).Background(p.selected).Children(func() {
			label(c, "所选工作区都没有未提交文件；本次不会创建新提交，只会推送已有的未推送提交。", 11, p.blue).Wrap()
		})
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if action(c, "workspace-git-batch-preview-retry", "refresh", "重新读取预览", p, false) && !flow.Loading && !s.busy {
			s.requestWorkspaceGitPreview()
		}
		if action(c, "workspace-git-batch-cancel", "close", "取消", p, false) {
			flow.Open = false
			s.cancelWorkspaceGitFlow()
		}
		canSubmit := s.workspaceGitBatchCanSubmit(flow, workspaces) && !flow.Loading && !s.busy && flow.PreviewLoaded
		button := ui.ButtonBase(c.Key("workspace-git-batch-submit")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label("确认执行").Tooltip("确认执行").Disabled(!canSubmit)
		button.Background(p.blue).Children(func() { label(c, "确认执行", 13, p.surface).NoWrap() })
		if button.Clicked() && canSubmit {
			s.submitWorkspaceGitFlow()
		}
	})
}

func (s *app) renderWorkspaceGitPreviewState(c *ui.Context, p palette, flow *workspaceGitFlowState, key string) {
	if flow.Loading {
		message := "正在读取 Git 变更预览…"
		if flow.Scope == "single" {
			message = "正在读取变更预览…"
		}
		label(c, message, 11, p.muted)
	}
	if flow.Error != "" {
		ui.Box(c).FillWidth().Padding(8, 10).Radius(8).Background(p.amberBg).Children(func() {
			label(c, flow.Error, 11, p.amber).Wrap()
		})
	}
	if key != "" && flow.PreviewLoaded {
		if _, ok := flow.Previews[key]; !ok && flow.Error == "" {
			label(c, "预览中没有返回当前工作区。", 11, p.amber).Wrap()
		}
	}
}

func (s *app) renderWorkspaceGitDisposition(c *ui.Context, p palette, flow *workspaceGitFlowState, key string, hasPreview bool, preview workspace.WorkspaceGitChangePreview) {
	if !hasPreview {
		label(c, "正在确认变更", 10, p.muted).SingleLine()
		return
	}
	if flow.Mode == workspaceGitModePush {
		return
	}
	if len(preview.Files) > 0 {
		return
	}
	text := "无本地变更，跳过提交"
	if flow.Mode == workspaceGitModeCommitAndPush {
		text = "无本地变更，仅推送已有提交"
	}
	label(c, text, 10, p.muted).SingleLine()
}

func (s *app) renderWorkspaceGitPreviewFiles(c *ui.Context, p palette, preview workspace.WorkspaceGitChangePreview) {
	label(c, fmt.Sprintf("变更文件 %d 个", len(preview.Files)), 10, p.muted)
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		limit := min(20, len(preview.Files))
		for _, file := range preview.Files[:limit] {
			label(c, file, 10, p.muted).SingleLine().Tooltip(file).Selectable()
		}
		if len(preview.Files) > limit {
			label(c, fmt.Sprintf("…还有 %d 个", len(preview.Files)-limit), 10, p.muted)
		}
	})
	if strings.TrimSpace(preview.DiffStat) != "" {
		ui.TextArea(c.Key("workspace-git-diffstat-"+workspaceGitUIKey(preview.WorkspacePath)), &preview.DiffStat).FillWidth().Height(48).Padding(6).Font("Consolas, monospace").FontSize(10).ReadOnly(true).Background(p.soft).Border(1, p.line).Radius(7)
	}
}

func (s *app) renderBatchWorkspaceGitResult(c *ui.Context, p palette, flow *workspaceGitFlowState) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		icon(c, "git", 18, p.blue)
		label(c, "批量 Git 操作结果", 17, p.text).FontWeight(600).Grow(1)
		if iconButton(c, "workspace-git-result-close", "close", "关闭", p).Clicked() {
			flow.ResultOpen = false
			s.cancelWorkspaceGitFlow()
		}
	})
	if flow.Result == nil || len(flow.Result.Items) == 0 {
		ui.Column(c).Grow(1).FillWidth().Center().Background(p.soft).Radius(9).Children(func() {
			label(c, "没有返回工作区结果。", 12, p.muted)
		})
	} else {
		ui.Scroll(c).Grow(1).MinHeight(160).Padding(8).Gap(8).Background(p.soft).Radius(9).Children(func() {
			for index, item := range flow.Result.Items {
				failed := item.CommitState == workspace.WorkspaceGitFailed || item.PushState == workspace.WorkspaceGitFailed
				skipped := item.CommitState == workspace.WorkspaceGitSkipped && item.PushState == workspace.WorkspaceGitNotRun
				stateText, color := "成功", p.green
				if failed {
					stateText, color = "失败", p.amber
				} else if skipped {
					stateText, color = "已跳过", p.muted
				}
				ui.Column(c).FillWidth().Padding(9, 10).Gap(4).Background(p.surface).Border(1, p.line).Radius(9).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						label(c, firstNonEmpty(item.ServiceName, filepath.Base(item.WorkspacePath)), 12, p.text).FontWeight(600).Grow(1).SingleLine()
						label(c, stateText, 10, color).NoWrap()
					})
					label(c, item.Branch, 10, p.muted).SingleLine()
					if item.Message != "" {
						label(c, item.Message, 11, color).Wrap().Selectable()
					}
					if failed && action(c, fmt.Sprintf("workspace-git-open-failed-%d", index), "folder", "打开失败工作区", p, false) {
						s.act("system.reveal", flow.TaskID, "", "", item.WorkspacePath, "", nil)
					}
				})
			}
		})
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if action(c, "workspace-git-copy-results", "copy", "复制全部结果", p, false) {
			s.copyTextToClipboard(c.Services(), workspaceGitBatchResultText(flow.Result), "Git 操作结果已复制")
		}
		if len(workspaceGitFailedPaths(flow.Result)) > 0 {
			if action(c, "workspace-git-retry-failed", "refresh", "仅重试失败项", p, false) {
				failed := workspaceGitFailedPaths(flow.Result)
				mode := flow.Mode
				messages := make(map[string]string, len(flow.Messages))
				for key, value := range flow.Messages {
					messages[key] = value
				}
				task := s.currentTask()
				s.openWorkspaceGitBatch(task, mode, failed)
				if retry := s.currentWorkspaceGitFlow(); retry != nil {
					for key, value := range messages {
						if _, exists := retry.Messages[key]; exists {
							retry.Messages[key] = value
						}
					}
				}
			}
		}
		if action(c, "workspace-git-result-dismiss", "close", "关闭", p, true) {
			flow.ResultOpen = false
			s.cancelWorkspaceGitFlow()
		}
	})
}

func (s *app) workspaceGitBatchCanSubmit(flow *workspaceGitFlowState, workspaces []application.Workspace) bool {
	selected := 0
	for _, item := range workspaces {
		key := workspaceGitUIKey(item.Path)
		if !flow.Selected[key] {
			continue
		}
		selected++
		preview, ok := flow.Previews[key]
		if !ok {
			return false
		}
		if flow.Mode != workspaceGitModePush && len(preview.Files) > 0 && strings.TrimSpace(flow.Messages[key]) == "" {
			return false
		}
	}
	return selected > 0
}

func (s *app) workspaceGitBatchOnlyPushes(flow *workspaceGitFlowState) bool {
	selected := 0
	for key, isSelected := range flow.Selected {
		if !isSelected {
			continue
		}
		selected++
		preview, ok := flow.Previews[key]
		if !ok || len(preview.Files) > 0 {
			return false
		}
	}
	return selected > 0
}

func hasWorkspaceGitPreview(flow *workspaceGitFlowState, key string) bool {
	_, ok := flow.Previews[key]
	return ok
}

func taskPhysicalGitWorkspaces(task task) []application.Workspace {
	seen := make(map[string]bool, len(task.workspaces))
	workspaces := make([]application.Workspace, 0, len(task.workspaces))
	for _, item := range task.workspaces {
		if strings.TrimSpace(item.Path) == "" {
			continue
		}
		key := workspaceGitUIKey(item.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		workspaces = append(workspaces, item)
	}
	return workspaces
}

func workspaceGitPayloadKey(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func workspaceGitUIKey(path string) string {
	return workspaceHealthPathKey(path)
}

func workspaceGitModeTitle(mode string) string {
	switch mode {
	case workspaceGitModeCommit:
		return "选择要提交的工作区"
	case workspaceGitModePush:
		return "选择要推送的工作区"
	case workspaceGitModeCommitAndPush:
		return "选择要提交并推送的工作区"
	default:
		return "Git 操作"
	}
}

func workspaceGitFailedPaths(result *workspace.WorkspaceGitBatchResult) []string {
	if result == nil {
		return nil
	}
	paths := make([]string, 0)
	for _, item := range result.Items {
		if item.CommitState == workspace.WorkspaceGitFailed || item.PushState == workspace.WorkspaceGitFailed {
			paths = append(paths, item.WorkspacePath)
		}
	}
	return paths
}

func workspaceGitBatchResultText(result *workspace.WorkspaceGitBatchResult) string {
	if result == nil {
		return ""
	}
	lines := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		lines = append(lines, fmt.Sprintf("%s [%s] %s", item.ServiceName, item.Branch, item.Message))
	}
	return strings.Join(lines, "\n")
}

func workspaceGitWritesBlocked(s *app, item application.Workspace) bool {
	branch := strings.TrimSpace(item.Branch)
	if health, ok := s.workspaceHealth(item.Path); ok && health.ActualBranch != nil && strings.TrimSpace(*health.ActualBranch) != "" {
		branch = strings.TrimSpace(*health.ActualBranch)
	}
	if branch == "" {
		return false
	}
	for _, value := range s.readConfigArray("blockedGitWriteBranches") {
		if configured, ok := value.(string); ok && strings.EqualFold(strings.TrimSpace(configured), branch) {
			return true
		}
	}
	return false
}

func (s *app) defaultWorkspaceCommitMessage(task task, item application.Workspace) string {
	var config struct {
		Groups []struct {
			ID       string
			Services []struct {
				ID                    string
				CommitMessageTemplate string
			}
		}
	}
	if json.Unmarshal([]byte(s.configJSON), &config) != nil {
		return ""
	}
	template := ""
	for _, group := range config.Groups {
		if group.ID != task.groupID {
			continue
		}
		for _, service := range group.Services {
			if service.ID == item.ServiceID {
				template = service.CommitMessageTemplate
				break
			}
		}
		break
	}
	requirementNumber := ""
	if match := workspaceRequirementDetailNumber.FindStringSubmatchIndex(task.requirementURL); len(match) >= 4 {
		end := match[1]
		if end == len(task.requirementURL) || strings.ContainsRune("/?#", rune(task.requirementURL[end])) {
			requirementNumber = task.requirementURL[match[2]:match[3]]
		}
	}
	if requirementNumber == "" {
		matches := workspaceRequirementNumber.FindAllString(task.requirementURL, -1)
		if len(matches) > 0 {
			requirementNumber = matches[len(matches)-1]
		}
	}
	message := strings.ReplaceAll(template, "{num}", requirementNumber)
	message = workspaceCommitHorizontalSpace.ReplaceAllString(message, " ")
	message = workspaceCommitNewlineSpace.ReplaceAllString(message, "\n")
	return strings.TrimSpace(message)
}
