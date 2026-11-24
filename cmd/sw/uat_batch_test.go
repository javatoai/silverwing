package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/snowball/genbuapi"
)

func TestCLIRejectsInvalidUATBatchArguments(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "请指定 --batch"},
		{[]string{"--batch", " "}, "请指定 --batch"},
		{[]string{"--batch", "fixture", "--env", "prod"}, "-env"},
		{[]string{"--batch", "fixture", "--preflight", " "}, "请指定有效的预检编号"},
		{[]string{"--batch", "fixture", "extra"}, "存在多余参数"},
	} {
		var stderr bytes.Buffer
		args := append([]string{"uat", "release-batch"}, test.args...)
		if code := run(t.Context(), args, io.Discard, &stderr); code != exitUsage || !strings.Contains(stderr.String(), test.want) {
			t.Errorf("sw %v returned %d with error %q, want %q", args, code, stderr.String(), test.want)
		}
	}
}

func TestCLIUATBatchOutputKeepsConfirmationAndPartialReceipts(t *testing.T) {
	preview := application.CLIUATBatchResult{Task: "test-task", BatchID: "test-batch", Preflight: "whole-batch-confirmation",
		Items: []application.UATBatchReleaseRow{{Item: application.UATBatchReleaseItem{ServiceID: "svc", Tag: "1.2.3.beta-1"},
			Result: application.CLIUATOperationResult{Environment: "UAT", Status: "preview", Group: "fixture-uat", Namespace: "fixture-ns"}}},
		Skipped: []application.UATBatchReleaseItem{{ServiceID: "skipped", ModuleID: "module"}},
	}
	var out, stderr bytes.Buffer
	if code := (options{}).outputResult(&out, &stderr, preview, 0, nil); code != 0 {
		t.Fatalf("preview returned %d: %s", code, stderr.String())
	}
	for _, want := range []string{"whole-batch-confirmation", "fixture-uat", "fixture-ns", "UAT 批量发布", "跳过 skipped/module"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("batch output omitted %q: %s", want, out.String())
		}
	}
	out.Reset()
	settings := options{asJSON: true}
	if code := settings.outputResult(&out, &stderr, preview, 0, nil); code != 0 {
		t.Fatalf("JSON preview returned %d", code)
	}
	var reply cliReply
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil || !reply.OK || reply.PreflightID != preview.Preflight {
		t.Fatalf("invalid preview: %s (%v)", out.String(), err)
	}
	out.Reset()
	preview.Preflight = ""
	preview.Items[0].Result.Status = "unknown"
	preview.Items[0].Error = "待核实"
	preview.Items = append(preview.Items, application.UATBatchReleaseRow{Result: application.CLIUATOperationResult{Status: "submitted"}})
	if code := settings.outputResult(&out, &stderr, preview, 0, errors.New("批量发布未全部提交")); code != exitError {
		t.Fatalf("partial submission returned %d", code)
	}
	reply = cliReply{}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil || reply.OK || reply.PreflightID != "" || reply.Error == "" {
		t.Fatalf("invalid partial receipt: %s (%v)", out.String(), err)
	}
	var receipt application.CLIUATBatchResult
	if err := json.Unmarshal(reply.Data, &receipt); err != nil || len(receipt.Items) != 2 ||
		receipt.Items[0].Result.Status != "unknown" || receipt.Items[1].Result.Status != "submitted" {
		t.Fatalf("partial result lost item statuses: %s (%v)", reply.Data, err)
	}
}

func TestCLIUATBatchReleaseAcrossProcessesUsesEmbeddedGenbu(t *testing.T) {
	var releases atomic.Int32
	fixture, batchID := newTagCLIStatusFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case genbuapi.BaseInfoPath:
			if r.Method != http.MethodGet {
				t.Errorf("expected enabled UAT step to stay read-only, got %s", r.Method)
			}
			fmt.Fprint(w, `{"code":20000,"response":[{"step":1,"status":1},{"step":7,"step_name":"UAT","status":0,"enabled":1}]}`)
		case genbuapi.UATReleaseGroupsPath:
			if r.Method != http.MethodGet || r.URL.Query().Get("env") != "UAT" {
				t.Errorf("batch queried the wrong release Groups: %s %s", r.Method, r.URL)
			}
			fmt.Fprint(w, `{"code":20000,"response":[{"group_id":1,"pod_group_name":"svc-uat","k8s_namespace":"bp","env":"UAT","app_id":101}]}`)
		case genbuapi.UATDeployInfoPath:
			if r.Method != http.MethodGet || r.URL.Query().Get("group_id") != "1" {
				t.Errorf("batch read deploy info for the wrong Group: %s %s", r.Method, r.URL)
			}
			fmt.Fprint(w, `{"code":20000,"response":{"group_id":1,"env":"UAT","k8s_namespace":"bp","pod_num":1,"auto_rollback":false,"mount_filebeat":false,"live_check_form":{"port":8080},"ready_check_form":{"port":8080},"startup_check_form":{"port":8080}}}`)
		case genbuapi.UATReleasePath:
			releases.Add(1)
			var body struct {
				Environment string `json:"env"`
				VersionID   int    `json:"version_id"`
				GroupIDs    []int  `json:"group_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Method != http.MethodPost ||
				body.Environment != "UAT" || body.VersionID != 3 || len(body.GroupIDs) != 1 || body.GroupIDs[0] != 1 {
				t.Errorf("batch used the wrong write scope: %s %+v (%v)", r.Method, body, err)
			}
			fmt.Fprint(w, `{"code":20000}`)
		default:
			return false
		}
		return true
	})
	args := []string{"uat", "release-batch", "--batch", batchID}
	preview, code, err := fixture.callCommand(args...)
	if err != nil || code != 0 || !preview.OK || preview.PreflightID == "" || releases.Load() != 0 {
		t.Fatalf("expected a read-only CLI preview, code = %d, reply = %+v, error = %v, writes = %d", code, preview, err, releases.Load())
	}
	args = append(args, "--task", "status-task", "--preflight", preview.PreflightID)
	for _, wantCode := range []int{0, exitError} {
		reply, code, err := fixture.callCommand(args...)
		var receipt application.CLIUATBatchResult
		decodeErr := json.Unmarshal(reply.Data, &receipt)
		if err != nil || decodeErr != nil || code != wantCode || len(receipt.Items) != 1 || releases.Load() != 1 ||
			receipt.Items[0].Result.Status != "submitted" || receipt.Items[0].Result.Namespace != "bp" {
			t.Fatalf("CLI submit/replay exit = %d, want %d, receipt = %+v, errors = %v / %v, writes = %d", code, wantCode, receipt, err, decodeErr, releases.Load())
		}
	}
}
