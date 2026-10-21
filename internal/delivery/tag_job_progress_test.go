package delivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type tagWorkflowFakeGit struct {
	mu                                                         sync.Mutex
	worktree, common                                           string
	dirty, localTag, remoteTag, failPush, failRemote, conflict bool
	commands                                                   []string
}

func (g *tagWorkflowFakeGit) CommonDirectory(string) (string, error) { return g.common, nil }
func (g *tagWorkflowFakeGit) RunContext(ctx context.Context, _ string, _ time.Duration, _, _ bool, args ...string) (process.Result, error) {
	if err := ctx.Err(); err != nil {
		return process.Result{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cmd := strings.Join(args, " ")
	g.commands = append(g.commands, cmd)
	result := process.Result{ExitCode: 0}
	switch {
	case cmd == "branch --show-current":
		result.Stdout = "feature/synthetic"
	case cmd == "rev-parse --show-toplevel":
		result.Stdout = g.worktree
	case strings.Contains(cmd, "--git-common-dir"):
		result.Stdout = g.common
	case cmd == "rev-parse --absolute-git-dir":
		result.Stdout = g.common
	case args[0] == "status":
		if g.dirty {
			result.Stdout = " M synthetic.go\n"
		}
	case args[0] == "fetch":
	case args[0] == "merge-base":
		result.ExitCode = 1
	case args[0] == "-c", args[0] == "worktree":
	case args[0] == "merge":
		if g.conflict && strings.Contains(cmd, "--no-commit") {
			result.ExitCode = 1
		}
	case args[0] == "diff":
		if g.conflict && strings.Contains(cmd, "--diff-filter=U") {
			result.Stdout = "synthetic.go\n"
		}
	case cmd == "remote get-url origin":
		if g.failRemote {
			return result, errors.New("synthetic repository is unavailable")
		}
		result.Stdout = "https://example.invalid/team/synthetic.git"
	case args[0] == "log":
		result.Stdout = "abcd synthetic commit\n"
	case args[0] == "for-each-ref":
		result.Stdout = "1 1.2.3.beta-1\n"
	case args[0] == "rev-parse" && strings.Contains(cmd, "origin/feature/synthetic"):
		result.ExitCode = 1
	case args[0] == "rev-parse" && strings.Contains(cmd, "refs/tags/"):
		if g.localTag {
			result.Stdout = "synthetic-sha"
		} else {
			result.ExitCode = 1
		}
	case args[0] == "rev-parse":
		result.Stdout = "synthetic-sha"
	case args[0] == "tag":
		g.localTag = true
	case args[0] == "ls-remote":
		if g.remoteTag {
			result.Stdout = "synthetic-sha\trefs/tags/1.2.3.beta-2\n"
		}
	case args[0] == "push":
		if strings.Contains(cmd, "refs/tags/") {
			if g.failPush {
				result.ExitCode = 1
				result.Stderr = "synthetic push failure"
			} else {
				g.remoteTag = true
			}
		}
	default:
		return process.Result{}, errors.New("unexpected synthetic Git command: " + cmd)
	}
	return result, nil
}

func TestTagJobMergeWorkflowStagesAndTypedConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "merge", true: "conflict"}[conflict], func(t *testing.T) {
			task, git := tagProgressTaskFixture(t)
			git.conflict = conflict
			target := "origin/synthetic-integration"
			task.task.manifest.Services[0].TagMode = domain.TagBuildMergeToTargetBranch
			task.task.manifest.Services[0].TagTargetRef = &target
			item, err := task.Plan(context.Background(), "service:module", "", "batch")
			if err != nil {
				t.Fatal(err)
			}
			var phases []string
			failure := ""
			operation, _, err := task.Run(context.Background(), "tag.build", item, func(event TagProgress) {
				if event.Phase != "" {
					phases = append(phases, event.Phase)
				}
				if event.FailureKind != "" {
					failure = event.FailureKind
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if conflict {
				if failure != "conflict" || operation.State != domain.TagOperationConflict || len(operation.ConflictFiles) != 1 {
					t.Fatalf("conflict was not typed and persisted: %+v %s", operation, failure)
				}
				return
			}
			want := []string{"checking", "pushing-source", "merging", "pushing-target", "creating-tag", "pushing-tag", "completed"}
			if operation.State != domain.TagOperationSuccess || !reflect.DeepEqual(phases, want) {
				t.Fatalf("merge stages mismatch: %v %+v", phases, operation)
			}
		})
	}
}

func TestTagJobRemoteReadFailureKeepsInitialItemAndFailsWithoutGitWrites(t *testing.T) {
	task, git := tagProgressTaskFixture(t)
	git.failRemote = true
	item, err := task.Plan(context.Background(), "service:module", "", "batch")
	if err != nil || item.Operation.OperationID == "" {
		t.Fatalf("one repository read rejected the entire selection: %v", err)
	}
	operation, _, err := task.Run(context.Background(), "tag.build", item, nil)
	if err == nil || operation.State != domain.TagOperationFailed || operation.OperationID != item.Operation.OperationID {
		t.Fatalf("preflight failure lost its stable record: %+v %v", operation, err)
	}
	stored, readErr := task.service.store.load(task.Directory(), operation.OperationID)
	if readErr != nil || stored.State != domain.TagOperationFailed {
		t.Fatal("failed item was not persisted")
	}
	if len(git.commands) != 1 || git.commands[0] != "remote get-url origin" {
		t.Fatalf("unavailable repository reached a mutation: %v", git.commands)
	}
}

func tagProgressTaskFixture(t *testing.T) (*TagTask, *tagWorkflowFakeGit) {
	t.Helper()
	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	directory := filepath.Join(taskRoot, "synthetic-task")
	worktree := filepath.Join(directory, "service")
	common := filepath.Join(root, "repository", ".git")
	for _, path := range []string{worktree, common} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &taskRoot
	config.Repositories = []domain.RepositoryConfig{{ID: "repository", RootPath: filepath.Dir(common), GitCommonDirectory: common}}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName = "synthetic-task"
	manifest.FolderName = "synthetic-task"
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "repository", ServiceName: "synthetic", WorktreePath: worktree, Branch: "feature/synthetic", GroupServiceID: "service", ModuleID: "module", TagEnabled: true, TagMode: domain.TagBuildCurrentBranch, PushRemote: "origin"}}
	git := &tagWorkflowFakeGit{worktree: worktree, common: common}
	paths := storage.NewPaths(filepath.Join(root, "home"))
	service := &Service{git: git, paths: paths, locks: workspace.NewOperationLocks(paths.Locks)}
	return &TagTask{service: service, task: resolvedTask{config: config, directory: directory, manifest: manifest}}, git
}

