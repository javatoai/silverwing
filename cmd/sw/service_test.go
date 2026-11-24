package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
	"silverwing-go/internal/snowball/genbuapi"
)

const cliServiceApprovalPath = "/genbu/basic/api/approve"
const cliServiceConfigDetailPath = "/genbu/basic/api/configMapDetail"

func TestServiceCLIRejectsMutationsAndInvalidScopeBeforeOpeningProfile(t *testing.T) {
	for _, args := range [][]string{
		{"config-edit", "--env", "uat", "--service", "svc"},
		{"config-submit", "--env", "uat", "--service", "svc"},
		{"approve", "--env", "uat", "--service", "svc"},
		{"config", "--env", "prod", "--service", "svc", "--set", "feature=true"},
		{"config", "--env", "prod", "--service", "svc", "--raw"},
		{"pods", "--env", "dev", "--service", "svc"},
		{"approval", "--env", "uat", "--service", "svc", "--id", "0"},
		{"approvals", "--env", "uat", "--service", "svc", "--start-date", "2026-11-12"},
		{"approvals", "--env", "uat", "--service", "svc", "--page-size", "101"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, stderr bytes.Buffer
			command := append([]string{"--json", "service"}, args...)
			if code := run(context.Background(), command, &out, &stderr); code != exitUsage || !strings.Contains(out.String(), `"ok":false`) {
				t.Fatalf("expected usage rejection, got code %d, stdout %q, stderr %q", code, out.String(), stderr.String())
			}
		})
	}
}

