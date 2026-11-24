package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
	"silverwing-go/internal/storage"
)

func TestCLIRejectsInvalidUATCommands(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing service", []string{"uat", "restart"}, "请指定 --service"},
		{"missing tag", []string{"uat", "release", "--service", "svc"}, "请指定 --tag"},
		{"tag on restart", []string{"uat", "restart", "--service", "svc", "--tag", "1.2.3"}, "tag"},
		{"environment override", []string{"uat", "restart", "--service", "svc", "--environment", "prod"}, "environment"},
		{"empty confirmation", []string{"uat", "restart", "--service", "svc", "--preflight", " "}, "请指定有效的预检编号"},
		{"extra argument", []string{"uat", "restart", "--service", "svc", "extra"}, "存在多余参数"},
		{"directory alias conflict", []string{"--conf-dir", first, "uat", "restart", "--service", "svc", "--home", second}, "--conf-dir 和 --home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			if code := run(context.Background(), tc.args, io.Discard, &errOut); code != exitUsage || !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("exit = %d, stderr = %q, expected %q", code, errOut.String(), tc.want)
			}
		})
	}
}

func TestCLIUATOutputKeepsPreflightAndUncertainReceipt(t *testing.T) {
	result := application.CLIUATOperationResult{
		Action: "restart", Status: "preview", Service: "svc", GenbuService: "svc", Environment: "uat",
		Group: "target group", GroupID: 71, Namespace: "sample", PodCount: 3, Preflight: "preview-1",
	}
	var out, stderr bytes.Buffer
	if code := (options{}).outputResult(&out, &stderr, result, 0, nil); code != 0 {
		t.Fatalf("preview returned exit code %d", code)
	}
	for _, want := range []string{"UAT 预检通过", "整个 Group", "3 个 Pod", "preview-1", "--preflight", "sample"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview omitted %q: %s", want, out.String())
		}
	}

	out.Reset()
	o := options{asJSON: true}
	o.outputResult(&out, &stderr, result, 0, nil)
	var response struct {
		OK          bool
		Data        application.CLIUATOperationResult
		PreflightID string
		Error       string
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || !response.OK ||
		response.Data.GroupID != 71 || response.PreflightID != "preview-1" {
		t.Fatalf("invalid preview response: %s (%v)", out.String(), err)
	}

	out.Reset()
	result.Status, result.Preflight = "unknown", ""
	if code := o.outputResult(&out, &stderr, result, 0, errors.New("待核实")); code != exitError {
		t.Fatalf("uncertain write returned exit code %d", code)
	}
	response.Data = application.CLIUATOperationResult{}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.OK ||
		response.Data.Status != "unknown" || response.Data.GroupID != 71 || response.Error == "" {
		t.Fatalf("uncertain write lost its target or status: %s (%v)", out.String(), err)
	}
}

