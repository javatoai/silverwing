package desktop

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type genbuApprovalsDialogState struct {
	open, listed, detailed     bool
	source                     genbuServiceReadScope
	openedAt                   time.Time
	list                       production.GenbuApprovalPage
	listPage                   int
	detail                     production.GenbuApprovalDetail
	detailID                   int64
	listError, detailError     string
	listRequest, detailRequest *genbuReadRequest
	listScroll, detailScroll   ui.ScrollState
	comparisonFile             string
	comparison                 *editor.DiffState
	metadataOpen               bool
	approvalError              string
}

func (s *app) openTaskServiceApprovals(v *taskServicesView, row *taskServiceRow, approvalID int64) {
	s.openTaskServiceApprovalsAt(v, row, approvalID, time.Time{})
}

func (s *app) openTaskServiceApprovalsAt(v *taskServicesView, row *taskServiceRow, approvalID int64, attemptedAt time.Time) {
	if v == nil || row == nil || row.environment != "uat" {
		return
	}
	source := genbuServiceReadScope{view: v, row: row, epoch: v.epoch}
	if !s.genbuServiceReadCurrent(source) {
		return
	}
	s.closeTaskServiceDialog(v, v.dialog)
	s.closeGenbuReadDialogs()
	d := &genbuApprovalsDialogState{open: true, source: source, openedAt: genbuApprovalRangeEnd(attemptedAt, time.Now())}
	s.genbuApprovals = d
	if approvalID > 0 {
		s.readGenbuApprovalDetail(d, approvalID)
		return
	}
	s.readGenbuApprovals(d, 1)
}

func genbuApprovalRangeEnd(attemptedAt, now time.Time) time.Time {
	if attemptedAt.IsZero() {
		return now
	}
	// Approval creation can cross midnight after a local submit begins.
	followingDay := domain.SilverWingLocalDate(attemptedAt).AddDate(0, 0, 1)
	today := domain.SilverWingLocalDate(now)
	if followingDay.After(today) {
		return today
	}
	return followingDay
}

func (s *app) genbuApprovalsCurrent(d *genbuApprovalsDialogState) bool {
	return d != nil && d.open && s.genbuApprovals == d && s.genbuServiceReadCurrent(d.source) && d.source.row.environment == "uat"
}

func (s *app) readGenbuApprovals(d *genbuApprovalsDialogState, page int) {
	if !s.genbuApprovalsCurrent(d) || d.detailID != 0 {
		return
	}
	d.listError, d.listed = "", false
	d.listPage = page
	d.list = production.GenbuApprovalPage{}
	d.listScroll = ui.ScrollState{}
	request := s.taskServiceAction(d.source.view, d.source.row, "task.services.approvals")
	request.Payload["openedAt"] = d.openedAt
	request.Payload["page"] = page
	s.runGenbuRead(&d.listRequest, func() bool { return s.genbuApprovalsCurrent(d) && d.detailID == 0 }, request, func(result application.ActionResult, err error) {
		data, err := genbuReadData[production.GenbuApprovalPage](result, err)
		if err != nil {
			d.listError = err.Error()
			return
		}
		if slices.ContainsFunc(data.Items, func(item production.GenbuApprovalItem) bool { return item.Environment != d.source.row.environment }) {
			d.listError = "Genbu 审批列表与当前环境不匹配"
			return
		}
		d.listed, d.list = true, data
	})
}

func (s *app) readGenbuApprovalDetail(d *genbuApprovalsDialogState, id int64) {
	if !s.genbuApprovalsCurrent(d) || id <= 0 {
		return
	}
	cancelGenbuRead(&d.listRequest)
	d.metadataOpen = false
	if d.detailID != id {
		d.comparisonFile = ""
	}
	d.detailID, d.detailError, d.detailed = id, "", false
	d.detail, d.comparison, d.approvalError = production.GenbuApprovalDetail{}, nil, ""
	d.detailScroll = ui.ScrollState{}
	request := s.taskServiceAction(d.source.view, d.source.row, "task.services.approval-detail")
	request.Payload["approvalId"] = strconv.FormatInt(id, 10)
	s.runGenbuRead(&d.detailRequest, func() bool { return s.genbuApprovalsCurrent(d) && d.detailID == id }, request, func(result application.ActionResult, err error) {
		data, err := genbuReadData[production.GenbuApprovalDetail](result, err)
		if err != nil {
			d.detailError = err.Error()
			return
		}
		if data.ID != id || data.Environment != d.source.row.environment {
			d.detailError = "Genbu 审批详情与当前审批编号或环境不匹配"
			return
		}
		s.setGenbuApprovalDetail(d, data)
	})
}

