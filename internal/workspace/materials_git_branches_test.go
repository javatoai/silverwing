package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/process"
)

func TestMaterialsGitHTTPSLoginBlocksHTTPRedirect(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	var httpsRequests, httpRequests atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpRequests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpsRequests.Add(1)
		http.Redirect(w, r, plain.URL+r.URL.RequestURI(), http.StatusMovedPermanently)
	}))
	defer secure.Close()
	git := f.service.Git
	git.Environment["GIT_SSL_NO_VERIFY"] = "1" // The loopback test server uses a self-signed certificate.
	git.Environment["NO_PROXY"], git.Environment["no_proxy"] = "127.0.0.1", "127.0.0.1"
	f.scope.URL = secure.URL + "/docs.git"
	if err := f.service.VerifyHTTPSLogin(context.Background(), f.scope); err == nil || httpsRequests.Load() == 0 || httpRequests.Load() != 0 {
		t.Fatalf("HTTPS login redirect: HTTPS requests=%d HTTP requests=%d error=%v", httpsRequests.Load(), httpRequests.Load(), err)
	}
}

func TestMaterialsGitRemoteBranchLookupRetriesHTTPSProjectURLWithGitSuffix(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	f.scope.URL = "https://gitlab.example.invalid/fp/obt-team-docs"
	var specs []process.Spec
	f.service.Git.readCommand = func(_ context.Context, spec process.Spec) (process.Result, error) {
		specs = append(specs, spec)
		if len(specs) == 1 {
			return process.Result{
				ExitCode: 128,
				Stderr:   `fatal: unable to access 'https://gitlab.example.invalid/fp/obt-team-docs/': Protocol "http" disabled (in redirect)`,
			}, nil
		}
		return process.Result{Stdout: "ca93\trefs/heads/master\n"}, nil
	}

	result, endpoint, git, options, err := f.service.readRemoteBranchHeadsAt(context.Background(), f.scope, "", false, time.Second, 4096, "refs/heads/master")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://gitlab.example.invalid/fp/obt-team-docs.git" {
		t.Fatalf("effective endpoint = %q", endpoint)
	}
	if git == nil || !reflect.DeepEqual(options, []string(nil)) {
		t.Fatalf("unexpected transport configuration: git=%v options=%v", git, options)
	}
	if len(specs) != 2 {
		t.Fatalf("Git attempts = %d, want 2", len(specs))
	}
	for i, spec := range specs {
		if spec.Env["GIT_ALLOW_PROTOCOL"] != "https" || spec.Env["GIT_TERMINAL_PROMPT"] != "0" {
			t.Fatalf("attempt %d did not retain HTTPS-only non-interactive transport: %#v", i, spec.Env)
		}
		attemptURL := f.scope.URL
		if i == 1 {
			attemptURL = endpoint
		}
		wantTail := []string{"ls-remote", "--heads", "--", attemptURL, "refs/heads/master"}
		if len(spec.Arguments) < len(wantTail) || !reflect.DeepEqual(spec.Arguments[len(spec.Arguments)-len(wantTail):], wantTail) {
			t.Fatalf("attempt %d arguments = %v, want suffix %v", i, spec.Arguments, wantTail)
		}
	}
	if strings.Contains(strings.Join(specs[1].Arguments, " "), "http://") {
		t.Fatal("fallback allowed an HTTP downgrade")
	}
	if !strings.Contains(result.Stdout, "refs/heads/master") || f.scope.URL != "https://gitlab.example.invalid/fp/obt-team-docs" {
		t.Fatal("branch read changed the configured URL or returned no ref")
	}
}

