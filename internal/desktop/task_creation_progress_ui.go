package desktop

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type taskCreationProgressState struct {
	token      string
	generation uint64
	signature  string
	mode       string

	phase          workspace.TaskCreationProgressPhase
	message        string
	finished       bool
	cancelling     bool
	interrupted    bool
	failure        string
	cleanupFailure bool

	services []*taskCreationProgressService
	scroll   ui.ScrollState
	lastSeq  uint64
}

type taskCreationProgressService struct {
	id      string
	name    string
	status  workspace.TaskCreationProgressStatus
	message string
	modules []*taskCreationProgressModule
}

type taskCreationProgressModule struct {
	id          string
	name        string
	phase       workspace.TaskCreationProgressPhase
	status      workspace.TaskCreationProgressStatus
	message     string
	active      []taskCreationModulePhase
	failure     string
	cleanup     string
	checked     bool
	prepared    bool
	rolledBack  bool
	cleanupFail bool
}

type taskCreationModulePhase struct {
	phase   workspace.TaskCreationProgressPhase
	message string
}

type taskCreationProgressRequest struct {
	ctx        context.Context
	window     *mygo.Window
	state      *taskCreationState
	progress   *taskCreationProgressState
	token      string
	generation uint64
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
		token: state.token, generation: generation,
		mode:    "create",
		phase:   workspace.TaskCreationPhaseChecking,
		message: "正在检查任务信息与分支状态…",
	}
	if state.preview != nil {
		progress.signature = state.preview.signature
	}
	for _, service := range state.config.ServiceCatalog() {
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
		token: state.token, generation: state.progressGeneration,
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
		token: preflight.token, generation: state.progressGeneration,
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
		if request.window == nil || request.window.IsDestroyed() || request.ctx.Err() != nil && !taskCreationProgressAfterCancellation(event) {
			return
		}
		request.window.Update(func() { s.applyTaskCreationProgress(request, event) })
	}
}

func (s *app) applyTaskCreationProgress(request *taskCreationProgressRequest, event workspace.TaskCreationProgress) {
	if request == nil || request.ctx == nil || request.window == nil || request.window.IsDestroyed() {
		return
	}
	s.applyTaskCreationProgressEvent(request, event)
}

func taskCreationProgressAfterCancellation(event workspace.TaskCreationProgress) bool {
	return event.Phase == workspace.TaskCreationPhaseRollback || event.Status == workspace.TaskCreationProgressCancelled
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
	} else if request.ctx.Err() != nil && (!taskCreationProgressAfterCancellation(event) || !s.operationCancelling) {
		return
	}
	if request.state == nil || s.taskUnit.state.taskCreation != request.state || !s.dialogOpen || s.form.mode != "task-create" ||
		request.kind == "task.create" && (!s.busy || s.operationKind != "task.create" || request.state.phase != "creating") ||
		request.state.token != request.token || request.state.preview == nil ||
		request.state.progressGeneration != request.generation ||
		request.state.creationProgress != request.progress || request.progress.generation != request.generation ||
		s.snapshot.Home != request.home || s.settingsUnit.state.configJSON != request.configJSON ||
		request.state.preview.signature != request.signature || request.state.preview.signature != s.taskCreationSignature() || event.Sequence == 0 || event.Sequence <= request.progress.lastSeq {
		return
	}
	progress := request.progress
	progress.lastSeq = event.Sequence
	if event.ServiceID == "" && event.ModuleID == "" {
		progress.applyTaskPhase(event)
		return
	}
	service := taskCreationProgressFindService(progress, event.ServiceID)
	if service == nil {
		return
	}
	if event.ModuleID == "" {
		service.status = event.Status
		service.message = strings.TrimSpace(event.Message)
		return
	}
	for _, module := range service.modules {
		if module.id == event.ModuleID {
			module.applyPhase(event)
			return
		}
	}
}

func (progress *taskCreationProgressState) applyTaskPhase(event workspace.TaskCreationProgress) {
	progress.phase = event.Phase
	progress.message = strings.TrimSpace(event.Message)
	if event.Phase == workspace.TaskCreationPhaseRollback && event.Status == workspace.TaskCreationProgressFailed {
		progress.cleanupFailure = true
	}
	if event.Phase == workspace.TaskCreationPhaseCompleted && event.Status == workspace.TaskCreationProgressCompleted {
		progress.finished = true
	}
	if progress.mode == "preflight" && event.Phase == workspace.TaskCreationPhaseChecking {
		switch event.Status {
		case workspace.TaskCreationProgressFailed:
			progress.finished = true
			progress.failure = progress.message
		case workspace.TaskCreationProgressCompleted:
			progress.finished = true
		case workspace.TaskCreationProgressCancelled:
			progress.finished = true
			progress.interrupted = true
		}
	}
}

