package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestTaskRecycleVerifiedForcePreservesMixedWorkspaces(t *testing.T) {
	f := newTaskRecycleFixture(t)
	var repositories []domain.RepositoryConfig
	var services []domain.GroupServiceConfig
	var workspaces []domain.ServiceWorkspace
	var remotes []string
	for _, name := range []string{"one", "two"} {
		repository := filepath.Join(f.base, "repo-"+name)
		remote := filepath.Join(f.base, "remote-"+name+".git")
		if err := os.MkdirAll(repository, 0700); err != nil {
			t.Fatal(err)
		}
		gitCompare(t, f.base, "init", "--bare", remote)
		gitCompare(t, repository, "init", "-b", "main")
		gitCompare(t, repository, "config", "user.name", "Recycle Fixture")
		gitCompare(t, repository, "config", "user.email", "recycle@example.invalid")
		writeCompareFile(t, repository, "base.txt", "original\n")
		commitCompare(t, repository, "base")
		gitCompare(t, repository, "remote", "add", "origin", remote)
		gitCompare(t, repository, "push", "-u", "origin", "main")
		repositories = append(repositories, domain.RepositoryConfig{
			ID: "repo-" + name, Name: name, RootPath: repository,
			GitCommonDirectory: filepath.Join(repository, ".git"), OriginURL: &remote,
		})
		services = append(services, domain.GroupServiceConfig{
			ID: name, RepositoryID: "repo-" + name, DisplayName: name, Enabled: true,
			MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
			Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "module", Strategy: domain.WorkspaceStandardWorktree}},
		})
		branch := "feature/force-" + name
		target := filepath.Join(f.task, name)
		gitCompare(t, repository, "worktree", "add", "-b", branch, target, "main")
		workspaces = append(workspaces, domain.ServiceWorkspace{
			RepositoryID: "repo-" + name, ServiceName: name, RepositoryPath: repository,
			WorktreePath: target, DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
			Branch: branch, Health: domain.WorkspaceReady, GroupServiceID: name, ModuleID: "module",
			ModuleName: "module", Strategy: domain.WorkspaceStandardWorktree,
			ModuleSource: domain.TaskModuleConfigured, OriginURL: &remote,
			TagMode: domain.TagBuildMergeToTargetBranch,
		})
		remotes = append(remotes, remote)
	}
	f.config.Repositories = repositories
	f.config.Groups[0].Services = services
	manifest, err := f.service.Manifests.Load(f.task)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Services = workspaces
	if err := f.service.Manifests.Save(f.task, manifest); err != nil {
		t.Fatal(err)
	}
	for _, ws := range manifest.Services {
		writeCompareFile(t, ws.WorktreePath, "base.txt", "dirty-"+ws.ServiceName+"\n")
		writeCompareFile(t, ws.WorktreePath, "untracked.txt", "preserve-"+ws.ServiceName+"\n")
	}
	// The second workspace remains in the task manifest, but its repository
	// configuration was removed, matching the reported deletion blocker.
	f.config.Repositories = repositories[:1]
	preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Force.CanForce || preview.Force.Guard == "" {
		t.Fatalf("force preview rejected a verified worktree: %+v", preview.Force)
	}
	if len(preview.Force.Workspaces) != 2 {
		t.Fatalf("force confirmation must show all existing workspaces: %v", preview.Force.Workspaces)
	}
	if len(preview.Force.Risks) != 2 {
		t.Fatalf("force preview did not capture risk snapshot: %+v", preview.Force.Risks)
	}
	normalBlocked := false
	for _, risk := range preview.Risks {
		if risk.ServiceName == "two" && risk.StatusCheckError != nil {
			normalBlocked = true
		}
	}
	if !normalBlocked {
		t.Fatal("missing repository config must block ordinary deletion")
	}
	remoteSnapshots := make([]string, len(remotes))
	for index, remote := range remotes {
		remoteSnapshots[index] = gitCompareOutput(t, remote, "show-ref")
	}
	if err := f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{ForceGuard: preview.Force.Guard}); err != nil {
		t.Fatal(err)
	}
	preservedRoot := filepath.Join(f.recycler.bin, filepath.Base(f.task))
	for _, ws := range manifest.Services {
		rel, err := filepath.Rel(f.task, ws.WorktreePath)
		if err != nil {
			t.Fatal(err)
		}
		workspaceTrash := filepath.Join(preservedRoot, rel)
		if got, err := os.ReadFile(filepath.Join(workspaceTrash, "base.txt")); err != nil || !strings.HasPrefix(string(got), "dirty-") {
			t.Fatalf("modified content not preserved for %s: %q %v", ws.ServiceName, got, err)
		}
		if got, err := os.ReadFile(filepath.Join(workspaceTrash, "untracked.txt")); err != nil || !strings.HasPrefix(string(got), "preserve-") {
			t.Fatalf("untracked content not preserved for %s: %q %v", ws.ServiceName, got, err)
		}
	}
	for index, repo := range repositories {
		ws := manifest.Services[index]
		if records := gitCompareOutput(t, repo.RootPath, "worktree", "list", "--porcelain"); strings.Contains(records, filepath.ToSlash(ws.WorktreePath)) {
			t.Fatalf("worktree registration remained after recycle: %s", ws.WorktreePath)
		}
		gitCompare(t, repo.RootPath, "show-ref", "--verify", "refs/heads/feature/force-"+ws.ServiceName)
		if got := gitCompareOutput(t, remotes[index], "show-ref"); got != remoteSnapshots[index] {
			t.Fatalf("force recycle changed remote refs for %s", ws.ServiceName)
		}
	}
}

