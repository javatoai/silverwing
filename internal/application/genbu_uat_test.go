package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/snowball/auth"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
)

const uatFixtureTag = "1.5.3.beta-1"

type uatHTTPFixture struct {
	service       string
	podID         atomic.Int32
	writes        atomic.Int32
	respond       func() (*http.Response, error)
	uatDisabled   atomic.Bool
	enableWrites  atomic.Int32
	enableRespond func() (*http.Response, error)
	releasePods   atomic.Int32
}

func (f *uatHTTPFixture) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case genbuapi.LoginPath:
		return platformFixtureResponse(`{"code":"000000","data":{"token":"beast-fixture"}}`)
	case genbuapi.DragonUserPath:
		return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"token":%q}`, platformFixtureJWT()))
	case genbuapi.ApplicationsPath:
		return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"response":[{"app_name":%q,"app_id":876}]}`, f.service))
	}
	if req.Header.Get("App-Id") != "876" || req.Header.Get("X-Token") == "" {
		return nil, fmt.Errorf("request used the wrong service or platform account: %s", req.URL.Path)
	}
	const group = `{"group_id":17,"pod_group_name":"fixture-group","k8s_namespace":"fixture-ns","match_labels":"app=fixture","env":"UAT","app_id":876}`
	switch req.URL.Path {
	case genbuapi.VersionPath:
		return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"total":1,"response":[{"version_id":42,"version_name":%q}]}`, uatFixtureTag))
	case genbuapi.BaseInfoPath:
		if req.Method == http.MethodPost {
			return f.enableUAT(req)
		}
		if req.URL.Query().Get("version_id") != "42" {
			return nil, errors.New("build or enable verification queried the wrong Tag")
		}
		enabled := 1
		if f.uatDisabled.Load() {
			enabled = 0
		}
		return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"response":[{"step":1,"status":1},{"step":7,"step_name":"UAT","status":0,"enabled":%d}]}`, enabled))
	case genbuapi.UATReleaseGroupsPath:
		if req.URL.Query().Get("env") != "UAT" {
			return nil, errors.New("release group query escaped UAT")
		}
		return platformFixtureResponse(`{"code":20000,"response":[` + group + `]}`)
	case genbuapi.UATDeployInfoPath:
		if req.URL.Query().Get("group_id") != "17" {
			return nil, errors.New("release settings queried the wrong group")
		}
		podCount := max(2, f.releasePods.Load())
		return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"response":{"group_id":17,"env":"UAT","k8s_namespace":"fixture-ns","pod_num":%d,"auto_rollback":false,"mount_filebeat":false,"live_check_form":{"port":8080},"ready_check_form":{"port":8080},"startup_check_form":{"port":8080}}}`, podCount))
	case genbuapi.PodGroupPath:
		if req.URL.Query().Get("env") != "UAT" {
			return nil, errors.New("Pod group query escaped UAT")
		}
		return platformFixtureResponse(`{"code":20000,"response":{"groups":[` + group + `]}}`)
	case genbuapi.PodPath:
		if req.URL.Query().Get("env") != "UAT" || req.URL.Query().Get("group_id") != "17" {
			return nil, errors.New("Pod query escaped the selected UAT group")
		}
		return platformFixtureResponse(fmt.Sprintf(`{"code":20000,"response":[{"pod_id":%d,"pod_name":"fixture-pod","restart_count":0}]}`, f.podID.Load()+1))
	case genbuapi.UATReleasePath, genbuapi.UATRedeployPath:
		f.writes.Add(1)
		if req.URL.Path == genbuapi.UATReleasePath && f.uatDisabled.Load() {
			return nil, errors.New("release dispatched before UAT enable was verified")
		}
		var payload struct {
			Environment string          `json:"env"`
			Group       json.RawMessage `json:"group_id"`
			Version     int             `json:"version_id"`
			Redeploy    bool            `json:"deleteDeployment"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if payload.Environment != "UAT" ||
			(req.URL.Path == genbuapi.UATReleasePath && (req.Method != http.MethodPost || string(payload.Group) != "[17]" || payload.Version != 42)) ||
			(req.URL.Path == genbuapi.UATRedeployPath && (req.Method != http.MethodPatch || string(payload.Group) != "17" || !payload.Redeploy)) {
			return nil, fmt.Errorf("unexpected Genbu UAT mutation %+v", payload)
		}
		if f.respond != nil {
			return f.respond()
		}
		return platformFixtureResponse(`{"code":20000}`)
	default:
		return nil, fmt.Errorf("unexpected fixture request %s %s", req.Method, req.URL.Path)
	}
}

