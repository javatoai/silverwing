package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type archiveBatchDeleteScope struct {
	home, taskRoot, configJSON, page string
}

type archiveBatchDeleteItem struct {
	Task            task
	Scope           taskMenuScope
	Preview         workspace.TaskDeletePreview
	Previewed       bool
	PreviewError    string
	ForceConfirmed  bool
	DiscardChanges  bool
	DeleteMaterials bool
	Status          string
	ResultText      string
}

type archiveBatchDeletePreviewJob struct {
	state      *archiveBatchDeleteState
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

type archiveBatchDeleteState struct {
	Open, PreviewLoading, Running, ExternalWindowsClosed bool
	StopRequested, AdvancePending, Closed, Started       bool
	Scope                                                archiveBatchDeleteScope
	Items                                                []archiveBatchDeleteItem
	Queue                                                []string
	QueuePosition                                        int
	CurrentKey, Error, Summary                           string
	Generation                                           uint64
	PreviewJob                                           *archiveBatchDeletePreviewJob
}

var archiveBatchPreviewSequence atomic.Uint64

// openArchiveBatchDelete starts a session-only preview for the selected
// archived task incarnations. The caller supplies the list in execution order.
func (s *app) openArchiveBatchDelete(selected []task) {
	if s.page != "archive" || s.busy || s.backend == nil || len(selected) == 0 {
		return
	}
	if previous := s.taskUnit.state.archiveBatchDelete; previous != nil {
		if previous.Running {
			s.lastMessage = "批量删除仍在执行，请先停止后续任务。"
			return
		}
		if previous.PreviewJob != nil {
			select {
			case <-previous.PreviewJob.done:
				previous.PreviewJob = nil
			default:
				previous.PreviewJob.cancel()
				s.lastMessage = "正在取消上一次删除检查，请稍后重试。"
				return
			}
		}
	}

	items := make([]archiveBatchDeleteItem, 0, len(selected))
	seen := make(map[string]bool, len(selected))
	for _, candidate := range selected {
		key := archiveBatchTaskKey(candidate)
		if key == "" || seen[key] {
			continue
		}
		current := s.taskUnit.taskByID(candidate.id)
		if archiveBatchTaskKey(current) != key {
			continue
		}
		scope, valid := s.taskMenuScopeFor(current)
		if !valid || !scope.archived || scope.page != "archive" {
			continue
		}
		seen[key] = true
		items = append(items, archiveBatchDeleteItem{Task: current, Scope: scope, Status: "检查中"})
	}
	if len(items) == 0 {
		s.lastError = "所选归档任务已变化，请重新选择。"
		return
	}

	state := &archiveBatchDeleteState{
		Open:       true,
		Scope:      archiveBatchDeleteScope{home: s.snapshot.Home, taskRoot: s.configString("taskRoot"), configJSON: s.settingsUnit.state.configJSON, page: s.page},
		Items:      items,
		Generation: 1,
	}
	s.taskUnit.state.archiveBatchDelete = state
	s.startArchiveBatchDeletePreview(state)
}

func (s *app) startArchiveBatchDeletePreview(state *archiveBatchDeleteState) {
	if state == nil || !state.Open || state.Closed || s.backend == nil {
		return
	}
	update := taskContextUpdater(s.win, s.taskUnit.state.taskContextUpdate)
	if update == nil {
		state.Error = "删除检查暂不可用，请重新打开。"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &archiveBatchDeletePreviewJob{state: state, generation: state.Generation, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	state.PreviewJob = job
	state.PreviewLoading = true
	state.Error = ""
	backend, window := s.backend, s.win
	snapshot := make([]archiveBatchDeleteItem, len(state.Items))
	copy(snapshot, state.Items)
	go s.runArchiveBatchDeletePreview(job, snapshot, backend, update, window)
}

func (s *app) runArchiveBatchDeletePreview(job *archiveBatchDeletePreviewJob, items []archiveBatchDeleteItem, backend application.Backend, update func(func()), window *mygo.Window) {
	defer close(job.done)
	for _, item := range items {
		if job.ctx.Err() != nil {
			return
		}
		if item.Previewed {
			continue
		}
		if err := waitForTaskMenuScope(job.ctx, update, func() bool {
			return s.archiveBatchDeletePreviewCurrent(job, item)
		}, func() bool { return window != nil && window.IsDestroyed() }); err != nil {
			if job.ctx.Err() != nil {
				return
			}
			if s.stopArchiveBatchPreviewForGlobalDrift(job, update, window) {
				return
			}
			if !s.deliverArchiveBatchDeletePreview(job, update, window, item, workspace.TaskDeletePreview{}, err) {
				return
			}
			continue
		}

		requestID := fmt.Sprint(archiveBatchPreviewSequence.Add(1))
		action := application.Action{Kind: "task.delete-risks", TaskID: item.Task.id, Path: item.Task.path,
			Payload: map[string]any{"taskDeleteCreatedAt": item.Task.createdAt, "taskDeleteRequestID": requestID}}
		action = taskMenuContextAction(action, item.Scope)
		if err := s.acquireBackend(job.ctx); err != nil {
			if job.ctx.Err() != nil {
				return
			}
			if !s.deliverArchiveBatchDeletePreview(job, update, window, item, workspace.TaskDeletePreview{}, err) {
				return
			}
			continue
		}
		result, err := backend.Dispatch(job.ctx, action)
		s.releaseBackend()
		if job.ctx.Err() != nil {
			return
		}
		if s.stopArchiveBatchPreviewForGlobalDrift(job, update, window) {
			return
		}
		var preview workspace.TaskDeletePreview
		if err == nil {
			preview, err = decodeArchiveBatchDeletePreview(result.Data)
			if err == nil && preview.Force.CanForce && (strings.TrimSpace(preview.Force.Guard) == "" || !archiveBatchSamePath(preview.Force.TaskPath, item.Task.path)) {
				err = errors.New("强制回收预检未能核对任务目录身份")
			}
		}
		if !s.deliverArchiveBatchDeletePreview(job, update, window, item, preview, err) {
			return
		}
	}
	update(func() {
		if s.archiveBatchDeleteJobCurrent(job) {
			state := job.state
			state.PreviewLoading = false
			if s.win != nil {
				s.win.Invalidate()
			}
		}
	})
}

func archiveBatchSamePath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(filepath.Clean(strings.TrimSpace(left)))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(strings.TrimSpace(right)))
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}

func (s *app) stopArchiveBatchPreviewForGlobalDrift(job *archiveBatchDeletePreviewJob, update func(func()), window *mygo.Window) bool {
	if window != nil && window.IsDestroyed() {
		job.cancel()
		return true
	}
	changed := false
	update(func() {
		if s.archiveBatchDeleteJobCurrent(job) && !s.archiveBatchDeleteGlobalScopeCurrent(job.state) {
			changed = true
			job.state.Error = "页面或应用配置已变化，删除检查已停止。"
			job.state.PreviewLoading = false
			for index := range job.state.Items {
				item := &job.state.Items[index]
				if item.Status == "检查中" || item.Status == "等待检查" {
					item.Status = "已跳过：页面或应用配置已变化"
				}
			}
			job.cancel()
			if s.win != nil {
				s.win.Invalidate()
			}
		}
	})
	return changed
}

func decodeArchiveBatchDeletePreview(value any) (workspace.TaskDeletePreview, error) {
	if value == nil {
		return workspace.TaskDeletePreview{}, errors.New("删除预检没有返回检查结果")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return workspace.TaskDeletePreview{}, err
	}
	var preview workspace.TaskDeletePreview
	if err := json.Unmarshal(encoded, &preview); err != nil {
		return workspace.TaskDeletePreview{}, errors.New("删除预检结果格式无效")
	}
	return preview, nil
}

func (s *app) archiveBatchDeletePreviewCurrent(job *archiveBatchDeletePreviewJob, item archiveBatchDeleteItem) bool {
	if !s.archiveBatchDeleteJobCurrent(job) || !s.archiveBatchDeleteGlobalScopeCurrent(job.state) {
		return false
	}
	current := s.taskUnit.taskByID(item.Task.id)
	actual, valid := s.taskMenuScopeFor(current)
	return valid && actual == item.Scope && archiveBatchTaskKey(current) == archiveBatchTaskKey(item.Task)
}

func (s *app) archiveBatchDeleteJobCurrent(job *archiveBatchDeletePreviewJob) bool {
	return job != nil && job.ctx != nil && job.ctx.Err() == nil &&
		s.taskUnit.state.archiveBatchDelete == job.state && job.state != nil && job.state.Open && !job.state.Closed &&
		job.state.PreviewJob == job && job.state.Generation == job.generation
}

func (s *app) deliverArchiveBatchDeletePreview(job *archiveBatchDeletePreviewJob, update func(func()), window *mygo.Window, item archiveBatchDeleteItem, preview workspace.TaskDeletePreview, err error) bool {
	ack := make(chan struct{}, 1)
	update(func() {
		defer func() { ack <- struct{}{} }()
		if !s.archiveBatchDeleteJobCurrent(job) {
			return
		}
		if !s.archiveBatchDeletePreviewCurrent(job, item) {
			err = errTaskContextChanged
			preview = workspace.TaskDeletePreview{}
		}
		for index := range job.state.Items {
			current := &job.state.Items[index]
			if archiveBatchTaskKey(current.Task) != archiveBatchTaskKey(item.Task) {
				continue
			}
			current.Previewed = err == nil
			current.PreviewError = ""
			current.Preview = preview
			current.ForceConfirmed = false
			current.DiscardChanges = false
			current.DeleteMaterials = false
			if err != nil {
				current.PreviewError = err.Error()
				current.Status = "检查失败"
			} else {
				current.Status = archiveBatchPreviewStatus(*current)
			}
			break
		}
		if s.win != nil {
			s.win.Invalidate()
		}
	})
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ack:
			return job.ctx.Err() == nil
		case <-job.ctx.Done():
			return false
		case <-ticker.C:
			if window != nil && window.IsDestroyed() {
				return false
			}
		}
	}
}

