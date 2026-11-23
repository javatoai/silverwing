package desktop

import "silverwing-go/internal/production"

func (s *app) productionCurrentBatch(v *productionView) *production.Batch {
	var latest *production.Batch
	for i := range s.releaseUnit.state.production.batches {
		b := &s.releaseUnit.state.production.batches[i]
		if b.Ended || b.Target.Selection != v.Selection {
			continue
		}
		if latest == nil || newerProductionBatch(*b, *latest) {
			latest = b
		}
	}
	return latest
}

func newerProductionBatch(a, b production.Batch) bool {
	if (a.InitialSHA != "") != (b.InitialSHA != "") {
		return a.InitialSHA != ""
	}
	aTime, bTime := productionBatchOrderTime(a), productionBatchOrderTime(b)
	if aTime.Equal(bTime) {
		return a.ID > b.ID
	}
	return aTime.After(bTime)
}

func (s *app) viewProductionHistory(d *productionHistoryDialog, b production.Batch) {
	if !s.productionHistoryCurrent(d) || d.View.Busy || s.confirmationDialog != nil || !b.Ended || b.Target.Selection != d.View.Selection {
		return
	}
	if current := d.record(b.ID); current != nil && current.Target == b.Target {
		d.SelectedBatchID, d.selectedTarget = b.ID, b.Target
		d.DetailError, d.DetailMessage = "", ""
	}
}

func (d *productionHistoryDialog) record(id string) *production.Batch {
	if id == "" || d.State.deletedBatches[id] {
		return nil
	}
	for i := range d.State.batches {
		b := &d.State.batches[i]
		if b.ID == id && b.Ended && b.Target.Selection == d.Selection {
			return b
		}
	}
	return nil
}

func (s *app) productionHistoryDetail(d *productionHistoryDialog) *production.Batch {
	if !s.productionHistoryCurrent(d) {
		return nil
	}
	if b := d.record(d.SelectedBatchID); b != nil && b.Target == d.selectedTarget {
		return b
	}
	return nil
}
