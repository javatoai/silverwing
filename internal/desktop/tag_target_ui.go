package desktop

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type tagTargetViewState struct {
	Open, Saving, FocusPending      bool
	TaskID, TaskPath, TaskCreatedAt string
	Home, TaskRoot                  string
	Workspace                       application.Workspace
	OriginalTarget                  *string
	Draft, Submitted, RequestKey    string
	Error                           string
	Choices                         tagTargetBranchPickerState
}

var tagTargetRequestSequence atomic.Uint64

func (s *app) workspaceTagTargetEnabled(t task, w application.Workspace) bool {
	return s.configBool("allowTaskTagTargetEditing", false) && s.tagWorkspaceCanBuild(t, w) &&
		w.Record.TagMode == domain.TagBuildMergeToTargetBranch
}

func (s *app) beginTagTargetEdit(t task, w application.Workspace) {
	if s.busy || !s.workspaceTagTargetEnabled(t, w) {
		return
	}
	if s.tagTarget != nil {
		cancelTagTargetBranchChoices(&s.tagTarget.Choices)
	}
	var original *string
	if w.Record.TagTargetRef != nil {
		copy := *w.Record.TagTargetRef
		original = &copy
	}
	draft := ""
	if original != nil {
		draft = *original
	}
	s.tagTarget = &tagTargetViewState{
		Open: true, FocusPending: true, TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt,
		Home: s.snapshot.Home, TaskRoot: s.configString("taskRoot"), Workspace: w,
		OriginalTarget: original, Draft: draft,
	}
}

func (s *app) tagTargetIdentityCurrent(state *tagTargetViewState) bool {
	if state == nil || state.Home != s.snapshot.Home || state.TaskRoot != s.configString("taskRoot") {
		return false
	}
	t := s.currentTask()
	return t.id == state.TaskID && t.path == state.TaskPath && t.createdAt == state.TaskCreatedAt &&
		s.workspaceTagTargetEnabled(t, state.Workspace) && tagBuildWorkspaceCurrent(t, state.Workspace)
}

func (s *app) submitTagTargetEdit() {
	state := s.tagTarget
	if state == nil || !state.Open || state.Saving || s.busy {
		return
	}
	if !s.tagTargetIdentityCurrent(state) {
		state.Error = "任务工作区或测试目标已变化，请关闭后重新打开设置。"
		return
	}
	target, err := domain.ParseRemoteBranchRef(strings.TrimSpace(state.Draft))
	if err != nil || strings.HasPrefix(target.Remote, "-") {
		state.Error = "请输入完整的 remote/branch，例如 origin/release/test。"
		return
	}
	if s.backend == nil {
		state.Error = "应用后端不可用。"
		return
	}
	state.Submitted = target.String()
	cancelTagTargetBranchChoices(&state.Choices)
	state.RequestKey = strconv.FormatUint(tagTargetRequestSequence.Add(1), 10)
	state.Saving, state.Error = true, ""
	var expected any
	if state.OriginalTarget != nil {
		expected = *state.OriginalTarget
	}
	w := state.Workspace
	s.act("tag.target.update", state.TaskID, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{
		"expectedTargetRef": expected, "targetRef": state.Submitted, "requestKey": state.RequestKey,
	})
}

func (s *app) tagTargetActionCurrent(action application.Action) bool {
	state := s.tagTarget
	return action.Kind == "tag.target.update" && state != nil && state.Open && state.Saving &&
		action.TaskID == state.TaskID && action.ServiceID == state.Workspace.ServiceID &&
		action.ModuleID == state.Workspace.ModuleID && action.Path == state.Workspace.Path &&
		payloadString(action.Payload, "requestKey") == state.RequestKey && s.tagTargetIdentityCurrent(state)
}

