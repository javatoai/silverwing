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

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestCreateTaskResolverFailurePreservesResolverOwnedTaskPath(t *testing.T) {
	for _, cancelDuringResolve := range []bool{false, true} {
		name := "resolver-error"
		if cancelDuringResolve {
			name = "resolver-cancel"
		}
		t.Run(name, func(t *testing.T) {
			service, _, _, config := newTaskCreationLockTestServices(t)
			taskPath := filepath.Join(*config.TaskRoot, name)
			sentinel := filepath.Join(taskPath, "user-note.txt")
			forcedErr := errors.New("forced materials preparation failure")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service.RequirementMaterials = func(context.Context, domain.AppConfig, string, string) (domain.RequirementMaterialsDirectory, *string, error) {
				if err := os.MkdirAll(taskPath, 0o755); err != nil {
					return domain.RequirementMaterialsDirectory{}, nil, err
				}
				if err := os.WriteFile(sentinel, []byte("external user file must survive"), 0o600); err != nil {
					return domain.RequirementMaterialsDirectory{}, nil, err
				}
				if cancelDuringResolve {
					cancel()
					return domain.RequirementMaterialsDirectory{}, nil, context.Canceled
				}
				return domain.RequirementMaterialsDirectory{}, nil, forcedErr
			}

			_, err := service.CreateTask(ctx, config, CreateTaskRequest{FolderName: name, RequirementLink: "123"})
			if cancelDuringResolve {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("CreateTask error = %v, want context cancellation", err)
				}
			} else if !errors.Is(err, forcedErr) {
				t.Fatalf("CreateTask error = %v, want resolver error", err)
			}
			if content, readErr := os.ReadFile(sentinel); readErr != nil || string(content) != "external user file must survive" {
				t.Fatalf("resolver-owned content changed: content=%q err=%v", content, readErr)
			}
		})
	}
}

func TestCreateTaskReportsSoftMaterialsFailureAndContinues(t *testing.T) {
	reason := "需求资料下载超时"
	blankReason := " \t\n"
	tests := []struct {
		name        string
		materials   domain.RequirementMaterialsDirectory
		wantMessage string
	}{
		{
			name: "failed with actionable reason",
			materials: domain.RequirementMaterialsDirectory{
				Status: domain.MaterialsFailed, FailureReason: &reason,
			},
			wantMessage: "需求资料未能准备，但任务创建可以继续：" + reason,
		},
		{
			name:        "failed without reason",
			materials:   domain.RequirementMaterialsDirectory{Status: domain.MaterialsFailed},
			wantMessage: "需求资料准备步骤已返回",
		},
		{
			name: "failed with blank reason",
			materials: domain.RequirementMaterialsDirectory{
				Status: domain.MaterialsFailed, FailureReason: &blankReason,
			},
			wantMessage: "需求资料准备步骤已返回",
		},
		{
			name:        "ordinary ready result",
			materials:   domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady},
			wantMessage: "需求资料准备步骤已返回",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			service, _, _, config := newTaskCreationLockTestServices(t)
			service.RequirementMaterials = func(context.Context, domain.AppConfig, string, string) (domain.RequirementMaterialsDirectory, *string, error) {
				return test.materials, nil, nil
			}
			var completed []TaskCreationProgress
			manifest, err := service.CreateTaskWithProgress(context.Background(), config, CreateTaskRequest{
				FolderName:      "materials-" + strings.ReplaceAll(strings.ToLower(test.name), " ", "-"),
				RequirementLink: "123",
			}, func(event TaskCreationProgress) {
				if event.Phase == TaskCreationPhasePreparingMaterials && event.Status == TaskCreationProgressCompleted {
					completed = append(completed, event)
				}
			})
			if err != nil {
				t.Fatalf("CreateTaskWithProgress returned an error for a soft materials result: %v", err)
			}
			if len(completed) != 1 || completed[0].Message != test.wantMessage {
				t.Fatalf("materials completion progress = %+v, want message %q", completed, test.wantMessage)
			}
			stored, err := service.Manifests.Load(filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName))
			if err != nil {
				t.Fatalf("soft materials result did not create a task: %v", err)
			}
			if stored.RequirementMaterials.Status != test.materials.Status {
				t.Fatalf("persisted material status = %q, want %q", stored.RequirementMaterials.Status, test.materials.Status)
			}
			if test.materials.FailureReason == nil {
				if stored.RequirementMaterials.FailureReason != nil {
					t.Fatalf("unexpected persisted failure reason: %q", *stored.RequirementMaterials.FailureReason)
				}
			} else if stored.RequirementMaterials.FailureReason == nil || *stored.RequirementMaterials.FailureReason != *test.materials.FailureReason {
				t.Fatalf("persisted failure reason = %v, want %q", stored.RequirementMaterials.FailureReason, *test.materials.FailureReason)
			}
		})
	}
}