func (s *app) archiveBatchDeleteGlobalScopeCurrent(state *archiveBatchDeleteState) bool {
	if state == nil {
		return false
	}
	return s.page == state.Scope.page && s.snapshot.Home == state.Scope.home &&
		s.settingsUnit.state.configJSON == state.Scope.configJSON && s.configString("taskRoot") == state.Scope.taskRoot
}

func archiveBatchPreviewStatus(item archiveBatchDeleteItem) string {
	if !item.Previewed {
		return item.Status
	}
	if item.Preview.Force.CanForce {
		return "需确认强制回收"
	}
	if archiveBatchHasStatusFailure(item.Preview.Risks) {
		return "无法安全确认"
	}
	if archiveBatchHasOperationInProgress(item.Preview.Risks) || archiveBatchHasOperationInProgress(item.Preview.Force.Risks) {
		return "Git 操作进行中"
	}
	if item.Preview.Materials.CanDelete {
		return "可删除，可选删除资料"
	}
	return "可删除"
}

func archiveBatchHasStatusFailure(risks []domain.DeleteRisk) bool {
	for _, risk := range risks {
		if risk.StatusCheckError != nil {
			return true
		}
	}
	return false
}

func archiveBatchNeedsDiscardAck(preview workspace.TaskDeletePreview) bool {
	if preview.Force.CanForce {
		return false
	}
	for _, risk := range preview.Risks {
		if risk.StatusCheckError == nil {
			return true
		}
	}
	return false
}

