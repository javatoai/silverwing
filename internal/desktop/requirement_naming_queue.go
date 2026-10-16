package desktop

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type requirementNamingFailure struct{ Key, Title, Message string }

type requirementNamingPreparationSnapshot struct {
	Total, Completed int
	Running, Paused  bool
	Failures         []requirementNamingFailure
	Retrying         map[string]bool
}

type requirementNamingPreparationWorker struct {
	epoch  uint64
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	read   func(context.Context, application.Requirement) error
}

// The queue is serial; the backend reserves another naming slot for the form.
// Mutable preparation state is independent of both global busy and readers.
type requirementNamingPreparationQueue struct {
	mu          sync.Mutex
	epoch       uint64
	total       int
	paused      bool
	closed      bool
	priority    string
	active      string
	pending     map[string]application.Requirement
	order       []string
	failed      map[string]requirementNamingFailure
	failedItems map[string]application.Requirement
	failedOrder []string
	completed   map[string]bool
	retrying    map[string]bool
	current     *requirementNamingPreparationWorker
	running     map[*requirementNamingPreparationWorker]struct{}
	read        func(context.Context, application.Requirement) error
	changed     func()
}

func newRequirementNamingPreparationQueue(changed func()) *requirementNamingPreparationQueue {
	return &requirementNamingPreparationQueue{running: make(map[*requirementNamingPreparationWorker]struct{}), changed: changed}
}

func (q *requirementNamingPreparationQueue) resetLocked() {
	q.epoch++
	if q.current != nil {
		q.current.cancel()
		q.current = nil
	}
	q.total, q.active = 0, ""
	q.pending, q.failed = make(map[string]application.Requirement), make(map[string]requirementNamingFailure)
	q.failedItems = make(map[string]application.Requirement)
	q.completed, q.retrying = make(map[string]bool), make(map[string]bool)
	q.order, q.failedOrder = nil, nil
}

func (q *requirementNamingPreparationQueue) start(items []application.Requirement, enabled bool,
	read func(context.Context, application.Requirement) error) {
	q.mu.Lock()
	paused := enabled && q.paused
	q.resetLocked()
	q.paused, q.read = paused, read
	if enabled && !q.closed {
		for _, item := range items {
			key := requirementKey(item)
			if domain.ParseFeishuWorkItemLink(item.URL) == nil {
				continue
			}
			if _, exists := q.pending[key]; !exists {
				q.pending[key] = item
				q.order = append(q.order, key)
			}
		}
	}
	q.total = len(q.pending)
	q.promoteLocked(q.priority)
	q.startWorkerLocked()
	q.mu.Unlock()
	q.notify()
}

func (q *requirementNamingPreparationQueue) startWorkerLocked() {
	if q.closed || q.paused || q.current != nil || len(q.pending) == 0 || q.read == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &requirementNamingPreparationWorker{epoch: q.epoch, ctx: ctx, cancel: cancel, done: make(chan struct{}), read: q.read}
	q.current = worker
	q.running[worker] = struct{}{}
	go q.run(worker)
}

func (q *requirementNamingPreparationQueue) run(worker *requirementNamingPreparationWorker) {
	defer func() {
		worker.cancel()
		q.mu.Lock()
		delete(q.running, worker)
		if q.current == worker {
			q.current, q.active = nil, ""
			q.startWorkerLocked()
		}
		close(worker.done)
		q.mu.Unlock()
		q.notify()
	}()
	for {
		q.mu.Lock()
		if worker.epoch != q.epoch || q.closed || q.paused || worker.ctx.Err() != nil || len(q.order) == 0 {
			q.mu.Unlock()
			return
		}
		key := q.order[0]
		q.order = q.order[1:]
		item := q.pending[key]
		delete(q.pending, key)
		q.active = key
		q.mu.Unlock()
		q.notify()
		err := worker.read(worker.ctx, item)
		q.mu.Lock()
		if worker.epoch == q.epoch && worker.ctx.Err() == nil && !q.closed {
			if err == nil {
				q.completed[key] = true
				delete(q.failed, key)
				delete(q.failedItems, key)
			} else {
				if _, exists := q.failed[key]; !exists {
					q.failedOrder = append(q.failedOrder, key)
				}
				q.failed[key] = requirementNamingFailure{Key: key, Title: item.Title, Message: requirementNamingFailureMessage(err)}
				q.failedItems[key] = item
				message := strings.ToLower(err.Error())
				for _, systemic := range []string{"login", "auth", "登录", "认证", "找不到 codex"} {
					q.paused = q.paused || strings.Contains(message, systemic)
				}
			}
			q.active = ""
			delete(q.retrying, key)
		}
		q.mu.Unlock()
		q.notify()
	}
}

