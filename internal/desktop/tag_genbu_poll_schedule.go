package desktop

import (
	"context"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func (s *app) tagGenbuProbeNow() time.Time {
	if state := s.releaseUnit.state.tagGenbuAutoProbe; state != nil && state.now != nil {
		return state.now()
	}
	return time.Now()
}

// Zero means automatic probing has finished. An unrecognised new Tag gets a
// short discovery window, then remains discoverable without a hot idle loop.
func tagGenbuProbeInterval(record application.TagRecord, now time.Time) time.Duration {
	status := record.GenbuStatus
	if !domain.TagGenbuProbeEligible(domain.TagOperationState(record.State), record.Tag) || status.UAT == domain.GenbuSuccess || status.Build == domain.GenbuFailed || status.StoppedByNewerRelease {
		return 0
	}
	started := tagRecordTime(record.Updated)
	if started.IsZero() {
		started = tagRecordTime(record.CreatedAt)
	}
	newTag := !started.IsZero() && now.Sub(started) < 2*time.Minute
	if status.NotFound || status.Build == "" || status.Build == domain.GenbuUnknown {
		if newTag {
			return 5 * time.Second
		}
		return 30 * time.Second
	}
	if status.Build == domain.GenbuBuilding || status.Build == domain.GenbuInitial || status.UAT == domain.GenbuBuilding || status.UAT == domain.GenbuInitial {
		return 5 * time.Second
	}
	return 30 * time.Second
}

func tagRecordTime(value string) time.Time {
	parsed, _ := domain.ParseSilverWingTime(value)
	return parsed
}

func tagGenbuProbeRecordKey(record application.TagRecord) string {
	return strings.Join([]string{record.TaskID, record.ID, record.ServiceID, record.ModuleID, record.Tag, record.Updated}, "\x00")
}

func (s *app) tagGenbuProbeRepository(record application.TagRecord) string {
	t := s.taskUnit.taskByID(record.TaskID)
	for _, workspace := range t.workspaces {
		if workspace.ServiceID == record.ServiceID && workspace.ModuleID == record.ModuleID {
			return tagWorkspaceRepositoryKey(workspace)
		}
	}
	return record.ServiceID + ":" + record.ModuleID
}

// Each repository has its own in-flight request. A slow item cannot hold back
// another repository's accepted result or next scheduled query.
func (s *app) startDueTagGenbuProbes(session *tagGenbuAutoProbeSession, win tagGenbuAutoProbeWindow) {
	if !s.tagGenbuAutoProbeSessionRelevant(session) || session.request != nil {
		return
	}
	state := s.releaseUnit.state.tagGenbuAutoProbe
	if state.refreshPending {
		state.refreshPending = false
		session.historyRefresh = true
		s.startTagGenbuAutoProbeRequest(session, win)
		return
	}
	records := s.taskTagHistoryRecords(s.taskUnit.currentTask().id)
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt > records[j].CreatedAt })
	now := s.tagGenbuProbeNow()
	var roundCtx context.Context
	for _, record := range records {
		if !domain.TagGenbuProbeEligible(domain.TagOperationState(record.State), record.Tag) {
			delete(session.forceRecords, record.ID)
			continue
		}
		key := tagGenbuProbeRecordKey(record)
		repository := s.tagGenbuProbeRepository(record)
		force := session.forceRecords[record.ID]
		if session.probes[repository] != nil {
			continue
		}
		interval := tagGenbuProbeInterval(record, now)
		if !force && (interval == 0 || !session.lastProbe[key].IsZero() && now.Sub(session.lastProbe[key]) < interval) {
			continue
		}
		if roundCtx == nil {
			roundCtx = application.NewTagProbeRoundContext(session.ctx)
		}
		ctx, cancel := context.WithCancel(roundCtx)
		payload := s.tagHistoryRecordPayload(record)
		payload["force"] = force
		payload["tagJobAccountIdentity"] = s.tagJobScope().AccountIdentity
		action := application.Action{Kind: "tag.status", TaskID: record.TaskID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Payload: payload}
		request := &tagGenbuAutoProbeRequest{session: session, identity: session.identity, action: action, ctx: ctx, cancel: cancel, done: make(chan struct{}), recordKey: key, repoKey: repository}
		state.mu.Lock()
		if state.closed {
			state.mu.Unlock()
			cancel()
			return
		}
		state.requests[request] = struct{}{}
		state.mu.Unlock()
		session.probes[repository] = request
		delete(session.forceRecords, record.ID)
		go s.runTagGenbuAutoProbeRequest(s.backend, win, request)
	}
}

func (s *app) updateTagGenbuProbe(request *tagGenbuAutoProbeRequest, win tagGenbuAutoProbeWindow, apply func()) {
	if request.ctx.Err() != nil || win == nil || win.IsDestroyed() {
		return
	}
	updated := make(chan struct{})
	win.Update(func() {
		defer close(updated)
		if s.tagGenbuAutoProbeRequestRelevant(request) {
			apply()
		}
	})
	select {
	case <-updated:
	case <-request.ctx.Done():
	}
}

func (s *app) applyTagProbeRecord(request *tagGenbuAutoProbeRequest, record application.TagRecord) {
	if record.TaskID != request.action.TaskID || record.ID == "" {
		return
	}
	if request.action.Kind == "tag.status" {
		// applyTagStatusResult retains the operation/tag/update CAS and never
		// replaces the local job state or phase with a Genbu response.
		s.restoreActiveTagJobRecords()
		s.applyTagStatusResult(request.action, application.ActionResult{Data: record})
		return
	}
	for i, current := range s.snapshot.TagHistory {
		if current.TaskID != record.TaskID || current.ID != record.ID {
			continue
		}
		if _, owned := s.tagRecordJobItem(current); owned {
			view := s.currentTagJobView()
			if view.snapshot.Busy || view.snapshot.ID != request.jobID || view.snapshot.Sequence > request.jobSequence {
				return
			}
		}
		if current.ServiceID == record.ServiceID && current.ModuleID == record.ModuleID && current.Updated <= record.Updated {
			s.snapshot.TagHistory[i] = record
		}
		return
	}
	s.snapshot.TagHistory = append(s.snapshot.TagHistory, record)
}
