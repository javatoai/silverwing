package desktop

import (
	"context"
	"maps"
	"slices"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

const (
	uatRelease      = "release"
	uatRestart      = "restart"
	uatReleaseLabel = "发布到 UAT"
	uatRestartLabel = "重启 UAT Pod"
)

type uatPodActionDialog struct {
	open                   bool
	operation, pinned, tag string
	service, genbuService  string
	request                application.Action
	serviceView            *taskServicesView
	current                func() bool
	showPods               func()
	tags                   production.GenbuTagPage
	tagsRequest            *genbuReadRequest
	tagsError, tagsKeyword string
	tagsLoaded             bool
	tagScroll              ui.ScrollState
	preview                *application.CLIUATOperationResult
	previewRequest         *genbuReadRequest
	failure                string
	receipt                string
}

func (d *uatPodActionDialog) title() string {
	if d.operation == uatRestart {
		return uatRestartLabel
	}
	return uatReleaseLabel
}

func (d *uatPodActionDialog) action(suffix string) application.Action {
	request := d.request
	request.Kind += suffix
	request.Payload = maps.Clone(request.Payload)
	request.Payload["operation"] = d.operation
	return request
}

func (s *app) taskUATPodActionAvailable(v *taskServicesView, row *taskServiceRow) bool {
	return v != nil && row != nil && !v.identity.archived && row.enabled && row.genbuName != "" &&
		row.environment == "uat" && v.environment == "uat" && s.integrationReady("genbu")
}

func (s *app) openCatalogUATPodAction(serviceID, operation string) {
	if s.catalogRuntimeEnvironment() != "uat" || s.taskServicesUnit.uatWriteRunning() {
		return
	}
	v, row := s.openCatalogRuntime(serviceID)
	s.openTaskUATPodAction(v, row, operation)
}

func (s *app) openTaskUATPodAction(v *taskServicesView, row *taskServiceRow, operation string) {
	if !s.taskUATPodActionAvailable(v, row) || !s.taskServicesCurrent(v) || !slices.Contains(v.rows, row) {
		return
	}
	connection := s.integrationContext("genbu")
	profile := s.snapshot.ProfileID
	scope := genbuServiceReadScope{view: v, row: row, epoch: v.epoch}
	s.openUATPodAction(&uatPodActionDialog{
		operation: operation, service: row.name, genbuService: row.genbuName, serviceView: v,
		request: s.taskServiceAction(v, row, "task.services.uat-"),
		current: func() bool {
			return connection.Err() == nil && s.snapshot.ProfileID == profile && s.taskUATPodActionAvailable(v, row) && s.genbuServiceReadCurrent(scope)
		},
		showPods: func() {
			s.openTaskServiceDialog(v, row, taskServicePodsDetail)
			if v.catalog == nil {
				s.requestTaskServicePods(v, row)
			}
		},
	})
}

func (s *app) tagUATPodService(record application.TagRecord) (domain.ServiceConfig, string) {
	task := s.taskUnit.currentTask()
	switch {
	case !s.integrationReady("genbu"):
		return domain.ServiceConfig{}, "Genbu 未启用"
	case s.page != "tasks" || s.tab != taskTagTab || s.workspaceReviewPageVisible() || task.archived:
		return domain.ServiceConfig{}, "当前任务无法发布"
	case record.ID == "" || record.TaskID != task.id:
		return domain.ServiceConfig{}, "Tag 记录已变化，请刷新历史"
	case !domain.TagGenbuProbeEligible(domain.TagOperationState(record.State), record.Tag):
		return domain.ServiceConfig{}, "Tag 尚未生成并推送"
	case record.GenbuStatus.NotFound:
		return domain.ServiceConfig{}, "Genbu 未找到该 Tag"
	case record.GenbuStatus.Build != domain.GenbuSuccess:
		return domain.ServiceConfig{}, "Genbu 构建尚未成功"
	}
	cfg, err := s.readSnapshotAppConfig()
	if err != nil {
		return domain.ServiceConfig{}, "无法读取服务配置"
	}
	service, err := cfg.Service(record.ServiceID)
	switch {
	case err != nil:
		return domain.ServiceConfig{}, "服务配置已移除"
	case !service.Enabled:
		return domain.ServiceConfig{}, "服务已停用"
	case !service.GenbuProbeEnabled:
		return domain.ServiceConfig{}, "服务未开启 Genbu 探测"
	case strings.TrimSpace(service.GenbuServiceName) == "":
		return domain.ServiceConfig{}, "请先配置 Genbu 服务名"
	}
	for _, workspace := range task.workspaces {
		if workspace.ServiceID == service.ID || workspace.ServiceID == "" && workspace.Name == service.DisplayName {
			return service, ""
		}
	}
	if len(task.workspaces) == 0 && slices.Contains(task.serviceIDs, service.ID) {
		return service, ""
	}
	return domain.ServiceConfig{}, "服务已从任务移除"
}

func (s *app) openTagUATPodAction(state *tagHistoryViewState, record application.TagRecord) {
	service, reason := s.tagUATPodService(record)
	if reason != "" || !s.tagHistoryStateCurrent(state) {
		return
	}
	connection := s.integrationContext("genbu")
	profile := s.snapshot.ProfileID
	configuration := s.tagConfigurationIdentity()
	current := func() bool {
		if connection.Err() != nil || s.snapshot.ProfileID != profile || !s.tagHistoryStateCurrent(state) ||
			s.tagConfigurationIdentity() != configuration {
			return false
		}
		for _, item := range s.snapshot.TagHistory {
			if item.ID == record.ID && item.TaskID == record.TaskID && item.CreatedAt == record.CreatedAt &&
				item.ServiceID == record.ServiceID && item.ModuleID == record.ModuleID && item.Tag == record.Tag {
				active, reason := s.tagUATPodService(item)
				return reason == "" && active.GenbuServiceName == service.GenbuServiceName && active.DisplayName == service.DisplayName
			}
		}
		return false
	}
	payload := tagHistoryScopePayload(state)
	payload["operationId"], payload["environment"] = record.ID, "uat"
	s.openUATPodAction(&uatPodActionDialog{
		operation: uatRelease, pinned: record.Tag, tag: record.Tag,
		service: firstNonEmpty(service.DisplayName, service.ID), genbuService: service.GenbuServiceName,
		request: application.Action{Kind: "tag.history.uat-", TaskID: state.TaskID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Payload: payload},
		current: current, showPods: func() { s.openLinkedUATPods(record.ServiceID, current) },
	})
}

func (s *app) openLinkedUATPods(serviceID string, current func() bool) {
	if !current() {
		return
	}
	v, _ := s.taskServicesProjection()
	if v == nil {
		return
	}
	for _, row := range v.visibleRows() {
		if row.serviceID == serviceID {
			s.leaveLinkedTaskServices()
			v.active = true
			v.linkedScope = current
			s.taskServicesUnit.linked = v
			s.openTaskServiceDialog(v, row, taskServicePodsDetail)
			return
		}
	}
}

func (s *app) uatPodActionCurrent(d *uatPodActionDialog) bool {
	return d != nil && d.open && s.taskServicesUnit.uatDialog == d && !s.taskServicesUnit.stopped && d.current()
}

func (s *app) openUATPodAction(d *uatPodActionDialog) {
	if s.busy || s.taskServicesUnit.stopped || s.taskServicesUnit.uatWriteRunning() ||
		(d.operation != uatRelease && d.operation != uatRestart) || !d.current() {
		return
	}
	s.closeUATPodAction(s.taskServicesUnit.uatDialog)
	s.closeUATBatchDialog(s.taskServicesUnit.uatBatchDialog)
	s.closeGenbuReadDialogs()
	s.leaveLinkedTaskServices()
	if v := d.serviceView; v != nil {
		s.closeTaskServiceDialog(v, v.dialog)
	}
	d.open = true
	s.taskServicesUnit.uatDialog = d
	if d.operation == uatRestart {
		s.previewUATPodAction(d)
	} else if d.pinned == "" {
		s.searchUATTags(d, "", 1)
	}
}

func (s *app) closeUATPodAction(d *uatPodActionDialog) {
	if d == nil || s.taskServicesUnit.uatDialog != d {
		return
	}
	s.taskServicesUnit.uatDialog = nil
	d.open = false
	cancelGenbuRead(&d.tagsRequest)
	cancelGenbuRead(&d.previewRequest)
}

func (s *app) editUATTag(d *uatPodActionDialog) {
	cancelGenbuRead(&d.previewRequest)
	cancelGenbuRead(&d.tagsRequest)
	d.preview, d.failure = nil, ""
}

func (s *app) searchUATTags(d *uatPodActionDialog, keyword string, page int) {
	if !s.uatPodActionCurrent(d) || d.pinned != "" || d.operation != uatRelease || d.previewRequest != nil ||
		d.receipt != "" || s.taskServicesUnit.uatWriteRunning() {
		return
	}
	s.editUATTag(d)
	action := d.action("tags")
	action.Payload["keyword"], action.Payload["page"] = keyword, page
	d.tagsKeyword, d.tagsError, d.tagsLoaded, d.tags = keyword, "", false, production.GenbuTagPage{}
	d.tagScroll = ui.ScrollState{}
	s.runGenbuRead(&d.tagsRequest, func() bool { return s.uatPodActionCurrent(d) }, action,
		func(result application.ActionResult, err error) {
			data, err := genbuReadData[production.GenbuTagPage](result, err)
			if err != nil {
				d.tagsError = err.Error()
				return
			}
			d.tags, d.tagsLoaded = data, true
		})
}

func (s *app) previewUATPodAction(d *uatPodActionDialog) {
	if !s.uatPodActionCurrent(d) || s.busy || s.taskServicesUnit.uatWriteRunning() || d.previewRequest != nil || d.receipt != "" {
		return
	}
	tag := strings.TrimSpace(d.tag)
	if d.operation == uatRelease && tag == "" {
		return
	}
	s.editUATTag(d)
	action := d.action("preview")
	action.Payload["tag"] = tag
	s.runGenbuRead(&d.previewRequest, func() bool { return s.uatPodActionCurrent(d) && strings.TrimSpace(d.tag) == tag }, action,
		func(result application.ActionResult, err error) {
			s.finishUATPodPreview(d, tag, result, err)
		})
}

func (s *app) finishUATPodPreview(d *uatPodActionDialog, tag string, result application.ActionResult, err error) {
	if !s.uatPodActionCurrent(d) || strings.TrimSpace(d.tag) != tag {
		return
	}
	preview, err := genbuReadData[application.CLIUATOperationResult](result, err)
	if err != nil {
		d.failure = err.Error()
		return
	}
	if preview.Status != "preview" || preview.Preflight == "" || !d.matches(preview, tag) {
		d.failure = "Genbu 返回了不同的操作目标，请重试。"
		return
	}
	if d.operation == uatRelease {
		s.submitUATPodAction(d, &preview)
	} else {
		d.preview = &preview
	}
}

func (d *uatPodActionDialog) matches(data application.CLIUATOperationResult, tag string) bool {
	return data.Action == d.operation && strings.EqualFold(data.Environment, "uat") &&
		data.GenbuService == d.genbuService && data.Tag == tag
}

func (s *app) submitUATPodAction(d *uatPodActionDialog, preview *application.CLIUATOperationResult) {
	unit := &s.taskServicesUnit
	if !s.uatPodActionCurrent(d) || preview == nil || d.receipt != "" || !d.matches(*preview, strings.TrimSpace(d.tag)) {
		return
	}
	if s.busy || unit.uatWriteRunning() {
		d.failure = "有操作正在执行，完成后可重试。"
		return
	}
	action := d.action("submit")
	action.Payload["tag"], action.Payload["preflight"] = preview.Tag, preview.Preflight
	unit.uatSubmission = d
	if !s.dispatchUATWrite(action, func(result application.ActionResult, err error) { s.finishUATPodAction(d, result, err) }) {
		unit.uatSubmission = nil
		d.failure = "Genbu 操作不可用，请重试。"
	}
}

func (s *app) dispatchUATWrite(action application.Action, finish func(application.ActionResult, error)) bool {
	win, backend := s.win, s.backend
	if win == nil || win.IsDestroyed() || backend == nil {
		return false
	}
	wg := &s.taskServicesUnit.wg
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Navigation cancels the reads only. A dispatched write still needs its receipt.
		result, err := backend.Dispatch(context.Background(), action)
		if !win.IsDestroyed() {
			win.Update(func() {
				if !win.IsDestroyed() {
					finish(result, err)
				}
			})
		}
	}()
	return true
}

