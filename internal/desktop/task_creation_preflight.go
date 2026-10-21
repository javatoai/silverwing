package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo"
)

type taskCreationPreflightRequest struct {
	form                      *taskCreationState
	token, home               string
	snapshotConfigFingerprint string
	previewSignature          string
	preview                   *taskCreationPreview
	action                    application.Action
	ctx                       context.Context
	cancel                    context.CancelFunc
	progress                  *taskCreationProgressState
	progressGeneration        uint64
	progressUpdates           func(workspace.TaskCreationProgress)
	cancelRequested           bool // UI-thread owned; distinguishes an explicit interrupt from stale work.
	done                      chan struct{}
}

type taskCreationPreflightJobState struct {
	current *taskCreationPreflightRequest // owned by the UI thread
	mu      sync.Mutex
	running map[*taskCreationPreflightRequest]struct{}
	closed  bool
}

func (s *app) ensureTaskCreationPreflights() *taskCreationPreflightJobState {
	if s == nil {
		return nil
	}
	if s.taskCreationPreflights == nil {
		s.taskCreationPreflights = &taskCreationPreflightJobState{running: make(map[*taskCreationPreflightRequest]struct{})}
	} else if s.taskCreationPreflights.running == nil {
		s.taskCreationPreflights.running = make(map[*taskCreationPreflightRequest]struct{})
	}
	return s.taskCreationPreflights
}

func cloneTaskCreationPreflightAction(action application.Action) application.Action {
	if action.Payload == nil {
		return action
	}
	payload := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		payload[key] = value
	}
	action.Payload = payload
	return action
}

func taskCreationSnapshotConfigFingerprint(snapshot application.Snapshot, config domain.AppConfig) (string, error) {
	encoded, err := json.Marshal(struct {
		ConfigJSON string           `json:"configJSON"`
		FormConfig domain.AppConfig `json:"formConfig"`
	}{ConfigJSON: snapshot.ConfigJSON, FormConfig: config})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// prepareTaskCreationPreflight binds a preview read to the exact form and
// preview that issued it. It deliberately does not inspect or mutate app-wide
// busy/cancellation state, so callers may also use it in headless proofs.
func (s *app) prepareTaskCreationPreflight(action application.Action) (*taskCreationPreflightRequest, error) {
	if s == nil || s.taskCreation == nil {
		return nil, errors.New("任务创建表单已关闭。")
	}
	if action.Kind != "task.create-preview" {
		return nil, errors.New("任务预检请求类型无效。")
	}
	form := s.taskCreation
	token := payloadString(action.Payload, "taskCreateToken")
	if token == "" || token != form.token {
		return nil, errors.New("任务预检身份已变化，请重新检查。")
	}
	preview := form.preview
	if preview == nil || form.phase != "preview" || preview.signature == "" || preview.signature != s.taskCreationSignature() {
		return nil, errors.New("任务信息已变化，请重新检查。")
	}
	fingerprint, err := taskCreationSnapshotConfigFingerprint(s.snapshot, form.config)
	if err != nil {
		return nil, fmt.Errorf("无法记录任务预检配置身份：%w", err)
	}

	jobs := s.ensureTaskCreationPreflights()
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskCreationPreflightRequest{
		form: form, token: token, home: s.snapshot.Home,
		snapshotConfigFingerprint: fingerprint, previewSignature: preview.signature,
		preview: preview, action: cloneTaskCreationPreflightAction(action),
		ctx: ctx, cancel: cancel, progress: form.creationProgress,
		progressGeneration: form.progressGeneration, done: make(chan struct{}),
	}
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		cancel()
		return nil, errors.New("任务预检读取已关闭。")
	}
	previous := form.preflight
	if jobs.current != nil && jobs.current != previous {
		oldCurrent := jobs.current
		if oldCurrent.form != nil && oldCurrent.form.preflight == oldCurrent {
			oldCurrent.form.preflight = nil
		}
		oldCurrent.cancel()
	}
	if previous != nil {
		previous.cancel()
	}
	form.preflight = request
	jobs.current = request
	jobs.running[request] = struct{}{}
	jobs.mu.Unlock()
	request.progressUpdates = s.taskCreationPreflightProgressUpdates(request)
	return request, nil
}