func (q *requirementNamingPreparationQueue) snapshot() requirementNamingPreparationSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := requirementNamingPreparationSnapshot{Total: q.total, Completed: len(q.completed),
		Running: q.current != nil, Paused: q.paused, Retrying: make(map[string]bool), Failures: []requirementNamingFailure{}}
	for _, key := range q.failedOrder {
		if failure, ok := q.failed[key]; ok {
			result.Failures = append(result.Failures, failure)
		}
	}
	for key, retrying := range q.retrying {
		result.Retrying[key] = retrying
	}
	return result
}

func (q *requirementNamingPreparationQueue) promoteLocked(key string) {
	if _, exists := q.pending[key]; !exists {
		return
	}
	for index, queued := range q.order {
		if queued == key {
			copy(q.order[1:index+1], q.order[:index])
			q.order[0] = key
			return
		}
	}
}

func (q *requirementNamingPreparationQueue) prioritize(item application.Requirement) {
	q.mu.Lock()
	q.priority = requirementKey(item)
	q.promoteLocked(q.priority)
	q.mu.Unlock()
}

func (q *requirementNamingPreparationQueue) setPaused(paused bool) {
	q.mu.Lock()
	q.paused = paused
	q.startWorkerLocked()
	q.mu.Unlock()
	q.notify()
}

func (q *requirementNamingPreparationQueue) retry(key string, all bool) {
	q.mu.Lock()
	keys := []string{key}
	if all {
		keys = q.failedOrder
	}
	for _, candidate := range keys {
		item, exists := q.failedItems[candidate]
		_, pending := q.pending[candidate]
		if !exists || candidate == q.active || pending || q.retrying[candidate] {
			continue
		}
		q.pending[candidate], q.retrying[candidate] = item, true
		q.order = append(q.order, candidate)
	}
	if all {
		q.promoteLocked(q.priority)
	} else {
		q.promoteLocked(key)
	}
	q.paused = false
	q.startWorkerLocked()
	q.mu.Unlock()
	q.notify()
}

func (q *requirementNamingPreparationQueue) cancel() {
	q.mu.Lock()
	q.resetLocked()
	q.paused = false
	q.mu.Unlock()
	q.notify()
}

func (q *requirementNamingPreparationQueue) close(ctx context.Context) error {
	q.mu.Lock()
	q.closed = true
	q.resetLocked()
	workers := make([]*requirementNamingPreparationWorker, 0, len(q.running))
	for worker := range q.running {
		worker.cancel()
		workers = append(workers, worker)
	}
	q.mu.Unlock()
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return fmt.Errorf("等待需求命名预热清理：%w", ctx.Err())
		}
	}
	return nil
}

func (q *requirementNamingPreparationQueue) notify() {
	if q.changed != nil {
		q.changed()
	}
}

var requirementNamingCredentials = regexp.MustCompile(`(https?://)[^/\s]*@`)

func requirementNamingFailureMessage(err error) string {
	message := strings.TrimSpace(strings.SplitN(err.Error(), "\n", 2)[0])
	message = requirementNamingCredentials.ReplaceAllString(message, "$1[已隐藏]@")
	if message == "" {
		return "命名生成失败"
	}
	return truncateDisplayRunes(message, 200)
}
