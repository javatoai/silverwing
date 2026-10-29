package desktop

import (
	"testing"

	"silverwing-go/internal/domain"
)

func taskModuleStableValues() taskModuleDraftValues {
	return taskModuleDraftValues{
		ID: "api", Name: "API", LastName: "API", Strategy: domain.WorkspaceStandardWorktree,
		BaseRemote: "origin", LastBaseRemote: "origin", BaseRef: "origin/main", LastBaseRef: "origin/main",
		TargetBranch: "feature/default", LastTargetBranch: "feature/default", Source: domain.TaskModuleConfigured,
	}
}

func TestTaskModuleDraftValuesRemoteAndRefChanges(t *testing.T) {
	for _, test := range []struct {
		name, remote, ref, wantRemote, wantRef string
		changes                                taskModuleDraftChanges
	}{
		{"both prefer remote", " upstream ", "mirror/release/next", " upstream ", "upstream/release/next", taskModuleDraftChanges{BaseRemote: true, BaseRef: true}},
		{"remote derives ref", " upstream ", "origin/main", " upstream ", "upstream/main", taskModuleDraftChanges{BaseRemote: true}},
		{"ref derives remote", "origin", " mirror/release/next ", "mirror", " mirror/release/next ", taskModuleDraftChanges{BaseRef: true}},
		{"blank remote leaves ref", " \t ", "origin/main", " \t ", "origin/main", taskModuleDraftChanges{BaseRemote: true}},
		{"invalid ref leaves remote", "origin", "not a remote branch", "origin", "not a remote branch", taskModuleDraftChanges{BaseRef: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := taskModuleStableValues()
			input.BaseRemote, input.BaseRef = test.remote, test.ref
			got, changes := syncTaskModuleDraftValues(input)
			if got.BaseRemote != test.wantRemote || got.BaseRef != test.wantRef || changes != test.changes {
				t.Fatalf("sync remote=%q ref=%q changes=%+v", got.BaseRemote, got.BaseRef, changes)
			}
			if got.LastBaseRemote != got.BaseRemote || got.LastBaseRef != got.BaseRef || got.TargetEdited {
				t.Fatalf("derived values were not acknowledged or marked the target edited: %+v", got)
			}
			stable, stableChanges := syncTaskModuleDraftValues(got)
			if stable != got || stableChanges.any() {
				t.Fatalf("synchronized values changed again on a stable frame: %+v", stableChanges)
			}
		})
	}
}

func TestTaskModuleDraftValuesRemoteParseFailureKeepsCutFallback(t *testing.T) {
	for _, test := range []struct{ ref, want string }{
		{" old/invalid..branch ", "mirror/invalid..branch"},
		{"old//nested", "mirror//nested"},
		{"/topic", "mirror/topic"},
		{"../release/main", "mirror/release/main"},
		{" old/topic with space ", "mirror/topic with space"},
		{"old/", "old/"},
		{" old/  ", " old/  "},
		{"topic", "topic"},
	} {
		t.Run(test.ref, func(t *testing.T) {
			input := taskModuleStableValues()
			input.BaseRemote, input.BaseRef = " mirror ", test.ref
			got, changes := syncTaskModuleDraftValues(input)
			if got.BaseRemote != " mirror " || got.BaseRef != test.want || changes != (taskModuleDraftChanges{BaseRemote: true, BaseRef: true}) {
				t.Fatalf("parse failure fallback changed: ref=%q remote=%q changes=%+v", got.BaseRef, got.BaseRemote, changes)
			}
		})
	}
}

func TestTaskModuleDraftValuesManualTargetIsSticky(t *testing.T) {
	input := taskModuleStableValues()
	input.Name, input.TargetBranch = " Renamed ", "feature/manual"
	got, changes := syncTaskModuleDraftValues(input)
	if changes != (taskModuleDraftChanges{Name: true, Target: true}) || !got.TargetEdited || got.LastTargetBranch != "feature/manual" || got.LastName != " Renamed " {
		t.Fatalf("manual target was not recorded relative to Last values: %+v changes=%+v", got, changes)
	}
	if retargeted := retargetTaskModuleDraftValues(got, "feature/automatic", 2); retargeted != got {
		t.Fatal("retargeting replaced a manually edited module")
	}
	// Returning to an old default, or clearing the target, remains a real edit.
	for _, target := range []string{"feature/default", ""} {
		got.TargetBranch = target
		got, changes = syncTaskModuleDraftValues(got)
		if !changes.Target || !got.TargetEdited || got.LastTargetBranch != target {
			t.Fatalf("manual edit lost its sticky state: %+v changes=%+v", got, changes)
		}
		if retargeted := retargetTaskModuleDraftValues(got, "feature/automatic", 1); retargeted != got {
			t.Fatal("automatic retargeting replaced the user's target")
		}
	}
}

