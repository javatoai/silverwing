package desktop

import (
	"fmt"
	"slices"
	"strings"

	"silverwing-go/internal/application"
)

type uatBatchScope struct {
	owner            taskFilesIdentity
	profile, groupID string
}

type uatBatchTarget struct {
	item                      application.UATBatchReleaseItem
	createdAt, service, alias string
}

type uatBatchRowOutcome struct {
	status, detail string
}

type uatBatchReleaseDialog struct {
	open, completed bool
	scope           uatBatchScope
	title           string
	history         *tagHistoryViewState
	current         func() bool
	previewRequest  *genbuReadRequest
	targets         []uatBatchTarget
	rows            []uatBatchRowOutcome
	failure         string
}

func (s *app) tagBatchReleaseScope(groupID string) uatBatchScope {
	return uatBatchScope{owner: s.taskFilesIdentity(), profile: s.snapshot.ProfileID, groupID: groupID}
}

func (s *app) tagBatchReleaseRecords(taskID, groupID string) []application.TagRecord {
	var records []application.TagRecord
	for _, record := range s.snapshot.TagHistory {
		if record.TaskID == taskID && record.BatchID == groupID {
			records = append(records, record)
		}
	}
	return records
}

func (s *app) tagBatchReleaseTargets(records []application.TagRecord, groupID string) ([]uatBatchTarget, string) {
	if len(records) == 0 {
		return nil, "未找到该批次的 Tag 记录"
	}
	cfg, err := s.readSnapshotAppConfig()
	if err != nil {
		return nil, "无法读取服务配置"
	}
	targets := make([]uatBatchTarget, 0, len(records))
	for _, record := range records {
		if record.BatchID != groupID {
			return nil, "批次内的 Tag 记录已变化"
		}
		if service, err := cfg.Service(record.ServiceID); err == nil && !service.GenbuProbeEnabled {
			continue
		}
		service, reason := s.tagUATPodService(record)
		if reason != "" {
			return nil, firstNonEmpty(record.ServiceName, record.ServiceID, "批次记录") + " · " + reason
		}
		targets = append(targets, uatBatchTarget{
			item:      application.UATBatchReleaseItem{OperationID: record.ID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Tag: record.Tag},
			createdAt: record.CreatedAt, service: firstNonEmpty(service.DisplayName, service.ID), alias: service.GenbuServiceName,
		})
	}
	// Probe refreshes can reorder the history. The batch's identity follows its members.
	slices.SortFunc(targets, func(a, b uatBatchTarget) int { return strings.Compare(a.item.OperationID, b.item.OperationID) })
	for i := 1; i < len(targets); i++ {
		if targets[i].item.OperationID == targets[i-1].item.OperationID {
			return nil, "批次中存在重复的 Tag 记录"
		}
	}
	return targets, ""
}

func (s *app) tagBatchReleaseCurrent(scope uatBatchScope, history *tagHistoryViewState) bool {
	return s.page == "tasks" && s.tab == taskTagTab && !s.workspaceReviewPageVisible() &&
		!scope.owner.archived && s.tagHistoryStateCurrent(history) && s.tagBatchReleaseScope(scope.groupID) == scope
}

func (s *app) openUATBatchRelease(history *tagHistoryViewState, group tagHistoryGroup, targets []uatBatchTarget) {
	scope := s.tagBatchReleaseScope(group.Item.GroupID)
	if group.Item.BatchID == nil || len(targets) == 0 ||
		!s.tagBatchReleaseCurrent(scope, history) || s.busy || s.taskServicesUnit.uatWriteRunning() {
		return
	}
	connection := s.integrationContext("genbu")
	configuration := s.tagConfigurationIdentity()
	current := func() bool {
		if connection.Err() != nil || !s.tagBatchReleaseCurrent(scope, history) || s.tagConfigurationIdentity() != configuration {
			return false
		}
		active, reason := s.tagBatchReleaseTargets(s.tagBatchReleaseRecords(history.TaskID, scope.groupID), scope.groupID)
		return reason == "" && slices.Equal(targets, active)
	}
	if !current() {
		return
	}
	s.openUATBatchDialog(&uatBatchReleaseDialog{
		scope: scope, history: history, title: firstNonEmpty(group.Item.FolderName, s.taskUnit.currentTask().title, "Tag 批次"),
		current: current, targets: targets,
	})
}

func (s *app) openUATBatchDialog(d *uatBatchReleaseDialog) {
	s.closeUATPodAction(s.taskServicesUnit.uatDialog)
	s.closeUATBatchDialog(s.taskServicesUnit.uatBatchDialog)
	s.closeGenbuReadDialogs()
	s.leaveLinkedTaskServices()
	d.open = true
	s.taskServicesUnit.uatBatchDialog = d
}

func (s *app) closeUATBatchDialog(d *uatBatchReleaseDialog) {
	if d != nil && s.taskServicesUnit.uatBatchDialog == d {
		s.taskServicesUnit.uatBatchDialog = nil
		d.open = false
		cancelGenbuRead(&d.previewRequest)
	}
}

