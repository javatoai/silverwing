// Package asyncjob tracks the mechanical lifetime of asynchronous requests.
// Request identity, replacement policy, result relevance and business cleanup
// remain with the caller.
package asyncjob

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed means that a Group no longer accepts work.
var ErrClosed = errors.New("async job group is closed")

// Options declares how closing a group affects one request. A request that
// must await a real backend result can opt out of cancellation on close.
type Options struct {
	CancelOnClose bool
}

// Group tracks requests until their caller-declared cleanup point. Its zero
// value is ready for use. A Group must not be copied after its first use.
//
// Begin never cancels or replaces another request. Callers retain their own
// current-request maps and decide whether an earlier read or mutation may be
// retired. Close and Wait are separate so the launch and UI-post boundaries can
// close before a worker waits for its backend and releases business resources.
type Group struct {
	postMu  sync.Mutex
	mu      sync.Mutex
	closed  bool
	closing chan struct{}
	running map[*Lease]struct{}
	drained chan struct{}
	err     error
}

// Lease owns one derived context and completion signal. Complete does not
// cancel its context: some callers finish backend cleanup before a retained UI
// result is settled. The caller must Cancel once it no longer needs the context.
type Lease struct {
	owner         *Group
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	cancelOnClose bool
	completed     bool  // protected by owner.mu
	err           error // protected by owner.mu
}

// Begin registers work, preserving the parent's values, deadline and
// cancellation. It returns ErrClosed without registering work after Close.
func (g *Group) Begin(parent context.Context, options Options) (*Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	if g.closed {
		return nil, ErrClosed
	}
	ctx, cancel := context.WithCancel(parent)
	lease := &Lease{owner: g, ctx: ctx, cancel: cancel, done: make(chan struct{}), cancelOnClose: options.CancelOnClose}
	if len(g.running) == 0 {
		g.drained = make(chan struct{})
	}
	g.running[lease] = struct{}{}
	return lease, nil
}

func (g *Group) initLocked() {
	if g.closing == nil {
		g.closing = make(chan struct{})
	}
	if g.running == nil {
		g.running = make(map[*Lease]struct{})
	}
	if g.drained == nil {
		g.drained = make(chan struct{})
		close(g.drained)
	}
}

// Closed reports whether the group has stopped accepting work and UI posts.
func (g *Group) Closed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// Closing closes when Close runs. A backend that must survive cancellation can
// still stop waiting for a UI callback when its window is shutting down.
func (g *Group) Closing() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.initLocked()
	return g.closing
}

// Close rejects future work and UI posts, then cancels requests that declared
// CancelOnClose. It does not remove requests or claim that cleanup has finished.
func (g *Group) Close() {
	g.postMu.Lock()
	g.mu.Lock()
	g.initLocked()
	if g.closed {
		g.mu.Unlock()
		g.postMu.Unlock()
		return
	}
	g.closed = true
	close(g.closing)
	cancels := make([]context.CancelFunc, 0, len(g.running))
	for lease := range g.running {
		if lease.cancelOnClose {
			cancels = append(cancels, lease.cancel)
		}
	}
	g.mu.Unlock()
	g.postMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Post serializes queue admission with Close. The scheduler must enqueue the
// supplied callback at most once and return without waiting for it; it returns
// false if no callback was queued. It must not call Close synchronously.
//
// Once closed, Post does not invoke the scheduler, and callbacks already queued
// skip apply. Callers still check their own request identity inside apply. Any
// acknowledgement needed even for a skipped callback belongs in the scheduler's
// wrapper; a worker must also observe Closing if the UI can stop draining posts.
func (g *Group) Post(schedule func(func()) bool, apply func()) bool {
	g.postMu.Lock()
	defer g.postMu.Unlock()
	if g.Closed() {
		return false
	}
	return schedule(func() {
		if !g.Closed() {
			apply()
		}
	})
}

// Wait waits for the current busy period to drain. Call Close first when no
// future requests may be admitted. It preserves both the wait context's error
// and every error supplied by completed requests, including on a timed-out wait.
func (g *Group) Wait(ctx context.Context) error {
	g.mu.Lock()
	g.initLocked()
	drained := g.drained
	g.mu.Unlock()
	select {
	case <-drained:
	case <-ctx.Done():
	}
	return errors.Join(ctx.Err(), g.Err())
}

// Err returns the errors that callers have supplied at their cleanup points.
// Business-operation errors may be handled separately by a result consumer;
// the group never filters an error that the caller passes to Complete.
func (g *Group) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

func (l *Lease) Context() context.Context { return l.ctx }
func (l *Lease) Cancel()                  { l.cancel() }
func (l *Lease) Done() <-chan struct{}    { return l.done }

// Complete publishes the caller's cleanup error before closing Done and
// removing the lease from its group. It is safe to call more than once; only
// the first completion takes effect. Cancellation alone never completes work.
func (l *Lease) Complete(err error) {
	g := l.owner
	g.mu.Lock()
	defer g.mu.Unlock()
	if l.completed {
		return
	}
	l.completed, l.err = true, err
	if err != nil {
		g.err = errors.Join(g.err, err)
	}
	delete(g.running, l)
	close(l.done)
	if len(g.running) == 0 {
		close(g.drained)
	}
}

// Err returns the error supplied to Complete, or nil before completion.
func (l *Lease) Err() error {
	l.owner.mu.Lock()
	defer l.owner.mu.Unlock()
	return l.err
}

// Wait waits for this request's cleanup, preserving both a wait context error
// and its completion error. A timeout does not remove or complete the lease.
func (l *Lease) Wait(ctx context.Context) error {
	select {
	case <-l.done:
	case <-ctx.Done():
	}
	return errors.Join(ctx.Err(), l.Err())
}
