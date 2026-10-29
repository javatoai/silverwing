package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/workspace"
)

func (s *app) renderTaskCreationBranchReuseDialog(c *ui.Context, p palette, state *taskCreationState) {
	if state == nil || state.phase != "confirm" || state.preview == nil || len(state.preview.conflicts) == 0 {
		return
	}
	preview := state.preview
	conflicts := preview.conflicts
	token := state.token
	open := true
	dialog := ui.DialogBase(c.Key("task-create-branch-reuse-confirmation-"+token), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .4))
		width, height := c.Size()
		dialogWidth := min(float32(760), max(float32(320), width-32))
		dialogHeight := min(float32(720), max(float32(300), height-32))
		panel.Width(dialogWidth).Height(dialogHeight).MaxHeight(dialogHeight).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(16).Label("确认复用已有分支")
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					label(c, "确认复用已有分支", 17, p.text).FontWeight(600)
					label(c, fmtTaskCreationReuseCount(len(conflicts)), 11, p.muted)
				})
				pill(c, fmt.Sprintf("%d 项", len(conflicts)), p)
			})
			label(c, "创建后会在新任务工作区中复用这些本地或远程分支；提交时仍会再次校验分支状态。", 11, p.muted).FillWidth().Wrap().Shrink(0)
			ui.Scroll(c.Key("task-create-branch-reuse-conflicts-" + token)).FillWidth().Grow(1).MinHeight(0).Gap(7).Label("分支复用冲突列表").Children(func() {
				for index, conflict := range conflicts {
					conflict := conflict
					ui.Row(c.Key(fmt.Sprintf("task-create-branch-reuse-conflict-%s-%d", token, index))).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Children(func() { renderBranchReuseConflictSummary(c, p, conflict) })
						if action(c, fmt.Sprintf("task-create-conflict-locate-%s-%d", token, index), "search", "定位配置", p, false) {
							if s.taskUnit.state.taskCreation == state && state.phase == "confirm" && state.preview == preview {
								open = false
								s.returnTaskCreationToEditing(state)
								s.focusTaskCreationConflict(conflict)
							}
						}
					})
				}
			})
			locked, occupied := taskCreationBranchReuseGates(conflicts)
			if occupied {
				s.settingsChoice(c, p, "task-create-shared-ack", "我了解已检出的分支会在多个 Worktree 间共享", preview.sharedAck, func() {
					if s.taskUnit.state.taskCreation == state && state.phase == "confirm" && state.preview == preview {
						preview.sharedAck = !preview.sharedAck
					}
				})
			}
			if locked {
				label(c, "锁定的 Worktree 不能自动清理或强制复用；请先解锁并重新预检。", 11, p.amber).FillWidth().Wrap().Shrink(0)
			}
			ui.Divider(c).Shrink(0)
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				back := ui.ButtonBase(c.Key("task-create-reuse-back-"+token)).Row().Height(34).Padding(0, 16).Radius(18).Label("返回修改").AutoFocus()
				back.Border(1, p.line).Children(func() { label(c, "返回修改", 12, p.text) })
				if back.Clicked() {
					if s.taskUnit.state.taskCreation == state && state.phase == "confirm" && state.preview == preview {
						open = false
						s.returnTaskCreationToEditing(state)
					}
				}
				confirm := "确认复用并创建"
				button := ui.ButtonBase(c.Key("task-create-reuse-confirm-"+token)).Row().Height(34).Padding(0, 16).Radius(18).Label(confirm)
				button.Background(p.blue).Disabled(locked || occupied && !preview.sharedAck || s.busy)
				button.Children(func() { label(c, confirm, 12, ui.Hex("#FFFFFF")) })
				if button.Clicked() {
					if s.taskUnit.state.taskCreation == state && state.phase == "confirm" && state.preview == preview {
						open = false
						keys := make([]workspace.BranchReuseKey, 0, len(conflicts))
						for _, conflict := range conflicts {
							keys = append(keys, conflict.Key)
						}
						s.submitTaskCreation(keys)
					}
				}
			})
		})
	})
	if dialog.Dismissed() && s.taskUnit.state.taskCreation == state && state.phase == "confirm" && state.preview == preview {
		s.returnTaskCreationToEditing(state)
	}
}

func fmtTaskCreationReuseCount(count int) string {
	if count == 1 {
		return "有 1 个模块的目标分支已存在。"
	}
	return fmt.Sprintf("有 %d 个模块的目标分支已存在。", count)
}

func taskCreationBranchReuseGates(conflicts []workspace.BranchReuseConflict) (locked, occupied bool) {
	for _, conflict := range conflicts {
		locked = locked || len(conflict.LockedWorktreePaths) > 0
		occupied = occupied || conflict.RequiresForceAttach
	}
	return locked, occupied
}

func (s *app) returnTaskCreationToEditing(state *taskCreationState) {
	if state == nil || s.taskUnit.state.taskCreation != state || state.phase != "confirm" {
		return
	}
	state.preview = nil
	state.phase = "editing"
	state.inlineError = ""
}
