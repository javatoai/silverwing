package workspace

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/process"
)

func TestMaterialsGitSyncUsesConfirmedHTTPSEndpoint(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	materialsGitTestRun(t, f.remote, "config", "http.receivepack", "true")
	backend := &cgi.Handler{
		Path: git,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(f.remote), "GIT_HTTP_EXPORT_ALL=1"},
	}
	var redirects, downgraded atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downgraded.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/remote/") {
			redirects.Add(1)
			http.Redirect(w, r, plain.URL+r.URL.RequestURI(), http.StatusMovedPermanently)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer secure.Close()

	f.service.Git.Environment["GIT_SSL_NO_VERIFY"] = "1" // Only the loopback test certificate is self-signed.
	f.service.Git.Environment["NO_PROXY"], f.service.Git.Environment["no_proxy"] = "127.0.0.1", "127.0.0.1"
	f.scope.URL = secure.URL + "/remote"
	materialsGitTestRun(t, filepath.Dir(f.root), "clone", "--branch", "master", f.remote, f.root)
	materialsGitTestRun(t, f.root, "remote", "set-url", "origin", f.scope.URL)
	materialsGitTestFile(t, f.root, "existing.md", "HTTPS push\n")

	f.sync()
	if redirects.Load() != 1 || downgraded.Load() != 0 {
		t.Fatalf("sync did not pin the confirmed HTTPS endpoint: redirects=%d HTTP requests=%d", redirects.Load(), downgraded.Load())
	}
	if got := materialsGitTestRun(t, f.remote, "show", "master:existing.md"); got != "HTTPS push" {
		t.Fatalf("unexpected remote content: %q", got)
	}
	if got := materialsGitTestRun(t, f.root, "remote", "get-url", "origin"); got != f.scope.URL {
		t.Fatalf("existing origin changed: %q", got)
	}
	state, err := f.service.load(f.scope)
	if err != nil || state.URL != f.scope.URL || state.StatusURL != f.scope.URL {
		t.Fatalf("binding no longer uses the configured address: %+v %v", state, err)
	}

	original := f.scope.URL
	f.scope.URL += ".git"
	if got, err := f.service.Status(f.scope); err != nil || got.LastSuccess == "" || got.Paused {
		t.Fatalf("same repository spelling lost the last sync status: %+v %v", got, err)
	}
	materialsGitTestFile(t, f.root, "existing.md", "HTTPS push after URL spelling edit\n")
	f.sync()
	if got := materialsGitTestRun(t, f.remote, "show", "master:existing.md"); got != "HTTPS push after URL spelling edit" {
		t.Fatalf("unexpected remote content: %q", got)
	}
	if got := materialsGitTestRun(t, f.root, "remote", "get-url", "origin"); got != original {
		t.Fatalf("existing origin changed after URL spelling edit: %q", got)
	}
	if downgraded.Load() != 0 {
		t.Fatalf("sync followed an HTTP downgrade: requests=%d", downgraded.Load())
	}
}

func TestMaterialsGitURLSuffixEditPreservesPausedBinding(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	f.scope.URL = "https://git.example.invalid/team/docs.git"
	f.service.Git.readCommand = func(context.Context, process.Spec) (process.Result, error) {
		t.Error("paused sync attempted a remote read")
		return process.Result{ExitCode: 128}, nil
	}
	previous := strings.TrimSuffix(f.scope.URL, ".git")
	if err := f.service.save(f.scope, materialsGitBinding{
		MaterialsGitSyncStatus: MaterialsGitSyncStatus{Paused: true, Error: "existing paused status"},
		URL:                    previous, Branch: f.scope.Branch,
		StatusURL: previous, StatusBranch: f.scope.Branch,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := f.service.Sync(context.Background(), f.scope, false, nil)
	if err == nil || err.Error() != "existing paused status" || !status.Paused {
		t.Fatalf("a suffix-only edit discarded the earlier pause: %+v %v", status, err)
	}
}

func TestMaterialsGitSuffixEditVerifiesExistingOriginBeforeSynchronizing(t *testing.T) {
	const plain = "https://git.example.invalid/team/docs"
	const suffixed = plain + ".git"
	for _, test := range []struct {
		name, configured, origin, probeURL, tip, acceptedURL string
	}{
		{"add suffix", suffixed, plain, plain, "same-tip", plain},
		{"drop suffix", plain, suffixed, suffixed, "same-tip", suffixed},
		{"configure trailing slash", suffixed + "/", plain, plain, "same-tip", plain},
		{"different branch tips", suffixed, plain, plain, "different-tip", ""},
		{"different repository", suffixed, "https://git.example.invalid/team/other", "", "", ""},
		{"existing HTTP origin", suffixed, "http://git.example.invalid/team/docs", plain, "same-tip", plain},
		{"SSH aliases", "git@git.example.invalid:team/docs.git", "git@git.example.invalid:team/docs", "git@git.example.invalid:team/docs", "same-tip", "git@git.example.invalid:team/docs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := NewGitClient(nil, "git")
			called := false
			git.readCommand = func(_ context.Context, spec process.Spec) (process.Result, error) {
				called = true
				args := spec.Arguments
				if got := args[len(args)-2]; got != test.probeURL {
					t.Errorf("checked endpoint %q, want %q", got, test.probeURL)
				}
				return process.Result{Stdout: test.tip + "\trefs/heads/master\n"}, nil
			}
			service := &MaterialsGitSyncService{Git: git}
			scope := MaterialsGitScope{Root: t.TempDir(), URL: test.configured, Branch: "master"}
			remote := materialsGitEndpoint{url: test.configured, branchTip: "same-tip", git: git}
			verified, err := service.confirmMaterialsGitRemote(context.Background(), scope, remote, test.origin)
			if err != nil || verified.url != test.acceptedURL || called != (test.probeURL != "") {
				t.Fatalf("verified endpoint=%q, called=%t, error=%v", verified.url, called, err)
			}
		})
	}
}