func archiveBatchItemCanDelete(item archiveBatchDeleteItem) bool {
	if !item.Previewed || item.PreviewError != "" {
		return false
	}
	if item.Preview.Force.CanForce && (!item.ForceConfirmed || strings.TrimSpace(item.Preview.Force.Guard) == "") {
		return false
	}
	if archiveBatchHasStatusFailure(item.Preview.Risks) && !item.ForceConfirmed {
		return false
	}
	if archiveBatchHasOperationInProgress(item.Preview.Risks) || archiveBatchHasOperationInProgress(item.Preview.Force.Risks) {
		return false
	}
	if archiveBatchNeedsDiscardAck(item.Preview) && !item.DiscardChanges {
		return false
	}
	if item.DeleteMaterials && (!item.Preview.Materials.CanDelete || strings.TrimSpace(item.Preview.Materials.Guard) == "") {
		return false
	}
	return true
}

func archiveBatchHasOperationInProgress(risks []domain.DeleteRisk) bool {
	for _, risk := range risks {
		if risk.OperationInProgress != nil && strings.TrimSpace(*risk.OperationInProgress) != "" {
			return true
		}
	}
	return false
}

func (s *app) archiveBatchDeleteCountReady(state *archiveBatchDeleteState) int {
	if state == nil {
		return 0
	}
	count := 0
	for _, item := range state.Items {
		if archiveBatchItemCanDelete(item) {
			count++
		}
	}
	return count
}