func (module *taskCreationProgressModule) applyPhase(event workspace.TaskCreationProgress) {
	module.phase = event.Phase
	module.status = event.Status
	module.message = strings.TrimSpace(event.Message)
	if event.Phase == workspace.TaskCreationPhaseRollback {
		module.active = nil
		module.prepared = false
		switch event.Status {
		case workspace.TaskCreationProgressCompleted:
			module.rolledBack = true
			module.cleanupFail = false
			module.cleanup = ""
		case workspace.TaskCreationProgressFailed:
			module.cleanupFail = true
			module.cleanup = module.message
		}
		return
	}

	module.active = slices.DeleteFunc(module.active, func(active taskCreationModulePhase) bool { return active.phase == event.Phase })
	switch event.Status {
	case workspace.TaskCreationProgressStarted:
		module.active = append(module.active, taskCreationModulePhase{phase: event.Phase, message: module.message})
	case workspace.TaskCreationProgressFailed:
		module.failure = module.message
	case workspace.TaskCreationProgressCancelled:
		module.active = nil
	case workspace.TaskCreationProgressCompleted:
		switch event.Phase {
		case workspace.TaskCreationPhaseChecking:
			module.checked = true
		case workspace.TaskCreationPhaseInitializingWorkspace:
			module.prepared = true
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

func (s *app) failTaskCreationProgress(state *taskCreationState, cause error) {
	if state == nil || state.creationProgress == nil {
		return
	}
	progress := state.creationProgress
	progress.finished = true
	progress.cancelling = false
	if cause == context.Canceled && !taskCreationProgressHasCleanupFailure(progress) {
		progress.interrupted = true
		progress.message = "已中断任务创建。"
		if progress.mode == "preflight" {
			progress.message = "已中断任务预检。"
		}
		return
	}
	progress.failure = strings.TrimSpace(cause.Error())
}

func (s *app) renderTaskCreationProgress(c *ui.Context, p palette, progress *taskCreationProgressState) {
	if progress == nil {
		return
	}
	preflight := progress.mode == "preflight"
	cancelling := progress.cancelling || !preflight && s.operationCancelling
	total, done := 0, 0
	for _, service := range progress.services {
		total += len(service.modules)
		done += completedTaskCreationModules(service.modules, preflight)
	}
	_, windowHeight := c.Size()
	progressHeight := min(float32(280), max(float32(210), windowHeight*0.32))
	ui.Column(c.Key("task-create-progress")).FillWidth().Height(progressHeight).Shrink(0).MinWidth(0).
		Padding(8, 10).Gap(5).Background(p.soft).Border(1, p.line).Radius(9).Label("任务创建进度").Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			title := "创建进度"
			if preflight {
				title = "预检进度"
			}
			label(c, title, 12, p.text).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			if total > 0 {
				label(c, fmt.Sprintf("已完成 %d/%d 个模块", done, total), 10, p.muted).Shrink(0).SingleLine()
			}
		})
		phaseText := taskCreationProgressPhaseText(progress.phase)
		phaseColor := p.blue
		if progress.failure != "" {
			phaseText = "创建未完成"
			if preflight {
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
			if preflight {
				phaseText = "预检已完成"
			}
			phaseColor = p.green
		} else if cancelling && progress.phase != workspace.TaskCreationPhaseRollback {
			phaseText = "正在中断"
		}
		message := strings.TrimSpace(progress.message)
		if progress.failure != "" {
			message = progress.failure
		}
		label(c, phaseText, 10, phaseColor).FillWidth().SingleLine()
		if message != "" {
			label(c, message, 9, p.muted).FillWidth().SingleLine().Tooltip(message)
		}
		ui.Scroll(c.Key("task-create-progress-list")).TrackScroll(&progress.scroll).FillWidth().Grow(1).MinHeight(0).Gap(3).Label("创建服务工作区进度").Children(func() {
			for _, service := range progress.services {
				ui.Column(c.Key("task-create-progress-service-"+service.id)).FillWidth().Padding(6, 7).Gap(2).Background(p.surface).Border(1, p.line).Radius(7).Label("服务进度：" + service.name).Children(func() {
					ui.Row(c.Key("task-create-progress-service-header-" + service.id)).FillWidth().MinWidth(0).Height(20).Gap(6).AlignItems(ui.Center).Children(func() {
						label(c, service.name, 10, p.text).Grow(1).MinWidth(0).SingleLine().Tooltip(service.name)
						switch service.status {
						case workspace.TaskCreationProgressFailed:
							label(c, "失败", 9, p.amber).Shrink(0).SingleLine().Tooltip(service.message)
						case workspace.TaskCreationProgressCancelled:
							label(c, "已中断", 9, p.muted).Shrink(0).SingleLine().Tooltip(service.message)
						}
						label(c, fmt.Sprintf("%d/%d", completedTaskCreationModules(service.modules, preflight), len(service.modules)), 9, p.muted).Shrink(0).SingleLine()
					})
					for _, module := range service.modules {
						name := module.name
						if name == "" {
							name = module.id
						}
						status, color := taskCreationProgressModuleStatus(module, progress, cancelling, p)
						ui.Row(c.Key("task-create-progress-module-"+service.id+"-"+module.id)).FillWidth().MinWidth(0).Height(19).Gap(6).Padding(0, 8).AlignItems(ui.Center).Label("模块进度：" + service.id + "/" + module.id).Children(func() {
							label(c, name, 9, p.muted).Grow(1).MinWidth(0).SingleLine().Tooltip(name)
							tooltip := module.message
							if len(module.active) > 0 && module.failure == "" {
								tooltip = module.active[len(module.active)-1].message
							}
							if module.failure != "" && module.failure != tooltip {
								tooltip = strings.TrimSpace(tooltip + "；失败原因：" + module.failure)
							}
							if module.cleanup != "" && module.cleanup != tooltip {
								tooltip = strings.TrimSpace(tooltip + "；清理信息：" + module.cleanup)
							}
							label(c, status, 9, color).Shrink(0).SingleLine().Tooltip(tooltip)
						})
						if module.cleanupFail && module.cleanup != "" {
							label(c, module.cleanup, 9, p.muted).FillWidth().MinWidth(0).Wrap()
						}
					}
				})
			}
		})
	})
}