func TestTagJobRealWorkflowProgressIsDurableAndSourceBytesStayOwned(t *testing.T) {
	task, git := tagProgressTaskFixture(t)
	item, err := task.Plan(context.Background(), "service:module", "", "synthetic-batch")
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	err = task.WithLock(func() error {
		operation, _, err := task.Run(context.Background(), "tag.build", item, func(event TagProgress) {
			if event.Phase != "" {
				phases = append(phases, event.Phase)
			}
			if event.Operation != nil {
				stored, readErr := task.service.store.load(task.Directory(), event.Operation.OperationID)
				if readErr != nil || !sameTagOperation(stored, *event.Operation) {
					t.Errorf("progress preceded durable write: %v", readErr)
				}
			}
		})
		if operation.OperationID != item.Operation.OperationID || operation.State != domain.TagOperationSuccess {
			t.Errorf("placeholder identity/result changed: %+v %v", operation, err)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"checking", "pushing-source", "creating-tag", "pushing-tag", "completed"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("actual workflow phases=%v", phases)
	}
	if !git.remoteTag {
		t.Fatal("synthetic remote push was not verified")
	}
	for _, cmd := range git.commands {
		if strings.HasPrefix(cmd, "push") && strings.Contains(cmd, "--force") {
			t.Fatal("remote write semantics expanded")
		}
	}
}

func TestTagJobTypedDirtyAndPartialResumeReuseExistingWorkflow(t *testing.T) {
	for _, dirty := range []bool{true, false} {
		t.Run(strings.ToUpper(map[bool]string{true: "dirty", false: "partial"}[dirty]), func(t *testing.T) {
			task, git := tagProgressTaskFixture(t)
			git.dirty, git.failPush = dirty, !dirty
			item, err := task.Plan(context.Background(), "service:module", "", "batch")
			if err != nil {
				t.Fatal(err)
			}
			kind := ""
			var operation domain.TagOperation
			err = task.WithLock(func() error {
				var runErr error
				operation, _, runErr = task.Run(context.Background(), "tag.build", item, func(event TagProgress) {
					if event.FailureKind != "" {
						kind = event.FailureKind
					}
				})
				return runErr
			})
			if err != nil {
				t.Fatal(err)
			}
			if dirty {
				if kind != "uncommitted" || operation.State != domain.TagOperationFailed || git.localTag {
					t.Fatal("dirty tree was not rejected before mutation")
				}
				return
			}
			if operation.State != domain.TagOperationPartial || operation.Tag == nil {
				t.Fatalf("partial result missing: %+v", operation)
			}
			git.failPush = false
			retry, err := task.Plan(context.Background(), "", operation.OperationID, "")
			if err != nil {
				t.Fatal(err)
			}
			err = task.WithLock(func() error {
				updated, _, err := task.Run(context.Background(), "tag.retry", retry, nil)
				if updated.State != domain.TagOperationSuccess || value(updated.Tag) != value(operation.Tag) {
					t.Error("partial retry did not recover the same tag")
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTagJobTransitionAndProbeCASKeepFieldOwnership(t *testing.T) {
	task, _ := tagProgressTaskFixture(t)
	item, err := task.Plan(context.Background(), "service:module", "", "batch")
	if err != nil {
		t.Fatal(err)
	}
	operation := item.Operation
	tag := "1.2.3.beta-2"
	operation.Tag = &tag
	operation.State = domain.TagOperationTagPushed
	if err := task.service.store.save(context.Background(), task.Directory(), operation); err != nil {
		t.Fatal(err)
	}
	stale := operation
	if err := task.service.transition(context.Background(), task.Directory(), &operation, domain.TagOperationSuccess, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, changed, err := task.service.store.updateIfUnchanged(context.Background(), task.Directory(), stale, func(current domain.TagOperation) domain.TagOperation {
		current.GenbuStatus.Build = domain.GenbuFailed
		return current
	})
	if err != nil || changed {
		t.Fatal("stale query overwrote a newer local phase")
	}
	_, changed, err = task.service.store.updateIfUnchanged(context.Background(), task.Directory(), operation, func(current domain.TagOperation) domain.TagOperation {
		current.GenbuStatus.Build = domain.GenbuBuilding
		return current
	})
	if err != nil || !changed {
		t.Fatal(err)
	}
	if err := task.service.transition(context.Background(), task.Directory(), &operation, domain.TagOperationSuccess, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if operation.GenbuStatus.Build != domain.GenbuBuilding {
		t.Fatal("local progress overwrote accepted Genbu status")
	}
}

func TestTagJobResourceAliasesUseOneRemoteLane(t *testing.T) {
	aliases := []string{"https://example.invalid/team/synthetic.git", "git@example.invalid:team/synthetic.git", "ssh://git@example.invalid/team/synthetic.git"}
	for _, alias := range aliases {
		if got := NormalizeTagRemote(alias); got != "example.invalid/team/synthetic" {
			t.Fatalf("unsafe alias identity %q", got)
		}
	}
}