func TestCreateTaskSavingCancellationRollsBackWithoutPublishing(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	taskPath := filepath.Join(*config.TaskRoot, "cancel-during-save")
	completed := false
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "cancel-during-save"}, func(event TaskCreationProgress) {
		if event.Phase == TaskCreationPhaseSavingManifest && event.Status == TaskCreationProgressStarted {
			cancel()
		}
		if event.Phase == TaskCreationPhaseCompleted && event.Status == TaskCreationProgressCompleted {
			completed = true
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateTaskWithProgress error = %v, want context cancellation", err)
	}
	if completed {
		t.Fatal("canceled task creation emitted a successful completion event")
	}
	if _, err := service.Manifests.Load(taskPath); err == nil {
		t.Fatal("canceled task creation published a manifest")
	}
	if _, err := os.Lstat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("pure generated task directory was not rolled back: %v", err)
	}
}

func TestCreateTaskRollbackDoesNotTouchReplacementTaskDirectory(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	taskPath := filepath.Join(*config.TaskRoot, "replaced")
	movedOriginal := filepath.Join(*config.TaskRoot, "original-directory")
	newSentinel := filepath.Join(taskPath, "new-owner.txt")
	newManifest := filepath.Join(taskPath, storage.TaskManifestFileName)
	manifestBytes := []byte("replacement owner's reserved manifest")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var callbackErr error
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "replaced"}, func(event TaskCreationProgress) {
		if callbackErr != nil || event.Phase != TaskCreationPhaseSavingManifest || event.Status != TaskCreationProgressStarted {
			return
		}
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
	for _, file := range []struct {
		path string
		want string
	}{{newSentinel, "new owner"}, {newManifest, string(manifestBytes)}} {
		data, readErr := os.ReadFile(file.path)
		if readErr != nil || string(data) != file.want {
			t.Errorf("rollback changed replacement file %s: %q %v", file.path, data, readErr)
		}
	}
	if _, statErr := os.Stat(movedOriginal); statErr != nil {
		t.Fatalf("original directory unexpectedly disappeared: %v", statErr)
	}
}

func TestCreateTaskSupportsTaskRootJunctionAndRollback(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	base := t.TempDir()
	targetRoot := filepath.Join(base, "real-task-root")
	linkedRoot := filepath.Join(base, "linked-task-root")
	if err := os.MkdirAll(targetRoot, 0755); err != nil {
		t.Fatal(err)
	}
	rootSentinel := filepath.Join(targetRoot, "keep-existing-root-file.txt")
	if err := os.WriteFile(rootSentinel, []byte("preserve linked root data"), 0600); err != nil {
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
	cancellationCallbackReached := false
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "linked-root-cancel"}, func(event TaskCreationProgress) {
		if event.Phase == TaskCreationPhaseSavingManifest && event.Status == TaskCreationProgressStarted {
			cancellationCallbackReached = true
			cancel()
		}
	})
	if !cancellationCallbackReached {
		t.Fatal("task creation did not reach the manifest-saving cancellation callback")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CreateTask under linked TaskRoot error = %v, want context cancellation", err)
	}
	if _, err := os.Lstat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("canceled task under linked TaskRoot was not safely removed: %v", err)
	}
	if content, err := os.ReadFile(rootSentinel); err != nil || string(content) != "preserve linked root data" {
		t.Fatalf("canceled task cleanup changed unrelated linked-root data: %q %v", content, err)
	}
}

func TestCreateTaskRollbackPreservesUnknownContentAndReportsItsPaths(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	taskPath := filepath.Join(*config.TaskRoot, "blocked-generated-document")
	rootSentinel := filepath.Join(taskPath, "user-note.txt")
	nestedSentinel := filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName, "keep.txt")

	_, err := service.CreateTaskWithProgress(context.Background(), config, CreateTaskRequest{FolderName: "blocked-generated-document"}, func(event TaskCreationProgress) {
		if event.Phase != TaskCreationPhaseSavingManifest || event.Status != TaskCreationProgressStarted {
			return
		}
		if writeErr := os.WriteFile(rootSentinel, []byte("keep"), 0o600); writeErr != nil {
			t.Errorf("write root sentinel: %v", writeErr)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(nestedSentinel), 0o755); mkdirErr != nil {
			t.Errorf("create blocked document path: %v", mkdirErr)
			return
		}
		if writeErr := os.WriteFile(nestedSentinel, []byte("keep nested content"), 0o600); writeErr != nil {
			t.Errorf("write nested sentinel: %v", writeErr)
		}
	})
	if err == nil {
		t.Fatal("task creation succeeded despite a directory occupying the generated context document path")
	}
	if content, readErr := os.ReadFile(rootSentinel); readErr != nil || string(content) != "keep" {
		t.Fatalf("unknown root content was not preserved: content=%q err=%v", content, readErr)
	}
	if content, readErr := os.ReadFile(nestedSentinel); readErr != nil || string(content) != "keep nested content" {
		t.Fatalf("unknown nested content was not preserved: content=%q err=%v", content, readErr)
	}
	if !strings.Contains(err.Error(), rootSentinel) || !strings.Contains(err.Error(), nestedSentinel) {
		t.Fatalf("rollback error = %q, want both preserved paths", err)
	}
	if _, statErr := os.Stat(filepath.Join(taskPath, "silverwing.json")); !os.IsNotExist(statErr) {
		t.Fatalf("generated manifest was not cleaned up: %v", statErr)
	}
}

