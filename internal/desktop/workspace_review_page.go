package desktop

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type workspaceReviewFocusReturn struct {
	Handle        ui.Handle
	TaskID        string
	TaskPath      string
	TaskCreatedAt string
	Page          string
	deferOneFrame bool
}

func (s *app) workspaceReviewPageVisible() bool {
	state := s.workspaceHistory
	if state == nil || state.Mode != "review" || !state.Open || (s.page != "tasks" && s.page != "archive") {
		return false
	}
	t := s.currentTask()
	if t.id != state.TaskID || t.path == "" || t.path != s.workspaceReviewTaskPath(state.TaskID) || (s.page == "archive") != t.archived {
		return false
	}
	if state.TaskPath != "" && state.TaskPath != t.path || state.TaskCreatedAt != "" && state.TaskCreatedAt != t.createdAt {
		return false
	}
	if state.RepositorySessionKey != "" && state.RepositorySessionKey != workspaceReviewRepositoryKey(s.snapshot.Home, state.Path) {
		return false
	}
	for _, item := range t.workspaces {
		if item.ServiceID == state.ServiceID && item.ModuleID == state.ModuleID &&
			workspaceHealthPathKey(item.Path) == workspaceHealthPathKey(state.Path) {
			return true
		}
	}
	return false
}

func (s *app) workspaceReviewTaskPath(taskID string) string {
	if taskID == "" {
		return ""
	}
	for _, item := range s.tasks {
		if item.id == taskID {
			return item.path
		}
	}
	return ""
}

func (s *app) restoreWorkspaceReviewFocus(c *ui.Context) {
	pending := s.workspaceReviewFocusReturn
	if pending == nil {
		return
	}
	t := s.currentTask()
	if s.page != pending.Page || t.id != pending.TaskID || t.path != pending.TaskPath || t.createdAt != pending.TaskCreatedAt {
		s.workspaceReviewFocusReturn = nil
		return
	}
	if state := s.workspaceHistory; state != nil && state.Open {
		return
	}
	if pending.deferOneFrame {
		pending.deferOneFrame = false
		return
	}
	pending.Handle.Focus(c)
	s.workspaceReviewFocusReturn = nil
}

