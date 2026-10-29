package desktop

import (
	"context"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type taskCreationProgressState struct {
	token      string
	generation uint64
	groupID    string
	signature  string
	mode       string

	phase       workspace.TaskCreationProgressPhase
	serviceName string
	moduleName  string
	message     string
	finished    bool
	cancelling  bool
	interrupted bool
	failure     string

	services []*taskCreationProgressService
	scroll   ui.ScrollState
	lastSeq  uint64
}

type taskCreationProgressService struct {
	id      string
	name    string
	phase   workspace.TaskCreationProgressPhase
	status  workspace.TaskCreationProgressStatus
	message string
	modules []*taskCreationProgressModule
}

type taskCreationProgressModule struct {
	id               string
	name             string
	phase            workspace.TaskCreationProgressPhase
	status           workspace.TaskCreationProgressStatus
	message          string
	failure          string
	cleanup          string
	checked          bool
	workspaceTouched bool
	prepared         bool
	rolledBack       bool
	cleanupFail      bool
}

type taskCreationProgressRequest struct {
	ctx        context.Context
	window     *mygo.Window
	state      *taskCreationState
	progress   *taskCreationProgressState
	token      string
	generation uint64
	groupID    string
	home       string
	configJSON string
	signature  string
	kind       string
	preflight  *taskCreationPreflightRequest
}

func newTaskCreationProgressState(s *app, state *taskCreationState, generation uint64) *taskCreationProgressState {
	if state == nil {
		return nil
	}
	progress := &taskCreationProgressState{
		token: state.token, generation: generation, groupID: state.groupID,
		mode:    "create",
		phase:   workspace.TaskCreationPhasePreparingWorktree,
		message: "正在创建任务并准备工作区…",
	}
	if state.preview != nil {
		progress.signature = state.preview.signature
	}
	group := s.taskCreationGroup()
	if group == nil {
		return progress
	}
	for _, service := range group.Services {
		if !state.services[service.ID] {
			continue
		}
		serviceProgress := &taskCreationProgressService{id: service.ID, name: taskServiceDisplayName(s, service)}
		for _, moduleID := range state.moduleOrder[service.ID] {
			draft := state.moduleData[service.ID][moduleID]
			if draft == nil {
				continue
			}
			name := strings.TrimSpace(draft.Name)
			if name == "" {
				name = draft.ID
			}
			serviceProgress.modules = append(serviceProgress.modules, &taskCreationProgressModule{id: moduleID, name: name})
		}
		if len(serviceProgress.modules) == 0 {
			serviceProgress.modules = append(serviceProgress.modules, &taskCreationProgressModule{name: "工作区"})
		}
		progress.services = append(progress.services, serviceProgress)
	}
	return progress
}

func (s *app) taskCreationProgressUpdates(ctx context.Context, action application.Action) func(workspace.TaskCreationProgress) {
	if ctx == nil || action.Kind != "task.create" {
		return nil
	}
	state := s.taskUnit.state.taskCreation
	if state == nil || !s.dialogOpen || s.form.mode != "task-create" ||
		payloadString(action.Payload, "taskCreateToken") != state.token || state.phase != "creating" || state.creationProgress == nil {
		return nil
	}
	progress := state.creationProgress
	if progress.token != state.token || progress.generation != state.progressGeneration {
		return nil
	}
	request := &taskCreationProgressRequest{
		ctx: ctx, window: s.win, state: state, progress: progress,
		token: state.token, generation: state.progressGeneration, groupID: state.groupID,
		home: s.snapshot.Home, configJSON: s.settingsUnit.state.configJSON, signature: progress.signature, kind: action.Kind,
	}
	return s.taskCreationProgressCallback(request)
}

func (s *app) taskCreationPreflightProgressUpdates(preflight *taskCreationPreflightRequest) func(workspace.TaskCreationProgress) {
	if s == nil || preflight == nil || preflight.ctx == nil || preflight.form == nil || preflight.form.creationProgress == nil {
		return nil
	}
	state := preflight.form
	progress := preflight.form.creationProgress
	if progress.token != state.token || progress.generation != state.progressGeneration || progress.mode != "preflight" {
		return nil
	}
	request := &taskCreationProgressRequest{
		ctx: preflight.ctx, window: s.win, state: state, progress: progress,
		token: preflight.token, generation: state.progressGeneration, groupID: state.groupID,
		home: preflight.home, configJSON: s.settingsUnit.state.configJSON, signature: preflight.previewSignature,
		kind: "task.create-preview", preflight: preflight,
	}
	return s.taskCreationProgressCallback(request)
}

func (s *app) taskCreationProgressCallback(request *taskCreationProgressRequest) func(workspace.TaskCreationProgress) {
	if request == nil || request.ctx == nil || request.window == nil || request.window.IsDestroyed() {
		return nil
	}
	return func(event workspace.TaskCreationProgress) {
		if request.window == nil || request.window.IsDestroyed() || request.ctx.Err() != nil && event.Phase != workspace.TaskCreationPhaseRollback {
			return
		}
		request.window.Update(func() { s.applyTaskCreationProgress(request, event) })
	}
}

func (s *app) applyTaskCreationProgress(request *taskCreationProgressRequest, event workspace.TaskCreationProgress) {
	if request == nil || request.ctx == nil || request.window == nil || request.window.IsDestroyed() {
		return
	}
	if request.ctx.Err() != nil && event.Phase != workspace.TaskCreationPhaseRollback {
		return
	}
	s.applyTaskCreationProgressEvent(request, event)
}

// applyTaskCreationProgressEvent applies an already-dispatched event on the
// UI thread. Keeping window scheduling outside this reducer lets headless UI
// proofs exercise the same identity and sequence gates as the live callback.
func (s *app) applyTaskCreationProgressEvent(request *taskCreationProgressRequest, event workspace.TaskCreationProgress) {
	if s == nil || request == nil || request.ctx == nil {
		return
	}
	if request.kind == "task.create-preview" {
		if request.preflight == nil || request.preflight.cancelRequested || request.ctx.Err() != nil ||
			!s.taskCreationPreflightIdentityRelevant(request.preflight) || request.state == nil ||
			request.state.creationProgress != request.progress || request.state.progressGeneration != request.generation {
			return
		}
	} else if request.ctx.Err() != nil && (event.Phase != workspace.TaskCreationPhaseRollback || !s.operationCancelling) {
		return
	}
	if request.state == nil || s.taskUnit.state.taskCreation != request.state || !s.dialogOpen || s.form.mode != "task-create" ||
		request.kind == "task.create" && (!s.busy || s.operationKind != "task.create" || request.state.phase != "creating") ||
		request.state.token != request.token || request.state.preview == nil ||
		request.state.progressGeneration != request.generation || request.state.groupID != request.groupID ||
		request.state.creationProgress != request.progress || request.progress.generation != request.generation ||
		s.snapshot.Home != request.home || s.settingsUnit.state.configJSON != request.configJSON ||
		request.state.preview.signature != request.signature || request.state.preview.signature != s.taskCreationSignature() || event.Sequence == 0 || event.Sequence <= request.progress.lastSeq {
		return
	}
	progress := request.progress
	progress.lastSeq = event.Sequence
	progress.phase = event.Phase
	progress.serviceName = strings.TrimSpace(event.ServiceName)
	progress.moduleName = strings.TrimSpace(event.ModuleName)
	progress.message = strings.TrimSpace(event.Message)

	if event.Phase == workspace.TaskCreationPhaseCompleted && event.Status == workspace.TaskCreationProgressCompleted {
		progress.finished = true
	}
	if request.kind == "task.create-preview" && event.Phase == workspace.TaskCreationPhaseChecking && event.Status != "" && event.ServiceID == "" && event.ModuleID == "" && event.Status != workspace.TaskCreationProgressStarted {
		progress.finished = event.Status == workspace.TaskCreationProgressCompleted || event.Status == workspace.TaskCreationProgressFailed
		if event.Status == workspace.TaskCreationProgressFailed {
			progress.failure = strings.TrimSpace(event.Message)
		}
	}
	if event.Phase == workspace.TaskCreationPhaseRollback {
		applyTaskCreationRollback(progress, event)
		return
	}
	if event.ServiceID != "" && event.ModuleID == "" {
		if service := taskCreationProgressFindService(progress, event.ServiceID); service != nil {
			service.phase = event.Phase
			service.status = event.Status
			service.message = strings.TrimSpace(event.Message)
		}
	}
	if event.ModuleID != "" {
		if module := taskCreationProgressFindModule(progress, event.ServiceID, event.ModuleID); module != nil {
			module.phase = event.Phase
			module.status = event.Status
			module.message = strings.TrimSpace(event.Message)
			if event.Status == workspace.TaskCreationProgressFailed {
				module.failure = strings.TrimSpace(event.Message)
			}
			if request.kind == "task.create-preview" && event.Phase == workspace.TaskCreationPhaseChecking && event.Status == workspace.TaskCreationProgressCompleted {
				module.checked = true
			}
			if event.Phase == workspace.TaskCreationPhasePreparingWorktree || event.Phase == workspace.TaskCreationPhaseCloningRepository || event.Phase == workspace.TaskCreationPhaseInitializingWorkspace {
				module.workspaceTouched = true
			}
			if event.Phase == workspace.TaskCreationPhaseInitializingWorkspace && event.Status == workspace.TaskCreationProgressCompleted {
				module.prepared = true
				module.rolledBack = false
				module.cleanupFail = false
			}
		}
	}
}

func taskCreationProgressFindService(progress *taskCreationProgressState, serviceID string) *taskCreationProgressService {
	if progress == nil || serviceID == "" {
		return nil
	}
	for _, service := range progress.services {
		if service.id == serviceID {
			return service
		}
	}
	return nil
}

func taskCreationProgressFindModule(progress *taskCreationProgressState, serviceID, moduleID string) *taskCreationProgressModule {
	if progress == nil || serviceID == "" || moduleID == "" {
		return nil
	}
	for _, service := range progress.services {
		if service.id != serviceID {
			continue
		}
		for _, module := range service.modules {
			if module.id == moduleID {
				return module
			}
		}
	}
	return nil
}

func applyTaskCreationRollback(progress *taskCreationProgressState, event workspace.TaskCreationProgress) {
	if progress == nil {
		return
	}
	for _, service := range progress.services {
		if event.ServiceID != "" && event.ServiceID != service.id {
			continue
		}
		for _, module := range service.modules {
			if event.ModuleID != "" && event.ModuleID != module.id {
				continue
			}
			// A wider rollback-completed event cannot erase a scoped cleanup failure.
			// The caller still needs to inspect the failed module after the overall
			// rollback workflow has finished.
			if module.cleanupFail && event.Status == workspace.TaskCreationProgressCompleted {
				continue
			}
			if event.ModuleID == "" && event.ServiceID != "" && !module.prepared {
				continue
			}
			if event.ServiceID == "" && !module.prepared && !module.workspaceTouched && !module.rolledBack && !module.cleanupFail {
				continue
			}
			module.phase = workspace.TaskCreationPhaseRollback
			module.status = event.Status
			module.prepared = false
			if event.Status == workspace.TaskCreationProgressCompleted {
				module.rolledBack = true
				module.cleanupFail = false
				module.cleanup = ""
				module.message = "工作区已回滚"
			} else if event.Status == workspace.TaskCreationProgressFailed {
				module.cleanupFail = true
				module.cleanup = strings.TrimSpace(event.Message)
				module.message = "清理结果需检查"
			} else {
				module.message = "正在回滚工作区…"
			}
		}
	}
}

func (s *app) failTaskCreationProgress(state *taskCreationState, message string) {
	if state == nil || state.creationProgress == nil {
		return
	}
	progress := state.creationProgress
	progress.finished = true
	progress.failure = strings.TrimSpace(message)
}

func (s *app) renderTaskCreationProgress(c *ui.Context, p palette, progress *taskCreationProgressState) {
	if progress == nil {
		return
	}
	_, windowHeight := c.Size()
	progressHeight := min(float32(280), max(float32(210), windowHeight*0.32))
	ui.Column(c.Key("task-create-progress")).FillWidth().Height(progressHeight).Shrink(0).MinWidth(0).
		Padding(8, 10).Gap(5).Background(p.soft).Border(1, p.line).Radius(9).Label("任务创建进度").Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			title := "创建进度"
			if progress.mode == "preflight" {
				title = "预检进度"
			}
			label(c, title, 12, p.text).FontWeight(600).Grow(1)
		})
		phaseText := taskCreationProgressPhaseText(progress.phase)
		phaseColor := p.blue
		if progress.failure != "" {
			phaseText = "创建未完成"
			if progress.mode == "preflight" {
				phaseText = "预检未完成"
			}
			phaseColor = p.amber
			if taskCreationProgressHasCleanupFailure(progress) {
				phaseText = "创建未完成，清理结果需检查"
			}
		} else if progress.interrupted {
			phaseText = "已中断"
			phaseColor = p.muted
		} else if progress.finished {
			phaseText = "创建流程已完成"
			if progress.mode == "preflight" {
				phaseText = "预检已完成"
			}
			phaseColor = p.green
		}
		message := strings.TrimSpace(progress.message)
		if progress.failure != "" {
			message = progress.failure
		}
		scopeName := progress.serviceName
		if progress.moduleName != "" {
			if scopeName != "" {
				scopeName += " · "
			}
			scopeName += progress.moduleName
		}
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			label(c, phaseText, 10, phaseColor).Shrink(0).SingleLine()
			if scopeName != "" {
				label(c, scopeName, 10, p.muted).Grow(1).MinWidth(0).SingleLine().Tooltip(scopeName)
			}
		})
		if message != "" {
			label(c, message, 9, p.muted).FillWidth().SingleLine().Tooltip(message)
		}
		ui.Scroll(c.Key("task-create-progress-list")).TrackScroll(&progress.scroll).FillWidth().Grow(1).MinHeight(0).Gap(3).Label("创建服务工作区进度").Children(func() {
			if len(progress.services) == 0 {
				label(c, "未能读取本次选择的服务；创建仍会继续。", 10, p.muted).FillWidth().Wrap()
				return
			}
			for _, service := range progress.services {
				servicePrepared := 0
				for _, module := range service.modules {
					if progress.mode == "preflight" && module.checked || progress.mode != "preflight" && module.prepared {
						servicePrepared++
					}
				}
				ui.Column(c.Key("task-create-progress-service-"+service.id)).FillWidth().Padding(6, 7).Gap(2).Background(p.surface).Border(1, p.line).Radius(7).Label("服务进度：" + service.name).Children(func() {
					ui.Row(c.Key("task-create-progress-service-header-" + service.id)).FillWidth().MinWidth(0).Height(20).Gap(6).AlignItems(ui.Center).Children(func() {
						label(c, service.name, 10, p.text).Grow(1).MinWidth(0).SingleLine().Tooltip(service.name)
						label(c, fmt.Sprintf("%d/%d", servicePrepared, len(service.modules)), 9, p.muted).Shrink(0).SingleLine()
					})
					for _, module := range service.modules {
						name := module.name
						if name == "" {
							name = module.id
						}
						status, color := taskCreationProgressModuleStatus(module, progress, p)
						ui.Row(c.Key("task-create-progress-module-"+service.id+"-"+module.id)).FillWidth().MinWidth(0).Height(19).Gap(6).Padding(0, 8).AlignItems(ui.Center).Label("模块进度：" + service.id + "/" + module.id).Children(func() {
							label(c, name, 9, p.muted).Grow(1).MinWidth(0).SingleLine().Tooltip(name)
							tooltip := module.message
							if module.failure != "" && module.failure != tooltip {
								tooltip = strings.TrimSpace(tooltip + "；失败原因：" + module.failure)
							}
							if module.cleanup != "" {
								tooltip = strings.TrimSpace(tooltip + "；清理信息：" + module.cleanup)
							}
							label(c, status, 9, color).Shrink(0).SingleLine().Tooltip(tooltip)
						})
					}
				})
			}
		})
	})
}

