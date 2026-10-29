package desktop

import "silverwing-go/internal/production"

// Only reading inputs are retained. A prepared Tag is never restored as an
// authorization to publish when returning from an ended batch.
type productionHistoryReturn struct {
	BatchID, ManualTag, Version, Branch, BranchQuery, SuggestedBranch string
	Home, Scope                                                       string
	Preview                                                           *production.Preview
}

func (s *app) productionCurrentBatch(v *productionView) *production.Batch {
	for i := range s.releaseUnit.state.production.batches {
		b := &s.releaseUnit.state.production.batches[i]
		if !b.Ended && b.Target.Selection == v.Selection {
			return b
		}
	}
	return nil
}

func (s *app) viewProductionHistory(d *productionHistoryDialog, b production.Batch) {
	if !s.productionHistoryCurrent(d) || d.View.Busy || s.confirmationDialog != nil || !b.Ended || b.Target.Selection != d.View.Selection {
		return
	}
	v := d.View
	// Resolve the clicked row again rather than navigating a stale row snapshot.
	for _, current := range s.releaseUnit.state.production.batches {
		if current.ID == b.ID && current.Ended && current.Target == b.Target {
			if v.BatchID == b.ID {
				return
			}
			if v.historyReturn == nil {
				batchID := v.BatchID
				if selected := s.productionBatch(v); selected != nil && selected.Ended {
					batchID = ""
					if active := s.productionCurrentBatch(v); active != nil {
						batchID = active.ID
					}
				}
				v.historyReturn = &productionHistoryReturn{BatchID: batchID, ManualTag: v.ManualTag, Version: v.Version, Branch: v.Branch,
					BranchQuery: v.BranchQuery, SuggestedBranch: v.SuggestedBranch, Home: s.snapshot.Home, Scope: s.currentProductionScope(), Preview: v.Preview}
			}
			v.Generation++
			v.selectProductionBatch(b.ID)
			d.Open = false
			return
		}
	}
}

func (s *app) returnFromProductionHistory(v *productionView) {
	if v == nil || v != s.productionView() || v.Busy {
		return
	}
	v.Generation++
	s.restoreProductionHistoryInputs(v)
}

func (s *app) restoreProductionHistoryInputs(v *productionView) {
	inputs := v.historyReturn
	v.historyReturn = nil
	active := s.productionCurrentBatch(v)
	batchID, version := "", ""
	if active != nil {
		batchID, version = active.ID, active.ReleaseBranch
	}
	v.selectProductionBatch(batchID)
	v.Version, v.SuggestedBranch = version, ""
	if inputs == nil || inputs.Home != s.snapshot.Home {
		return
	}
	v.ManualTag, v.Version, v.Branch = inputs.ManualTag, inputs.Version, inputs.Branch
	v.BranchQuery, v.SuggestedBranch = inputs.BranchQuery, inputs.SuggestedBranch
	if inputs.BatchID == batchID && inputs.Scope == s.currentProductionScope() {
		v.Preview = inputs.Preview
	}
	// selectProductionBatch already invalidated preparation and step results.
}
