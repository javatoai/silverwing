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

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/requirementprovider"
	"silverwing-go/internal/storage"
)

func releaseReviewAssertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("file changed: %s, got %q, want %q, error %v", path, got, want, err)
	}
}

func TestReleaseReviewDeleteRejectsInvalidTaskDirectories(t *testing.T) {
	for _, location := range []string{"external", "root", "nested", "mismatched basename", "missing configured root", "configured root is file", "unconfigured root"} {
		t.Run(location, func(t *testing.T) {
			service, _, _, config := newTaskCreationLockTestServices(t)
			manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "protected-task"})
			if err != nil {
				t.Fatal(err)
			}
			root := *config.TaskRoot
			original := filepath.Join(root, manifest.TaskDirectoryName)
			target := original
			switch location {
			case "external":
				target = filepath.Join(t.TempDir(), manifest.TaskDirectoryName)
			case "root":
				target = root
				if err := service.Manifests.Save(root, manifest); err != nil {
					t.Fatal(err)
				}
			case "nested":
				target = filepath.Join(root, "container", manifest.TaskDirectoryName)
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
			case "mismatched basename":
				target = filepath.Join(root, "different-task")
			case "missing configured root":
				missing := filepath.Join(t.TempDir(), "missing-root")
				config.TaskRoot = &missing
			case "configured root is file":
				file := filepath.Join(t.TempDir(), "root-file")
				if err := os.WriteFile(file, []byte("configured file bytes"), 0600); err != nil {
					t.Fatal(err)
				}
				config.TaskRoot = &file
			case "unconfigured root":
				config.TaskRoot = nil
			}
			if target != original && target != root {
				if err := os.Rename(original, target); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := filepath.Join(target, "user-sentinel.txt")
			bytes := []byte("user bytes\r\nwith trailing spaces  ")
			if err := os.WriteFile(sentinel, bytes, 0600); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(target, storage.TaskManifestFileName)
			manifestBytes, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Delete(context.Background(), config, target, false); err == nil {
				t.Fatal("Delete accepted an invalid task directory")
			}
			releaseReviewAssertFile(t, sentinel, bytes)
			releaseReviewAssertFile(t, manifestPath, manifestBytes)
		})
	}
}

func TestReleaseReviewDeletePreservesSiblingAndExternalMaterials(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "delete-task"})
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(*config.TaskRoot, "sibling.txt")
	externalRoot := t.TempDir()
	external := filepath.Join(externalRoot, "materials.txt")
	for _, path := range []string{sibling, external} {
		if err := os.WriteFile(path, []byte("keep these bytes\r\n "), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &externalRoot}
	taskPath := filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName)
	if err := service.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Delete(ctx, config, taskPath, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Delete = %v, want context.Canceled", err)
	}
	if _, err := service.Manifests.Load(taskPath); err != nil {
		t.Fatal("canceled deletion changed the task", err)
	}
	if err := service.Delete(context.Background(), config, taskPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("valid task was not removed: %v", err)
	}
	for _, path := range []string{sibling, external} {
		releaseReviewAssertFile(t, path, []byte("keep these bytes\r\n "))
	}
}

