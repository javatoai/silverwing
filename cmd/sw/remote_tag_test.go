package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

func TestRemoteTagCLIQueriesAndRedactsWithEmbeddedGenbu(t *testing.T) {
	const tag = "2.1.0.beta-2"
	fixture, _ := newTagCLIStatusFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case genbuapi.VersionPath:
			if r.Method != http.MethodPost {
				t.Errorf("unexpected version query method %s", r.Method)
			}
			fmt.Fprintf(w, `{"code":20000,"total":1,"response":[{"version_id":3,"version_name":%q,"created_at":"2026-11-24 10:00:00"}]}`, tag)
		case genbuapi.BuildLogPath:
			if r.Method != http.MethodGet || r.URL.Query().Get("version_id") != "3" || r.URL.Query().Get("tail_lines") != "42" {
				t.Errorf("build log was not bound to the exact Tag and line limit: %s %s", r.Method, r.URL.RequestURI())
			}
			fmt.Fprintf(w, `{"code":20000,"response":%q}`, "fixture build\npassword=fixture-password\n")
		default:
			return false
		}
		return true
	})
	list, _ := callTag[application.CLIRemoteTagResult](t, fixture, 0, "remote-list", "--service", "svc", "--page", "2")
	if list.Page == nil || list.Page.Page != 2 || len(list.Page.Items) != 1 || list.Page.Items[0].Tag != tag {
		t.Fatalf("wrong Tag page: %+v", list)
	}
	status, _ := callTag[application.CLIRemoteTagResult](t, fixture, 0, "query", "--service", "svc-genbu", "--tag", tag)
	if status.Status == nil || status.Status.Build != domain.GenbuSuccess || status.Status.Production != domain.GenbuInitial {
		t.Fatalf("wrong Tag status: %+v", status)
	}
	notFound, _ := callTag[application.CLIRemoteTagResult](t, fixture, exitError, "query", "--service", "svc", "--tag", "2.1")
	if notFound.Status == nil || !notFound.Status.NotFound {
		t.Fatalf("an inexact Tag match was not rejected: %+v", notFound)
	}
	log, reply := callTag[application.CLIRemoteTagResult](t, fixture, 0, "logs", "--service", "svc", "--tag", tag, "--tail-lines", "42")
	if !strings.Contains(log.Log, "fixture build") || strings.Contains(string(reply.Data), "fixture-password") || !strings.Contains(log.Log, "<redacted>") {
		t.Fatal("build log or secret masking was lost")
	}
}

func TestCLIRejectsOutOfScopeOperations(t *testing.T) {
	cases := [][]string{
		{"platform", "status"},
		{"platform", "test"},
		{"production", "publish"},
		{"service", "config-edit", "--env", "uat", "--service", "svc"},
		{"service", "approval-approve", "--env", "uat", "--service", "svc", "--id", "1"},
		{"uat", "release", "--env", "prod", "--service", "svc", "--tag", "1.0"},
		{"tag", "logs", "--service", "svc", "--tag", "1.0", "--tail-lines", "1001"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := run(context.Background(), args, &out, &stderr); code != exitUsage {
				t.Fatalf("unexpected exit %d for %v", code, args)
			}
		})
	}
}
