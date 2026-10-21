package desktop

import (
	"fmt"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func tagJobGroupRecord(id, group string) application.TagRecord {
	return application.TagRecord{ID: id, TaskID: "task-a", BatchID: group, ServiceID: "service-" + id, ModuleID: "module", ServiceName: "fixture-service-" + id,
		Tag: "fixture-tag-" + id, State: "SUCCESS", SourceBranch: "feature/fixture", TargetBranch: "release/test", CreatedAt: "2026-10-15 12:00:00", Updated: "2026-10-15 12:01:00",
		GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
}

func tagJobGroupItem(id, kind string, record application.TagRecord) application.TagJobItem {
	return application.TagJobItem{ID: id, Kind: kind, OperationID: record.ID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Record: &record, Phase: application.TagJobPhaseQueued, Busy: true, Queued: true}
}

func TestTagJobHistoryGroupsAndFocus(t *testing.T) {
	for _, kind := range []string{"tag.retry", "tag.workspace-check"} {
		for _, width := range []int{360, 1100} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d-dark-%t", kind, width, dark), func(t *testing.T) {
					s, _, _ := tagDesktopTestApp()
					for _, record := range []application.TagRecord{tagJobGroupRecord("a0", "history-a"), tagJobGroupRecord("a1", "history-a"), tagJobGroupRecord("a2", "history-a"), tagJobGroupRecord("b0", "history-b"), tagJobGroupRecord("b1", "history-b")} {
						s.snapshot.TagHistory = append(s.snapshot.TagHistory, record)
					}
					scope := s.tagJobScope()
					view := s.tagJobView(scope)
					snapshot := application.TagJobSnapshot{Scope: scope, ID: "current-job", BatchID: "new-build-group", Sequence: 1, Busy: true, Items: []application.TagJobItem{tagJobGroupItem("first-action", kind, s.snapshot.TagHistory[1])}}
					if !s.acceptTagJobSnapshot(view, scope, snapshot, true) {
						t.Fatal("initial job snapshot rejected")
					}
					if progress := s.tagBatchProgress("history-a"); progress.Total != 1 || progress.Running != 1 {
						t.Fatalf("single operation confused with 3 historical records: %+v", progress)
					}
					if s.tagBatchProgress(snapshot.BatchID).Total != 0 || !s.tagBatchFocused("history-a") || s.consumeTagBatchFocus(snapshot.BatchID) {
						t.Fatal("retry/check focused the job batch instead of the historical group")
					}
					tester := tagLayoutTester(s, width, dark)
					if view.focusBatch != "" || s.consumeTagBatchFocus("history-a") {
						t.Fatal("native group rendering did not consume focus exactly once")
					}
					if !tester.HasText("本次操作 1 项 · 执行中 1 · 完成 0 · 失败 0") || !tester.HasText("等待执行") || !tester.HasText("fixture-service-a0") || !tester.HasText("fixture-service-a2") {
						t.Fatal("UI lost original rows or current operation summary")
					}
					// The same live job can check a different existing group.
					snapshot.Sequence++
					check := tagJobGroupItem("second-action", "tag.workspace-check", s.snapshot.TagHistory[4])
					check.Phase = application.TagJobPhaseChecking
					check.Queued = false
					snapshot.Items = append(snapshot.Items, check)
					s.acceptTagJobSnapshot(view, scope, snapshot, false)
					if !s.tagBatchFocused("history-b") || view.focusBatch != "history-b" {
						t.Fatal("appended historical check did not focus its own group")
					}
					tester.Frame()
					if view.focusBatch != "" || !tester.HasText("检查工作区") || s.tagBatchProgress("history-b").Total != 1 {
						t.Fatal("check was omitted from group progress or native focus")
					}
					// A new build joins this job using the job's new batch identity.
					snapshot.Sequence++
					buildRecord := tagJobGroupRecord("new", snapshot.BatchID)
					buildRecord.State = "CREATED"
					buildRecord.Tag = ""
					snapshot.Items = append(snapshot.Items, tagJobGroupItem("third-action", "tag.build", buildRecord))
					s.acceptTagJobSnapshot(view, scope, snapshot, false)
					if !s.tagBatchFocused("new-build-group") || view.focusBatch != "new-build-group" {
						t.Fatal("appended build did not focus its new history group")
					}
					tester.Frame()
					for _, group := range []string{"history-a", "history-b", "new-build-group"} {
						if s.tagBatchProgress(group).Total != 1 {
							t.Fatalf("group %s counted another group's item", group)
						}
					}
					if view.focusBatch != "" || len(s.snapshot.TagHistory) != 6 {
						t.Fatal("append lost rows or did not consume native focus")
					}
					// Ordinary phase updates update each group's counts, never focus.
					snapshot.Sequence++
					snapshot.Items = append([]application.TagJobItem(nil), snapshot.Items...)
					snapshot.Items[0].Phase = application.TagJobPhaseCompleted
					snapshot.Items[0].Done = true
					snapshot.Items[0].Busy = false
					snapshot.Items[0].Queued = false
					snapshot.Items[1].Phase = application.TagJobPhaseFailed
					snapshot.Items[1].Done = true
					snapshot.Items[1].Busy = false
					snapshot.Items[2].Phase = application.TagJobPhaseMerging
					snapshot.Items[2].Queued = false
					s.acceptTagJobSnapshot(view, scope, snapshot, false)
					tester.Frame()
					if view.focusBatch != "" || s.tagBatchProgress("history-a").Completed != 1 || s.tagBatchProgress("history-b").Failed != 1 || s.tagBatchProgress("new-build-group").Running != 1 {
						t.Fatal("phase counts crossed group ownership or refocused")
					}
					for _, text := range []string{"本次操作 1 项 · 执行中 0 · 完成 1 · 失败 0", "本次操作 1 项 · 执行中 0 · 完成 0 · 失败 1", "合并到目标分支"} {
						if !tester.HasText(text) {
							t.Fatalf("native group progress missing %q", text)
						}
					}
					if tester.HasText("已完成") {
						t.Fatal("completed operation retained a redundant phase caption")
					}
					captureWorkspaceRefresh(t, tester, fmt.Sprintf("tag-completed-%s-%d-dark-%t", kind, width, dark))
					for _, record := range s.snapshot.TagHistory {
						if record.ID != "new" {
							want := "history-a"
							if record.ID[0] == 'b' {
								want = "history-b"
							}
							if record.BatchID != want {
								t.Fatalf("historical record %s moved into %s", record.ID, record.BatchID)
							}
						}
					}
					if s.tagRecordPhase(s.snapshot.TagHistory[0]) != "" {
						t.Fatal("untouched historical row received another item's phase")
					}
				})
			}
		}
	}
}

func TestTagJobGroupFallbackAndOperationCounts(t *testing.T) {
	s, _, _ := tagDesktopTestApp()
	scope := s.tagJobScope()
	view := s.tagJobView(scope)
	record := tagJobGroupRecord("legacy", "")
	snapshot := application.TagJobSnapshot{Scope: scope, ID: "job", BatchID: "new-group", Sequence: 1, Busy: true, Items: []application.TagJobItem{tagJobGroupItem("retry", "tag.retry", record), tagJobGroupItem("check", "tag.workspace-check", record), {ID: "new-build", Kind: "tag.build", Phase: application.TagJobPhaseQueued, Queued: true}}}
	s.acceptTagJobSnapshot(view, scope, snapshot, true)
	if s.tagBatchProgress("legacy").Total != 2 || s.tagBatchProgress("new-group").Total != 1 {
		t.Fatal("current operation items were deduplicated or a recordless build lost its group")
	}
	if !s.consumeTagBatchFocus("new-group") || s.consumeTagBatchFocus("new-group") {
		t.Fatal("recordless build focus was not one-shot")
	}
	if snapshot.Items[0].Record.BatchID != "" || snapshot.Items[1].Record.BatchID != "" {
		t.Fatal("legacy single history group was reassigned")
	}
}
