package desktop

import (
	"context"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func (s *app) productionWaitingToStart(v *productionView, b *production.Batch) bool {
	if v == nil || v.Selection.ServiceID == "" {
		return false
	}
	if b != nil {
		return b.Ended
	}
	return !v.ReleaseStarted && v.Preview == nil
}

func (s *app) productionGenbuProbeDisabled(serviceID string) bool {
	service := s.readServiceConfig(serviceID)
	return service != nil && !mapBool(service, "genbuProbeEnabled", false)
}

func (s *app) productionServiceConfigured(v *productionView) bool {
	if v == nil || v.Selection.ServiceID == "" || s.productionGenbuProbeDisabled(v.Selection.ServiceID) {
		return false
	}
	for _, choice := range s.ensureProduction().choices {
		if choice.ID == v.Selection.ServiceID {
			return !choice.Historical
		}
	}
	return false
}

func (s *app) startProductionRelease(v *productionView) {
	if s.page != "production" || v == nil || v != s.productionView() || v.Busy ||
		!s.productionServiceConfigured(v) || !s.productionWaitingToStart(v, s.productionBatch(v)) {
		return
	}
	v.resetProductionRelease()
	s.productionInspect("")
	// Keep the initialization controls available so the user can retry a failed check.
	v.ReleaseStarted = v.Busy
}

func (s *app) inspectProductionOnSelection(v *productionView) {
	if s.page != "production" || v == nil || v != s.productionView() {
		return
	}
	state := s.ensureProduction()
	state.entryPending = v.Busy && state.entryRead == nil
	s.productionRequestFor(v, application.Action{Kind: "production.list"}, "enter")
}

func (s *app) finishProductionEntryRead(state *productionUIState, v *productionView, worker *productionWorker, revision uint64, report production.Report, err error, optionalCtx context.Context) {
	if v.request == worker {
		v.Busy, v.request = false, nil
	}
	if s.releaseUnit.state.production != state || state.entryRead != worker {
		return
	}
	state.entryRead = nil
	if s.page != "production" || !s.productionAvailable() {
		return
	}
	if optionalCtx.Err() != nil || worker.identity.Home != s.snapshot.Home || worker.identity.ConfigScope != s.currentProductionScope() || revision != state.batchRevision {
		s.inspectProductionOnSelection(s.productionView())
		return
	}
	if err != nil {
		state.entryError = err.Error()
		return
	}
	state.replaceBatches(report.Batches)
	state.addHistoricalChoices(s.snapshot)
	state.detachEndedProductionBatches()
	current := s.productionView()
	if current != nil && current.Busy {
		state.entryPending = true
		return
	}
	s.applyProductionEntry(current)
}

func (s *app) applyProductionEntry(v *productionView) {
	if s.page != "production" || v == nil || v != s.productionView() || v.Busy {
		return
	}
	b := s.productionCurrentBatch(v)
	if b == nil {
		if v.BatchID != "" {
			v.resetProductionRelease()
		}
		if s.productionWaitingToStart(v, nil) {
			s.startProductionRelease(v)
		} else if s.productionServiceConfigured(v) {
			s.productionInspect("")
		}
		return
	}
	if v.BatchID != b.ID {
		v.resetProductionRelease()
		v.BatchID = b.ID
	}
	if s.productionGenbuProbeDisabled(v.Selection.ServiceID) {
		return
	}
	if !s.maybePromptOldProductionRelease(v, *b) {
		s.inspectSelectedProductionRelease(v)
	}
}

func (s *app) inspectSelectedProductionRelease(v *productionView) {
	if s.page == "production" && v != nil && v == s.productionView() && !v.Busy && v.Preview == nil && s.productionServiceConfigured(v) {
		s.productionInspect("")
	}
}

func productionBatchOrderTime(b production.Batch) time.Time {
	if b.InitialSHA != "" {
		return productionReleaseCreatedTime(b)
	}
	created, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
	return created
}

func (v *productionView) resetProductionRelease() {
	v.selectProductionBatch("")
	v.Version, v.SuggestedBranch, v.BranchQuery = "", "", ""
	v.ReleaseStarted = false
}

func (state *productionUIState) detachEndedProductionBatches() {
	for i := range state.batches {
		b := &state.batches[i]
		if !b.Ended {
			continue
		}
		v := state.views[productionKey(b.Target.Selection)]
		if v == nil || v.Busy || v.BatchID != b.ID {
			continue
		}
		v.resetProductionRelease()
		state.stopProductionProbe(b.ID)
	}
}

func (state *productionUIState) stopProductionProbe(id string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if timer := state.timers[id]; timer != nil {
		timer.Stop()
		delete(state.timers, id)
	}
}