func taskCreationProgressHasCleanupFailure(progress *taskCreationProgressState) bool {
	if progress == nil {
		return false
	}
	for _, service := range progress.services {
		for _, module := range service.modules {
			if module.cleanupFail {
				return true
			}
		}
	}
	return false
}

func taskCreationProgressModuleStatus(module *taskCreationProgressModule, progress *taskCreationProgressState, p palette) (string, ui.Color) {
	if module == nil {
		return "等待开始", p.muted
	}
	if progress != nil && progress.mode == "preflight" {
		if module.checked {
			return "已检查", p.green
		}
		if progress.interrupted && module.status == "" {
			return "未检查", p.muted
		}
		if module.status == "" {
			return "未执行", p.muted
		}
		if progress.interrupted && module.status == workspace.TaskCreationProgressStarted {
			return "已中断", p.muted
		}
		if module.status == workspace.TaskCreationProgressFailed {
			return "失败", p.amber
		}
		if module.status == workspace.TaskCreationProgressStarted {
			return taskCreationProgressPhaseShort(module.phase), p.blue
		}
		return taskCreationProgressPhaseShort(module.phase), p.muted
	}
	if module.cleanupFail {
		return "清理需检查", p.amber
	}
	if module.rolledBack {
		return "已回滚", p.muted
	}
	if module.phase == workspace.TaskCreationPhaseRollback && module.status == workspace.TaskCreationProgressStarted {
		return "回滚中", p.blue
	}
	if module.prepared {
		return "已准备", p.green
	}
	if module.status == workspace.TaskCreationProgressFailed {
		return "失败", p.amber
	}
	if module.status == workspace.TaskCreationProgressStarted {
		return taskCreationProgressPhaseShort(module.phase), p.blue
	}
	if module.status == workspace.TaskCreationProgressCompleted {
		return taskCreationProgressPhaseShort(module.phase) + "完成", p.green
	}
	return "等待开始", p.muted
}