func TestTaskRecycleVerifiedForceRejectsStaleGuardAndUntrackedCheckout(t *testing.T) {
	for _, scenario := range []string{"stale-guard", "untracked-checkout", "changed-origin", "changed-checkout"} {
		t.Run(scenario, func(t *testing.T) {
			f, manifest, _ := newSingleMissingRepositoryForceFixture(t)
			preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
			if err != nil || !preview.Force.CanForce {
				t.Fatalf("force preview: %+v %v", preview.Force, err)
			}
			switch scenario {
			case "stale-guard":
				writeCompareFile(t, manifest.Services[0].WorktreePath, "late.txt", "changed after confirmation\n")
			case "untracked-checkout":
				nested := filepath.Join(f.task, "unexpected-checkout")
				if err := os.MkdirAll(nested, 0700); err != nil {
					t.Fatal(err)
				}
				gitCompare(t, nested, "init", "-b", "main")
			case "changed-checkout":
				original := manifest.Services[0].WorktreePath + ".moved"
				if err := os.Rename(manifest.Services[0].WorktreePath, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(manifest.Services[0].WorktreePath, 0700); err != nil {
					t.Fatal(err)
				}
			case "changed-origin":
				manifest.Services[0].OriginURL = ptr("https://example.invalid/different.git")
				if err := f.service.Manifests.Save(f.task, manifest); err != nil {
					t.Fatal(err)
				}
				preview, err = f.service.InspectDeletion(context.Background(), f.config, f.task)
				if err != nil {
					t.Fatal(err)
				}
				if preview.Force.CanForce || preview.Force.Reason == "" {
					t.Fatalf("changed origin was not rejected: %+v", preview.Force)
				}
				return
			}
			err = f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{ForceGuard: preview.Force.Guard})
			if err == nil || len(f.recycler.calls) != 0 {
				t.Fatalf("stale or unsafe force deleted the task: %v %v", err, f.recycler.calls)
			}
			if _, err := os.Stat(f.task); err != nil {
				t.Fatal("task removed after force revalidation failed")
			}
		})
	}
}

