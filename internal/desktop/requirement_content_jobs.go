package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"silverwing-go/internal/application"
)

type requirementContentJob struct {
	key, home, executable, taskPath, taskCreatedAt string
	reader                                         *requirementReaderState
	identity                                       requirementListIdentity
	identityContext                                requirementIdentityContext
	hasIdentityContext                             bool
	action                                         application.Action
	ctx                                            context.Context
	cancel                                         context.CancelFunc
	done                                           chan struct{}
}

type requirementContentJobState struct {
	requests map[string]*requirementContentJob // owned by the window thread
	visible  string
	entered  *requirementReaderState
	mu       sync.Mutex
	running  map[*requirementContentJob]struct{}
	closed   bool
}

func requirementContentRequestKind(action application.Action) string {
	if action.Kind == "requirements.attachment" && payloadString(action.Payload, "scope") == "comment" {
		return "requirements.attachment:comment"
	}
	return action.Kind
}

func independentRequirementContentAction(kind string) bool {
	return kind == "requirements.read" || kind == "requirements.comments" || kind == "requirements.attachment"
}

func (s *app) ensureRequirementContentJobs() *requirementContentJobState {
	if s.requirementUnit.state.requirementContentJobs == nil {
		s.requirementUnit.state.requirementContentJobs = &requirementContentJobState{
			requests: make(map[string]*requirementContentJob), running: make(map[*requirementContentJob]struct{}),
		}
	}
	return s.requirementUnit.state.requirementContentJobs
}

func (s *app) beginRequirementContentJob(action application.Action) (*requirementContentJob, error) {
	reader := s.requirementUnit.requirementReaderForAction(action)
	if reader == nil {
		return nil, errors.New("需求读取身份已变化，请重试。")
	}
	state := s.ensureRequirementContentJobs()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return nil, errors.New("需求读取已关闭。")
	}
	key := requirementReaderKey(reader.taskID, reader.url) + "\x00" + requirementContentRequestKind(action)
	if previous := state.requests[key]; previous != nil {
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &requirementContentJob{key: key, home: s.snapshot.Home,
		executable: s.configString("meegleExecutablePath"), reader: reader,
		identity: s.currentRequirementListIdentity(),
		action:   cloneRequirementContentAction(action), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if identityState := s.requirementUnit.state.requirementIdentityJobs; identityState != nil && identityState.hasContext {
		request.identityContext, request.hasIdentityContext = identityState.context, true
	}
	if action.TaskID != "" {
		task := s.taskUnit.taskByID(action.TaskID)
		request.taskPath, request.taskCreatedAt = task.path, task.createdAt
	}
	state.requests[key] = request
	state.mu.Lock()
	state.running[request] = struct{}{}
	state.mu.Unlock()
	return request, nil
}

func cloneRequirementContentAction(action application.Action) application.Action {
	copy := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		copy[key] = value
	}
	action.Payload = copy
	return action
}

func (s *app) requirementContentJobRelevant(request *requirementContentJob) bool {
	state := s.requirementUnit.state.requirementContentJobs
	if state == nil || request == nil || state.requests[request.key] != request ||
		s.snapshot.Home != request.home || s.configString("meegleExecutablePath") != request.executable ||
		s.currentRequirementListIdentity() != request.identity ||
		s.requirementUnit.requirementReaderForAction(request.action) != request.reader {
		return false
	}
	if request.hasIdentityContext && s.currentRequirementIdentityContext() != request.identityContext {
		return false
	}
	if request.action.TaskID != "" {
		task := s.taskUnit.taskByID(request.action.TaskID)
		if task.path != request.taskPath || task.createdAt != request.taskCreatedAt || strings.TrimSpace(task.requirementURL) != request.reader.url {
			return false
		}
	}
	return true
}

