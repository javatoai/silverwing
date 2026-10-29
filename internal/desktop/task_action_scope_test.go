package desktop

import (
	"errors"
	"testing"
	"time"

	"silverwing-go/internal/application"
)

func TestTaskMaterialsOpenScopesPreserveSelectionAndRejectStaleResults(t *testing.T) {
	for _, change := range []string{"selection", "materials", "created", "removed", "home", "root", "page", "status"} {
		t.Run(change, func(t *testing.T) {
			s, _ := taskMenuFixture(t)
			target := s.taskUnit.state.tasks[1]
			scope, _ := s.taskMenuScopeFor(target)
			action := application.Action{Kind: "system.open", TaskID: target.id, Path: target.materialsPath,
				Payload: map[string]any{"desktopSystemOpenDirectory": true}}
			if _, err := s.beginSystemOpenJob(action); err == nil {
				t.Fatal("ordinary system-open requests lost their selected-task restriction")
			}
			request, err := s.beginScopedSystemOpenJob(action, &scope)
			if err != nil || request == nil || !s.systemOpenJobRelevant(request) {
				t.Fatalf("non-current materials request rejected: %v", err)
			}
			t.Cleanup(func() { s.completeSystemOpenJob(request) })
			// The job owns its scope, rather than borrowing a caller's pointer.
			scope.taskID = "changed-local-copy"
			switch change {
			case "selection":
				s.taskUnit.state.selected, s.tab = 2, 3
			case "materials":
				s.taskUnit.state.tasks[1].materialsPath += "-changed"
			case "created":
				s.taskUnit.state.tasks[1].createdAt += "-replacement"
			case "removed":
				s.taskUnit.state.tasks = append(s.taskUnit.state.tasks[:1], s.taskUnit.state.tasks[2:]...)
			case "home":
				s.snapshot.Home += "-changed"
			case "root":
				s.settingsUnit.state.configJSON = `{"taskRoot":"D:/changed"}`
			case "page":
				s.page = "settings"
			case "status":
				s.taskUnit.state.tasks[1].materialsStatus = "FAILED"
			}
			valid := change == "selection"
			if s.systemOpenJobRelevant(request) != valid {
				t.Fatal("system-open scope followed selection or retained a stale target")
			}
			s.finishSystemOpenJob(request, application.ActionResult{Message: "opened target"}, nil)
			if (s.systemOpenJobs.message == "opened target") != valid {
				t.Fatal("system-open completion published a stale result")
			}
		})
	}
}

func TestTaskMaterialsScopeCannotOpenOtherResources(t *testing.T) {
	for _, kind := range []string{"system.terminal", "system.ide", "document.open", "system.open"} {
		s, _ := taskMenuFixture(t)
		target := s.taskUnit.state.tasks[1]
		scope, _ := s.taskMenuScopeFor(target)
		action := application.Action{Kind: kind, TaskID: target.id, Path: target.path,
			Payload: map[string]any{"desktopSystemOpenDirectory": true}}
		if _, err := s.beginScopedSystemOpenJob(action, &scope); err == nil {
			t.Fatalf("task materials scope allowed %s on another resource", kind)
		}
	}
}

func TestTaskContextActionsRecheckAfterBackendQueueWait(t *testing.T) {
	for _, kind := range []string{"codex.run", "task.materials-retry", "task.archive", "task.restore"} {
		t.Run(kind, func(t *testing.T) {
			s, backend := taskMenuFixture(t)
			if kind == "task.restore" {
				s.taskUnit.state.tasks[1].archived = true
				s.page = "archive"
			}
			target := s.taskUnit.state.tasks[1]
			scope, _ := s.taskMenuScopeFor(target)
			updates := make(chan func(), 1)
			s.taskUnit.state.taskContextUpdate = func(update func()) { updates <- update }
			s.backendGate <- struct{}{}
			action := application.Action{Kind: kind, TaskID: target.id}
			switch kind {
			case "codex.run", "task.archive", "task.restore":
				action.Path = target.path
				if kind == "task.archive" {
					action.Payload = map[string]any{"cleanup": false}
				}
			}
			s.dispatchTaskContextAction(action, &scope)
			s.releaseBackend()
			var update func()
			select {
			case update = <-updates:
			case <-time.After(time.Second):
				t.Fatal("queued task context was not checked on the UI scheduler")
			}
			// The replacement is visible to the UI projection after the backend
			// slot opens but before its queued scope check runs.
			s.taskUnit.state.tasks[1].createdAt += "-replacement"
			update()
			select {
			case got := <-backend.calls:
				t.Fatalf("stale queued %s action reached backend: %+v", kind, got)
			case <-time.After(30 * time.Millisecond):
			}
			select {
			case s.backendGate <- struct{}{}:
				s.releaseBackend()
			default:
				t.Fatal("stale context check leaked the backend slot")
			}
		})
	}
}