func (s *app) setGenbuApprovalDetail(d *genbuApprovalsDialogState, data production.GenbuApprovalDetail) {
	d.detailed, d.detail, d.comparison = true, data, nil
	if genbuApprovalTerminal(data.Status) && s.taskServicesUnit.approvalWrites[data.ID] != genbuApprovalSubmitting {
		delete(s.taskServicesUnit.approvalWrites, data.ID)
	}
}

func (s *app) backToGenbuApprovals(d *genbuApprovalsDialogState) {
	if !s.genbuApprovalsCurrent(d) {
		return
	}
	cancelGenbuRead(&d.detailRequest)
	d.detailID, d.detailError, d.detailed = 0, "", false
	d.detail, d.comparison = production.GenbuApprovalDetail{}, nil
	d.comparisonFile, d.approvalError, d.metadataOpen = "", "", false
	if !d.listed {
		s.readGenbuApprovals(d, max(1, d.listPage))
	}
}

func (s *app) renderGenbuApprovalsDialog(c *ui.Context, p palette, d *genbuApprovalsDialogState) {
	row := d.source.row
	title := "配置审批"
	if d.detailID > 0 {
		title = "审批详情 #" + strconv.FormatInt(d.detailID, 10)
	}
	closeDialog := func() {
		if s.genbuApprovals == d {
			s.closeGenbuReadDialogs()
		}
	}
	genbuReadDialog(c, p, "genbu-approvals", title, d.source.view.dialogScope(row), &d.open, closeDialog, func(panel ui.Element) {
		if !d.metadataOpen {
			panel.OnShortcut(0, ui.KeyEscape, func() {
				if d.metadataOpen {
					return
				}
				closeDialog()
			})
		}
		if d.detailID > 0 {
			s.renderGenbuApprovalDetail(c, p, d)
			return
		}
		s.renderGenbuApprovalList(c, p, d)
	})
	if d.metadataOpen && d.detailed && d.detailID == d.detail.ID && s.genbuApprovalsCurrent(d) {
		renderGenbuApprovalMetadataDialog(c, p, d)
	}
}

func renderGenbuApprovalMetadataDialog(c *ui.Context, p palette, d *genbuApprovalsDialogState) {
	data := d.detail
	title := "审批信息 #" + strconv.FormatInt(data.ID, 10)
	scope := d.source.view.dialogScope(d.source.row)
	closeDialog := func() { d.metadataOpen = false }
	width, height := c.Size()
	ui.DialogBase(c.Key("genbu-approval-metadata-dialog"), &d.metadataOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		panel.Width(max(1, min(560, width-32))).MaxHeight(max(1, height-32)).Padding(18).Gap(10).
			Background(p.surface).Border(1, p.line).Radius(16).Label(title).Description(scope).
			OnShortcut(0, ui.KeyEscape, closeDialog).Children(func() {
			ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					label(c, title, 17, p.text).FontWeight(600)
					label(c, scope, 12, p.muted).SingleLine().Tooltip(scope)
				})
				if iconButton(c, "genbu-approval-metadata-close", "close", "关闭", p).Clicked() {
					closeDialog()
				}
			})
			ui.Divider(c)
			ui.Scroll(c.Key("genbu-approval-metadata-body")).FillWidth().MinHeight(0).Shrink(1).Gap(9).Children(func() {
				renderGenbuApprovalField(c, p, "审批状态", genbuApprovalStatusLabel(data.Status))
				renderGenbuApprovalField(c, p, "执行结果", data.Result)
				ui.Divider(c)
				renderGenbuApprovalMetadata(c, p, data)
			})
		})
	})
}

func (s *app) renderGenbuApprovalList(c *ui.Context, p palette, d *genbuApprovalsDialogState) {
	if d.listRequest != nil {
		label(c, "正在查询审批…", 12, p.muted).Wrap()
	}
	if d.listError != "" {
		label(c, d.listError, 12, p.danger).Wrap().Selectable()
		if ui.Button(c.Key("genbu-approval-retry-page"), "重试本页").AlignSelf(ui.Start).Clicked() {
			s.readGenbuApprovals(d, d.listPage)
		}
	}
	if !d.listed {
		return
	}
	ui.Scroll(c.Key("genbu-approval-list")).TrackScroll(&d.listScroll).Grow(1).FillWidth().MinHeight(0).Gap(0).Children(func() {
		for index, item := range d.list.Items {
			if index > 0 {
				ui.Divider(c)
			}
			s.renderGenbuApprovalRow(c.Key(fmt.Sprintf("%d:%d", index, item.ID)), p, d, item)
		}
	})
	if d.list.Page > 1 || d.list.Total > 0 {
		genbuReadPagination(c.Key("genbu-approval-pages"), p, d.list.Page, d.list.PageSize, d.list.Total, false, false,
			func(page int) { s.readGenbuApprovals(d, page) })
	}
}