func (s *app) archiveBatchDeleteConfirmationEnabled(state *archiveBatchDeleteState) bool {
	return state != nil && state.Open && !state.Started && !state.PreviewLoading && !state.Running && !state.StopRequested &&
		state.ExternalWindowsClosed && s.archiveBatchDeleteCountReady(state) > 0 && !s.busy && s.archiveBatchDeleteGlobalScopeCurrent(state)
}

func (s *app) confirmArchiveBatchDelete() {
	state := s.taskUnit.state.archiveBatchDelete
	if !s.archiveBatchDeleteConfirmationEnabled(state) {
		return
	}
	state.Queue = nil
	state.QueuePosition = 0
	for index := range state.Items {
		item := &state.Items[index]
		if !item.Previewed || item.PreviewError != "" {
			item.Status = "已跳过：预检未通过"
			continue
		}
		if !archiveBatchItemCanDelete(*item) {
			item.Status = "已跳过：尚未完成本项确认"
			continue
		}
		item.Status = "等待回收"
		state.Queue = append(state.Queue, archiveBatchTaskKey(item.Task))
	}
	if len(state.Queue) == 0 {
		state.Error = "没有完成确认且通过检查的任务。"
		return
	}
	state.Running = true
	state.Started = true
	state.StopRequested = false
	state.AdvancePending = true
	state.CurrentKey = ""
	state.Summary = ""
	state.Error = ""
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) stopArchiveBatchDeleteQueue() {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || !state.Running {
		return
	}
	state.StopRequested = true
	state.AdvancePending = true
	if s.win != nil {
		s.win.Invalidate()
	}
}

// advanceArchiveBatchDelete intentionally starts at most one ordinary delete.
// The action completion callback only marks the next item pending; the next
// action is launched by a later render pass, avoiding recursive dispatch.
func (s *app) advanceArchiveBatchDelete() {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || !state.Running || !state.AdvancePending || state.CurrentKey != "" || state.Closed {
		return
	}
	state.AdvancePending = false
	if state.StopRequested {
		s.finishArchiveBatchDeleteQueue(state, "已按要求停止后续任务。")
		return
	}
	if !s.archiveBatchDeleteGlobalScopeCurrent(state) {
		s.finishArchiveBatchDeleteQueue(state, "页面或应用配置已变化，已停止尚未开始的删除。")
		return
	}
	if s.busy {
		state.AdvancePending = true
		return
	}
	for state.QueuePosition < len(state.Queue) {
		key := state.Queue[state.QueuePosition]
		state.QueuePosition++
		index := s.archiveBatchDeleteItemIndex(state, key)
		if index < 0 {
			continue
		}
		item := &state.Items[index]
		current := s.taskUnit.taskByID(item.Task.id)
		actual, valid := s.taskMenuScopeFor(current)
		if !valid || actual != item.Scope || archiveBatchTaskKey(current) != key {
			item.Status = "已跳过：任务身份已变化"
			state.AdvancePending = true
			if s.win != nil {
				s.win.Invalidate()
			}
			return
		}
		if !archiveBatchItemCanDelete(*item) {
			item.Status = "已跳过：确认状态已变化"
			state.AdvancePending = true
			if s.win != nil {
				s.win.Invalidate()
			}
			return
		}
		if !state.ExternalWindowsClosed {
			item.Status = "已跳过：未确认外部窗口已关闭"
			state.AdvancePending = true
			if s.win != nil {
				s.win.Invalidate()
			}
			return
		}
		requestID := fmt.Sprintf("archive-batch-%d-%d", state.Generation, state.QueuePosition)
		action := archiveBatchDeleteAction(*item, requestID)
		item.Status = "正在移入回收站"
		state.CurrentKey = key
		scope := item.Scope
		s.dispatchTaskContextActionWithCompletion(action, &scope, func(result application.ActionResult, actionErr, refreshErr error) {
			s.completeArchiveBatchDeleteItem(state, key, result, actionErr, refreshErr)
		})
		return
	}
	s.finishArchiveBatchDeleteQueue(state, "所选任务处理完成。")
}

