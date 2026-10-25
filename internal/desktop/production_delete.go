package desktop

import (
	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func (s *app) confirmProductionDelete(v *productionView, b production.Batch) {
	s.confirmProductionDeleteRecord(v, b, nil)
}

func (s *app) confirmProductionDeleteRecord(v *productionView, b production.Batch, history *productionHistoryDialog) {
	if v == nil || v.Busy || !b.Ended || v.Selection != b.Target.Selection || (history == nil && v.BatchID != b.ID) || (history != nil && !s.productionHistoryCurrent(history)) {
		return
	}
	token, err := production.BatchRecordToken(b)
	if err != nil {
		v.ActiveStep, v.Error = 0, "无法确认批次记录："+err.Error()
		return
	}
	previous := s.confirmationDialog
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "删除批次", ConfirmLabel: "删除本地记录",
		Source: firstNonEmpty(b.ReleaseBranch, "初始化批次"), Destination: "删除本地记录",
		Detail:  productionTargetDetail(b.Target) + "\n批次 ID：" + b.ID + "\n创建时间：" + b.CreatedAt + "\n更新时间：" + b.UpdatedAt,
		Notices: []string{"仅删除本地批次记录，保留工作区及其中的修改、远端分支和 Tag。"},
	}, func() {
		s.productionRequestViewFromHistory(v, application.Action{Kind: "production.delete", Payload: map[string]any{"batchId": b.ID, "recordToken": token, "confirmed": true}}, "", history)
	})
	if d := s.confirmationDialog; d != nil && d != previous && d.Title == "删除批次" {
		current := d.Current
		d.Current = func() bool {
			if !current() || v.Busy || (history != nil && !s.productionHistoryCurrent(history)) {
				return false
			}
			for _, record := range s.production.batches {
				if record.ID == b.ID {
					currentToken, err := production.BatchRecordToken(record)
					return record.Ended && err == nil && currentToken == token
				}
			}
			return false
		}
	}
}

func (state *productionUIState) replaceBatches(records []production.Batch) {
	state.batches = nil
	for _, b := range records {
		if !state.deletedBatches[b.ID] {
			state.batches = append(state.batches, b)
		}
	}
}

// A successful removal must not tombstone a different record recreated with
// the same ID while its result was queued for the UI thread.
func (state *productionUIState) productionDeleteRecordUnchanged(action application.Action) bool {
	for _, b := range state.batches {
		if b.ID == action.Payload["batchId"] {
			token, err := production.BatchRecordToken(b)
			return b.Ended && err == nil && token == action.Payload["recordToken"]
		}
	}
	return true
}

// Tombstones are process-local and scoped to loadedHome. They prevent an older
// list/status response from restoring a record after its confirmed deletion.
func (s *app) forgetProductionBatch(state *productionUIState, id string) {
	if state.deletedBatches == nil {
		state.deletedBatches = make(map[string]bool)
	}
	state.deletedBatches[id] = true
	records := state.batches
	state.replaceBatches(records)
	state.mu.Lock()
	if timer := state.timers[id]; timer != nil {
		timer.Stop()
		delete(state.timers, id)
	}
	state.mu.Unlock()
	if state.diffDialog != nil && state.diffDialog.BatchID == id {
		s.closeProductionDiff()
	}
	for _, view := range state.views {
		if view == nil || view.BatchID != id {
			continue
		}
		if view.request != nil && view.request.identity.BatchID == id {
			view.request.cancel()
			view.Busy, view.request = false, nil
		}
		view.Generation++
		s.restoreProductionHistoryInputs(view)
	}
	s.pruneProductionHistoricalChoices(state)
}

func (s *app) pruneProductionHistoricalChoices(state *productionUIState) {
	choices := state.choices[:0]
	for _, choice := range state.choices {
		key := productionKey(production.Selection{GroupID: choice.GroupID, ServiceID: choice.ID})
		retained := !choice.Historical
		if d := state.historyDialog; d != nil && d.Open && productionKey(d.Selection) == key {
			// Keep the empty list visible until it is dismissed, including for
			// a service that was removed from configuration after its release.
			retained = true
		}
		for _, record := range state.batches {
			retained = retained || productionKey(record.Target.Selection) == key
		}
		if retained {
			choices = append(choices, choice)
		} else {
			delete(state.views, key)
			if state.selected == key {
				state.selected = ""
			}
		}
	}
	state.choices = choices
	if state.selected == "" && len(choices) > 0 {
		state.selected = productionKey(production.Selection{GroupID: choices[0].GroupID, ServiceID: choices[0].ID})
	}
}
