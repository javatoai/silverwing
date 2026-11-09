package delivery

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func newTagRecoveryTestRepository(t *testing.T) (*Service, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is unavailable")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "empty-gitconfig"))
	repository := filepath.Join(root, "source")
	if err := os.Mkdir(repository, 0700); err != nil {
		t.Fatal(err)
	}
	service := &Service{git: workspace.NewGitClient(nil, git), paths: storage.NewPaths(filepath.Join(root, "home"))}
	tagRecoveryGit(t, service, repository, "init", "--initial-branch=main")
	tagRecoveryGit(t, service, repository, "config", "user.name", "Tag test")
	tagRecoveryGit(t, service, repository, "config", "user.email", "tag-test@example.invalid")
	return service, repository
}

func tagRecoveryGit(t *testing.T, service *Service, repository string, args ...string) string {
	t.Helper()
	result, err := service.run(context.Background(), repository, 15*time.Second, true, false, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(result.Stdout)
}

func TestTagWriteEndpointUsesExactEffectivePushURL(t *testing.T) {
	ctx := context.Background()
	service, repository := newTagRecoveryTestRepository(t)
	fetch := "ssh://git@example.invalid:2222/Team/Repo.git"
	tagRecoveryGit(t, service, repository, "remote", "add", "origin", fetch)
	_, identity, err := service.tagRemoteIdentity(ctx, repository, "origin")
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(fetch+"\x00"+fetch)))
	if err != nil || identity != expected {
		t.Fatalf("legacy identity changed: %s %v", identity, err)
	}
	for _, push := range []string{
		"ssh://git@example.invalid:2223/Team/Repo.git",
		"ssh://git@example.invalid:2222/team/Repo.git",
		"https://user:fixture-secret@example.invalid/Team/Repo.git",
	} {
		tagRecoveryGit(t, service, repository, "config", "remote.origin.pushurl", push)
		if _, _, err := service.tagRemoteIdentity(ctx, repository, "origin"); err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("different endpoint accepted or its credentials were exposed: %v", err)
		}
	}
	tagRecoveryGit(t, service, repository, "config", "remote.origin.pushurl", fetch)
	tagRecoveryGit(t, service, repository, "config", "--add", "remote.origin.pushurl", fetch)
	if _, _, err := service.tagRemoteIdentity(ctx, repository, "origin"); err == nil {
		t.Fatal("multiple effective push addresses accepted")
	}
	tagRecoveryGit(t, service, repository, "config", "--unset-all", "remote.origin.pushurl")
	tagRecoveryGit(t, service, repository, "config", "url.ssh://git@example.invalid:2223/.pushInsteadOf", "ssh://git@example.invalid:2222/")
	if _, _, err := service.tagRemoteIdentity(ctx, repository, "origin"); err == nil {
		t.Fatal("pushInsteadOf redirected the effective write endpoint")
	}
}

func TestTagWriteEndpointGroupsHTTPHostnameAndDefaultPortOnly(t *testing.T) {
	ctx := context.Background()
	service, repository := newTagRecoveryTestRepository(t)
	first := "https://user@example.invalid/Team/%2FRepo.git?mode=full"
	second := "https://user@EXAMPLE.INVALID:443/Team/%2FRepo.git?mode=full"
	tagRecoveryGit(t, service, repository, "remote", "add", "origin", first)
	tagRecoveryGit(t, service, repository, "remote", "add", "other", second)
	primary, err := service.readTagWriteEndpoint(ctx, repository, "origin")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := service.readTagWriteEndpoint(ctx, repository, "other")
	if err != nil || alias.key != primary.key || alias.identity() == primary.identity() {
		t.Fatalf("HTTP aliases should share sequencing and have distinct raw identities: %v", err)
	}
	tagRecoveryGit(t, service, repository, "config", "remote.origin.pushurl", second)
	if _, err := service.readTagWriteEndpoint(ctx, repository, "origin"); err == nil {
		t.Fatal("HTTP sequencing equivalence authorized a different effective write address")
	}
	for _, other := range []string{
		"https://user@example.invalid:444/Team/%2FRepo.git?mode=full",
		"https://user@example.invalid/team/%2FRepo.git?mode=full",
		"https://user@example.invalid/Team/%2fRepo.git?mode=full",
		"https://user@example.invalid/Team/%2FRepo.git?mode=other",
	} {
		if canonicalTagBatchURL(other) == canonicalTagBatchURL(first) {
			t.Errorf("distinct address grouped with primary: %s", other)
		}
	}
}