func taskMenuSnapshotForTest(s *app) application.Snapshot {
	snapshot := application.Snapshot{Home: s.snapshot.Home, ConfigJSON: s.settingsUnit.state.configJSON, Groups: s.snapshot.Groups}
	for _, item := range s.taskUnit.state.tasks {
		snapshot.Tasks = append(snapshot.Tasks, application.Task{
			ID: item.id, Title: item.title, Path: item.path, CreatedAt: item.createdAt,
			MaterialsPath: item.materialsPath, RequirementURL: item.requirementURL, RequirementID: item.requirementID,
			MaterialsStatus: item.materialsStatus, MaterialsFailureReason: item.materialsFailureReason,
			GroupID: item.groupID, Archived: item.archived,
		})
	}
	return snapshot
}

func TestLifecycleResultCanReconcileOnlyItsOwnTransition(t *testing.T) {
	for _, kind := range []string{"task.archive", "task.restore", "task.delete"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := taskMenuFixture(t)
			if kind == "task.restore" {
				s.taskUnit.state.tasks[1].archived = true
				s.page = "archive"
			}
			target := s.taskUnit.state.tasks[1]
			scope, valid := s.taskMenuScopeFor(target)
			if !valid {
				t.Fatal("could not capture lifecycle scope")
			}
			action := application.Action{Kind: kind, TaskID: target.id, Path: target.path}
			if kind == "task.archive" {
				s.taskUnit.state.tasks[1].archived = true
			} else if kind == "task.restore" {
				s.taskUnit.state.tasks[1].archived = false
			} else {
				s.taskUnit.state.tasks = append(s.taskUnit.state.tasks[:1], s.taskUnit.state.tasks[2:]...)
			}
			snapshot := taskMenuSnapshotForTest(s)
			if !s.taskLifecycleResultBelongsToContext(action, scope, snapshot) {
				t.Fatal("captured lifecycle transition was not allowed to reconcile")
			}
			if kind == "task.delete" {
				snapshot.Tasks = append(snapshot.Tasks, application.Task{ID: target.id, Path: target.path, CreatedAt: target.createdAt + "-replacement"})
			} else {
				for index := range snapshot.Tasks {
					if snapshot.Tasks[index].ID == target.id {
						snapshot.Tasks[index].CreatedAt += "-replacement"
					}
				}
			}
			if s.taskLifecycleResultBelongsToContext(action, scope, snapshot) {
				t.Fatal("lifecycle result for a replacement task was accepted")
			}
			s.page = "settings"
			if s.taskLifecycleResultBelongsToContext(action, scope, taskMenuSnapshotForTest(s)) {
				t.Fatal("lifecycle result was accepted after the application page changed")
			}
		})
	}
}

func TestScopedMaterialsOpenRechecksBeforeBackendDispatch(t *testing.T) {
	s, backend := taskMenuFixture(t)
	target := s.taskUnit.state.tasks[1]
	scope, _ := s.taskMenuScopeFor(target)
	action := application.Action{Kind: "system.open", TaskID: target.id, Path: target.materialsPath,
		Payload: map[string]any{"desktopSystemOpenDirectory": true}}
	request, err := s.beginScopedSystemOpenJob(action, &scope)
	if err != nil || request == nil {
		t.Fatalf("could not create scoped materials job: %v", err)
	}
	s.taskUnit.state.tasks[1].createdAt += "-replacement"
	if _, err := executeSystemOpenJob(request); !errors.Is(err, errTaskContextChanged) {
		t.Fatalf("stale materials directory reached backend: %v", err)
	}
	select {
	case got := <-backend.calls:
		t.Fatalf("stale materials directory reached backend: %+v", got)
	default:
	}
	s.completeSystemOpenJob(request)
}