func genbuApprovalStatusLabel(status string) string {
	switch strings.ToLower(status) {
	case "pending", "待处理":
		return "待审批"
	case "approved":
		return "已通过"
	case "rejected":
		return "已驳回"
	case "revoked":
		return "已撤销"
	default:
		return firstNonEmpty(status, "未返回")
	}
}

func (s *app) renderGenbuApprovalRow(c *ui.Context, p palette, d *genbuApprovalsDialogState, item production.GenbuApprovalItem) {
	id := "#" + strconv.FormatInt(item.ID, 10)
	ui.Row(c).FillWidth().Padding(9, 6).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				label(c, id, 13, p.text).FontWeight(600).NoWrap()
				label(c, genbuApprovalStatusLabel(item.Status), 12, p.text).NoWrap()
				if item.Result != "" {
					label(c, "执行结果  "+item.Result, 11, p.muted).Wrap()
				}
			})
			if metadata := strings.Join(nonemptyGenbuApprovalFields(item.Creator, item.CreatedAt), " · "); metadata != "" {
				label(c, metadata, 11, p.muted).Wrap()
			}
		})
		if ui.Button(c.Key("details"), "查看详情").Label("查看审批 " + id).Shrink(0).Clicked() {
			s.readGenbuApprovalDetail(d, item.ID)
		}
	})
}

func nonemptyGenbuApprovalFields(fields ...string) []string {
	return slices.DeleteFunc(fields, func(field string) bool { return strings.TrimSpace(field) == "" })
}

func renderGenbuApprovalField(c *ui.Context, p palette, field, value string) {
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
		label(c, field, 12, p.muted).Width(84).Shrink(0).Wrap()
		label(c, firstNonEmpty(value, "未返回"), 12, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
	})
}

func (s *app) renderGenbuApprovalDetail(c *ui.Context, p palette, d *genbuApprovalsDialogState) {
	writeState := s.taskServicesUnit.approvalWrites[d.detailID]
	ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		if ui.Button(c.Key("genbu-approval-back"), "返回审批列表").Clicked() {
			s.backToGenbuApprovals(d)
		}
		if ui.Button(c.Key("genbu-approval-reload"), "刷新详情").Disabled(d.detailRequest != nil || d.detailID == 0 || writeState == genbuApprovalSubmitting).Clicked() {
			s.readGenbuApprovalDetail(d, d.detailID)
		}
		if d.detailed && len(d.detail.Changes) > 0 {
			ui.Spacer(c)
			if ui.Button(c.Key("genbu-approval-metadata"), "审批信息").Clicked() {
				d.metadataOpen = true
			}
		}
	})
	if d.detailRequest != nil {
		label(c, "正在读取审批详情…", 12, p.muted).Wrap()
	}
	if d.detailError != "" {
		label(c, d.detailError, 12, p.danger).Wrap().Selectable()
	}
	if !d.detailed {
		renderGenbuApprovalWriteStatus(c, p, d, writeState)
		return
	}
	data := d.detail
	note := data.ComparisonNote
	if note == "" && len(data.Changes) == 0 {
		note = "没有可显示的配置差异。"
	}
	if note != "" {
		label(c, note, 12, p.muted).Wrap().Shrink(0)
	}
	if len(data.Changes) == 0 {
		ui.Scroll(c.Key("genbu-approval-detail")).TrackScroll(&d.detailScroll).Grow(1).FillWidth().MinHeight(0).Padding(8, 0).Gap(9).Children(func() {
			renderGenbuApprovalField(c, p, "审批状态", genbuApprovalStatusLabel(data.Status))
			renderGenbuApprovalField(c, p, "执行结果", data.Result)
			ui.Divider(c)
			renderGenbuApprovalMetadata(c, p, data)
		})
	} else {
		ui.Row(c).FillWidth().Shrink(0).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
			label(c, "审批状态", 12, p.muted).NoWrap()
			label(c, genbuApprovalStatusLabel(data.Status), 12, p.text).FontWeight(600).NoWrap()
			label(c, "执行结果", 12, p.muted).NoWrap()
			label(c, firstNonEmpty(data.Result, "未返回"), 12, p.text).Wrap()
		})
		renderGenbuApprovalComparison(c, p, d)
	}
	renderGenbuApprovalWriteStatus(c, p, d, writeState)
	if data.CanApprove && genbuApprovalPending(data.Status) && canEditTaskServiceConfig(d.source.view, d.source.row) {
		ui.Row(c).FillWidth().Shrink(0).Justify(ui.End).Children(func() {
			canApprove := s.canApproveGenbuDetail(d, data.ID, data.Revision)
			if productionButton(c, "genbu-approval-accept", "同意审批", p, true, !canApprove) {
				s.confirmGenbuApproval(d)
			}
		})
	}
}