func (s *app) renderWorkspaceReviewPage(c *ui.Context, p palette) {
	state := s.workspaceHistory
	if !s.workspaceReviewPageVisible() {
		return
	}
	t := s.currentTask()
	item, exists := workspaceReviewWorkspace(t, state)
	if !exists {
		s.dismissWorkspaceHistory()
		return
	}
	serviceName, moduleName := s.workspaceNames(t.groupID, item)
	title := firstNonEmpty(serviceName, item.ServiceID)
	if moduleName != "" && moduleName != serviceName {
		title += " · " + moduleName
	}
	branch := firstNonEmpty(item.Branch, item.Record.Branch, "未知分支")
	width, _ := c.Size()
	key := workspaceReviewKey(state)

	c.OnShortcut(0, ui.KeyEscape, func() {
		if s.overflowMenus != nil && s.overflowMenus.openKey != "" {
			return
		}
		if state.CommitPickerOpen {
			state.CommitPickerOpen = false
			return
		}
		s.backWorkspaceReview(state)
	})

	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Padding(8).Gap(8).Children(func() {
		ui.Row(c).FillWidth().Height(44).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
			back := ui.ButtonBase(c.Key(key+"-back")).Row().Height(34).Padding(0, 10).Gap(6).Radius(9).Label("返回任务").Tooltip("返回任务列表").FocusRing(true)
			if back.Hovered() {
				back.Background(p.soft)
			}
			back.Children(func() {
				icon(c, "chevron-left", 16, p.text)
				label(c, "返回", 12, p.text).NoWrap()
			})
			if back.Clicked() {
				s.backWorkspaceReview(state)
			}
			ui.Box(c).Width(1).Height(22).Background(p.line)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				label(c, title, 14, p.text).FontWeight(600).SingleLine().Tooltip(title)
				label(c, branch, 11, p.muted).SingleLine().Tooltip(branch)
			})
			if iconButtonRunning(c, key+"-refresh", "refresh", "刷新差异", p, state.FilesLoading || state.HistoryLoading || state.PreviewLoading, state.FilesLoading || state.HistoryLoading || state.PreviewLoading).Clicked() {
				s.refreshWorkspaceReview(state)
			}
			s.overflowMenu(c, p, key+"-more", "更多审阅操作", false, func(menu *overflowMenuBuilder) {
				if state.CompareMode == workspaceReviewMainline {
					if menu.Item("include-working-tree", "包含本地未提交").Icon("check").Checked(state.IncludeWorkingTree).Chosen() {
						s.saveWorkspaceReviewSelection(state)
						state.IncludeWorkingTree = !state.IncludeWorkingTree
						s.readWorkspaceReviewFiles(state)
					}
				}
				if state.CompareMode == workspaceReviewCommits && len(state.CommitHashes) > 0 {
					if menu.Item("simulate-on-mainline", "模拟应用到主分支").Icon("check").Checked(state.SimulateOnMainline).Chosen() {
						s.saveWorkspaceReviewSelection(state)
						state.SimulateOnMainline = !state.SimulateOnMainline
						s.readWorkspaceReviewFiles(state)
					}
				}
				menu.Separator()
				fileViewLabel, fileViewKey := "按相对路径平铺", "file-view-flat"
				if state.FileView == "flat" {
					fileViewLabel, fileViewKey = "按目录树排列", "file-view-tree"
				}
				if menu.Item(fileViewKey, fileViewLabel).Icon("folder").Chosen() {
					if state.FileView == "flat" {
						state.FileView = "tree"
					} else {
						state.FileView = "flat"
					}
					s.saveWorkspaceReviewSelection(state)
				}
				menu.Separator()
				if menu.Item("open-folder", "打开工作区目录").Icon("folder").Chosen() {
					s.act("system.open", t.id, item.ServiceID, item.ModuleID, item.Path, "", map[string]any{"desktopSystemOpenDirectory": true})
				}
				if menu.Item("terminal", "在终端中打开").Icon("terminal").Chosen() {
					s.act("system.terminal", t.id, item.ServiceID, item.ModuleID, item.Path, "", nil)
				}
			})
		})

		ui.Row(c).FillWidth().Height(38).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
			workspaceReviewModeButton(c, key+"-mode-mainline", "整体差异", state.CompareMode == workspaceReviewMainline, p, func() {
				s.setWorkspaceReviewMode(state, workspaceReviewMainline)
			})
			workspaceReviewModeButton(c, key+"-mode-uncommitted", "本地未提交", state.CompareMode == workspaceReviewUncommitted, p, func() {
				s.setWorkspaceReviewMode(state, workspaceReviewUncommitted)
			})
			workspaceReviewModeButton(c, key+"-mode-commits", "提交范围", state.CompareMode == workspaceReviewCommits, p, func() {
				s.setWorkspaceReviewMode(state, workspaceReviewCommits)
			})
			if state.CompareMode == workspaceReviewCommits {
				s.renderWorkspaceReviewCommitPicker(c, p, state, key)
			}
			ui.Spacer(c)
		})
		if state.Error != "" {
			ui.Box(c).FillWidth().Shrink(0).Padding(8, 10).Radius(8).Background(p.amberBg).Children(func() {
				label(c, state.Error, 12, p.danger).Wrap()
			})
		}

		ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			leftWidth := state.LeftWidth
			if leftWidth <= 0 {
				leftWidth = 260
			}
			if state.LeftCollapsed {
				leftWidth = 38
			} else {
				leftWidth = min(max(float32(185), leftWidth), max(float32(185), width-440))
				state.LeftWidth = leftWidth
			}
			ui.Column(c).Width(leftWidth).Shrink(0).MinWidth(0).MinHeight(0).Background(p.side).Border(1, p.line).Radius(12).Clip().Children(func() {
				s.renderWorkspaceReviewFileList(c, p, state, key)
			})
			handle := ui.Box(c.Key(key + "-resize")).Width(8).FillHeight().Cursor(ui.CursorResizeEW).Focusable().Role(ui.RoleSplitter).Label("拖动调整文件列表宽度").Tooltip("拖动调整文件列表宽度")
			dx, _, held := handle.Dragged()
			if !state.LeftCollapsed {
				if held {
					state.LeftWidth = min(max(float32(185), state.LeftWidth+dx), max(float32(185), width-440))
					state.LeftDragging = true
				} else if state.LeftDragging {
					state.LeftDragging = false
				}
			}
			divider := p.line
			if held || handle.Hovered() {
				divider = p.blue
			}
			handle.Center().Children(func() { ui.Box(c).Width(2).FillHeight().Margin(14, 0).Background(divider) })
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(p.surface).Border(1, p.line).Radius(12).Clip().Padding(8).Gap(7).Children(func() {
				s.renderWorkspaceReviewPreview(c, p, state, key)
			})
		})
	})
}