func completedTaskCreationModules(modules []*taskCreationProgressModule, preflight bool) int {
	done := 0
	for _, module := range modules {
		if preflight && module.checked || !preflight && module.prepared {
			done++
		}
	}
	return done
}

func taskCreationProgressHasCleanupFailure(progress *taskCreationProgressState) bool {
	if progress == nil {
		return false
	}
	if progress.cleanupFailure {
		return true
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

func taskCreationProgressModuleStatus(module *taskCreationProgressModule, progress *taskCreationProgressState, cancelling bool, p palette) (string, ui.Color) {
	switch {
	case module.cleanupFail:
		return "清理需检查", p.amber
	case module.rolledBack:
		if module.failure != "" {
			return "失败，已回滚", p.amber
		}
		return "已回滚", p.muted
	case module.phase == workspace.TaskCreationPhaseRollback && module.status == workspace.TaskCreationProgressStarted:
		return "回滚中", p.blue
	case module.failure != "" || module.status == workspace.TaskCreationProgressFailed:
		return "失败", p.amber
	case module.prepared:
		return "已准备", p.green
	case progress.mode == "preflight" && module.checked:
		return "已检查", p.green
	case module.status == workspace.TaskCreationProgressCancelled:
		return "已中断", p.muted
	case len(module.active) > 0:
		if progress.finished {
			return "已中断", p.muted
		}
		if cancelling {
			return "正在中断", p.muted
		}
		return taskCreationProgressPhaseShort(module.active[len(module.active)-1].phase), p.blue
	case progress.finished:
		return "未执行", p.muted
	case module.status == workspace.TaskCreationProgressCompleted:
		switch module.phase {
		case workspace.TaskCreationPhaseChecking, workspace.TaskCreationPhaseRefreshingRemote:
			return "已检查", p.muted
		case workspace.TaskCreationPhasePreparingWorktree, workspace.TaskCreationPhaseCloningRepository:
			return "待初始化", p.muted
		}
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
	case workspace.TaskCreationPhasePreparingWorktree, workspace.TaskCreationPhaseProvisioningWorkspaces:
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
