package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestCreateTaskCancellationAtProgressBoundariesRollsBackOwnedDirectory(t *testing.T) {
	for _, target := range []struct {
		name   string
		phase  TaskCreationProgressPhase
		status TaskCreationProgressStatus
	}{
		{name: "saving-started", phase: TaskCreationPhaseSavingManifest, status: TaskCreationProgressStarted},
		{name: "saving-completed", phase: TaskCreationPhaseSavingManifest, status: TaskCreationProgressCompleted},
		{name: "documents-started", phase: TaskCreationPhaseWritingDocuments, status: TaskCreationProgressStarted},
		{name: "documents-completed", phase: TaskCreationPhaseWritingDocuments, status: TaskCreationProgressCompleted},
	} {
		t.Run(target.name, func(t *testing.T) {
			service, config, root := optionalServiceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var canceled bool
			_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "cancelled"}, func(event TaskCreationProgress) {
				if !canceled && event.Phase == target.phase && event.Status == target.status {
					canceled = true
					cancel()
				}
			})
			if !canceled || !errors.Is(err, context.Canceled) {
				t.Fatalf("progress-boundary cancellation was not returned: callback=%t err=%v", canceled, err)
			}
			if _, statErr := os.Lstat(filepath.Join(root, "cancelled")); !os.IsNotExist(statErr) {
				t.Fatalf("generated-only failed task directory was not cleaned: %v", statErr)
			}
		})
	}
}

func TestCreateTaskRollbackPreservesUnknownRootAndNestedFiles(t *testing.T) {
	for _, target := range []string{"root", "nested"} {
		t.Run(target, func(t *testing.T) {
			service, config, root := optionalServiceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var taskPath, sentinel string
			var callbackErr error
			phase, status := TaskCreationPhasePreparingWorktree, TaskCreationProgressCompleted
			if target == "nested" {
				phase, status = TaskCreationPhaseWritingDocuments, TaskCreationProgressCompleted
			}
			_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "retained"}, func(event TaskCreationProgress) {
				if callbackErr != nil || event.Phase != phase || event.Status != status {
					return
				}
				taskPath = filepath.Join(root, "retained")
				if target == "root" {
					sentinel = filepath.Join(taskPath, "user-root-data.txt")
				} else {
					sentinel = filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, "user-nested-data.txt")
				}
				callbackErr = os.WriteFile(sentinel, []byte("preserve me"), 0600)
				if callbackErr == nil {
					cancel()
				}
			})
			if callbackErr != nil {
				t.Fatal(callbackErr)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation with retained content, got %v", err)
			}
			data, readErr := os.ReadFile(sentinel)
			if readErr != nil || string(data) != "preserve me" {
				t.Fatalf("rollback removed unknown %s content: %q %v", target, data, readErr)
			}
			if err == nil || !strings.Contains(err.Error(), sentinel) {
				t.Fatalf("rollback did not report the retained path: %v", err)
			}
		})
	}
}

func TestCreateTaskRollbackDoesNotTouchReplacementTaskDirectory(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var callbackErr error
	newSentinel := filepath.Join(root, "replaced", "new-owner.txt")
	newManifest := filepath.Join(root, "replaced", storage.TaskManifestFileName)
	manifestBytes := []byte("replacement owner's reserved manifest")
	movedOriginal := filepath.Join(root, "original-directory")
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "replaced"}, func(event TaskCreationProgress) {
		if callbackErr != nil || event.Phase != TaskCreationPhasePreparingWorktree || event.Status != TaskCreationProgressCompleted {
			return
		}
		taskPath := filepath.Join(root, "replaced")
		if callbackErr = os.Rename(taskPath, movedOriginal); callbackErr != nil {
			return
		}
		if callbackErr = os.Mkdir(taskPath, 0755); callbackErr != nil {
			return
		}
		callbackErr = os.WriteFile(newSentinel, []byte("new owner"), 0600)
		if callbackErr == nil {
			callbackErr = os.WriteFile(newManifest, manifestBytes, 0600)
		}
		if callbackErr == nil {
			cancel()
		}
	})
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "任务目录已被替换") {
		t.Fatalf("replacement was not reported while preserving cancellation: %v", err)
	}
	data, readErr := os.ReadFile(newSentinel)
	if readErr != nil || string(data) != "new owner" {
		t.Fatalf("rollback touched replacement directory: %q %v", data, readErr)
	}
	data, readErr = os.ReadFile(newManifest)
	if readErr != nil || string(data) != string(manifestBytes) {
		t.Fatalf("rollback touched replacement reserved manifest: %q %v", data, readErr)
	}
	if _, statErr := os.Stat(movedOriginal); statErr != nil {
		t.Fatalf("original directory unexpectedly disappeared: %v", statErr)
	}
}

func TestCreateTaskSupportsTaskRootJunctionAndRollback(t *testing.T) {
	service, config, profileRoot := optionalServiceFixture(t)
	targetRoot := filepath.Join(profileRoot, "real-task-root")
	linkedRoot := filepath.Join(profileRoot, "linked-task-root")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", linkedRoot, targetRoot).CombinedOutput()
		if err != nil {
			t.Skipf("create TaskRoot junction: %v: %s", err, output)
		}
	} else if err := os.Symlink(targetRoot, linkedRoot); err != nil {
		t.Skipf("create TaskRoot symlink: %v", err)
	}
	config.TaskRoot = &linkedRoot

	if _, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "linked-root-success"}); err != nil {
		t.Fatalf("CreateTask under linked TaskRoot: %v", err)
	}
	if _, err := service.Manifests.Load(filepath.Join(linkedRoot, "linked-root-success")); err != nil {
		t.Fatalf("task under linked TaskRoot was not created: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	taskPath := filepath.Join(linkedRoot, "linked-root-cancel")
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "linked-root-cancel"}, func(event TaskCreationProgress) {
		if event.Phase == TaskCreationPhaseWritingDocuments && event.Status == TaskCreationProgressStarted {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CreateTask under linked TaskRoot error = %v, want context cancellation", err)
	}
	if _, err := os.Lstat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("canceled task under linked TaskRoot was not safely removed: %v", err)
	}
}