func workspaceReviewWorkspace(t task, state *workspaceHistoryState) (application.Workspace, bool) {
	if state == nil {
		return application.Workspace{}, false
	}
	for _, item := range t.workspaces {
		if item.ServiceID == state.ServiceID && item.ModuleID == state.ModuleID &&
			workspaceHealthPathKey(item.Path) == workspaceHealthPathKey(state.Path) {
			return item, true
		}
	}
	return application.Workspace{}, false
}

func workspaceReviewKey(state *workspaceHistoryState) string {
	if state == nil {
		return "workspace-review"
	}
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(state.TaskID + "-" + state.ServiceID + "-" + state.ModuleID)
}

func (s *app) refreshWorkspaceReview(state *workspaceHistoryState) {
	if state == nil || state != s.workspaceHistory || !s.workspaceReviewPageVisible() {
		return
	}
	s.cancelWorkspaceHistoryRead()
	if state.CompareMode == workspaceReviewCommits {
		state.CommitsLoaded = false
		s.readWorkspaceReviewCommits(state)
		return
	}
	s.readWorkspaceReviewFiles(state)
}

func workspaceReviewModeButton(c *ui.Context, key, text string, selected bool, p palette, click func()) {
	button := ui.ButtonBase(c.Key(key)).Row().Height(32).Padding(0, 11).Radius(8).Label(text).FocusRing(true)
	fg := p.text
	if selected {
		button.Background(p.selected)
		fg = p.blue
	} else if button.Hovered() {
		button.Background(p.soft)
	}
	button.Children(func() { label(c, text, 12, fg).NoWrap() })
	if button.Clicked() {
		click()
	}
}

