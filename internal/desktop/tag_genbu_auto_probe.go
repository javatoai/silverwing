package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"silverwing-go/internal/application"
)

// Schedule locally at one-second resolution; only due records issue a backend
// query, with the separate active/idle 5s/30s minimum intervals below.
const tagGenbuAutoProbeInterval = time.Second

type tagGenbuAutoProbeWindow interface {
	Update(func())
	IsDestroyed() bool
}

// tagGenbuAutoProbeState tracks page generations and the cleanup set. The
// current session is UI-thread-owned; only mu, closed, sessions and requests
// are shared with timer/backend workers.
type tagGenbuAutoProbeState struct {
	current        *tagGenbuAutoProbeSession
	next           uint64
	visibleScope   string
	refreshPending bool
	forcePending   bool
	forceIdentity  string
	now            func() time.Time
	mu             sync.Mutex
	sessions       map[*tagGenbuAutoProbeSession]struct{}
	requests       map[*tagGenbuAutoProbeRequest]struct{}
	closed         bool
}

type tagGenbuAutoProbeSession struct {
	generation       uint64
	identity         string
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	request          *tagGenbuAutoProbeRequest            // UI-thread-owned
	historyRefresh   bool                                 // UI-thread-owned; consumed when the request starts
	retentionPending bool                                 // Retry on timer, never on every frame.
	probes           map[string]*tagGenbuAutoProbeRequest // repository lanes, UI-thread-owned
	lastProbe        map[string]time.Time
	forceRecords     map[string]bool
}

type tagGenbuAutoProbeRequest struct {
	session     *tagGenbuAutoProbeSession
	identity    string
	action      application.Action
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	recordKey   string
	repoKey     string
	jobID       string
	jobSequence uint64
}

type tagGenbuAutoProbeIdentity struct {
	Home      string
	Config    string
	TagRecord []tagGenbuTagIdentity
}

type tagGenbuTagIdentity struct {
	ID, TaskID, ServiceID, ModuleID, Tag, Updated string
}

// observeTagGenbuAutoProbe is called from the UI render tail. It owns only
// the visible page's cancellable polling session; automatic dispatches bypass
// the global busy slot and backend gate.
func (s *app) observeTagGenbuAutoProbe() {
	if s == nil {
		return
	}
	var win tagGenbuAutoProbeWindow
	if s.win != nil {
		win = s.win
	}
	s.observeTagGenbuAutoProbeWindow(win)
}

func (s *app) observeTagGenbuAutoProbeWindow(win tagGenbuAutoProbeWindow) {
	if s == nil {
		return
	}
	identity := s.taskTagGenbuIdentity()
	if identity == "" {
		s.stopTagGenbuAutoProbe()
		if state := s.tagGenbuAutoProbe; state != nil {
			state.visibleScope, state.refreshPending, state.forcePending, state.forceIdentity = "", false, false, ""
		}
		return
	}
	if s.loading || s.backend == nil || win == nil || win.IsDestroyed() {
		s.stopTagGenbuAutoProbe()
		return
	}
	// Restore the current job's complete item set before reading local history,
	// which may still contain only the first persisted item of an active batch.
	if _, ok := s.backend.(application.TagJobBackend); ok {
		if s.tagJobs == nil || s.tagJobs.current == nil || s.tagJobs.current.scope != s.tagJobScope() || !s.tagJobs.current.loaded {
			s.stopTagGenbuAutoProbe()
			return
		}
	}
	state := s.ensureTagGenbuAutoProbeState()
	scope := identity // Includes saved retention settings as well as task identity.
	if state.visibleScope != scope {
		state.visibleScope, state.refreshPending = scope, true
		if state.forceIdentity != identity {
			state.forcePending = false
			state.forceIdentity = ""
		}
	}
	session := s.ensureTagGenbuAutoProbe(identity, win)
	if session != nil && state.refreshPending && session.request == nil {
		state.refreshPending = false
		session.historyRefresh = true
		s.startTagGenbuAutoProbeRequest(session, win)
	}
}

// Entry, repeated tab clicks and the menu share one cancellable request owner.
// Multiple clicks during an in-flight request coalesce into one pending refresh.
func (s *app) requestTagHistoryRefresh() {
	s.ensureTagGenbuAutoProbeState().refreshPending = true
	s.tagGenbuAutoProbe.forcePending = true
	s.tagGenbuAutoProbe.forceIdentity = s.taskTagGenbuIdentity()
}

