package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderTaskRepositoryBranchPicker(
	c *ui.Context,
	p palette,
	owner, token, taskID, serviceID, moduleID, repositoryID string,
	draft *taskCreationModuleDraft,
	readOnly bool,
) {
	if draft == nil {
		return
	}
	picker := &draft.repositoryPicker
	picker.ensureMaps()
	prepareTaskRepositoryPickerIdentity(picker, repositoryID, s.repositoryRootPath(repositoryID))
	remote := strings.TrimSpace(draft.BaseRemote)
	if remote == "" {
		remote = taskBranchRemote(draft.BaseRef)
	}
	if !picker.remotesLoaded && !picker.remotesLoading && picker.remotesError == "" && !readOnly {
		s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, "", false)
	}

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		if taskRepositoryRemotePickerVisible(picker, remote) {
			if strings.TrimSpace(picker.remoteQuery) == "" || !picker.remoteOpen {
				picker.remoteQuery = remote
			}
			ui.Column(c).FillWidth().Gap(3).Children(func() {
				label(c, "基础远程", 10, p.muted)
				var combo ui.ComboboxParts
				ui.Row(c).FillWidth().Gap(5).Children(func() {
					combo = ui.ComboboxBase(c.Key("task-repository-remotes-"+owner+"-"+token+"-"+serviceID+"-"+moduleID), &picker.remoteQuery)
					combo.Input.Grow(1).FillWidth().MinWidth(0).Height(34).Padding(0, 30, 0, 9).Placeholder("选择或输入远程").Background(p.surface).Border(1, p.line).Radius(7).Label("基础远程选择器")
					drawTaskComboboxChevron(combo.Input, p)
					if taskRepositoryChoicesRefreshButton(c, "task-repository-remotes-refresh-button-"+owner+"-"+token+"-"+serviceID+"-"+moduleID, "刷新远程", p, readOnly) && !readOnly {
						s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, "", true)
					}
				})
				combo.Popup(func(panel ui.Element) {
					panel.Padding(4).MaxHeight(220).Background(p.surface).Border(1, p.line).Radius(9).Shadow(0, 5, 16, 0, ui.RGBA(0, 0, 0, 0.14))
					ui.Scroll(c).FillWidth().MaxHeight(212).Gap(2).Children(func() {
						for _, name := range picker.remotes {
							if combo.Filtering() && !strings.Contains(strings.ToLower(name), strings.ToLower(strings.TrimSpace(picker.remoteQuery))) {
								continue
							}
							item := combo.Item(name).FillWidth().Height(32).Padding(0, 9).Radius(6)
							if item.Highlighted() {
								item.Background(p.selected)
							}
							item.Children(func() { label(c, name, 11, p.text).SingleLine() })
						}
					})
				})
				wasOpen := picker.remoteOpen
				picker.remoteOpen = combo.Open()
				if wasOpen && !picker.remoteOpen {
					cancelTaskRepositoryRemoteRead(picker)
				}
				if picker.remoteOpen && !picker.remotesLoaded && !picker.remotesLoading && !readOnly {
					s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, "", false)
				}
				if name, ok := combo.Chosen(); ok && !readOnly {
					cancelTaskRepositoryRemoteRead(picker)
					draft.BaseRemote = name
					draft.BaseRef = taskRemoteBranchRef(name, draft.BaseRef)
					picker.remoteQuery = name
					remote = name
					if taskRepositoryBranchChoicesExpired(picker, remote) && !picker.branchesLoading[remote] {
						s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, remote, false)
					}
				}
				if picker.remotesLoading || picker.remotesError != "" {
					ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
						message := ""
						switch {
						case picker.remotesLoading:
							message = "正在读取远程…"
						case picker.remotesError != "":
							message = "远程读取失败：" + picker.remotesError
						}
						label(c, message, 10, map[bool]ui.Color{true: p.amber, false: p.muted}[picker.remotesError != ""]).Grow(1).Wrap()
						if picker.remotesError != "" && action(c, "task-repository-remotes-refresh-"+owner+"-"+token+"-"+serviceID+"-"+moduleID, "refresh", "重试", p, false) && !readOnly {
							s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, "", true)
						}
					})
				}
			})
		}

		ui.Column(c).FillWidth().Gap(3).Children(func() {
			label(c, "基础分支引用", 10, p.muted)
			var combo ui.ComboboxParts
			ui.Row(c).FillWidth().Gap(5).Children(func() {
				combo = ui.ComboboxBase(c.Key("task-repository-ref-"+owner+"-"+token+"-"+serviceID+"-"+moduleID), &draft.BaseRef)
				combo.Input.Grow(1).FillWidth().MinWidth(0).Height(34).Padding(0, 30, 0, 9).Placeholder("origin/main").ReadOnly(readOnly).Background(p.surface).Border(1, p.line).Radius(7).Label("基础分支引用选择器")
				if picker.branchFocusPending {
					combo.Input.Focus().ScrollIntoView()
					picker.branchFocusPending = false
				}
				drawTaskComboboxChevron(combo.Input, p)
				if taskRepositoryChoicesRefreshButton(c, "task-repository-branches-refresh-button-"+owner+"-"+token+"-"+serviceID+"-"+moduleID, "刷新分支", p, readOnly) && !readOnly {
					s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, remote, true)
				}
			})
			combo.Input.OnChange(func() {
				if branchRemote := taskBranchRemote(draft.BaseRef); branchRemote != "" {
					draft.BaseRemote = branchRemote
				}
			})
			branches := mergeRecentTaskBranches(s.recentTaskBranches(repositoryID, remote), picker.branches[remote])
			if combo.Filtering() {
				branches = filterTaskRepositoryBranches(branches, draft.BaseRef)
			}
			combo.Popup(func(panel ui.Element) {
				panel.Padding(4).MaxHeight(260).Background(p.surface).Border(1, p.line).Radius(9).Shadow(0, 5, 16, 0, ui.RGBA(0, 0, 0, 0.14))
				ui.Scroll(c).FillWidth().MaxHeight(252).Gap(2).Children(func() {
					for _, branch := range branches {
						value := branch
						if combo.Filtering() && !strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(draft.BaseRef))) {
							continue
						}
						item := combo.Item(value).FillWidth().Height(32).Padding(0, 9).Radius(6)
						if item.Highlighted() {
							item.Background(p.selected)
						}
						item.Children(func() { label(c, value, 11, p.text).SingleLine() })
					}
				})
			})
			wasOpen := picker.branchOpen
			picker.branchOpen = combo.Open()
			if wasOpen && !picker.branchOpen {
				cancelTaskRepositoryBranchRead(picker, remote)
			}
			if picker.branchOpen && taskRepositoryBranchChoicesExpired(picker, remote) && !picker.branchesLoading[remote] && picker.branchesError[remote] == "" && !readOnly {
				s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, remote, false)
			}
			if branch, ok := combo.Chosen(); ok && !readOnly {
				s.recordRecentTaskBranch(repositoryID, remote, branch)
				draft.BaseRef = branch
				draft.BaseRemote = taskBranchRemote(branch)
				picker.branchFocusPending = false
				cancelTaskRepositoryBranchRead(picker, remote)
			}
			if picker.branchesLoading[remote] || picker.branchesError[remote] != "" {
				ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
					message := ""
					switch {
					case picker.branchesLoading[remote] && len(picker.branches[remote]) == 0:
						message = "正在读取远程分支…"
					case picker.branchesLoading[remote]:
						message = "正在刷新，候选仍可使用。"
					case picker.branchesError[remote] != "":
						message = "分支读取失败：" + picker.branchesError[remote]
					}
					label(c, message, 10, map[bool]ui.Color{true: p.amber, false: p.muted}[picker.branchesError[remote] != ""]).Grow(1).Wrap()
					if picker.branchesError[remote] != "" && action(c, "task-repository-branches-refresh-"+owner+"-"+token+"-"+serviceID+"-"+moduleID, "refresh", "重试", p, false) && !readOnly {
						s.requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, remote, true)
					}
				})
			}
		})
	})
}

func taskRepositoryChoicesRefreshButton(c *ui.Context, key, title string, p palette, disabled bool) bool {
	button := ui.ButtonBase(c.Key(key)).Size(34, 34).Padding(0).Radius(7).Border(1, p.line).Background(p.surface).Label(title).Tooltip(title).Disabled(disabled)
	button.Children(func() { icon(c, "refresh", 14, p.blue) })
	if button.Hovered() && !disabled {
		button.Background(p.soft)
	}
	return button.Clicked()
}
