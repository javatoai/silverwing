package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

type tagTargetBranchPickerState struct {
	Open, Loading                                    bool
	Remote, Query, Error, RequestKey, RepositoryRoot string
	Remotes                                          []string
	Branches                                         []string
	Cancel                                           context.CancelFunc
}

func cancelTagTargetBranchChoices(picker *tagTargetBranchPickerState) {
	if picker.Cancel != nil {
		picker.Cancel()
	}
	picker.Cancel, picker.Loading, picker.RequestKey = nil, false, ""
}

func (s *app) requestTagTargetBranchChoices(state *tagTargetViewState, remote string, force bool) {
	if s.tagTarget != state || state.Saving || !state.Open || !s.tagTargetIdentityCurrent(state) || s.backend == nil {
		return
	}
	picker := &state.Choices
	cancelTagTargetBranchChoices(picker)
	picker.Remote, picker.Loading, picker.Error = remote, true, ""
	picker.Branches = nil
	picker.RepositoryRoot = s.repositoryRootPath(state.Workspace.Record.RepositoryID)
	picker.RequestKey = newDesktopEditorID("tag-target-choices")
	payload := map[string]any{"repositoryId": state.Workspace.Record.RepositoryID, "force": force, "requestKey": picker.RequestKey}
	if remote != "" {
		payload["remote"] = remote
	}
	action := application.Action{Kind: "repository.choices", TaskID: state.TaskID, ServiceID: state.Workspace.ServiceID, ModuleID: state.Workspace.ModuleID, Payload: payload}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	picker.Cancel = cancel
	backend := s.backend
	go func() {
		defer cancel()
		result, err := backend.Dispatch(ctx, action)
		if s.win == nil || s.win.IsDestroyed() {
			return
		}
		s.win.Update(func() { s.applyTagTargetBranchChoices(action, result, err) })
	}()
}

func (s *app) applyTagTargetBranchChoices(action application.Action, result application.ActionResult, readErr error) {
	state := s.tagTarget
	if state == nil || !state.Open || state.Saving || !s.tagTargetIdentityCurrent(state) || action.Kind != "repository.choices" ||
		action.TaskID != state.TaskID || action.ServiceID != state.Workspace.ServiceID || action.ModuleID != state.Workspace.ModuleID {
		return
	}
	picker := &state.Choices
	if picker.RequestKey == "" || payloadString(action.Payload, "requestKey") != picker.RequestKey ||
		payloadString(action.Payload, "repositoryId") != state.Workspace.Record.RepositoryID || payloadString(action.Payload, "remote") != picker.Remote {
		return
	}
	picker.Loading, picker.Cancel = false, nil
	if result.Data != nil {
		raw, err := json.Marshal(result.Data)
		var choices application.RepositoryChoices
		if err == nil {
			err = json.Unmarshal(raw, &choices)
		}
		responseRemote := ""
		if choices.Remote != nil {
			responseRemote = *choices.Remote
		}
		if err != nil || choices.RepositoryID != state.Workspace.Record.RepositoryID || responseRemote != picker.Remote ||
			!sameTaskRepositoryPath(choices.RootPath, picker.RepositoryRoot) || !sameTaskRepositoryPath(picker.RepositoryRoot, s.repositoryRootPath(choices.RepositoryID)) {
			picker.Error = "分支候选与当前仓库或远程不一致，请重新读取。"
			return
		}
		picker.Remotes = append([]string(nil), choices.Remotes...)
		picker.Branches = nil
		for _, branch := range choices.Branches {
			if strings.HasPrefix(branch, picker.Remote+"/") && len(branch) > len(picker.Remote)+1 {
				picker.Branches = append(picker.Branches, branch)
			}
		}
	}
	if readErr != nil {
		if !errors.Is(readErr, context.Canceled) {
			picker.Error = readErr.Error()
		}
		return
	}
	if result.Data == nil {
		picker.Error = "分支候选为空，请重试。"
		return
	}
	picker.Error = ""
	if picker.Remote == "" && len(picker.Remotes) > 0 && picker.Open {
		s.requestTagTargetBranchChoices(state, picker.Remotes[0], false)
	}
}

func (s *app) renderTagTargetBranchPicker(c *ui.Context, p palette, state *tagTargetViewState) {
	picker := &state.Choices
	ui.Row(c).Gap(8).Children(func() {
		if toolButtonWithEnabled(c, "tag-target-branches", "选择远端分支", p, false, !state.Saving && !s.busy) {
			picker.Open = !picker.Open
			if picker.Open {
				remote := firstNonEmpty(taskBranchRemote(state.Draft), state.Workspace.Record.PushRemote)
				s.requestTagTargetBranchChoices(state, remote, false)
			} else {
				cancelTagTargetBranchChoices(picker)
			}
		}
		if picker.Open && toolButtonWithEnabled(c, "tag-target-branches-refresh", "刷新分支", p, false, !state.Saving && !picker.Loading) {
			s.requestTagTargetBranchChoices(state, picker.Remote, true)
		}
	})
	if !picker.Open {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Padding(8).Border(1, p.line).Radius(8).Children(func() {
		if len(picker.Remotes) > 1 || len(picker.Remotes) == 1 && picker.Remotes[0] != picker.Remote {
			ui.Row(c).Gap(5).Wrap().Children(func() {
				for _, value := range picker.Remotes {
					remote := value
					if toolButtonWithEnabled(c, "tag-target-remote-"+remote, remote, p, picker.Remote == remote, !state.Saving) {
						state.Draft = taskRemoteBranchRef(remote, state.Draft)
						s.requestTagTargetBranchChoices(state, remote, false)
					}
				}
			})
		}
		ui.TextInput(c.Key("tag-target-branch-query"), &picker.Query).FillWidth().Height(32).Label("筛选测试目标分支").Placeholder("搜索远端分支").Disabled(state.Saving)
		if picker.Loading {
			label(c, "正在读取远端分支…", 11, p.muted)
		}
		if picker.Error != "" {
			label(c, picker.Error, 12, p.danger).Wrap()
		}
		ui.Scroll(c).FillWidth().MaxHeight(150).Gap(3).Children(func() {
			query := strings.ToLower(strings.TrimSpace(picker.Query))
			visible := 0
			for index, value := range picker.Branches {
				branch := value
				if query != "" && !strings.Contains(strings.ToLower(branch), query) {
					continue
				}
				visible++
				if toolButtonWithEnabled(c, fmt.Sprintf("tag-target-branch-%d", index), branch, p, state.Draft == branch, !state.Saving && !picker.Loading) {
					state.Draft, state.Error, picker.Open = branch, "", false
					cancelTagTargetBranchChoices(picker)
				}
			}
			if visible == 0 && !picker.Loading && picker.Error == "" {
				label(c, "没有匹配的远端分支，可直接输入完整 remote/branch。", 11, p.muted).Wrap()
			}
		})
	})
}