func TestCreateTaskRollbackDoesNotTraverseUnknownDirectoryLink(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	taskPath := filepath.Join(*config.TaskRoot, "linked-workspace")
	linkedWorkspace := filepath.Join(taskPath, AgentWorkspaceDir)
	external := filepath.Join(t.TempDir(), "external-workspace")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	externalSentinel := filepath.Join(external, "must-survive.txt")
	if err := os.WriteFile(externalSentinel, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkCreated := false
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "linked-workspace"}, func(event TaskCreationProgress) {
		if event.Phase != TaskCreationPhaseSavingManifest || event.Status != TaskCreationProgressStarted {
			return
		}
		if runtime.GOOS == "windows" {
			output, linkErr := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", linkedWorkspace, external).CombinedOutput()
			if linkErr != nil {
				t.Skipf("create junction fixture: %v: %s", linkErr, output)
			}
		} else if linkErr := os.Symlink(external, linkedWorkspace); linkErr != nil {
			t.Skipf("create symlink fixture: %v", linkErr)
		}
		linkCreated = true
		cancel()
	})
	if !linkCreated {
		t.Fatal("link fixture was not created")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateTaskWithProgress error = %v, want context cancellation", err)
	}
	if _, statErr := os.Lstat(linkedWorkspace); statErr != nil {
		t.Fatalf("unknown link was not preserved: %v", statErr)
	}
	if content, readErr := os.ReadFile(externalSentinel); readErr != nil || string(content) != "external" {
		t.Fatalf("rollback changed link target content: content=%q err=%v", content, readErr)
	}
	if !strings.Contains(err.Error(), linkedWorkspace) {
		t.Fatalf("rollback error = %q, want link path %q", err, linkedWorkspace)
	}
}

func TestCreateTaskRequiresAgentContextAndHandoffTogether(t *testing.T) {
	for _, onlyContext := range []bool{true, false} {
		name := "handoff-only"
		if onlyContext {
			name = "context-only"
		}
		t.Run(name, func(t *testing.T) {
			service, _, _, config := newTaskCreationLockTestServices(t)
			resolverCalls := 0
			service.RequirementMaterials = func(context.Context, domain.AppConfig, string, string) (domain.RequirementMaterialsDirectory, *string, error) {
				resolverCalls++
				return domain.RequirementMaterialsDirectory{}, nil, nil
			}
			request := CreateTaskRequest{FolderName: name, RequirementLink: "123"}
			if onlyContext {
				request.AgentContext = &domain.AgentTaskContext{ProtocolVersion: "2", DocumentationDirectory: "docs", HandoffRelativePath: ".workspace/HANDOFF.md"}
			} else {
				emptyHandoff := ""
				request.AgentHandoffMarkdown = &emptyHandoff
			}

			if _, err := service.CreateTask(context.Background(), config, request); err == nil {
				t.Fatal("CreateTask accepted an unpaired Agent context or handoff")
			}
			if resolverCalls != 0 {
				t.Fatalf("materials resolver was called %d times before pair validation", resolverCalls)
			}
			if _, err := os.Lstat(filepath.Join(*config.TaskRoot, name)); !os.IsNotExist(err) {
				t.Fatalf("unpaired request created a task directory: %v", err)
			}
		})
	}
}

func TestCreateTaskAllowsBothAgentFieldsAndOrdinaryTask(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	ordinary, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "ordinary-task"})
	if err != nil {
		t.Fatalf("CreateTask ordinary request: %v", err)
	}
	if ordinary.AgentContext != nil {
		t.Fatalf("ordinary task unexpectedly has AgentContext: %+v", ordinary.AgentContext)
	}

	ctx := &domain.AgentTaskContext{ProtocolVersion: "2", DocumentationDirectory: "docs", HandoffRelativePath: ".workspace/HANDOFF.md"}
	emptyHandoff := ""
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{
		FolderName: "paired-agent-task", AgentContext: ctx, AgentHandoffMarkdown: &emptyHandoff,
	})
	if err != nil {
		t.Fatalf("CreateTask paired request with empty handoff: %v", err)
	}
	if manifest.AgentContext == nil || manifest.AgentContext.HandoffRelativePath != ".workspace/HANDOFF.md" {
		t.Fatalf("paired task context was not retained: %+v", manifest.AgentContext)
	}
	handoffPath := filepath.Join(*config.TaskRoot, "paired-agent-task", AgentWorkspaceDir, HandoffFileName)
	content, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("read default handoff document: %v", err)
	}
	if !strings.HasPrefix(string(content), "# 任务交接") {
		t.Fatalf("empty provided handoff did not produce default template: %q", content)
	}
}
