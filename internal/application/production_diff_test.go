package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type productionDiffGenbu struct{}

func (productionDiffGenbu) Versions(context.Context, domain.AppConfig, string) ([]string, error) {
	return []string{"1.0.1"}, nil
}

func (productionDiffGenbu) Status(context.Context, domain.AppConfig, string, string) (domain.GenbuTagProbeStatus, error) {
	return domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}, nil
}

// Exercise the actual Action JSON boundary and Git reader, rather than a mock
// Dispatch that would accept a partial Preview's invalid zero-valued statuses.
func TestProductionDiffActionsReadOnlyAcrossPayloads(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "Synthetic")
	t.Setenv("GIT_AUTHOR_EMAIL", "synthetic@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Synthetic")
	t.Setenv("GIT_COMMITTER_EMAIL", "synthetic@example.invalid")
	git := workspace.NewGitClient(nil, "git")
	run := func(dir string, args ...string) string {
		t.Helper()
		result, err := git.RunContext(context.Background(), dir, time.Minute, true, false, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(result.Stdout)
	}
	repo, remote := filepath.Join(root, "repo space"), filepath.Join(root, "remote.git")
	run(root, "init", "--bare", "--initial-branch=master", remote)
	run(root, "init", "--initial-branch=master", repo)
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("service.go", "package service\nconst Version = 1\n")
	run(repo, "add", ".")
	run(repo, "commit", "-m", "base")
	base := run(repo, "rev-parse", "HEAD")
	run(repo, "checkout", "-b", "production-baseline")
	write("service.go", "package service\nconst Version = 2\n")
	run(repo, "commit", "-am", "production change")
	source := run(repo, "rev-parse", "HEAD")
	run(repo, "tag", "-a", "1.0.1", "-m", "production baseline")
	run(repo, "checkout", "master")
	write("main.txt", "retained main change\n")
	run(repo, "add", ".")
	run(repo, "commit", "-m", "retained main change")
	target := run(repo, "rev-parse", "HEAD")
	run(repo, "remote", "add", "origin", remote)
	run(repo, "push", "origin", "master", "--tags")
	paths := storage.NewPaths(filepath.Join(root, "app home"))
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Runner: process.NewRunner()}
	t.Cleanup(func() { _ = a.CloseWorkspaceDiffSessions(context.Background()) })
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "synthetic", RootPath: repo, GitCommonDirectory: filepath.Join(repo, ".git")}}
	config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "synthetic", Enabled: true, MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", GenbuServiceName: "synthetic", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}}}}
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	service, err := a.productionService()
	if err != nil {
		t.Fatal(err)
	}
	service.Genbu = productionDiffGenbu{}
	preview, err := service.Inspect(context.Background(), production.Selection{GroupID: config.Groups[0].ID, ServiceID: "service"}, "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	beforeRefs := run(repo, "show-ref")
	for _, legacy := range []bool{false, true} {
		for _, wire := range []bool{false, true} {
			for _, incoming := range []bool{false, true} {
				name := strings.Join([]string{map[bool]string{false: "target", true: "legacy-preview"}[legacy], map[bool]string{false: "typed", true: "json"}[wire], map[bool]string{false: "exact", true: "incoming"}[incoming]}, "/")
				t.Run(name, func(t *testing.T) {
					payload := map[string]any{"sourceSha": source, "targetSha": target, "incoming": incoming, "retainSession": true}
					if legacy {
						payload["preview"] = production.Preview{Target: preview.Target}
					} else {
						payload["target"] = preview.Target
					}
					dispatch := func(action Action) production.DiffResult {
						t.Helper()
						if wire {
							data, err := json.Marshal(action)
							if err != nil {
								t.Fatal(err)
							}
							if err := json.Unmarshal(data, &action); err != nil {
								t.Fatal(err)
							}
						}
						result, err := a.Dispatch(context.Background(), action)
						if err != nil {
							t.Fatal(err)
						}
						return result.Data.(production.DiffResult)
					}
					files := dispatch(Action{Kind: "production.diff-files", Payload: payload})
					wantBase, count := target, 2
					if incoming {
						wantBase, count = base, 1
					}
					if files.Files.BaseCommit != wantBase || files.Files.TargetCommit != source || len(files.Files.Files) != count || !strings.Contains(files.Commits, "production change") {
						t.Fatalf("incorrect immutable comparison: %+v", files)
					}
					payload["fileKind"] = string(domain.WorkspaceGitModified)
					payload["sessionId"] = files.Files.SessionID
					if files.Files.SessionID == "" {
						t.Fatal("production diff did not retain an immutable session")
					}
					file := dispatch(Action{Kind: "production.diff-preview", Path: "service.go", Payload: payload})
					if file.Preview == nil || file.Preview.Comparison == nil || file.Preview.Comparison.OldContent == nil || file.Preview.Comparison.NewContent == nil || !strings.Contains(file.Preview.Comparison.OldContent.Text, "Version = 1") || !strings.Contains(file.Preview.Comparison.NewContent.Text, "Version = 2") {
						t.Fatalf("missing shared file comparison: %+v", file.Preview)
					}
					if len(file.Files.Files) != 0 || file.Files.SessionID != files.Files.SessionID {
						t.Fatal("production preview repeated the file list or changed session identity")
					}
					if _, err := a.Dispatch(context.Background(), Action{Kind: workspace.CompareSessionCloseAction, Payload: map[string]any{"sessionId": files.Files.SessionID}}); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
	t.Run("legacy-one-shot-does-not-retain-session", func(t *testing.T) {
		result, err := a.Dispatch(context.Background(), Action{Kind: "production.diff-files", Payload: map[string]any{"target": preview.Target, "sourceSha": source, "targetSha": target}})
		if err != nil {
			t.Fatal(err)
		}
		data := result.Data.(production.DiffResult)
		if data.Files.SessionID != "" || len(data.Files.Files) != 2 {
			t.Fatal("legacy read retained an unowned session or lost the full list")
		}
	})
	t.Run("guards", func(t *testing.T) {
		changed := preview.Target
		changed.Remote = "another-remote"
		for _, action := range []Action{
			{Kind: "production.diff-files"},
			{Kind: "production.diff-files", Payload: map[string]any{"target": "invalid-target"}},
			{Kind: "production.diff-files", Payload: map[string]any{"target": changed, "sourceSha": source, "targetSha": target}},
			{Kind: "production.diff-files", Payload: map[string]any{"target": preview.Target, "sourceSha": "master", "targetSha": target}},
			{Kind: "production.diff-preview", Path: "../outside", Payload: map[string]any{"target": preview.Target, "sourceSha": source, "targetSha": target}},
		} {
			if _, err := a.Dispatch(context.Background(), action); err == nil {
				t.Fatalf("invalid diff scope accepted: %+v", action)
			}
		}
	})
	t.Run("refresh-build-reads-only-one-batch", func(t *testing.T) {
		store := production.Store{Directory: filepath.Join(paths.Home, "production-releases")}
		batch := production.Batch{ID: strings.Repeat("a", 32), Target: preview.Target}
		if err := store.Save(&batch); err != nil {
			t.Fatal(err)
		}
		corrupt := filepath.Join(store.Directory, strings.Repeat("b", 32)+".json")
		if err := os.WriteFile(corrupt, []byte("{corrupt-unrelated-batch"), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := a.Dispatch(context.Background(), Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": batch.ID}})
		if err != nil {
			t.Fatalf("single batch refresh scanned unrelated history: %v", err)
		}
		report := result.Data.(production.Report)
		if report.Batch == nil || report.Batch.ID != batch.ID || len(report.Batches) != 0 {
			t.Fatalf("single-batch refresh shape: %+v", report)
		}
		if _, err := a.Dispatch(context.Background(), Action{Kind: "production.list"}); err == nil {
			t.Fatal("explicit full-list read stopped surfacing corrupt history")
		}
		if err := os.RemoveAll(store.Directory); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("config-changed", func(t *testing.T) {
		_, err := a.Config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
			c.Groups[0].Services[0].MasterBranch = "origin/another-main"
			return c, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.Dispatch(context.Background(), Action{Kind: "production.diff-files", Payload: map[string]any{"target": preview.Target, "sourceSha": source, "targetSha": target}})
		if err == nil || !strings.Contains(err.Error(), "生产发布目标已变化") {
			t.Fatalf("accepted stale confirmed target: %v", err)
		}
	})
	if afterRefs := run(repo, "show-ref"); afterRefs != beforeRefs || run(repo, "rev-parse", "HEAD") != target || run(repo, "status", "--porcelain") != "" {
		t.Fatal("difference Action changed repository refs, checkout or working tree")
	}
	if _, err := os.Stat(filepath.Join(paths.Home, "production-releases")); !os.IsNotExist(err) {
		t.Fatalf("difference read created production history: %v", err)
	}
}
