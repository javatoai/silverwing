package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

type tagJobManager struct {
	app          *App
	mu           sync.Mutex
	jobs         map[string]*tagJob // one current/last job per task, not per batch
	resources    map[string]bool    // shared across every task and later batch
	pending      []*tagJobWork
	closed       bool
	wg           sync.WaitGroup
	openTask     func(domain.AppConfig, string) (tagTaskRunner, error)
	probeFlights map[string]*tagProbeFlight
	probe        tagJobProbeFunc
}

type tagJob struct {
	state          TagJobSnapshot
	task           tagTaskRunner
	ctx            context.Context
	cancel         context.CancelFunc
	works          []*tagJobWork
	remaining      int
	ready, closing bool
	finish, done   chan struct{}
	watchers       map[chan TagJobSnapshot]struct{}
}

type tagJobWork struct {
	job     *tagJob
	planned delivery.TagTaskItem
	view    TagJobItem
	held    bool
}

func (a *App) StartTagJob(ctx context.Context, scope TagJobScope, action Action) (TagJobSnapshot, error) {
	config, err := a.validateTagJobScope(ctx, scope)
	if err != nil {
		return TagJobSnapshot{}, err
	}
	m := a.tagJobManager()
	task, err := m.openTask(config, scope.TaskID)
	if err != nil {
		return TagJobSnapshot{}, err
	}
	if configTagIdentity(task.Config()) != scope.ConfigIdentity || task.Manifest().CreatedAt != scope.TaskCreatedAt || !sameTagHistoryTaskPath(task.Directory(), scope.TaskPath) {
		return TagJobSnapshot{}, errors.New("Tag任务快照在启动前已变化")
	}
	batchID := delivery.NewTagJobID()
	works, err := m.prepare(ctx, scope, action, task, batchID)
	if err != nil {
		return TagJobSnapshot{}, err
	}
	if _, err := a.validateTagJobScope(ctx, scope); err != nil {
		return TagJobSnapshot{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return TagJobSnapshot{}, errors.New("应用正在关闭")
	}
	if old := m.jobs[tagJobScopeKey(scope)]; old != nil && old.state.Busy {
		if old.state.Scope != scope {
			m.mu.Unlock()
			return TagJobSnapshot{}, errors.New("已有其它身份的Tag作业尚未结束")
		}
		if old.state.Cancelling {
			m.mu.Unlock()
			return TagJobSnapshot{}, errors.New("Tag作业正在取消")
		}
		if old.closing {
			done := old.done
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return TagJobSnapshot{}, ctx.Err()
			case <-done:
			}
			return a.StartTagJob(ctx, scope, action)
		}
		m.admitLocked(old, works)
		snapshot := m.snapshotLocked(old)
		m.mu.Unlock()
		return snapshot, nil
	}
	defer m.mu.Unlock()
	jobCtx, cancel := context.WithCancel(context.Background())
	j := &tagJob{state: TagJobSnapshot{Scope: scope, ID: delivery.NewTagJobID(), BatchID: batchID, Busy: true},
		task: task, ctx: jobCtx, cancel: cancel, works: works, remaining: len(works), finish: make(chan struct{}), done: make(chan struct{}), watchers: map[chan TagJobSnapshot]struct{}{}}
	for _, w := range works {
		w.job = j
	}
	m.jobs[tagJobScopeKey(scope)] = j
	m.publishLocked(j)
	m.wg.Add(1)
	go m.runJob(j)
	return m.snapshotLocked(j), nil
}