func (s *app) taskTagGenbuIdentity() string {
	if s == nil || (s.page != "tasks" && s.page != "archive") || s.tab != taskTagTab {
		return ""
	}
	t := s.currentTask()
	if t.id == "" {
		return ""
	}
	encoded, _ := json.Marshal(s.tagJobScope())
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *app) ensureTagGenbuAutoProbe(identity string, win tagGenbuAutoProbeWindow) *tagGenbuAutoProbeSession {
	if s == nil || s.backend == nil || win == nil || win.IsDestroyed() {
		return nil
	}
	state := s.ensureTagGenbuAutoProbeState()
	if state.current != nil && state.current.identity == identity && state.current.ctx.Err() == nil {
		return state.current
	}
	s.stopTagGenbuAutoProbe()

	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil
	}
	state.next++
	ctx, cancel := context.WithCancel(context.Background())
	session := &tagGenbuAutoProbeSession{generation: state.next, identity: identity, ctx: ctx, cancel: cancel, done: make(chan struct{}), historyRefresh: state.refreshPending,
		probes: make(map[string]*tagGenbuAutoProbeRequest), lastProbe: make(map[string]time.Time), forceRecords: make(map[string]bool)}
	state.refreshPending = false
	state.current = session
	if state.sessions == nil {
		state.sessions = make(map[*tagGenbuAutoProbeSession]struct{})
	}
	state.sessions[session] = struct{}{}
	state.mu.Unlock()
	go s.runTagGenbuAutoProbe(session, win)
	s.startTagGenbuAutoProbeRequest(session, win)
	return session
}

func (s *app) ensureTagGenbuAutoProbeState() *tagGenbuAutoProbeState {
	if s.tagGenbuAutoProbe == nil {
		s.tagGenbuAutoProbe = &tagGenbuAutoProbeState{
			sessions: make(map[*tagGenbuAutoProbeSession]struct{}),
			requests: make(map[*tagGenbuAutoProbeRequest]struct{}),
		}
	}
	return s.tagGenbuAutoProbe
}

func (s *app) runTagGenbuAutoProbe(session *tagGenbuAutoProbeSession, win tagGenbuAutoProbeWindow) {
	defer func() {
		state := s.tagGenbuAutoProbe
		if state == nil {
			close(session.done)
			return
		}
		state.mu.Lock()
		delete(state.sessions, session)
		close(session.done)
		state.mu.Unlock()
	}()
	ticker := time.NewTicker(tagGenbuAutoProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-session.ctx.Done():
			return
		case <-ticker.C:
			s.queueTagGenbuAutoProbe(session, win)
		}
	}
}

func (s *app) queueTagGenbuAutoProbe(session *tagGenbuAutoProbeSession, win tagGenbuAutoProbeWindow) {
	if session == nil || session.ctx.Err() != nil || win == nil || win.IsDestroyed() {
		return
	}
	win.Update(func() {
		if session.ctx.Err() != nil || !s.tagGenbuAutoProbeSessionRelevant(session) ||
			s.loading || s.backend == nil || win.IsDestroyed() ||
			s.taskTagGenbuIdentity() != session.identity || session.request != nil {
			return
		}
		if session.retentionPending {
			if view := s.currentTagJobView(); view == nil || !view.snapshot.Busy {
				session.historyRefresh = true
			}
		}
		s.startTagGenbuAutoProbeRequest(session, win)
	})
}

