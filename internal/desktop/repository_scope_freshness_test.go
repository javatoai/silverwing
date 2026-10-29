package desktop

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func repositoryScopeFreshnessFixture(t *testing.T) (*app, string, string) {
	t.Helper()
	s := newApp(nil, Options{CapturePath: "isolated-repository-scope"})
	oldRoot, newRoot := filepath.Join(t.TempDir(), "old"), filepath.Join(t.TempDir(), "new")
	s.snapshot.Repositories = []application.Repository{{ID: "repo", Name: "Repository", RootPath: oldRoot}}
	s.snapshot.Groups = []application.Group{{ID: "group"}}
	s.serviceCatalog() // Every scope test starts with a retained display catalog.
	return s, oldRoot, newRoot
}

func TestRepositoryCurrentScopeReadsLiveSnapshotWithoutRebuildingDisplayCatalog(t *testing.T) {
	s, oldRoot, newRoot := repositoryScopeFreshnessFixture(t)
	catalog, revision := s.serviceCatalog(), s.workspaceUnit.state.serviceCatalogRevision
	s.snapshot.Repositories[0].RootPath = newRoot
	s.snapshot.Repositories[0].Name = "Current repository"
	s.snapshot.Repositories = append(s.snapshot.Repositories, application.Repository{ID: "repo", Name: "Duplicate", RootPath: oldRoot})
	if name, root := s.currentRepositoryDetails("repo"); name != "Current repository" || root != newRoot {
		t.Fatalf("scope did not read the current first matching repository: %q, %q", name, root)
	}
	if name, root := s.repositoryDetails("repo"); name != "Repository" || root != oldRoot {
		t.Fatal("scope read mutated the retained display snapshot")
	}
	if s.repositoryRootPath("repo") != newRoot || s.serviceCatalog() != catalog || s.workspaceUnit.state.serviceCatalogRevision != revision {
		t.Fatal("live identity lookup rebuilt the display catalog or retained its old root")
	}
	s.snapshot.Repositories = s.snapshot.Repositories[1:]
	if name, root := s.currentRepositoryDetails("repo"); name != "Duplicate" || root != oldRoot {
		t.Fatal("live lookup lost first-match duplicate semantics after replacement")
	}
	s.snapshot.Repositories = nil
	if name, root := s.currentRepositoryDetails("repo"); name != "repo" || root != "" || s.repositoryRootPath("repo") != "" {
		t.Fatal("removed repository retained a current root")
	}
}

