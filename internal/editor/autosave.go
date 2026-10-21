package editor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SaveFunc func(context.Context, string, SaveSnapshot) error

type ConflictSaveError struct{ Err error }

func (e ConflictSaveError) Error() string {
	if e.Err == nil {
		return "文件已被外部修改"
	}
	return e.Err.Error()
}
func (e ConflictSaveError) Unwrap() error { return e.Err }

type RetryWhenIdleError struct{ Err error }

func (e RetryWhenIdleError) Error() string {
	if e.Err == nil {
		return "操作仍在进行，Markdown 保存将在空闲后重试"
	}
	return e.Err.Error()
}
func (e RetryWhenIdleError) Unwrap() error { return e.Err }

type saveEntry struct {
	content          string
	revision         uint64
	savedRevision    uint64
	saving           bool
	quiesceCount     int
	failClosed       bool
	error            string
	conflict         bool
	composing        bool
	timer            *time.Timer
	draftTimer       *time.Timer
	draftPending     bool
	draftSaving      bool
	draftRevision    uint64
	draftUnsupported bool
	cacheError       string
}

// Autosave serializes document writes, waits until the shared operation gate
// is idle, and always captures the newest revision after the 650ms debounce.
type Autosave struct {
	mu           sync.Mutex
	entries      map[string]*saveEntry
	pending      []string
	pendingSet   map[string]bool
	isBusy       func() bool
	save         SaveFunc
	onSaved      func(string, SaveSnapshot)
	debounce     time.Duration
	context      context.Context
	cancel       context.CancelFunc
	worker       bool
	workerDone   chan struct{}
	draftWriter  func(context.Context, string, string) error
	draftRemover func(context.Context, string) error
	draftJournal *draftJournal
}

func NewAutosave(save SaveFunc, isBusy func() bool) *Autosave {
	ctx, cancel := context.WithCancel(context.Background())
	return &Autosave{entries: make(map[string]*saveEntry), pendingSet: make(map[string]bool),
		isBusy: isBusy, save: save, debounce: 650 * time.Millisecond, context: ctx, cancel: cancel}
}

func (a *Autosave) SetDebounce(delay time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if delay >= 0 {
		a.debounce = delay
	}
}

func (a *Autosave) SetOnSaved(onSaved func(string, SaveSnapshot)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onSaved = onSaved
}

func (a *Autosave) Seed(path, content string) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil {
		a.entries[key] = &saveEntry{content: content}
		return
	}
	if entry.revision == entry.savedRevision && !entry.saving && entry.content != content {
		a.stopTimerLocked(entry)
		entry.content = content
		entry.savedRevision = entry.revision
		entry.error, entry.conflict, entry.failClosed = "", false, false
	}
}

func (a *Autosave) Edit(path, content string) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entryLocked(key)
	if entry.content == content {
		return
	}
	entry.content = content
	entry.revision++
	a.queueDraftLocked(key)
	if !entry.composing && entry.error == "" && !entry.conflict && !entry.failClosed && entry.quiesceCount == 0 {
		a.scheduleLocked(key, entry)
	}
}

func (a *Autosave) SetComposing(path string, composing bool) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil || entry.composing == composing {
		return
	}
	entry.composing = composing
	if composing {
		a.stopTimerLocked(entry)
	} else if entry.revision > entry.savedRevision && entry.error == "" && !entry.conflict && !entry.failClosed && entry.quiesceCount == 0 {
		a.scheduleLocked(key, entry)
	}
}

// AutosaveLease pauses only the selected identities while a filesystem rename
// is in flight. Call Release on both success and failure paths.
type AutosaveLease struct {
	a      *Autosave
	mu     sync.Mutex
	keys   []string
	closed bool
}

