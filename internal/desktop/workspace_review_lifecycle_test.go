package desktop

import (
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestWorkspaceReviewNewRangeHidesOldCodeBeforeDebounce(t *testing.T) {
	state := &workspaceHistoryState{
		Mode: "review", Open: true, CompareMode: workspaceReviewCommits,
		StartCommit: "a", EndCommit: "a", CommitHashes: []string{"a"},
		Commits:      []domain.WorkspaceGitCommit{{FullHash: "c", ParentHashes: []string{"b"}}, {FullHash: "b", ParentHashes: []string{"a"}}, {FullHash: "a"}},
		Files:        []workspace.CompareChange{{Path: "old.go", Kind: "MODIFIED"}},
		SelectedPath: "old.go", Preview: &workspace.WorkspaceGitFilePreview{},
	}
	s := &app{
		workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
	}
	s.selectWorkspaceReviewCommit(state, "c")
	t.Cleanup(func() { s.cancelWorkspaceReviewDebounce(state) })
	if state.Preview != nil || len(state.Files) != 0 || state.SelectedPath != "" || !state.FilesLoading {
		t.Fatal("old comparison remained visible under the new range during debounce")
	}
	if state.StartCommit != "a" || state.EndCommit != "c" || len(state.CommitHashes) != 3 {
		t.Fatalf("range selection = %s..%s (%v), want A through C", state.StartCommit, state.EndCommit, state.CommitHashes)
	}
}

func TestWorkspaceReviewRemovedCommitCannotStartASelection(t *testing.T) {
	start, end, selected, err := workspaceReviewNextCommitRange([]domain.WorkspaceGitCommit{{FullHash: "current"}}, "", "", "retired", nil)
	if err == nil || start != "" || end != "" || len(selected) != 0 {
		t.Fatalf("retired commit changed selection: %q..%q %v, error=%v", start, end, selected, err)
	}
}

func TestWorkspaceReviewSameSnapshotRefreshRetainsPreviewAndConfiguredBaseline(t *testing.T) {
	preview := &workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "original\n"}}
	change := workspace.CompareChange{Path: "kept.go", Kind: "MODIFIED"}
	state := &workspaceHistoryState{
		TaskID: "task", Path: `C:\synthetic\repo`, Mode: "review", Open: true,
		CompareMode: workspaceReviewUncommitted, BaselineRef: "origin/main",
		BaseCommit: "base", TargetCommit: "worktree:head:tree", FilesLoading: true,
		DiffSessionID: "previous-session",
		Files:         []workspace.CompareChange{change}, SelectedPath: change.Path,
		SelectedKind: domain.WorkspaceGitModified, Preview: preview,
	}
	backend := &workspaceDiffSessionReleaseBackend{actions: make(chan application.Action, 1)}
	s := &app{
		workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
		backend:       backend,
	}
	action := application.Action{Kind: "workspace.diff-files", TaskID: state.TaskID, Path: state.Path,
		Payload: map[string]any{"compare": workspaceReviewComparePayload(state)}}
	s.applyWorkspaceHistoryResult(action, application.ActionResult{Data: workspace.CompareFilesResult{
		SessionID: "refreshed-session",
		Files:     []workspace.CompareChange{change}, BaselineRef: "HEAD", BaseCommit: state.BaseCommit, TargetCommit: state.TargetCommit,
	}})
	if state.Error != "" || state.DiffSessionID != "refreshed-session" {
		t.Fatalf("refresh did not adopt the new session: session=%q, error=%q", state.DiffSessionID, state.Error)
	}
	if state.Preview != preview || state.PreviewLoading || state.FilesLoading || state.SelectedPath != change.Path {
		t.Fatal("same-snapshot refresh discarded or reloaded the current preview")
	}
	if state.BaselineRef != "origin/main" || state.ResultBaselineRef != "HEAD" {
		t.Fatalf("configured baseline=%q, result label=%q; want origin/main and HEAD", state.BaselineRef, state.ResultBaselineRef)
	}
	select {
	case action := <-backend.actions:
		if action.Kind != workspace.CompareSessionCloseAction || action.Payload["sessionId"] != "previous-session" {
			t.Fatalf("unexpected session cleanup: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not release the previous session")
	}
}