func (f *uatHTTPFixture) enableUAT(req *http.Request) (*http.Response, error) {
	f.enableWrites.Add(1)
	var payload struct {
		Version     int    `json:"version_id"`
		Environment string `json:"env"`
		Enable      int    `json:"enable"`
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Version != 42 || payload.Environment != "UAT" || payload.Enable != 1 {
		return nil, fmt.Errorf("unexpected Genbu UAT enable mutation: %+v, %v", payload, err)
	}
	if f.enableRespond != nil {
		return f.enableRespond()
	}
	f.uatDisabled.Store(false)
	return platformFixtureResponse(`{"code":20000}`)
}

func uatCatalogFixture(t *testing.T) (*App, Action) {
	t.Helper()
	app, action := serviceRuntimeFixture(t)
	id, err := storage.LoadOrCreateProfileID(app.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	app.Paths.ProfileID = id
	return app, action
}

func previewUATCLI(t *testing.T, app *App, action string) CLIUATOperationResult {
	t.Helper()
	request := CLIUATOperationRequest{Action: action, Service: "service"}
	if action == "release" {
		request.Tag = uatFixtureTag
	}
	result, err := app.CLIUATOperation(t.Context(), request)
	if err != nil || result.Status != "preview" || result.Preflight == "" {
		t.Fatalf("UAT preview = %+v, error = %v", result, err)
	}
	return result
}

func TestUATPodDesktopReleaseAndCLIConfirmSameProfileOnce(t *testing.T) {
	app, action := uatCatalogFixture(t)
	fixture := &uatHTTPFixture{service: "fixture-runtime"}
	app.connections.transport = fixture
	action.Kind = "service.runtime.uat-tags"
	action.Payload["keyword"] = "1.5"
	action.Payload["page"] = 2
	list, err := app.Dispatch(t.Context(), action)
	if page, ok := list.Data.(production.GenbuTagPage); err != nil || !ok || page.Page != 2 || len(page.Items) != 1 || page.Items[0].Tag != uatFixtureTag {
		t.Fatalf("UAT Tag choices = %+v, error = %v", list, err)
	}
	action.Kind = "service.runtime.uat-preview"
	action.Payload["operation"], action.Payload["tag"] = "release", uatFixtureTag
	display, err := app.Dispatch(t.Context(), action)
	preview, ok := display.Data.(CLIUATOperationResult)
	if err != nil || !ok || preview.Service != "Catalog service" || preview.GenbuService != fixture.service ||
		preview.Tag != uatFixtureTag || preview.Environment != "UAT" || preview.Namespace != "fixture-ns" || preview.Group != "fixture-group" ||
		preview.Status != "preview" || preview.PodCount != 2 || preview.Preflight == "" || fixture.writes.Load() != 0 {
		t.Fatalf("desktop preview = %+v, error = %v, write count = %d", display, err, fixture.writes.Load())
	}
	if _, err := os.Stat(filepath.Join(app.Paths.Home, "genbu-uat-submissions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only preview created a submission directory: %v", err)
	}
	cli, err := NewUATCLI(app.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	cli.connections.transport = fixture
	request := CLIUATOperationRequest{Action: "release", Service: "Catalog service", Tag: uatFixtureTag, Preflight: preview.Preflight}
	if result, err := cli.CLIUATOperation(t.Context(), request); err != nil || result.Status != "submitted" || fixture.writes.Load() != 1 {
		t.Fatalf("CLI release = %+v, error = %v, write count = %d", result, err, fixture.writes.Load())
	}
	if result, err := app.CLIUATOperation(t.Context(), request); err == nil || result.Status != "submitted" || fixture.writes.Load() != 1 {
		t.Fatalf("the same confirmation replayed: %+v, error = %v, write count = %d", result, err, fixture.writes.Load())
	}
}

func TestUATPodReleaseRecordsEnableAndDeploymentOutcomesSeparately(t *testing.T) {
	for _, tc := range []struct {
		name         string
		wantStatus   string
		wantEnable   string
		enableWrites int32
		deployWrites int32
	}{
		{name: "unknown switch", wantStatus: "not-sent", wantEnable: "unknown", enableWrites: 1},
		{name: "rejected deploy", wantStatus: "rejected", wantEnable: "enabled", enableWrites: 1, deployWrites: 1},
		{name: "changed deployment target after enable", wantStatus: "not-sent", wantEnable: "enabled", enableWrites: 1},
		{name: "unknown deploy", wantStatus: "unknown", wantEnable: "enabled", enableWrites: 1, deployWrites: 1},
		{name: "already enabled by submit", wantStatus: "submitted", deployWrites: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := uatCatalogFixture(t)
			fixture := &uatHTTPFixture{service: "fixture-runtime"}
			fixture.uatDisabled.Store(true)
			fixture.enableRespond = func() (*http.Response, error) {
				directory := filepath.Join(app.Paths.Home, "genbu-uat-submissions")
				files, err := os.ReadDir(directory)
				if err != nil || len(files) != 1 {
					t.Errorf("enable sent before local reservation: files = %d, error = %v", len(files), err)
				} else if receipt, err := readUATPodReceipt(filepath.Join(directory, files[0].Name())); !errors.Is(err, production.ErrUATPodSubmitUnknown) || receipt.Status != "unknown" || receipt.EnableStatus != "unknown" {
					t.Errorf("initial reservation must preserve both uncertain stages: %+v, error = %v", receipt, err)
				}
				if tc.name == "unknown switch" {
					return nil, io.ErrUnexpectedEOF
				}
				fixture.uatDisabled.Store(false)
				if tc.name == "changed deployment target after enable" {
					fixture.releasePods.Store(3)
				}
				return platformFixtureResponse(`{"code":20000}`)
			}
			switch tc.name {
			case "rejected deploy":
				fixture.respond = func() (*http.Response, error) {
					response, _ := platformFixtureResponse(`{"code":40301}`)
					response.StatusCode = http.StatusForbidden
					return response, nil
				}
			case "unknown deploy":
				fixture.respond = func() (*http.Response, error) { return nil, io.ErrUnexpectedEOF }
			}
			app.connections.transport = fixture
			preview := previewUATCLI(t, app, "release")
			if preview.EnableStatus != "required" || fixture.enableWrites.Load() != 0 || fixture.writes.Load() != 0 {
				t.Fatalf("disabled Tag preview = %+v, switch requests = %d, release requests = %d", preview, fixture.enableWrites.Load(), fixture.writes.Load())
			}
			if tc.name == "already enabled by submit" {
				fixture.uatDisabled.Store(false)
			}
			request := CLIUATOperationRequest{Action: "release", Service: "service", Tag: uatFixtureTag, Preflight: preview.Preflight}
			result, err := app.CLIUATOperation(t.Context(), request)
			if result.Status != tc.wantStatus || result.EnableStatus != tc.wantEnable || fixture.enableWrites.Load() != tc.enableWrites || fixture.writes.Load() != tc.deployWrites || (err == nil) != (tc.wantStatus == "submitted") {
				t.Fatalf("deploy outcome = %+v, error = %v, switch requests = %d, release requests = %d", result, err, fixture.enableWrites.Load(), fixture.writes.Load())
			}
			if tc.wantEnable == "enabled" && err != nil && !strings.Contains(err.Error(), "UAT 发布开关已自动开启") {
				t.Fatalf("error lost the enable outcome: %v", err)
			}
			if tc.wantStatus == "not-sent" {
				var notSent genbuapi.RequestNotSentError
				if !errors.As(err, &notSent) || errors.Is(err, production.ErrUATPodSubmitUnknown) {
					t.Fatalf("undispatched release lost its classification: %v", err)
				}
			}
			replay, err := app.CLIUATOperation(t.Context(), request)
			if err == nil || replay != result || fixture.enableWrites.Load() != tc.enableWrites || fixture.writes.Load() != tc.deployWrites {
				t.Fatalf("replay did not preserve both outcomes: %+v, error = %v, switch requests = %d, release requests = %d", replay, err, fixture.enableWrites.Load(), fixture.writes.Load())
			}
		})
	}
}

func TestUATPodUnknownAndGroupLockAcrossProfiles(t *testing.T) {
	first, _ := uatCatalogFixture(t)
	second, _ := uatCatalogFixture(t)
	entered, returnReceipt := make(chan struct{}, 1), make(chan struct{}, 1)
	t.Cleanup(func() { close(returnReceipt) })
	fixture := &uatHTTPFixture{service: "fixture-runtime"}
	fixture.respond = func() (*http.Response, error) {
		if fixture.writes.Load() == 1 {
			entered <- struct{}{}
			<-returnReceipt
		}
		return nil, io.ErrUnexpectedEOF
	}
	first.connections.transport, second.connections.transport = fixture, fixture
	preview := previewUATCLI(t, first, "restart")
	request := CLIUATOperationRequest{Action: "restart", Service: "service", Preflight: preview.Preflight}
	if _, err := second.CLIUATOperation(t.Context(), request); !errors.Is(err, production.ErrUATPodPreflightStale) {
		t.Fatalf("other profile accepted a foreign preview: %v", err)
	}
	other := previewUATCLI(t, second, "release")
	type response struct {
		result CLIUATOperationResult
		err    error
	}
	outcome := make(chan response, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() {
		result, err := first.CLIUATOperation(ctx, request)
		outcome <- response{result, err}
	}()
	select {
	case <-entered:
	case early := <-outcome:
		t.Fatalf("restart did not reach the mock mutation: %+v %v", early.result, early.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	otherRequest := CLIUATOperationRequest{Action: "release", Service: "service", Tag: uatFixtureTag, Preflight: other.Preflight}
	if _, err := second.CLIUATOperation(ctx, otherRequest); !errors.Is(err, storage.ErrLockBusy) || fixture.writes.Load() != 1 {
		t.Fatalf("another profile sent a simultaneous mutation: %v, writes = %d", err, fixture.writes.Load())
	}
	returnReceipt <- struct{}{}
	select {
	case got := <-outcome:
		if !errors.Is(got.err, production.ErrUATPodSubmitUnknown) || got.result.Status != "unknown" || got.result.GroupID != 17 || got.result.PodCount != 1 {
			t.Fatalf("unconfirmed restart receipt = %+v, error = %v", got.result, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if result, err := first.CLIUATOperation(t.Context(), request); !errors.Is(err, production.ErrUATPodSubmitUnknown) || result.Status != "unknown" || fixture.writes.Load() != 1 {
		t.Fatalf("unknown update replayed: %+v, error = %v, writes = %d", result, err, fixture.writes.Load())
	}
}

func TestUATPodConfirmationRejectsChangedPodsAccountAndService(t *testing.T) {
	app, _ := uatCatalogFixture(t)
	fixture := &uatHTTPFixture{service: "fixture-runtime"}
	app.connections.transport = fixture
	preview := previewUATCLI(t, app, "restart")
	request := CLIUATOperationRequest{Action: "restart", Service: "service", Preflight: preview.Preflight}
	fixture.podID.Add(1)
	if _, err := app.CLIUATOperation(t.Context(), request); !errors.Is(err, production.ErrUATPodPreflightStale) {
		t.Fatalf("changed pods accepted a stale preview: %v", err)
	}
	request.Preflight = previewUATCLI(t, app, "restart").Preflight
	if _, err := app.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.Services[0].GenbuServiceName = "stale-alias"
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CLIUATOperation(t.Context(), request); !errors.Is(err, production.ErrUATPodPreflightStale) {
		t.Fatalf("changed service accepted a stale preview: %v", err)
	}
	if _, err := app.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.Services[0].GenbuServiceName = fixture.service
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	store, err := auth.UserStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(auth.Credentials{Username: "fixture", Password: "changed fixture password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CLIUATOperation(t.Context(), request); !errors.Is(err, production.ErrUATPodPreflightStale) || fixture.writes.Load() != 0 {
		t.Fatalf("changed shared account accepted the old preview: %v, writes = %d", err, fixture.writes.Load())
	}
}

func TestUATPodHistoryRequiresExactCurrentRecordAndActiveTask(t *testing.T) {
	app, base := taskServicesFixture(t)
	fixture := &uatHTTPFixture{service: "alias"}
	app.connections.transport = fixture
	files := base.Payload["taskServicesContext"].(TaskServicesContext).TaskFilesContext
	tag := uatFixtureTag
	op := domain.TagOperation{
		OperationID: delivery.NewTagJobID(), GroupServiceID: "service", ModuleID: "one", ServiceName: "service", FolderName: base.TaskID,
		RepositoryID: "repo", SourceBranch: "feature/fixture", Remote: "origin", TagMode: domain.TagBuildCurrentBranch,
		State: domain.TagOperationSuccess, CreatedAt: "2026-11-19T08:00:00Z", UpdatedAt: "2026-11-19T08:00:00Z", Tag: &tag, ConflictFiles: []string{},
		GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown},
	}
	saveTagProbeFixture(t, files.TaskPath, op)
	action := Action{Kind: "tag.history.uat-preview", TaskID: base.TaskID, ServiceID: "service", ModuleID: "one", Payload: map[string]any{
		"tagHistoryHome": files.Home, "tagHistoryTaskRoot": files.TaskRoot, "tagHistoryTaskPath": files.TaskPath,
		"tagHistoryTaskCreatedAt": files.TaskCreatedAt, "operationId": op.OperationID, "operation": "release", "tag": "unrelated-tag",
	}}
	if _, err := app.Dispatch(t.Context(), action); err == nil {
		t.Fatal("Tag history preview accepted an unrelated Tag")
	}
	action.Payload["tag"] = tag
	result, err := app.Dispatch(t.Context(), action)
	preview, ok := result.Data.(CLIUATOperationResult)
	if err != nil || !ok || preview.Preflight == "" || preview.Tag != tag || fixture.writes.Load() != 0 {
		t.Fatalf("history preview = %+v, error = %v", result, err)
	}
	manifest, err := app.Manifests.Load(files.TaskPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.LifecycleStatus = domain.TaskArchived
	if err := app.Manifests.Save(files.TaskPath, manifest); err != nil {
		t.Fatal(err)
	}
	action.Kind, action.Payload["preflight"] = "tag.history.uat-submit", preview.Preflight
	if _, err := app.Dispatch(t.Context(), action); err == nil || !strings.Contains(err.Error(), "已归档") || fixture.writes.Load() != 0 {
		t.Fatalf("archived task dispatched UAT write: %v, write count = %d", err, fixture.writes.Load())
	}
}
