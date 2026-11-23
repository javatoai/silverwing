package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/dragon"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/snowball/auth"
)

const dragonQATestRequirement = "https://project.feishu.cn/obt/othertask/detail/1001234"

type dragonQATestClient struct {
	account      string
	accountReads int
	create       func(string, string, []string) (dragon.Approval, error)
	rows         []dragon.Summary
	approvals    map[string]dragon.Approval
}

func (f *dragonQATestClient) AccountIdentity(context.Context, domain.AppConfig) (string, error) {
	f.accountReads++
	return f.account, nil
}

func (f *dragonQATestClient) Create(_ context.Context, _ domain.AppConfig, requirementURL, remark string, apps []string) (dragon.Approval, error) {
	return f.create(requirementURL, remark, apps)
}

func (f *dragonQATestClient) Query(context.Context, domain.AppConfig, string) ([]dragon.Summary, error) {
	return f.rows, nil
}

func (f *dragonQATestClient) Detail(_ context.Context, _ domain.AppConfig, id string) (dragon.Approval, error) {
	if approval, ok := f.approvals[id]; ok {
		return approval, nil
	}
	return dragon.Approval{}, errors.New("mock was not given a matching approval")
}

type dragonQATestComments struct {
	sent, checked int
	err           error
	visible       string
	onSend        func()
}

func (f *dragonQATestComments) ResolveDragonRequirementCommentScope(context.Context, string) (string, error) {
	return "mock-meegle-scope", nil
}

func (f *dragonQATestComments) SyncDragonRequirementComment(context.Context, string, string, string, string) (string, error) {
	f.sent++
	if f.onSend != nil {
		f.onSend()
	}
	return "comment-1", f.err
}

func (f *dragonQATestComments) CheckDragonRequirementComment(context.Context, string, string, string, string) (string, error) {
	f.checked++
	return f.visible, nil
}

func dragonQAFixture(t *testing.T) (*App, DragonQASubmitRequest, *dragonQATestClient, *dragonQATestComments) {
	t.Helper()
	home := isolatePlatformUserHome(t)
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(a.Paths.Home, "task-root")
	cfg := domain.DefaultAppConfig()
	cfg.TaskRoot = &root
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := (auth.Store{Directory: filepath.Join(home, ".snowball-config")}).Save(auth.Credentials{Username: "dragon-account", Password: "fixture-password"}); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName = "test-task", "test-task"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-11-20T08:10:26Z", "2026-11-20T08:10:26Z"
	manifest.RequirementLink = dragonQATestRequirement
	manifest.Services = []domain.ServiceWorkspace{
		{GroupServiceID: "api", ServiceName: "api-service", ModuleID: "default"},
		{GroupServiceID: "api", ServiceName: "api-service", ModuleID: "additional"},
		{GroupServiceID: "second", ServiceName: "Another-Service", ModuleID: "default"},
	}
	for i := range manifest.Services {
		service := &manifest.Services[i]
		service.DevelopmentTool = domain.DevelopmentToolIntelliJIdea
		service.Health = domain.WorkspaceReady
		service.Strategy = domain.WorkspaceStandardWorktree
		service.ModuleSource = domain.TaskModuleConfigured
		service.TagMode = domain.TagBuildCurrentBranch
	}
	path := filepath.Join(root, manifest.TaskDirectoryName)
	if err := a.Manifests.Save(path, manifest); err != nil {
		t.Fatal(err)
	}
	task := DragonQATask{TaskID: manifest.TaskDirectoryName, TaskPath: path, TaskCreatedAt: manifest.CreatedAt}
	cli, comments := &dragonQATestClient{account: "dragon-account"}, &dragonQATestComments{}
	a.dragonQAClient, a.dragonQAComments = cli, comments
	state, err := a.DragonQAStatus(context.Background(), task)
	want := []DragonQAService{{ID: "api", Name: "api-service"}, {ID: "second", Name: "Another-Service"}}
	if err != nil || state.Phase != "" || state.RequirementURL != dragonQATestRequirement || !slices.Equal(state.Services, want) {
		t.Fatalf("unexpected initial status %+v, %v", state, err)
	}
	return a, DragonQASubmitRequest{DragonQATask: task, RequirementURL: state.RequirementURL, Services: state.Services}, cli, comments
}

