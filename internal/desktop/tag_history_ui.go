package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func (s *app) renderTagHistory(c *ui.Context, p palette) {
	s.renderTaskTagHistory(c, p, s.currentTask())
}

func (s *app) replicaTags(c *ui.Context, p palette) {
	s.renderTaskTagHistory(c, p, s.currentTask())
}

func (s *app) renderTagHistoryPageMenu(c *ui.Context, p palette, t task) {
	if t.id == "" {
		return
	}
	state := s.ensureTaskTagHistoryView(t)
	records := s.taskTagHistoryRecords(t.id)
	s.overflowMenu(c, p, tagHistoryMenuKey(state, "page"), "Tag 历史更多操作", s.busy, func(menu *overflowMenuBuilder) {
		if menu.Item("refresh", "刷新").Icon("refresh").Chosen() {
			s.requestTagHistoryRefresh()
		}
		if menu.Item("select-groups", "选择批次").Icon("check").Disabled(state.Managing || len(records) == 0).Chosen() {
			state.Managing = true
			state.Selected = map[string]bool{}
		}
		menu.Separator()
		if menu.Item("clear-history", "清空该任务历史").Icon("delete").Dangerous().Disabled(len(records) == 0).Chosen() {
			s.beginTagHistoryDelete(true)
		}
	})
}

func (s *app) renderTaskTagHistory(c *ui.Context, p palette, t task) {
	if t.id == "" {
		emptyPanel(c, p, "没有研发任务", "请先选择需求对应的研发任务。")
		return
	}
	state := s.ensureTaskTagHistoryView(t)
	records := s.taskTagHistoryRecords(t.id)
	groups := filterTagHistory(records, "", false)
	ui.Column(c).Grow(1).MinHeight(0).Padding(8, 28, 28, 8).Gap(10).Children(func() {
		if state.Managing {
			allVisibleSelected := len(groups) > 0
			for _, group := range groups {
				if !state.Selected[group.Item.GroupID] {
					allVisibleSelected = false
				}
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Checkbox(c.Key("tag-history-select-visible"), &allVisibleSelected, "全选").Shrink(0).
					Disabled(s.busy || len(groups) == 0).OnChange(func() {
					if s.tagHistory != state {
						return
					}
					for _, group := range groups {
						if allVisibleSelected {
							state.Selected[group.Item.GroupID] = true
						} else {
							delete(state.Selected, group.Item.GroupID)
						}
					}
				})
				label(c, fmt.Sprintf("已选 %d 批", len(state.Selected)), 11, p.muted).Grow(1).Wrap()
				ui.Row(c).Disabled(s.busy || len(state.Selected) == 0).Children(func() {
					if action(c, "tag-history-delete-selected", "delete", "删除所选", p, false) {
						s.beginTagHistoryDelete(false)
					}
				})
				ui.Row(c).Disabled(s.busy).Children(func() {
					if action(c, "tag-history-management-done", "check", "完成", p, false) {
						state.Managing = false
						state.Selected = map[string]bool{}
						state.DeleteOpen = false
					}
				})
			})
		}
		if message := strings.TrimSpace(*s.input(tagGenbuTaskErrorKey(t.id), "")); message != "" {
			label(c, "Genbu 刷新失败："+message, 12, tagStatusErrorColor(s.dark)).Wrap()
		}
		if state.Error != "" {
			label(c, state.Error, 12, tagStatusErrorColor(s.dark)).Wrap()
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Gap(10).Children(func() {
			pending := s.renderPendingTagSubmissions(c, p)
			if len(groups) == 0 && !pending {
				emptyPanel(c, p, "没有构建记录", "构建 Tag 后可在这里查看构建和 UAT 结果。")
			}
			for _, group := range groups {
				s.renderTagHistoryGroup(c.Key("tag-group-"+group.Item.GroupID), p, state, group)
			}
		})
	})
	s.renderTagHistoryDeleteDialog(c, p, state)
}

