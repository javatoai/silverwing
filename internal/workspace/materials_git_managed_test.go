package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func managedDocumentsTestScope(f *materialsGitFixture, role, branch string) MaterialsGitScope {
	scope := MaterialsGitScope{Home: f.service.Paths.Home, URL: f.remote, Branch: branch, RepositoryRole: role}
	if role == domain.ProjectInstructionsRepositoryRole {
		scope.Root = domain.ManagedProjectInstructionsRoot(scope.Home, scope.URL)
		scope.Subdirectory = domain.ProjectInstructionsDirectory
	} else {
		scope.Root = domain.ManagedTaskMaterialsRoot(scope.Home, domain.MaterialsGitSyncSettings{RepositoryURL: scope.URL})
	}
	return scope
}

func syncManagedDocumentsTest(t *testing.T, f *materialsGitFixture, scope MaterialsGitScope) {
	t.Helper()
	status, err := f.service.Sync(context.Background(), scope, true, nil)
	if err != nil || status.Paused || status.LastSuccess == "" {
		t.Fatalf("sync %s: %+v %v", scope.RepositoryRole, status, err)
	}
}

func TestManagedDocumentsUseIndependentCheckoutsAndScopedPushes(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	materialsGitTestRun(t, f.seed, "checkout", "-b", "project-docs")
	materialsGitTestRun(t, f.seed, "push", "origin", "project-docs")
	materialsGitTestRun(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/project-docs")
	project := managedDocumentsTestScope(f, domain.ProjectInstructionsRepositoryRole, "")
	tasks := managedDocumentsTestScope(f, domain.TaskMaterialsRepositoryRole, "master")
	if project.Root == tasks.Root || filepath.Dir(project.Root) == filepath.Dir(tasks.Root) {
		t.Fatal("two roles share a checkout")
	}
	syncManagedDocumentsTest(t, f, project)
	documentsRoot, err := ValidateManagedDocumentsRoot(project)
	if err != nil || documentsRoot != filepath.Join(project.Root, domain.ProjectInstructionsDirectory) {
		t.Fatalf("project documents root: %q %v", documentsRoot, err)
	}
	entries, err := os.ReadDir(documentsRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a new project directory should start empty: %v %v", entries, err)
	}
	if branch := materialsGitTestRun(t, project.Root, "branch", "--show-current"); branch != "project-docs" {
		t.Fatalf("project did not follow remote default: %q", branch)
	}
	const projectDocument = domain.ProjectInstructionsDirectory + "/指令.md"
	materialsGitTestFile(t, project.Root, projectDocument, "project")
	materialsGitTestFile(t, project.Root, "outside-private.md", "leave local")
	syncManagedDocumentsTest(t, f, project)
	if got := materialsGitTestRun(t, f.remote, "show", "project-docs:"+projectDocument); got != "project" {
		t.Fatalf("remote project document: %q", got)
	}
	if got := materialsGitTestRun(t, project.Root, "status", "--short"); got != "?? outside-private.md" {
		t.Fatalf("scope touched outside working file: %q", got)
	}
	const taskDocument = "requirements/2026/123-需求/研发/created-before-clone.md"
	materialsGitTestFile(t, tasks.Root, taskDocument, "task")
	materialsGitTestFile(t, tasks.Root, domain.ProjectInstructionsDirectory+"/other-role.md", "leave local")
	syncManagedDocumentsTest(t, f, tasks)
	if got := materialsGitTestRun(t, f.remote, "show", "master:"+taskDocument); got != "task" {
		t.Fatalf("remote task document: %q", got)
	}
	if got := materialsGitTestRun(t, tasks.Root, "status", "--short"); got != "?? "+domain.ProjectInstructionsDirectory+"/" {
		t.Fatalf("task checkout changed outside working file: %q", got)
	}
	if err := os.Remove(filepath.Join(project.Root, filepath.FromSlash(projectDocument))); err != nil {
		t.Fatal(err)
	}
	syncManagedDocumentsTest(t, f, project)
	if got := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "project-docs", "--", domain.ProjectInstructionsDirectory); got != "" {
		t.Fatalf("deletion did not reach the default branch: %q", got)
	}
	oldHome := f.service.Paths.Home
	movedHome := filepath.Join(filepath.Dir(oldHome), "moved-home")
	if err := os.Rename(oldHome, movedHome); err != nil {
		t.Fatal(err)
	}
	f.service.Paths = storage.NewPaths(movedHome)
	project = managedDocumentsTestScope(f, domain.ProjectInstructionsRepositoryRole, "")
	status, err := f.service.Status(project)
	if err != nil || status.LastSuccess == "" {
		t.Fatalf("sync state did not move with the profile: %+v %v", status, err)
	}
	if content, err := os.ReadFile(filepath.Join(project.Root, "outside-private.md")); err != nil || string(content) != "leave local" {
		t.Fatalf("working checkout did not move with the profile: %q %v", content, err)
	}
}