func TestCLIUATPreviewAndSubmitWithEmbeddedGenbu(t *testing.T) {
	root := t.TempDir()
	userHome := isolateTagTestUserHome(t, root)
	paths := newTagCLIProfilePaths(t, root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const (
		tag   = "1.2.3.beta-1"
		group = `{"group_id":1,"pod_group_name":"svc-uat","k8s_namespace":"bp","match_labels":"app=svc"}`
	)
	var enables, enabledReads, releases, restarts atomic.Int32
	legacyGenbu, _ := tagTestGenbuFixture(t, userHome, executable, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case genbuapi.BaseInfoPath:
			switch r.Method {
			case http.MethodGet:
				enabled := 0
				if enables.Load() > 0 {
					enabled = 1
					enabledReads.Add(1)
				}
				fmt.Fprintf(w, `{"code":20000,"response":[{"step":1,"status":1},{"step":7,"step_name":"UAT","enabled":%d,"status":0}]}`, enabled)
			case http.MethodPost:
				var body struct {
					VersionID int    `json:"version_id"`
					Env       string `json:"env"`
					Enable    int    `json:"enable"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.VersionID != 3 ||
					body.Env != "UAT" || body.Enable != 1 || r.Header.Get("App-Id") != "101" {
					t.Errorf("unexpected UAT switch request: %+v (%v)", body, err)
				}
				enables.Add(1)
				fmt.Fprint(w, `{"code":20000}`)
			default:
				t.Errorf("unexpected UAT switch method %s", r.Method)
				http.NotFound(w, r)
			}
		case genbuapi.PodGroupPath:
			fmt.Fprint(w, `{"code":20000,"response":{"groups":[`+group+`]}}`)
		case genbuapi.UATReleaseGroupsPath:
			if r.URL.Query().Get("env") != "UAT" {
				t.Error("expected UAT release group")
			}
			fmt.Fprint(w, `{"code":20000,"response":[`+group+`]}`)
		case genbuapi.UATDeployInfoPath:
			fmt.Fprint(w, `{"code":20000,"response":{"group_id":1,"env":"UAT","k8s_namespace":"bp","pod_num":1,"auto_rollback":false,"mount_filebeat":false,"live_check_form":{"port":8080},"ready_check_form":{"port":8080},"startup_check_form":{"port":8080}}}`)
		case genbuapi.UATReleasePath, genbuapi.UATRedeployPath:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["env"] != "UAT" || r.Header.Get("App-Id") != "101" {
				t.Errorf("unexpected UAT target for %s: %+v (%v)", r.URL.Path, body, err)
			}
			if r.URL.Path == genbuapi.UATReleasePath {
				releases.Add(1)
				if enables.Load() != 1 || enabledReads.Load() == 0 {
					t.Error("release did not verify the single UAT switch write")
				}
				if r.Method != http.MethodPost || body["version_id"] != float64(3) || !reflect.DeepEqual(body["group_id"], []any{float64(1)}) {
					t.Errorf("unexpected release payload: %s %+v", r.Method, body)
				}
			} else {
				restarts.Add(1)
				if r.Method != http.MethodPatch || body["group_id"] != float64(1) || body["deleteDeployment"] != true {
					t.Errorf("unexpected restart payload: %s %+v", r.Method, body)
				}
			}
			fmt.Fprint(w, `{"code":20000}`)
		default:
			return false
		}
		return true
	})
	cfg := domain.DefaultAppConfig()
	cfg.GenbuExecutablePath = &legacyGenbu
	repo := filepath.Join(root, "repository")
	cfg.Repositories = []domain.RepositoryConfig{{
		ID: "repo", Name: "repo", RootPath: repo, GitCommonDirectory: filepath.Join(repo, ".git"),
	}}
	cfg.Services = []domain.ServiceConfig{{
		ID: "svc", DisplayName: "Fixture Service", RepositoryID: "repo", Enabled: true, GenbuServiceName: "svc-genbu", GenbuProbeEnabled: true,
		MasterBranch: "origin/main", TestTagBaselineRef: "origin/main", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch}},
	}}
	if err := storage.NewConfigStore(paths).Save(cfg); err != nil {
		t.Fatal(err)
	}
	fixture := cliFixture{executable: executable, confDir: paths.Home}
	call := func(wantCode int, args ...string) (application.CLIUATOperationResult, cliReply) {
		t.Helper()
		reply, code, err := fixture.callCommand(append([]string{"uat"}, args...)...)
		if err != nil || code != wantCode || reply.OK != (wantCode == 0) {
			t.Fatalf("sw uat %v exit = %d, reply = %s, error = %s (%v)", args, code, reply.Data, reply.Error, err)
		}
		var result application.CLIUATOperationResult
		if err := json.Unmarshal(reply.Data, &result); err != nil {
			t.Fatalf("invalid UAT result: %v", err)
		}
		return result, reply
	}

	for i, operation := range []struct {
		args   []string
		writes *atomic.Int32
	}{
		{[]string{"release", "--service", "Fixture Service", "--tag", tag}, &releases},
		{[]string{"restart", "--service", "svc"}, &restarts},
	} {
		preview, reply := call(0, operation.args...)
		if preview.Status != "preview" || preview.Action != operation.args[0] || preview.GroupID != 1 ||
			preview.Namespace != "bp" || preview.PodCount != 1 || reply.PreflightID == "" ||
			releases.Load()+restarts.Load() != int32(i) || enables.Load() != int32(i) {
			t.Fatalf("unexpected or mutating preview: %+v", preview)
		}
		if operation.args[0] == "release" && preview.EnableStatus != "required" {
			t.Fatalf("disabled UAT preview omitted the enable step: %+v", preview)
		}
		args := append(operation.args, "--preflight", reply.PreflightID)
		submitted, _ := call(0, args...)
		if submitted.Status != "submitted" || submitted.Tag != preview.Tag || operation.writes.Load() != 1 {
			t.Fatalf("submission = %+v, writes = %d", submitted, operation.writes.Load())
		}
		if operation.args[0] == "release" && submitted.EnableStatus != "enabled" {
			t.Fatalf("submission omitted the verified UAT switch: %+v", submitted)
		}
		duplicate, _ := call(exitError, args...)
		if duplicate.Status != "submitted" || operation.writes.Load() != 1 {
			t.Fatalf("duplicate submission = %+v, writes = %d", duplicate, operation.writes.Load())
		}
	}
	if enables.Load() != 1 {
		t.Fatalf("UAT switch enabled %d times, want one", enables.Load())
	}
}