func (s *app) renderTagHistoryGroup(c *ui.Context, p palette, state *tagHistoryViewState, group tagHistoryGroup) {
	card := ui.Column(c).FillWidth().Padding(8, 12).Gap(4).Background(p.surface).Border(1, p.line).Radius(12).Label("Tag 批次 · " + group.Item.GroupID)
	if s.consumeTagBatchFocus(group.Item.GroupID) {
		card.ScrollIntoView()
	}
	card.Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).Wrap().Justify(ui.End).AlignItems(ui.Center).Label("Tag 批次标题 · " + group.Item.GroupID).Children(func() {
			if state.Managing {
				selected := state.Selected[group.Item.GroupID]
				ui.Checkbox(c.Key("group-select"), &selected, "").Shrink(0).Disabled(s.busy).Label("选择批次 " + group.Item.GroupID).
					Tooltip(fmt.Sprintf("选择整个批次（%d 条记录）", len(group.Item.Operations))).OnChange(func() {
					if s.tagHistory == state {
						if selected {
							state.Selected[group.Item.GroupID] = true
						} else {
							delete(state.Selected, group.Item.GroupID)
						}
					}
				})
			}
			ui.Column(c).Grow(1).Basis(150).MinWidth(0).Gap(2).Children(func() {
				title := firstNonEmpty(group.Item.FolderName, s.taskByID(state.TaskID).title, "Tag 构建")
				label(c, title, 12, p.text).FontWeight(600).FillWidth().MinWidth(0).SingleLine().Tooltip(title).Label("Tag 批次名称 · " + group.Item.GroupID)
				label(c, tagHistoryBuildTimeLabel(group.Item.CreatedAt), 10, p.muted).SingleLine().Tooltip("发起构建时间").Label("Tag 构建时间 · " + group.Item.GroupID)
			})
			copy := ui.ButtonBase(c.Key("copy-announcement")).Row().Shrink(0).Height(28).Padding(0, 8).Gap(5).AlignItems(ui.Center).
				Radius(8).Border(1, p.line).Label("复制发版通知 · " + group.Item.GroupID).Tooltip("复制整个批次的发版通知").Disabled(s.busy)
			if copy.Hovered() && !s.busy {
				copy.Background(p.soft)
			}
			copy.Children(func() {
				icon(c, "copy", 14, p.muted)
				label(c, "复制发版通知", 11, p.muted).NoWrap()
			})
			if copy.Clicked() {
				text := s.tagHistoryAnnouncement(group)
				s.copyTextToClipboard(c.Services(), text, s.tagHistoryAnnouncementCopiedMessage(group, text))
			}
		})
		if progress := s.tagBatchProgress(group.Item.GroupID); progress.Total > 0 {
			text := fmt.Sprintf("本次操作 %d 项 · 执行中 %d · 完成 %d · 失败 %d", progress.Total, progress.Running, progress.Completed, progress.Failed)
			label(c, text, 11, p.muted).Wrap()
		}
		for i, record := range group.Visible {
			if i > 0 {
				ui.Divider(c)
			}
			s.renderTagHistoryRecord(c.Key("tag-record-"+record.ID), p, state, record)
		}
	})
}