func (s *app) uatBatchDialogCurrent(d *uatBatchReleaseDialog) bool {
	return d != nil && d.open && s.taskServicesUnit.uatBatchDialog == d && !s.taskServicesUnit.stopped && d.current()
}

func (d *uatBatchReleaseDialog) action(phase string, items []application.UATBatchReleaseItem) application.Action {
	payload := tagHistoryScopePayload(d.history)
	payload["groupId"], payload["items"] = d.scope.groupID, items
	return application.Action{Kind: "tag.history.uat-batch-" + phase, TaskID: d.history.TaskID, Payload: payload}
}

func (s *app) previewUATBatchRelease(d *uatBatchReleaseDialog) {
	if !s.uatBatchDialogCurrent(d) || d.completed || s.busy || s.taskServicesUnit.uatWriteRunning() || d.previewRequest != nil {
		return
	}
	d.failure, d.rows = "", nil
	items := make([]application.UATBatchReleaseItem, 0, len(d.targets))
	for _, target := range d.targets {
		items = append(items, target.item)
	}
	s.runGenbuRead(&d.previewRequest, func() bool { return s.uatBatchDialogCurrent(d) }, d.action("preview", items), func(result application.ActionResult, err error) {
		s.finishUATBatchPreview(d, result, err)
	})
}

func (t uatBatchTarget) matchesItem(item application.UATBatchReleaseItem) bool {
	item.Preflight = ""
	return item == t.item
}

func (t uatBatchTarget) matchesResult(result application.CLIUATOperationResult) bool {
	return result.Action == uatRelease && strings.EqualFold(result.Environment, "uat") && result.GenbuService == t.alias && result.Tag == t.item.Tag
}

func (s *app) finishUATBatchPreview(d *uatBatchReleaseDialog, result application.ActionResult, err error) {
	if !s.uatBatchDialogCurrent(d) || d.completed {
		return
	}
	response, ok := result.Data.(application.UATBatchReleaseResult)
	valid := ok && len(response.Items) == len(d.targets)
	items := make([]application.UATBatchReleaseItem, len(d.targets))
	rows := make([]uatBatchRowOutcome, len(d.targets))
	for i, target := range d.targets {
		rows[i].status = "not-sent"
		if i >= len(response.Items) || !target.matchesItem(response.Items[i].Item) {
			valid = false
			continue
		}
		row := response.Items[i]
		rows[i].detail = row.Error
		if row.Error != "" || row.Result.Status != "preview" || row.Result.Preflight == "" || !target.matchesResult(row.Result) {
			valid = false
			continue
		}
		items[i] = target.item
		items[i].Preflight = row.Result.Preflight
	}
	if err != nil || !valid {
		d.rows, d.failure = rows, "批次未提交，无法核对全部发布目标。"
		if err != nil {
			d.failure = err.Error()
		}
		return
	}
	unit := &s.taskServicesUnit
	if s.busy || unit.uatWriteRunning() {
		d.failure = "有操作正在执行，完成后可重试。"
		return
	}
	unit.uatBatchSubmission = d
	if !s.dispatchUATWrite(d.action("submit", items), func(result application.ActionResult, err error) { s.finishUATBatchRelease(d, result, err) }) {
		unit.uatBatchSubmission = nil
		d.failure = "Genbu 操作不可用，请重试。"
	}
}

func (s *app) finishUATBatchRelease(d *uatBatchReleaseDialog, result application.ActionResult, err error) {
	unit := &s.taskServicesUnit
	if unit.uatBatchSubmission != d {
		return
	}
	unit.uatBatchSubmission = nil
	d.completed = true
	if err != nil {
		d.failure = err.Error()
	}
	response, _ := result.Data.(application.UATBatchReleaseResult)
	d.rows = make([]uatBatchRowOutcome, len(d.targets))
	counts := map[string]int{}
	for i, target := range d.targets {
		outcome := uatBatchRowOutcome{status: "unknown"}
		if i < len(response.Items) && target.matchesItem(response.Items[i].Item) {
			row := response.Items[i]
			outcome.detail = row.Error
			switch {
			case row.Result.Status == "submitted" && target.matchesResult(row.Result):
				outcome.status = "submitted"
			case row.Result.Status == "preview" || row.Result.Status == "not-sent" || row.Result.Status == "rejected":
				outcome.status = "not-sent"
			}
		}
		d.rows[i] = outcome
		counts[outcome.status]++
	}
	if !s.uatBatchDialogCurrent(d) {
		scope := d.title + " · 批量发布到 UAT"
		if counts["submitted"] == len(d.targets) && err == nil {
			s.lastError, s.lastMessage = "", fmt.Sprintf("%s · 已提交 %d 项", scope, counts["submitted"])
		} else {
			s.lastMessage, s.lastError = "", fmt.Sprintf("%s · 已提交 %d 项 · 未提交 %d 项 · 待确认 %d 项", scope, counts["submitted"], counts["not-sent"], counts["unknown"])
		}
	}
}
