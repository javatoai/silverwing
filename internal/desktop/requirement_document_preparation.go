package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

type requirementDocumentPreparationWork struct {
	item             application.Requirement
	force            bool
	expectedIdentity string
}

type requirementDocumentPreparationStart struct {
	selectedKey      string
	expectedIdentity string
}

type requirementDocumentPreparationRun struct {
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	pending          []requirementDocumentPreparationWork
	failed           []requirementDocumentPreparationWork
	total, completed int
	active           bool
}

// The queue owns preparation only. Body/image storage and account validation
// remain in the existing application/integration routes.
type requirementDocumentPreparationState struct {
	mu       sync.Mutex
	current  *requirementDocumentPreparationRun
	running  map[*requirementDocumentPreparationRun]struct{}
	priority string
	identity requirementListIdentity
	closed   bool
	notify   func()
}

func (state *requirementDocumentPreparationState) start(backend application.Backend, items []application.Requirement, force bool, options ...requirementDocumentPreparationStart) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.cancelLocked()
	if state.closed || backend == nil {
		return
	}
	start := requirementDocumentPreparationStart{}
	if len(options) > 0 {
		start = options[0]
	}
	// Install the restored selection before either worker can take its first item.
	state.priority = start.selectedKey
	positions := make(map[string]int, len(items))
	work := make([]requirementDocumentPreparationWork, 0, len(items))
	for _, item := range items {
		key := requirementKey(item)
		if index, found := positions[key]; found {
			// Match the returned list's latest item without changing queue order.
			work[index].item = item
			continue
		}
		positions[key] = len(work)
		work = append(work, requirementDocumentPreparationWork{item: item, force: force, expectedIdentity: start.expectedIdentity})
	}
	state.launchLocked(backend, work, len(work), 0)
}

func (state *requirementDocumentPreparationState) launchLocked(backend application.Backend, work []requirementDocumentPreparationWork, total, completed int) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &requirementDocumentPreparationRun{ctx: ctx, cancel: cancel, done: make(chan struct{}), pending: work, total: total, completed: completed, active: len(work) > 0}
	state.current = run
	if len(work) == 0 {
		close(run.done)
		cancel()
		return
	}
	if state.running == nil {
		state.running = make(map[*requirementDocumentPreparationRun]struct{})
	}
	state.running[run] = struct{}{}
	go state.execute(backend, run)
}

func (state *requirementDocumentPreparationState) execute(backend application.Backend, run *requirementDocumentPreparationRun) {
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				state.mu.Lock()
				if state.current != run || run.ctx.Err() != nil || len(run.pending) == 0 {
					state.mu.Unlock()
					return
				}
				index := 0
				for i, work := range run.pending {
					if requirementKey(work.item) == state.priority {
						index = i
						break
					}
				}
				work := run.pending[index]
				run.pending = append(run.pending[:index], run.pending[index+1:]...)
				state.mu.Unlock()
				retryBody, err := prepareRequirementDocument(run.ctx, backend, work)
				state.mu.Lock()
				if state.current == run && run.ctx.Err() == nil {
					if err != nil {
						work.force = retryBody
						run.failed = append(run.failed, work)
					} else {
						run.completed++
					}
				}
				state.mu.Unlock()
				state.invalidate()
			}
		}()
	}
	workers.Wait()
	run.cancel()
	state.mu.Lock()
	run.active = false
	delete(state.running, run)
	close(run.done)
	state.mu.Unlock()
	state.invalidate()
}

func (state *requirementDocumentPreparationState) invalidate() {
	if state.notify != nil {
		state.notify()
	}
}

func (state *requirementDocumentPreparationState) cancelLocked() {
	if state.current != nil {
		state.current.cancel()
	}
	state.current, state.priority = nil, ""
}

func (state *requirementDocumentPreparationState) cancel() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.cancelLocked()
}

