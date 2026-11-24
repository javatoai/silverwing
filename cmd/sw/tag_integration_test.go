package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
)

func TestMain(m *testing.M) {
	if os.Getenv("SW_TAG_TEST_CHILD") == "1" {
		args := os.Args[1:]
		if len(args) > 0 && (args[0] == "--conf-dir" || args[0] == "--home") {
			if err := installTagTestTransport(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(89)
			}
			os.Exit(run(context.Background(), args, os.Stdout, os.Stderr))
		}
		os.Exit(runTagTestCLI(args))
	}
	os.Exit(m.Run())
}

func runTagTestCLI(args []string) int {
	if name := strings.ToLower(filepath.Base(os.Args[0])); name == "genbu" || name == "genbu.exe" {
		_ = os.WriteFile(os.Getenv("SW_TAG_TEST_GENBU_CLI_MARKER"), []byte(strings.Join(args, " ")), 0600)
		return 88
	}
	if len(args) == 0 {
		return 90
	}
	if root := os.Getenv("SW_TAG_TEST_STATUS_ROOT"); root != "" {
		if index := slices.Index(args, "-C"); index >= 0 && index+2 < len(args) {
			repository, command := args[index+1], args[index+2:]
			switch {
			case slices.Equal(command, []string{"branch", "--show-current"}):
				fmt.Println("feature/primary")
			case slices.Equal(command, []string{"rev-parse", "--show-toplevel"}):
				fmt.Println(repository)
			case slices.Equal(command, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}):
				fmt.Println(filepath.Join(root, "repository", ".git"))
			case len(command) == 3 && command[0] == "ls-remote":
				fmt.Println(strings.Repeat("a", 40) + "\t" + command[2])
			default:
				fmt.Fprintln(os.Stderr, "status fixture rejects Git command", command)
				return 98
			}
			return 0
		}
	}
	fmt.Fprintln(os.Stderr, "unexpected fixture command", args[0])
	return 91
}

func installTagTestTransport() error {
	address := os.Getenv("SW_TAG_TEST_GENBU_ADDRESS")
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("Genbu fixture requires a loopback server address")
	}
	certificateDER, err := base64.StdEncoding.DecodeString(os.Getenv("SW_TAG_TEST_GENBU_CERT"))
	if err != nil {
		return err
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, ServerName: host}
	transport.DialContext = func(ctx context.Context, network, remote string) (net.Conn, error) {
		switch remote {
		case "beast.snowballtech.com:443", "genbu.snowballtech.com:443":
			return (&net.Dialer{}).DialContext(ctx, network, address)
		default:
			return nil, fmt.Errorf("unexpected destination in sw Tag test: %s", remote)
		}
	}
	http.DefaultTransport = transport
	return nil
}