func TestDragonQADisabledBlocksSubmissionAndReconciliation(t *testing.T) {
	a, request, cli, _ := dragonQAFixture(t)
	ctx := context.Background()
	setEnabled := func(enabled bool) {
		t.Helper()
		if _, err := a.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
			cfg.OptionalCLIs.Dragon = &enabled
			return cfg, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setEnabled(false)
	state, err := a.SubmitDragonQA(ctx, request)
	if err == nil || state.Phase != "" || cli.accountReads != 0 {
		t.Fatalf("disabled submission state=%+v error=%v account reads=%d", state, err, cli.accountReads)
	}
	if _, err := a.DragonQAStatus(ctx, request.DragonQATask); err != nil {
		t.Fatalf("disabled Dragon hid local state: %v", err)
	}
	setEnabled(true)
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		return dragon.Approval{}, context.DeadlineExceeded
	}
	state, err = a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAUnknown {
		t.Fatalf("missing receipt did not leave an uncertain attempt: %+v %v", state, err)
	}
	setEnabled(false)
	state, err = a.RecheckDragonQA(ctx, request.DragonQATask, "")
	if err == nil || state.Phase != DragonQAUnknown || cli.accountReads != 1 {
		t.Fatalf("disabled reconciliation state=%+v error=%v account reads=%d", state, err, cli.accountReads)
	}
}

func TestDragonQAConfigChangePreservesReceiptWithoutAutomaticComment(t *testing.T) {
	for _, change := range []string{"disable", "switch account"} {
		t.Run(change, func(t *testing.T) {
			a, request, cli, comments := dragonQAFixture(t)
			ctx := context.Background()
			request.SyncComment = true
			approval := dragonQATestApproval()
			cli.create = func(string, string, []string) (dragon.Approval, error) {
				if change == "disable" {
					if _, err := a.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
						enabled := false
						cfg.OptionalCLIs.Dragon = &enabled
						return cfg, nil
					}); err != nil {
						t.Fatal(err)
					}
				} else {
					store, err := auth.UserStore()
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.Save(auth.Credentials{Username: "other-account", Password: "fixture-password"}); err != nil {
						t.Fatal(err)
					}
				}
				return approval, nil
			}
			state, err := a.SubmitDragonQA(ctx, request)
			if err != nil || state.Phase != DragonQAApproved || state.ApprovalID != approval.ID || state.CommentStatus != DragonQACommentPending || comments.sent != 0 {
				t.Fatalf("original submission state=%+v err=%v comments=%d", state, err, comments.sent)
			}
			state, err = a.DragonQAStatus(ctx, request.DragonQATask)
			if err != nil || state.Phase != DragonQAApproved || state.ApprovalURL != approval.URL || state.CommentStatus != DragonQACommentPending {
				t.Fatalf("stored approval lost state=%+v err=%v", state, err)
			}
			if change == "disable" {
				state, err = a.RetryDragonQAComment(ctx, request.DragonQATask)
				if err == nil || state.ApprovalID != approval.ID || state.CommentStatus != DragonQACommentPending || comments.sent != 0 {
					t.Fatalf("disabled comment retry state=%+v err=%v comments=%d", state, err, comments.sent)
				}
				return
			}
			state, err = a.RetryDragonQAComment(ctx, request.DragonQATask)
			if err != nil || state.ApprovalID != approval.ID || state.CommentStatus != DragonQACommentSynced || comments.sent != 1 || cli.accountReads != 1 {
				t.Fatalf("fresh comment retry state=%+v err=%v comments=%d account reads=%d", state, err, comments.sent, cli.accountReads)
			}
		})
	}
}

