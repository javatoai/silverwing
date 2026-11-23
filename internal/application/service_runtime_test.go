package application

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
)

func serviceRuntimeFixture(t *testing.T) (*App, Action) {
	t.Helper()
	app := composeApp(storage.NewPaths(t.TempDir()), storage.NewManifestStore())
	saveFixturePlatformAccount(t)
	cfg := domain.DefaultAppConfig()
	enabled := true
	cfg.OptionalCLIs.Genbu = &enabled
	repo := t.TempDir()
	cfg.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "Local repo", RootPath: repo, GitCommonDirectory: filepath.Join(repo, ".git")}}
	cfg.Services = []domain.ServiceConfig{{
		ID: "service", RepositoryID: "repo", DisplayName: "Catalog service", Enabled: true,
		GenbuProbeEnabled: true, GenbuServiceName: "fixture-runtime", MasterBranch: "origin/main",
		TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch}},
	}}
	if cfg.TaskRoot != nil {
		t.Fatal("runtime fixture must have no task root")
	}
	if err := app.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return app, serviceRuntimeTestAction(app, cfg, "fixture-runtime")
}

func serviceRuntimeTestAction(app *App, cfg domain.AppConfig, genbuName string) Action {
	return Action{Kind: "service.runtime.pods", ServiceID: "service", Payload: map[string]any{
		"environment": "uat", serviceRuntimeContextKey: ServiceRuntimeContext{
			Home: app.Paths.Home, GenbuServiceName: genbuName, QueryIdentity: TaskServiceQueryIdentity(cfg),
		},
	}}
}