func (s *app) renderWorkspaceReviewCommitPicker(c *ui.Context, p palette, state *workspaceHistoryState, key string) {
	labelText := "选择提交"
	tooltip := "选择要比较的提交范围"
	if len(state.CommitHashes) == 1 {
		labelText = "提交 " + shortCommitID(state.CommitHashes[0])
		tooltip = state.CommitHashes[0]
	} else if len(state.CommitHashes) > 1 {
		labelText = fmt.Sprintf("%s → %s · %d 次", shortCommitID(state.StartCommit), shortCommitID(state.EndCommit), len(state.CommitHashes))
		tooltip = state.StartCommit + " → " + state.EndCommit
	}
	trigger := ui.ButtonBase(c.Key(key+"-range-picker")).Row().Height(32).Padding(0, 10).Gap(6).Radius(8).
		Role(ui.RoleButton).Label("提交范围：" + labelText).Tooltip(tooltip).FocusRing(true)
	if state.CommitPickerOpen || trigger.Focused() {
		trigger.Background(p.selected)
	} else if trigger.Hovered() {
		trigger.Background(p.soft)
	}
	trigger.Children(func() {
		icon(c, "commit", 14, p.blue)
		label(c, labelText, 11, p.text).NoWrap().Tooltip(tooltip)
		icon(c, "chevron", 13, p.muted)
	})
	if trigger.Clicked() {
		state.CommitPickerOpen = !state.CommitPickerOpen
	}
	if !state.CommitPickerOpen {
		return
	}
	width, height := c.Size()
	maxWidth := max(float32(1), width-20)
	panelWidth := min(float32(560), maxWidth)
	maxHeight := max(float32(120), min(float32(510), height-24))
	ui.PopoverBase(c, trigger, &state.CommitPickerOpen, func(panel ui.Element) {
		panel.AttachTo(trigger, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(4, 0, 0, 0).
			Width(panelWidth).MaxWidth(maxWidth).MaxHeight(maxHeight).
			Padding(10).Background(p.surface).Border(1, p.line).Radius(12).
			Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).Children(func() {
			ui.Column(c).FillWidth().MaxHeight(maxHeight - 20).MinHeight(0).Gap(8).Children(func() {
				ui.Row(c).FillWidth().Height(30).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						label(c, "选择提交范围", 12, p.text).FontWeight(600)
						label(c, "单选查看该次提交；选择两端会自动包含中间提交。", 10, p.muted).SingleLine()
					})
					if len(state.CommitHashes) > 0 {
						label(c, fmt.Sprintf("%d 次", len(state.CommitHashes)), 10, p.blue).NoWrap()
					}
				})
				ui.Divider(c)
				if state.HistoryLoading && len(state.Commits) == 0 {
					ui.Column(c).Grow(1).FillWidth().Center().MinHeight(100).Children(func() { label(c, "正在读取提交…", 11, p.muted) })
				} else if len(state.Commits) == 0 {
					ui.Column(c).Grow(1).FillWidth().Center().MinHeight(100).Padding(12).Children(func() {
						label(c, "主分支之后没有可选提交。", 11, p.muted).Wrap()
					})
				} else {
					ui.Scroll(c.Key(key + "-commit-picker-list")).Grow(1).MinHeight(0).TrackScroll(&state.CommitPickerScroll).Padding(3).Gap(3).Children(func() {
						ui.Column(c).Gap(3).FocusGroup(ui.Vertical).Role(ui.RoleMenu).Label("提交范围").Children(func() {
							for _, commit := range state.Commits {
								s.renderWorkspaceReviewCommitOption(c, p, state, key, commit)
							}
						})
					})
				}
				ui.Divider(c)
				ui.Row(c).FillWidth().Height(34).Shrink(0).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Spacer(c)
					done := ui.ButtonBase(c.Key(key+"-commit-picker-done")).Height(30).Padding(0, 13).Radius(8).Label("完成").FocusRing(true)
					if done.Hovered() {
						done.Background(p.soft)
					}
					done.Children(func() { label(c, "完成", 11, p.blue).NoWrap() })
					if done.Clicked() {
						state.CommitPickerOpen = false
					}
				})
			})
		})
	})
}