func renderGenbuApprovalMetadata(c *ui.Context, p palette, data production.GenbuApprovalDetail) {
	renderGenbuApprovalField(c, p, "服务", data.Service)
	renderGenbuApprovalField(c, p, "创建人", data.Creator)
	renderGenbuApprovalField(c, p, "创建时间", data.CreatedAt)
	renderGenbuApprovalField(c, p, "审批人", data.Approver)
	renderGenbuApprovalField(c, p, "审批时间", data.ApprovedAt)
	for _, field := range slices.Sorted(maps.Keys(data.Details)) {
		renderGenbuApprovalField(c, p, field, data.Details[field])
	}
	if len(data.FileNames) > 0 {
		renderGenbuApprovalField(c, p, "配置文件", strings.Join(data.FileNames, "\n"))
	}
}

func renderGenbuApprovalComparison(c *ui.Context, p palette, d *genbuApprovalsDialogState) {
	changes := d.detail.Changes
	index := slices.IndexFunc(changes, func(change production.GenbuApprovalConfigChange) bool { return change.File == d.comparisonFile })
	if index < 0 {
		index = 0
	}
	file := changes[index].File
	if len(changes) > 1 {
		files := make([]string, len(changes))
		for index, change := range changes {
			files[index] = change.File
		}
		if ui.Select(c.Key("genbu-approval-file"), &file, files).AlignSelf(ui.Start).Width(350).MaxWidthPercent(100).
			Height(32).Shrink(0).Label("选择审批配置文件").Tooltip(file).Changed() {
			if selected := slices.Index(files, file); selected >= 0 {
				index = selected
			}
		}
	} else {
		label(c, file, 12, p.text).FontWeight(600).SingleLine().Tooltip(file).Shrink(0)
	}
	change := changes[index]
	if d.comparison == nil || d.comparisonFile != change.File {
		d.comparisonFile = change.File
		d.comparison = &editor.DiffState{
			Old: taskServiceConfigEditorBuffer(change.File, change.Before), New: taskServiceConfigEditorBuffer(change.File, change.After),
			Layout: editor.DiffSplit, ExpandUnchanged: true, Inline: editor.DiffInlineWordAlt,
			Indicators: editor.DiffClassic, SyncHorizontal: true,
			Font: ui.Font{Family: "monospace", Size: textEditorFontSize}, LineHeight: textEditorLineHeight,
		}
	}
	d.comparison.Theme = textEditorTheme(p)
	before, after := "当前配置（读取时）", "审批变更"
	if !change.BeforeExists {
		before = "当前配置（文件不存在）"
	}
	if !change.AfterExists {
		after += "（已删除）"
	}
	if change.BeforeExists && change.AfterExists && change.Before == change.After {
		label(c, "脱敏后两侧的可见内容一致。", 12, p.muted).Wrap().Shrink(0)
	}
	ui.Column(c.Key("genbu-approval-compare")).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Border(1, p.line).Radius(8).Clip().Children(func() {
		ui.Row(c).FillWidth().Height(30).Shrink(0).Background(p.soft).AlignItems(ui.Center).Children(func() {
			for _, title := range []string{before, after} {
				ui.Box(c).Grow(1).Basis(0).MinWidth(0).Padding(0, 10).Children(func() {
					label(c, title, 12, p.muted).SingleLine()
				})
			}
		})
		editor.DiffView(c.Key(fmt.Sprintf("%d:%s", d.detailID, change.File)), d.comparison).Grow(1).MinHeight(0).FillWidth()
	})
}