func TestServiceRuntimeReadsSavedMappingWithoutTask(t *testing.T) {
	ctx := context.Background()
	app, action := serviceRuntimeFixture(t)
	var wantEnv string
	app.connections.transport = platformFixtureTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case genbuapi.LoginPath:
			return platformFixtureResponse(`{"code":"000000","data":{"token":"beast-fixture"}}`)
		case genbuapi.DragonUserPath:
			return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"token":%q}`, platformFixtureJWT()))
		case genbuapi.ApplicationsPath:
			return platformFixtureResponse(`{"code":20000,"response":[{"app_name":"fixture-runtime","app_id":876}]}`)
		case genbuapi.PodGroupPath, genbuapi.ConfigMapListPath:
			if req.Header.Get("App-Id") != "876" || req.URL.Query().Get("env") != wantEnv {
				return nil, fmt.Errorf("wrong Genbu destination: %s %s", req.Header.Get("App-Id"), req.URL.RawQuery)
			}
			if req.URL.Path == genbuapi.PodGroupPath {
				return platformFixtureResponse(`{"code":20000,"response":{"groups":[]}}`)
			}
			if req.URL.Query().Get("namespace") != "team" {
				return nil, errors.New("configuration request used the wrong namespace")
			}
			return platformFixtureResponse(`{"code":20000,"response":[{"update_time":"2026-11-19 09:00:00","encoded_data":[{"content":"enabled: true\npassword: fixture-secret\n"}]}]}`)
		default:
			return nil, fmt.Errorf("unexpected fixture path %s", req.URL.Path)
		}
	})
	for _, env := range []string{"uat", "prod"} {
		wantEnv = map[string]string{"uat": "UAT", "prod": "PRD"}[env]
		action.Kind, action.Payload["environment"] = "service.runtime.pods", env
		result, err := app.Dispatch(ctx, action)
		pods, ok := result.Data.(TaskServicePodsResult)
		if err != nil || !ok || pods.ServiceID != action.ServiceID || pods.Environment != env || pods.GenbuServiceName != "fixture-runtime" || len(pods.Pods) != 0 {
			t.Fatalf("catalog Pod read failed: %+v, %v", result, err)
		}
		action.Kind, action.Payload["namespace"] = "service.runtime.config", "team"
		result, err = app.Dispatch(ctx, action)
		config, ok := result.Data.(TaskServiceConfigResult)
		if err != nil || !ok || config.ServiceID != action.ServiceID || config.Environment != env || config.Namespace != "team" ||
			!strings.Contains(config.Text, "enabled: true") || strings.Contains(config.Text, "fixture-secret") {
			t.Fatalf("catalog redacted config read failed: %+v, %v", result, err)
		}
	}
	setTestGenbuEnabled(t, app, false)
	if _, err := app.Dispatch(ctx, action); err == nil || !strings.Contains(err.Error(), "Genbu") {
		t.Fatalf("disabled platform admitted catalog action: %v", err)
	}
}

func TestServiceRuntimeRejectsStaleOrUnavailableService(t *testing.T) {
	ctx := context.Background()
	app, action := serviceRuntimeFixture(t)
	wrongScope := action
	wrongScope.Payload = maps.Clone(action.Payload)
	owner := action.Payload[serviceRuntimeContextKey].(ServiceRuntimeContext)
	owner.Home = t.TempDir()
	wrongScope.Payload[serviceRuntimeContextKey] = owner
	reader := taskServicesFake{pods: func(context.Context, string, string) (production.ServicePods, error) {
		t.Fatal("invalid catalog target reached Genbu")
		return production.ServicePods{}, nil
	}}
	if _, err := app.taskServicesActionWithReader(ctx, wrongScope, reader); err == nil {
		t.Fatal("accepted catalog context from another data directory")
	}
	for _, state := range []struct {
		label string
		set   func(*domain.ServiceConfig)
	}{
		{"停用", func(service *domain.ServiceConfig) { service.Enabled = false }},
		{"探测", func(service *domain.ServiceConfig) { service.GenbuProbeEnabled, service.GenbuServiceName = false, "" }},
		{"映射", func(service *domain.ServiceConfig) { service.GenbuServiceName = "other-runtime" }},
	} {
		cfg, err := app.Config.LoadForPreview(ctx)
		if err != nil {
			t.Fatal(err)
		}
		service := &cfg.Services[0]
		service.Enabled, service.GenbuProbeEnabled, service.GenbuServiceName = true, true, "fixture-runtime"
		state.set(service)
		if err := app.Config.Save(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := app.taskServicesActionWithReader(ctx, action, reader); err == nil || !strings.Contains(err.Error(), state.label) {
			t.Fatalf("expected %s rejection: %v", state.label, err)
		}
	}
}

func TestServiceRuntimeApprovalQueryUsesSelectedServiceAndEnvironment(t *testing.T) {
	app, action := serviceRuntimeFixture(t)
	ctx := context.Background()
	action.Kind, action.Payload["environment"] = "service.runtime.approvals", "prod"
	const approvalID = 702
	item := production.GenbuApprovalItem{ID: approvalID, Environment: "prod", Service: "fixture-runtime"}
	client := genbuApprovalRouteFake{
		list: func(query production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error) {
			if query.Environment != "prod" || query.Service != "fixture-runtime" || query.Type != "7" {
				t.Fatalf("unexpected approval query %+v", query)
			}
			return production.GenbuApprovalPage{Items: []production.GenbuApprovalItem{item}}, nil
		},
		detail: func(env, service string, id int64) (production.GenbuApprovalDetail, error) {
			if env != "prod" || service != "fixture-runtime" || id != approvalID {
				t.Fatalf("unexpected approval detail target %s %s %d", env, service, id)
			}
			return production.GenbuApprovalDetail{GenbuApprovalItem: item}, nil
		},
	}
	result, err := app.taskServiceGenbuAction(ctx, action, client)
	page, ok := result.Data.(production.GenbuApprovalPage)
	if err != nil || !ok || len(page.Items) != 1 || page.Items[0].ID != approvalID {
		t.Fatalf("catalog approvals list failed: %+v %v", result, err)
	}
	action.Kind, action.Payload["approvalId"] = "service.runtime.approval-detail", fmt.Sprint(approvalID)
	result, err = app.taskServiceGenbuAction(ctx, action, client)
	detail, ok := result.Data.(production.GenbuApprovalDetail)
	if err != nil || !ok || detail.ID != approvalID {
		t.Fatalf("catalog approval detail failed: %+v %v", result, err)
	}
}

type serviceRuntimeConfigFake struct {
	genbuConfigSourceFake
	onAccountRead func() error
}

func (f *serviceRuntimeConfigFake) AccountIdentity(ctx context.Context, cfg domain.AppConfig) (string, error) {
	if f.onAccountRead != nil {
		if err := f.onAccountRead(); err != nil {
			return "", err
		}
	}
	return f.genbuConfigSourceFake.AccountIdentity(ctx, cfg)
}

func TestServiceRuntimeConfigEditRequiresCurrentConfirmedPreview(t *testing.T) {
	app, action := serviceRuntimeFixture(t)
	ctx := context.Background()
	const file, draft = "app.yml", "edited: true\n"
	snapshot := genbuTestConfigSnapshot("prod")
	snapshot.Service = "fixture-runtime"
	client := &serviceRuntimeConfigFake{}
	client.identity = "fixture-account"
	client.files = func(env, service string) (production.GenbuConfigFiles, error) {
		if env != "prod" || service != snapshot.Service {
			t.Fatalf("wrong file list target %s %s", env, service)
		}
		return production.GenbuConfigFiles{GenbuConfigSnapshot: snapshot, Files: []string{file}}, nil
	}
	client.source = func(env, service, filename string) (production.GenbuConfigFile, error) {
		if env != "prod" || service != snapshot.Service || filename != file {
			t.Fatalf("wrong source target %s %s %s", env, service, filename)
		}
		return production.GenbuConfigFile{GenbuConfigSnapshot: snapshot, File: file, Content: "edited: false\n"}, nil
	}
	client.preview = func(request production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
		if request.Environment != "prod" || request.Service != snapshot.Service || request.File != file || !request.StdinReplacement || string(request.Replacement) != draft {
			t.Fatalf("preview changed the catalog draft or target: %+v", request)
		}
		preview := genbuConfigTestPreview(request)
		preview.Service, preview.SnapshotID = snapshot.Service, snapshot.SnapshotID
		return preview, nil
	}
	client.submit = func(production.GenbuConfigEditRequest, string) (production.GenbuConfigEditResult, error) {
		t.Fatal("unconfirmed, discarded or stale catalog preview reached Genbu submission")
		return production.GenbuConfigEditResult{}, nil
	}
	action.Kind, action.Payload["environment"] = "service.runtime.config-files", "prod"
	result, err := app.taskServiceGenbuAction(ctx, action, client)
	files, ok := result.Data.(production.GenbuConfigFiles)
	if err != nil || !ok || len(files.Files) != 1 || files.Files[0] != file {
		t.Fatalf("catalog files failed: %+v %v", result, err)
	}
	read := genbuTestConfigSourceAction(action, file)
	read.Kind = "service.runtime.config-source"
	result, err = app.taskServiceGenbuAction(ctx, read, client)
	source, ok := result.Data.(TaskServiceConfigSourceResult)
	if err != nil || !ok || source.Content != "edited: false\n" {
		t.Fatalf("catalog source read failed: %+v %v", result, err)
	}
	preview, submit := genbuTestInlineConfigActions(action, source, draft)
	preview.Kind, submit.Kind = "service.runtime.config-preview", "service.runtime.config-submit"
	getToken := func() string {
		t.Helper()
		result, err := app.taskServiceGenbuAction(ctx, preview, client)
		data, ok := result.Data.(TaskServiceConfigPreviewResult)
		if err != nil || !ok || data.PreviewToken == "" {
			t.Fatalf("catalog preview failed: %+v %v", result, err)
		}
		return data.PreviewToken
	}
	submit.Payload["previewToken"], submit.Payload["confirmed"] = getToken(), false
	if _, err := app.taskServiceGenbuAction(ctx, submit, client); err == nil {
		t.Fatal("catalog PROD submission accepted without confirmation")
	}
	discard := action
	discard.Kind, discard.Payload = "service.runtime.config-preview-discard", maps.Clone(action.Payload)
	discard.Payload["previewToken"] = submit.Payload["previewToken"]
	if _, err := app.Dispatch(ctx, discard); err != nil {
		t.Fatal(err)
	}
	submit.Payload["confirmed"] = true
	if _, err := app.taskServiceGenbuAction(ctx, submit, client); !errors.Is(err, errGenbuConfigPreviewGone) {
		t.Fatalf("discarded token was usable: %v", err)
	}
	submit.Payload["previewToken"] = getToken()
	client.onAccountRead = func() error {
		_, err := app.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
			cfg.Services[0].GenbuServiceName = "changed-runtime"
			return cfg, nil
		})
		return err
	}
	if _, err := app.taskServiceGenbuAction(ctx, submit, client); err == nil || !strings.Contains(err.Error(), "映射") {
		t.Fatalf("mapping changed during account verification was not retired: %v", err)
	}
}

func TestServiceRuntimeUnknownSubmissionIsDeduplicatedAcrossEntries(t *testing.T) {
	app, task := taskServicesFixture(t)
	ctx := context.Background()
	cfg, err := app.Config.LoadForPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catalog := serviceRuntimeTestAction(app, cfg, "alias")
	catalogPreview, catalogSubmit := genbuConfigTestActions(catalog, "app.yml", "content", "edited: true\n")
	catalogPreview.Kind, catalogSubmit.Kind = "service.runtime.config-preview", "service.runtime.config-submit"
	taskPreview, taskSubmit := genbuConfigTestActions(task, "app.yml", "content", "edited: true\n")
	submits := 0
	client := &genbuEditRouteFake{identity: "fixture-account", preview: func(req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
		if req.Environment != "uat" || req.Service != "alias" {
			t.Fatalf("unexpected preview scope: %+v", req)
		}
		return genbuConfigTestPreview(req), nil
	}, submit: func(production.GenbuConfigEditRequest, string) (production.GenbuConfigEditResult, error) {
		submits++
		return production.GenbuConfigEditResult{}, production.ErrGenbuConfigSubmitUnknown
	}}
	for _, swap := range []struct{ preview, submit Action }{{catalogPreview, catalogSubmit}, {taskPreview, taskSubmit}} {
		result, err := app.taskServiceGenbuAction(ctx, swap.preview, client)
		data, ok := result.Data.(TaskServiceConfigPreviewResult)
		if err != nil || !ok || data.PreviewToken == "" {
			t.Fatalf("preview failed: %+v %v", result, err)
		}
		swap.submit.Payload["previewToken"] = data.PreviewToken
	}
	crossEntry := taskSubmit
	crossEntry.Payload = maps.Clone(taskSubmit.Payload)
	crossEntry.Payload["previewToken"] = catalogSubmit.Payload["previewToken"]
	if _, err := app.taskServiceGenbuAction(ctx, crossEntry, client); !errors.Is(err, errGenbuConfigPreviewGone) || submits != 0 {
		t.Fatalf("catalog token crossed to task entry: %v submits=%d", err, submits)
	}
	for _, submit := range []Action{catalogSubmit, taskSubmit} {
		var pending *GenbuConfigPendingError
		if _, err := app.taskServiceGenbuAction(ctx, submit, client); !errors.As(err, &pending) || pending.AttemptedAt.IsZero() || submits != 1 {
			t.Fatalf("unknown approval incorrectly retried by %s: %v submits=%d", submit.Kind, err, submits)
		}
	}
}
