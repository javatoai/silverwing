package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestMain(m *testing.M) {
	if os.Getenv("SW_TAG_TEST_CHILD") == "1" {
		args := os.Args[1:]
		if len(args) > 0 && args[0] == "--home" {
			os.Exit(run(context.Background(), args, os.Stdout, os.Stderr))
		}
		os.Exit(runTagTestCLI(args))
	}
	os.Exit(m.Run())
}

func runTagTestCLI(args []string) int {
	if len(args) == 0 {
		return 90
	}
	value := func(flag string) string {
		for i, arg := range args[:len(args)-1] {
			if arg == flag {
				return args[i+1]
			}
		}
		return ""
	}
	switch args[0] {
	case "query-tag":
		uat := "初始"
		if os.Getenv("SW_TAG_TEST_UAT_POD_VERSION") != "" {
			uat = "成功"
		}
		fmt.Printf("{\"build_status\":\"成功\",\"uat_status\":%q,\"production_status\":\"初始\"}\n", uat)
	case "query-service-pods":
		if len(args) != 4 || args[1] != "--json" || args[2] != "uat" {
			return 97
		}
		fmt.Printf("{\"service\":%q,\"environment\":\"uat\",\"pods\":[{\"namespace\":\"bp\",\"pod_name\":\"fixture-pod\",\"ready\":true,\"app_version\":%q}]}\n",
			args[3], os.Getenv("SW_TAG_TEST_UAT_POD_VERSION"))
	case "config":
		fmt.Println("fixture-meegle")
	case "user":
		fmt.Println(`{"user_key":"fixture-account","tenant_key":"fixture-tenant"}`)
	case "workitem":
		switch os.Getenv("SW_TAG_TEST_ROSTER") {
		case "failed":
			fmt.Fprintln(os.Stderr, "fixture metadata unavailable")
			return 92
		case "empty":
			fmt.Println(`{"name":"隔离测试需求","work_item_attribute":{"role_members":[]}}`)
		default:
			fmt.Println(`{"name":"隔离测试需求","work_item_attribute":{"role_members":[{"name":"QC Owner","members":[{"name":"Tester"},{"name":"Unmapped"}]}]}}`)
		}
	case "auth":
		if value("--profile") == "" {
			fmt.Println(`{"authenticated":true,"host":"project.feishu.cn","tenant_key":"fixture-tenant"}`)
		} else {
			if value("--profile") != "fixture-lark" {
				return 93
			}
			verified := slices.Contains(args, "--verify") && os.Getenv("SW_TAG_TEST_LOGIN") != "expired"
			fmt.Printf("{\"appId\":\"cli_fixture\",\"identities\":{\"user\":{\"userName\":\"Fixture applicant\",\"openId\":\"ou_self\",\"verified\":%t},\"bot\":{\"status\":\"ready\",\"verified\":true}}}\n", verified)
		}
	case "im":
		if value("--profile") != "fixture-lark" || value("--as") != "bot" || value("--msg-type") != "interactive" {
			return 94
		}
		card, recipient, key := value("--content"), value("--user-id"), value("--idempotency-key")
		var notice struct {
			Header struct{ Title struct{ Content string } }
		}
		wantHeader := "Tester、Unmapped, 申请发布UAT环境版本"
		if roster := os.Getenv("SW_TAG_TEST_ROSTER"); roster == "empty" || roster == "failed" {
			wantHeader = "测试负责人待确认, 申请发布UAT环境版本"
		}
		if json.Unmarshal([]byte(card), &notice) != nil || notice.Header.Title.Content != wantHeader || strings.Contains(card, "申请人：") || key == "" {
			return 95
		}
		log, err := os.OpenFile(os.Getenv("SW_TAG_TEST_SEND_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 96
		}
		fmt.Fprintln(log, recipient+" "+key)
		log.Close()
		fmt.Printf("{\"ok\":true,\"data\":{\"message_id\":\"om_%s\"}}\n", recipient)
	default:
		fmt.Fprintln(os.Stderr, "unexpected fixture command", args[0])
		return 91
	}
	return 0
}

type cliFixture struct {
	executable, git, home, taskDir, sends string
	repository, remote, base, source      string
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
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	f := cliFixture{executable: executable, git: git, home: filepath.Join(root, "home"),
		taskDir: filepath.Join(root, "tasks", "验收 task"), sends: filepath.Join(root, "sent.log")}
	t.Setenv("SW_TAG_TEST_SEND_LOG", f.sends)
	if err := os.MkdirAll(f.taskDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := domain.DefaultAppConfig()
	taskRoot := filepath.Dir(f.taskDir)
	cfg.TaskRoot, cfg.GitExecutablePath = &taskRoot, &git
	cfg.GenbuExecutablePath, cfg.LarkExecutablePath, cfg.MeegleExecutablePath = &executable, &executable, &executable
	cfg.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "fixture", ProjectKey: "fixture-project"}}
	cfg.CommandProxyURL, cfg.CommandProxyTargets = nil, nil
	cfg.LarkNotice = &domain.LarkNoticeConfig{Profile: "fixture-lark", AppID: "cli_fixture", CLIDefaultTester: "Fallback",
		Contacts: []domain.LarkNoticeContact{{Name: "Tester", OpenID: "ou_qa"}, {Name: "Fallback", OpenID: "ou_fallback"}}}
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
		service := domain.GroupServiceConfig{ID: id, DisplayName: id, RepositoryID: id, Enabled: true,
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
		cfg.Groups[0].Services = append(cfg.Groups[0].Services, service)
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
	if err := storage.NewConfigStore(storage.NewPaths(f.home)).Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := storage.NewManifestStore().Save(f.taskDir, manifest); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args = append([]string{"--home", f.home, "tag"}, args...)
	command := exec.CommandContext(ctx, f.executable, append(args, "--json")...)
	command.Env = append(os.Environ(), "SW_TAG_TEST_CHILD=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
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

func TestTagCLIRecoversConflictAndNotifiesFromPersistedBatch(t *testing.T) {
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
	callTag[application.CLITagNoticeResult](t, f, exitError, "notify", "--batch", batch.BatchID)

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
	t.Setenv("SW_TAG_TEST_UAT_POD_VERSION", "")
	t.Setenv("SW_TAG_TEST_LOGIN", "expired")
	_, denied := callTag[application.CLITagNoticeResult](t, f, exitError, "notify", "--batch", batch.BatchID)
	if !strings.Contains(denied.Error, "fixture-lark") {
		t.Fatal("unverified user did not receive a profile-specific error", denied.Error)
	}
	if _, err := os.Stat(f.sends); !os.IsNotExist(err) {
		t.Fatal("messages reached the fake CLI before all gates passed")
	}
	t.Setenv("SW_TAG_TEST_LOGIN", "")
	first, _ := callTag[application.CLITagNoticeResult](t, f, 0, "notify", "--batch", batch.BatchID)
	if len(first.Recipients) != 1 || !first.Recipients[0].Self || first.Recipients[0].MessageID != "om_ou_self" {
		t.Fatal(first)
	}
	for _, roster := range []string{"failed", "empty", ""} {
		t.Setenv("SW_TAG_TEST_ROSTER", roster)
		repeated, _ := callTag[application.CLITagNoticeResult](t, f, 0, "notify", "--batch", batch.BatchID)
		if len(repeated.Recipients) != 1 || !repeated.Recipients[0].Self || !repeated.Recipients[0].Skipped || repeated.Recipients[0].MessageID != "om_ou_self" {
			t.Fatal("tester metadata changed the self-only recipient or duplicated the message", repeated)
		}
	}
	sends, err := os.ReadFile(f.sends)
	fields := strings.Fields(string(sends))
	if err != nil || len(fields) != 2 || fields[0] != "ou_self" {
		t.Fatal("notification repeated a successful receipt", string(sends), err)
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
