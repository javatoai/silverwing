package application

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func annualLocalGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "missing"), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=SW Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=SW Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, result, err)
	}
	return strings.TrimSpace(string(result))
}

func TestAnnualGitAppliedTreeUpdatesAllAgentsWhenPushFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	a, scope, taskPath := annualApplicationFixture(t)
	c, _ := a.Config.Load()
	// A validated UI address is used only as a binding. All Git commands in
	// this test use the isolated local bare repository below.
	c.MaterialsGitSync = &domain.MaterialsGitSyncSettings{RepositoryURL: "https://example.invalid/materials.git", Branch: "master", IntervalMinutes: 5}
	if err := a.Config.Save(c); err != nil {
		t.Fatal(err)
	}
	expected := MaterialsGitScope(a.Paths.Home, c)
	archived, _ := a.Manifests.Load(taskPath)
	archived.TaskDirectoryName = "archived"
	archived.FolderName = "archived"
	archived.LifecycleStatus = domain.TaskArchived
	archivedPath := filepath.Join(*c.TaskRoot, "archived")
	if err := a.Manifests.Save(archivedPath, archived); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.NewAgentDocumentService(a.Paths, a.Manifests).EnsureTaskDocuments(archivedPath, archived, c); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	bare, seed := filepath.Join(base, "remote.git"), filepath.Join(base, "peer-clone")
	for _, path := range []string{bare, seed} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	annualLocalGit(t, bare, "init", "--bare", "--initial-branch=master")
	annualLocalGit(t, seed, "init", "--initial-branch=master")
	write := func(root, path, content string) {
		t.Helper()
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(seed, "2026/123-需求/测试/case.md", "peer")
	write(seed, "2026/123-需求/研发/remote.md", "remote")
	annualLocalGit(t, seed, "add", ".")
	annualLocalGit(t, seed, "commit", "-m", "seed")
	annualLocalGit(t, seed, "remote", "add", "origin", bare)
	annualLocalGit(t, seed, "push", "origin", "master")
	service, err := a.materialsGitService(expected)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(base, "missing"), "GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "SW Test", "GIT_CONFIG_KEY_1": "user.email", "GIT_CONFIG_VALUE_1": "test@example.invalid"} {
		service.Git.Environment[k] = v
	}
	local := expected
	local.URL = bare
	status, syncErr := service.Sync(context.Background(), local, true, nil)
	if _, err := a.finishMaterialsGitSync(context.Background(), expected, status, syncErr, service.Check); err != nil {
		t.Fatal(err)
	}
	annualLocalGit(t, seed, "mv", "2026/123-需求/测试", "2026/123-需求/验证")
	write(seed, "2026/123-需求/产品/design.md", "peer design")
	annualLocalGit(t, seed, "add", ".")
	annualLocalGit(t, seed, "commit", "-m", "peer rename and add")
	annualLocalGit(t, seed, "push", "origin", "master")
	write(scope.Base, "2026/123-需求/研发/local.txt", "owned change")
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho local-test-reject >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	status, syncErr = service.Sync(context.Background(), local, true, nil)
	if syncErr == nil {
		t.Fatal("push rejection did not happen")
	}
	rootAgent := filepath.Join(taskPath, "AGENTS.md")
	saved, _ := os.ReadFile(rootAgent)
	if err := os.WriteFile(rootAgent, []byte("custom preserved"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = a.finishMaterialsGitSync(context.Background(), expected, status, syncErr, service.Check)
	if err == nil || !strings.Contains(err.Error(), "本地资料目录说明") {
		t.Fatal(err)
	}
	// Other tasks still update when one task's custom file cannot be managed.
	data, _ := os.ReadFile(filepath.Join(archivedPath, "AGENTS.md"))
	for _, name := range []string{"验证", "产品"} {
		if !strings.Contains(string(data), filepath.Join(scope.Root, name)) {
			t.Fatal(string(data))
		}
	}
	if strings.Contains(string(data), "`"+filepath.Join(scope.Root, "测试")+"`") {
		t.Fatal("removed peer remained")
	}
	head := annualLocalGit(t, scope.Base, "rev-parse", "HEAD")
	remote := annualLocalGit(t, bare, "rev-parse", "master")
	if err := os.WriteFile(rootAgent, saved, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.documentAction(context.Background(), Action{Kind: "document.list", TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(rootAgent)
	if !strings.Contains(string(data), filepath.Join(scope.Root, "验证")) {
		t.Fatal(string(data))
	}
	if annualLocalGit(t, scope.Base, "rev-parse", "HEAD") != head || annualLocalGit(t, bare, "rev-parse", "master") != remote {
		t.Fatal("local retry repeated Git writes")
	}
}
