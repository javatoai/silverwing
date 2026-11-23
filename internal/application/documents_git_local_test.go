package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func localDocumentsGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
	return strings.TrimSpace(string(output))
}

func writeLocalDocumentsFixture(t *testing.T, root, path, body string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBindSharedDocumentsCheckoutWithDifferentHTTPSchemes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(base, "missing-config"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	const httpURL = "http://example.invalid/team/docs.git"
	const httpsURL = "https://example.invalid/team/docs.git"
	shared := filepath.Join(base, "shared")
	localDocumentsGit(t, base, "init", "--initial-branch=main", shared)
	localDocumentsGit(t, shared, "-c", "user.name=SW Test", "-c", "user.email=sw@example.invalid", "commit", "--allow-empty", "--no-gpg-sign", "-m", "seed")
	localDocumentsGit(t, shared, "remote", "add", "origin", httpURL)
	separate := filepath.Join(base, "separate")
	localDocumentsGit(t, base, "clone", shared, separate)
	localDocumentsGit(t, separate, "remote", "set-url", "origin", httpURL)

	for _, scenario := range []struct {
		name, savedProjectURL, selected string
		wantSuccess                     bool
	}{
		{"shared", httpsURL, shared, true},
		{"different-remote-path", "https://example.invalid/team/other.git", shared, false},
		{"separate-checkout", httpsURL, separate, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			app, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			config, err := app.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
				config.ProjectInstructionsGitURL = scenario.savedProjectURL
				config.ProjectInstructionsGitBranch = "main"
				return config, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			bind := func(role, path string, scope workspace.MaterialsGitScope) error {
				_, err := app.Dispatch(ctx, Action{Kind: "config.documents-git.bind", Path: path, Payload: map[string]any{"role": role, "scope": scope}})
				return err
			}
			if err := bind(domain.TaskMaterialsRepositoryRole, shared, MaterialsGitScope(app.Paths.Home, config)); err != nil {
				t.Fatalf("bind task checkout: %v", err)
			}
			project := ProjectInstructionsGitScope(app.Paths.Home, config)
			bindErr := bind(domain.ProjectInstructionsRepositoryRole, scenario.selected, project)
			config, err = app.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			updated := ProjectInstructionsGitScope(app.Paths.Home, config)
			if !scenario.wantSuccess {
				if bindErr == nil || !strings.Contains(bindErr.Error(), "请先解绑") || updated != project {
					t.Fatalf("expected to keep the original project binding: %+v %v", updated, bindErr)
				}
				return
			}
			if bindErr != nil || updated.URL != httpURL || !workspace.SameDocumentsCheckout(updated.Root, shared) || updated.BindingID == "" {
				t.Fatalf("project did not adopt the verified shared checkout: %+v %v", updated, bindErr)
			}
		})
	}
}

