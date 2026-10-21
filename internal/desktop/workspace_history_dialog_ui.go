package desktop

import (
	"strings"
	"time"

	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func (s *app) workspaceCommitHistoryDialogVisible(state *workspaceHistoryState) bool {
	if state == nil || state.Mode != "history" || !state.Open || (s.page != "tasks" && s.page != "archive") {
		return false
	}
	t := s.currentTask()
	if t.id != state.TaskID || t.path != state.TaskPath || t.createdAt != state.TaskCreatedAt || (s.page == "archive") != t.archived {
		return false
	}
	_, exists := workspaceReviewWorkspace(t, state)
	return exists
}

func (s *app) renderWorkspaceCommitHistoryDialog(c *ui.Context, p palette, state *workspaceHistoryState) {
	if !s.workspaceCommitHistoryDialogVisible(state) {
		if state != nil && state == s.workspaceHistory && state.Mode == "history" {
			s.dismissWorkspaceHistory()
		}
		return
	}
	width, height := c.Size()
	maxHeight := max(float32(1), min(float32(720), height-40))
	dialog := ui.DialogBase(c.Key("workspace-commit-history-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		panel.Width(max(float32(420), min(float32(920), width-40))).Height(maxHeight).
			Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(16)
		panel.OnShortcut(0, ui.KeyEscape, func() { s.closeWorkspaceCommitHistoryDialog(state) })
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					label(c, "提交历史", 17, p.text).FontWeight(600)
					label(c, "主分支 "+firstNonEmpty(state.BaselineDisplayRef, state.BaselineRef)+" 之后的提交", 11, p.muted).SingleLine().Tooltip(state.BaselineDisplayRef)
				})
				if iconButtonRunning(c, "workspace-history-dialog-refresh", "refresh", "重新读取提交历史", p, state.HistoryLoading, state.HistoryLoading).Clicked() {
					s.cancelWorkspaceHistoryRead()
					state.Commits = nil
					state.CommitsLoaded = false
					s.readWorkspaceCommitHistoryDialog(state)
				}
				if iconButton(c, "workspace-history-dialog-close", "close", "关闭", p).Clicked() {
					s.closeWorkspaceCommitHistoryDialog(state)
				}
			})
			ui.Divider(c)
			if state.Error != "" {
				ui.Box(c).FillWidth().Shrink(0).Padding(9, 11).Radius(8).Background(p.amberBg).Children(func() {
					label(c, state.Error, 12, p.danger).Wrap().Selectable()
				})
				ui.Row(c).FillWidth().Justify(ui.End).Shrink(0).Children(func() {
					if action(c, "workspace-history-dialog-retry", "refresh", "重试", p, state.HistoryLoading) {
						s.readWorkspaceCommitHistoryDialog(state)
					}
				})
				return
			}
			if state.HistoryLoading && len(state.Commits) == 0 {
				ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
					label(c, "获取中…", 12, p.muted)
				})
				return
			}
			if len(state.Commits) == 0 {
				ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
					label(c, "主分支之后没有提交。", 12, p.muted)
				})
				return
			}
			ui.Scroll(c.Key("workspace-commit-history-list")).Grow(1).MinHeight(0).Padding(2, 4).Gap(2).Children(func() {
				for index, commit := range state.Commits {
					s.renderWorkspaceCommitHistoryRow(c, p, state, index, commit)
				}
			})
		})
	})
	if dialog.Dismissed() || !state.Open {
		s.closeWorkspaceCommitHistoryDialog(state)
	}
}

func (s *app) renderWorkspaceCommitHistoryRow(c *ui.Context, p palette, state *workspaceHistoryState, index int, commit domain.WorkspaceGitCommit) {
	key := "workspace-commit-history-" + commit.FullHash
	if commit.FullHash == "" {
		key += string(rune(index + 1))
	}
	message := strings.TrimSpace(commit.Message)
	expanded := state.ExpandedCommits[commit.FullHash]
	shownMessage, long := workspaceCommitHistoryMessage(message, expanded)
	ui.Column(c).FillWidth().Shrink(0).Padding(9, 10).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					label(c, commit.ShortHash, 11, p.blue).FontWeight(600).NoWrap().Selectable()
					label(c, workspaceCommitHistoryLocalTime(commit.CommittedAt), 10, p.muted).NoWrap()
				})
				label(c, shownMessage, 12, p.text).FillWidth().Wrap().Tooltip(message).Selectable()
				if long {
					toggleLabel := "展开提交信息"
					if expanded {
						toggleLabel = "收起"
					}
					if ui.Button(c.Key(key+"-expand"), toggleLabel).AlignSelf(ui.Start).Clicked() {
						if state.ExpandedCommits == nil {
							state.ExpandedCommits = make(map[string]bool)
						}
						state.ExpandedCommits[commit.FullHash] = !expanded
					}
				}
			})
			if ui.Button(c.Key(key+"-diff"), "查看差异").Tooltip("在全屏左右 Diff 中查看此提交").Clicked() {
				s.openWorkspaceCommitDiff(state, commit)
			}
		})
	})
	if index < len(state.Commits)-1 {
		ui.Divider(c)
	}
}

func workspaceCommitHistoryMessage(message string, expanded bool) (shown string, long bool) {
	message = strings.TrimSpace(message)
	firstLine := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	long = strings.Contains(message, "\n") || len([]rune(message)) > 150
	if expanded {
		return message, long
	}
	runes := []rune(firstLine)
	if len(runes) > 150 {
		firstLine = string(runes[:147]) + "…"
	}
	return firstLine, long
}

func workspaceCommitHistoryLocalTime(value time.Time) string {
	return value.Local().Format("2006-01-02 15:04:05")
}

func (s *app) readWorkspaceCommitHistoryDialog(state *workspaceHistoryState) {
	if state == nil || state != s.workspaceHistory || !s.workspaceCommitHistoryDialogVisible(state) {
		return
	}
	if strings.TrimSpace(state.BaselineRef) == "" {
		state.Error = "请先在仓库配置中设置此模块的主分支。"
		state.Loading, state.HistoryLoading = false, false
		return
	}
	state.Error = ""
	state.Loading, state.HistoryLoading = true, true
	state.AllHistory = false
	s.act("workspace.history", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"baselineRef": state.BaselineRef,
		"allHistory":  false,
	})
}

func (s *app) closeWorkspaceCommitHistoryDialog(state *workspaceHistoryState) {
	if state == nil || state != s.workspaceHistory || state.Mode != "history" {
		return
	}
	t := s.currentTask()
	hasFocus, focus := state.HasHistoryFocus, state.HistoryFocus
	s.dismissWorkspaceHistory()
	if hasFocus {
		s.workspaceReviewFocusReturn = &workspaceReviewFocusReturn{
			Handle: focus, TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt, Page: s.page, deferOneFrame: true,
		}
	}
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Invalidate()
	}
}

func (s *app) openWorkspaceCommitDiff(state *workspaceHistoryState, commit domain.WorkspaceGitCommit) {
	if !s.workspaceCommitHistoryDialogVisible(state) || strings.TrimSpace(commit.FullHash) == "" {
		return
	}
	t := s.currentTask()
	item, exists := workspaceReviewWorkspace(t, state)
	if !exists {
		return
	}
	commits := append([]domain.WorkspaceGitCommit(nil), state.Commits...)
	var focus *ui.Handle
	if state.HasHistoryFocus {
		focus = &state.HistoryFocus
	}
	s.openWorkspaceReviewWithCommits(t, item, workspaceReviewCommits, nil, focus, commits, []string{commit.FullHash})
}