func archiveBatchDeleteAction(item archiveBatchDeleteItem, requestID string) application.Action {
	forceDiscard := item.DiscardChanges && !item.Preview.Force.CanForce
	payload := map[string]any{
		"forceDiscard":        forceDiscard,
		"forceGuard":          "",
		"deleteMaterials":     item.DeleteMaterials,
		"materialsGuard":      item.Preview.Materials.Guard,
		"taskDeleteCreatedAt": item.Task.createdAt,
		"taskDeleteRequestID": requestID,
	}
	if item.ForceConfirmed {
		payload["forceGuard"] = item.Preview.Force.Guard
	}
	return application.Action{Kind: "task.delete", TaskID: item.Task.id, Path: item.Task.path, Payload: payload}
}

func (s *app) archiveBatchDeleteItemIndex(state *archiveBatchDeleteState, key string) int {
	for index := range state.Items {
		if archiveBatchTaskKey(state.Items[index].Task) == key {
			return index
		}
	}
	return -1
}

func (s *app) completeArchiveBatchDeleteItem(state *archiveBatchDeleteState, key string, result application.ActionResult, actionErr, refreshErr error) {
	if state == nil || state != s.taskUnit.state.archiveBatchDelete || state.Closed {
		return
	}
	index := s.archiveBatchDeleteItemIndex(state, key)
	if index < 0 {
		state.CurrentKey = ""
		state.AdvancePending = true
		return
	}
	item := &state.Items[index]
	state.CurrentKey = ""
	switch {
	case actionErr != nil:
		item.Status = "失败：" + actionErr.Error()
	case strings.HasPrefix(result.Message, application.TaskDeleteCleanupWarningMessage):
		item.Status = "已回收，工作区登记待清理"
		item.ResultText = result.Text
	case strings.TrimSpace(result.Message) != "":
		item.Status = "已移入回收站"
	default:
		item.Status = "已移入回收站"
	}
	if actionErr == nil && (strings.HasPrefix(result.Message, application.TaskDeleteCleanupWarningMessage) || s.taskUnit.taskByID(item.Task.id).id == "") {
		selection := s.taskIndexBrowsingFor(true).archiveBatchSelected
		delete(selection, archiveBatchTaskKey(item.Task))
	}
	if refreshErr != nil {
		state.StopRequested = true
		state.Error = "刷新归档任务失败，已停止后续删除：" + refreshErr.Error()
		if actionErr == nil {
			item.Status = "结果待核实：刷新归档失败"
			item.ResultText = refreshErr.Error()
		}
	}
	if !s.archiveBatchDeleteGlobalScopeCurrent(state) {
		state.StopRequested = true
		state.Error = "页面或应用配置已变化，已停止尚未开始的删除。"
	}
	state.AdvancePending = true
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) finishArchiveBatchDeleteQueue(state *archiveBatchDeleteState, summary string) {
	if state == nil {
		return
	}
	state.Running = false
	state.StopRequested = false
	state.AdvancePending = false
	state.CurrentKey = ""
	state.Queue = nil
	state.QueuePosition = 0
	for index := range state.Items {
		if state.Items[index].Status == "等待回收" {
			state.Items[index].Status = "已跳过：" + firstNonEmpty(strings.TrimSpace(summary), "未执行")
		}
	}
	state.Summary = archiveBatchDeleteSummary(state)
	if strings.TrimSpace(summary) != "" && strings.TrimSpace(summary) != "所选任务处理完成。" {
		state.Summary += " · " + summary
	}
	if state.Error != "" {
		state.Summary += " · " + state.Error
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) closeArchiveBatchDeleteDialog() {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || state.Running {
		return
	}
	state.Open = false
	if state.PreviewJob != nil {
		state.PreviewLoading = false
		state.PreviewJob.cancel()
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) observeArchiveBatchDelete() {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil {
		return
	}
	if state.PreviewJob != nil {
		select {
		case <-state.PreviewJob.done:
			state.PreviewJob = nil
			state.PreviewLoading = false
		default:
		}
	}
	if state.Closed {
		return
	}
	if state.Running && !state.Open {
		// Escape or backdrop dismissal must not hide an active deletion queue.
		state.Open = true
	}
	if !state.Open && state.PreviewJob != nil {
		state.PreviewLoading = false
		state.PreviewJob.cancel()
	}
	if state.Open && state.PreviewLoading && !s.archiveBatchDeleteGlobalScopeCurrent(state) {
		state.Error = "页面或应用配置已变化，删除检查已停止。"
		if state.PreviewJob != nil {
			state.PreviewJob.cancel()
		}
		state.PreviewLoading = false
	}
	if state.Running {
		if !s.archiveBatchDeleteGlobalScopeCurrent(state) {
			state.StopRequested = true
			if state.CurrentKey == "" {
				state.AdvancePending = true
			}
		}
		s.advanceArchiveBatchDelete()
	}
	if !state.Open && !state.Running && state.PreviewJob == nil {
		s.taskUnit.state.archiveBatchDelete = nil
	}
}

func (s *app) stopArchiveBatchDelete() {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil {
		return
	}
	state.Closed = true
	state.Open = false
	state.StopRequested = true
	if state.PreviewJob != nil {
		state.PreviewLoading = false
		state.PreviewJob.cancel()
	}
}

func (s *app) drainArchiveBatchDelete(ctx context.Context) error {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || state.PreviewJob == nil {
		return nil
	}
	select {
	case <-state.PreviewJob.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待批量删除预检结束：%w", ctx.Err())
	}
}

func (s *app) renderArchiveBatchDelete(c *ui.Context, p palette) {
	s.observeArchiveBatchDelete()
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || !state.Open || state.Closed {
		return
	}
	ui.DialogBase(c.Key("archive-batch-delete-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		width, height := c.Size()
		panel.Width(maxFloat32(1, minFloat32(920, width-48))).MaxHeight(maxFloat32(1, minFloat32(760, height-48))).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "delete", 18, p.danger)
				label(c, fmt.Sprintf("删除所选归档任务（%d）", len(state.Items)), 17, p.text).FontWeight(600).Grow(1)
			})
			label(c, "任务目录和工作区将移入系统回收站，本地与远程分支保留。任务资料目录默认保留；未完成逐项确认的任务会跳过。", 12, p.text).Wrap()
			ui.Box(c).FillWidth().Padding(10, 12).Radius(9).Background(p.amberBg).Border(1, p.line).Children(func() {
				label(c, "请先关闭 Codex、IDE 和文件管理器中打开的相关任务或目录。正在使用的文件可能导致回收失败。", 11, p.danger).Wrap()
			})
			ui.Checkbox(c.Key("archive-batch-delete-external"), &state.ExternalWindowsClosed, "我已关闭上述外部窗口和文件夹").FillWidth().Disabled(state.Running)
			if state.PreviewLoading {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(15, 15).TextColor(p.blue).Label("检查中")
					label(c, "正在逐项检查工作区和资料目录…", 12, p.muted)
				})
			}
			if !state.PreviewLoading && !state.Running && !state.Started && s.archiveBatchDeleteGlobalScopeCurrent(state) && s.archiveBatchDeleteHasPreviewFailures(state) {
				if action(c, "archive-batch-delete-retry-preview", "refresh", "重新检查失败项", p, false) {
					s.archiveBatchDeletePreviewRetry()
				}
			}
			ui.Scroll(c.Key("archive-batch-delete-items")).FillWidth().MinHeight(0).MaxHeight(maxFloat32(1, minFloat32(450, height-360))).Padding(3).Gap(8).Children(func() {
				for index := range state.Items {
					s.renderArchiveBatchDeleteItem(c, p, state, index)
				}
			})
			if state.Error != "" {
				label(c, state.Error, 12, p.danger).Wrap().Selectable()
			}
			if state.Summary != "" {
				label(c, state.Summary, 12, p.blue).Wrap()
			}
			if state.Running {
				current := state.CurrentKey
				status := "正在按确认顺序处理任务…"
				if current != "" {
					status = "正在处理：" + s.archiveBatchDeleteTaskTitle(state, current)
				}
				label(c, status, 12, p.muted).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if state.Running {
					if action(c, "archive-batch-delete-stop", "close", "停止后续删除", p, false) {
						s.stopArchiveBatchDeleteQueue()
					}
				} else if action(c, "archive-batch-delete-cancel", "close", "关闭", p, false) {
					s.closeArchiveBatchDeleteDialog()
				}
				ready := s.archiveBatchDeleteCountReady(state)
				confirmLabel := fmt.Sprintf("移入 %d 项回收站", ready)
				if state.Started && !state.Running {
					confirmLabel = "本批已处理"
				}
				button := ui.ButtonBase(c.Key("archive-batch-delete-confirm")).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).Label(confirmLabel).Tooltip(confirmLabel).Disabled(!s.archiveBatchDeleteConfirmationEnabled(state))
				button.Background(p.danger).Children(func() {
					icon(c, "delete", 16, ui.Hex("#FFFFFF"))
					label(c, confirmLabel, 12, ui.Hex("#FFFFFF")).NoWrap()
				})
				if button.Clicked() && s.archiveBatchDeleteConfirmationEnabled(state) {
					s.confirmArchiveBatchDelete()
				}
			})
		})
	})
}

