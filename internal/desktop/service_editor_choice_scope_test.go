package desktop

import (
	"reflect"
	"testing"

	"silverwing-go/internal/application"
)

func TestServiceBranchChoicesIsolateRepositoryRemoteAndDraft(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "isolated-test"})
	s.snapshot.Home = `C:\synthetic\home`
	s.snapshot.Repositories = []application.Repository{{ID: "repo", RootPath: `C:\synthetic\repo`}}
	s.dialogOpen = true
	s.serviceEditor = &serviceEditorState{session: "session", initialText: make(map[string]string), initialBools: make(map[string]bool), service: map[string]any{"repositoryId": "repo"}}
	state := s.serviceEditor
	scope := s.serviceRepositoryChoiceScope()
	s.serviceEditorBranchChoices("origin") // Initialize transient maps.
	state.branchChoices[serviceEditorBranchChoiceKey(scope, "origin")] = []string{"origin/main"}
	state.branchChoicesLoaded[serviceEditorBranchChoiceKey(scope, "origin")] = true
	state.branchChoices[serviceEditorBranchChoiceKey(scope, "github")] = []string{"github/develop"}
	state.branchChoicesLoaded[serviceEditorBranchChoiceKey(scope, "github")] = true
	draft := s.serviceEditorText("masterBranch", "origin/main")
	*draft = "manual/custom-mainline"
	for remote, want := range map[string][]string{"origin": {"origin/main"}, "github": {"github/develop"}} {
		got, loaded, failed := s.serviceEditorBranchChoices(remote)
		if !loaded || failed || !reflect.DeepEqual(got, want) {
			t.Fatalf("remote %s choices = %v, %v, %v", remote, got, loaded, failed)
		}
	}
	state.repositoryScope, state.repositoryScopeSet = scope, true
	s.snapshot.Repositories[0].RootPath = `C:\synthetic\other-repo`
	s.observeServiceRepositoryChoiceScope()
	if got, loaded, _ := s.serviceEditorBranchChoices("origin"); loaded || len(got) != 0 {
		t.Fatal("choices leaked across repository paths")
	}
	if *draft != "manual/custom-mainline" {
		t.Fatal("refreshing choices replaced the hand-entered branch")
	}
}

func TestServiceRepositoryChoiceKeysStayScoped(t *testing.T) {
	base := serviceRepositoryChoiceScope{repositoryID: "repo", rootPath: `C:\synthetic\repo`}
	key := serviceEditorBranchChoiceKey(base, "origin")
	for _, changed := range []string{
		serviceEditorBranchChoiceKey(base, "github"),
		serviceEditorBranchChoiceKey(serviceRepositoryChoiceScope{repositoryID: "other", rootPath: base.rootPath}, "origin"),
		serviceEditorBranchChoiceKey(serviceRepositoryChoiceScope{repositoryID: base.repositoryID, rootPath: `C:\synthetic\other`}, "origin"),
	} {
		if changed == key {
			t.Fatal("different repository or remote shared a candidate key")
		}
	}
}
