package desktop

import (
	"context"
	"errors"
	"sort"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/releasenotice"
)

type noticeSendState struct {
	loading, sending           bool
	owner                      taskFilesIdentity
	kind, link                 string
	groupID, configID, batchID string
	session                    *releasenotice.Session
	cancel                     context.CancelFunc
}

type releaseNoticeController struct {
	pending *noticeSendState
	stopped bool
	wg      sync.WaitGroup
}

func (n *releaseNoticeController) running() bool {
	return n.pending != nil && (n.pending.loading || n.pending.sending)
}

// Use the complete batch. Search/visibility projections cannot enable a
// notification for only the successful subset of a batch.
func (s *app) releaseNoticeReady(group tagHistoryGroup) bool {
	c, e := s.readSnapshotAppConfig()
	if e != nil || !c.TagEnabled || len(group.Item.Operations) == 0 {
		return false
	}
	t := s.taskUnit.currentTask()
	for _, op := range group.Item.Operations {
		service, e := c.GroupService(t.groupID, op.GroupServiceID)
		if e != nil || !service.GenbuProbeEnabled || op.State != domain.TagOperationSuccess || op.Tag == nil || *op.Tag == "" || op.GenbuStatus.Build != domain.GenbuSuccess || op.GenbuStatus.NotFound || op.GenbuStatus.FailureReason != nil && *op.GenbuStatus.FailureReason != "" {
			return false
		}
	}
	return true
}

func releaseNoticeBatchID(g tagHistoryGroup) string {
	rows := make([]releasenotice.Row, 0, len(g.Item.Operations))
	for _, op := range g.Item.Operations {
		tag := ""
		if op.Tag != nil {
			tag = *op.Tag
		}
		rows = append(rows, releasenotice.Row{OperationID: op.OperationID, Service: op.ServiceName, Version: tag})
	}
	// Group order changes when a probe updates UpdatedAt; scope follows IDs.
	sort.Slice(rows, func(i, j int) bool { return rows[i].OperationID < rows[j].OperationID })
	return releasenotice.Digest(rows)
}

func (s *app) sendTagNotice(history *tagHistoryViewState, g tagHistoryGroup) {
	if s.busy || s.releaseNotices.running() || s.releaseNotices.stopped || !s.tagHistoryStateCurrent(history) || !s.releaseNoticeReady(g) {
		return
	}
	cfg, e := s.readSnapshotAppConfig()
	if e != nil {
		s.lastError = e.Error()
		return
	}
	d := &noticeSendState{owner: s.taskFilesIdentity(), link: s.taskUnit.currentTask().requirementURL, groupID: g.Item.GroupID,
		configID: application.ReleaseNoticeConfigIdentity(cfg), batchID: releaseNoticeBatchID(g)}
	s.startNoticeSend(d)
}

func (s *app) sendBranchNotice(t task) {
	if s.busy || s.releaseNotices.running() || s.releaseNotices.stopped || s.taskUnit.currentTask().id != t.id {
		return
	}
	rows, e := releasenotice.BranchRows(taskBranchInfoManifest(t).Services)
	if e != nil {
		s.lastError = e.Error()
		return
	}
	cfg, e := s.readSnapshotAppConfig()
	if e != nil {
		s.lastError = e.Error()
		return
	}
	d := &noticeSendState{kind: releasenotice.Branches, owner: s.taskFilesIdentity(), link: t.requirementURL,
		configID: application.BranchNoticeConfigIdentity(cfg), batchID: releasenotice.Digest(rows)}
	s.startNoticeSend(d)
}

func (s *app) startNoticeSend(d *noticeSendState) {
	s.dismissReleaseNotice()
	d.loading = true
	s.releaseNotices.pending = d
	s.lastError, s.lastMessage = "", ""
	s.runReleaseNotice(d)
}

func (s *app) releaseNoticeCurrent(d *noticeSendState) bool {
	if d == nil || s.releaseNotices.stopped || s.releaseNotices.pending != d || s.page != "tasks" && s.page != "archive" || s.workspaceReviewPageVisible() {
		return false
	}
	expectedTab := taskTagTab
	if d.kind == releasenotice.Branches {
		expectedTab = 0
	}
	if s.tab != expectedTab || s.taskFilesIdentity() != d.owner || s.taskUnit.currentTask().requirementURL != d.link {
		return false
	}
	cfg, e := s.readSnapshotAppConfig()
	if e != nil {
		return false
	}
	if d.kind == releasenotice.Branches {
		rows, e := releasenotice.BranchRows(taskBranchInfoManifest(s.taskUnit.currentTask()).Services)
		return e == nil && application.BranchNoticeConfigIdentity(cfg) == d.configID && releasenotice.Digest(rows) == d.batchID
	}
	if application.ReleaseNoticeConfigIdentity(cfg) != d.configID {
		return false
	}
	for _, g := range filterTagHistory(s.taskTagHistoryRecords(d.owner.id), "", false) {
		if g.Item.GroupID == d.groupID {
			return releaseNoticeBatchID(g) == d.batchID
		}
	}
	return false
}

