package editor

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrDraftUnsupported = errors.New("此编辑器未配置持久草稿")

type draftWork struct {
	context context.Context
	work    func() error
	done    chan error
}

// The journal serializes cache writes, explicit discard and successful document
// saves. Typing after a save was queued remains a later write with the new hash.
type draftJournal struct {
	mu      sync.Mutex
	queue   []draftWork
	running bool
}

func (j *draftJournal) submit(ctx context.Context, work func() error) <-chan error {
	done := make(chan error, 1)
	j.mu.Lock()
	j.queue = append(j.queue, draftWork{context: ctx, work: work, done: done})
	if !j.running {
		j.running = true
		go j.run()
	}
	j.mu.Unlock()
	return done
}

func (j *draftJournal) run() {
	for {
		j.mu.Lock()
		if len(j.queue) == 0 {
			j.running = false
			j.mu.Unlock()
			return
		}
		job := j.queue[0]
		j.queue[0] = draftWork{}
		j.queue = j.queue[1:]
		j.mu.Unlock()
		err := job.context.Err()
		if err == nil {
			err = job.work()
		}
		job.done <- err
		close(job.done)
	}
}

func (j *draftJournal) wait(ctx context.Context, work func() error) error {
	select {
	case err := <-j.submit(ctx, work):
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Autosave) SetDraftWriter(write func(context.Context, string, string) error, remove func(context.Context, string) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.draftWriter, a.draftRemover = write, remove
	if write != nil && a.draftJournal == nil {
		a.draftJournal = &draftJournal{}
	}
}

func (a *Autosave) queueDraftLocked(key string) {
	entry := a.entries[key]
	if entry == nil || a.draftWriter == nil || entry.revision <= entry.savedRevision {
		return
	}
	entry.draftPending = true
	a.stopDraftTimerLocked(entry)
	var timer *time.Timer
	timer = time.AfterFunc(250*time.Millisecond, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.entries[key] == entry && entry.draftTimer == timer {
			entry.draftTimer = nil
			a.submitDraftLocked(key, false)
		}
	})
	entry.draftTimer = timer
}

func (a *Autosave) stopDraftTimerLocked(entry *saveEntry) {
	if entry.draftTimer != nil {
		entry.draftTimer.Stop()
		entry.draftTimer = nil
	}
}

func (a *Autosave) submitDraftLocked(key string, force bool) <-chan error {
	entry := a.entries[key]
	if entry == nil || !entry.draftPending || a.draftJournal == nil || !force && entry.quiesceCount > 0 {
		return nil
	}
	a.stopDraftTimerLocked(entry)
	entry.draftPending = false
	writer := a.draftWriter
	return a.draftJournal.submit(a.context, func() error {
		a.mu.Lock()
		entry := a.entries[key]
		if entry == nil || entry.revision <= entry.savedRevision {
			a.mu.Unlock()
			return nil
		}
		if !force && entry.quiesceCount > 0 {
			entry.draftPending = true
			a.mu.Unlock()
			return nil
		}
		entry.draftSaving = true
		content := entry.content
		revision := entry.revision
		a.mu.Unlock()
		err := writer(a.context, key, content)
		a.mu.Lock()
		entry.draftUnsupported = errors.Is(err, ErrDraftUnsupported)
		if entry.draftUnsupported {
			err = nil
		}
		entry.draftSaving = false
		entry.cacheError = ""
		if err != nil {
			entry.cacheError = err.Error()
		}
		if err == nil && !entry.draftUnsupported {
			entry.draftRevision = revision
		}
		a.mu.Unlock()
		return err
	})
}

func (a *Autosave) FlushDrafts(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	journal := a.draftJournal
	for key, entry := range a.entries {
		if entry.revision > entry.savedRevision {
			entry.draftPending = true
		}
		a.submitDraftLocked(key, true)
	}
	a.mu.Unlock()
	if journal == nil {
		return nil
	}
	if err := journal.wait(ctx, func() error { return nil }); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, entry := range a.entries {
		if entry.revision > entry.savedRevision && entry.cacheError != "" {
			return errors.New(entry.cacheError)
		}
	}
	return nil
}

func (a *Autosave) DiscardDraft(ctx context.Context, key string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	journal, remove := a.draftJournal, a.draftRemover
	if entry := a.entries[normalizePath(key)]; entry != nil {
		a.stopDraftTimerLocked(entry)
		entry.draftPending = false
	}
	a.mu.Unlock()
	if journal == nil || remove == nil {
		return nil
	}
	return journal.wait(ctx, func() error { return remove(ctx, normalizePath(key)) })
}

// Restored content is visible immediately but does not start automatic saving
// until a real edit or an explicit save. This matches the source draft store.
func (a *Autosave) RestoreDraft(path, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.entryLocked(normalizePath(path))
	if entry.revision > entry.savedRevision {
		return
	}
	entry.content = content
	entry.revision++
	entry.draftRevision = entry.revision
}

// Closing makes the latest draft durable and waits for saves already running.
// It does not turn restored or conflicted drafts into implicit disk writes.
func (a *Autosave) PrepareClose(ctx context.Context) []FlushFailure {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.FlushDrafts(ctx); err != nil && ctx.Err() != nil {
		return a.PendingFailures()
	}
	a.mu.Lock()
	for key, entry := range a.entries {
		if (a.draftWriter == nil || entry.draftUnsupported) && entry.revision > entry.savedRevision && !entry.composing && entry.error == "" && !entry.conflict && !entry.failClosed && entry.quiesceCount == 0 {
			a.stopTimerLocked(entry)
			a.enqueueLocked(key)
		}
	}
	a.mu.Unlock()
	for {
		a.mu.Lock()
		done, running := a.workerDone, a.worker
		a.mu.Unlock()
		if !running {
			return a.PendingFailures()
		}
		select {
		case <-done:
		case <-ctx.Done():
			return a.PendingFailures()
		}
	}
}

func (a *Autosave) SetCacheError(path, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entryLocked(normalizePath(path)).cacheError = message
}

func (a *Autosave) saveWithDraftJournal(path string, snapshot SaveSnapshot) error {
	a.mu.Lock()
	journal := a.draftJournal
	var draftDone <-chan error
	if journal != nil {
		if entry := a.entries[path]; entry != nil {
			// A disk save can beat the draft debounce. Persist the latest source
			// in this key's journal before completing its draft save; otherwise
			// an older cached draft can be mistaken for typing during the save.
			entry.draftPending = true
			draftDone = a.submitDraftLocked(path, true)
		}
	}
	a.mu.Unlock()
	if a.save == nil {
		return errors.New("Markdown 保存器未配置")
	}
	if journal == nil {
		return a.save(a.context, path, snapshot)
	}
	return journal.wait(a.context, func() error {
		if draftDone != nil {
			if err := <-draftDone; err != nil {
				return err
			}
		}
		if err := a.save(a.context, path, snapshot); err != nil {
			return err
		}
		// Mark the revision inside the journal so a later cache job cannot
		// recreate a draft that this successful save has just cleared.
		a.mu.Lock()
		if entry := a.entries[path]; entry != nil {
			if entry.content == snapshot.Content {
				entry.savedRevision = entry.revision
				a.stopDraftTimerLocked(entry)
				entry.draftPending = false
			} else if snapshot.Revision > entry.savedRevision {
				entry.savedRevision = snapshot.Revision
			}
		}
		a.mu.Unlock()
		return nil
	})
}
