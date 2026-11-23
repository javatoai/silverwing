package production

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestNativeGenbuApprovalScopeAndProposalRedaction(t *testing.T) {
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/genbu/basic/api/approve" || r.Method != http.MethodGet {
			t.Errorf("unexpected approval request: %s %s", r.Method, r.URL)
		}
		if q := r.URL.Query(); q.Get("keyword") != "" &&
			(q.Get("keyword") != "mp-pay-center" || q.Get("env") != "PRD" ||
				q.Get("current") != "2" || q.Get("approve_type") != "7") {
			t.Errorf("unexpected approval filters: %v", q)
		}
		writeGenbuResponse(w, map[string]any{"total": 2, "response": []map[string]any{
			{"id": 43, "env": "PRD", "app_name": "mp-pay-center-other"},
			{"id": 42, "env": "PRD", "app_name": "mp-pay-center", "approve_type": 7,
				"reason": "checked fixture-secret", "external_id": "Q-42",
				"content": map[string]any{"encoded_data": []map[string]any{
					{"name": "application.yaml", "content": "password: private-proposal"},
				}}},
		}})
	})
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	list, err := client.ApprovalList(ctx, cfg, GenbuApprovalListRequest{
		Environment: "prod", Service: "PC", Type: "ConfigMap 修改", Page: 2,
	})
	if err != nil || list.Total != 2 || list.Page != 2 || len(list.Items) != 1 || list.Items[0].ID != 42 {
		t.Fatalf("ApprovalList() = %+v, %v", list, err)
	}
	detail, err := client.ApprovalDetail(ctx, cfg, "prod", "pc", 42)
	if err != nil || detail.ID != 42 || detail.Details["工单号"] != "Q-42" ||
		!strings.Contains(detail.Details["审批备注"], "checked") ||
		!reflect.DeepEqual(detail.FileNames, []string{"application.yaml"}) {
		t.Fatalf("ApprovalDetail() metadata did not match the requested approval: %v", err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil || strings.Contains(string(encoded), "private-proposal") || strings.Contains(string(encoded), "fixture-secret") {
		t.Error("ApprovalDetail() disclosed a proposal body or credentials", err)
	}
	if _, err := client.ApprovalDetail(ctx, cfg, "uat", "pc", 42); err == nil {
		t.Error("accepted a production approval for UAT")
	}
	if _, err := client.ApprovalDetail(ctx, cfg, "prod", "pc", 43); err == nil {
		t.Error("accepted an approval for another service")
	}
}