func TestWorkspaceReviewFilesWithoutSessionAreRejected(t *testing.T) {
	for _, testCase := range []struct{ name, sessionID string }{
		{name: "empty"},
		{name: "whitespace", sessionID: " \t "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			preview := &workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "original\n"}}
			state := &workspaceHistoryState{
				TaskID: "task", Path: `C:\synthetic\repo`, Mode: "review", Open: true,
				CompareMode: workspaceReviewUncommitted, BaselineRef: "origin/main", ResultBaselineRef: "previous-result",
				DiffSessionID: "previous-session", BaseCommit: "base", TargetCommit: "target", FilesLoading: true,
				Files: []workspace.CompareChange{{Path: "kept.go", Kind: "MODIFIED"}}, SelectedPath: "kept.go",
				SelectedKind: domain.WorkspaceGitModified, Preview: preview,
			}
			s := &app{
				workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
			}
			action := application.Action{Kind: "workspace.diff-files", TaskID: state.TaskID, Path: state.Path,
				Payload: map[string]any{"compare": workspaceReviewComparePayload(state)}}
			s.applyWorkspaceHistoryResult(action, application.ActionResult{Data: workspace.CompareFilesResult{
				SessionID: testCase.sessionID, Files: []workspace.CompareChange{{Path: "new.go", Kind: "ADDED"}},
				BaselineRef: "HEAD", BaseCommit: "new-base", TargetCommit: "new-target",
			}})
			if state.Error == "" || state.FilesLoading || state.DiffSessionID != "" || workspaceReviewSnapshotsReady(state) {
				t.Fatalf("sessionless result was not rejected: session=%q, error=%q, loading=%v", state.DiffSessionID, state.Error, state.FilesLoading)
			}
			if state.BaselineRef != "origin/main" || state.ResultBaselineRef != "previous-result" || state.BaseCommit != "base" || state.TargetCommit != "target" ||
				len(state.Files) != 1 || state.Files[0].Path != "kept.go" || state.SelectedPath != "kept.go" || state.Preview != preview {
				t.Fatal("sessionless result replaced the previous comparison")
			}
		})
	}
}

func TestWorkspaceReviewReadingScopesAndTasksAreIsolated(t *testing.T) {
	state := &workspaceHistoryState{CompareMode: workspaceReviewMainline}
	mainlineKey := workspaceReviewComparisonKey(state)
	state.StartCommit, state.EndCommit, state.SimulateOnMainline = "a", "c", true
	if workspaceReviewComparisonKey(state) != mainlineKey {
		t.Fatal("inactive range options changed the mainline reading key")
	}
	state.IncludeWorkingTree = true
	if workspaceReviewComparisonKey(state) == mainlineKey {
		t.Fatal("working-tree and committed reading scopes overlap")
	}
	s := &app{snapshot: application.Snapshot{Home: `C:\synthetic\home`}}
	item := application.Workspace{Path: `C:\synthetic\repo`, ServiceID: "service", ModuleID: "module"}
	first, repositoryKey, taskKey := s.ensureWorkspaceReviewSession(task{id: "one", path: "one", createdAt: "first"}, item)
	reading := &workspaceHistoryState{Mode: "review", CompareMode: workspaceReviewMainline,
		RepositorySessionKey: repositoryKey, TaskSessionKey: taskKey, SelectedPath: "one.go", SplitRatio: .65}
	s.saveWorkspaceReviewSession(reading)
	second, _, otherTaskKey := s.ensureWorkspaceReviewSession(task{id: "two", path: "two", createdAt: "second"}, item)
	if second != first || second.SplitRatio != .65 || len(second.TaskReadings[otherTaskKey]) != 0 {
		t.Fatal("repository pane ratio or task reading isolation was lost")
	}
	reading.SelectedPath = ""
	reading.FilesLoading = true
	s.saveWorkspaceReviewSelection(reading)
	if first.TaskReadings[taskKey][mainlineKey].Path != "one.go" {
		t.Fatal("leaving during a pending refresh overwrote the previous reading selection")
	}
	s.snapshot.Home = `C:\synthetic\other-home`
	otherHome, _, _ := s.ensureWorkspaceReviewSession(task{id: "one", path: "one", createdAt: "first"}, item)
	if otherHome == first {
		t.Fatal("different application homes share review state")
	}
}