func taskCreationProgressPhaseText(phase workspace.TaskCreationProgressPhase) string {
	switch phase {
	case workspace.TaskCreationPhaseChecking:
		return "检查配置与分支"
	case workspace.TaskCreationPhasePreparingMaterials:
		return "准备需求资料"
	case workspace.TaskCreationPhaseRefreshingRemote:
		return "检查远程分支"
	case workspace.TaskCreationPhasePreparingWorktree:
		return "准备任务工作区"
	case workspace.TaskCreationPhaseCloningRepository:
		return "克隆仓库"
	case workspace.TaskCreationPhaseInitializingWorkspace:
		return "初始化工作区"
	case workspace.TaskCreationPhaseSavingManifest:
		return "保存任务清单"
	case workspace.TaskCreationPhaseWritingDocuments:
		return "写入任务资料"
	case workspace.TaskCreationPhaseLaunchingTools:
		return "启动工作区工具"
	case workspace.TaskCreationPhaseRollback:
		return "回滚本次创建"
	case workspace.TaskCreationPhaseCompleted:
		return "创建流程完成"
	default:
		return "创建任务"
	}
}

func taskCreationProgressPhaseShort(phase workspace.TaskCreationProgressPhase) string {
	switch phase {
	case workspace.TaskCreationPhaseChecking:
		return "检查中"
	case workspace.TaskCreationPhaseRefreshingRemote:
		return "检查远程中"
	case workspace.TaskCreationPhasePreparingWorktree:
		return "准备中"
	case workspace.TaskCreationPhaseCloningRepository:
		return "克隆中"
	case workspace.TaskCreationPhaseInitializingWorkspace:
		return "初始化中"
	case workspace.TaskCreationPhaseRollback:
		return "回滚中"
	default:
		return "处理中"
	}
}