func TestReleaseReviewDeleteAllowsLinkedTaskRootAncestor(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	base := t.TempDir()
	realAncestor := filepath.Join(base, "real")
	linkedAncestor := filepath.Join(base, "linked")
	if err := os.MkdirAll(filepath.Join(realAncestor, "tasks"), 0755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", linkedAncestor, realAncestor).CombinedOutput()
		if err != nil {
			t.Skipf("create TaskRoot ancestor junction: %v: %s", err, output)
		}
	} else if err := os.Symlink(realAncestor, linkedAncestor); err != nil {
		t.Skipf("create TaskRoot ancestor symlink: %v", err)
	}
	root := filepath.Join(linkedAncestor, "tasks")
	config.TaskRoot = &root
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "linked-task"})
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(realAncestor, "tasks", "keep.txt")
	if err := os.WriteFile(sibling, []byte("linked ancestor content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), config, filepath.Join(root, manifest.TaskDirectoryName), false); err != nil {
		t.Fatalf("legitimate linked ancestor was rejected: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(realAncestor, "tasks", manifest.TaskDirectoryName)); !os.IsNotExist(err) {
		t.Fatalf("task remained under linked ancestor: %v", err)
	}
	releaseReviewAssertFile(t, sibling, []byte("linked ancestor content"))
}

func TestReleaseReviewGeneratedAgentContextRoutesToHandoff(t *testing.T) {
	service, _, _, config := newTaskCreationLockTestServices(t)
	handoff := "# 任务交接\n\n## 目标\n\n保留已验证事实。\n"
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{
		FolderName: "agent-task", AgentContext: &domain.AgentTaskContext{ProtocolVersion: "2", DocumentationDirectory: "docs", HandoffRelativePath: ".workspace/HANDOFF.md"}, AgentHandoffMarkdown: &handoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName)
	handoffPath := filepath.Join(taskPath, AgentWorkspaceDir, HandoffFileName)
	original, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentRulesFileName)
	userRules := []byte("# user rules\r\nPreserve trailing spaces  \r\n")
	if err := os.WriteFile(rules, userRules, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Documents.EnsureTaskDocuments(taskPath, manifest, config); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	contextBytes, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatal(err)
	}
	contextText := string(contextBytes)
	for _, required := range []string{".workspace/HANDOFF.md", "在阅读、修改或执行任何任务操作前，先阅读", "目标、范围、已验证事实、风险与下一步的权威交接记录", "缺失、损坏、过期", "停止执行会产生副作用的操作"} {
		if !strings.Contains(contextText, required) {
			t.Fatalf("generated context omitted handoff contract %q: %s", required, contextText)
		}
	}
	router, err := os.ReadFile(filepath.Join(taskPath, AgentRouterFileName))
	if err != nil || !strings.Contains(string(router), ".workspace/agent/TASK-CONTEXT.md") {
		t.Fatalf("root router did not lead to generated context: %q, %v", router, err)
	}
	for _, retired := range []string{"Worktree", "Genbu", "Tag"} {
		if strings.Contains(contextText, retired) {
			t.Fatalf("generated context restored retired development capability %q", retired)
		}
	}
	releaseReviewAssertFile(t, handoffPath, original)
	releaseReviewAssertFile(t, rules, userRules)
	// Rendering retains the historical default-path fallback without relaxing
	// the stricter persisted AgentContext validation used above.
	manifest.AgentContext.HandoffRelativePath = ""
	preview, err := service.Documents.RenderPreviewChecked(taskPath, manifest, config, string(userRules))
	if err != nil {
		t.Fatal(err)
	}
	foundDefault := false
	for _, file := range preview.Files {
		if filepath.Base(file.RelativePath) == AgentContextFileName && strings.Contains(file.Content, ".workspace/HANDOFF.md") {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Fatal("document preview omitted the default handoff path")
	}
	manifest.AgentContext = nil
	if _, err := service.Documents.EnsureTaskDocuments(taskPath, manifest, config); err != nil {
		t.Fatal(err)
	}
	ordinary, err := os.ReadFile(contextPath)
	if err != nil || strings.Contains(string(ordinary), "权威交接记录") {
		t.Fatalf("ordinary task acquired a mandatory Agent handoff: %q, %v", ordinary, err)
	}
}

type releaseReviewOfflinePreparationRunner struct{ calls int }

func (r *releaseReviewOfflinePreparationRunner) Run(context.Context, process.Spec) (process.Result, error) {
	r.calls++
	return process.Result{}, errors.New("this regression must not run a CLI or network request")
}

func TestReleaseReviewMaterialsUseCapturedYearInitiallyAndOnRetry(t *testing.T) {
	for _, softFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "soft failure"}[softFailure], func(t *testing.T) {
			service, _, paths, config := newTaskCreationLockTestServices(t)
			captured := time.Date(2024, 12, 31, 23, 59, 59, 0, time.Local)
			service.Now = func() time.Time { return captured }
			materialsRoot, subdirectory := t.TempDir(), "materials"
			config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = &materialsRoot, &subdirectory
			runner := &releaseReviewOfflinePreparationRunner{}
			preparation := documents.NewPreparationService(paths, requirementprovider.NewMeegle(runner))
			var plans []documents.DocumentationPlan
			service.RequirementMaterials = func(ctx context.Context, cfg domain.AppConfig, link, folder string) (domain.RequirementMaterialsDirectory, *string, error) {
				plan, err := preparation.Plan(ctx, cfg, link, "年度资料需求", folder)
				if err != nil {
					return domain.RequirementMaterialsDirectory{}, nil, err
				}
				plans = append(plans, plan)
				if softFailure && len(plans) == 1 {
					reason := "offline materials preparation failed before directory creation"
					return domain.RequirementMaterialsDirectory{Status: domain.MaterialsFailed, FailureReason: &reason}, nil, nil
				}
				// An existing requirement directory makes Ensure use its local path.
				// The runner above rejects any accidental CLI or network fallback.
				if err := os.MkdirAll(plan.RequirementDirectory, 0755); err != nil {
					return domain.RequirementMaterialsDirectory{}, nil, err
				}
				materials, err := preparation.Ensure(ctx, cfg, link, folder)
				return materials, nil, err
			}
			manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "year-task", RequirementLink: "https://project.feishu.cn/obt/userstory/detail/123"})
			if err != nil {
				t.Fatal(err)
			}
			if manifest.CreatedAt != domain.FormatSilverWingTime(captured) || len(plans) != 1 || !strings.Contains(plans[0].RequirementDirectory, filepath.Join("2024", "123-year-task")) {
				t.Fatalf("initial manifest and materials used different years: %+v, plans %+v", manifest, plans)
			}
			if softFailure && manifest.RequirementMaterials.Status != domain.MaterialsFailed || !softFailure && manifest.RequirementMaterials.Status != domain.MaterialsReady {
				t.Fatalf("initial preparation status changed: %+v", manifest.RequirementMaterials)
			}
			service.Now = func() time.Time { return captured.Add(2 * time.Second) }
			updated, err := service.RetryRequirementMaterials(context.Background(), config, filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName))
			if err != nil {
				t.Fatal(err)
			}
			if updated.CreatedAt != manifest.CreatedAt || updated.RequirementMaterials.Status != domain.MaterialsReady || len(plans) != 2 || plans[1].RequirementDirectory != plans[0].RequirementDirectory {
				t.Fatalf("retry crossed the captured year or lost status: %+v, plans %+v", updated, plans)
			}
			if runner.calls != 0 {
				t.Fatalf("offline regression ran a CLI %d times", runner.calls)
			}
			if _, err := os.Stat(filepath.Join(materialsRoot, "2025")); !os.IsNotExist(err) {
				t.Fatalf("retry created the next year's materials directory: %v", err)
			}
		})
	}
}
