package desktop

import (
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type tagBuildViewState struct {
	Open                            bool
	TaskID, TaskPath, TaskCreatedAt string
	Home, TaskRoot                  string
	Workspaces                      []application.Workspace
	Selected                        map[string]bool
	Error                           string
}

// Task workspaces own their saved Tag mode, permission and destination. A
// later service-editor change only applies to subsequently created workspaces.
func (s *app) tagWorkspaceCanBuild(t task, w application.Workspace) bool {
	if t.id == "" || t.archived || !s.configBool("tagEnabled", false) || !w.Record.TagEnabled ||
		w.ServiceID == "" || w.ModuleID == "" || t.groupID == "" || !s.hasGroup(t.groupID) {
		return false
	}
	return strings.EqualFold(w.State, "READY") || strings.EqualFold(w.State, "READY_WITH_WARNINGS")
}

func workspaceTagActionLabel(w application.Workspace) string {
	if w.Record.TagMode == domain.TagBuildCurrentBranch {
		return "构建测试Tag · 当前分支 " + w.Record.Branch
	}
	target := "未设置"
	if w.Record.TagTargetRef != nil && strings.TrimSpace(*w.Record.TagTargetRef) != "" {
		target = *w.Record.TagTargetRef
	}
	return "构建测试Tag · 目标 " + target
}

func (s *app) beginWorkspaceTagBuild(t task, w application.Workspace) {
	if s.tagWorkspaceBusy(t, w) || !s.tagWorkspaceCanBuild(t, w) {
		return
	}
	current := s.taskUnit.currentTask()
	if current.id != t.id || current.path != t.path || current.createdAt != t.createdAt || !tagBuildWorkspaceCurrent(current, w) {
		s.lastError = "任务工作区已变化，请刷新后重新构建。"
		return
	}
	s.act("tag.build", t.id, w.ServiceID, w.ModuleID, w.Path, "", nil)
	s.showStartedTagBuild(t)
}

func (s *app) showStartedTagBuild(t task) bool {
	current := s.taskUnit.currentTask()
	if current.id != t.id || current.path != t.path || current.createdAt != t.createdAt {
		return false
	}
	if _, ok := s.backend.(application.TagJobBackend); ok {
		view := s.currentTagJobView()
		if view == nil || !hasPendingTagSubmission(view) && (view.snapshot.ID == "" || len(view.commands) != 0) {
			return false
		}
	} else if !s.busy || s.operationKind != "tag.build" {
		return false
	}
	s.markReadingInteraction()
	s.observeReadingSession()
	if s.tab == 3 {
		s.deactivateInstructionEditor()
	}
	s.tab = taskTagTab
	s.observeReadingSession()
	return true
}

func (s *app) openTagBuildBatch(t task) {
	if t.archived {
		return
	}
	workspaces := make([]application.Workspace, 0, len(t.workspaces))
	for _, w := range t.workspaces {
		if s.tagWorkspaceCanBuild(t, w) {
			workspaces = append(workspaces, w)
		}
	}
	if len(workspaces) == 0 {
		return
	}
	s.releaseUnit.state.tagBuild = &tagBuildViewState{
		Open: true, TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt,
		Home: s.snapshot.Home, TaskRoot: s.configString("taskRoot"),
		Workspaces: workspaces, Selected: make(map[string]bool),
	}
}

func tagBuildWorkspaceKey(w application.Workspace) string { return w.ServiceID + ":" + w.ModuleID }

func tagBuildWorkspaceCurrent(t task, expected application.Workspace) bool {
	for _, w := range t.workspaces {
		if w.ServiceID == expected.ServiceID && w.ModuleID == expected.ModuleID && w.Path == expected.Path {
			a, b := w.Record, expected.Record
			return a.RepositoryID == b.RepositoryID && a.Branch == b.Branch && a.PushRemote == b.PushRemote &&
				a.TagEnabled == b.TagEnabled && a.TagMode == b.TagMode && a.TagMessagePrefix == b.TagMessagePrefix &&
				tagOptionalStringEqual(a.TagTargetRef, b.TagTargetRef)
		}
	}
	return false
}

func tagOptionalStringEqual(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (s *app) tagBuildIdentityCurrent(state *tagBuildViewState) bool {
	if state == nil || state.Home != s.snapshot.Home || state.TaskRoot != s.configString("taskRoot") {
		return false
	}
	t := s.taskUnit.currentTask()
	if t.id != state.TaskID || t.path != state.TaskPath || t.createdAt != state.TaskCreatedAt || t.archived {
		return false
	}
	for _, w := range state.Workspaces {
		if !s.tagWorkspaceCanBuild(t, w) || !tagBuildWorkspaceCurrent(t, w) {
			return false
		}
	}
	return true
}

func (s *app) submitTagBuildBatch() {
	state := s.releaseUnit.state.tagBuild
	if state == nil || !state.Open || s.tagBuildSelectedBusy(state) {
		return
	}
	if !s.tagBuildIdentityCurrent(state) {
		state.Error = "任务工作区或测试目标已变化，请关闭后重新选择。"
		return
	}
	keys := make([]string, 0, len(state.Selected))
	for _, w := range state.Workspaces {
		key := tagBuildWorkspaceKey(w)
		if state.Selected[key] {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	t := s.taskUnit.currentTask()
	s.act("tag.build", state.TaskID, "", "", "", "", map[string]any{"selectionKeys": keys, "batch": true})
	if s.showStartedTagBuild(t) {
		state.Open = false
		s.releaseUnit.state.tagBuild = nil
	}
}

func (s *app) renderTagBuildDialog(c *ui.Context, p palette) {
	state := s.releaseUnit.state.tagBuild
	if state == nil || !state.Open {
		return
	}
	if !s.tagBuildIdentityCurrent(state) {
		state.Error = "任务工作区或测试目标已变化，请关闭后重新选择。"
	}
	ui.DialogBase(c.Key("tag-build-batch-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(max(320, min(680, w-48))).MaxHeight(max(260, min(650, h-48))).Padding(18).Gap(12).
			Background(p.surface).Border(1, p.line).Radius(14)
		panel.Children(func() {
			label(c, "批量测试Tag", 18, p.text).FontWeight(600)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, "选择要构建的服务模块", 12, p.muted).Grow(1)
				ui.Row(c).Gap(8).Children(func() {
					if action(c, "tag-build-select-all", "", "全选", p, false) {
						for _, w := range state.Workspaces {
							if !s.tagWorkspaceBusy(s.taskUnit.currentTask(), w) {
								state.Selected[tagBuildWorkspaceKey(w)] = true
							}
						}
					}
					if action(c, "tag-build-select-none", "", "全不选", p, false) {
						state.Selected = make(map[string]bool)
					}
				})
			})
			ui.Scroll(c).FillWidth().MinHeight(120).MaxHeight(420).Gap(6).Children(func() {
				for _, workspace := range state.Workspaces {
					item := workspace
					key := tagBuildWorkspaceKey(item)
					selected := state.Selected[key]
					name := firstNonEmpty(item.Record.ModuleName, item.Record.ServiceName, item.Name)
					// One native checkbox owns the entire row, including its box,
					// text and padding, so a pointer gesture only toggles once.
					row := ui.Checkbox(c.Key("tag-build-row-"+key), &selected, "").FillWidth().Padding(8).Gap(10).AlignItems(ui.Center).
						Background(p.soft).Radius(8).Label("选择构建 " + name).Disabled(s.tagWorkspaceBusy(s.taskUnit.currentTask(), item)).OnChange(func() {
						if selected {
							state.Selected[key] = true
						} else {
							delete(state.Selected, key)
						}
					})
					if selected {
						row.Background(p.selected)
					} else if row.Hovered() && !s.tagWorkspaceBusy(s.taskUnit.currentTask(), item) {
						row.Background(p.line)
					}
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							label(c, name, 13, p.text).FontWeight(600).Wrap()
							label(c, firstNonEmpty(item.Record.ServiceName, item.Name)+" · "+item.Record.Branch, 11, p.muted).Wrap()
							label(c, workspaceTagActionLabel(item), 11, p.muted).Wrap()
						})
					})
				}
			})
			if state.Error != "" {
				label(c, state.Error, 12, p.danger).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "tag-build-cancel", "", "取消", p, false) {
					state.Open = false
				}
				canSubmit := len(state.Selected) > 0 && state.Error == "" && !s.tagBuildSelectedBusy(state)
				if toolButtonWithEnabled(c, "tag-build-submit", "开始构建", p, true, canSubmit) {
					s.submitTagBuildBatch()
				}
			})
		})
	})
	if !state.Open && s.releaseUnit.state.tagBuild == state {
		s.releaseUnit.state.tagBuild = nil
	}
}

func (s *app) tagBuildSelectedBusy(state *tagBuildViewState) bool {
	for _, workspace := range state.Workspaces {
		if state.Selected[tagBuildWorkspaceKey(workspace)] && s.tagWorkspaceBusy(s.taskUnit.currentTask(), workspace) {
			return true
		}
	}
	return false
}
