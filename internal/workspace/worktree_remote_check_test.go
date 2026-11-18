package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func TestGitRemoteCheckDiagnosesSSHConnectionFailure(t *testing.T) {
	for _, row := range []struct {
		stderr, want string
		kind         GitRemoteFailureKind
	}{
		{"Connection closed by 198.18.0.31 port 22\nfatal: Could not read from remote repository.", "端口 22 被关闭", GitRemoteClosed},
		{"ssh: connect to host 198.18.0.31 port 2222: Connection refused", "端口 2222 拒绝连接", GitRemotePortRefused},
		{"git@198.18.0.31: Permission denied (publickey).", "远端认证失败", GitRemoteAuthentication},
		{"Host key verification failed.", "SSH 主机密钥验证失败", GitRemoteHostKey},
	} {
		err := diagnoseGitRemoteError(context.Background(), &GitException{Result: process.Result{Stderr: row.stderr}})
		var failure *GitRemoteFailure
		if !errors.As(err, &failure) || failure.Kind != row.kind || !strings.Contains(err.Error(), row.want) || strings.Contains(err.Error(), "198.18.0.31") {
			t.Fatalf("diagnosis = %v, want kind %v and safe port detail", err, row.kind)
		}
	}
}

func TestWorktreeRemoteCheckUsesSelectedRemoteAndDoesNotFetch(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	git := f.service.Git
	materialsGitTestRun(t, f.seed, "remote", "add", "chosen", f.remote)
	before := materialsGitTestRun(t, f.seed, "show-ref")
	worktrees := materialsGitTestRun(t, f.seed, "worktree", "list", "--porcelain")
	repository, err := NewRepositoryInspector(git).Inspect(f.seed)
	if err != nil {
		t.Fatal(err)
	}
	for branch, kind := range map[string]GitRemoteFailureKind{"master": "", "missing/nested": GitRemoteBranchMissing} {
		base := domain.RemoteBranchRef{Remote: "chosen", Branch: branch}
		err := git.checkWorktreeRemote(context.Background(), repository, base)
		if kind == "" && err != nil {
			t.Fatalf("base %v: %v", base, err)
		}
		var failure *GitRemoteFailure
		if kind != "" && (!errors.As(err, &failure) || failure.Kind != kind) {
			t.Fatalf("base %v: %v", base, err)
		}
	}
	if got := materialsGitTestRun(t, f.seed, "show-ref"); got != before || materialsGitTestRun(t, f.seed, "worktree", "list", "--porcelain") != worktrees {
		t.Fatal("connection check changed local references or worktrees")
	}
}

func TestWorktreeBranchFetchPrunesStaleBranchesButKeepsLocalTags(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	const tag = "refs/tags/local-only"
	const staleBranch = "refs/remotes/origin/stale"
	commit := materialsGitTestRun(t, f.seed, "rev-parse", "HEAD")
	materialsGitTestRun(t, f.seed, "update-ref", tag, commit)
	materialsGitTestRun(t, f.seed, "update-ref", staleBranch, commit)
	materialsGitTestRun(t, f.seed, "config", "fetch.pruneTags", "true")
	materialsGitTestRun(t, f.seed, "config", "remote.origin.pruneTags", "true")
	materialsGitTestRun(t, f.seed, "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")

	if err := fetchWorktreeRemote(context.Background(), f.service.Git, f.seed, "origin"); err != nil {
		t.Fatal(err)
	}
	if got := materialsGitTestRun(t, f.seed, "rev-parse", tag); got != commit {
		t.Fatalf("local tag = %s, want %s", got, commit)
	}
	refs := materialsGitTestRun(t, f.seed, "for-each-ref", "--format=%(refname)", staleBranch)
	if refs != "" {
		t.Fatalf("stale branch remained after fetch: %s", refs)
	}
}

func TestWorktreePreflightCancellationStopsFetch(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	started := make(chan struct{}, 1)
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-finished:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(finished)
	git := f.service.Git
	git.Environment["NO_PROXY"], git.Environment["no_proxy"] = "127.0.0.1", "127.0.0.1"
	repository, err := NewRepositoryInspector(git).Inspect(f.seed)
	if err != nil {
		t.Fatal(err)
	}
	materialsGitTestRun(t, f.seed, "remote", "set-url", "origin", server.URL+"/team/docs.git")
	service := domain.GroupServiceConfig{ID: "sample", DisplayName: "sample", MasterBranch: "origin/master",
		Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := NewWorkspaceBranchReuseInspector(git, NewOperationLocks(f.service.Paths.Locks)).Inspect(ctx, repository, service, "feature/sample", nil)
		result <- err
	}()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("preflight returned before mandatory fetch: %v", err)
	case <-time.After(8 * time.Second):
		t.Fatal("preflight did not start fetch")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("preflight error after fetch cancellation = %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("fetch ignored cancellation")
	}
}