func (s *app) startTagGenbuAutoProbeRequest(session *tagGenbuAutoProbeSession, win tagGenbuAutoProbeWindow) {
	state := s.tagGenbuAutoProbe
	if state == nil || session == nil || session.ctx.Err() != nil || session.request != nil {
		return
	}
	if !session.historyRefresh {
		s.startDueTagGenbuProbes(session, win)
		return
	}
	ctx, cancel := context.WithCancel(session.ctx)
	t := s.currentTask()
	action := application.Action{Kind: "tag.history", TaskID: t.id, Payload: map[string]any{
		"force": false, "tagHistoryHome": s.snapshot.Home, "tagHistoryTaskRoot": s.configString("taskRoot"),
		"tagHistoryTaskPath": t.path, "tagHistoryTaskCreatedAt": t.createdAt,
		"tagJobAccountIdentity": s.tagJobScope().AccountIdentity,
	}}
	session.historyRefresh = false
	session.retentionPending = false // A real read error waits for explicit retry.
	s.ensureTaskTagHistoryView(t).Error = ""
	*s.input(tagGenbuTaskErrorKey(t.id), "") = ""
	request := &tagGenbuAutoProbeRequest{session: session, identity: session.identity, action: action, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if view := s.currentTagJobView(); view != nil {
		request.jobID, request.jobSequence = view.snapshot.ID, view.snapshot.Sequence
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		cancel()
		return
	}
	if state.requests == nil {
		state.requests = make(map[*tagGenbuAutoProbeRequest]struct{})
	}
	state.requests[request] = struct{}{}
	state.mu.Unlock()
	session.request = request
	backend := s.backend
	go s.runTagGenbuAutoProbeRequest(backend, win, request)
}

func (s *app) runTagGenbuAutoProbeRequest(backend application.Backend, win tagGenbuAutoProbeWindow, request *tagGenbuAutoProbeRequest) {
	defer func() {
		request.cancel()
		state := s.tagGenbuAutoProbe
		if state == nil {
			close(request.done)
			return
		}
		state.mu.Lock()
		delete(state.requests, request)
		close(request.done)
		state.mu.Unlock()
	}()
	if err := request.ctx.Err(); err != nil {
		return
	}
	var result application.ActionResult
	var err error
	if probe, ok := backend.(application.TagProbeBackend); ok {
		result, err = probe.DispatchTagProbe(request.ctx, request.action, func(record application.TagRecord) {
			s.updateTagGenbuProbe(request, win, func() { s.applyTagProbeRecord(request, record) })
		})
	} else {
		result, err = backend.Dispatch(request.ctx, request.action)
	}
	if request.ctx.Err() != nil || win == nil || win.IsDestroyed() {
		return
	}
	updated := make(chan struct{})
	win.Update(func() {
		defer close(updated)
		if request.session.request == request {
			request.session.request = nil
		}
		if !s.tagGenbuAutoProbeRequestRelevant(request) {
			return
		}
		if request.action.Kind == "tag.history" {
			if err != nil {
				s.ensureTagHistoryView().Error = "刷新 Tag 失败：" + err.Error()
				return
			}
			s.applyDeliveryActionResult(request.action, result)
			s.restoreActiveTagJobRecords()
			// A job can finish between the local disk read and this UI callback.
			// Keep the sequence that arrived after that read, including its final
			// result, instead of briefly replacing it with the older disk view.
			if view := s.currentTagJobView(); view != nil && (view.snapshot.ID != request.jobID || view.snapshot.Sequence > request.jobSequence) {
				s.upsertTagJobRecords(view.snapshot)
			}
			request.session.retentionPending = len(filterTagHistory(s.taskTagHistoryRecords(request.action.TaskID), "", false)) > s.configInt("tagHistoryMaxGroups", 3)
			if s.tagGenbuAutoProbe.forcePending {
				for _, record := range s.taskTagHistoryRecords(request.action.TaskID) {
					request.session.forceRecords[record.ID] = true
				}
				s.tagGenbuAutoProbe.forcePending = false
			}
			s.startDueTagGenbuProbes(request.session, win)
			return
		}
		delete(request.session.probes, request.repoKey)
		request.session.lastProbe[request.recordKey] = s.tagGenbuProbeNow()
		defer s.startDueTagGenbuProbes(request.session, win)
		if err != nil {
			s.applyTagStatusFailure(request.action, err)
			return
		}
		s.applyTagStatusResult(request.action, result)
	})
	select {
	case <-updated:
	case <-request.ctx.Done():
	}
}

func (s *app) tagGenbuAutoProbeSessionRelevant(session *tagGenbuAutoProbeSession) bool {
	if s == nil || session == nil || session.ctx.Err() != nil || s.tagGenbuAutoProbe == nil || s.tagGenbuAutoProbe.current != session {
		return false
	}
	state := s.tagGenbuAutoProbe
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	return !closed
}

func (s *app) tagGenbuAutoProbeRequestRelevant(request *tagGenbuAutoProbeRequest) bool {
	if s == nil || request == nil || request.ctx.Err() != nil || request.session == nil ||
		!s.tagGenbuAutoProbeSessionRelevant(request.session) || request.identity != request.session.identity ||
		s.loading || s.backend == nil {
		return false
	}
	return s.taskTagGenbuIdentity() == request.identity && s.currentTask().id == request.action.TaskID
}

func (s *app) stopTagGenbuAutoProbe() {
	if s == nil || s.tagGenbuAutoProbe == nil || s.tagGenbuAutoProbe.current == nil {
		return
	}
	session := s.tagGenbuAutoProbe.current
	s.tagGenbuAutoProbe.current = nil
	session.cancel()
	if session.request != nil {
		session.request.cancel()
	}
}

func (s *app) closeTagGenbuAutoProbe(ctx context.Context) error {
	if s == nil || s.tagGenbuAutoProbe == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state := s.tagGenbuAutoProbe
	state.mu.Lock()
	state.closed = true
	sessions := make([]*tagGenbuAutoProbeSession, 0, len(state.sessions))
	for session := range state.sessions {
		session.cancel()
		sessions = append(sessions, session)
	}
	requests := make([]*tagGenbuAutoProbeRequest, 0, len(state.requests))
	for request := range state.requests {
		request.cancel()
		requests = append(requests, request)
	}
	state.mu.Unlock()
	s.stopTagGenbuAutoProbe()
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-ctx.Done():
			return fmt.Errorf("等待 Tag 自动 Genbu 探测定时任务清理：%w", ctx.Err())
		}
	}
	for _, request := range requests {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待 Tag 自动 Genbu 后端探测清理：%w", ctx.Err())
		}
	}
	return nil
}

func tagGenbuIdentity(snapshot application.Snapshot) string {
	items := make([]tagGenbuTagIdentity, 0, len(snapshot.TagHistory))
	for _, record := range snapshot.TagHistory {
		items = append(items, tagGenbuTagIdentity{
			ID: record.ID, TaskID: record.TaskID, ServiceID: record.ServiceID,
			ModuleID: record.ModuleID, Tag: record.Tag, Updated: record.Updated,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		left, _ := json.Marshal(items[i])
		right, _ := json.Marshal(items[j])
		return string(left) < string(right)
	})
	encoded, _ := json.Marshal(tagGenbuAutoProbeIdentity{Home: snapshot.Home, Config: snapshot.ConfigJSON, TagRecord: items})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