func (s *app) dispatchIndependentRequirementContent(action application.Action) bool {
	if !independentRequirementContentAction(action.Kind) {
		return false
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.applyRequirementContentFailure(action, errors.New("需求后端或窗口不可用，请重试。"))
		return true
	}
	request, err := s.beginRequirementContentJob(action)
	if err != nil {
		s.applyRequirementContentFailure(action, err)
		return true
	}
	backend, win, state := s.backend, s.win, s.requirementUnit.state.requirementContentJobs
	win.Invalidate()
	go func() {
		result, err := executeRequirementContentJob(backend, request)
		if !win.IsDestroyed() {
			win.Update(func() { s.finishRequirementContentJob(request, result, err) })
		}
		request.cancel()
		state.mu.Lock()
		delete(state.running, request)
		close(request.done)
		state.mu.Unlock()
	}()
	return true
}

func executeRequirementContentJob(backend application.Backend, request *requirementContentJob) (application.ActionResult, error) {
	if backend == nil || request == nil {
		return application.ActionResult{}, errors.New("需求读取缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	result, err := executeRequirementOperation(request.ctx, backend, request.action, nil)
	if request.ctx.Err() != nil {
		return application.ActionResult{}, errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) finishRequirementContentJob(request *requirementContentJob, result application.ActionResult, err error) {
	if !s.requirementContentJobRelevant(request) {
		return
	}
	delete(s.requirementUnit.state.requirementContentJobs.requests, request.key)
	if err != nil {
		s.applyRequirementContentFailure(request.action, err)
	} else {
		s.applyRequirementContentResult(request.action, result)
	}
}

func (s *requirementController) cancelRequirementContentJob(request *requirementContentJob) {
	if request == nil {
		return
	}
	request.cancel()
	if state := s.state.requirementContentJobs; state != nil && state.requests[request.key] == request {
		delete(state.requests, request.key)
	}
	if reader := s.requirementReaderForAction(request.action); reader != nil {
		delete(reader.requestIDs, requirementContentRequestKind(request.action))
		switch request.action.Kind {
		case "requirements.read":
			reader.bodyLoading = false
		case "requirements.comments":
			reader.commentsLoading = false
		case "requirements.attachment":
			if payloadString(request.action.Payload, "scope") == "comment" {
				reader.commentResourceLoading = ""
			} else {
				reader.resourceLoading = ""
			}
		}
	}
}

// Retire the token as well as the worker: comment refreshes after publishing
// also use reader tokens, but own their worker outside this request map.
func (s *app) retireRequirementReaderRequest(reader *requirementReaderState, kind string) {
	if kind == "requirements.comments" || kind == "requirements.attachment" {
		s.retireRequirementCommentImages(reader)
	}
	if state := s.requirementUnit.state.requirementContentJobs; state != nil {
		for _, request := range state.requests {
			if request.reader == reader && request.action.Kind == kind {
				s.requirementUnit.cancelRequirementContentJob(request)
			}
		}
	}
	delete(reader.requestIDs, kind)
	switch kind {
	case "requirements.read":
		reader.bodyLoading = false
	case "requirements.comments":
		reader.commentsLoading = false
	case "requirements.attachment":
		reader.resourceLoading = ""
	}
}

// Each frame records the actual visible reader. Leaving it cancels reads even
// when a backend ignores cancellation; removed requests cannot apply later.
func (s *app) observeRequirementContentJobs() {
	state := s.requirementUnit.state.requirementContentJobs
	if state == nil {
		return
	}
	if state.entered != nil && requirementReaderKey(state.entered.taskID, state.entered.url) != state.visible {
		state.entered = nil
	}
	for _, request := range state.requests {
		if requirementReaderKey(request.reader.taskID, request.reader.url) != state.visible || !s.requirementContentJobRelevant(request) {
			s.requirementUnit.cancelRequirementContentJob(request)
		}
	}
}

func (s *requirementController) cancelRequirementContentReads() {
	if state := s.state.requirementContentJobs; state != nil {
		for _, request := range state.requests {
			s.cancelRequirementContentJob(request)
		}
	}
}

func (s *requirementController) closeRequirementContentJobs(ctx context.Context) error {
	state := s.state.requirementContentJobs
	if state == nil {
		return nil
	}
	s.cancelRequirementContentReads()
	state.mu.Lock()
	state.closed = true
	running := make([]*requirementContentJob, 0, len(state.running))
	for request := range state.running {
		request.cancel()
		running = append(running, request)
	}
	state.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待需求读取清理：%w", ctx.Err())
		}
	}
	return nil
}