func (s *app) renderWorkspaceReviewCommitOption(c *ui.Context, p palette, state *workspaceHistoryState, key string, commit domain.WorkspaceGitCommit) {
	if state.ExpandedCommits == nil {
		state.ExpandedCommits = make(map[string]bool)
	}
	selected := containsString(state.CommitHashes, commit.FullHash)
	rowKey := key + "-commit-" + commit.FullHash
	expanded := state.ExpandedCommits[commit.FullHash]
	message := strings.TrimSpace(commit.Message)
	displayMessage := message
	if !expanded {
		displayMessage = strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	}
	rowMinHeight := float32(60)
	if expanded {
		rowMinHeight = 72
	}
	ui.Row(c).FillWidth().MinHeight(rowMinHeight).Shrink(0).Gap(5).AlignItems(ui.Center).Children(func() {
		button := ui.ButtonBase(c.Key(rowKey)).Grow(1).MinWidth(0).FillHeight().Row().Padding(6, 8).Gap(8).Radius(8).
			Role(ui.RoleMenuItemCheckBox).Label(commit.ShortHash + " " + message).Checked(selected).FocusRing(true)
		if selected {
			button.Background(p.selected)
		} else if button.Hovered() {
			button.Background(p.soft)
		}
		button.Children(func() {
			if selected {
				icon(c, "check", 15, p.blue)
			} else {
				ui.Box(c).Width(14).Height(14).Shrink(0).Border(1, p.line).Radius(3)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					label(c, firstNonEmpty(commit.ShortHash, shortCommitID(commit.FullHash)), 10, p.blue).FontWeight(600).NoWrap().Tooltip(commit.FullHash)
					if !commit.CommittedAt.IsZero() {
						label(c, commit.CommittedAt.Local().Format("2006-01-02 15:04"), 9, p.muted).NoWrap()
					}
				})
				messageLabel := label(c, displayMessage, 10, p.text).FillWidth().Tooltip(message)
				if expanded {
					messageLabel.Wrap()
				} else {
					messageLabel.SingleLine()
				}
			})
		})
		if button.Clicked() {
			s.selectWorkspaceReviewCommit(state, commit.FullHash)
		}
		if strings.Contains(message, "\n") || len(message) > 96 {
			expandText := "展开"
			if expanded {
				expandText = "收起"
			}
			more := ui.ButtonBase(c.Key(rowKey + "-expand")).Width(44).Height(28).Radius(7).Label(expandText).Tooltip(expandText)
			if more.Hovered() {
				more.Background(p.soft)
			}
			more.Children(func() { label(c, expandText, 9, p.blue).NoWrap() })
			if more.Clicked() {
				state.ExpandedCommits[commit.FullHash] = !expanded
			}
		}
	})
}

func (s *app) renderWorkspaceReviewFileList(c *ui.Context, p palette, state *workspaceHistoryState, key string) {
	state.PrefetchFrameVisible = state.PrefetchFrameVisible[:0]
	if state.FileSelected == nil {
		defer func() { s.updateWorkspaceReviewPrefetch(state, state.PrefetchFrameVisible) }()
	}
	if state.LeftCollapsed {
		ui.Column(c).FillWidth().MinHeight(0).AlignItems(ui.Center).Padding(3).Children(func() {
			if iconButton(c, key+"-expand-left", "arrow", "展开文件列表", p).Clicked() {
				state.LeftCollapsed = false
			}
		})
		return
	}
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		ui.Row(c).FillWidth().Height(38).Shrink(0).Padding(0, 9).Gap(5).AlignItems(ui.Center).Children(func() {
			label(c, "变更文件", 12, p.text).FontWeight(600).Grow(1)
			if state.FileView == "flat" {
				label(c, "路径", 10, p.muted).NoWrap()
			} else {
				label(c, "目录", 10, p.muted).NoWrap()
			}
			if iconButton(c, key+"-collapse-left", "chevron-left", "收起文件列表", p).Clicked() {
				state.LeftCollapsed = true
			}
		})
		ui.Divider(c)
		if state.FilesLoading && len(state.Files) == 0 {
			ui.Column(c).Grow(1).FillWidth().Center().Children(func() { label(c, "正在读取文件…", 11, p.muted) })
			return
		}
		if state.CompareMode == workspaceReviewCommits && len(state.CommitHashes) == 0 {
			ui.Column(c).Grow(1).FillWidth().Center().Padding(16).Children(func() { label(c, "选择提交范围后查看变更。", 11, p.muted).Wrap() })
			return
		}
		if len(state.Files) == 0 {
			message := "没有差异文件。"
			if state.CompareMode == workspaceReviewCommits && state.FilesLoading {
				message = "正在整理所选提交的差异…"
			}
			ui.Column(c).Grow(1).FillWidth().Center().Padding(16).Children(func() { label(c, message, 11, p.muted).Wrap() })
			return
		}
		workspaceReviewEnsureExpandedDirectories(state, state.Files)
		if state.FileScrolls == nil {
			state.FileScrolls = make(map[string]*ui.ScrollState)
		}
		scroll := state.FileScrolls[state.FileView]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			state.FileScrolls[state.FileView] = scroll
		}
		list := ui.Scroll(c.Key(key + "-files-" + state.FileView)).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(5).Gap(2)
		state.PrefetchViewport = list.Bounds()
		list.Children(func() {
			if state.FileView == "flat" {
				for _, file := range workspaceReviewSortedFiles(state.Files) {
					s.renderWorkspaceReviewFileRow(c, p, state, key, file, 0)
				}
				return
			}
			root := workspaceReviewTree(state.Files)
			for _, file := range root.Files {
				s.renderWorkspaceReviewFileRow(c, p, state, key, file, 0)
			}
			for _, directory := range workspaceReviewSortedDirectories(root) {
				s.renderWorkspaceReviewDirectory(c, p, state, key, directory, 0)
			}
		})
	})
}