func TestCreateGitTaskCancellationDuringDocumentsRollsBackWorktreeAndBranch(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	remote := filepath.Join(root, "remote.git")
	taskRoot := filepath.Join(root, "tasks")
	if err := os.MkdirAll(repository, 0700); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, root, "init", "--bare", "--initial-branch=main", remote)
	gitCompare(t, repository, "init", "--initial-branch=main")
	gitCompare(t, repository, "config", "user.name", "Task Creation Rollback Test")
	gitCompare(t, repository, "config", "user.email", "task-rollback@example.invalid")
	writeCompareFile(t, repository, "base.txt", "base\n")
	commitCompare(t, repository, "base")
	gitCompare(t, repository, "remote", "add", "origin", remote)
	gitCompare(t, repository, "push", "-u", "origin", "main")
	common := gitCompareOutput(t, repository, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(repository, common)
	}
	globalConfig := filepath.Join(root, "empty-gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0600); err != nil {
		t.Fatal(err)
	}
	git := NewGitClient(nil, "git")
	git.Environment = map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": globalConfig, "GIT_TERMINAL_PROMPT": "0",
	}
	git.RemoveEnvironment = []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_NAMESPACE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG", "GIT_TEMPLATE_DIR", "GIT_EXEC_PATH",
		"GIT_SSH_COMMAND", "GIT_SSH",
	}
	service := NewTaskApplicationService(nil, git, storage.NewPaths(filepath.Join(root, "profile")))
	config := domain.DefaultAppConfig()
	config.TaskRoot = &taskRoot
	config.Repositories = []domain.RepositoryConfig{{
		ID: "repo", Name: "fixture", RootPath: repository, GitCommonDirectory: common, OriginURL: &remote,
	}}
	group := config.Groups[0]
	mainRef := "origin/main"
	serviceConfig := domain.GroupServiceConfig{
		ID: "git-service", RepositoryID: "repo", DisplayName: "git-service", Enabled: true,
		MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		Modules: []domain.ServiceModuleConfig{{
			ID: "api", Name: "api", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch,
			MasterBranch: &mainRef, CustomCommands: []domain.WorkspaceCommandConfig{},
		}},
	}
	group.Services = []domain.GroupServiceConfig{serviceConfig}
	for index := range config.Groups {
		if config.Groups[index].ID == group.ID {
			config.Groups[index] = group
		}
	}
	featureBranch := "feature/cancel-git-task"
	taskPath := filepath.Join(taskRoot, "cancel-git-task")
	modulePathName, err := moduleDirectoryName(serviceConfig, serviceConfig.Modules[0])
	if err != nil {
		t.Fatal(err)
	}
	worktreePath := filepath.Join(taskPath, modulePathName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worktreeWasPrepared, branchWasPrepared, canceled := false, false, false
	_, err = service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{
		FolderName: "cancel-git-task", GroupID: group.ID, FeatureBranch: featureBranch, ServiceIDs: []string{serviceConfig.ID},
	}, func(event TaskCreationProgress) {
		if canceled || event.Phase != TaskCreationPhaseWritingDocuments || event.Status != TaskCreationProgressStarted {
			return
		}
		canceled = true
		worktrees, worktreeErr := git.Run(repository, "worktree", "list", "--porcelain")
		if worktreeErr == nil {
			for _, line := range strings.Split(worktrees.Stdout, "\n") {
				if strings.HasPrefix(line, "worktree ") && samePath(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")), worktreePath) {
					worktreeWasPrepared = true
				}
			}
		}
		branch, branchErr := git.Run(repository, "show-ref", "--verify", "--quiet", "refs/heads/"+featureBranch)
		branchWasPrepared = branchErr == nil && branch.ExitCode == 0
		cancel()
	})
	if !canceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("document-phase cancellation error = %v (callback reached=%t), want context cancellation", err, canceled)
	}
	if !worktreeWasPrepared || !branchWasPrepared {
		t.Fatalf("test did not cancel after a created Git worktree and task branch: worktree=%t branch=%t", worktreeWasPrepared, branchWasPrepared)
	}
	if _, statErr := os.Lstat(worktreePath); !os.IsNotExist(statErr) {
		t.Errorf("rollback left the created worktree directory: %v", statErr)
	}
	if _, statErr := os.Lstat(taskPath); !os.IsNotExist(statErr) {
		t.Errorf("rollback left the task directory: %v", statErr)
	}
	worktrees, worktreeErr := git.Run(repository, "worktree", "list", "--porcelain")
	if worktreeErr != nil {
		t.Fatalf("list worktrees after rollback: %v", worktreeErr)
	}
	for _, line := range strings.Split(worktrees.Stdout, "\n") {
		if strings.HasPrefix(line, "worktree ") && samePath(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")), worktreePath) {
			t.Fatalf("Git worktree registration remains after rollback: %s", line)
		}
	}
	branch, branchErr := git.RunContext(context.Background(), repository, 2*time.Minute, false, false, "show-ref", "--verify", "--quiet", "refs/heads/"+featureBranch)
	if branchErr != nil {
		t.Fatalf("check task branch after rollback: %v", branchErr)
	}
	if branch.ExitCode == 0 {
		t.Fatal("task-created local branch remains after rollback")
	}
}