func TestServiceCLIGenbuReadsAndProductionApprovalDiff(t *testing.T) {
	var badScope, badBaseline atomic.Bool
	var configDetailReads atomic.Int32
	f, _ := newTagCLIStatusFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		q := r.URL.Query()
		switch r.URL.Path {
		case genbuapi.PodGroupPath, genbuapi.PodPath, genbuapi.ConfigMapListPath, cliServiceApprovalPath, cliServiceConfigDetailPath:
			if r.Method != http.MethodGet || (q.Get("env") != "PRD" && q.Get("env") != "UAT") {
				t.Errorf("unexpected business request %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return true
			}
		default:
			return false
		}

		env := q.Get("env")
		switch r.URL.Path {
		case genbuapi.PodGroupPath:
			fmt.Fprintf(w, `{"code":20000,"response":{"groups":[{"group_id":1,"namespace":"bp","match_labels":"app=svc","env":%q,"app_id":"101"}]}}`, env)
		case genbuapi.PodPath:
			fmt.Fprintf(w, `{"code":20000,"response":[{"pod_name":"fixture-pod","ready":true,"restart_count":0,"app_version":"1.2.3","env":%q,"app_id":"101"}]}`, env)
		case genbuapi.ConfigMapListPath:
			if q.Get("namespace") != "bp" || r.Header.Get("App-Id") != "101" {
				t.Errorf("config request lost namespace or service: %s", r.URL)
			}
			fmt.Fprintf(w, `{"code":20000,"response":[{"update_time":"2026-11-24 08:00:00","env":%q,"namespace":"bp","app_id":"101","encoded_data":[{"name":"app.yaml","content":"feature: current\npassword: fixture-current-secret\n"},{"name":"token.txt","content":"fixture-file-secret"}]}]}`, env)
		case cliServiceApprovalPath:
			if q.Get("approve_type") != "7" || r.Header.Get("App-Id") != "" {
				t.Errorf("unexpected approval request %s", r.URL)
			}
			if q.Get("keyword") != "" && (q.Get("keyword") != "svc-genbu" || q.Get("size") != "5" || q.Get("status") != "0" ||
				q.Get("date_begin") != "2026-11-13" || q.Get("date_end") != "2026-11-24") {
				t.Errorf("list filters were lost: %s", r.URL)
			}
			if badScope.Load() {
				env = "UAT"
			}
			fmt.Fprintf(w, `{"code":20000,"total":1,"response":[{"id":42,"env":%q,"app_name":"svc-genbu","approve_type":7,"status":0,"result":0,"reason":"change feature","content":{"name":"runtime","namespace":"bp","env":%q,"app_id":"101","encoded_data":[{"name":"app.yaml","content":"feature: proposed\npassword: fixture-proposed-secret\n"}]}}]}`, env, env)
		case cliServiceConfigDetailPath:
			configDetailReads.Add(1)
			if q.Get("name") != "runtime" || q.Get("namespace") != "bp" || q.Get("app_id") != "101" || r.Header.Get("App-Id") != "101" {
				t.Errorf("comparison lost the exact ConfigMap target: %s", r.URL)
			}
			if badBaseline.Load() {
				env = "UAT"
			}
			fmt.Fprintf(w, `{"code":20000,"response":{"name":"runtime","namespace":"bp","env":%q,"app_id":"101","encoded_data":[{"name":"app.yaml","content":"feature: current\npassword: fixture-current-secret\n"}]}}`, env)
		}
		return true
	})
	read := func(code int, command, env string, flags ...string) cliReply {
		t.Helper()
		args := append([]string{"service", command, "--env", env, "--service", "svc"}, flags...)
		reply, actual, err := f.callCommand(args...)
		if err != nil || actual != code || reply.OK != (code == 0) {
			t.Fatalf("%v returned exit %d instead of %d, error %q, err %v", args, actual, code, reply.Error, err)
		}
		for _, value := range []string{"fixture-current-secret", "fixture-proposed-secret", "fixture-file-secret", "fixture-password", "can_approve", "revision"} {
			if strings.Contains(string(reply.Data), value) || strings.Contains(reply.Error, value) {
				t.Fatalf("CLI exposed a secret or write-only field (%s)", value)
			}
		}
		return reply
	}
	for _, env := range []string{"uat", "prod"} {
		reply := read(0, "pods", env)
		var pods production.ServicePods
		if err := json.Unmarshal(reply.Data, &pods); err != nil || pods.Environment != env || len(pods.Pods) != 1 || pods.Pods[0].Name != "fixture-pod" {
			t.Fatalf("unexpected pod response for %s: %+v (%v)", env, pods, err)
		}
		reply = read(0, "config", env, "--namespace", "bp", "--file", "app.yaml")
		var config production.GenbuServiceConfig
		if err := json.Unmarshal(reply.Data, &config); err != nil || config.File != "app.yaml" || config.Environment != env ||
			!strings.Contains(config.Text, "feature: current") || !strings.Contains(config.Text, "<redacted>") {
			t.Fatalf("unexpected filtered config response for %s: %+v (%v)", env, config, err)
		}
		reply = read(0, "approvals", env, "--page-size", "5", "--status", "pending", "--start-date", "2026-11-13", "--end-date", "2026-11-24")
		var approvals production.GenbuApprovalPage
		if err := json.Unmarshal(reply.Data, &approvals); err != nil || approvals.PageSize != 5 || len(approvals.Items) != 1 ||
			approvals.Items[0].ID != 42 || approvals.Items[0].Environment != env {
			t.Fatalf("unexpected approval page for %s: %+v (%v)", env, approvals, err)
		}
		before := configDetailReads.Load()
		reply = read(0, "approval", env, "--id", "42")
		var approval application.CLIConfigApprovalResult
		if err := json.Unmarshal(reply.Data, &approval); err != nil || approval.ID != 42 || approval.Environment != env ||
			approval.Comparison != nil || len(approval.FileNames) != 1 || approval.FileNames[0] != "app.yaml" ||
			configDetailReads.Load() != before {
			t.Fatalf("unexpected approval detail for %s: %+v (%v)", env, approval, err)
		}
		reply = read(0, "approval-diff", env, "--id", "42")
		if err := json.Unmarshal(reply.Data, &approval); err != nil || approval.Comparison == nil || !approval.Comparison.Compared ||
			len(approval.Comparison.Changes) != 1 || !strings.Contains(approval.Comparison.Changes[0].Unified, "feature: proposed") ||
			!strings.Contains(approval.Comparison.Changes[0].Unified, "feature: current") {
			t.Fatalf("unexpected diff for %s: %+v (%v)", env, approval, err)
		}
	}
	badScope.Store(true)
	before := configDetailReads.Load()
	read(exitError, "approval-diff", "prod", "--id", "42")
	if configDetailReads.Load() != before {
		t.Fatal("an approval from the wrong environment triggered a comparison")
	}
	badScope.Store(false)
	badBaseline.Store(true)
	reply := read(exitError, "approval-diff", "prod", "--id", "42")
	var approval application.CLIConfigApprovalResult
	if err := json.Unmarshal(reply.Data, &approval); err != nil || approval.Comparison == nil || approval.Comparison.Compared || approval.Comparison.Note == "" {
		t.Fatalf("mismatched baseline appeared comparable: %+v (%v)", approval, err)
	}
}
