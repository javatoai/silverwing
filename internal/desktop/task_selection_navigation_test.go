package desktop

import (
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTaskCreateNavigationAcceptsNarrowResult(t *testing.T) {
	for _, kind := range []string{"legacy-id", "legacy-incarnation", "legacy-unrelated-fields", "manifest", "manifest-pointer"} {
		t.Run(kind, func(t *testing.T) {
			s := taskNavigationFixture()
			target := s.taskUnit.state.tasks[1]
			manifest := domain.TaskManifest{TaskDirectoryName: target.id, CreatedAt: target.createdAt}
			var data any
			switch kind {
			case "legacy-id":
				data = map[string]any{"taskDirectoryName": target.id}
			case "legacy-incarnation":
				data = map[string]any{"taskDirectoryName": target.id, "createdAt": target.createdAt}
			case "legacy-unrelated-fields":
				// A navigation result never required persisted manifest fields or
				// JSON-encodable data unrelated to the two navigation identities.
				data = map[string]any{"taskDirectoryName": target.id, "createdAt": target.createdAt, "schemaVersion": "unrelated", "services": make(chan struct{})}
			case "manifest":
				data = manifest
			case "manifest-pointer":
				data = &manifest
			}
			s.page = "settings"
			if !s.applyTaskSnapshotSelection(application.Action{Kind: "task.create"}, application.ActionResult{Data: data}) {
				t.Fatal("task creation result was not handled")
			}
			if s.taskUnit.currentTask().id != target.id || s.page != "tasks" || s.lastError != "" || s.taskIndexBrowsingFor(false).revealTaskKey != taskNavigationKey(target) {
				t.Fatalf("narrow creation result did not reach navigation: selected=%q page=%q error=%q", s.taskUnit.currentTask().id, s.page, s.lastError)
			}
		})
	}
}

func TestTaskCreateNavigationRejectsReplacementIncarnation(t *testing.T) {
	for _, kind := range []string{"legacy", "manifest", "manifest-pointer"} {
		t.Run(kind, func(t *testing.T) {
			s := taskNavigationFixture()
			target := s.taskUnit.state.tasks[1]
			manifest := domain.TaskManifest{TaskDirectoryName: target.id, CreatedAt: "older-incarnation"}
			var data any = manifest
			if kind == "legacy" {
				data = map[string]any{"taskDirectoryName": target.id, "createdAt": manifest.CreatedAt}
			} else if kind == "manifest-pointer" {
				data = &manifest
			}
			s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty = "draft.md", "retained draft", true
			s.applyTaskSnapshotSelection(application.Action{Kind: "task.create"}, application.ActionResult{Data: data})
			if s.taskUnit.state.selected != 0 || s.materialsUnit.state.documentContent != "retained draft" || !s.materialsUnit.state.documentDirty || s.taskIndexBrowsingFor(false).revealTaskKey != "" || s.lastError != "任务已创建，但刷新后的任务列表中未找到该任务；请刷新后重试。" {
				t.Fatalf("creation result selected a replacement task or lost its draft: selected=%d error=%q", s.taskUnit.state.selected, s.lastError)
			}
		})
	}
}

func TestTaskCreateNavigationMissingIdentityKeepsOriginalError(t *testing.T) {
	var missingManifest *domain.TaskManifest
	for _, data := range []any{nil, missingManifest, map[string]any{"createdAt": "created"}} {
		s := taskNavigationFixture()
		s.page = "settings"
		s.applyTaskSnapshotSelection(application.Action{Kind: "task.create"}, application.ActionResult{Data: data})
		if s.page != "settings" || s.taskUnit.state.selected != 0 || s.lastError != "任务已创建，但返回结果缺少任务目录标识，无法自动选中。" {
			t.Fatalf("missing identity changed navigation or diagnostic: page=%q selected=%d error=%q", s.page, s.taskUnit.state.selected, s.lastError)
		}
	}
}