func (s *app) renderWorkspaceReviewDirectory(c *ui.Context, p palette, state *workspaceHistoryState, key string, directory *workspaceReviewTreeDirectory, depth int) {
	chain := []*workspaceReviewTreeDirectory{directory}
	terminal := directory
	for len(terminal.Files) == 0 && len(terminal.Directories) == 1 {
		children := workspaceReviewSortedDirectories(terminal)
		terminal = children[0]
		chain = append(chain, terminal)
	}
	chainKeys := make([]string, len(chain))
	segments := make([]string, len(chain))
	expanded := true
	for index, entry := range chain {
		chainKeys[index] = entry.Path
		segments[index] = entry.Name
		if !state.ExpandedDirectories[entry.Path] {
			expanded = false
		}
	}
	path := terminal.Path
	name := strings.Join(segments, "/")
	button := ui.ButtonBase(c.Key(key+"-dir-"+path)).FillWidth().Row().Height(28).Padding(3, 7+float32(depth)*12).Gap(7).Radius(6).
		Label(name).Tooltip(strings.Join(chainKeys, "/"))
	if button.Hovered() {
		button.Background(p.surface)
	}
	button.Children(func() {
		iconName := "arrow"
		if expanded {
			iconName = "chevron"
		}
		icon(c, iconName, 12, p.muted)
		icon(c, "folder", 15, p.blue)
		label(c, name, 10, p.text).Grow(1).MinWidth(0).SingleLine().Tooltip(terminal.Path)
	})
	if button.Clicked() {
		for _, entry := range chain {
			state.ExpandedDirectories[entry.Path] = !expanded
		}
	}
	if !expanded {
		return
	}
	for _, file := range terminal.Files {
		s.renderWorkspaceReviewFileRow(c, p, state, key, file, depth+1)
	}
	for _, child := range workspaceReviewSortedDirectories(terminal) {
		s.renderWorkspaceReviewDirectory(c, p, state, key, child, depth+1)
	}
}