func TestLocalDocumentsBindingsAndRealScopedSync(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	base := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(base, "missing-config"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	const remoteName = "documents"
	const projectFile = domain.ProjectInstructionsDirectory + "/guide.md"
	const taskFile = "requirements/2026/123-test/dev/new.md"
	const original = "# Original instruction\n"
	remote := filepath.Join(base, "documents.git")
	other := filepath.Join(base, "other.git")
	for _, path := range []string{remote, other} {
		localDocumentsGit(t, base, "init", "--bare", "--initial-branch=main", path)
	}
	clone := func(name, address string) string {
		t.Helper()
		root := filepath.Join(base, name)
		localDocumentsGit(t, base, "clone", "-o", remoteName, address, root)
		localDocumentsGit(t, root, "config", "user.name", "SW Test")
		localDocumentsGit(t, root, "config", "user.email", "sw@example.invalid")
		return root
	}
	seed := clone("seed", remote)
	writeLocalDocumentsFixture(t, seed, projectFile, original)
	writeLocalDocumentsFixture(t, seed, "requirements/2026/123-test/dev/existing.md", "existing")
	localDocumentsGit(t, seed, "add", ".")
	localDocumentsGit(t, seed, "commit", "-m", "seed")
	localDocumentsGit(t, seed, "push", remoteName, "main")
	localDocumentsGit(t, seed, "push", other, "main")
	localDocumentsGit(t, seed, "push", remoteName, "main:refs/heads/docs/test")

	a, err := New(filepath.Join(base, "home"))
	if err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(base, "tasks")
	_, err = a.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		subdir := "dev"
		config.TaskRoot = &taskRoot
		config.RequirementMaterialsSubdirectory = &subdir
		return config, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(a.Paths.Home, "materials")
	const existingTaskFile = "requirements/2026/123-test/dev/existing.md"
	writeLocalDocumentsFixture(t, legacy, existingTaskFile, "old checkout content")
	oldOwned := filepath.Dir(filepath.Join(legacy, filepath.FromSlash(existingTaskFile)))
	manifest := taskIdentityFixture(t, a, taskRoot, "task", "task")
	manifest.RequirementLink = "https://project.feishu.cn/obt/userstory/detail/123"
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &oldOwned}
	taskPath := filepath.Join(taskRoot, "task")
	if err := a.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	oldRead := Action{Kind: "document.read", TaskID: "task", Path: filepath.Join(oldOwned, "existing.md")}
	if result, err := a.Dispatch(ctx, oldRead); err != nil || result.Text != "old checkout content" {
		t.Fatalf("old task file did not open before bind: %+v %v", result, err)
	}
	config := func() domain.AppConfig {
		t.Helper()
		value, err := a.Config.Load()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	bind := func(kind, role, path string, expected workspace.MaterialsGitScope) (ActionResult, error) {
		return a.Dispatch(ctx, Action{Kind: "config.documents-git." + kind, Path: path, Payload: map[string]any{"role": role, "scope": expected}})
	}
	taskRole, projectRole := domain.TaskMaterialsRepositoryRole, domain.ProjectInstructionsRepositoryRole
	taskScope := func() workspace.MaterialsGitScope { return MaterialsGitScope(a.Paths.Home, config()) }
	projectScope := func() workspace.MaterialsGitScope { return ProjectInstructionsGitScope(a.Paths.Home, config()) }
	taskClone, projectClone, otherClone := clone("task", remote), clone("project", remote), clone("other", other)
	readFile := func(path string) []byte {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	configFile := filepath.Join(taskClone, ".git", "config")
	gitConfig := readFile(configFile)
	initial := taskScope()
	if result, err := bind("bind", taskRole, taskClone, initial); err != nil || !workspace.SameDocumentsCheckout(result.Path, taskClone) {
		t.Fatalf("bind local task clone: %+v %v", result, err)
	}
	if !bytes.Equal(readFile(configFile), gitConfig) {
		t.Fatal("binding modified local Git config")
	}
	tasks := taskScope()
	if tasks.RemoteName != remoteName || tasks.URL != remote || tasks.Branch != "main" || tasks.BindingID == "" || config().MaterialsGitSettings().Enabled {
		t.Fatalf("task scope was not inferred from the clone: %+v", tasks)
	}
	if paths, err := config().MaterialsPaths(); err != nil || paths.Root != tasks.Root || paths.Requirements != filepath.Join(tasks.Root, domain.RequirementsDirectory) {
		t.Fatalf("configured requirements directory: %+v %v", paths, err)
	}
	newOwned := filepath.Dir(filepath.Join(taskClone, filepath.FromSlash(existingTaskFile)))
	if snapshot, err := a.SnapshotFor(ctx, SnapshotRequest{Tasks: true}); err != nil || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].MaterialsPath != filepath.Dir(newOwned) || !snapshot.Tasks[0].Materials.CanWrite {
		t.Fatalf("task snapshot still points outside the selected clone: %+v %v", snapshot.Tasks, err)
	}
	if _, err := a.Dispatch(ctx, oldRead); err == nil {
		t.Fatal("old task materials disk path still opened after bind")
	}
	if result, err := a.Dispatch(ctx, Action{Kind: "document.read", TaskID: "task", Path: filepath.Join(newOwned, "existing.md")}); err != nil || result.Text != "existing" {
		t.Fatalf("task did not read the same requirement in the selected clone: %+v %v", result, err)
	}
	if original, err := a.Manifests.Load(taskPath); err != nil || value(original.RequirementMaterials.WriteRoot) != oldOwned {
		t.Fatalf("task manifest was rewritten on bind: %+v %v", original.RequirementMaterials, err)
	}
	newTask := taskIdentityFixture(t, a, taskRoot, "new-task", "new-task")
	newTask.RequirementLink = manifest.RequirementLink
	newTask.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &newOwned}
	newTaskPath := filepath.Join(taskRoot, "new-task")
	if err := a.Manifests.Save(newTaskPath, newTask); err != nil {
		t.Fatalf("new profile task cannot persist external materials: %v", err)
	}
	if persisted, err := a.Manifests.Load(newTaskPath); err != nil || value(persisted.RequirementMaterials.WriteRoot) != newOwned {
		t.Fatalf("profile manifest failed to round trip external materials: %+v %v", persisted.RequirementMaterials, err)
	}
	if result, err := a.Dispatch(ctx, Action{Kind: "task.delete-risks", TaskID: "new-task", Path: newTaskPath}); err != nil {
		t.Fatalf("inspect shared external materials: %v", err)
	} else if preview := result.Data.(workspace.TaskDeletePreview); preview.Materials.CanDelete || !strings.Contains(preview.Materials.Reason, "共用") {
		t.Fatalf("materials projected for another task could be deleted: %+v", preview.Materials)
	}
	if result, err := bind("bind", projectRole, taskClone, projectScope()); err != nil || !workspace.SameDocumentsCheckout(result.Path, taskClone) {
		t.Fatalf("project instructions could not share the task clone: %+v %v", result, err)
	}
	sharedProject := projectScope()
	if taskScope() != tasks || sharedProject.Root != tasks.Root || sharedProject.Branch != tasks.Branch ||
		sharedProject.BindingID == "" || sharedProject.BindingID == tasks.BindingID {
		t.Fatalf("the shared clone did not keep separate role bindings on the same branch: task=%+v project=%+v", taskScope(), sharedProject)
	}
	const sharedTaskFile = "requirements/2026/123-test/dev/shared.md"
	const sharedProjectFile = domain.ProjectInstructionsDirectory + "/shared.md"
	writeLocalDocumentsFixture(t, taskClone, sharedTaskFile, "shared task materials")
	writeLocalDocumentsFixture(t, taskClone, sharedProjectFile, "shared project instructions")
	writeLocalDocumentsFixture(t, taskClone, "outside.txt", "stay local")
	if status, err := a.SyncMaterialsGit(ctx, tasks, true, nil); err != nil || status.LastSuccess == "" {
		t.Fatalf("sync task materials in shared clone: %+v %v", status, err)
	}
	if got := localDocumentsGit(t, remote, "diff-tree", "-r", "--no-commit-id", "--name-only", "main"); got != sharedTaskFile {
		t.Fatalf("task sync uploaded outside its directory: %q", got)
	}
	if got := localDocumentsGit(t, taskClone, "status", "--short"); got != "?? outside.txt\n?? "+sharedProjectFile {
		t.Fatalf("task sync modified project or outside files: %q", got)
	}
	if status, err := a.SyncProjectInstructionsGit(ctx, sharedProject, true, nil); err != nil || status.LastSuccess == "" {
		t.Fatalf("sync project instructions in shared clone: %+v %v", status, err)
	}
	if got := localDocumentsGit(t, remote, "diff-tree", "-r", "--no-commit-id", "--name-only", "main"); got != sharedProjectFile {
		t.Fatalf("project sync uploaded outside its directory: %q", got)
	}
	if got := localDocumentsGit(t, taskClone, "status", "--short"); got != "?? outside.txt" {
		t.Fatalf("project sync modified the outside file: %q", got)
	}
	if got := string(readFile(filepath.Join(taskClone, "outside.txt"))); got != "stay local" {
		t.Fatalf("shared sync changed the outside file content: %q", got)
	}
	for _, branch := range []string{"docs/test", "main"} {
		encodedBranch, _ := json.Marshal(branch)
		if _, err := a.Dispatch(ctx, Action{Kind: "config.fields.save", Payload: map[string]any{
			"update":           ConfigFieldsUpdate{Fields: map[string]json.RawMessage{"projectInstructionsGitBranch": encodedBranch}},
			"documentsGitRole": projectRole, "documentsGitScope": projectScope(),
		}}); err != nil {
			t.Fatalf("save shared branch %q: %v", branch, err)
		}
		if taskScope().Branch != branch || projectScope().Branch != branch {
			t.Fatalf("shared checkout split across branches after saving %q: tasks=%q project=%q", branch, taskScope().Branch, projectScope().Branch)
		}
	}
	if _, err := bind("bind", taskRole, otherClone, tasks); err == nil || !strings.Contains(err.Error(), "请先解绑") {
		t.Fatalf("replacing a remote without unlink was accepted: %v", err)
	}
	localDocumentsGit(t, projectClone, "remote", "add", "extra", other)
	if _, err := bind("bind", projectRole, projectClone, projectScope()); err == nil || !strings.Contains(err.Error(), "仅配置一个") {
		t.Fatalf("multiple remotes were accepted: %v", err)
	}
	localDocumentsGit(t, projectClone, "remote", "remove", "extra")
	localDocumentsGit(t, projectClone, "remote", "set-url", "--push", remoteName, other)
	if _, err := bind("bind", projectRole, projectClone, projectScope()); err == nil || !strings.Contains(err.Error(), "拉取和推送地址不同") {
		t.Fatalf("different push and pull endpoints were accepted: %v", err)
	}
	localDocumentsGit(t, projectClone, "config", "--unset-all", "remote."+remoteName+".pushurl")
	localDocumentsGit(t, projectClone, "config", "--add", "remote."+remoteName+".url", other)
	if _, err := bind("bind", projectRole, projectClone, projectScope()); err == nil || !strings.Contains(err.Error(), "各配置一个") {
		t.Fatalf("multiple pull endpoints were accepted: %v", err)
	}
	localDocumentsGit(t, projectClone, "config", "--unset-all", "remote."+remoteName+".url")
	localDocumentsGit(t, projectClone, "config", "remote."+remoteName+".url", remote)
	if _, err := bind("bind", projectRole, projectClone, projectScope()); err != nil {
		t.Fatal(err)
	}
	project := projectScope()
	if project.BindingID == "" || project.Root == tasks.Root || project.RemoteName != remoteName {
		t.Fatalf("project scope was not independent: %+v", project)
	}
	if settings := config().ProjectInstructionsGitSettings(); !settings.Enabled || settings.IntervalMinutes != 5 {
		t.Fatalf("initial project sync preference was not preserved: %+v", settings)
	}
	if refreshed, err := a.Dispatch(ctx, Action{Kind: "task.refresh-agents", TaskID: "task", Path: taskPath}); err != nil || refreshed.Path == "" {
		t.Fatalf("regenerate AGENTS from current repository bindings: %+v %v", refreshed, err)
	} else if content := string(readFile(refreshed.Path)); !strings.Contains(content, filepath.Join(projectClone, domain.ProjectInstructionsDirectory, workspace.AgentRouterFileName)) || !strings.Contains(content, filepath.Dir(newOwned)) {
		t.Fatalf("regenerated AGENTS still references an old checkout: %s", content)
	}
	if branches, err := a.MaterialsGitBranches(ctx, tasks); err != nil || !slices.Equal(branches, []string{"docs/test", "main"}) {
		t.Fatalf("read branches from the configured remote: %v %v", branches, err)
	}

	writeLocalDocumentsFixture(t, taskClone, taskFile, "task upload")
	writeLocalDocumentsFixture(t, taskClone, "outside.txt", "stay local")
	if status, err := a.SyncMaterialsGit(ctx, tasks, true, nil); err != nil || status.LastSuccess == "" {
		t.Fatalf("sync task clone: %+v %v", status, err)
	}
	if got := localDocumentsGit(t, remote, "show", "main:"+taskFile); got != "task upload" {
		t.Fatalf("task file missing from remote: %q", got)
	}
	if got := localDocumentsGit(t, taskClone, "status", "--short"); got != "?? outside.txt" {
		t.Fatalf("outside file did not stay local: %q", got)
	}
	if tracked, head := localDocumentsGit(t, taskClone, "rev-parse", remoteName+"/main"), localDocumentsGit(t, taskClone, "rev-parse", "HEAD"); tracked != head {
		t.Fatalf("custom tracking ref was not advanced: tracked %s, HEAD %s", tracked, head)
	}
	projectAction := func(kind string, scope workspace.MaterialsGitScope, text string) (ActionResult, error) {
		return a.Dispatch(ctx, Action{Kind: "document." + kind, Path: "guide.md", Text: text, Payload: map[string]any{
			ProjectInstructionsScopePayloadKey: scope, "expectedHash": documents.Hash([]byte(original)), "markdownEditContext": taskMaterialsMarkdownEditContext,
		}})
	}
	if result, err := projectAction("read", project, ""); err != nil || result.Text != original {
		t.Fatalf("already checked out project file did not open before sync: %+v %v", result, err)
	}
	const draft = "# Draft from previous binding\n"
	if _, err := projectAction("draft", project, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := bind("unbind", projectRole, "", project); err != nil {
		t.Fatal(err)
	}
	if _, err := projectAction("read", project, ""); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatalf("stale project disk read did not stop: %v", err)
	}
	if result, err := projectAction("draft-read", project, ""); err != nil || result.Text != draft {
		t.Fatalf("retiring editor lost its cached draft: %+v %v", result, err)
	}
	if _, err := a.SyncProjectInstructionsGit(ctx, project, true, nil); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatalf("stale project sync did not stop: %v", err)
	}
	if _, err := bind("bind", projectRole, otherClone, projectScope()); err != nil {
		t.Fatal(err)
	}
	rebound := projectScope()
	if result, err := projectAction("read", rebound, ""); err != nil || result.Data.(documents.Content).Draft != nil {
		t.Fatalf("new checkout inherited the old draft: %+v %v", result, err)
	}
	if disk := readFile(filepath.Join(projectClone, filepath.FromSlash(projectFile))); string(disk) != original {
		t.Fatalf("unlink changed the retired clone: %q", disk)
	}
	localDocumentsGit(t, otherClone, "remote", "set-url", remoteName, remote)
	if _, err := a.SyncProjectInstructionsGit(ctx, rebound, true, nil); err == nil || !strings.Contains(err.Error(), "远端已变化") {
		t.Fatalf("sync accepted an external remote change: %v", err)
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "config.fields.save", Payload: map[string]any{"update": ConfigFieldsUpdate{Fields: map[string]json.RawMessage{"projectInstructionsGitUrl": json.RawMessage(`"https://example.invalid/repo.git"`)}}}}); err == nil {
		t.Fatal("generic settings changed an inferred URL")
	}
	updateTaskSync := func(values map[string]json.RawMessage) {
		t.Helper()
		if _, err := a.Dispatch(ctx, Action{Kind: "config.fields.save", Payload: map[string]any{"update": ConfigFieldsUpdate{ObjectFields: map[string]map[string]json.RawMessage{"materialsGitSync": values}}}}); err != nil {
			t.Fatal(err)
		}
	}
	updateTaskSync(map[string]json.RawMessage{"branch": json.RawMessage(`"docs/test"`), "intervalMinutes": json.RawMessage(`11`)})
	updateTaskSync(map[string]json.RawMessage{"enabled": json.RawMessage(`true`)})
	if _, err := bind("bind", taskRole, seed, taskScope()); err != nil {
		t.Fatalf("bind different clone of same remote: %v", err)
	}
	if settings := config().MaterialsGitSettings(); !settings.Enabled || settings.Branch != "docs/test" || settings.IntervalMinutes != 11 || settings.BindingID == tasks.BindingID {
		t.Fatalf("same-source clone replacement lost the sync preferences or scope identity: %+v", settings)
	}
	if _, err := bind("unbind", projectRole, "", projectScope()); err != nil {
		t.Fatal(err)
	}
	sharedTasks := taskScope()
	if _, err := bind("bind", projectRole, seed, projectScope()); err != nil {
		t.Fatalf("bind project instructions to the configured task clone: %v", err)
	}
	if projectScope().Branch != sharedTasks.Branch {
		t.Fatal("project binding did not adopt its peer's configured branch")
	}
	if _, err := bind("unbind", projectRole, "", projectScope()); err != nil {
		t.Fatal(err)
	}
	if taskScope() != sharedTasks || projectScope().URL != "" {
		t.Fatalf("unlinking one role changed the other role or kept the project binding: tasks=%+v project=%+v", taskScope(), projectScope())
	}
}