func TestTaskRecycleVerifiedForceRejectsActiveGitOperation(t *testing.T) {
	f, manifest, _ := newSingleMissingRepositoryForceFixture(t)
	admin := strings.TrimSpace(gitCompareOutput(t, manifest.Services[0].WorktreePath, "rev-parse", "--absolute-git-dir"))
	if err := os.WriteFile(filepath.Join(admin, "MERGE_HEAD"), []byte("deadbeef\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Force.CanForce || !strings.Contains(preview.Force.Reason, "进行中的 Git 操作") {
		t.Fatalf("force preview must block an active merge: %+v", preview.Force)
	}
	if err := f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{ForceGuard: preview.Force.Guard}); err == nil {
		t.Fatal("delete accepted an active Git operation")
	}
	if _, err := os.Stat(f.task); err != nil {
		t.Fatal("task removed while a Git operation was active")
	}
	if err := os.Remove(filepath.Join(admin, "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "index.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	preview, err = f.service.InspectDeletion(context.Background(), f.config, f.task)
	if err != nil || preview.Force.CanForce || !strings.Contains(preview.Force.Reason, "index.lock") {
		t.Fatalf("force preview must block a locked index: %+v %v", preview.Force, err)
	}
}

func TestTaskRecycleVerifiedForceRecycleFailureKeepsTaskAndRegistration(t *testing.T) {
	f, manifest, repo := newSingleMissingRepositoryForceFixture(t)
	preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
	if err != nil || !preview.Force.CanForce {
		t.Fatalf("force preview: %+v %v", preview.Force, err)
	}
	f.recycler.fail = f.task
	err = f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{ForceGuard: preview.Force.Guard})
	if err == nil || len(f.recycler.calls) != 0 {
		t.Fatalf("expected task recycle failure without moving the task: %v %v", err, f.recycler.calls)
	}
	if _, err := os.Stat(f.task); err != nil {
		t.Fatal("task disappeared after recycler failure")
	}
	records := gitCompareOutput(t, repo, "worktree", "list", "--porcelain")
	if !strings.Contains(records, filepath.ToSlash(manifest.Services[0].WorktreePath)) {
		t.Fatalf("registration was cleaned despite recycle failure: %s", records)
	}
}

func TestTaskRecycleVerifiedForceAllowsOwnedCloneWithoutFormerSource(t *testing.T) {
	f, manifest, clonePath := newVerifiedForceCloneFixture(t)
	// The independent clone keeps its own .git after the original checkout moves.
	if err := os.Rename(manifest.Services[0].RepositoryPath, filepath.Join(f.base, "moved-source")); err != nil {
		t.Fatal(err)
	}
	preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
	if err != nil || !preview.Force.CanForce {
		t.Fatalf("owned clone force preview: %+v %v", preview.Force, err)
	}
	if err := f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{ForceGuard: preview.Force.Guard}); err != nil {
		t.Fatal(err)
	}
	verifyRecycledClone(t, f, clonePath)
}

