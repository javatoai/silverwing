package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestProfileWorktreeIsolationAndRecycleRetry(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repository := filepath.Join(root, "repository")
	gitCompare(t, root, "init", "--bare", "--initial-branch=main", remote)
	gitCompare(t, root, "init", "--initial-branch=main", repository)
	gitCompare(t, repository, "config", "user.name", "Workspace Test")
	gitCompare(t, repository, "config", "user.email", "fixture@example.invalid")
	writeCompareFile(t, repository, "README.md", "main\n")
	commitCompare(t, repository, "main")
	gitCompare(t, repository, "remote", "add", "origin", remote)
	gitCompare(t, repository, "push", "origin", "main")
	git := NewGitClient(nil, "git")
	common, err := git.CommonDirectory(repository)
	if err != nil {
		t.Fatal(err)
	}
	newProfile := func(id string) (storage.Paths, domain.AppConfig, *TaskApplicationService) {
		paths := storage.NewPaths(filepath.Join(root, "profile-"+id))
		paths.ProfileID = id
		config := domain.DefaultAppConfig()
		tasks := filepath.Join(root, "tasks-"+id)
		if err := os.Mkdir(tasks, 0o755); err != nil {
			t.Fatal(err)
		}
		config.TaskRoot = &tasks
		config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "repo", RootPath: repository, GitCommonDirectory: common, OriginURL: &remote}}
		config.Services = []domain.ServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "service", Enabled: true, MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch}}}}
		return paths, config, NewTaskApplicationService(storage.NewProfileManifestStore(paths), git, paths)
	}
	firstPaths, firstConfig, first := newProfile("first")
	_, secondConfig, second := newProfile("second")
	request := CreateTaskRequest{FolderName: "same-name", FeatureBranch: "feature/first", ServiceIDs: []string{"service"}}
	firstRecord := filepath.Join(*firstConfig.TaskRoot, request.FolderName)
	standalone := request
	standalone.ServiceIDs = nil
	firstManifest, err := first.CreateTask(ctx, firstConfig, standalone)
	if err != nil {
		t.Fatal(err)
	}
	if firstManifest.WorkspaceDirectory != "" {
		t.Fatalf("standalone task allocated an external workspace: %s", firstManifest.WorkspaceDirectory)
	}
	firstManifest, err = first.AddServices(ctx, firstConfig, firstRecord, AddWorkspaceRequest{ServiceIDs: request.ServiceIDs})
	if err != nil {
		t.Fatal(err)
	}
	if firstManifest.WorkspaceDirectory != "" || len(firstManifest.Services) != 1 || filepath.Dir(firstManifest.Services[0].WorktreePath) != firstRecord {
		t.Fatalf("unexpected worktree location: %+v", firstManifest)
	}
	manifestJSON, err := os.ReadFile(filepath.Join(firstRecord, storage.TaskManifestFileName))
	if err != nil || strings.Contains(string(manifestJSON), `"workspaceDirectory"`) {
		t.Fatalf("unexpected task record under configured root: %s %v", manifestJSON, err)
	}
	temporaryRoot := firstPaths.TaskWorktreeNamespace(firstConfig.TaskRoot)
	if _, err := os.Stat(temporaryRoot); !os.IsNotExist(err) {
		t.Fatalf("task worktree creation allocated the temporary worktree directory: %v", err)
	}
	agents, err := os.ReadFile(filepath.Join(firstRecord, AgentRouterFileName))
	if err != nil || !strings.Contains(string(agents), "## Worktree 范围") || !strings.Contains(string(agents), firstPaths.GlobalAgents) {
		t.Fatalf("task instructions missing: %q %v", agents, err)
	}
	if _, err := second.CreateTask(ctx, secondConfig, request); err == nil || !strings.Contains(err.Error(), "已在其他工作树检出") {
		t.Fatalf("same local branch was not blocked across profiles: %v", err)
	}
	request.FeatureBranch = "feature/second"
	secondManifest, err := second.CreateTask(ctx, secondConfig, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondManifest.Services) != 1 || filepath.Dir(secondManifest.Services[0].WorktreePath) != filepath.Join(*secondConfig.TaskRoot, request.FolderName) {
		t.Fatalf("second profile worktree outside configured root: %+v", secondManifest)
	}
	if err := second.Locks.ValidateWorkspaceDirectory(secondConfig, firstRecord, firstManifest); err == nil {
		t.Fatal("first task accepted under second profile's configured root")
	}
	secondCommon, err := git.CommonDirectory(secondManifest.Services[0].WorktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Locks.WithRepository(common, func() error {
		return second.Locks.WithRepository(secondCommon, func() error { return errors.New("two operations entered") })
	}); !errors.Is(err, storage.ErrLockBusy) {
		t.Fatalf("linked worktree did not share operation lock: %v", err)
	}
	firstManifest, err = first.AddModules(ctx, firstConfig, firstRecord, AddWorkspaceRequest{ServiceID: "service", Modules: []TaskModuleSelection{{ID: "extra", Name: "extra", BaseRef: "origin/main", TargetBranch: "feature/first-extra", Strategy: domain.WorkspaceStandardWorktree, Source: domain.TaskModuleTemporary, TagMode: domain.TagBuildCurrentBranch}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range firstManifest.Services {
		if filepath.Dir(ws.WorktreePath) != firstRecord {
			t.Fatalf("a task Worktree was created outside its task directory: %s", ws.WorktreePath)
		}
	}
	modulePath := firstManifest.Services[1].WorktreePath
	preview, err := first.InspectModuleRemoval(firstConfig, firstRecord, modulePath)
	if err != nil {
		t.Fatal(err)
	}
	recycler := &profileWorkspaceRecycler{trash: filepath.Join(root, "test-trash")}
	first.ModuleRemoval.Recycler = recycler
	removed, err := first.RemoveModule(ctx, firstConfig, firstRecord, preview, WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint, AcknowledgeDataLoss: true})
	if err != nil || !removed.DirectoryRecycled || removed.CleanupError != "" {
		t.Fatalf("failed to recycle module: %+v, %v", removed, err)
	}
	movedPaths := storage.NewPaths(filepath.Join(root, "moved-profile"))
	movedPaths.ProfileID = firstPaths.ProfileID
	if err := os.Rename(firstPaths.Home, movedPaths.Home); err != nil {
		t.Fatal(err)
	}
	first = NewTaskApplicationService(storage.NewProfileManifestStore(movedPaths), git, movedPaths)
	movedManifest, err := first.Manifests.Load(firstRecord)
	if err != nil || movedManifest.WorkspaceDirectory != firstManifest.WorkspaceDirectory {
		t.Fatalf("moving the profile changed its workspace: %q, %v", movedManifest.WorkspaceDirectory, err)
	}
	if movedManifest.RequirementMaterials.WriteRoot == nil || !isWithin(*movedManifest.RequirementMaterials.WriteRoot, firstRecord) {
		t.Fatalf("local materials left the task directory: %+v", movedManifest.RequirementMaterials)
	}
	if err := first.Documents.RebindProfileTaskDocuments(firstRecord, movedManifest, firstConfig); err != nil {
		t.Fatalf("rebind moved profile instructions: %v", err)
	}
	agents, err = os.ReadFile(filepath.Join(firstRecord, AgentRouterFileName))
	if err != nil || !strings.Contains(string(agents), movedPaths.GlobalAgents) || strings.Contains(string(agents), firstPaths.GlobalAgents) {
		t.Fatalf("task instructions did not follow the profile: %q %v", agents, err)
	}
	if err := os.Rename(filepath.Join(recycler.trash, "1"), modulePath); err != nil {
		t.Fatal(err)
	}
	if recovered, err := first.RestoreRecycledWorkspaces(ctx, firstConfig, firstRecord); err != nil || recovered != 1 {
		t.Fatalf("moving the profile broke the module recovery receipt: restored %d, %v", recovered, err)
	}
	archived, err := first.Archive(ctx, firstConfig, firstRecord, false)
	if err != nil || archived.LifecycleStatus != domain.TaskArchived {
		t.Fatalf("failed to archive task in configured root: %v", err)
	}
	if path, err := first.RegenerateTaskAgents(ctx, firstConfig, firstRecord); err != nil || path != filepath.Join(firstRecord, AgentRouterFileName) {
		t.Fatalf("failed to regenerate archived task instructions: %q %v", path, err)
	}
	recycler.failPath = firstRecord
	first.Lifecycle.Recycler = recycler
	if err := first.Delete(ctx, firstConfig, firstRecord, false); err == nil || !strings.Contains(err.Error(), "test receipt folder recycle failed") {
		t.Fatalf("failed recycle was not reported: %v", err)
	}
	if _, err := os.Stat(firstRecord); err != nil {
		t.Fatalf("record did not survive failed recycle: %v", err)
	}
	if got := gitCompareOutput(t, repository, "worktree", "list", "--porcelain"); !worktreeListContainsBranch(got, "feature/first") || !worktreeListContainsBranch(got, "feature/second") {
		t.Fatalf("failed recycle removed worktree registrations: %s", got)
	}
	recycler.failPath = ""
	if err := first.Delete(ctx, firstConfig, firstRecord, false); err != nil {
		t.Fatal(err)
	}
	if got := gitCompareOutput(t, repository, "worktree", "list", "--porcelain"); worktreeListContainsBranch(got, "feature/first") || worktreeListContainsBranch(got, "feature/first-extra") || !worktreeListContainsBranch(got, "feature/second") {
		t.Fatalf("recycle left stale registrations or removed a sibling registration: %s", got)
	}
	if _, err := os.Stat(filepath.Join(*secondConfig.TaskRoot, request.FolderName, storage.TaskManifestFileName)); err != nil {
		t.Fatalf("sibling profile's task was disturbed: %v", err)
	}
	if branch := gitCompareOutput(t, secondManifest.Services[0].WorktreePath, "branch", "--show-current"); branch != request.FeatureBranch {
		t.Fatalf("sibling checkout changed to %q", branch)
	}
}

func worktreeListContainsBranch(porcelain, branch string) bool {
	return slices.Contains(strings.Split(porcelain, "\n"), "branch refs/heads/"+branch)
}

func TestProfileWorkspaceDirectoryValidatesConfiguredRootAndManifest(t *testing.T) {
	paths := storage.NewPaths(filepath.Join(t.TempDir(), "profile"))
	paths.ProfileID = "profile"
	locks := NewTaskOperationLocks(paths)
	config := domain.DefaultAppConfig()
	if root := locks.TaskRoot(config); root != "" {
		t.Fatalf("unconfigured task root = %q", root)
	}
	root := t.TempDir()
	task := filepath.Join(root, "task")
	manifest := domain.TaskManifest{TaskDirectoryName: "task", Services: []domain.ServiceWorkspace{{WorktreePath: filepath.Join(task, "service-module")}}}
	if err := locks.ValidateWorkspaceDirectory(config, task, manifest); err == nil {
		t.Fatal("task accepted without configured root")
	}
	config.TaskRoot = &root
	if err := locks.ValidateWorkspaceDirectory(config, task, manifest); err != nil {
		t.Fatalf("single task directory rejected: %v", err)
	}
	manifest.WorkspaceDirectory = task
	if err := locks.ValidateWorkspaceDirectory(config, task, manifest); err != nil {
		t.Fatalf("same-directory record rejected: %v", err)
	}
	manifest.WorkspaceDirectory = filepath.Join(paths.Tasks, "task")
	if err := locks.ValidateWorkspaceDirectory(config, task, manifest); err == nil {
		t.Fatal("divergent workspace directory accepted")
	}
	manifest.WorkspaceDirectory = ""
	manifest.Services[0].WorktreePath = filepath.Join(root, "another-task", "service-module")
	if err := locks.ValidateWorkspaceDirectory(config, task, manifest); err == nil {
		t.Fatal("worktree outside task directory accepted")
	}
	manifest.Services[0].WorktreePath = filepath.Join(task, "service-module")
	manifest.TaskDirectoryName = "another-task"
	if err := locks.ValidateWorkspaceDirectory(config, task, manifest); err == nil {
		t.Fatal("divergent task identity accepted")
	}
}

func TestProfileCreateTaskRejectsUnavailableConfiguredRoot(t *testing.T) {
	parent := t.TempDir()
	paths := storage.NewPaths(filepath.Join(parent, "profile"))
	paths.ProfileID = "profile"
	config := domain.DefaultAppConfig()
	root := filepath.Join(parent, "missing-tasks")
	config.TaskRoot = &root
	service := NewTaskApplicationService(storage.NewProfileManifestStore(paths), nil, paths)
	_, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "task"})
	if err == nil || !strings.Contains(err.Error(), "任务根目录不可用") {
		t.Fatalf("missing root accepted: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("missing task root was recreated: %v", err)
	}
}

type profileWorkspaceRecycler struct {
	trash, failPath string
	count           int
}

func (r *profileWorkspaceRecycler) Recycle(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == r.failPath {
		return errors.New("test receipt folder recycle failed")
	}
	if err := os.MkdirAll(r.trash, 0o755); err != nil {
		return err
	}
	r.count++
	return os.Rename(path, filepath.Join(r.trash, fmt.Sprint(r.count)))
}
