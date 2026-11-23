package application

import (
	"context"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type genbuApprovalRouteFake struct {
	taskServiceGenbuClient
	list   func(production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error)
	detail func(string, string, int64) (production.GenbuApprovalDetail, error)
}

func (f genbuApprovalRouteFake) ApprovalList(_ context.Context, _ domain.AppConfig, request production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error) {
	return f.list(request)
}

func (f genbuApprovalRouteFake) ApprovalDetail(_ context.Context, _ domain.AppConfig, env, service string, id int64) (production.GenbuApprovalDetail, error) {
	return f.detail(env, service, id)
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
	action.Payload["environment"], action.Payload["service"] = "prod", "unrelated"
	action.Payload["status"], action.Payload["type"] = "rejected", "8"
	action.Payload["startDate"], action.Payload["endDate"] = "2026-11-01", "2026-11-02"
	action.Payload["scope"], action.Payload["dateKind"] = "submitted", "approved"
	action.Payload["openedAt"] = time.Date(2026, time.November, 18, 17, 30, 0, 0, time.UTC)
	want := production.GenbuApprovalListRequest{
		Environment: "prod", Service: "alias", Status: "all", Type: "7",
		StartDate: "2026-11-13", EndDate: "2026-11-19", Scope: "all", DateKind: "created",
	}
	const approvalID = 9007199254740993 // Keep IDs exact past JavaScript's integer precision.
	item := production.GenbuApprovalItem{ID: approvalID, Service: "canonical", Environment: "prod"}
	client := genbuApprovalRouteFake{
		list: func(request production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error) {
			if request != want {
				t.Fatalf("approval list request %+v, want %+v", request, want)
			}
			return production.GenbuApprovalPage{Page: request.Page, Items: []production.GenbuApprovalItem{item}}, nil
		}, detail: func(env, service string, id int64) (production.GenbuApprovalDetail, error) {
			if env != "prod" || service != "alias" || id != approvalID {
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