func (s *app) observeReleaseNotice() {
	if d := s.releaseNotices.pending; d != nil && !s.releaseNoticeCurrent(d) {
		s.dismissReleaseNotice()
	}
}
func (s *app) dismissReleaseNotice() {
	if d := s.releaseNotices.pending; d != nil && d.cancel != nil {
		d.cancel()
	}
	s.releaseNotices.pending = nil
}
func (s *app) stopReleaseNotices() { s.releaseNotices.stopped = true; s.dismissReleaseNotice() }
func (s *app) closeReleaseNotices(ctx context.Context) error {
	s.stopReleaseNotices()
	done := make(chan struct{})
	go func() { s.releaseNotices.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *app) releaseNoticeAction(d *noticeSendState, kind string) application.Action {
	if d.kind == releasenotice.Branches {
		action := d.owner.action(kind, "")
		action.Payload["noticeConfigIdentity"] = d.configID
		return action
	}
	payload := map[string]any{"tagHistoryHome": d.owner.home, "tagHistoryTaskRoot": d.owner.taskRoot, "tagHistoryTaskPath": d.owner.taskPath, "tagHistoryTaskCreatedAt": d.owner.createdAt,
		"groupId": d.groupID, "noticeConfigIdentity": d.configID}
	return application.Action{Kind: kind, TaskID: d.owner.id, Payload: payload}
}
func (d *noticeSendState) actionKind(phase string) string {
	if d.kind == releasenotice.Branches {
		return "task.branch-notice." + phase
	}
	return "tag.notice." + phase
}

// The worker owns the two dispatch phases; the UI owns only scoped progress.
func (s *app) runReleaseNotice(d *noticeSendState) {
	if !s.releaseNoticeCurrent(d) {
		return
	}
	if s.win == nil || s.win.IsDestroyed() || s.backend == nil {
		d.loading, d.sending = false, false
		s.lastError = "通知窗口或后端不可用"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	window, backend := s.win, s.backend
	prepare := s.releaseNoticeAction(d, d.actionKind("prepare"))
	send := s.releaseNoticeAction(d, d.actionKind("send"))
	s.releaseNotices.wg.Add(1)
	go func() {
		defer s.releaseNotices.wg.Done()
		defer cancel()
		update := func(apply func()) error {
			if window.IsDestroyed() {
				return context.Canceled
			}
			done := make(chan struct{})
			applied := false
			window.Update(func() {
				defer close(done)
				if ctx.Err() != nil || !s.releaseNoticeCurrent(d) {
					return
				}
				apply()
				applied = true
			})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
			}
			if !applied {
				return context.Canceled
			}
			return nil
		}
		session, e := executeNoticeSend(ctx, prepare, send, backend.Dispatch, func(v releasenotice.Session) error {
			return update(func() { d.session = &v; d.loading = false; d.sending = true })
		})
		if ctx.Err() != nil {
			return
		}
		_ = update(func() {
			d.loading, d.sending = false, false
			if session.ID != "" {
				d.session = &session
			}
			s.finishNoticeSend(session, e)
		})
	}()
}

func (s *app) finishNoticeSend(session releasenotice.Session, e error) {
	if session.Audience == releasenotice.UserTesters {
		complete := len(session.Recipients) > 0
		for _, r := range session.Deliveries() {
			if r.State != "sent" {
				complete = false
			}
		}
		if e != nil {
			s.lastError = session.DeliverySummary() + "；" + e.Error()
		} else if !complete {
			s.lastError = session.DeliverySummary()
		} else {
			s.lastMessage = session.DeliverySummary()
		}
		return
	}
	if e != nil {
		s.lastError = e.Error()
		return
	}
	if r, ok := session.LatestReceipt(); ok {
		if r.State == "sent" && r.MessageID != "" {
			s.lastMessage = "已发送给自己"
			return
		}
		if r.Error != "" {
			s.lastError = r.Error
			return
		}
	}
	s.lastError = "发送结果待核实，请重试原通知"
}

type noticeDispatch func(context.Context, application.Action) (application.ActionResult, error)

// Receivers are never supplied by the UI. Only retry intent crosses dispatch.
func executeNoticeSend(ctx context.Context, prepare, send application.Action, dispatch noticeDispatch, prepared func(releasenotice.Session) error) (releasenotice.Session, error) {
	result, e := dispatch(ctx, prepare)
	if e != nil {
		return releasenotice.Session{}, e
	}
	session, e := decodeTaskFilesResult[releasenotice.Session](result)
	if e != nil || session.ID == "" {
		return session, errors.New("通知准备结果无效")
	}
	choice, e := session.SendChoice()
	if e != nil {
		return session, e
	}
	if e = ctx.Err(); e != nil {
		return session, e
	}
	if prepared != nil {
		if e = prepared(session); e != nil {
			return session, e
		}
	}
	if e = ctx.Err(); e != nil {
		return session, e
	}
	payload := make(map[string]any, len(send.Payload)+3)
	for k, v := range send.Payload {
		payload[k] = v
	}
	payload["sessionId"], payload["selection"], payload["confirmed"] = session.ID, choice, true
	send.Payload = payload
	result, e = dispatch(ctx, send)
	if result.Data != nil {
		v, decodeErr := decodeTaskFilesResult[releasenotice.Session](result)
		if decodeErr == nil && v.ID != "" {
			session = v
		} else if e == nil {
			e = errors.New("通知发送结果无效")
		}
	} else if e == nil {
		e = errors.New("通知未返回发送结果")
	}
	return session, e
}
