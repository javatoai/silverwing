package desktop

import (
	"time"

	"silverwing-go/internal/production"
)

type productionReleaseReminderKey struct{ home, batchID string }

func productionReleaseCreatedTime(b production.Batch) time.Time {
	if b.InitialSHA == "" || b.ReleaseBranch == "" {
		return time.Time{}
	}
	created, _ := time.Parse(time.RFC3339Nano, b.ReleaseCreatedAt)
	return created
}

func productionReleaseIsOld(b production.Batch, now time.Time) bool {
	created := productionReleaseCreatedTime(b)
	return !b.Ended && !created.IsZero() && now.Sub(created) > 12*time.Hour
}

func (s *app) maybePromptOldProductionRelease(v *productionView, b production.Batch) bool {
	if s.page != "production" || !s.productionAvailable() || v == nil || v.Busy || v.BatchID == "" || v.BatchID != b.ID || !productionReleaseIsOld(b, time.Now()) {
		return false
	}
	state := s.releaseUnit.state.production
	if state == nil || state.views[state.selected] != v || state.diffDialog != nil || state.worktreesDialog != nil || state.historyDialog != nil {
		return false
	}
	key := productionReleaseReminderKey{s.snapshot.Home, b.ID}
	if _, shown := s.releaseUnit.state.productionReminders[key]; shown {
		return false
	}
	selected := s.productionConfirmationCurrent(v)
	current := func() bool {
		if !selected() || v.Busy {
			return false
		}
		active := s.productionCurrentBatch(v)
		return active != nil && !active.Ended && active.ID == b.ID && active.Target == b.Target &&
			active.ReleaseBranch == b.ReleaseBranch && active.ReleaseCreatedAt == b.ReleaseCreatedAt && active.InitialSHA == b.InitialSHA
	}
	endLabel := "结束并开始新发布"
	if !s.productionServiceConfigured(v) {
		endLabel = "结束发布"
	}
	if !current() || !s.showConfirmationDialog("检测到旧发布，是否关闭？", "", "", []confirmationDialogButton{
		{label: endLabel}, {label: "继续发布", primary: true},
	}, 1, 1, func(index int) {
		if !current() {
			return
		}
		if index == 0 {
			s.requestProductionEnd(v, b.ID, "end-start")
		} else {
			s.inspectSelectedProductionRelease(v)
		}
	}) {
		return false
	}
	dialog := s.confirmationDialog
	dialog.Current = current
	dialog.Summary = []confirmationDialogField{
		{Label: "服务", Value: b.Target.ServiceName},
		{Label: "分支", Value: b.ReleaseBranch},
		{Label: "创建时间", Value: productionHistoryCreated(b)},
	}
	dialog.Notices = []string{"结束后归入历史，保留远端分支、Tag 和工作区。"}
	if s.releaseUnit.state.productionReminders == nil {
		s.releaseUnit.state.productionReminders = make(map[productionReleaseReminderKey]struct{})
	}
	s.releaseUnit.state.productionReminders[key] = struct{}{}
	return true
}