func TestTaskModuleDraftValuesAutomaticRetargetIsStable(t *testing.T) {
	input := taskModuleStableValues()
	input.Source, input.TagEnabled, input.TagTargetRef = domain.TaskModuleTemporary, false, ""
	input.Name = " Renamed "
	got, changes := syncTaskModuleDraftValues(input)
	if changes != (taskModuleDraftChanges{Name: true}) || got.TargetEdited {
		t.Fatalf("name edit was confused with a target edit: %+v", changes)
	}
	got = retargetTaskModuleDraftValues(got, "feature/task", 2)
	if got.TargetBranch != "feature/task-Renamed" || got.LastTargetBranch != got.TargetBranch || got.Name != " Renamed " || got.TargetEdited {
		t.Fatalf("automatic retarget changed the raw name or Last contract: %+v", got)
	}
	for range 3 {
		next, changes := syncTaskModuleDraftValues(got)
		if next != got || changes.any() {
			t.Fatalf("stable frame produced an edit: %+v", changes)
		}
		if retargeted := retargetTaskModuleDraftValues(next, "feature/task", 2); retargeted != next {
			t.Fatal("stable retargeting changed values")
		}
	}
	if got.Source != domain.TaskModuleTemporary || got.TagEnabled || got.TagTargetRef != "" {
		t.Fatal("value synchronization changed temporary-module metadata")
	}
}

func TestTaskModuleDraftValuesTargetBranchUsesModuleCount(t *testing.T) {
	for _, test := range []struct {
		name, id, feature string
		count             int
		want              string
	}{
		{" API ", "api", " feature/task ", 0, "feature/task"},
		{" API ", "api", " feature/task ", 1, "feature/task"},
		{" API ", "api", " feature/task ", 2, "feature/task-API"},
		{" API ", "api", " feature/task ", 3, "feature/task-API"},
		{"  ", " api ", "feature/task", 2, "feature/task-api"},
		{"", "", "feature/task", 2, "feature/task-default"},
		{"API", "api", " \t ", 2, ""},
	} {
		input := taskModuleStableValues()
		input.Name, input.ID = test.name, test.id
		got := retargetTaskModuleDraftValues(input, test.feature, test.count)
		if got.TargetBranch != test.want || got.LastTargetBranch != test.want || got.Name != test.name || got.TargetEdited {
			t.Fatalf("count=%d feature=%q name=%q: got %+v, want target %q", test.count, test.feature, test.name, got, test.want)
		}
	}
}

func TestTaskModuleDraftValuesConfigurationDefaults(t *testing.T) {
	service := domain.GroupServiceConfig{
		MasterBranch: " origin/main ", TestTagBaselineRef: "origin/release/test",
		Modules: []domain.ServiceModuleConfig{{ID: "api"}, {ID: "worker"}},
	}
	module := domain.ServiceModuleConfig{ID: "api", Name: " API ", TagEnabled: true}
	got := newTaskModuleDraftValues(service, module, " feature/task ")
	if got.ID != "api" || got.Name != " API " || got.LastName != got.Name || got.Strategy != domain.WorkspaceStandardWorktree || got.BaseRemote != "origin" || got.BaseRef != " origin/main " || got.TargetBranch != "feature/task-API" || got.TagMode != domain.TagBuildMergeToTargetBranch || got.TagMessagePrefix != "Tag" || got.Source != domain.TaskModuleConfigured {
		t.Fatalf("configuration defaults changed: %+v", got)
	}
	// EffectiveTagTargetRef sees the original configured mode, as before. A
	// zero mode defaults the draft field but does not invent a configured target.
	if got.TagTargetRef != "" || got.LastBaseRemote != got.BaseRemote || got.LastBaseRef != got.BaseRef || got.LastTargetBranch != got.TargetBranch {
		t.Fatalf("defaults changed the configured tag target or Last values: %+v", got)
	}
	if stable, changes := syncTaskModuleDraftValues(got); stable != got || changes.any() {
		t.Fatal("a new configured draft was unstable on its first frame")
	}
	module.TagMode = domain.TagBuildMergeToTargetBranch
	if got := newTaskModuleDraftValues(service, module, "feature/task"); got.TagTargetRef != service.TestTagBaselineRef {
		t.Fatalf("configured tag baseline was not inherited: %+v", got)
	}
	baseRef, tagRef := "mirror/maintenance", "mirror/testing"
	module.MasterBranch, module.TagTargetRef = &baseRef, &tagRef
	module.Strategy, module.TagMessagePrefix = domain.WorkspaceIndependentClone, " Custom "
	service.Modules = service.Modules[:1]
	got = newTaskModuleDraftValues(service, module, "feature/task")
	if got.BaseRemote != "mirror" || got.BaseRef != baseRef || got.TagTargetRef != tagRef || got.Strategy != domain.WorkspaceIndependentClone || got.TagMessagePrefix != " Custom " || got.TargetBranch != "feature/task" {
		t.Fatalf("module overrides or single-module defaults changed: %+v", got)
	}
	module.TagMode = domain.TagBuildCurrentBranch
	if got := newTaskModuleDraftValues(service, module, ""); got.TagTargetRef != "" || got.TargetBranch != "" || got.TagMode != domain.TagBuildCurrentBranch {
		t.Fatalf("current-branch tag or empty task branch changed: %+v", got)
	}
}
