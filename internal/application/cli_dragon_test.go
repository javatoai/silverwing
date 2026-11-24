package application

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/dragon"
	"silverwing-go/internal/snowball/auth"
	"silverwing-go/internal/snowball/dragonapi"
)

func cliDragonFixture(t *testing.T) (*App, DragonQASubmitRequest, *dragonQATestClient, *dragonQATestComments) {
	t.Helper()
	app, request, client, comments := dragonQAFixture(t)
	cfg, err := app.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := app.taskInConfig(Action{TaskID: request.TaskID}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Services[2].ServiceName = "operation-center"
	feign := manifest.Services[0]
	feign.GroupServiceID, feign.ServiceName = "renamed-client", "feign 的自定义显示名"
	origin := "git@some.example:platform/feign-client.git"
	feign.OriginURL = &origin
	manifest.Services = append(manifest.Services, feign)
	if err := app.Manifests.Save(request.TaskPath, manifest); err != nil {
		t.Fatal(err)
	}
	request.Services = []DragonQAService{{ID: "api", Name: "api-service"}, {ID: "second", Name: "operation-center"}}
	return app, request, client, comments
}

func TestCLIDragonPreflightBindsTaskServicesCommentAndAccount(t *testing.T) {
	app, task, client, comments := cliDragonFixture(t)
	ctx := context.Background()
	calls := 0
	client.create = func(requirement, remark string, services []string) (dragon.Approval, error) {
		calls++
		if requirement != task.RequirementURL || remark != "UAT 核对" || !slices.Equal(services, []string{"api-service", "operation-center"}) {
			t.Fatalf("unexpected approval inputs: %q %q %v", requirement, remark, services)
		}
		return dragonQATestApproval(), nil
	}
	request := CLIDragonSubmitRequest{Task: task.TaskID, Remark: "UAT 核对"}
	preview, err := app.CLIDragonSubmit(ctx, request)
	if err != nil || preview.Phase != "preview" || len(preview.Services) != 2 || preview.Preflight == "" || preview.SyncComment || calls != 0 {
		t.Fatalf("invalid preview: %+v error=%v calls=%d", preview, err, calls)
	}
	if local, err := app.dragonQAStore(task.DragonQATask).load(); err != nil || local.Phase != "" {
		t.Fatalf("preview wrote a task record: %+v, %v", local, err)
	}
	for _, change := range []func(*CLIDragonSubmitRequest){
		func(input *CLIDragonSubmitRequest) { input.Remark = "改变备注" },
		func(input *CLIDragonSubmitRequest) { input.SyncComment = true },
	} {
		changed := request
		change(&changed)
		changed.Preflight = preview.Preflight
		if _, err := app.CLIDragonSubmit(ctx, changed); !errors.Is(err, errCLIDragonPreflightStale) {
			t.Fatalf("changed command accepted a stale preflight: %v", err)
		}
	}
	shared, err := auth.UserStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Save(auth.Credentials{Username: "dragon-account", Password: "new-fixture-password"}); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Preflight = preview.Preflight
	if _, err := app.CLIDragonSubmit(ctx, changed); !errors.Is(err, errCLIDragonPreflightStale) {
		t.Fatalf("changed account accepted a stale preflight: %v", err)
	}
	preview, err = app.CLIDragonSubmit(ctx, request)
	if err != nil || preview.Preflight == "" {
		t.Fatalf("new account preflight failed: %+v %v", preview, err)
	}
	request.Preflight = preview.Preflight
	result, err := app.CLIDragonSubmit(ctx, request)
	if err != nil || result.Phase != DragonQAApproved || result.ApprovalID == "" || calls != 1 || comments.sent != 0 {
		t.Fatalf("unexpected submission: %+v error=%v calls=%d comments=%d", result, err, calls, comments.sent)
	}
	desktop, err := app.SubmitDragonQA(ctx, task)
	if err != nil || desktop.ApprovalID != result.ApprovalID || calls != 1 {
		t.Fatalf("desktop did not reuse CLI receipt: %+v error=%v calls=%d", desktop, err, calls)
	}
}

func TestCLIDragonUncertainReceiptRequiresRecheck(t *testing.T) {
	app, task, client, comments := cliDragonFixture(t)
	ctx := context.Background()
	calls := 0
	client.create = func(string, string, []string) (dragon.Approval, error) {
		calls++
		return dragon.Approval{}, &dragon.CreateError{Err: errors.New("模拟结果待核实"), PossiblyCreated: true}
	}
	request := CLIDragonSubmitRequest{Task: task.TaskID, SyncComment: true}
	preview, err := app.CLIDragonSubmit(ctx, request)
	if err != nil || preview.Preflight == "" {
		t.Fatalf("unexpected preflight %+v, %v", preview, err)
	}
	request.Preflight = preview.Preflight
	for i := 0; i < 2; i++ {
		result, err := app.CLIDragonSubmit(ctx, request)
		if err != nil || result.Phase != DragonQAUnknown || calls != 1 || comments.sent != 0 {
			t.Fatalf("uncertain submit replayed: %+v %v calls=%d comments=%d", result, err, calls, comments.sent)
		}
	}
	approval := dragonQATestApproval()
	approval.Remark = request.Remark
	approval.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	client.rows = []dragon.Summary{approval.Summary}
	client.approvals = map[string]dragon.Approval{approval.ID: approval}
	state, err := app.CLIDragonRecheck(ctx, task.TaskID, "")
	if err != nil || state.Phase != DragonQAUnknown || len(state.Candidates) != 1 || state.Candidates[0].ID != approval.ID {
		t.Fatalf("read-only candidate lookup returned %+v, %v", state, err)
	}
	comments.err = errors.New("模拟评论未发送")
	state, err = app.CLIDragonRecheck(ctx, task.TaskID, approval.ID)
	if err != nil || state.ApprovalID != approval.ID || state.CommentStatus != DragonQACommentFailed || calls != 1 || comments.sent != 1 {
		t.Fatalf("recheck returned %+v %v calls=%d comments=%d", state, err, calls, comments.sent)
	}
	comments.err = nil
	state, err = app.CLIDragonRetryComment(ctx, task.TaskID)
	if err != nil || state.CommentStatus != DragonQACommentSynced || calls != 1 || comments.sent != 2 {
		t.Fatalf("comment retry returned %+v %v calls=%d comments=%d", state, err, calls, comments.sent)
	}
}

func TestCLIDragonAndDesktopShareSubmissionLock(t *testing.T) {
	app, task, client, _ := cliDragonFixture(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int32
	client.create = func(string, string, []string) (dragon.Approval, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return dragonQATestApproval(), nil
	}
	request := CLIDragonSubmitRequest{Task: task.TaskID}
	preview, err := app.CLIDragonSubmit(ctx, request)
	if err != nil || preview.Preflight == "" {
		t.Fatalf("unexpected preflight %+v, %v", preview, err)
	}
	request.Preflight = preview.Preflight
	done := make(chan error, 1)
	go func() {
		state, err := app.CLIDragonSubmit(ctx, request)
		if err == nil && state.Phase != DragonQAApproved {
			err = fmt.Errorf("unexpected CLI result %+v", state)
		}
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("CLI exited before submit: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("CLI submission did not reach mock create")
	}
	desktop, err := app.SubmitDragonQA(ctx, task)
	if err != nil || desktop.Phase != DragonQASubmitting || calls.Load() != 1 {
		t.Fatalf("overlapping desktop submit was not blocked: %+v %v calls=%d", desktop, err, calls.Load())
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI submission did not return")
	}
}

func TestCLIDragonUsesEmbeddedReadOnlyApprovalQueries(t *testing.T) {
	app, _, _, _ := cliDragonFixture(t)
	installLegacyDragonProbe(t, app)
	token := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(time.Hour).Unix())) + ".fixture"
	var requests []string
	app.connections.transport = dragonQAHTTPFixture(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		var data string
		switch req.URL.Path {
		case "/beast/api/login":
			data = `{"token":"beast-fixture"}`
		case "/dragon/api/admin/user/beastUser":
			data = fmt.Sprintf(`{"token":%q,"user":{"userId":33}}`, token)
		case "/dragon/api/approval/approval/myCreate":
			if query := req.URL.Query(); query.Get("no") != "QA-123" || query.Get("page") != "2" || query.Get("size") != "8" {
				return nil, fmt.Errorf("unexpected approval query %v", query)
			}
			data = `{"page":2,"size":8,"total":9,"data":[{"approvalId":123,"no":"QA-123"}]}`
		case "/dragon/api/approval/approval/123":
			data = `{"workOrder":{"approvalId":123,"no":"QA-123"}}`
		default:
			return nil, fmt.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		body := io.NopCloser(strings.NewReader(`{"code":"000000","data":` + data + `}`))
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})
	list, err := app.CLIDragonApprovals(context.Background(), dragonapi.QueryInput{No: "QA-123", Page: 2, Size: 8})
	if err != nil || len(list.Items) != 1 || list.Items[0].ApprovalID != 123 {
		t.Fatalf("unexpected list response %+v, %v", list, err)
	}
	detail, err := app.CLIDragonDetail(context.Background(), 123)
	if err != nil || detail.ApprovalID != 123 {
		t.Fatalf("unexpected detail response %+v, %v", detail, err)
	}
	want := []string{
		"POST /beast/api/login", "GET /dragon/api/admin/user/beastUser",
		"GET /dragon/api/approval/approval/myCreate", "GET /dragon/api/approval/approval/123",
	}
	if !slices.Equal(requests, want) {
		t.Fatalf("unexpected Dragon requests %v", requests)
	}
}

func installLegacyDragonProbe(t *testing.T, app *App) {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "dragon")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	marker := filepath.Join(dir, "legacy-launch")
	source := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(source, []byte(`package main
import "os"
func main() {
	if err := os.WriteFile(os.Getenv("SW_TEST_DRAGON_CLI_MARKER"), []byte("launched"), 0600); err != nil {
		os.Exit(2)
	}
	os.Exit(1)
}`), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", executable, source).CombinedOutput(); err != nil {
		t.Fatalf("build Dragon process probe: %v\n%s", err, out)
	}
	t.Setenv("SW_TEST_DRAGON_CLI_MARKER", marker)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := app.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.DragonExecutablePath = &executable
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("legacy Dragon CLI started or marker could not be checked: %v", err)
		}
	})
}