func (state *requirementDocumentPreparationState) retry(backend application.Backend) {
	state.mu.Lock()
	defer state.mu.Unlock()
	previous := state.current
	if state.closed || backend == nil || previous == nil || previous.active || len(previous.failed) == 0 {
		return
	}
	state.launchLocked(backend, append([]requirementDocumentPreparationWork(nil), previous.failed...), previous.total, previous.completed)
}

func (state *requirementDocumentPreparationState) snapshot() (total, completed, failures int, active bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if run := state.current; run != nil {
		return run.total, run.completed, len(run.failed), run.active
	}
	return
}

func (state *requirementDocumentPreparationState) close(ctx context.Context) error {
	state.mu.Lock()
	state.closed = true
	state.cancelLocked()
	running := make([]*requirementDocumentPreparationRun, 0, len(state.running))
	for run := range state.running {
		run.cancel()
		running = append(running, run)
	}
	state.mu.Unlock()
	for _, run := range running {
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func requirementPreparationPayload(item application.Requirement, force bool) map[string]any {
	return map[string]any{"projectKey": firstNonEmpty(item.WorkItem.ProjectKey, item.ProjectKey), "projectName": firstNonEmpty(item.WorkItem.ProjectName, item.Project), "type": firstNonEmpty(item.WorkItem.Type, item.Type), "id": firstNonEmpty(item.WorkItem.ID, item.ID), "title": item.Title, "url": item.URL, "force": force}
}

func requirementPreparationContent(data any) (application.RequirementContent, error) {
	if content, ok := data.(application.RequirementContent); ok {
		return content, nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return application.RequirementContent{}, err
	}
	var content application.RequirementContent
	err = json.Unmarshal(encoded, &content)
	return content, err
}

// A stale/read/cache failure retries the body. An image-only failure keeps the
// cached body and retries missing/failed images, as the Source coordinator does.
func prepareRequirementDocument(ctx context.Context, backend application.Backend, work requirementDocumentPreparationWork) (bool, error) {
	if backend == nil {
		return true, errors.New("需求正文后端不可用")
	}
	payload := requirementPreparationPayload(work.item, work.force)
	expected, err := requirementPreparationIdentity(ctx, backend, work.expectedIdentity)
	if err != nil {
		return true, err
	}
	if expected != "" {
		payload["expectedIdentity"] = expected
	}
	read := func(force bool) (application.RequirementContent, error) {
		if _, err := requirementPreparationIdentity(ctx, backend, expected); err != nil {
			return application.RequirementContent{}, err
		}
		payload["force"] = force
		result, err := backend.Dispatch(ctx, application.Action{Kind: "requirements.read", Path: work.item.URL, Payload: clonePreparationPayload(payload)})
		if err != nil {
			return application.RequirementContent{}, err
		}
		return requirementPreparationContent(result.Data)
	}
	body, err := read(work.force)
	if err != nil {
		return true, err
	}
	if body.CacheError != "" || body.RefreshError != "" || body.Stale || body.Generation == "" || body.MarkdownPath == "" {
		return true, errors.New(firstNonEmpty(body.CacheError, body.RefreshError, "正文尚未完整缓存"))
	}
	failures := make([]error, len(body.Resources))
	var images sync.WaitGroup
	for index, image := range body.Resources {
		if image.Path != "" && image.Error == "" && !work.force {
			continue
		}
		images.Add(1)
		go func() {
			defer images.Done()
			if _, err := requirementPreparationIdentity(ctx, backend, expected); err != nil {
				failures[index] = err
				return
			}
			imagePayload := clonePreparationPayload(payload)
			imagePayload["fileUrl"], imagePayload["generation"], imagePayload["retry"] = image.URL, body.Generation, work.force || image.Error != ""
			result, err := backend.Dispatch(ctx, application.Action{Kind: "requirements.attachment", Payload: imagePayload})
			if err == nil {
				encoded, marshalErr := json.Marshal(result.Data)
				var resource application.RequirementResource
				if marshalErr != nil {
					err = marshalErr
				} else {
					err = json.Unmarshal(encoded, &resource)
				}
				if err == nil && (resource.Path == "" || resource.Error != "") {
					err = errors.New(firstNonEmpty(resource.Error, "图片尚未缓存"))
				}
			}
			if err != nil {
				failures[index] = err
			}
		}()
	}
	images.Wait()
	if err := errors.Join(failures...); err != nil {
		return false, err
	}
	updated, err := read(false)
	if err != nil {
		return true, err
	}
	if updated.Generation != body.Generation || updated.Stale || updated.RefreshError != "" || updated.CacheError != "" {
		return true, errors.New("正文已变化或缓存失败，请重试")
	}
	if !updated.Complete {
		return false, errors.New("部分图片尚未完整缓存")
	}
	return false, nil
}

func requirementPreparationIdentity(ctx context.Context, backend application.Backend, expected string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	provider, ok := backend.(application.RequirementIdentityBackend)
	if !ok {
		return expected, nil
	}
	account, err := provider.RequirementIdentity(ctx)
	if err != nil {
		return "", err
	}
	if account.State != application.RequirementIdentityAuthenticated || account.Identity == "" {
		return "", errors.New("无法确认当前 Meegle 登录身份，请登录后继续全文缓存")
	}
	if expected != "" && account.Identity != expected {
		return "", &application.RequirementIdentityChangedError{Snapshot: account}
	}
	return account.Identity, nil
}

func clonePreparationPayload(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func (s *app) startRequirementDocumentPreparation(items []application.Requirement, force bool, selectedKey string) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return
	}
	if s.requirementDocumentPreparation == nil {
		state := &requirementDocumentPreparationState{}
		window := s.win
		state.notify = func() {
			if !window.IsDestroyed() {
				window.Update(func() {
					if s.requirementDocumentPreparation == state {
						window.Invalidate()
					}
				})
			}
		}
		s.requirementDocumentPreparation = state
	}
	state := s.requirementDocumentPreparation
	state.identity = s.currentRequirementListIdentity()
	expected := ""
	if identity := s.requirementIdentityJobs; identity != nil && identity.confirmed {
		expected = identity.identity
	}
	state.start(s.backend, items, force, requirementDocumentPreparationStart{selectedKey: selectedKey, expectedIdentity: expected})
}

func (s *app) prioritizeRequirementDocumentPreparation(item application.Requirement) {
	if state := s.requirementDocumentPreparation; state != nil {
		state.mu.Lock()
		state.priority = requirementKey(item)
		state.mu.Unlock()
	}
}

func (s *app) cancelRequirementDocumentPreparation() {
	if state := s.requirementDocumentPreparation; state != nil {
		state.cancel()
	}
}

func (s *app) observeRequirementDocumentPreparation() {
	if state := s.requirementDocumentPreparation; state != nil && state.identity != s.currentRequirementListIdentity() {
		state.cancel()
	}
}

func (s *app) closeRequirementDocumentPreparation(ctx context.Context) error {
	var err error
	if state := s.requirementDocumentPreparation; state != nil {
		err = state.close(ctx)
	}
	if backend, ok := s.backend.(interface{ CloseRequirementDocuments(context.Context) error }); ok {
		err = errors.Join(err, backend.CloseRequirementDocuments(ctx))
	}
	return err
}

func (s *app) renderRequirementDocumentPreparation(c *ui.Context, p palette) {
	state := s.requirementDocumentPreparation
	if state == nil {
		return
	}
	total, completed, failures, active := state.snapshot()
	if total == 0 {
		return
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, fmt.Sprintf("全文缓存 %d/%d", completed, total), 11, p.muted)
		if active {
			label(c, "正在缓存正文与图片…", 11, p.muted)
		}
		if failures > 0 {
			label(c, fmt.Sprintf("%d 项失败", failures), 11, p.amber)
			if toolButtonWithEnabled(c, "requirement-document-preparation-retry", "重试失败项", p, false, !active) {
				if state.identity == s.currentRequirementListIdentity() {
					state.retry(s.backend)
				} else {
					state.cancel()
				}
			}
		}
	})
}