// Quiesce pauses the selected keys and waits for saves that already started.
// It does not flush unrelated entries or clear their saved error/conflict state.
func (a *Autosave) Quiesce(ctx context.Context, paths []string) (*AutosaveLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	keys := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		key := normalizePath(path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	lease := &AutosaveLease{a: a, keys: keys}
	a.mu.Lock()
	for _, key := range keys {
		entry := a.entries[key]
		if entry == nil {
			continue
		}
		entry.quiesceCount++
		a.stopTimerLocked(entry)
		a.removePendingLocked(key)
	}
	a.mu.Unlock()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := a.FlushDrafts(ctx); err != nil {
			lease.Release()
			return nil, err
		}
		if !a.anySaving(keys) {
			return lease, nil
		}
		select {
		case <-ctx.Done():
			lease.Release()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *Autosave) anySaving(keys []string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, key := range keys {
		if entry := a.entries[key]; entry != nil && (entry.saving || entry.draftSaving) {
			return true
		}
	}
	return false
}

// Move atomically transfers selected, quiesced entries to their new keys.
// Existing destination entries are replaced, matching the desktop draft-store
// rename behavior. Both source and destination keys must be held by this lease
// so an in-flight save cannot write to either path during the move.
func (l *AutosaveLease) Move(paths map[string]string) error {
	if l == nil || l.a == nil {
		return errors.New("Markdown autosave lease is unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("Markdown autosave lease is already released")
	}
	a := l.a
	owned := make(map[string]bool, len(l.keys))
	for _, key := range l.keys {
		owned[key] = true
	}
	normalized := make(map[string]string, len(paths))
	destinations := make(map[string]bool, len(paths))
	for oldPath, newPath := range paths {
		oldKey, newKey := normalizePath(oldPath), normalizePath(newPath)
		if oldKey == "" || newKey == "" {
			return errors.New("Markdown 草稿迁移路径不能为空")
		}
		if !owned[oldKey] {
			return errors.New("Markdown 草稿不在当前重命名锁定范围内")
		}
		if oldKey == newKey {
			continue
		}
		if destinations[newKey] {
			return errors.New("多个 Markdown 草稿映射到了同一目标路径")
		}
		destinations[newKey] = true
		normalized[oldKey] = newKey
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for oldKey, newKey := range normalized {
		entry := a.entries[oldKey]
		if entry == nil {
			continue
		}
		if entry.quiesceCount == 0 || entry.saving {
			return errors.New("Markdown 自动保存尚未暂停")
		}
		if destination := a.entries[newKey]; destination != nil && destination != entry {
			if destination.saving || destination.quiesceCount == 0 {
				return errors.New("目标路径的 Markdown 草稿未被当前操作暂停")
			}
		}
	}
	for oldKey, newKey := range normalized {
		entry := a.entries[oldKey]
		if entry == nil {
			continue
		}
		a.stopTimerLocked(entry)
		a.stopDraftTimerLocked(entry)
		a.removePendingLocked(oldKey)
		if destination := a.entries[newKey]; destination != nil && destination != entry {
			a.stopTimerLocked(destination)
			a.stopDraftTimerLocked(destination)
			a.removePendingLocked(newKey)
			delete(a.entries, newKey)
		}
		delete(a.entries, oldKey)
		a.entries[newKey] = entry
	}
	targetKeys := make(map[string]bool, len(normalized))
	for _, newKey := range normalized {
		targetKeys[newKey] = true
	}
	updated := make([]string, 0, len(l.keys))
	seen := make(map[string]bool, len(l.keys))
	for _, key := range l.keys {
		if newKey, ok := normalized[key]; ok {
			key = newKey
		} else if targetKeys[key] {
			continue
		}
		if !seen[key] {
			seen[key] = true
			updated = append(updated, key)
		}
	}
	l.keys = updated
	return nil
}

// Release resumes the lease's current keys without changing conflict or error
// state. Dirty, healthy entries receive a fresh debounce interval.
func (l *AutosaveLease) Release() {
	l.release("")
}

// FailClosed releases a lease without re-arming autosave after a rename has
// already succeeded but its in-memory key migration could not be completed.
// Existing conflicts and errors remain authoritative; a new error is recorded
// only when the entry had no prior error.
func (l *AutosaveLease) FailClosed(message string) {
	l.release(message)
}

func (l *AutosaveLease) release(failClosedMessage string) {
	if l == nil || l.a == nil {
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	keys := append([]string(nil), l.keys...)
	l.mu.Unlock()

	a := l.a
	a.mu.Lock()
	for _, key := range keys {
		entry := a.entries[key]
		if entry == nil || entry.quiesceCount == 0 {
			continue
		}
		entry.quiesceCount--
		if entry.quiesceCount == 0 {
			a.queueDraftLocked(key)
		}
		if failClosedMessage != "" {
			entry.failClosed = true
			if entry.error == "" && !entry.conflict {
				entry.error = failClosedMessage
			}
		}
		if entry.quiesceCount == 0 && entry.revision > entry.savedRevision && entry.error == "" && !entry.conflict && !entry.failClosed && !entry.composing {
			a.scheduleLocked(key, entry)
		}
	}
	a.mu.Unlock()
}

func (a *Autosave) Status(path string) SaveState {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil {
		return SaveState{}
	}
	return a.stateOf(entry)
}

func (a *Autosave) Content(path string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[normalizePath(path)]
	if entry == nil {
		return "", false
	}
	return entry.content, true
}

func (a *Autosave) Pause(path, message string) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil {
		return
	}
	a.stopTimerLocked(entry)
	a.removePendingLocked(key)
	entry.error = message
}

func (a *Autosave) MarkConflict(path, localContent, message string) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entryLocked(key)
	if entry.content != localContent {
		entry.content = localContent
		entry.revision++
	}
	a.stopTimerLocked(entry)
	a.removePendingLocked(key)
	if message == "" {
		message = "文件已被外部修改"
	}
	entry.error, entry.conflict = message, true
}

func (a *Autosave) AcceptDisk(path, content string, force bool) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entryLocked(key)
	if !force && entry.revision > entry.savedRevision && entry.content != content {
		a.stopTimerLocked(entry)
		a.removePendingLocked(key)
		entry.error, entry.conflict = "文件已被外部修改", true
		return
	}
	a.stopTimerLocked(entry)
	a.removePendingLocked(key)
	entry.content = content
	entry.revision++
	entry.savedRevision = entry.revision
	entry.error, entry.conflict, entry.failClosed = "", false, false
	a.stopDraftTimerLocked(entry)
	entry.draftPending = false
}

// AcceptLocal marks the resolver's confirmed local bytes as the new disk state.
func (a *Autosave) AcceptLocal(path string) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil {
		return
	}
	a.stopTimerLocked(entry)
	a.removePendingLocked(key)
	entry.savedRevision = entry.revision
	entry.error, entry.conflict = "", false
}

func (a *Autosave) ResumeAfterConflict(path string) {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil {
		return
	}
	entry.error, entry.conflict = "", false
	if entry.revision > entry.savedRevision && !entry.composing && !entry.failClosed && entry.quiesceCount == 0 {
		a.scheduleLocked(key, entry)
	}
}

// Retry clears a paused error and queues exactly one current snapshot. Conflicts
// and uncommitted IME compositions must be resolved before retrying.
func (a *Autosave) Retry(path string) bool {
	key := normalizePath(path)
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entries[key]
	if entry == nil || entry.conflict || entry.composing || entry.failClosed || entry.quiesceCount > 0 || entry.revision <= entry.savedRevision {
		return false
	}
	entry.error = ""
	a.stopTimerLocked(entry)
	a.enqueueLocked(key)
	return true
}

func (a *Autosave) Flush(ctx context.Context) []FlushFailure {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = a.FlushDrafts(ctx)
	a.mu.Lock()
	for path, entry := range a.entries {
		a.stopTimerLocked(entry)
		if entry.revision > entry.savedRevision && entry.error == "" && !entry.conflict && !entry.failClosed && !entry.composing && entry.quiesceCount == 0 {
			a.enqueueLocked(path)
		}
	}
	a.ensureWorkerLocked()
	a.mu.Unlock()

	for {
		a.mu.Lock()
		done, running := a.workerDone, a.worker
		a.mu.Unlock()
		if !running {
			break
		}
		select {
		case <-done:
		case <-ctx.Done():
			return a.PendingFailures()
		}
	}
	return a.PendingFailures()
}

func (a *Autosave) Close(ctx context.Context) []FlushFailure {
	failures := a.PrepareClose(ctx)
	a.mu.Lock()
	for _, entry := range a.entries {
		a.stopTimerLocked(entry)
		a.stopDraftTimerLocked(entry)
	}
	a.cancel()
	a.mu.Unlock()
	return failures
}

// PendingFailures reports unresolved drafts without flushing or cancelling
// the worker. A window can recheck this after an asynchronous save completes.
func (a *Autosave) PendingFailures() []FlushFailure {
	a.mu.Lock()
	defer a.mu.Unlock()
	var failures []FlushFailure
	for path, entry := range a.entries {
		if entry.revision <= entry.savedRevision {
			continue
		}
		message := entry.error
		if message == "" {
			switch {
			case entry.conflict:
				message = "文件存在外部修改冲突"
			case entry.composing:
				message = "中文输入尚未结束，最新内容未提交"
			case entry.saving:
				message = "保存仍在进行"
			default:
				message = "文档仍未保存"
			}
		}
		failures = append(failures, FlushFailure{Path: path, Message: message})
	}
	return failures
}

func (a *Autosave) entryLocked(path string) *saveEntry {
	entry := a.entries[path]
	if entry == nil {
		entry = &saveEntry{}
		a.entries[path] = entry
	}
	return entry
}

func (a *Autosave) scheduleLocked(path string, entry *saveEntry) {
	a.stopTimerLocked(entry)
	delay := a.debounce
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if entry.timer != timer {
			return
		}
		entry.timer = nil
		if entry.revision > entry.savedRevision && !entry.composing && entry.error == "" && !entry.conflict && !entry.failClosed && entry.quiesceCount == 0 {
			a.enqueueLocked(path)
		}
	})
	entry.timer = timer
}