func (s *app) startTaskCreationPreflight(action application.Action) {
	if s == nil {
		return
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.taskCreationActionError(action, errors.New("任务预检后端或窗口不可用，请重试。"))
		return
	}
	request, err := s.prepareTaskCreationPreflight(action)
	if err != nil {
		s.taskCreationActionError(action, err)
		return
	}
	backend, win := s.backend, s.win
	win.Invalidate()
	go s.runTaskCreationPreflight(backend, win, request)
}

func executeTaskCreationPreflight(backend application.Backend, request *taskCreationPreflightRequest) (application.ActionResult, error) {
	if backend == nil || request == nil || request.ctx == nil {
		return application.ActionResult{}, errors.New("任务预检缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	var result application.ActionResult
	var err error
	if progressBackend, ok := backend.(application.TaskCreationProgressBackend); ok {
		result, err = progressBackend.DispatchTaskCreation(request.ctx, request.action, request.progressUpdates)
	} else {
		result, err = backend.Dispatch(request.ctx, request.action)
	}
	if request.ctx.Err() != nil {
		return application.ActionResult{}, errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) runTaskCreationPreflight(backend application.Backend, win *mygo.Window, request *taskCreationPreflightRequest) {
	defer s.completeTaskCreationPreflight(request)
	result, err := executeTaskCreationPreflight(backend, request)
	if win == nil || win.IsDestroyed() {
		return
	}
	updateDone := make(chan struct{})
	win.Update(func() {
		defer close(updateDone)
		s.finishTaskCreationPreflight(request, result, err)
	})
	select {
	case <-updateDone:
	case <-request.ctx.Done():
	}
}

func (s *app) taskCreationPreflightRelevant(request *taskCreationPreflightRequest) bool {
	return request != nil && request.ctx != nil && request.ctx.Err() == nil && !request.cancelRequested && s.taskCreationPreflightIdentityRelevant(request)
}

func (s *app) taskCreationPreflightIdentityRelevant(request *taskCreationPreflightRequest) bool {
	if s == nil || request == nil {
		return false
	}
	jobs := s.taskCreationPreflights
	if jobs == nil || request.ctx == nil {
		return false
	}
	jobs.mu.Lock()
	current, closed := jobs.current, jobs.closed
	jobs.mu.Unlock()
	if closed || !s.dialogOpen || s.form.mode != "task-create" || current != request || request.form == nil || s.taskCreation != request.form ||
		request.form.preflight != request || request.form.token != request.token ||
		payloadString(request.action.Payload, "taskCreateToken") != request.token ||
		request.form.preview != request.preview || request.preview == nil ||
		request.preview.signature != request.previewSignature || request.form.phase != "preview" ||
		request.form.preview.signature != s.taskCreationSignature() || s.snapshot.Home != request.home ||
		request.form.creationProgress != request.progress || request.form.progressGeneration != request.progressGeneration {
		return false
	}
	fingerprint, err := taskCreationSnapshotConfigFingerprint(s.snapshot, request.form.config)
	if err != nil || fingerprint != request.snapshotConfigFingerprint {
		return false
	}
	return true
}

func (s *app) retireStaleTaskCreationPreflight(request *taskCreationPreflightRequest) {
	if s == nil || request == nil || request.cancelRequested {
		return
	}
	jobs := s.taskCreationPreflights
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	if jobs.closed || jobs.current != request {
		jobs.mu.Unlock()
		return
	}
	jobs.current = nil
	form := request.form
	stillBound := form != nil && form.preflight == request
	if stillBound {
		form.preflight = nil
	}
	jobs.mu.Unlock()
	request.cancel()

	if stillBound && s.taskCreation == form && form.token == request.token && form.phase == "preview" && form.preview == request.preview {
		form.preview = nil
		form.phase = "editing"
		form.inlineError = "任务信息或配置已变化，预检已取消，请重新检查。"
	}
}

func (s *app) finishTaskCreationPreflight(request *taskCreationPreflightRequest, result application.ActionResult, err error) {
	if request != nil && request.cancelRequested {
		s.finishCancelledTaskCreationPreflight(request)
		return
	}
	if s == nil || !s.taskCreationPreflightRelevant(request) {
		s.retireStaleTaskCreationPreflight(request)
		return
	}
	jobs := s.taskCreationPreflights
	jobs.mu.Lock()
	if jobs.current == request {
		jobs.current = nil
	}
	jobs.mu.Unlock()
	if request.form.preflight == request {
		request.form.preflight = nil
	}
	if err != nil {
		s.taskCreationActionError(request.action, err)
	} else {
		s.taskCreationActionResult(request.action, result)
	}
}

func (s *app) finishCancelledTaskCreationPreflight(request *taskCreationPreflightRequest) {
	if s == nil || request == nil || request.form == nil || s.taskCreationPreflights == nil {
		return
	}
	identityRelevant := s.taskCreationPreflightIdentityRelevant(request)
	jobs := s.taskCreationPreflights
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		return
	}
	current := jobs.current == request
	if current {
		jobs.current = nil
	}
	form := request.form
	bound := form.preflight == request
	if bound {
		form.preflight = nil
	}
	jobs.mu.Unlock()

	if !current || !bound || s.taskCreation != form || !s.dialogOpen || s.form.mode != "task-create" || form.token != request.token || form.phase != "preview" || form.preview != request.preview {
		return
	}
	if request.progress != nil && form.creationProgress == request.progress && form.progressGeneration == request.progressGeneration {
		request.progress.cancelling = false
		request.progress.interrupted = true
		request.progress.finished = true
		request.progress.message = "预检已中断，没有继续创建任务。"
	}
	form.preview = nil
	form.phase = "editing"
	form.inlineError = ""
	if !identityRelevant {
		form.inlineError = "任务信息或配置已变化，预检已中断，请重新检查。"
	}
}

func (s *app) completeTaskCreationPreflight(request *taskCreationPreflightRequest) {
	if request == nil {
		return
	}
	request.cancel()
	jobs := s.taskCreationPreflights
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	if _, exists := jobs.running[request]; exists {
		delete(jobs.running, request)
		close(request.done)
	}
	jobs.mu.Unlock()
}

func (s *app) cancelTaskCreationPreflight(form *taskCreationState) {
	if s == nil || form == nil || form.preflight == nil {
		return
	}
	request := form.preflight
	form.preflight = nil
	request.cancel()
	if jobs := s.taskCreationPreflights; jobs != nil {
		jobs.mu.Lock()
		if jobs.current == request {
			jobs.current = nil
		}
		jobs.mu.Unlock()
	}
}

func (s *app) interruptTaskCreationPreflight(form *taskCreationState) bool {
	if s == nil || form == nil || form.phase != "preview" {
		return false
	}
	request := form.preflight
	if request == nil || request.cancelRequested {
		return false
	}
	request.cancelRequested = true
	if request.progress != nil && form.creationProgress == request.progress {
		request.progress.cancelling = true
		request.progress.message = "正在中断检查，请等待当前操作结束…"
	}
	request.cancel()
	return true
}

func (s *app) observeTaskCreationPreflight() {
	if s == nil || s.taskCreationPreflights == nil {
		return
	}
	jobs := s.taskCreationPreflights
	jobs.mu.Lock()
	request := jobs.current
	closed := jobs.closed
	jobs.mu.Unlock()
	if request == nil || closed || request.cancelRequested || s.taskCreationPreflightRelevant(request) {
		return
	}
	s.retireStaleTaskCreationPreflight(request)
}

func (s *app) closeTaskCreationPreflights(ctx context.Context) error {
	if s == nil {
		return nil
	}
	jobs := s.taskCreationPreflights
	if jobs == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	jobs.mu.Lock()
	jobs.closed = true
	if jobs.current != nil {
		jobs.current.cancel()
		jobs.current = nil
	}
	running := make([]*taskCreationPreflightRequest, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		running = append(running, request)
	}
	jobs.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待任务预检 worker 清理：%w", ctx.Err())
		}
	}
	return nil
}
