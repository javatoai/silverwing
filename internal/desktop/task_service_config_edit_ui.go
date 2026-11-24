package desktop

import (
	"slices"
	"strings"
	"time"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (s *app) renderTaskServiceConfigEdit(c *ui.Context, p palette, v *taskServicesView, dialog *taskServiceDialog) {
	edit, row := &dialog.edit, dialog.row
	key := taskServiceConfigEditKey(v, row)
	submitting := s.taskServicesUnit.configSubmissions[key]

	locked := submitting || edit.busy()
	if len(edit.files) > 1 {
		ui.Row(c).FillWidth().Shrink(0).Children(func() {
			selected := firstNonEmpty(edit.pendingRead.file, edit.file, "选择配置文件")
			if ui.Select(c.Key("config-edit-file"), &selected, edit.files).
				Grow(1).MinWidth(90).MaxWidth(350).Height(32).Disabled(locked).
				Label("选择配置文件").Tooltip(selected).Changed() {
				s.chooseTaskServiceConfigFile(v, dialog, selected)
			}
		})
	}
	if edit.diff != nil {
		ui.Column(c.Key("config-edit-compare")).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Border(1, p.line).Radius(8).Clip().Children(func() {
			ui.Row(c).FillWidth().Height(30).Shrink(0).Background(p.soft).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Grow(1).MinWidth(0).Padding(0, 10).Children(func() {
					label(c, "原配置（只读）", 12, p.muted).SingleLine()
				})
				ui.Row(c).Grow(1).MinWidth(0).Padding(0, 10).Justify(ui.End).Children(func() {
					label(c, "修改后配置", 12, p.text).SingleLine()
				})
			})
			edit.diff.Editable = !submitting && (edit.pendingRead.file == "" || edit.pendingRead.keepDraft)
			edit.diff.Theme = textEditorTheme(p)
			editor.DiffView(c.Key(edit.file), edit.diff).Grow(1).MinHeight(0).FillWidth()
		})
	} else {
		ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			switch {
			case edit.listLoading:
				label(c, "正在读取配置文件列表…", 12, p.muted)
			case edit.pendingRead.file != "":
				label(c, "正在读取配置…", 12, p.muted)
			case edit.catalog.SnapshotID != "" && len(edit.files) == 0 && edit.readFailure == nil:
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					label(c, "当前 ConfigMap 没有可编辑的配置文件。", 12, p.muted).Wrap()
					if productionButton(c, "config-edit-read-empty", "重新读取文件列表", p, false, locked) {
						s.requestTaskServiceConfigFiles(v, dialog)
					}
				})
			}
		})
	}
	s.syncTaskServiceConfigDraft(v, dialog)
	pending := !edit.pendingApprovalAt.IsZero()
	if pending {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Shrink(0).Wrap().Children(func() {
			label(c, "此变更的审批提交结果待确认。", 12, p.amber).Wrap()
			if productionButton(c, "config-check-approvals", "查看配置审批", p, false, locked) {
				s.openTaskServiceApprovalsAt(v, row, 0, edit.pendingApprovalAt)
			}
		})
	}
	failed := edit.readFailure
	failedReload := failed != nil && failed.source.keepDraft
	if failed != nil {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Shrink(0).Wrap().Children(func() {
			message, retryText := "读取配置文件列表失败。", "重试读取文件列表"
			if failed.source.file != "" {
				message, retryText = "读取 "+failed.source.file+" 失败。", "重试读取配置"
			}
			label(c, message+failed.message, 12, p.danger).Wrap()
			if productionButton(c, "config-edit-retry-read", retryText, p, false, locked) {
				s.retryTaskServiceConfigRead(v, dialog, failed.source)
			}
		})
	}
	if edit.failure != "" && !failedReload {
		label(c, edit.failure, 12, p.danger).Wrap().Shrink(0)
	} else if edit.notice != "" {
		label(c, edit.notice, 12, p.muted).Wrap().Shrink(0)
	}
	ui.Divider(c).Shrink(0)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Shrink(0).Wrap().Children(func() {
		status := ""
		switch {
		case submitting:
			status = "正在提交配置审批…"
		case edit.previewing:
			status = "正在校验配置变更…"
		case edit.diff != nil && edit.pendingRead.file != "":
			status = "正在读取配置…"
		case edit.diff != nil && edit.listLoading:
			status = "正在读取配置文件列表…"
		case !pending && !edit.noChange && edit.draftDirty():
			status = "有未提交的修改"
		}
		if status != "" {
			label(c, status, 12, p.muted)
		}
		ui.Spacer(c)
		if edit.stale && edit.diff != nil && !failedReload && productionButton(c, "config-edit-reload", "重新读取原配置", p, false, locked) {
			s.requestTaskServiceConfigSource(v, dialog, edit.file, true)
		}
		canSubmit := !locked && !pending && !edit.stale && !edit.noChange && edit.sourceToken != "" && edit.draftDirty()
		if productionButton(c, "config-edit-submit", "提交配置审批", p, true, !canSubmit) && canSubmit {
			if edit.previewCurrent(edit.preview, time.Now()) {
				s.submitTaskServiceConfig(v, dialog, edit.preview)
			} else {
				s.requestTaskServiceConfigPreview(v, dialog)
			}
		}
	})
}