func TestTagWriteEndpointRejectsRetargetedLocalAlias(t *testing.T) {
	ctx := context.Background()
	service, repository := newTagRecoveryTestRepository(t)
	root := filepath.Dir(repository)
	first, second := filepath.Join(root, "first.git"), filepath.Join(root, "second.git")
	link, alternate := filepath.Join(root, "linked"), filepath.Join(root, "alternate")
	for _, directory := range []string{link, alternate} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	tagRecoveryGit(t, service, link, "init", "--separate-git-dir="+first)
	tagRecoveryGit(t, service, alternate, "init", "--separate-git-dir="+second)
	tagRecoveryGit(t, service, repository, "remote", "add", "origin", "../linked")
	pinned, err := service.readTagWriteEndpoint(ctx, repository, "origin")
	if err != nil {
		t.Fatal(err)
	}
	ctx = withTagRemoteIdentity(ctx, pinned.identity(), pinned.key)
	if err := os.WriteFile(filepath.Join(link, ".git"), []byte("gitdir: "+filepath.ToSlash(second)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := service.readTagWriteEndpoint(ctx, repository, "origin")
	if err != nil || actual.identity() != pinned.identity() || actual.key == pinned.key {
		t.Fatalf("retargeting did not change the local key independently of the raw identity: %v", err)
	}
	if err := service.requireTagRemoteIdentity(ctx, repository, "origin", pinned.identity()); err == nil {
		t.Fatal("per-write check accepted a retargeted local remote")
	}
}

func TestTagWriteEndpointPinsLocalBranchWithoutOverwritingDrift(t *testing.T) {
	ctx := context.Background()
	service, repository := newTagRecoveryTestRepository(t)
	upstream := filepath.Join(filepath.Dir(repository), "upstream.git")
	if err := os.Mkdir(upstream, 0700); err != nil {
		t.Fatal(err)
	}
	tagRecoveryGit(t, service, upstream, "init", "--bare")
	tagRecoveryGit(t, service, repository, "remote", "add", "origin", "../upstream.git")
	tagRecoveryGit(t, service, repository, "config", "remote.origin.pushurl", filepath.ToSlash(upstream))
	tagRecoveryGit(t, service, repository, "remote", "add", "other-alias", filepath.ToSlash(upstream))
	_, identity, err := service.tagRemoteIdentity(ctx, repository, "origin")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.tagWriteEndpointKey(ctx, repository, "origin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.tagWriteEndpointKey(ctx, repository, "other-alias")
	if err != nil || first == "" || first != second {
		t.Fatalf("aliases of the same local repository did not share an endpoint key: %q %q %v", first, second, err)
	}
	tagRecoveryGit(t, service, repository, "commit", "--allow-empty", "-m", "base")
	base := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	tagRecoveryGit(t, service, repository, "commit", "--allow-empty", "-m", "next")
	next := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	tagRecoveryGit(t, service, repository, "commit", "--allow-empty", "-m", "later")
	later := tagRecoveryGit(t, service, repository, "rev-parse", "HEAD")
	ref := "refs/heads/feature"
	if err := service.pushPinnedTagBranch(ctx, repository, "origin", identity, ref, "", base); err != nil {
		t.Fatal(err)
	}
	if err := service.pushPinnedTagBranch(ctx, repository, "origin", identity, ref, base, next); err != nil {
		t.Fatal(err)
	}
	if err := service.pushPinnedTagBranch(ctx, repository, "origin", identity, ref, base, later); err == nil {
		t.Fatal("write succeeded after the approved old value drifted")
	}
	if err := service.pushPinnedTagBranch(ctx, repository, "origin", identity, ref, next, base); err == nil {
		t.Fatal("lease permitted a non-fast-forward write")
	}
	if got := tagRecoveryGit(t, service, upstream, "rev-parse", ref); got != next {
		t.Fatalf("unexpected remote branch: got %s, want %s", got, next)
	}
}