func TestManagedProjectInstructionsSyncUsesConfiguredBranch(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	const branch = "docs/instructions"
	const path = domain.ProjectInstructionsDirectory + "/guide.md"
	materialsGitTestRun(t, f.seed, "checkout", "-b", branch)
	materialsGitTestRun(t, f.seed, "push", "origin", branch)
	scope := managedDocumentsTestScope(f, domain.ProjectInstructionsRepositoryRole, branch)
	syncManagedDocumentsTest(t, f, scope)
	if _, err := f.service.ProjectInstructionsDocumentsRoot(scope, false); err != nil {
		t.Fatalf("successful sync did not bind the configured checkout: %v", err)
	}
	if got := materialsGitTestRun(t, scope.Root, "branch", "--show-current"); got != branch {
		t.Fatalf("project checkout = %q, want %q", got, branch)
	}
	materialsGitTestFile(t, scope.Root, path, "instructions")
	syncManagedDocumentsTest(t, f, scope)
	if got := materialsGitTestRun(t, f.remote, "show", branch+":"+path); got != "instructions" {
		t.Fatalf("configured branch did not receive the project document: %q", got)
	}
	if got := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "master", "--", path); got != "" {
		t.Fatalf("sync wrote to the remote default branch: %q", got)
	}
	scope.Branch = ""
	if _, err := f.service.ProjectInstructionsDocumentsRoot(scope, true); err == nil {
		t.Fatal("selecting remote default immediately exposed the previous explicit checkout")
	}
	syncManagedDocumentsTest(t, f, scope)
	if got := materialsGitTestRun(t, scope.Root, "branch", "--show-current"); got != "master" {
		t.Fatalf("clearing the selected branch did not restore the remote default: %q", got)
	}
	if _, err := f.service.ProjectInstructionsDocumentsRoot(scope, false); err != nil {
		t.Fatalf("successful sync did not bind the remote default checkout: %v", err)
	}
}

func TestManagedDocumentsBlockStagedAndUnpushedPathsOutsideScope(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	scope := managedDocumentsTestScope(f, domain.ProjectInstructionsRepositoryRole, "")
	syncManagedDocumentsTest(t, f, scope)
	oldHead := materialsGitTestRun(t, f.remote, "rev-parse", "HEAD")
	materialsGitTestFile(t, scope.Root, "other.md", "outside")
	materialsGitTestFile(t, scope.Root, domain.ProjectInstructionsDirectory+"/local.md", "within scope")
	materialsGitTestRun(t, scope.Root, "add", "other.md")
	for _, test := range []string{"staged", "committed"} {
		t.Run(test, func(t *testing.T) {
			if test == "committed" {
				materialsGitTestRun(t, scope.Root, "commit", "-m", "outside scope")
			}
			status, err := f.service.Sync(context.Background(), scope, true, nil)
			if err == nil || !status.Paused || !strings.Contains(err.Error(), "other.md") {
				t.Fatalf("outside path escaped guard: %+v %v", status, err)
			}
			if got := materialsGitTestRun(t, f.remote, "rev-parse", "HEAD"); got != oldHead {
				t.Fatalf("remote changed despite invalid scope: %q", got)
			}
			if got, err := os.ReadFile(filepath.Join(scope.Root, "other.md")); err != nil || string(got) != "outside" {
				t.Fatalf("outside working file not preserved: %q %v", got, err)
			}
		})
	}
}

func TestManagedDocumentsRetryPushAfterDisjointRemoteChange(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	project := managedDocumentsTestScope(f, domain.ProjectInstructionsRepositoryRole, "")
	tasks := managedDocumentsTestScope(f, domain.TaskMaterialsRepositoryRole, "master")
	syncManagedDocumentsTest(t, f, project)
	syncManagedDocumentsTest(t, f, tasks)
	const projectDocument = domain.ProjectInstructionsDirectory + "/independent.md"
	materialsGitTestFile(t, project.Root, projectDocument, "project")
	const taskDocument = "requirements/2026/123-需求/研发/disjoint.md"
	materialsGitTestFile(t, tasks.Root, taskDocument, "task")
	status, err := f.service.Sync(context.Background(), project, true, func(phase string) {
		if phase == "推送资料" {
			syncManagedDocumentsTest(t, f, tasks)
		}
	})
	if err == nil || status.Paused {
		t.Fatalf("ordinary concurrent push should retry: %+v %v", status, err)
	}
	syncManagedDocumentsTest(t, f, project)
	for path, want := range map[string]string{projectDocument: "project", taskDocument: "task"} {
		if got := materialsGitTestRun(t, f.remote, "show", "HEAD:"+path); got != want {
			t.Errorf("remote content %s: %q", path, got)
		}
	}
}

func TestManagedDocumentsKeepLocalFileOnConflict(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	const relative = domain.ProjectInstructionsDirectory + "/shared.md"
	materialsGitTestFile(t, f.seed, relative, "baseline\n")
	materialsGitTestRun(t, f.seed, "add", relative)
	materialsGitTestRun(t, f.seed, "commit", "-m", "prepare project doc")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	scope := managedDocumentsTestScope(f, domain.ProjectInstructionsRepositoryRole, "")
	syncManagedDocumentsTest(t, f, scope)
	materialsGitTestFile(t, scope.Root, relative, "local edits\n")
	materialsGitTestFile(t, f.seed, relative, "remote edits\n")
	materialsGitTestRun(t, f.seed, "add", relative)
	materialsGitTestRun(t, f.seed, "commit", "-m", "change remote doc")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	status, err := f.service.Sync(context.Background(), scope, true, nil)
	if err == nil || !status.Paused || !strings.Contains(err.Error(), "本地修改已保留") {
		t.Fatalf("conflict did not pause: %+v %v", status, err)
	}
	if local, err := os.ReadFile(filepath.Join(scope.Root, filepath.FromSlash(relative))); err != nil || string(local) != "local edits\n" {
		t.Fatalf("conflict overwrote local file: %q %v", local, err)
	}
	if got := materialsGitTestRun(t, f.remote, "show", "HEAD:"+relative); got != "remote edits" {
		t.Fatalf("conflict overwrote remote file: %q", got)
	}
}