func dragonQATestApproval() dragon.Approval {
	return dragon.Approval{
		Summary: dragon.Summary{ID: "9223372036854775806", CreatedAt: time.Now().Format(time.RFC3339Nano)},
		URL:     "https://dragon.snowballtech.com/#/workorder/detail/9223372036854775806",
		Meego:   "1001234",
		Apps:    []string{"api", "second"},
	}
}

func TestDragonQASubmitOnceAndReloadWithAutoCommentDisabled(t *testing.T) {
	a, request, cli, comments := dragonQAFixture(t)
	ctx := context.Background()
	request.Remark = " 提测备注\n第二行 "
	approval := dragonQATestApproval()
	createCalls := 0
	cli.create = func(requirement, remark string, names []string) (dragon.Approval, error) {
		createCalls++
		if requirement != request.RequirementURL || remark != request.Remark || !slices.Equal(names, []string{"api-service", "Another-Service"}) {
			t.Errorf("changed approval input %q %q %q", requirement, remark, names)
		}
		state, err := a.SubmitDragonQA(ctx, request)
		if err != nil || state.Phase != DragonQASubmitting {
			t.Errorf("concurrent submit did not observe pending approval: %+v, %v", state, err)
		}
		return approval, nil
	}
	state, err := a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAApproved || state.ApprovalURL != approval.URL || state.CommentStatus != "" {
		t.Fatalf("unexpected submit result %+v, %v", state, err)
	}
	reopened, err := newApp(a.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	reopened.dragonQAClient, reopened.dragonQAComments = cli, comments
	state, err = reopened.DragonQAStatus(ctx, request.DragonQATask)
	if err != nil || state.ApprovalID != approval.ID || state.ApprovalURL != approval.URL {
		t.Fatalf("receipt was lost after reopening %+v, %v", state, err)
	}
	state, err = reopened.SubmitDragonQA(ctx, request)
	if err != nil || state.ApprovalID != approval.ID || createCalls != 1 || comments.sent != 0 {
		t.Fatalf("repeat click changed external state %+v, %v, creates=%d comments=%d", state, err, createCalls, comments.sent)
	}
}

func TestDragonQALostReceiptRequiresEvidence(t *testing.T) {
	a, request, cli, _ := dragonQAFixture(t)
	ctx := context.Background()
	approval := dragonQATestApproval()
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		return dragon.Approval{}, context.DeadlineExceeded
	}
	state, err := a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAUnknown {
		t.Fatalf("deadline should leave an uncertain attempt %+v, %v", state, err)
	}
	state, err = a.RecheckDragonQA(ctx, request.DragonQATask, "")
	if err != nil || state.Phase != DragonQAUnknown || len(state.Candidates) != 0 {
		t.Fatalf("empty query should leave the task blocked %+v, %v", state, err)
	}
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		t.Error("uncertain approval was submitted again")
		return dragon.Approval{}, nil
	}
	state, err = a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAUnknown {
		t.Fatalf("retry bypassed uncertain state %+v, %v", state, err)
	}
	cli.rows = []dragon.Summary{approval.Summary}
	cli.approvals = map[string]dragon.Approval{approval.ID: approval}
	state, err = a.RecheckDragonQA(ctx, request.DragonQATask, "")
	if err != nil || len(state.Candidates) != 1 || state.Candidates[0].ApprovalID != approval.ID || state.Phase != DragonQAUnknown {
		t.Fatalf("unexpected candidate status %+v, %v", state, err)
	}
	state, err = a.RecheckDragonQA(ctx, request.DragonQATask, approval.ID)
	if err != nil || state.Phase != DragonQAApproved || state.ApprovalURL != approval.URL {
		t.Fatalf("could not recover approval %+v, %v", state, err)
	}
}