func tagTestGenbuFixture(t *testing.T, userHome, executable string, override func(http.ResponseWriter, *http.Request) bool) (string, *atomic.Int32) {
	t.Helper()
	credentialsDir := filepath.Join(userHome, ".snowball-config")
	if err := os.MkdirAll(credentialsDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Seed the same file and JSON shape as the standalone Genbu and Dragon tools.
	if err := os.WriteFile(filepath.Join(credentialsDir, "credentials.json"), []byte(`{"version":1,"username":"tag-fixture","password":"fixture-password"}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	legacyCommand := filepath.Join(dir, "genbu")
	if runtime.GOOS == "windows" {
		legacyCommand += ".exe"
	}
	if err := os.Link(executable, legacyCommand); err != nil {
		binary, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacyCommand, binary, 0755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(dir, "invoked")
	t.Setenv("SW_TAG_TEST_GENBU_CLI_MARKER", marker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if data, err := os.ReadFile(marker); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("sw used the legacy Genbu executable: %q, %v", data, err)
		}
	})
	var calls atomic.Int32
	tokenPayload := fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(time.Hour).Unix())
	token := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(tokenPayload) + ".fixture"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == genbuapi.LoginPath {
			var account struct{ Username, Password string }
			if err := json.NewDecoder(r.Body).Decode(&account); err != nil || r.Method != http.MethodPost || r.Host != "beast.snowballtech.com" ||
				account.Username != "tag-fixture" || account.Password != "fixture-password" {
				t.Errorf("invalid fixture Beast login %v", err)
			}
			fmt.Fprint(w, `{"code":"000000","data":{"token":"fixture-beast-token"}}`)
			return
		}
		if r.Host != "genbu.snowballtech.com" || r.Header.Get("System-Code") != "ANDCN" {
			t.Errorf("invalid fixture Genbu target %s %s", r.Host, r.URL.Path)
		}
		if r.URL.Path != genbuapi.DragonUserPath && r.Header.Get("X-Token") != token {
			t.Errorf("missing Genbu session for %s", r.URL.Path)
		}
		if r.URL.Path != genbuapi.DragonUserPath && r.URL.Path != genbuapi.ApplicationsPath && r.URL.Path != cliServiceApprovalPath &&
			!slices.Contains([]string{"101", "102"}, r.Header.Get("App-Id")) {
			t.Errorf("missing Genbu application for %s", r.URL.Path)
		}
		if override != nil && override(w, r) {
			return
		}
		version := os.Getenv("SW_TAG_TEST_UAT_POD_VERSION")
		switch r.URL.Path {
		case genbuapi.DragonUserPath:
			if r.URL.Query().Get("token") != "fixture-beast-token" {
				t.Error("Genbu exchange did not use the Beast token")
			}
			fmt.Fprintf(w, `{"code":20000,"token":%q}`, token)
		case genbuapi.ApplicationsPath:
			fmt.Fprint(w, `{"code":20000,"response":[{"app_name":"svc-genbu","app_id":101},{"app_name":"independent-genbu","app_id":102}]}`)
		case genbuapi.VersionPath:
			var query struct {
				Conditions struct {
					Tag string `json:"key_word"`
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query.Conditions.Tag == "" {
				t.Errorf("missing Tag in version query %v", err)
			}
			fmt.Fprintf(w, `{"code":20000,"response":[{"version_id":3,"version_name":%q}]}`, query.Conditions.Tag)
		case genbuapi.BaseInfoPath:
			uat := 0
			if version != "" {
				uat = 1
			}
			fmt.Fprintf(w, `{"code":20000,"response":[{"step":1,"status":1},{"step":7,"status":%d},{"step":9,"status":0}]}`, uat)
		case genbuapi.PodGroupPath:
			if r.URL.Query().Get("env") != "UAT" {
				t.Error("expected a UAT Pod Group query")
			}
			fmt.Fprint(w, `{"code":20000,"response":{"groups":[{"group_id":1,"namespace":"bp","match_labels":"app=svc"}]}}`)
		case genbuapi.PodPath:
			if r.URL.Query().Get("namespace") != "bp" || r.URL.Query().Get("env") != "UAT" {
				t.Error("expected a UAT Pod query for namespace bp")
			}
			fmt.Fprintf(w, `{"code":20000,"response":[{"pod_name":"fixture-pod","ready":true,"restart_count":0,"app_version":%q}]}`, version)
		default:
			t.Errorf("unexpected Genbu request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("SW_TAG_TEST_GENBU_ADDRESS", server.Listener.Addr().String())
	t.Setenv("SW_TAG_TEST_GENBU_CERT", base64.StdEncoding.EncodeToString(server.Certificate().Raw))
	t.Setenv("SW_TAG_TEST_UAT_POD_VERSION", "")
	return legacyCommand, &calls
}

func isolateTagTestUserHome(t *testing.T, root string) string {
	t.Helper()
	userHome := filepath.Join(root, "isolated-user-home")
	if err := os.MkdirAll(userHome, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("HOME", userHome)
	return userHome
}

// The status fixture serves Git reads from the test binary and stores a completed Tag operation.
func newTagCLIStatusFixture(t *testing.T, override func(http.ResponseWriter, *http.Request) bool) (cliFixture, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	userHome := isolateTagTestUserHome(t, root)
	paths := newTagCLIProfilePaths(t, root)
	f := cliFixture{executable: executable, git: executable, confDir: paths.Home, userHome: userHome, taskDir: filepath.Join(paths.Tasks, "status-task")}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	repository, worktree := filepath.Join(root, "repository"), filepath.Join(f.taskDir, "svc-primary")
	for _, directory := range []string{repository, worktree, filepath.Join(f.taskDir, "tag-operations")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SW_TAG_TEST_STATUS_ROOT", root)
	legacyGenbu, genbuCalls := tagTestGenbuFixture(t, f.userHome, executable, override)
	f.genbuCalls = genbuCalls
	config.GitExecutablePath, config.GenbuExecutablePath = &executable, &legacyGenbu
	config.TagEnabled = true
	config.CommandProxyURL, config.CommandProxyTargets = nil, nil
	config.Repositories = []domain.RepositoryConfig{{ID: "svc", Name: "svc", RootPath: repository, GitCommonDirectory: filepath.Join(repository, ".git")}}
	config.Services = []domain.ServiceConfig{{ID: "svc", DisplayName: "svc", RepositoryID: "svc", Enabled: true, GenbuServiceName: "svc-genbu", GenbuProbeEnabled: true, MasterBranch: "origin/uat", TestTagBaselineRef: "origin/uat", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Modules: []domain.ServiceModuleConfig{{ID: "primary", Name: "primary", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch, TagEnabled: true}}}}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = "status-task", "status-task", "feature/primary"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-11-10 01:02:03", "2026-11-10 01:02:03"
	manifest.RequirementLink = "https://project.feishu.cn/fixture/userstory/detail/101"
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "svc", ServiceName: "svc", RepositoryPath: repository, WorktreePath: worktree, Branch: "feature/primary", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured, Warnings: []string{}, GroupServiceID: "svc", ModuleID: "primary", ModuleName: "primary", TagEnabled: true, TagMode: domain.TagBuildCurrentBranch, PushRemote: "origin"}}
	if err := storage.NewConfigStore(paths).Save(config); err != nil {
		t.Fatal(err)
	}
	if err := storage.NewProfileManifestStore(paths).Save(f.taskDir, manifest); err != nil {
		t.Fatal(err)
	}
	batch, tag, sha := "status-batch", "1.2.3.beta-1", strings.Repeat("a", 40)
	now := domain.FormatSilverWingTime(time.Now())
	op := domain.TagOperation{OperationID: delivery.NewTagJobID(), BatchID: &batch, FolderName: "status-task", ServiceName: "svc", RepositoryID: "svc", SourceBranch: "feature/primary", Remote: "origin", TagMode: domain.TagBuildCurrentBranch, State: domain.TagOperationSuccess, CreatedAt: now, UpdatedAt: now, SourceSHA: &sha, Tag: &tag, GroupServiceID: "svc", ModuleID: "primary", ConflictFiles: []string{}, GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuInitial, Production: domain.GenbuInitial, CheckedAt: &now}}
	data, err := json.Marshal(op)
	if err != nil || os.WriteFile(filepath.Join(f.taskDir, "tag-operations", op.OperationID+".json"), data, 0600) != nil {
		t.Fatal("cannot save isolated Tag batch", err)
	}
	return f, batch
}

func TestTagCLIStatusUsesGitWithoutGenbu(t *testing.T) {
	f, batch := newTagCLIStatusFixture(t, nil)
	t.Setenv("SW_TAG_TEST_UAT_POD_VERSION", "1.2.3.beta-1")
	ready, _ := callTag[[]delivery.TagObservation](t, f, 0, "status", "--batch", batch, "--namespace", "bp")
	if len(ready) != 1 || ready[0].Runtime == nil || ready[0].Runtime.State != delivery.TagRuntimeReady {
		t.Fatal("enabled Genbu did not confirm the UAT runtime", ready)
	}
	calls := f.genbuCalls.Load()
	if calls == 0 {
		t.Fatal("enabled status did not make Genbu HTTP requests")
	}
	store := storage.NewConfigStore(storage.NewPaths(f.confDir))
	disabled := false
	if _, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.OptionalCLIs.Genbu = &disabled
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	gitOnly, _ := callTag[[]delivery.TagObservation](t, f, 0, "status", "--batch", batch)
	if len(gitOnly) != 1 || gitOnly[0].Git.State != "PUSHED" || !gitOnly[0].GenbuSkipped || gitOnly[0].Runtime != nil ||
		gitOnly[0].Operation.GenbuStatus.CheckedAt != nil {
		t.Fatal("disabled Genbu affected Git status or exposed stale pipeline results", gitOnly)
	}
	_, denied := callTag[[]delivery.TagObservation](t, f, exitError, "status", "--operation", "missing", "--namespace", "bp")
	if !strings.Contains(denied.Error, "Genbu 已关闭") {
		t.Fatal("namespace status did not reject disabled Genbu at admission", denied)
	}
	if got := f.genbuCalls.Load(); got != calls {
		t.Fatalf("disabled status made %d Genbu HTTP requests", got-calls)
	}

	if err := os.Remove(filepath.Join(f.userHome, ".snowball-config", "credentials.json")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credentials.json", "genbu.json"} {
		path := filepath.Join(f.confDir, name)
		if err := os.WriteFile(path, []byte(`{"version":1,"username":"profile-only","password":"fixture-password"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		enabled := true
		cfg.OptionalCLIs.Genbu = &enabled
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, denied = callTag[[]delivery.TagObservation](t, f, exitError, "status", "--batch", batch, "--namespace", "bp")
	if !strings.Contains(denied.Error, "共用账号") {
		t.Fatal("namespace status did not require the user home credentials", denied)
	}
	if got := f.genbuCalls.Load(); got != calls {
		t.Fatalf("status with profile-only credentials made %d Genbu HTTP requests", got-calls)
	}
}

type cliFixture struct {
	executable, git, confDir, userHome, taskDir string
	repository, remote, base, source            string
	genbuCalls                                  *atomic.Int32
}

func (f cliFixture) gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command(f.git, args...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func newTagCLIProfilePaths(t *testing.T, root string) storage.Paths {
	t.Helper()
	paths := storage.NewPaths(filepath.Join(root, "profile"))
	paths.Tasks = filepath.Join(root, "tasks")
	var err error
	paths.ProfileID, err = storage.LoadOrCreateProfileID(paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func newCLIFixture(t *testing.T, moreServices bool) cliFixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is required for the isolated Tag integration test")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	userHome := isolateTagTestUserHome(t, root)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	paths := newTagCLIProfilePaths(t, root)
	f := cliFixture{executable: executable, git: git, confDir: paths.Home, userHome: userHome,
		taskDir: filepath.Join(paths.Tasks, "验收 task")}
	if err := os.MkdirAll(f.taskDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := domain.DefaultAppConfig()
	cfg.TaskRoot = &paths.Tasks
	cfg.GitExecutablePath = &git
	legacyGenbu, genbuCalls := tagTestGenbuFixture(t, f.userHome, executable, nil)
	f.genbuCalls = genbuCalls
	cfg.GenbuExecutablePath = &legacyGenbu
	cfg.CommandProxyURL, cfg.CommandProxyTargets = nil, nil
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = "验收 task", "验收 task", "feature/primary"
	manifest.RequirementLink = "https://project.feishu.cn/fixture/userstory/detail/101"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-11-06 10:00:00", "2026-11-06 10:00:00"
	addRepo := func(id string, modules []string, conflict bool) (repo, remote string) {
		repo, remote = filepath.Join(root, id), filepath.Join(root, id+"-remote.git")
		f.gitRun(t, root, "init", "-b", "uat", repo)
		f.gitRun(t, root, "init", "--bare", remote)
		f.gitRun(t, repo, "config", "user.email", "test@example.invalid")
		f.gitRun(t, repo, "config", "user.name", "Tag fixture")
		f.gitRun(t, repo, "config", "core.autocrlf", "false")
		f.gitRun(t, repo, "config", "commit.gpgsign", "false")
		writeFixtureFile(t, filepath.Join(repo, "base.txt"), "base\n")
		writeFixtureFile(t, filepath.Join(repo, ".gitignore"), "local-test-output.txt\n")
		f.gitRun(t, repo, "add", "base.txt", ".gitignore")
		f.gitRun(t, repo, "commit", "-m", "base")
		f.gitRun(t, repo, "tag", "-a", "1.2.3.beta-1", "-m", "seed")
		f.gitRun(t, repo, "remote", "add", "origin", remote)
		f.gitRun(t, repo, "push", "--tags", "origin", "uat")
		cfg.Repositories = append(cfg.Repositories, domain.RepositoryConfig{ID: id, Name: id, RootPath: repo, GitCommonDirectory: filepath.Join(repo, ".git")})
		service := domain.ServiceConfig{ID: id, DisplayName: id, RepositoryID: id, Enabled: true,
			GenbuServiceName: id + "-genbu", GenbuProbeEnabled: true, MasterBranch: "origin/uat",
			TestTagBaselineRef: "origin/uat", DevelopmentTool: domain.DevelopmentToolVisualStudioCode}
		for i, module := range modules {
			dir, branch := filepath.Join(f.taskDir, id+"-"+module), "feature/"+module
			f.gitRun(t, repo, "worktree", "add", "-b", branch, dir, "uat")
			file := module + ".txt"
			if conflict && i == 0 {
				file = "base.txt"
			}
			writeFixtureFile(t, filepath.Join(dir, file), "source "+module+"\n")
			f.gitRun(t, dir, "add", file)
			f.gitRun(t, dir, "commit", "-m", "source "+module)
			mode, ref := domain.TagBuildCurrentBranch, (*string)(nil)
			if conflict {
				target := "origin/uat"
				mode, ref = domain.TagBuildMergeToTargetBranch, &target
			}
			service.Modules = append(service.Modules, domain.ServiceModuleConfig{ID: module, Name: module,
				Strategy: domain.WorkspaceStandardWorktree, TagMode: mode, TagTargetRef: ref, TagEnabled: true})
			manifest.Services = append(manifest.Services, domain.ServiceWorkspace{RepositoryID: id, ServiceName: id,
				RepositoryPath: repo, WorktreePath: dir, Branch: branch, DevelopmentTool: service.DevelopmentTool,
				Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured,
				Warnings: []string{}, GroupServiceID: id, ModuleID: module, ModuleName: module,
				TagEnabled: true, TagMode: mode, TagTargetRef: ref, PushRemote: "origin", TagMessagePrefix: "test"})
		}
		cfg.Services = append(cfg.Services, service)
		if conflict {
			writeFixtureFile(t, filepath.Join(repo, "base.txt"), "target\n")
			f.gitRun(t, repo, "add", "base.txt")
			f.gitRun(t, repo, "commit", "-m", "target")
			f.gitRun(t, repo, "push", "origin", "uat")
			writeFixtureFile(t, filepath.Join(repo, "local-only.txt"), "keep checked-out target files\n")
		}
		return repo, remote
	}
	modules := []string{"primary"}
	if moreServices {
		modules = append(modules, "extra")
	}
	f.repository, f.remote = addRepo("svc", modules, true)
	f.base = f.gitRun(t, f.repository, "rev-parse", "uat")
	f.source = f.gitRun(t, f.repository, "rev-parse", "feature/primary")
	if moreServices {
		addRepo("independent", []string{"module"}, false)
	}
	if err := storage.NewConfigStore(paths).Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := storage.NewProfileManifestStore(paths).Save(f.taskDir, manifest); err != nil {
		t.Fatal(err)
	}
	return f
}

type cliReply struct {
	OK          bool
	Data        json.RawMessage
	Error       string
	PreflightID string
	Warnings    []string
}

func (f cliFixture) call(args ...string) (cliReply, int, error) {
	return f.callCommand(append([]string{"tag"}, args...)...)
}

func (f cliFixture) callCommand(args ...string) (cliReply, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args = append([]string{"--conf-dir", f.confDir}, args...)
	command := exec.CommandContext(ctx, f.executable, append(args, "--json")...)
	command.Env = append(os.Environ(), "SW_TAG_TEST_CHILD=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if ctx.Err() != nil {
		return cliReply{}, -1, ctx.Err()
	}
	code := 0
	var failure *exec.ExitError
	if errors.As(err, &failure) {
		code = failure.ExitCode()
	} else if err != nil {
		return cliReply{}, -1, err
	}
	var reply cliReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return reply, code, fmt.Errorf("invalid CLI result %s: %w, stderr %s", out, err, stderr.String())
	}
	return reply, code, nil
}

func callTag[T any](t *testing.T, f cliFixture, code int, args ...string) (T, cliReply) {
	t.Helper()
	reply, actual, err := f.call(args...)
	if err != nil || actual != code || reply.OK != (code == 0) {
		t.Fatalf("sw tag %v: exit %d wanted %d, response %s, error %s, %v", args, actual, code, reply.Data, reply.Error, err)
	}
	var result T
	if len(reply.Data) > 0 {
		if err := json.Unmarshal(reply.Data, &result); err != nil {
			t.Fatal(err)
		}
	}
	return result, reply
}

func TestTagCLIRecoversConflictAndReadsPersistedBatch(t *testing.T) {
	f := newCLIFixture(t, true)
	task := filepath.Base(f.taskDir)
	rows, _ := callTag[[]application.CLITagTarget](t, f, 0, "list", "--task", task)
	if len(rows) != 3 || !rows[0].Selectable || rows[0].SelectionKey != "svc:primary" || rows[0].TargetBranch != "uat" {
		t.Fatal(rows)
	}
	preflightArgs := []string{"build", "--task", task, "--all-services"}
	preview, reply := callTag[[]application.CLITagPreflightItem](t, f, exitConflict, append(slices.Clone(preflightArgs), "--dry-run")...)
	id := reply.PreflightID
	if id == "" || len(preview) != 3 || len(preview[0].Preview.ConflictFiles) != 1 ||
		preview[1].BlockedBy != preview[0].OperationID || preview[2].Error != "" {
		t.Fatal(preview)
	}
	if len(reply.Warnings) != 1 || !strings.Contains(reply.Warnings[0], "svc-genbu") {
		t.Fatal("preflight did not expose the shared deployment service", reply.Warnings)
	}
	if f.gitRun(t, f.remote, "rev-parse", "uat") != f.base || strings.Count(f.gitRun(t, f.remote, "tag", "--list"), "\n") != 0 {
		t.Fatal("preflight wrote to the isolated remote")
	}
	callTag[application.CLITagJobResult](t, f, exitUsage, preflightArgs...)
	applyArgs := append(slices.Clone(preflightArgs), "--preflight", id)
	callTag[application.CLITagJobResult](t, f, exitError, applyArgs...)
	conflict := preview[0]
	worktree := conflict.Worktree
	if !strings.Contains(conflict.Error, "禁止将 origin/uat 分支合并到 feature/primary 分支") ||
		conflict.Preview.SourceSHA != f.source || *conflict.Preview.TargetSHA != f.base ||
		f.gitRun(t, worktree, "branch", "--show-current") != "" || f.gitRun(t, worktree, "rev-parse", "MERGE_HEAD") != f.source {
		t.Fatal("detached merge did not retain its original direction", conflict)
	}
	writeFixtureFile(t, filepath.Join(worktree, "base.txt"), "resolved\n")
	testOutput := filepath.Join(worktree, "local-test-output.txt")
	writeFixtureFile(t, testOutput, "keep test evidence\n")
	f.gitRun(t, worktree, "add", "base.txt")
	passed, rechecked := callTag[[]application.CLITagPreflightItem](t, f, 0, append(slices.Clone(applyArgs), "--dry-run")...)
	plannedMerge := f.gitRun(t, worktree, "rev-parse", "HEAD")
	if rechecked.PreflightID != id || passed[0].OperationID != conflict.OperationID || *passed[1].Preview.TargetSHA != plannedMerge ||
		f.gitRun(t, f.remote, "rev-parse", "uat") != f.base {
		t.Fatal("recheck failed to retain the original resolution and simulate the next target", passed)
	}
	callTag[application.CLITagJobResult](t, f, exitError, "build", "--task", task, "--service", "svc", "--preflight", id)

	hook := filepath.Join(f.remote, "hooks", "pre-receive")
	writeFixtureFile(t, hook, "#!/bin/sh\nwhile read old new ref; do\n case \"$ref\" in refs/heads/feature/primary) exit 1;; esac\ndone\n")
	batch, _ := callTag[application.CLITagJobResult](t, f, exitError, applyArgs...)
	if batch.BatchID != id || len(batch.Items) != 3 || batch.Items[0].Record.State != string(domain.TagOperationPreflightPassed) ||
		batch.Items[1].Record.BlockedBy != conflict.OperationID || batch.Items[2].Record.State != string(domain.TagOperationSuccess) {
		t.Fatalf("incorrect partial batch %+v", batch.Items)
	}
	interrupted, _ := callTag[[]delivery.TagObservation](t, f, exitError, "status", "--operation", conflict.OperationID)
	if len(interrupted) != 1 || interrupted[0].Operation.PreflightWorktree != worktree ||
		interrupted[0].Operation.Message == nil || !strings.Contains(*interrupted[0].Operation.Message, "推送远端分支") {
		t.Fatal("status did not persist branch failure and recovery location", interrupted)
	}

	// Fail the Tag push after both branch pushes. A new process must reuse
	// the confirmed merge and the reserved Tag.
	writeFixtureFile(t, hook, "#!/bin/sh\nwhile read old new ref; do\n case \"$ref\" in refs/tags/*) exit 1;; esac\ndone\n")
	partial, _ := callTag[application.CLITagJobResult](t, f, exitError, "retry", "--operation", conflict.OperationID)
	saved := partial.Items[0].Record
	if saved.State != string(domain.TagOperationPartial) {
		t.Fatal("Tag failure did not persist a partial push", saved)
	}
	merge := f.gitRun(t, f.remote, "rev-parse", "uat")
	if merge != plannedMerge || saved.Tag == "" ||
		f.gitRun(t, f.repository, "rev-list", "--parents", "-n", "1", merge) != merge+" "+f.base+" "+f.source {
		t.Fatal("resolved merge was not journaled at the original target", saved)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Go(func() {
			_, code, err := f.call("retry", "--operation", conflict.OperationID)
			if err != nil {
				t.Error(err)
			}
			codes[i] = code
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(codes, []int{0, 1}) && !reflect.DeepEqual(codes, []int{1, 0}) {
		t.Fatal("concurrent retries did not share the lease", codes)
	}
	if f.gitRun(t, f.remote, "rev-parse", "refs/tags/"+saved.Tag+"^{commit}") != merge ||
		f.gitRun(t, f.repository, "rev-parse", "uat") != f.base || !strings.Contains(f.gitRun(t, f.repository, "status", "--porcelain"), "?? local-only.txt") {
		t.Fatal("retry altered the checked-out target or allocated a different Tag")
	}
	if data, err := os.ReadFile(testOutput); err != nil || string(data) != "keep test evidence\n" {
		t.Fatalf("the ignored evidence was removed or changed: %v", err)
	}
	callTag[application.CLITagJobResult](t, f, 0, "retry", "--operation", batch.Items[1].OperationID)
	if _, err := os.Stat(passed[1].Worktree); !os.IsNotExist(err) {
		t.Fatalf("the clean second worktree still exists: %v", err)
	}
	callTag[application.CLITagJobResult](t, f, exitError, applyArgs...)
	status, _ := callTag[[]delivery.TagObservation](t, f, 0, "status", "--batch", batch.BatchID)
	if len(status) != len(batch.Items) {
		t.Fatal("status omitted items from the persisted batch", status)
	}
	for _, item := range status {
		if item.Git.State != "PUSHED" || item.Operation.GenbuStatus.Build != domain.GenbuSuccess || item.Operation.GenbuStatus.UAT != domain.GenbuInitial {
			t.Fatal("Git, build and UAT results were conflated", item)
		}
	}
	t.Setenv("SW_TAG_TEST_UAT_POD_VERSION", saved.Tag)
	runtime, _ := callTag[[]delivery.TagObservation](t, f, 0, "status", "--operation", conflict.OperationID, "--namespace", "bp")
	if len(runtime) != 1 || runtime[0].Runtime == nil || runtime[0].Runtime.State != delivery.TagRuntimeReady {
		t.Fatal("CLI did not confirm the target runtime version", runtime)
	}
}

func TestTagCLIRejectsDriftForEntireApprovedBatchBeforePush(t *testing.T) {
	f := newCLIFixture(t, true)
	args := []string{"build", "--task", filepath.Base(f.taskDir), "--service", "independent:module", "--service", "svc:primary"}
	preview, reply := callTag[[]application.CLITagPreflightItem](t, f, exitConflict, append(slices.Clone(args), "--dry-run")...)
	item := preview[1]
	resolved := filepath.Join(item.Worktree, "base.txt")
	writeFixtureFile(t, resolved, "resolved\n")
	f.gitRun(t, item.Worktree, "add", "base.txt")
	args = append(args, "--preflight", reply.PreflightID)
	callTag[[]application.CLITagPreflightItem](t, f, 0, append(slices.Clone(args), "--dry-run")...)
	approved := f.gitRun(t, item.Worktree, "rev-parse", "HEAD")
	f.gitRun(t, f.repository, "commit", "--allow-empty", "-m", "remote target advanced")
	f.gitRun(t, f.repository, "push", "origin", "uat")
	moved := f.gitRun(t, f.remote, "rev-parse", "uat")
	job, denied := callTag[application.CLITagJobResult](t, f, exitError, args...)
	if len(job.Items) != 0 || !strings.Contains(denied.Error, moved) || !strings.Contains(denied.Error, f.base) {
		t.Fatal("build did not report both baselines before starting", denied.Error)
	}
	if data, err := os.ReadFile(resolved); err != nil || string(data) != "resolved\n" {
		t.Fatal("staged conflict resolution lost", err)
	}
	if f.gitRun(t, item.Worktree, "rev-parse", "HEAD") != approved || f.gitRun(t, f.remote, "rev-parse", "uat") != moved {
		t.Fatal("stale approval changed either baseline")
	}
	independent := filepath.Join(filepath.Dir(f.remote), "independent-remote.git")
	if got := f.gitRun(t, independent, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags"); got != "refs/heads/uat\nrefs/tags/1.2.3.beta-1" && got != "refs/heads/uat\r\nrefs/tags/1.2.3.beta-1" {
		t.Fatal("the earlier independent item was pushed before whole-batch validation", got)
	}
}
