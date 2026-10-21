package desktop

import (
	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func (s *app) confirmProductionDelete(v *productionView, b production.Batch) {
	if v == nil || v.Busy || !b.Ended || v.BatchID != b.ID || v.Selection != b.Target.Selection {
		return
	}
	token, err := production.BatchRecordToken(b)
	if err != nil {
		v.ActiveStep, v.Error = 0, "无法确认批次记录："+err.Error()
		return
	}
	previous := s.confirmationDialog
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "删除批次", Message: "确认删除这个已结束批次的本地记录？",
		Source: firstNonEmpty(b.ReleaseBranch, "初始化批次"), Destination: "删除本地记录",
		Detail:  productionTargetDetail(b.Target) + "\n批次 ID：" + b.ID + "\n创建时间：" + b.CreatedAt + "\n更新时间：" + b.UpdatedAt,
		Notices: []string{"仅删除本地批次记录，保留工作区及其中的修改、远端分支和 Tag。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.delete", Payload: map[string]any{"batchId": b.ID, "recordToken": token, "confirmed": true}}, "")
	})
	if d := s.confirmationDialog; d != nil && d != previous && d.Title == "删除批次" {
		d.Buttons[0].label = "删除批次"
		current := d.Current
		d.Current = func() bool {
			if !current() || v.Busy {
				return false
			}
			for _, record := range s.production.batches {
				if record.ID == b.ID {
					return record.Ended && record.Target == b.Target && record.CreatedAt == b.CreatedAt && record.UpdatedAt == b.UpdatedAt
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
		nextID, nextVersion := "", ""
		for _, record := range state.batches {
			if record.Target.Selection == view.Selection && (nextID == "" || !record.Ended) {
				nextID, nextVersion = record.ID, record.ReleaseBranch
				if !record.Ended {
					break
				}
			}
		}
		view.selectProductionBatch(nextID)
		view.Version, view.SuggestedBranch, view.OpenedConflict = nextVersion, "", ""
	}
	choices := state.choices[:0]
	for _, choice := range state.choices {
		key := productionKey(production.Selection{GroupID: choice.GroupID, ServiceID: choice.ID})
		retained := !choice.Historical
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