func TestDragonQAKnownApprovalIDCanBeRecovered(t *testing.T) {
	a, request, cli, _ := dragonQAFixture(t)
	ctx := context.Background()
	approval := dragonQATestApproval()
	cli.approvals = map[string]dragon.Approval{approval.ID: approval}
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		return dragon.Approval{}, &dragon.CreateError{ApprovalID: approval.ID, PossiblyCreated: true, Err: errors.New("response failed after creation")}
	}
	state, err := a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAUnknown || state.ApprovalID != approval.ID {
		t.Fatalf("known approval ID was not preserved %+v, %v", state, err)
	}
	cli.account = "other-account"
	if _, err := a.RecheckDragonQA(ctx, request.DragonQATask, ""); err == nil {
		t.Fatal("reconciliation accepted a different account")
	}
	cli.account = "dragon-account"
	state, err = a.RecheckDragonQA(ctx, request.DragonQATask, "")
	if err != nil || state.Phase != DragonQAApproved || state.ApprovalURL != approval.URL {
		t.Fatalf("could not recover known approval %+v, %v", state, err)
	}
}

func TestDragonQADefiniteRejectionAllowsCorrectedSubmission(t *testing.T) {
	a, request, cli, _ := dragonQAFixture(t)
	ctx := context.Background()
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		return dragon.Approval{}, &dragon.CreateError{Err: errors.New("invalid application rejected before submission")}
	}
	state, err := a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAFailed || state.Message == "" || len(state.Services) != 2 {
		t.Fatalf("definite rejection should allow correction %+v, %v", state, err)
	}
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		return dragonQATestApproval(), nil
	}
	request.Services = request.Services[:1]
	state, err = a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAApproved {
		t.Fatalf("corrected submission should work %+v, %v", state, err)
	}
}

func TestDragonQARetryCommentUsesStoredApproval(t *testing.T) {
	a, request, cli, comments := dragonQAFixture(t)
	ctx := context.Background()
	request.SyncComment = true
	approval := dragonQATestApproval()
	createCalls := 0
	cli.create = func(string, string, []string) (dragon.Approval, error) {
		createCalls++
		return approval, nil
	}
	comments.onSend = func() {
		state, err := a.DragonQAStatus(ctx, request.DragonQATask)
		if err != nil || state.Phase != DragonQAApproved || state.ApprovalURL != approval.URL {
			t.Errorf("approval must be persisted before publishing the comment: %+v, %v", state, err)
		}
	}
	comments.err = errors.New("comment preflight failed")
	state, err := a.SubmitDragonQA(ctx, request)
	if err != nil || state.ApprovalURL != approval.URL || state.CommentStatus != DragonQACommentFailed {
		t.Fatalf("comment failure changed approval %+v, %v", state, err)
	}
	comments.err = &integrations.DragonRequirementCommentUncertainError{Cause: context.DeadlineExceeded}
	state, err = a.RetryDragonQAComment(ctx, request.DragonQATask)
	if err != nil || state.CommentStatus != DragonQACommentUnknown {
		t.Fatalf("uncertain comment status %+v, %v", state, err)
	}
	comments.visible = "comment-1"
	state, err = a.RetryDragonQAComment(ctx, request.DragonQATask)
	if err != nil || state.CommentStatus != DragonQACommentSynced || createCalls != 1 || comments.sent != 2 || comments.checked != 1 {
		t.Fatalf("uncertain comment retry wrote again: %+v, %v, creates=%d sent=%d checked=%d", state, err, createCalls, comments.sent, comments.checked)
	}
}

type dragonQAHTTPFixture func(*http.Request) (*http.Response, error)

