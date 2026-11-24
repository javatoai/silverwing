package desktop

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type genbuApprovalWriteState uint8

const (
	genbuApprovalSubmitting genbuApprovalWriteState = iota + 1
	genbuApprovalOutcomeUnknown
	genbuApprovalApproved
)

func genbuApprovalPending(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "待处理":
		return true
	default:
		return false
	}
}

func genbuApprovalTerminal(status string) bool {
	switch genbuApprovalStatusLabel(status) {
	case "已通过", "已拒绝", "已驳回", "已撤销":
		return true
	default:
		return false
	}
}

func (s *app) canApproveGenbuDetail(d *genbuApprovalsDialogState, id int64, revision string) bool {
	return s.genbuApprovalsCurrent(d) && s.integrationReady("genbu") &&
		d.detailed && d.detailRequest == nil && d.detailID == id && d.detail.ID == id &&
		revision != "" && revision == d.detail.Revision && d.detail.CanApprove && genbuApprovalPending(d.detail.Status) &&
		canEditTaskServiceConfig(d.source.view, d.source.row) && d.approvalError == "" &&
		s.taskServicesUnit.approvalWrites[id] == 0
}

func (s *app) confirmGenbuApproval(d *genbuApprovalsDialogState) {
	id, revision := d.detail.ID, d.detail.Revision
	current := func() bool { return s.canApproveGenbuDetail(d, id, revision) }
	if !current() {
		return
	}
	buttons := []confirmationDialogButton{{label: "取消"}, {label: "确认同意", primary: true}}
	if s.showConfirmationDialog("同意配置审批", "确认同意这条 UAT 配置审批？", "", buttons, 0, 0, func(choice int) {
		if choice == 1 {
			s.submitGenbuApproval(d, id, revision)
		}
	}) {
		state := s.confirmationDialog
		state.Current = current
		state.Summary = []confirmationDialogField{
			{Label: "审批", Value: fmt.Sprintf("#%d", id)},
			{Label: "服务", Value: firstNonEmpty(d.detail.Service, d.source.row.name)},
			{Label: "环境", Value: "UAT"},
		}
	}
}

func (s *app) submitGenbuApproval(d *genbuApprovalsDialogState, id int64, revision string) {
	if !s.canApproveGenbuDetail(d, id, revision) {
		return
	}
	v, row := d.source.view, d.source.row
	action := s.taskServiceAction(v, row, "task.services.approval-approve")
	action.Payload["approvalId"] = strconv.FormatInt(id, 10)
	action.Payload["revision"] = revision
	if s.taskServicesUnit.approvalWrites == nil {
		s.taskServicesUnit.approvalWrites = make(map[int64]genbuApprovalWriteState)
	}
	s.taskServicesUnit.approvalWrites[id] = genbuApprovalSubmitting
	connection := s.integrationContext("genbu")
	if !s.dispatchUATWrite(action, func(result application.ActionResult, err error) {
		s.finishGenbuApproval(d, id, result, err, connection.Err() == nil)
	}) {
		delete(s.taskServicesUnit.approvalWrites, id)
		d.approvalError = "Genbu 不可用，请刷新详情后重试。"
	}
}

func (s *app) finishGenbuApproval(d *genbuApprovalsDialogState, id int64, result application.ActionResult, err error, canPresent bool) {
	writes := s.taskServicesUnit.approvalWrites
	if writes[id] != genbuApprovalSubmitting {
		return
	}
	data, ok := result.Data.(production.GenbuApprovalDetail)
	matches := ok && data.ID == id && data.Environment == "uat"
	approved := matches && genbuApprovalStatusLabel(data.Status) == "已通过"
	unknown := errors.Is(err, production.ErrGenbuApprovalUnknown) || err == nil && !approved
	failure := ""
	switch {
	case approved:
		writes[id] = genbuApprovalApproved
	case unknown:
		writes[id] = genbuApprovalOutcomeUnknown
	default:
		delete(writes, id)
		failure = err.Error()
	}
	d.listed = false
	if !canPresent || !s.integrationReady("genbu") {
		return
	}
	if s.genbuApprovalsCurrent(d) && d.detailID == id {
		cancelGenbuRead(&d.detailRequest)
		if matches {
			s.setGenbuApprovalDetail(d, data)
		}
		d.approvalError = failure
		return
	}
	if s.genbuApprovalsCurrent(d) && d.detailID == 0 {
		s.readGenbuApprovals(d, max(1, d.listPage))
	}
	scope := fmt.Sprintf("%s · UAT · 审批 #%d", d.source.row.name, id)
	switch {
	case approved:
		s.lastError, s.lastMessage = "", scope+" 已通过"
	case unknown:
		s.lastMessage, s.lastError = "", scope+" 结果待确认，请刷新审批详情。"
	default:
		s.lastMessage, s.lastError = "", scope+" "+failure
	}
}

func renderGenbuApprovalWriteStatus(c *ui.Context, p palette, d *genbuApprovalsDialogState, state genbuApprovalWriteState) {
	switch state {
	case genbuApprovalSubmitting:
		label(c, "正在同意审批…", 12, p.muted).Wrap().Shrink(0)
	case genbuApprovalOutcomeUnknown:
		label(c, "审批结果待确认，请刷新详情。", 12, p.amber).Wrap().Shrink(0)
	case genbuApprovalApproved:
		label(c, "审批已通过，请刷新详情。", 12, p.green).Wrap().Shrink(0)
	}
	if d.approvalError != "" {
		label(c, d.approvalError, 12, p.danger).Wrap().Shrink(0).Selectable()
	}
}