func (s *app) renderWorkspaceReviewFileRow(c *ui.Context, p palette, state *workspaceHistoryState, key string, file workspace.CompareChange, depth int) {
	selected := file.Path == state.SelectedPath && file.OldPath == state.SelectedOldPath && file.Kind == string(state.SelectedKind)
	display := workspaceReviewBaseName(file.Path)
	tooltip := file.Path
	if file.OldPath != "" && file.OldPath != file.Path {
		display = workspaceReviewBaseName(file.OldPath) + " → " + display
		tooltip = file.OldPath + " → " + file.Path
	}
	kind := workspaceGitChangeKind(file.Kind)
	tooltip = workspaceChangeKindLabel(kind) + "：" + tooltip
	image, color := workspaceReviewFileStyle(kind, p)
	button := ui.ButtonBase(c.Key(key+"-file-"+file.Kind+"-"+file.OldPath+"-"+file.Path)).FillWidth().Row().Padding(6, 7+float32(depth)*12).Gap(7).Radius(7).Label(tooltip).Tooltip(tooltip)
	box, viewport := button.Bounds(), state.PrefetchViewport
	if len(state.PrefetchFrameVisible) < 16 && box.W > 0 && box.H > 0 && viewport.W > 0 && viewport.H > 0 &&
		box.X < viewport.X+viewport.W && box.X+box.W > viewport.X && box.Y < viewport.Y+viewport.H && box.Y+box.H > viewport.Y {
		state.PrefetchFrameVisible = append(state.PrefetchFrameVisible, file)
	}
	if selected {
		button.Background(p.selected)
	} else if button.Hovered() {
		button.Background(p.surface)
	}
	button.Children(func() {
		icon(c, image, 13, color)
		label(c, display, 10, color).Grow(1).MinWidth(0).SingleLine().Tooltip(tooltip)
	})
	if button.Clicked() && !selected {
		if state.FileSelected != nil {
			state.FileSelected(file)
		} else {
			s.selectWorkspaceReviewFile(state, file)
		}
	}
}

func workspaceReviewFileStyle(kind domain.WorkspaceGitFileChangeKind, p palette) (string, ui.Color) {
	switch kind {
	case domain.WorkspaceGitAdded, domain.WorkspaceGitUntracked:
		return "file", p.green
	case domain.WorkspaceGitModified:
		return "file", p.blue
	case domain.WorkspaceGitDeleted:
		return "file", p.danger
	case domain.WorkspaceGitRenamed, domain.WorkspaceGitCopied:
		return "file", p.purple
	case domain.WorkspaceGitTypeChanged:
		return "file", p.amber
	case domain.WorkspaceGitConflicted:
		return "close", p.danger
	default:
		return "file", p.text
	}
}

func workspaceGitChangeKind(kind string) domain.WorkspaceGitFileChangeKind {
	return domain.WorkspaceGitFileChangeKind(kind)
}

func (s *app) renderWorkspaceReviewPreview(c *ui.Context, p palette, state *workspaceHistoryState, key string) {
	ui.Row(c).FillWidth().Height(38).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
		if state.SelectedPath == "" {
			label(c, "选择文件查看变更", 12, p.muted).Grow(1)
			return
		}
		path := state.SelectedPath
		if state.SelectedOldPath != "" && state.SelectedOldPath != state.SelectedPath {
			path = state.SelectedOldPath + " → " + state.SelectedPath
		}
		label(c, path, 12, p.text).Grow(1).MinWidth(0).FontWeight(600).SingleLine().Tooltip(path)
		label(c, workspaceChangeKindLabel(state.SelectedKind), 10, p.muted).NoWrap()
	})
	ui.Divider(c)
	if state.PreviewLoading {
		ui.Column(c).Grow(1).FillWidth().Center().Children(func() { label(c, "正在读取 Diff…", 12, p.muted) })
		return
	}
	if state.Preview != nil {
		preview, started := state.Preview, state.PreviewStartedAt
		ui.Column(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
			if bounds.W > 0 && bounds.H > 0 && !state.PreviewFirstFrame && !started.IsZero() && state.Preview == preview && state.PreviewStartedAt.Equal(started) {
				// DrawOver runs after the preview's children have produced their
				// paint commands. GPU presentation remains owned by the platform.
				slog.Default().Debug("workspace git review", "phase", "first_paint", "duration_ms", float64(time.Since(started).Microseconds())/1000)
				state.PreviewFirstFrame = true
			}
		}).Children(func() { s.renderWorkspaceFilePreview(c, p, state, preview) })
		return
	}
	if state.SelectedPath != "" && state.Error == "" {
		ui.Column(c).Grow(1).FillWidth().Center().Children(func() { label(c, "等待差异结果…", 11, p.muted) })
		return
	}
	ui.Column(c).Grow(1).FillWidth().Center().Children(func() { label(c, "选择左侧文件查看变更", 12, p.muted) })
}