func (f dragonQAHTTPFixture) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDragonQAEmbeddedWriteAndRecoveryWithoutReplay(t *testing.T) {
	fixture, request, _, _ := dragonQAFixture(t)
	a, err := newApp(fixture.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	request.Services = request.Services[:1]
	request.Remark = "提测备注"
	approval := dragonQATestApproval()
	claims := fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(time.Hour).Unix())
	token := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	var calls []string
	a.connections.transport = dragonQAHTTPFixture(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Host+req.URL.Path)
		status := http.StatusOK
		code := "000000"
		var data string
		switch req.URL.Path {
		case "/beast/api/login":
			var credentials struct{ Username, Password string }
			if err := json.NewDecoder(req.Body).Decode(&credentials); err != nil || credentials.Username != "dragon-account" || credentials.Password != "fixture-password" {
				t.Errorf("unexpected Beast credentials or decoding error %v", err)
			}
			data = `{"token":"beast-fixture"}`
		case "/dragon/api/admin/user/beastUser":
			if req.URL.Query().Get("token") != "beast-fixture" {
				t.Error("Dragon exchange did not use Beast token")
			}
			data = fmt.Sprintf(`{"token":%q,"user":{"userId":33}}`, token)
		case "/dragon/api/approval/process/structure/24":
			data = `{
				"process":{"processTemplateId":24,"name":"提测审批","classifyId":4},
				"processTemplateStructureId":87,
				"nodes":[{"clazz":"start","id":"start","label":"开始"},{"id":"next","label":"审批"}],
				"edges":[{"source":"start","target":"next","label":"提交"}],
				"tpls":[{"formTemplateId":3,"content":{"list":[{"el":"test-approval","model":"testApproval"}]}}]
			}`
		case "/dragon/api/approval/approval":
			var payload struct {
				Templates struct {
					Forms []struct {
						Test struct {
							Meego  string `json:"meegoCode"`
							Remark string
							Apps   []struct{ AppName string } `json:"overviewData"`
						} `json:"testApproval"`
					} `json:"formData"`
				} `json:"tpls"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload.Templates.Forms) != 1 {
				t.Errorf("invalid approval payload %v", err)
			} else if form := payload.Templates.Forms[0].Test; form.Meego != request.RequirementURL || form.Remark != request.Remark ||
				len(form.Apps) != 1 || form.Apps[0].AppName != "bp-api-service" {
				t.Errorf("unexpected approval form %+v", form)
			}
			status, code = http.StatusBadGateway, "000500"
			data = `{"approvalId":` + approval.ID + `}`
		case "/dragon/api/approval/approval/" + approval.ID:
			data = fmt.Sprintf(`{"workOrder":{"approvalId":%s},"tpls":[{"formData":{"testApproval":{"meegoCode":%q,"remark":%q}}}]}`,
				approval.ID, approval.Meego, request.Remark)
		default:
			return nil, fmt.Errorf("unexpected Dragon request %s %s", req.Method, req.URL.Path)
		}
		body := fmt.Sprintf(`{"code":%q,"data":%s}`, code, data)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	ctx := context.Background()
	state, err := a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAUnknown || state.ApprovalID != approval.ID {
		t.Fatalf("uncertain POST state %+v error %v", state, err)
	}
	state, err = a.SubmitDragonQA(ctx, request)
	if err != nil || state.Phase != DragonQAUnknown || len(calls) != 4 {
		t.Fatalf("uncertain submission may have replayed state %+v error %v calls %v", state, err, calls)
	}
	state, err = a.RecheckDragonQA(ctx, request.DragonQATask, "")
	if err != nil || state.Phase != DragonQAApproved || state.ApprovalURL != approval.URL {
		t.Fatalf("known-ID recovery state %+v error %v", state, err)
	}
	want := []string{
		"POST beast.snowballtech.com/beast/api/login",
		"GET dragon.snowballtech.com/dragon/api/admin/user/beastUser",
		"GET dragon.snowballtech.com/dragon/api/approval/process/structure/24",
		"POST dragon.snowballtech.com/dragon/api/approval/approval",
		"GET dragon.snowballtech.com/dragon/api/approval/approval/" + approval.ID,
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("unexpected Dragon requests %v", calls)
	}
}