func (s *app) renderTagHistoryRecord(c *ui.Context, p palette, state *tagHistoryViewState, record application.TagRecord) {
	genbuEligible := domain.TagGenbuProbeEligible(domain.TagOperationState(record.State), record.Tag)
	item, _ := s.tagRecordJobItem(record)
	recordActive := item.Busy || item.Queued
	phase := s.tagRecordPhase(record)
	if s.tagHistoryRecordCommandQueued(record) {
		if !recordActive {
			phase = "等待执行"
		}
		recordActive = true
	}
	ui.Column(c).FillWidth().MinWidth(0).Padding(5, 0).Gap(5).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(12).Wrap().Justify(ui.End).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).Basis(230).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					name := record.ServiceName
					if name == "" {
						name = record.ServiceID
					}
					text := record.Tag
					if text == "" {
						text = "尚未生成 Tag"
					}
					ui.Row(c).FillWidth().MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
						label(c, name, 13, p.text).FontWeight(600).Shrink(1).MinWidth(0).SingleLine().Tooltip(name).Selectable()
						label(c, text, 13, p.blue).FontWeight(600).Shrink(1).MinWidth(0).SingleLine().Tooltip(text).Selectable()
					})
					branches := record.SourceBranch + " → " + record.TargetBranch
					ui.Row(c).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Center).Label("合并分支 · " + record.ID).Tooltip(branches).Children(func() {
						label(c, record.SourceBranch, 11, p.muted).Shrink(1).MinWidth(0).SingleLine().Label("来源分支 · " + record.ID)
						label(c, "→", 11, p.muted).Shrink(0).NoWrap()
						label(c, record.TargetBranch, 11, p.muted).Shrink(1).MinWidth(0).SingleLine().Label("目标分支 · " + record.ID)
					})
				})
			})
			if genbuEligible {
				ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
					renderTagGenbuStages(c, p, s.dark, record)
				})
			}
		})
		// SUCCESS is the local create/push operation, not the remote build.
		// Its normal detail repeats the service and Tag already shown above.
		compactFailure := tagWorkspaceCheckEligible(record)
		if phase != "" {
			color := p.muted
			if recordActive {
				color = p.blue
			}
			label(c, phase, 11, color).Wrap()
		} else if record.State != "SUCCESS" && !compactFailure && !recordActive {
			stateLabel := tagHistoryStateLabel(record.State)
			if record.State == "FAILED" {
				stateLabel = "Tag 创建失败"
			}
			if stateLabel == "" {
				stateLabel = "Tag 状态未知"
			}
			label(c, stateLabel, 11, p.muted).Wrap()
		}
		detail := strings.TrimSpace(record.Detail)
		if record.State == "SUCCESS" {
			parts := strings.SplitN(strings.ReplaceAll(detail, "：", ":"), ":", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == record.ServiceName && strings.TrimSpace(parts[1]) == record.Tag {
				detail = ""
			}
		}
		if detail != "" && !compactFailure && !recordActive {
			label(c, detail, 12, p.muted).Wrap().LineHeight(1.5)
		}
		if compactFailure && !recordActive {
			s.renderTagConflictCard(c, p, record)
		}
		if genbuEligible {
			renderTagGenbuNotes(c, p, s.dark, record, s.tagGenbuError(record.ID))
		}
		retryLabel, retryExplanation := tagHistoryRetryPresentation(record)
		if retryExplanation != "" && tagHistoryRetryable(record) && !compactFailure && !recordActive {
			label(c, retryExplanation, 12, tagStatusErrorColor(s.dark)).Wrap().LineHeight(1.5)
		}
		if !compactFailure && tagHistoryRetryable(record) && !recordActive {
			ui.Row(c).Gap(8).Children(func() {
				if tagRecordAction(c, p, "tag-retry-"+record.ID, "refresh", retryLabel, s.tagRecordBusy(record)).Clicked() {
					s.queueTagAction(application.Action{Kind: "tag.retry", TaskID: record.TaskID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Payload: s.tagHistoryRecordPayload(record)})
				}
			})
		}
	})
}

// Pending commands have no job item until the backend acknowledges them.
// Match the record itself; repository-level busy only guards duplicate actions.
func (s *app) tagHistoryRecordCommandQueued(record application.TagRecord) bool {
	view := s.currentTagJobView()
	if view == nil || record.TaskID != s.currentTask().id {
		return false
	}
	for _, command := range view.commands {
		action := command.action
		if (action.Kind == "tag.retry" || action.Kind == "tag.workspace-check") &&
			action.TaskID == record.TaskID && action.ServiceID == record.ServiceID && action.ModuleID == record.ModuleID &&
			payloadString(action.Payload, "operationId") == record.ID {
			return true
		}
	}
	return false
}

func tagHistoryBuildTimeLabel(value string) string {
	if parsed, err := domain.ParseSilverWingTime(strings.TrimSpace(value)); err == nil {
		return "构建于 " + parsed.Local().Format("2006-01-02 15:04")
	}
	return "构建时间未知"
}