func (s *app) applyTagTargetResult(action application.Action, result application.ActionResult) {
	if !s.tagTargetActionCurrent(action) {
		return
	}
	state := s.tagTarget
	state.Saving = false
	raw, err := json.Marshal(result.Data)
	var manifest domain.TaskManifest
	if err == nil {
		err = json.Unmarshal(raw, &manifest)
	}
	if err != nil || manifest.TaskDirectoryName != state.TaskID {
		state.Error = "保存测试目标分支的返回结果不完整，请刷新任务后检查。"
		return
	}
	var updated *domain.ServiceWorkspace
	for index := range manifest.Services {
		w := &manifest.Services[index]
		if w.GroupServiceID == state.Workspace.ServiceID && w.ModuleID == state.Workspace.ModuleID && w.WorktreePath == state.Workspace.Path {
			updated = w
			break
		}
	}
	if updated == nil || updated.TagTargetRef == nil || *updated.TagTargetRef != state.Submitted {
		state.Error = "保存结果与当前模块或目标不一致，请刷新任务后检查。"
		return
	}
	for taskIndex := range s.tasks {
		if s.tasks[taskIndex].id != state.TaskID {
			continue
		}
		for workspaceIndex := range s.tasks[taskIndex].workspaces {
			w := &s.tasks[taskIndex].workspaces[workspaceIndex]
			if w.ServiceID == updated.GroupServiceID && w.ModuleID == updated.ModuleID && w.Path == updated.WorktreePath {
				w.Record = *updated
			}
		}
	}
	state.Open = false
	cancelTagTargetBranchChoices(&state.Choices)
	s.tagTarget = nil
}

func (s *app) applyTagTargetFailure(action application.Action, err error) bool {
	if action.Kind != "tag.target.update" {
		return false
	}
	if s.tagTargetActionCurrent(action) {
		s.tagTarget.Saving = false
		if err != nil {
			s.tagTarget.Error = err.Error()
		}
	}
	return true
}

func (s *app) renderTagTargetDialog(c *ui.Context, p palette) {
	state := s.tagTarget
	if state == nil {
		return
	}
	if state.Saving {
		state.Open = true
	}
	if !state.Open {
		cancelTagTargetBranchChoices(&state.Choices)
		s.tagTarget = nil
		return
	}
	if !s.tagTargetIdentityCurrent(state) {
		state.Error = "任务工作区或测试目标已变化，请关闭后重新打开设置。"
	}
	c.OnShortcut(ui.Cmd, ui.KeyEnter, func() { s.submitTagTargetEdit() })
	ui.DialogBase(c.Key("tag-target-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(max(320, min(620, w-48))).MaxHeight(max(260, min(520, h-48))).Padding(20).Gap(12).
			Background(p.surface).Border(1, p.line).Radius(14)
		panel.Children(func() {
			label(c, "设置测试目标分支", 18, p.text).FontWeight(600)
			label(c, firstNonEmpty(state.Workspace.Record.ServiceName, state.Workspace.Name)+" / "+state.Workspace.Record.ModuleName, 13, p.text).Wrap()
			current := "未设置"
			if state.OriginalTarget != nil {
				current = *state.OriginalTarget
			}
			label(c, "当前目标："+current, 12, p.muted).Wrap().Selectable()
			field := ui.TextInput(c.Key("tag-target-ref"), &state.Draft).FillWidth().Height(40).
				Label("测试目标分支输入").Placeholder("origin/release/test").Disabled(state.Saving).
				OnChange(func() { state.Error = "" })
			if state.FocusPending {
				field.Focus()
				state.FocusPending = false
			}
			s.renderTagTargetBranchPicker(c, p, state)
			label(c, "仅影响当前任务此模块之后创建的测试Tag。历史操作重试沿用原目标。", 12, p.muted).Wrap()
			if state.Error != "" {
				label(c, state.Error, 12, p.danger).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if toolButtonWithEnabled(c, "tag-target-cancel", "取消", p, false, !state.Saving) {
					state.Open = false
				}
				ref, err := domain.ParseRemoteBranchRef(strings.TrimSpace(state.Draft))
				canSave := err == nil && !strings.HasPrefix(ref.Remote, "-") && !state.Saving && !s.busy && s.tagTargetIdentityCurrent(state)
				text := "保存"
				if state.Saving {
					text = "正在验证并保存…"
				}
				if toolButtonWithEnabled(c, "tag-target-save", text, p, true, canSave) {
					s.submitTagTargetEdit()
				}
			})
		})
	})
	if !state.Open && !state.Saving && s.tagTarget == state {
		cancelTagTargetBranchChoices(&state.Choices)
		s.tagTarget = nil
	}
}
