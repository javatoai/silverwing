package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type tagPlanGitFixture struct {
	Worktree, Common, Started, Release, Log, LocalTag, RemoteTag string
}

// The child is this test binary, never a real Git executable. A missing broken
// worktree exercises process.Runner's actual start error before the child runs.
func TestMain(m *testing.M) {
	if fixture := os.Getenv("SILVERWING_TEST_TAG_PLAN_GIT"); fixture != "" {
		os.Exit(runTagPlanGitFixture(fixture))
	}
	os.Exit(m.Run())
}

func runTagPlanGitFixture(path string) int {
	var f tagPlanGitFixture
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &f) != nil {
		return 90
	}
	var repository string
	var args []string
	for i, arg := range os.Args[1:] {
		if arg == "-C" && i+3 <= len(os.Args[1:]) {
			repository, args = os.Args[i+2], os.Args[i+3:]
			break
		}
	}
	if len(args) == 0 {
		return 91
	}
	command := strings.Join(args, " ")
	file, err := os.OpenFile(f.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 92
	}
	_, _ = fmt.Fprintln(file, repository+"\t"+command)
	_ = file.Close()
	exists := func(path string) bool { _, err := os.Stat(path); return err == nil }
	switch {
	case command == "branch --show-current":
		_ = os.WriteFile(f.Started, nil, 0600)
		deadline := time.Now().Add(5 * time.Second)
		for !exists(f.Release) {
			if time.Now().After(deadline) {
				return 93
			}
			time.Sleep(5 * time.Millisecond)
		}
		fmt.Println("feature/synthetic")
	case command == "rev-parse --show-toplevel":
		fmt.Println(f.Worktree)
	case strings.Contains(command, "--git-common-dir"), command == "rev-parse --absolute-git-dir":
		fmt.Println(f.Common)
	case command == "remote get-url origin":
		fmt.Println("https://example.invalid/healthy.git")
	case args[0] == "status", args[0] == "fetch":
	case args[0] == "log":
		fmt.Println("abcd synthetic commit")
	case args[0] == "for-each-ref":
		fmt.Println("1 1.2.3.beta-1")
	case args[0] == "rev-parse" && strings.Contains(command, "origin/feature/synthetic"):
		return 1
	case args[0] == "rev-parse" && strings.Contains(command, "refs/tags/"):
		if !exists(f.LocalTag) {
			return 1
		}
		fmt.Println("synthetic-sha")
	case args[0] == "rev-parse":
		fmt.Println("synthetic-sha")
	case args[0] == "tag":
		_ = os.WriteFile(f.LocalTag, nil, 0600)
	case args[0] == "ls-remote":
		if exists(f.RemoteTag) {
			fmt.Println("synthetic-sha\trefs/tags/1.2.3.beta-2")
		}
	case args[0] == "push":
		if strings.Contains(command, "refs/tags/") {
			_ = os.WriteFile(f.RemoteTag, nil, 0600)
		}
	default:
		fmt.Fprintln(os.Stderr, "unexpected synthetic Git command: "+command)
		return 94
	}
	return 0
}

func TestTagJobsMixedRealPlansIsolateRepositoryFailure(t *testing.T) {
	a, scopes, tasks := tagJobManagerFixture(t, "synthetic")
	scope, original := scopes["synthetic"], tasks["synthetic"]
	config, manifest := original.config, original.manifest
	a.Runner = process.NewRunner()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config.GitExecutablePath = &executable
	fixtureRoot := t.TempDir()
	healthy := filepath.Join(scope.TaskPath, "healthy")
	broken := filepath.Join(scope.TaskPath, "broken") // deliberately absent
	common := filepath.Join(fixtureRoot, "healthy-repository", ".git")
	for _, path := range []string{healthy, common} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"broken", "healthy"} {
		repository := filepath.Join(fixtureRoot, key+"-repository")
		config.Repositories = append(config.Repositories, domain.RepositoryConfig{ID: key, Name: key, RootPath: repository, GitCommonDirectory: filepath.Join(repository, ".git")})
		manifest.Services = append(manifest.Services, domain.ServiceWorkspace{RepositoryID: key, ServiceName: key, RepositoryPath: repository, WorktreePath: filepath.Join(scope.TaskPath, key), Branch: "feature/synthetic", DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured, Warnings: []string{}, GroupServiceID: key, ModuleID: "module", TagMode: domain.TagBuildCurrentBranch, PushRemote: "origin", TagEnabled: true})
	}
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	if err := a.Manifests.Save(scope.TaskPath, manifest); err != nil {
		t.Fatal(err)
	}
	config, err = a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	scope.ConfigIdentity = configTagIdentity(config)
	f := tagPlanGitFixture{Worktree: healthy, Common: common, Started: filepath.Join(fixtureRoot, "started"), Release: filepath.Join(fixtureRoot, "release"), Log: filepath.Join(fixtureRoot, "commands"), LocalTag: filepath.Join(fixtureRoot, "local-tag"), RemoteTag: filepath.Join(fixtureRoot, "remote-tag")}
	data, _ := json.Marshal(f)
	fixture := filepath.Join(fixtureRoot, "git-fixture.json")
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SILVERWING_TEST_TAG_PLAN_GIT", fixture)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	a.tagJobManager().openTask = func(c domain.AppConfig, id string) (tagTaskRunner, error) { return a.delivery(c).OpenTagTask(id) }
	initial := startTagFixture(t, a, scope, "broken:module", "healthy:module")
	if len(initial.Items) != 2 {
		t.Fatal("mixed real plans lost initial items")
	}
	for _, item := range initial.Items {
		if item.OperationID == "" || item.Record == nil || !item.Busy || !item.Queued {
			t.Fatalf("initial placeholder was not retained: %+v", item)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(f.Started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("healthy real workflow did not reach its barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := a.AppendTagJob(context.Background(), scope, initial.ID, Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"not-owned:module"}}}); err == nil {
		t.Fatal("invalid append bypassed ownership validation")
	}
	active, err := a.TagJobSnapshot(context.Background(), scope)
	if err != nil || !active.Busy || active.Cancelling || len(active.Items) != 2 {
		t.Fatalf("rejected append changed active job: %+v %v", active, err)
	}
	if err := os.WriteFile(f.Release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var final TagJobSnapshot
	if err := a.WatchTagJob(ctx, scope, initial.ID, 0, func(snapshot TagJobSnapshot) { final = snapshot }); err != nil {
		commands, _ := os.ReadFile(f.Log)
		t.Fatalf("real workflow did not finish: %v; commands=%s; snapshot=%+v", err, commands, final)
	}
	for _, item := range final.Items {
		want := TagJobPhaseCompleted
		if item.SelectionKey == "broken:module" {
			want = TagJobPhaseFailed
		}
		if item.Phase != want || !item.Done || item.Busy || item.Record == nil || item.OperationID != item.Record.ID {
			t.Fatalf("mixed plan result lost or stalled: %+v", item)
		}
		data, err := os.ReadFile(filepath.Join(scope.TaskPath, "tag-operations", item.OperationID+".json"))
		var saved domain.TagOperation
		if err != nil || json.Unmarshal(data, &saved) != nil || string(saved.State) != item.Record.State {
			t.Fatal("terminal result did not follow durable save")
		}
	}
	commands, err := os.ReadFile(f.Log)
	if err != nil || strings.Contains(string(commands), broken+"\t") {
		t.Fatal("broken repository reached a Git child or mutation")
	}
	if _, err := os.Stat(f.RemoteTag); err != nil {
		t.Fatal("healthy repository did not complete verified synthetic tag push")
	}
}
