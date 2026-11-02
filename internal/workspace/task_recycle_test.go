package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type fixtureDirectoryRecycler struct {
	base, bin string
	fail      string
	calls     []string
}

func (r *fixtureDirectoryRecycler) Recycle(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isWithin(path, r.base) || !isWithin(r.bin, r.base) {
		return errors.New("fixture path outside sandbox")
	}
	if path == r.fail {
		return errors.New("fixture recycle failure")
	}
	r.calls = append(r.calls, path)
	if err := os.MkdirAll(r.bin, 0700); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(r.bin, filepath.Base(path)))
}

type taskRecycleFixture struct {
	base, task, material string
	service              *TaskApplicationService
	config               domain.AppConfig
	recycler             *fixtureDirectoryRecycler
}

func newTaskRecycleFixture(t *testing.T) *taskRecycleFixture {
	t.Helper()
	base := t.TempDir()
	root, data := filepath.Join(base, "tasks"), filepath.Join(base, "资料")
	config := domain.DefaultAppConfig()
	config.TaskRoot = &root
	service := NewTaskApplicationService(nil, nil, storage.NewPaths(filepath.Join(base, "profile")))
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "task"})
	if err != nil {
		t.Fatal(err)
	}
	config.RequirementMaterialsRoot = &data
	sub := "研发"
	config.RequirementMaterialsSubdirectory = &sub
	material := filepath.Join(data, "requirements", "2026", "7132215081-需求")
	if err := os.MkdirAll(filepath.Join(material, sub), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"temp", "产品", "测试"} {
		if err := os.MkdirAll(filepath.Join(material, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(material, "产品", ".hidden.md"), []byte("keep all teams"), 0600); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(material, sub)
	manifest.RequirementLink = "https://project.feishu.cn/obt/technical/detail/7132215081"
	id := "7132215081"
	manifest.RequirementID = &id
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &owned}
	task := filepath.Join(root, "task")
	if err := service.Manifests.Save(task, manifest); err != nil {
		t.Fatal(err)
	}
	recycler := &fixtureDirectoryRecycler{base: base, bin: filepath.Join(base, "recycle-bin")}
	service.Lifecycle.Recycler = recycler
	return &taskRecycleFixture{base: base, task: task, material: material, service: service, config: config, recycler: recycler}
}

func TestTaskRecycleOptionsAndWholeMaterials(t *testing.T) {
	for _, deleteMaterials := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep-materials", true: "recycle-materials"}[deleteMaterials], func(t *testing.T) {
			f := newTaskRecycleFixture(t)
			preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
			if err != nil || !preview.Materials.CanDelete {
				t.Fatalf("preview: %+v %v", preview, err)
			}
			err = f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{DeleteMaterials: deleteMaterials, MaterialsGuard: preview.Materials.Guard})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(f.task); !os.IsNotExist(err) {
				t.Fatal("task not recycled")
			}
			expected := f.material
			if deleteMaterials {
				expected = filepath.Join(f.recycler.bin, filepath.Base(f.material))
			}
			if got, err := os.ReadFile(filepath.Join(expected, "产品", ".hidden.md")); err != nil || string(got) != "keep all teams" {
				t.Fatalf("materials lost: %q %v", got, err)
			}
			if got := f.recycler.calls[len(f.recycler.calls)-1]; got != f.task {
				t.Fatalf("task was not recycled last: %v", f.recycler.calls)
			}
		})
	}
}

