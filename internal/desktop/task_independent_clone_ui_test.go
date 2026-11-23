package desktop

import (
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestIndependentCloneCreationUsesOnlySelectedRemoteBranch(t *testing.T) {
	s, tester := taskCreationCompactFixture(t, 1100, false, 2)
	form := s.taskUnit.state.taskCreation
	if !tester.HasText("任务分支输入") || !tester.HasText("模块目标分支 · service:api") {
		t.Fatal("mixed selection lost its Worktree branch fields")
	}
	s.removeTaskCreationModule("service", "api")
	clone := form.moduleData["service"]["worker"]
	clone.TargetBranch, clone.LastTargetBranch, clone.TargetEdited = "feature/old-worktree", "feature/old-worktree", true
	form.draft.Branch, form.lastBranch = "", ""
	tester.Frame()
	if tester.HasText("任务分支输入") || tester.HasText("模块目标分支 · service:worker") || !tester.HasText("克隆分支选择器") {
		t.Fatal("clone-only form should render just the remote branch selector")
	}
	if err := tester.Click("克隆分支选择器"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("origin/develop"); err != nil {
		t.Fatal(err)
	}
	if message := s.taskCreationValidationError(); message != "" {
		t.Fatal(message)
	}
	payload, err := s.taskCreationPayload()
	if err != nil {
		t.Fatal(err)
	}
	modules := payload["serviceSelections"].([]workspace.TaskServiceSelection)[0].Modules
	if payload["featureBranch"] != "" || len(modules) != 1 || modules[0].BaseRef != "origin/develop" || modules[0].TargetBranch != "" {
		t.Fatalf("unexpected clone-only request: %#v", payload)
	}
}

func TestIndependentCloneAppendUsesOnlySelectedRemoteBranch(t *testing.T) {
	for _, mode := range []string{"services", "modules"} {
		t.Run(mode, func(t *testing.T) {
			s, tester := appendServiceToggleFixture(t, 1100, false)
			s.taskUnit.state.tasks[0].branch = ""
			serviceID := "alpha"
			if mode == "modules" {
				serviceID = "existing"
				s.openTaskAppendModules(s.taskUnit.currentTask(), serviceID)
			} else {
				s.openTaskAppendServices(s.taskUnit.currentTask())
				s.toggleTaskAppendService(serviceID)
			}
			state := s.workspaceUnit.state.workspaceAppend
			t.Cleanup(func() { cancelTaskAppendRepositoryChoices(state) })
			draft := state.moduleData[serviceID]["api"]
			picker := &draft.repositoryPicker
			prepareTaskRepositoryPickerIdentity(picker, "repo-"+serviceID, "")
			picker.ensureMaps()
			picker.remotes, picker.remotesLoaded = []string{"origin"}, true
			picker.branches["origin"] = []string{"origin/main", "origin/develop"}
			picker.branchesLoaded["origin"], picker.branchesLoadedAt["origin"] = true, time.Now()
			tester.Frame()
			if !tester.HasText("请输入目标分支") {
				t.Fatal("a Worktree appended to a task without a branch preset needs a per-module target input")
			}
			draft.TargetBranch, draft.LastTargetBranch, draft.TargetEdited = "feature/preserved", "feature/preserved", true
			if err := tester.Click("独立克隆"); err != nil {
				t.Fatal(err)
			}
			if !tester.HasText("克隆分支选择器") || tester.HasText("请输入目标分支") {
				t.Fatal("append form still renders a second branch input for clone")
			}
			if err := tester.Click("克隆分支选择器"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("origin/develop"); err != nil {
				t.Fatal(err)
			}
			payload, _, err := s.taskAppendRequest()
			if err != nil {
				t.Fatal(err)
			}
			var modules []workspace.TaskModuleSelection
			if mode == "services" {
				modules = payload["serviceSelections"].([]workspace.TaskServiceSelection)[0].Modules
			} else {
				modules = payload["modules"].([]workspace.TaskModuleSelection)
			}
			if len(modules) != 1 || modules[0].Strategy != domain.WorkspaceIndependentClone || modules[0].BaseRef != "origin/develop" || modules[0].TargetBranch != "" {
				t.Fatalf("unexpected appended clone request: %#v", payload)
			}
			if err := tester.Click("Worktree"); err != nil {
				t.Fatal(err)
			}
			if draft.TargetBranch != "feature/preserved" || !tester.HasText("请输入目标分支") {
				t.Fatal("strategy switch lost the Worktree target draft")
			}
		})
	}
}