func (a *Autosave) stopTimerLocked(entry *saveEntry) {
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
}

func (a *Autosave) enqueueLocked(path string) {
	entry := a.entries[path]
	if entry == nil || entry.revision <= entry.savedRevision || entry.error != "" || entry.conflict || entry.failClosed || entry.composing || entry.quiesceCount > 0 {
		return
	}
	if !a.pendingSet[path] {
		a.pending = append(a.pending, path)
		a.pendingSet[path] = true
	}
	a.ensureWorkerLocked()
}

func (a *Autosave) removePendingLocked(path string) {
	if !a.pendingSet[path] {
		return
	}
	delete(a.pendingSet, path)
	for i := range a.pending {
		if a.pending[i] == path {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			break
		}
	}
}

func (a *Autosave) ensureWorkerLocked() {
	if a.worker || len(a.pending) == 0 {
		return
	}
	a.worker = true
	a.workerDone = make(chan struct{})
	done := a.workerDone
	go a.runWorker(done)
}

func (a *Autosave) runWorker(done chan struct{}) {
	defer func() {
		a.mu.Lock()
		if a.workerDone == done {
			a.worker = false
			a.workerDone = nil
		}
		a.mu.Unlock()
		close(done)
	}()
	for {
		a.mu.Lock()
		if len(a.pending) == 0 {
			a.worker = false
			a.workerDone = nil
			a.mu.Unlock()
			return
		}
		path := a.pending[0]
		a.pending = a.pending[1:]
		delete(a.pendingSet, path)
		entry := a.entries[path]
		if entry == nil || entry.revision <= entry.savedRevision || entry.error != "" || entry.conflict || entry.failClosed || entry.composing || entry.quiesceCount > 0 {
			a.mu.Unlock()
			continue
		}
		entry.saving = true
		a.mu.Unlock()

		for {
			a.mu.Lock()
			entry = a.entries[path]
			if entry == nil || entry.error != "" || entry.conflict || entry.failClosed || entry.composing || entry.quiesceCount > 0 || entry.revision <= entry.savedRevision {
				if entry != nil {
					entry.saving = false
				}
				a.mu.Unlock()
				break
			}
			waiting := entry.timer != nil
			a.mu.Unlock()
			if waiting || (a.isBusy != nil && a.isBusy()) {
				if !sleepContext(a.context, 80*time.Millisecond) {
					return
				}
				continue
			}
			a.mu.Lock()
			entry = a.entries[path]
			if entry == nil || entry.error != "" || entry.conflict || entry.failClosed || entry.composing || entry.quiesceCount > 0 || entry.revision <= entry.savedRevision || entry.timer != nil {
				a.mu.Unlock()
				continue
			}
			snapshot := SaveSnapshot{Revision: entry.revision, Content: entry.content, Captured: time.Now().UTC()}
			a.mu.Unlock()

			var saveErr error
			if a.save == nil {
				saveErr = errors.New("Markdown 保存器未配置")
			} else {
				saveErr = a.saveWithDraftJournal(path, snapshot)
			}

			a.mu.Lock()
			entry = a.entries[path]
			if entry == nil {
				a.mu.Unlock()
				break
			}
			if saveErr != nil {
				var retry RetryWhenIdleError
				var retryPointer *RetryWhenIdleError
				if errors.As(saveErr, &retry) || errors.As(saveErr, &retryPointer) {
					entry.saving = false
					a.enqueueLocked(path)
					a.mu.Unlock()
					if !sleepContext(a.context, 80*time.Millisecond) {
						return
					}
					break
				}
				if errors.Is(saveErr, context.Canceled) && a.context.Err() != nil {
					entry.saving = false
					a.mu.Unlock()
					return
				}
				entry.saving = false
				entry.error = saveErr.Error()
				var conflict ConflictSaveError
				var conflictPointer *ConflictSaveError
				if errors.As(saveErr, &conflict) || errors.As(saveErr, &conflictPointer) {
					entry.conflict = true
				}
				a.mu.Unlock()
				break
			}
			if entry.content == snapshot.Content {
				entry.savedRevision = entry.revision
			} else if snapshot.Revision > entry.savedRevision {
				entry.savedRevision = snapshot.Revision
			}
			if entry.content == snapshot.Content {
				a.stopTimerLocked(entry)
				a.removePendingLocked(path)
				a.stopDraftTimerLocked(entry)
				entry.draftPending = false
			}
			entry.saving = false
			onSaved := a.onSaved
			a.mu.Unlock()
			if onSaved != nil {
				onSaved(path, snapshot)
			}
			break
		}
	}
}

func (a *Autosave) stateOf(entry *saveEntry) SaveState {
	return SaveState{Revision: entry.revision, SavedRevision: entry.savedRevision, Dirty: entry.revision > entry.savedRevision,
		DraftSavedRevision: entry.draftRevision, Saving: entry.saving, Error: entry.error, CacheError: entry.cacheError, Conflict: entry.conflict, Composing: entry.composing}
}

func normalizePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