func TestTaskRecycleSharedArchivedAndUnreadableManifests(t *testing.T) {
	for _, variant := range []string{"active", "archived", "unreadable"} {
		t.Run(variant, func(t *testing.T) {
			f := newTaskRecycleFixture(t)
			old, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
			if err != nil || !old.Materials.CanDelete {
				t.Fatalf("preview: %+v %v", old, err)
			}
			other := filepath.Join(*f.config.TaskRoot, "other")
			if err := os.MkdirAll(other, 0700); err != nil {
				t.Fatal(err)
			}
			if variant == "unreadable" {
				if err := os.WriteFile(filepath.Join(other, "silverwing.json"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				manifest, err := f.service.Manifests.Load(f.task)
				if err != nil {
					t.Fatal(err)
				}
				manifest.FolderName, manifest.TaskDirectoryName = "other", "other"
				if variant == "archived" {
					manifest.LifecycleStatus = domain.TaskArchived
				}
				if err := f.service.Manifests.Save(other, manifest); err != nil {
					t.Fatal(err)
				}
			}
			err = f.service.DeleteWithOptions(context.Background(), f.config, f.task, TaskDeleteOptions{DeleteMaterials: true, MaterialsGuard: old.Materials.Guard})
			if err == nil || len(f.recycler.calls) != 0 {
				t.Fatalf("unsafe shared deletion: %v %+v", err, f.recycler.calls)
			}
			if err := f.service.Delete(context.Background(), f.config, f.task, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(f.material); err != nil {
				t.Fatal("shared materials removed")
			}
		})
	}
}

func TestTaskRecycleReplacementCancellationAndPartialFailure(t *testing.T) {
	for _, variant := range []string{"replacement", "cancelled", "materials-failure", "task-failure", "missing-guard"} {
		t.Run(variant, func(t *testing.T) {
			f := newTaskRecycleFixture(t)
			preview, err := f.service.InspectDeletion(context.Background(), f.config, f.task)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			guard := preview.Materials.Guard
			switch variant {
			case "replacement":
				if err := os.Rename(f.material, filepath.Join(f.base, "original-materials")); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(f.material, "研发"), 0700); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "materials-failure":
				f.recycler.fail = f.material
			case "task-failure":
				f.recycler.fail = f.task
			case "missing-guard":
				guard = ""
			}
			err = f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{DeleteMaterials: true, MaterialsGuard: guard})
			if err == nil {
				t.Fatal("expected a safe failure")
			}
			if _, err := os.Stat(f.task); err != nil {
				t.Fatal("task removed on failed operation")
			}
			if variant == "task-failure" {
				if !strings.Contains(err.Error(), "资料已移入回收站") || len(f.recycler.calls) != 1 {
					t.Fatalf("partial result missing: %v %v", err, f.recycler.calls)
				}
			} else if len(f.recycler.calls) != 0 {
				t.Fatalf("unexpected recycle: %v", f.recycler.calls)
			}
		})
	}
}

func TestTaskRecycleKeepsWorktreeAndCloneFiles(t *testing.T) {
	for _, strategy := range []domain.WorkspaceStrategy{domain.WorkspaceStandardWorktree, domain.WorkspaceIndependentClone} {
		t.Run(string(strategy), func(t *testing.T) {
			f := newTaskRecycleFixture(t)
			repo, remote := filepath.Join(f.base, "repo"), filepath.Join(f.base, "remote.git")
			if err := os.MkdirAll(repo, 0700); err != nil {
				t.Fatal(err)
			}
			gitCompare(t, f.base, "init", "--bare", remote)
			gitCompare(t, repo, "init", "-b", "main")
			gitCompare(t, repo, "config", "user.name", "Recycle Fixture")
			gitCompare(t, repo, "config", "user.email", "recycle@example.invalid")
			writeCompareFile(t, repo, "base.txt", "original\n")
			commitCompare(t, repo, "base")
			gitCompare(t, repo, "remote", "add", "origin", remote)
			gitCompare(t, repo, "push", "-u", "origin", "main")
			remoteBefore := gitCompareOutput(t, remote, "show-ref")
			f.config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "fixture", RootPath: repo, GitCommonDirectory: filepath.Join(repo, ".git"), OriginURL: &remote}}
			f.config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "one", RepositoryID: "repo", DisplayName: "one", Enabled: true, MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "module", Strategy: strategy, CustomCommands: []domain.WorkspaceCommandConfig{}}}}}
			manifest, err := f.service.AddServices(context.Background(), f.config, f.task, AddWorkspaceRequest{ServiceIDs: []string{"one"}, ServiceSelections: []TaskServiceSelection{{ServiceID: "one", Modules: []TaskModuleSelection{{ID: "module", Name: "module", Strategy: strategy, BaseRef: "origin/main", BaseRemote: "origin", TargetBranch: "feature/recycle", Source: domain.TaskModuleConfigured, TagMode: domain.TagBuildMergeToTargetBranch}}}}})
			if err != nil {
				t.Fatal(err)
			}
			ws := manifest.Services[0]
			writeCompareFile(t, ws.WorktreePath, "base.txt", "keep modified file\n")
			writeCompareFile(t, ws.WorktreePath, "untracked.txt", "keep untracked file\n")
			if err := f.service.Delete(context.Background(), f.config, f.task, true); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(f.task, ws.WorktreePath)
			if err != nil {
				t.Fatal(err)
			}
			preserved := filepath.Join(f.recycler.bin, filepath.Base(f.task), rel)
			for file, want := range map[string]string{"base.txt": "keep modified file\n", "untracked.txt": "keep untracked file\n"} {
				if got, err := os.ReadFile(filepath.Join(preserved, file)); err != nil || string(got) != want {
					t.Fatalf("workspace bytes lost: %s %q %v", file, got, err)
				}
			}
			if strategy == domain.WorkspaceIndependentClone {
				if info, err := os.Stat(filepath.Join(preserved, ".git")); err != nil || !info.IsDir() {
					t.Fatalf("clone metadata lost: %v", err)
				}
			} else {
				if records := gitCompareOutput(t, repo, "worktree", "list", "--porcelain"); strings.Contains(records, filepath.ToSlash(ws.WorktreePath)) {
					t.Fatal("recycled worktree still registered")
				}
				gitCompare(t, repo, "show-ref", "--verify", "refs/heads/feature/recycle")
			}
			if got := gitCompareOutput(t, remote, "show-ref"); got != remoteBefore {
				t.Fatal("deletion changed remote branches")
			}
		})
	}
}