func (a *App) AppendTagJob(ctx context.Context, scope TagJobScope, id string, action Action) (TagJobSnapshot, error) {
	if action.Kind != "tag.build" && action.Kind != "tag.retry" && action.Kind != "tag.workspace-check" {
		return TagJobSnapshot{}, errors.New("只能追加Tag构建、检查或重试")
	}
	if _, err := a.validateTagJobScope(ctx, scope); err != nil {
		return TagJobSnapshot{}, err
	}
	m := a.tagJobManager()
	m.mu.Lock()
	j, err := m.findLocked(scope, id)
	if err != nil {
		m.mu.Unlock()
		return TagJobSnapshot{}, err
	}
	if j.closing || !j.state.Busy {
		done := j.done
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return TagJobSnapshot{}, ctx.Err()
		case <-done:
		}
		return a.StartTagJob(ctx, scope, action)
	}
	if j.state.Cancelling {
		m.mu.Unlock()
		return TagJobSnapshot{}, errors.New("Tag作业正在取消")
	}
	m.mu.Unlock()
	works, err := m.prepare(ctx, scope, action, j.task, j.state.BatchID)
	if err != nil {
		return TagJobSnapshot{}, err
	}
	if _, err := a.validateTagJobScope(ctx, scope); err != nil {
		return TagJobSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || j.closing || j.ctx.Err() != nil {
		return TagJobSnapshot{}, errors.New("Tag作业已结束或正在关闭")
	}
	m.admitLocked(j, works)
	return m.snapshotLocked(j), nil
}