func TestServiceRepositoryResultGuardsUseCurrentRootAndKeepManualDraft(t *testing.T) {
	for _, kind := range []string{"repository.choices", "repository.addresses"} {
		t.Run(kind, func(t *testing.T) {
			s, oldRoot, newRoot := repositoryScopeFreshnessFixture(t)
			s.dialogOpen = true
			state := &serviceEditorState{session: "session", initialText: make(map[string]string), initialBools: make(map[string]bool), service: map[string]any{"repositoryId": "repo"}, activeRemote: "origin"}
			s.workspaceUnit.state.serviceEditor = state
			draft := s.serviceEditorText("masterBranch", "origin/main")
			*draft = "manual/custom-mainline"
			scope := s.serviceRepositoryChoiceScope()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			action := application.Action{Kind: kind, Payload: map[string]any{"repositoryId": "repo", "requestKey": "old", "remote": "origin"}}
			request := &serviceRepositoryChoiceJob{action: action, scope: scope, editor: state, remote: "origin", ctx: ctx, cancel: cancel}
			jobs := &serviceRepositoryChoiceJobs{current: map[string]*serviceRepositoryChoiceJob{kind: request}}
			s.workspaceUnit.state.serviceRepositoryChoices = jobs
			state.choicesRequest, state.addressesRequest = "old", "old"
			state.choicesLoading, state.addressesLoading = true, true
			s.snapshot.Repositories[0].RootPath = newRoot
			if s.serviceRepositoryChoiceReadCurrent(kind, "origin") {
				t.Fatal("captured read remained current after its repository root changed")
			}
			oldResult := application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": oldRoot, "remote": "origin", "branches": []string{"origin/old"}, "remotes": []map[string]any{{"remote": "origin", "gitUrl": "old"}}}}
			s.finishServiceRepositoryChoiceRead(jobs, request, oldResult, nil)
			if len(state.branches) != 0 || state.addressesLoaded {
				t.Fatal("old-root queued read wrote into the current editor")
			}
			// The result handlers are also called outside the job wrapper. They
			// must independently reject old data and accept a fresh-root retry.
			action.Payload["requestKey"] = "retry"
			state.choicesRequest, state.addressesRequest = "retry", "retry"
			if kind == "repository.choices" {
				s.applyServiceRepositoryChoices(action, oldResult)
				if state.choicesError == "" || len(state.branches) != 0 {
					t.Fatal("choices result handler accepted the old repository root")
				}
				s.applyServiceRepositoryChoices(action, application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": newRoot, "remote": "origin", "branches": []string{"origin/current"}}})
				got, loaded, failed := s.serviceEditorBranchChoices("origin")
				if !loaded || failed || !slices.Equal(got, []string{"origin/current"}) || state.choicesError != "" {
					t.Fatal("new-root choices retry was not accepted")
				}
			} else {
				s.applyServiceRepositoryAddresses(action, oldResult)
				if state.addressesError == "" || state.addressesLoaded {
					t.Fatal("addresses result handler accepted the old repository root")
				}
				s.applyServiceRepositoryAddresses(action, application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": newRoot, "remotes": []map[string]any{{"remote": "origin", "gitUrl": "current"}}}})
				if !state.addressesLoaded || state.addressesError != "" || len(state.remoteAddresses) != 1 || mapString(state.remoteAddresses[0], "gitUrl") != "current" {
					t.Fatal("new-root addresses retry was not accepted")
				}
			}
			if *draft != "manual/custom-mainline" {
				t.Fatal("repository freshness checks replaced a hand-entered branch")
			}
		})
	}
}

func TestTaskRepositoryCreateAndAppendPickersRejectOldRootResults(t *testing.T) {
	for _, owner := range []string{taskRepositoryChoicesCreateOwner, taskRepositoryChoicesAppendOwner} {
		t.Run(owner, func(t *testing.T) {
			s, oldRoot, newRoot := repositoryScopeFreshnessFixture(t)
			service := domain.GroupServiceConfig{ID: "service", RepositoryID: "repo"}
			group := domain.GroupConfig{ID: "group", Services: []domain.GroupServiceConfig{service}}
			draft := &taskCreationModuleDraft{taskModuleDraftValues: taskModuleDraftValues{BaseRef: "manual/custom", BaseRemote: "origin"}}
			moduleData := map[string]map[string]*taskCreationModuleDraft{"service": {"module": draft}}
			const token, taskID = "picker-token", "task"
			if owner == taskRepositoryChoicesCreateOwner {
				s.dialogOpen, s.form.mode = true, "task-create"
				s.taskUnit.state.taskCreation = &taskCreationState{token: token, phase: "editing", groupID: group.ID, config: domain.AppConfig{Groups: []domain.GroupConfig{group}}, services: map[string]bool{"service": true}, moduleData: moduleData}
			} else {
				item := task{id: taskID, path: "/task", branch: "feature/task", createdAt: "created", updated: "updated"}
				s.taskUnit.replaceTasks([]task{item}, 0)
				state := taskAppendNewState(item, "services", group)
				state.token, state.services[service.ID], state.selectedServices[service.ID], state.moduleData = token, service, true, moduleData
				s.workspaceUnit.state.workspaceAppend = state
			}
			picker := &draft.repositoryPicker
			prepareTaskRepositoryPickerIdentity(picker, "repo", oldRoot)
			picker.ensureMaps()
			picker.branchRequestKeys["origin"], picker.branchesLoading["origin"] = "old", true
			action := application.Action{Kind: "repository.choices", Payload: map[string]any{"pickerOwner": owner, "pickerToken": token, "pickerTaskId": taskID, "serviceId": "service", "moduleId": "module", "repositoryId": "repo", "remote": "origin", "requestKey": "old"}}
			s.snapshot.Repositories[0].RootPath = newRoot
			s.applyTaskRepositoryChoices(action, application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": oldRoot, "remote": "origin", "branches": []string{"origin/old"}}}, nil)
			if picker.branchesError["origin"] == "" || picker.branchesLoaded["origin"] || len(picker.branches["origin"]) != 0 {
				t.Fatal("task repository picker accepted an old-root result")
			}
			prepareTaskRepositoryPickerIdentity(picker, "repo", s.repositoryRootPath("repo"))
			if picker.repositoryRoot != newRoot || len(picker.branchesError) != 0 {
				t.Fatal("picker identity did not reset for the current repository root")
			}
			picker.branchRequestKeys["origin"], picker.branchesLoading["origin"] = "current", true
			action.Payload["requestKey"] = "current"
			s.applyTaskRepositoryChoices(action, application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": newRoot, "remote": "origin", "branches": []string{"origin/current"}}}, nil)
			if !picker.branchesLoaded["origin"] || !slices.Equal(picker.branches["origin"], []string{"origin/current"}) || picker.branchesError["origin"] != "" || draft.BaseRef != "manual/custom" {
				t.Fatal("fresh repository result was rejected or replaced the manual branch draft")
			}
		})
	}
}

func TestTagTargetBranchPickerRejectsChangedRootAndAcceptsCurrentRetry(t *testing.T) {
	s, oldRoot, newRoot := repositoryScopeFreshnessFixture(t)
	s.settingsUnit.state.configJSON = `{"tagEnabled":true,"allowTaskTagTargetEditing":true}`
	w := application.Workspace{ServiceID: "service", ModuleID: "module", Path: "/task/workspace", State: "READY", Record: domain.ServiceWorkspace{RepositoryID: "repo", Branch: "feature/task", TagEnabled: true, TagMode: domain.TagBuildMergeToTargetBranch}}
	item := task{id: "task", path: "/task", createdAt: "created", groupID: "group", workspaces: []application.Workspace{w}}
	s.taskUnit.replaceTasks([]task{item}, 0)
	s.beginTagTargetEdit(item, w)
	state := s.releaseUnit.state.tagTarget
	if state == nil || !s.tagTargetIdentityCurrent(state) {
		t.Fatal("could not open the current Tag target")
	}
	state.Draft = "manual/custom"
	state.Choices = tagTargetBranchPickerState{Open: true, Loading: true, Remote: "origin", RequestKey: "old", RepositoryRoot: oldRoot}
	action := application.Action{Kind: "repository.choices", TaskID: item.id, ServiceID: w.ServiceID, ModuleID: w.ModuleID, Payload: map[string]any{"repositoryId": "repo", "remote": "origin", "requestKey": "old"}}
	s.snapshot.Repositories[0].RootPath = newRoot
	s.applyTagTargetBranchChoices(action, application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": oldRoot, "remote": "origin", "branches": []string{"origin/old"}}}, nil)
	if state.Choices.Error == "" || len(state.Choices.Branches) != 0 {
		t.Fatal("Tag picker accepted a result from its former repository root")
	}
	state.Choices.RepositoryRoot, state.Choices.RequestKey = s.repositoryRootPath("repo"), "current"
	action.Payload["requestKey"] = "current"
	s.applyTagTargetBranchChoices(action, application.ActionResult{Data: map[string]any{"repositoryId": "repo", "rootPath": newRoot, "remote": "origin", "branches": []string{"origin/current"}}}, nil)
	if state.Choices.Error != "" || !slices.Equal(state.Choices.Branches, []string{"origin/current"}) || state.Draft != "manual/custom" {
		t.Fatal("Tag picker rejected a fresh-root retry or replaced its manual draft")
	}
}