func TestMaterialsGitRemoteBranchLookupDoesNotRetryForAuthenticationFailure(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	f.scope.URL = "https://gitlab.example.invalid/fp/obt-team-docs"
	var calls atomic.Int32
	f.service.Git.readCommand = func(_ context.Context, _ process.Spec) (process.Result, error) {
		calls.Add(1)
		return process.Result{ExitCode: 128, Stderr: "fatal: Authentication failed"}, nil
	}

	_, err := f.service.readRemoteBranchHeads(context.Background(), f.scope, false, time.Second)
	if calls.Load() != 1 || !GitRemoteNeedsAuthentication(err) {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestMaterialsGitLoginUsesOnlyExplicitCredentialPrompt(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip(err)
	}
	git := NewGitClient(nil, gitPath)
	git.Environment = map[string]string{
		"GIT_CONFIG_GLOBAL": filepath.Join(t.TempDir(), "missing"), "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": "fixture",
		"GIT_ASKPASS": "configured-askpass", "https_proxy": "http://proxy.example.invalid:3128", "GIT_TRACE_CURL": "sensitive-trace",
	}
	ctx := context.Background()
	scope := MaterialsGitScope{URL: "https://git.example.invalid/team/docs.git"}
	var specs []process.Spec
	git.readCommand = func(_ context.Context, spec process.Spec) (process.Result, error) {
		specs = append(specs, spec)
		if len(specs) == 1 {
			return process.Result{ExitCode: 128, Stderr: "fatal: could not read Username for 'https://secret-marker@git.example.invalid': terminal prompts disabled"}, nil
		}
		return process.Result{Stdout: "ca93\trefs/heads/master\n"}, nil
	}
	service := &MaterialsGitSyncService{Git: git}
	if _, err := service.RemoteBranches(ctx, scope); !GitRemoteNeedsAuthentication(err) || strings.Contains(err.Error(), "secret-marker") {
		t.Fatalf("branch error = %v", err)
	}
	if err := service.VerifyHTTPSLogin(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if branches, err := service.RemoteBranches(ctx, scope); err != nil || !reflect.DeepEqual(branches, []string{"master"}) {
		t.Fatalf("branches=%v err=%v", branches, err)
	}
	for index, spec := range specs {
		if spec.Executable != gitPath || !reflect.DeepEqual(spec.Arguments, []string{"--no-optional-locks", "-c", "core.longpaths=true", "ls-remote", "--heads", "--", scope.URL}) ||
			spec.Env["https_proxy"] != git.Environment["https_proxy"] || spec.Env["GIT_TERMINAL_PROMPT"] != "0" ||
			spec.Env["GIT_ALLOW_PROTOCOL"] != "https" || spec.Env["GIT_TRACE_CURL"] != "0" {
			t.Fatalf("wrong Git route on call %d: %+v", index, spec)
		}
		if index == 1 {
			if spec.Env["GCM_INTERACTIVE"] != "auto" || spec.Env["GIT_ASKPASS"] != "configured-askpass" {
				t.Fatal("explicit login did not permit the configured helper UI")
			}
		} else if spec.Env["GCM_INTERACTIVE"] != "never" || spec.Env["GIT_ASKPASS"] != "" {
			t.Fatal("background branch query allowed a credential prompt")
		}
	}
	if git.Environment["GIT_ASKPASS"] != "configured-askpass" || git.Environment["GCM_INTERACTIVE"] != "" {
		t.Fatal("operation mutated the shared Git environment")
	}
	scope.URL = "http://git.example.invalid/team/docs.git"
	if err := service.VerifyHTTPSLogin(ctx, scope); err == nil || len(specs) != 3 {
		t.Fatalf("login accepted insecure address or launched Git: %v", err)
	}
	if _, err := service.HTTPSLoginArguments(ctx, scope); err == nil {
		t.Fatal("terminal command accepted insecure address")
	}
	if upgraded := MaterialsGitHTTPSUpgrade(scope.URL); upgraded != "https://git.example.invalid/team/docs.git" || !materialsGitSameURL(scope.URL, upgraded) || materialsGitSameURL(upgraded, scope.URL) {
		t.Fatalf("unexpected HTTP → HTTPS result %q", upgraded)
	}
}

func TestMaterialsGitRemoteBranchesReadOnlyWithoutLocalRepository(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	materialsGitTestRun(t, f.seed, "branch", "docs/team")
	materialsGitTestRun(t, f.seed, "push", "origin", "docs/team")
	before := materialsGitTestRun(t, f.remote, "show-ref")
	got, err := f.service.RemoteBranches(context.Background(), f.scope)
	if err != nil || !reflect.DeepEqual(got, []string{"docs/team", "master"}) {
		t.Fatalf("branches=%v err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !os.IsNotExist(err) {
		t.Fatal("read initialized repository")
	}
	if _, err := os.Stat(f.service.statePath(f.scope)); !os.IsNotExist(err) {
		t.Fatal("read wrote sync state")
	}
	if materialsGitTestRun(t, f.remote, "show-ref") != before {
		t.Fatal("read changed remote refs")
	}
	f.service.Check = func() error { return ErrMaterialsGitScopeChanged }
	if _, err := f.service.RemoteBranches(context.Background(), f.scope); !errors.Is(err, ErrMaterialsGitScopeChanged) {
		t.Fatal(err)
	}
}

func TestMaterialsGitRemoteBranchesEmptyAndReadFailure(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	if branches, err := f.service.RemoteBranches(context.Background(), f.scope); err != nil || len(branches) != 0 {
		t.Fatalf("%v %v", branches, err)
	}
	f.scope.URL = filepath.Join(t.TempDir(), "absent.git")
	if _, err := f.service.RemoteBranches(context.Background(), f.scope); err == nil {
		t.Fatal("read failure reported as empty branches")
	}
}

func TestMaterialsGitDeletedRemoteBranchDoesNotCommitOrRecreate(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	materialsGitTestFile(t, f.root, "requirements/2026/123-需求/研发/new.md", "unsynced")
	materialsGitTestRun(t, f.remote, "update-ref", "-d", "refs/heads/master")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused {
		t.Fatalf("%+v %v", status, err)
	}
	if materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != head {
		t.Fatal("committed after remote branch deletion")
	}
	if refs := materialsGitTestRun(t, f.remote, "for-each-ref", "refs/heads"); refs != "" {
		t.Fatal("recreated branch")
	}
}

func TestMaterialsGitRemoteBranchDeletedBeforePushStops(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.root, "requirements/2026/123-需求/研发/new.md", "new")
	status, err := f.service.Sync(context.Background(), f.scope, true, func(phase string) {
		if phase == "推送资料" {
			materialsGitTestRun(t, f.remote, "update-ref", "-d", "refs/heads/master")
		}
	})
	if err == nil || !status.Paused {
		t.Fatalf("%+v %v", status, err)
	}
	if refs := materialsGitTestRun(t, f.remote, "for-each-ref", "refs/heads"); strings.TrimSpace(refs) != "" {
		t.Fatal("push recreated deleted branch")
	}
}