func tagWorkEquivalent(a, b *tagJobWork) bool {
	if a.view.Kind != b.view.Kind {
		return false
	}
	if a.view.Kind != "tag.build" {
		return a.view.OperationID == b.view.OperationID
	}
	left, right := a.planned.Operation, b.planned.Operation
	return a.planned.SelectionKey == b.planned.SelectionKey &&
		a.planned.WorkspacePath == b.planned.WorkspacePath && a.planned.TagMessagePrefix == b.planned.TagMessagePrefix && slices.Equal(a.planned.Resources, b.planned.Resources) &&
		left.RepositoryID == right.RepositoryID && left.SourceBranch == right.SourceBranch && left.Remote == right.Remote && left.TagMode == right.TagMode && optionalTagString(left.TargetBranch) == optionalTagString(right.TargetBranch)
}
func optionalTagString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func (m *tagJobManager) admitLocked(j *tagJob, works []*tagJobWork) {
	for _, w := range works {
		duplicate := false
		for _, existing := range j.works {
			if existing.view.Busy && tagWorkEquivalent(existing, w) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		w.job = j
		if w.view.Kind == "tag.build" {
			batch := j.state.BatchID
			w.planned.Operation.BatchID = &batch
			w.view.Record.BatchID = batch
		}
		j.works = append(j.works, w)
		j.remaining++
		if j.ready {
			m.pending = append(m.pending, w)
		}
	}
	m.publishLocked(j)
	m.scheduleLocked()
}

func (m *tagJobManager) runJob(j *tagJob) {
	defer m.wg.Done()
	err := j.task.WithLock(func() error {
		// Archive/delete share this lease. Revalidate after the potentially slow
		// Plan window, before any record initialization can recreate an old task.
		if _, err := m.app.validateTagJobScope(j.ctx, j.state.Scope); err != nil {
			return err
		}
		m.mu.Lock()
		j.ready = true
		m.pending = append(m.pending, j.works...)
		m.scheduleLocked()
		m.mu.Unlock()
		<-j.finish
		return nil
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		j.state.Error = err.Error()
		phase, failure := TagJobPhaseFailed, TagJobFailureOther
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			phase, failure = TagJobPhaseCanceled, TagJobFailureCanceled
		}
		for _, w := range j.works {
			w.view.Busy, w.view.Queued, w.view.Done = false, false, true
			w.view.Phase, w.view.FailureKind, w.view.Error = phase, failure, err.Error()
		}
	}
	j.state.Busy = false
	j.cancel()
	m.publishLocked(j)
	close(j.done) // task lease has already been released
}

// A single aggregator owns all queue state, counters, snapshots and resource
// reservations. Independent lanes start immediately; no global concurrency cap.
func (m *tagJobManager) scheduleLocked() {
	kept := m.pending[:0]
	for _, w := range m.pending {
		if !w.job.ready {
			kept = append(kept, w)
			continue
		}
		free := true
		for _, key := range w.planned.Resources {
			if m.resources[key] {
				free = false
				break
			}
		}
		if !free && w.job.ctx.Err() == nil {
			kept = append(kept, w)
			continue
		}
		if w.job.ctx.Err() == nil {
			for _, key := range w.planned.Resources {
				m.resources[key] = true
			}
			w.held = true
		}
		w.view.Queued, w.view.Phase, w.view.Step = false, TagJobPhaseChecking, "检查工作区与远端"
		m.publishLocked(w.job)
		go m.runWork(w)
	}
	m.pending = kept
}

func (m *tagJobManager) runWork(w *tagJobWork) {
	j := w.job
	var operation domain.TagOperation
	var check *delivery.TagWorkspaceCheckReport
	err := j.ctx.Err()
	if err != nil {
		operation = w.planned.Operation
		if w.view.Kind == "tag.build" {
			var saveErr error
			operation, saveErr = j.task.CancelPending(w.planned)
			err = errors.Join(err, saveErr)
		}
	} else {
		if w.view.Kind == "tag.build" {
			err = j.task.Initialize(j.ctx, w.planned)
		}
		if err == nil {
			operation, check, err = j.task.Run(j.ctx, w.view.Kind, w.planned, func(event delivery.TagProgress) {
				m.mu.Lock()
				defer m.mu.Unlock()
				if event.Phase != "" {
					w.view.Phase = TagJobPhase(event.Phase)
				}
				if event.Step != "" {
					w.view.Step = event.Step
				}
				if event.FailureKind != "" {
					w.view.FailureKind = event.FailureKind
				}
				if event.Operation != nil {
					m.setRecordLocked(w, *event.Operation)
				}
				m.publishLocked(j)
			})
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if operation.OperationID != "" {
		m.setRecordLocked(w, operation)
	}
	if check != nil {
		w.view.Check = &TagJobCheckResult{Clean: check.Clean, Changes: check.Changes}
	}
	switch {
	case err != nil:
		w.view.Phase, w.view.FailureKind, w.view.Error = TagJobPhaseFailed, TagJobFailureOther, err.Error()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			w.view.Phase, w.view.FailureKind = TagJobPhaseCanceled, TagJobFailureCanceled
		}
	case w.view.Kind == "tag.workspace-check":
		w.view.Phase = TagJobPhaseCompleted
	case w.view.FailureKind == TagJobFailureCanceled && operation.State != domain.TagOperationSuccess:
		w.view.Phase = TagJobPhaseCanceled
	case operation.State == domain.TagOperationConflict:
		w.view.Phase, w.view.FailureKind = TagJobPhaseConflict, TagJobFailureConflict
	case operation.State == domain.TagOperationFailed || operation.State == domain.TagOperationPartial:
		w.view.Phase = TagJobPhaseFailed
		if w.view.Record != nil {
			w.view.Error = w.view.Record.Detail
		}
		if w.view.FailureKind == "" {
			w.view.FailureKind = TagJobFailureOther
		}
	default:
		w.view.Phase = TagJobPhaseCompleted
	}
	w.view.Busy, w.view.Queued, w.view.Done = false, false, true
	if w.held {
		for _, key := range w.planned.Resources {
			delete(m.resources, key)
		}
	}
	j.remaining--
	m.publishLocked(j)
	if j.remaining == 0 {
		j.closing = true
		close(j.finish)
	}
	m.scheduleLocked()
}

func (m *tagJobManager) setRecordLocked(w *tagJobWork, operation domain.TagOperation) {
	if current := w.view.Record; current != nil && current.ID == operation.OperationID && current.Tag != "" && current.Tag == value(operation.Tag) && current.GenbuStatus.CheckedAt != nil &&
		!tagStatusNewer(operation.GenbuStatus, current.GenbuStatus) {
		operation.GenbuStatus = current.GenbuStatus
	}
	record := tagRecord(w.job.state.Scope.TaskID, delivery.DescribeTagOperation(operation, w.job.state.Scope.TaskID))
	w.view.Record, w.view.OperationID = &record, record.ID
}

func tagStatusNewer(incoming, current domain.GenbuTagProbeStatus) bool {
	if incoming.CheckedAt == nil {
		return false
	}
	if current.CheckedAt == nil {
		return true
	}
	incomingTime, incomingErr := domain.ParseSilverWingTime(*incoming.CheckedAt)
	currentTime, currentErr := domain.ParseSilverWingTime(*current.CheckedAt)
	return incomingErr == nil && (currentErr != nil || incomingTime.After(currentTime))
}

func (m *tagJobManager) findLocked(scope TagJobScope, id string) (*tagJob, error) {
	j := m.jobs[tagJobScopeKey(scope)]
	if j == nil || (id != "" && j.state.ID != id) || j.state.Scope != scope {
		return nil, errors.New("Tag作业身份已变化")
	}
	return j, nil
}

func (m *tagJobManager) snapshotLocked(j *tagJob) TagJobSnapshot {
	snapshot := j.state
	snapshot.Items = make([]TagJobItem, len(j.works))
	for i, w := range j.works {
		snapshot.Items[i] = w.view
	}
	data, _ := json.Marshal(snapshot)
	var copy TagJobSnapshot
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (m *tagJobManager) publishLocked(j *tagJob) {
	j.state.Sequence++
	for watcher := range j.watchers {
		select {
		case <-watcher:
		default:
		}
		watcher <- m.snapshotLocked(j)
	}
}

func (a *App) TagJobSnapshot(ctx context.Context, scope TagJobScope) (TagJobSnapshot, error) {
	if _, err := a.validateTagJobScope(ctx, scope); err != nil {
		return TagJobSnapshot{}, err
	}
	m := a.tagJobManager()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[tagJobScopeKey(scope)] == nil {
		return TagJobSnapshot{Scope: scope}, nil
	}
	j, err := m.findLocked(scope, "")
	if err != nil {
		return TagJobSnapshot{}, err
	}
	return m.snapshotLocked(j), nil
}

func (a *App) WatchTagJob(ctx context.Context, scope TagJobScope, id string, after uint64, onSnapshot func(TagJobSnapshot)) error {
	if onSnapshot == nil {
		return errors.New("缺少Tag进度回调")
	}
	m := a.tagJobManager()
	m.mu.Lock()
	j, err := m.findLocked(scope, id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	updates := make(chan TagJobSnapshot, 1)
	j.watchers[updates] = struct{}{}
	initial := m.snapshotLocked(j)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(j.watchers, updates); m.mu.Unlock() }()
	if initial.Sequence > after {
		onSnapshot(initial)
		after = initial.Sequence
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snapshot := <-updates:
			if snapshot.Sequence > after {
				onSnapshot(snapshot)
				after = snapshot.Sequence
			}
		case <-j.done:
			m.mu.Lock()
			final := m.snapshotLocked(j)
			m.mu.Unlock()
			if final.Sequence > after {
				onSnapshot(final)
			}
			return nil
		}
	}
}

func (a *App) CancelTagJob(ctx context.Context, scope TagJobScope, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m := a.tagJobManager()
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.findLocked(scope, id)
	if err != nil {
		return err
	}
	if j.state.Busy {
		j.state.Cancelling = true
		j.cancel()
		m.publishLocked(j)
		m.scheduleLocked()
	}
	return nil
}

func (a *App) CloseTagJobs(ctx context.Context) error {
	m := a.tagJobManager()
	m.mu.Lock()
	m.closed = true
	for _, j := range m.jobs {
		if j.state.Busy {
			j.state.Cancelling = true
			j.cancel()
			m.publishLocked(j)
		}
	}
	for _, probe := range m.probeFlights {
		probe.cancel()
	}
	m.scheduleLocked()
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ TagJobBackend = (*App)(nil)
var _ TagJobCloser = (*App)(nil)