func (s *app) finishUATPodAction(d *uatPodActionDialog, result application.ActionResult, err error) {
	if s.taskServicesUnit.uatSubmission != d {
		return
	}
	s.taskServicesUnit.uatSubmission = nil
	receipt, ok := result.Data.(application.CLIUATOperationResult)
	scope := d.service + " · " + d.title()
	if d.tag != "" {
		scope += " · " + d.tag
	}
	current := s.uatPodActionCurrent(d)
	switch {
	case ok && receipt.Status == "submitted" && d.matches(receipt, strings.TrimSpace(d.tag)):
		d.receipt = "submitted"
		s.lastError, s.lastMessage = "", scope+"：已提交。可查看 Pod 明细确认进度。"
		if err != nil {
			d.failure = "回执记录异常。" + err.Error()
			if !current {
				s.lastMessage += " " + d.failure
			}
		}
	case ok && (receipt.Status == "rejected" || receipt.Status == "not-sent") || err != nil && (!ok || receipt.Status == ""):
		d.preview = nil
		d.failure = "未提交。"
		if err != nil {
			d.failure += err.Error()
		} else {
			d.failure += result.Message
		}
		if !current {
			s.lastMessage, s.lastError = "", scope+"："+d.failure
		}
	default:
		d.receipt = "unknown"
		s.lastMessage, s.lastError = "", scope+"：结果待确认，请查看 Pod 明细。"
	}
}