func (s *app) renderArchiveBatchDeleteItem(c *ui.Context, p palette, state *archiveBatchDeleteState, index int) {
	if state == nil || index < 0 || index >= len(state.Items) {
		return
	}
	item := &state.Items[index]
	ui.Column(c.Key("archive-batch-delete-item-"+fmt.Sprint(index))).FillWidth().Padding(11, 12).Gap(7).Radius(9).Background(p.soft).Border(1, p.line).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, firstNonEmpty(strings.TrimSpace(item.Task.title), item.Task.id), 13, p.text).FontWeight(600).Grow(1).Wrap()
			statusColor := p.muted
			if strings.Contains(item.Status, "失败") || strings.Contains(item.Status, "无法") || strings.Contains(item.Status, "进行中") {
				statusColor = p.danger
			} else if strings.Contains(item.Status, "回收") || strings.Contains(item.Status, "移入回收站") {
				statusColor = p.green
			}
			label(c, item.Status, 11, statusColor).Wrap()
		})
		label(c, item.Task.path, 11, p.muted).Wrap().Selectable()
		if item.PreviewLoadingPlaceholder() {
			return
		}
		if item.PreviewError != "" {
			label(c, "无法安全确认："+item.PreviewError, 11, p.danger).Wrap().Selectable()
			return
		}
		if !item.Previewed {
			return
		}
		if !item.Preview.Force.CanForce {
			for riskIndex, risk := range item.Preview.Risks {
				label(c, "• "+taskDeleteRiskSummary(risk, riskIndex), 11, p.danger).Wrap().Selectable()
			}
		}
		if item.Preview.Force.Reason != "" {
			label(c, item.Preview.Force.Reason, 11, p.danger).Wrap().Selectable()
		}
		if item.Preview.Force.CanForce {
			label(c, "强制回收目标：", 11, p.danger).Wrap()
			if item.Preview.Force.TaskPath != "" {
				label(c, "任务目录："+item.Preview.Force.TaskPath, 11, p.text).Wrap().Selectable()
			}
			for _, path := range item.Preview.Force.Workspaces {
				label(c, "工作区："+path, 11, p.text).Wrap().Selectable()
			}
			for riskIndex, risk := range item.Preview.Force.Risks {
				label(c, "• "+taskDeleteRiskSummary(risk, riskIndex), 11, p.danger).Wrap().Selectable()
			}
			ui.Checkbox(c.Key("archive-batch-force-"+fmt.Sprint(index)), &item.ForceConfirmed, "我已核对路径和工作区风险，确认相关文件及本地改动可以一起移入回收站").FillWidth().Disabled(state.Running || state.PreviewLoading)
		}
		if archiveBatchNeedsDiscardAck(item.Preview) {
			ui.Checkbox(c.Key("archive-batch-discard-"+fmt.Sprint(index)), &item.DiscardChanges, "我确认将此项中显示的未提交改动或未推送提交一并移入回收站").FillWidth().Disabled(state.Running || state.PreviewLoading)
		}
		materials := item.Preview.Materials
		if materials.CanDelete {
			ui.Checkbox(c.Key("archive-batch-materials-"+fmt.Sprint(index)), &item.DeleteMaterials, "同时删除任务资料目录").FillWidth().Disabled(state.Running || state.PreviewLoading)
		}
		if materials.Root != "" {
			label(c, materials.Root, 11, p.muted).Wrap().Selectable()
		}
		if materials.Reason != "" {
			label(c, materials.Reason, 11, p.muted).Wrap()
		}
		if item.ResultText != "" {
			label(c, item.ResultText, 11, p.amber).Wrap().Selectable()
		}
	})
}