func TestWorkspaceReviewPageRejectsRecreatedTaskAndOtherHome(t *testing.T) {
	item := application.Workspace{Path: `C:\synthetic\repo`, ServiceID: "service", ModuleID: "module"}
	taskItem := task{id: "same-id", path: `C:\synthetic\task`, createdAt: "first", workspaces: []application.Workspace{item}}
	state := &workspaceHistoryState{TaskID: taskItem.id, TaskPath: taskItem.path, TaskCreatedAt: taskItem.createdAt,
		ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path, Mode: "review", Open: true}
	s := &app{
		taskUnit:      taskController{state: taskState{tasks: []task{taskItem}}},
		workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
		page:          "tasks",
		snapshot:      application.Snapshot{Home: `C:\synthetic\home`},
	}
	state.RepositorySessionKey = workspaceReviewRepositoryKey(s.snapshot.Home, item.Path)
	if !s.workspaceReviewPageVisible() {
		t.Fatal("current review is not visible")
	}
	s.taskUnit.state.tasks[0].createdAt = "recreated"
	if s.workspaceReviewPageVisible() {
		t.Fatal("review from a previous task incarnation remained visible")
	}
	s.taskUnit.state.tasks[0].createdAt = "first"
	s.snapshot.Home = `C:\synthetic\other-home`
	if s.workspaceReviewPageVisible() {
		t.Fatal("review from another application home remained visible")
	}
}

func TestWorkspaceReviewTreeAndFlatPreserveFullPathIdentity(t *testing.T) {
	files := []workspace.CompareChange{{Path: "z/Shared.java", Kind: "MODIFIED"}, {Path: "a/b/Shared.java", Kind: "ADDED"}}
	flat := workspaceReviewSortedFiles(files)
	if flat[0].Path != "a/b/Shared.java" || workspaceReviewBaseName(flat[0].Path) != "Shared.java" || workspaceReviewBaseName(flat[1].Path) != "Shared.java" {
		t.Fatalf("flat list = %#v", flat)
	}
	if selected, ok := workspaceReviewSelection(files, files[0].Path, "", domain.WorkspaceGitModified); !ok || selected.Path != files[0].Path {
		t.Fatal("same-basename selection did not preserve relative-path identity")
	}
	tree := workspaceReviewTree(files)
	if len(tree.Directories) != 2 || tree.Directories["a"].Directories["b"].Files[0].Path != "a/b/Shared.java" {
		t.Fatal("changed-file directory tree lost or duplicated a file")
	}
	state := &workspaceHistoryState{ExpandedDirectories: map[string]bool{"a": false}}
	workspaceReviewEnsureExpandedDirectories(state, files)
	if state.ExpandedDirectories["a"] || !state.ExpandedDirectories["a/b"] {
		t.Fatal("refresh reset an explicitly collapsed directory")
	}
}

func TestWorkspaceReviewLeavingReleasesRenderedSource(t *testing.T) {
	for _, leave := range []string{"back", "navigation"} {
		t.Run(leave, func(t *testing.T) {
			state := reviewRendererTestState(false)
			state.Mode, state.Open = "review", true
			s := &app{
				workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
			}
			if s.workspaceNativeDiffView(state, state.Preview.Comparison) == nil {
				t.Fatal("fixture has no native source buffers")
			}
			if leave == "back" {
				s.backWorkspaceReview(state)
			} else {
				s.observeWorkspaceHistoryJobs()
			}
			if state.Open || state.Preview != nil || state.NativeDiff != nil || state.NativeDiffIdentity != nil || state.DiffRows != nil {
				t.Fatal("closed review retained rendered source text or editor buffers")
			}
			if len(state.DiffRendererStates) == 0 {
				t.Fatal("leaving discarded lightweight reading position")
			}
		})
	}
}