func (s *app) retryTaskServiceConfigRead(v *taskServicesView, dialog *taskServiceDialog, failed taskServiceConfigSourceRead) {
	switch {
	case failed.file == "":
		s.requestTaskServiceConfigFiles(v, dialog)
	case failed.keepDraft:
		s.requestTaskServiceConfigSource(v, dialog, failed.file, true)
	default:
		s.chooseTaskServiceConfigFile(v, dialog, failed.file)
	}
}

func (s *app) chooseTaskServiceConfigFile(v *taskServicesView, dialog *taskServiceDialog, file string) {
	if !s.taskServiceConfigEditCurrent(v, dialog) || s.taskServiceConfigEditBlocked(v, dialog) {
		return
	}
	edit := &dialog.edit
	if edit.busy() || file == edit.file || !slices.Contains(edit.files, file) {
		return
	}
	choose := func() { s.requestTaskServiceConfigSource(v, dialog, file, false) }
	if edit.draftDirty() {
		s.confirmTaskServiceConfigDiscard(v, dialog, "切换配置文件", "切换后将放弃当前文件的未提交修改。", "放弃并切换", choose)
		return
	}
	choose()
}

func (s *app) requestTaskServiceConfigEditClose(v *taskServicesView, dialog *taskServiceDialog) {
	if !s.taskServiceConfigEditCurrent(v, dialog) {
		return
	}
	closeEdit := func() { s.closeTaskServiceDialog(v, dialog) }
	if dialog.edit.draftDirty() && !s.taskServiceConfigEditBlocked(v, dialog) {
		s.confirmTaskServiceConfigDiscard(v, dialog, "关闭配置编辑", "关闭后将放弃当前文件的未提交修改。", "放弃并关闭", closeEdit)
		return
	}
	closeEdit()
}

func (s *app) confirmTaskServiceConfigDiscard(v *taskServicesView, dialog *taskServiceDialog, title, message, accept string, apply func()) {
	buttons := []confirmationDialogButton{{label: "继续编辑"}, {label: accept, primary: true}}
	if s.showConfirmationDialog(title, message, "", buttons, 0, 0, func(choice int) {
		if choice == 1 {
			apply()
		}
	}) {
		s.confirmationDialog.Current = func() bool {
			return s.taskServiceConfigEditCurrent(v, dialog)
		}
	}
}

func taskServiceConfigEditorBuffer(file, body string) *editor.Buffer {
	buffer := editor.NewBufferPreservingLineEndings(body)
	buffer.SetLanguage(textEditorBackgroundLanguage(textEditorLanguage(file)))
	return buffer
}

func newTaskServiceConfigDiff(file, body, bom string) *editor.DiffState {
	draft := taskServiceConfigEditorBuffer(file, body)
	draft.SetMaxSourceBytes(production.GenbuConfigMaxBytes - len(bom))
	return &editor.DiffState{
		Old: taskServiceConfigEditorBuffer(file, body), New: draft,
		Layout: editor.DiffSplit, Editable: true, ExpandUnchanged: true,
		Inline: editor.DiffInlineWordAlt, Indicators: editor.DiffClassic, SyncHorizontal: true,
		Font: ui.Font{Family: "monospace", Size: textEditorFontSize}, LineHeight: textEditorLineHeight,
	}
}

func splitTaskServiceConfigBOM(source string) (body, bom string) {
	const utf8BOM = "\ufeff"
	if body, ok := strings.CutPrefix(source, utf8BOM); ok {
		return body, utf8BOM
	}
	return source, ""
}
