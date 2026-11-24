package application

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type genbuApprovalRouteFake struct {
	taskServiceGenbuClient
	list    func(production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error)
	detail  func(string, string, int64) (production.GenbuApprovalDetail, error)
	approve func(context.Context, string, string, int64, string) (production.GenbuApprovalDetail, error)
}

func (f genbuApprovalRouteFake) ApprovalList(_ context.Context, _ domain.AppConfig, request production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error) {
	return f.list(request)
}

func (f genbuApprovalRouteFake) ApprovalDetail(_ context.Context, _ domain.AppConfig, env, service string, id int64) (production.GenbuApprovalDetail, error) {
	return f.detail(env, service, id)
}

func (f genbuApprovalRouteFake) ApproveUATConfigChange(ctx context.Context, _ domain.AppConfig, env, service string, id int64, revision string) (production.GenbuApprovalDetail, error) {
	return f.approve(ctx, env, service, id, revision)
}

func TestTaskServiceGenbuConfigApprovalsScopeAndFixedQuery(t *testing.T) {
	app, action := taskServicesFixture(t)
	cfg, err := app.Config.LoadForPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.TagEnabled = false
	if err := app.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	action.Kind = "task.services.approvals"
	action.Payload["service"] = "unrelated"
	action.Payload["status"], action.Payload["type"] = "rejected", "8"
	action.Payload["startDate"], action.Payload["endDate"] = "2026-11-01", "2026-11-02"
	action.Payload["scope"], action.Payload["dateKind"] = "submitted", "approved"
	action.Payload["openedAt"] = time.Date(2026, time.November, 18, 17, 30, 0, 0, time.UTC)
	want := production.GenbuApprovalListRequest{
		Environment: "uat", Service: "alias", Status: "all", Type: "7",
		StartDate: "2026-11-13", EndDate: "2026-11-19", Scope: "all", DateKind: "created",
	}
	const approvalID = 9007199254740993 // Keep IDs exact past JavaScript's integer precision.
	item := production.GenbuApprovalItem{ID: approvalID, Service: "canonical", Environment: "uat"}
	client := genbuApprovalRouteFake{
		list: func(request production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error) {
			if request != want {
				t.Fatalf("approval list request %+v, want %+v", request, want)
			}
			return production.GenbuApprovalPage{Page: request.Page, Items: []production.GenbuApprovalItem{item}}, nil
		}, detail: func(env, service string, id int64) (production.GenbuApprovalDetail, error) {
			if env != "uat" || service != "alias" || id != approvalID {
				t.Fatalf("detail request lost scope or ID: %s %s %d", env, service, id)
			}
			return production.GenbuApprovalDetail{GenbuApprovalItem: item}, nil
		},
	}
	ctx := context.Background()
	for _, page := range []int{1, 2} {
		action.Payload["page"], want.Page = page, page
		result, err := app.taskServiceGenbuAction(ctx, action, client)
		if err != nil || result.Data.(production.GenbuApprovalPage).Items[0].ID != approvalID {
			t.Fatal(result, err)
		}
	}
	action.Kind, action.Payload["approvalId"] = "task.services.approval-detail", "9007199254740993"
	result, err := app.taskServiceGenbuAction(ctx, action, client)
	if err != nil || result.Data.(production.GenbuApprovalDetail).ID != approvalID {
		t.Fatal(result, err)
	}
}

func TestTaskServiceGenbuApprovalBlocksDuplicateAfterUncertainReceipt(t *testing.T) {
	app, action := taskServicesFixture(t)
	ctx := context.Background()
	cfg, err := app.Config.LoadForPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 64)
	const id = 722
	detail := production.GenbuApprovalDetail{
		GenbuApprovalItem: production.GenbuApprovalItem{ID: id, Service: "alias", Environment: "uat", Status: "待处理"},
		CanApprove:        true,
		Revision:          revision,
	}
	calls := 0
	client := genbuApprovalRouteFake{
		approve: func(_ context.Context, env, service string, approvalID int64, observed string) (production.GenbuApprovalDetail, error) {
			calls++
			if env != "uat" || service != "alias" || approvalID != id || observed != revision {
				t.Fatalf("unexpected approval request: %s %s %d", env, service, approvalID)
			}
			return detail, production.ErrGenbuApprovalUnknown
		},
		detail: func(string, string, int64) (production.GenbuApprovalDetail, error) { return detail, nil },
	}
	action.Kind = "task.services.approval-approve"
	action.Payload["approvalId"], action.Payload["revision"] = "722", revision
	result, err := app.taskServiceGenbuAction(ctx, action, client)
	if !errors.Is(err, production.ErrGenbuApprovalUnknown) || result.Data.(production.GenbuApprovalDetail).ID != id {
		t.Fatalf("unconfirmed outcome lost its receipt or scope: %v", err)
	}
	catalog := serviceRuntimeTestAction(app, cfg, "alias")
	catalog.Kind = "service.runtime.approval-approve"
	catalog.Payload["approvalId"], catalog.Payload["revision"] = "722", revision
	if _, err := app.taskServiceGenbuAction(ctx, catalog, client); !errors.Is(err, production.ErrGenbuApprovalUnknown) || calls != 1 {
		t.Fatalf("catalog allowed a second delivery: calls=%d error=%v", calls, err)
	}
	read := catalog
	read.Kind, read.Payload = "service.runtime.approval-detail", maps.Clone(catalog.Payload)
	delete(read.Payload, "revision")
	result, err = app.taskServiceGenbuAction(ctx, read, client)
	latest, ok := result.Data.(production.GenbuApprovalDetail)
	if err != nil || !ok || latest.CanApprove || latest.ComparisonNote == "" {
		t.Fatalf("refresh after ambiguous submit left approval action enabled: %v", err)
	}
}
