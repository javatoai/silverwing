package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"silverwing-go/internal/application"
)

type taskFilesRequest struct {
	view   *taskFilesView
	epoch  uint64
	key    string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// current is UI-owned. running also includes replaced/cancelled requests and
// resource disposal, so shutdown waits for the real workers rather than slots.
type taskFilesJobs struct {
	current    map[string]*taskFilesRequest
	mu         sync.Mutex
	running    map[*taskFilesRequest]struct{}
	closed     bool
	mediaSlots chan struct{}
}

func (jobs *taskFilesJobs) cancel(key string) {
	if request := jobs.current[key]; request != nil {
		request.cancel()
		delete(jobs.current, key)
	}
}

func (jobs *taskFilesJobs) cancelView(view *taskFilesView) {
	for key, request := range jobs.current {
		if request.view == view {
			jobs.cancel(key)
		}
	}
}

func (jobs *taskFilesJobs) cancelPreview() {
	for key := range jobs.current {
		if strings.HasPrefix(key, "preview:") {
			jobs.cancel(key)
		}
	}
}

func (jobs *taskFilesJobs) acquireMedia(ctx context.Context) (func(), error) {
	jobs.mu.Lock()
	if jobs.mediaSlots == nil {
		jobs.mediaSlots = make(chan struct{}, 2)
	}
	slots := jobs.mediaSlots
	jobs.mu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (jobs *taskFilesJobs) add(request *taskFilesRequest, cleanup bool) bool {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.closed && !cleanup {
		return false
	}
	if jobs.running == nil {
		jobs.running = make(map[*taskFilesRequest]struct{})
	}
	jobs.running[request] = struct{}{}
	return true
}

func (jobs *taskFilesJobs) finish(request *taskFilesRequest) {
	jobs.mu.Lock()
	delete(jobs.running, request)
	close(request.done)
	jobs.mu.Unlock()
}

func (jobs *taskFilesJobs) dispose(run func()) {
	if run == nil {
		return
	}
	request := &taskFilesRequest{done: make(chan struct{})}
	jobs.add(request, true)
	go func() { defer jobs.finish(request); run() }()
}

func (jobs *taskFilesJobs) stop() {
	jobs.mu.Lock()
	jobs.closed = true
	for request := range jobs.running {
		if request.cancel != nil {
			request.cancel()
		}
	}
	jobs.mu.Unlock()
	clear(jobs.current)
}

func (jobs *taskFilesJobs) close(ctx context.Context) error {
	jobs.stop()
	for {
		jobs.mu.Lock()
		pending := make([]<-chan struct{}, 0, len(jobs.running))
		for request := range jobs.running {
			pending = append(pending, request.done)
		}
		jobs.mu.Unlock()
		if len(pending) == 0 {
			return nil
		}
		for _, done := range pending {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (jobs *taskFilesJobs) accepts(request *taskFilesRequest, view *taskFilesView) bool {
	return request != nil && view != nil && request.ctx.Err() == nil && view.active &&
		request.view == view && request.epoch == view.epoch && jobs.current[request.key] == request
}

// dispose is called exactly once for an unpublished result, including a queued
// UI callback cancelled during shutdown. A published result belongs to the view.
func (s *app) runTaskFilesJob(view *taskFilesView, key string,
	work func(context.Context) (any, error), publish func(any, error), dispose func(any)) {
	if !s.taskFilesCurrent(view) {
		return
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		publish(nil, errors.New("任务文件读取后端或窗口不可用，请重试"))
		return
	}
	jobs := &s.taskFilesUnit.jobs
	jobs.cancel(key)
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskFilesRequest{view: view, epoch: view.epoch, key: key, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if !jobs.add(request, false) {
		cancel()
		return
	}
	if jobs.current == nil {
		jobs.current = make(map[string]*taskFilesRequest)
	}
	jobs.current[key] = request
	win := s.win
	go func() {
		defer jobs.finish(request)
		defer cancel()
		value, err := work(ctx)
		var once sync.Once
		discard := func() {
			once.Do(func() {
				if dispose != nil {
					dispose(value)
				}
			})
		}
		if ctx.Err() != nil || win.IsDestroyed() {
			discard()
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			if !jobs.accepts(request, view) || !s.taskFilesCurrent(view) {
				discard()
				return
			}
			delete(jobs.current, key)
			once.Do(func() { publish(value, err) })
		})
		select {
		case <-updated:
		case <-ctx.Done():
			discard()
		}
	}()
}

func decodeTaskFilesResult[T any](result application.ActionResult) (T, error) {
	if value, ok := result.Data.(T); ok {
		return value, nil
	}
	var value T
	if result.Data == nil {
		return value, errors.New("任务文件返回为空，请重试")
	}
	data, err := json.Marshal(result.Data)
	if err == nil {
		err = json.Unmarshal(data, &value)
	}
	return value, err
}