func (item archiveBatchDeleteItem) PreviewLoadingPlaceholder() bool {
	return !item.Previewed && item.PreviewError == "" && item.Status == "检查中"
}

func (s *app) archiveBatchDeleteTaskTitle(state *archiveBatchDeleteState, key string) string {
	if index := s.archiveBatchDeleteItemIndex(state, key); index >= 0 {
		return firstNonEmpty(state.Items[index].Task.title, state.Items[index].Task.id)
	}
	return "所选任务"
}

func (s *app) archiveBatchDeletePreviewRetry() {
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || state.Running || !state.Open || !s.archiveBatchDeleteGlobalScopeCurrent(state) {
		return
	}
	for index := range state.Items {
		item := &state.Items[index]
		if item.Previewed {
			continue
		}
		item.PreviewError = ""
		item.Status = "检查中"
	}
	state.Generation++
	s.startArchiveBatchDeletePreview(state)
}

func (s *app) archiveBatchDeleteHasPreviewFailures(state *archiveBatchDeleteState) bool {
	if state == nil {
		return false
	}
	for _, item := range state.Items {
		if !item.Previewed && item.PreviewError != "" {
			return true
		}
	}
	return false
}

func archiveBatchDeleteSummary(state *archiveBatchDeleteState) string {
	if state == nil {
		return ""
	}
	counts := map[string]int{}
	for _, item := range state.Items {
		switch {
		case strings.HasPrefix(item.Status, "已移入回收站"):
			counts["done"]++
		case strings.HasPrefix(item.Status, "已回收"):
			counts["warning"]++
		case strings.HasPrefix(item.Status, "失败"):
			counts["failed"]++
		case strings.HasPrefix(item.Status, "结果待核实"):
			counts["unknown"]++
		case strings.HasPrefix(item.Status, "已跳过"):
			counts["skipped"]++
		}
	}
	return fmt.Sprintf("已回收 %d 项 · 登记待清理 %d 项 · 结果待核实 %d 项 · 失败 %d 项 · 跳过 %d 项", counts["done"], counts["warning"], counts["unknown"], counts["failed"], counts["skipped"])
}