func TestTaskRecycleVerifiedForceLegacyAndUnmarkedArchivedClone(t *testing.T) {
	f, manifest, clonePath := newVerifiedForceCloneFixture(t)
	ws := manifest.Services[0]
	f.config.Repositories = []domain.RepositoryConfig{{
		ID: ws.RepositoryID, RootPath: ws.RepositoryPath, GitCommonDirectory: filepath.Join(ws.RepositoryPath, ".git"), OriginURL: ws.OriginURL,
	}}
	marker := cloneMarker(clonePath)
	legacy := filepath.Join(filepath.Dir(marker), "awm-owner")
	if err := os.Rename(marker, legacy); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	current, err := f.service.InspectDeletion(ctx, f.config, f.task)
	if err != nil || current.Force.CanForce {
		t.Fatalf("recovery must be limited to archived tasks: %+v %v", current.Force, err)
	}
	manifest.LifecycleStatus = domain.TaskArchived
	if err := f.service.Manifests.Save(f.task, manifest); err != nil {
		t.Fatal(err)
	}
	withLegacy, err := f.service.InspectDeletion(ctx, f.config, f.task)
	if err != nil || !withLegacy.Force.CanForce || withLegacy.Force.Guard == "" {
		t.Fatalf("verified legacy marker rejected: %+v %v", withLegacy.Force, err)
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	withoutMarker, err := f.service.InspectDeletion(ctx, f.config, f.task)
	if err != nil || !withoutMarker.Force.CanForce || withoutMarker.Force.Guard == withLegacy.Force.Guard {
		t.Fatalf("clean unmarked archived clone must have a separate approval: %+v %v", withoutMarker.Force, err)
	}
	if err := f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{ForceGuard: withLegacy.Force.Guard}); err == nil || len(f.recycler.calls) != 0 {
		t.Fatalf("stale legacy approval was accepted: %v", err)
	}
	dirty := filepath.Join(clonePath, "local.txt")
	if err := os.WriteFile(dirty, []byte("uncommitted"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked, err := f.service.InspectDeletion(ctx, f.config, f.task)
	if err != nil || blocked.Force.CanForce || !strings.Contains(blocked.Force.Reason, "本地改动") {
		t.Fatalf("unmarked dirty clone should be blocked: %+v %v", blocked.Force, err)
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{ForceGuard: withoutMarker.Force.Guard}); err != nil {
		t.Fatal(err)
	}
	verifyRecycledClone(t, f, clonePath)
}

func newVerifiedForceCloneFixture(t *testing.T) (*taskRecycleFixture, domain.TaskManifest, string) {
	t.Helper()
	f := newTaskRecycleFixture(t)
	repository, remote := filepath.Join(f.base, "source-repo"), filepath.Join(f.base, "source-remote.git")
	if err := os.MkdirAll(repository, 0700); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, f.base, "init", "--bare", remote)
	gitCompare(t, repository, "init", "-b", "main")
	gitCompare(t, repository, "config", "user.name", "Recycle Fixture")
	gitCompare(t, repository, "config", "user.email", "recycle@example.invalid")
	writeCompareFile(t, repository, "source.txt", "clone me\n")
	commitCompare(t, repository, "base")
	gitCompare(t, repository, "remote", "add", "origin", remote)
	gitCompare(t, repository, "push", "-u", "origin", "main")
	gitCompare(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	clonePath := filepath.Join(f.task, "owned-clone")
	gitCompare(t, f.base, "clone", remote, clonePath)
	const repositoryID, serviceID, moduleID = "missing-repo", "service", "module"
	if err := os.WriteFile(cloneMarker(clonePath), []byte(cloneOwnership(f.task, repositoryID, serviceID, moduleID)), 0600); err != nil {
		t.Fatal(err)
	}
	f.config.Repositories = nil
	f.config.Groups[0].Services = []domain.GroupServiceConfig{{ID: serviceID, RepositoryID: repositoryID, DisplayName: serviceID, Enabled: true, MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Modules: []domain.ServiceModuleConfig{{ID: moduleID, Name: moduleID, Strategy: domain.WorkspaceIndependentClone}}}}
	manifest, err := f.service.Manifests.Load(f.task)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Services = []domain.ServiceWorkspace{{
		RepositoryID: repositoryID, ServiceName: serviceID, RepositoryPath: repository,
		WorktreePath: clonePath, DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		Branch: "main", Health: domain.WorkspaceReady, GroupServiceID: serviceID,
		ModuleID: moduleID, ModuleName: moduleID, Strategy: domain.WorkspaceIndependentClone,
		ModuleSource: domain.TaskModuleConfigured, OriginURL: &remote,
		TagMode: domain.TagBuildMergeToTargetBranch,
	}}
	if err := f.service.Manifests.Save(f.task, manifest); err != nil {
		t.Fatal(err)
	}
	return f, manifest, clonePath
}

func verifyRecycledClone(t *testing.T, f *taskRecycleFixture, clonePath string) {
	t.Helper()
	cloneTrash := filepath.Join(f.recycler.bin, filepath.Base(f.task), filepath.Base(clonePath))
	if got, err := os.ReadFile(filepath.Join(cloneTrash, "source.txt")); err != nil || string(got) != "clone me\n" {
		t.Fatalf("independent clone content was not preserved: %q %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(cloneTrash, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("independent clone Git metadata was not preserved: %v", err)
	}
	gitCompare(t, cloneTrash, "show-ref", "--verify", "refs/heads/main")
}

func newSingleMissingRepositoryForceFixture(t *testing.T) (*taskRecycleFixture, domain.TaskManifest, string) {
	t.Helper()
	f := newTaskRecycleFixture(t)
	repository, remote := filepath.Join(f.base, "repo-force"), filepath.Join(f.base, "remote-force.git")
	if err := os.MkdirAll(repository, 0700); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, f.base, "init", "--bare", remote)
	gitCompare(t, repository, "init", "-b", "main")
	gitCompare(t, repository, "config", "user.name", "Recycle Fixture")
	gitCompare(t, repository, "config", "user.email", "recycle@example.invalid")
	writeCompareFile(t, repository, "base.txt", "original\n")
	commitCompare(t, repository, "base")
	gitCompare(t, repository, "remote", "add", "origin", remote)
	gitCompare(t, repository, "push", "-u", "origin", "main")
	f.config.Repositories = []domain.RepositoryConfig{{ID: "repo-force", Name: "force", RootPath: repository, GitCommonDirectory: filepath.Join(repository, ".git"), OriginURL: &remote}}
	f.config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "force", RepositoryID: "repo-force", DisplayName: "force", Enabled: true, MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "module", Strategy: domain.WorkspaceStandardWorktree}}}}
	branch, target := "feature/force", filepath.Join(f.task, "force")
	gitCompare(t, repository, "worktree", "add", "-b", branch, target, "main")
	manifest, err := f.service.Manifests.Load(f.task)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "repo-force", ServiceName: "force", RepositoryPath: repository, WorktreePath: target, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Branch: branch, Health: domain.WorkspaceReady, GroupServiceID: "force", ModuleID: "module", ModuleName: "module", Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured, OriginURL: &remote, TagMode: domain.TagBuildMergeToTargetBranch}}
	if err := f.service.Manifests.Save(f.task, manifest); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, target, "dirty.txt", "dirty\n")
	f.config.Repositories = nil
	return f, manifest, repository
}
