package dragon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/dragonapi"
)

func TestNativeAccountIdentityPreservesRecoveryDigest(t *testing.T) {
	const legacyIdentity = "97cf59733f0ad2d63b606dd83b97d7f624ea9681974d1087d68b65a202a8c3d6"
	account := " account "
	native := Native{Identity: func(context.Context, domain.AppConfig) (string, error) { return account, nil }}
	ctx, cfg := context.Background(), domain.AppConfig{}
	for _, username := range []string{" account ", "account", "another account", " "} {
		account = username
		got, err := native.AccountIdentity(ctx, cfg)
		switch username {
		case " account ", "account":
			if got != legacyIdentity || err != nil {
				t.Fatalf("expected legacy digest %q, got %q and %v", legacyIdentity, got, err)
			}
		case "another account":
			if got == legacyIdentity || len(got) != 64 || err != nil {
				t.Fatalf("different account returned %q and %v", got, err)
			}
		default:
			if got != "" || err == nil {
				t.Fatalf("blank account returned %q and %v", got, err)
			}
		}
	}
}

func TestNativeQueryPaginatesAndDetailPreservesApprovalFields(t *testing.T) {
	const requirementID = "7191234567"
	const createdAt = "2026-11-10T15:12:12.497+08:00"
	for _, mode := range []string{"complete", "duplicate", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method %s", r.Method)
				}
				switch r.URL.Path {
				case "/dragon/api/approval/approval/myCreate":
					query := r.URL.Query()
					if query.Get("formData") != requirementID || query.Get("size") != "100" || query.Get("processTemplateId") != "24" {
						t.Errorf("unexpected query %s", query.Encode())
					}
					switch page := query.Get("page"); page {
					case "1":
						fmt.Fprintf(w, `{"code":"000000","data":{"total":2,"page":1,"size":1,"data":[{"approvalId":19001,"createdAt":%q}]}}`, createdAt)
					case "2":
						id := int64(9223372036854775807)
						if mode == "duplicate" {
							id = 19001
						}
						fmt.Fprintf(w, `{"code":"000000","data":{"total":2,"page":2,"size":1,"data":[{"approvalId":%d}]}}`, id)
					default:
						t.Errorf("unexpected query page %q", page)
						w.WriteHeader(http.StatusBadRequest)
					}
				case "/dragon/api/approval/approval/19001":
					fmt.Fprintf(w, `{"code":"000000","data":{"workOrder":{"approvalId":19001,"currentHandlers":[{"Name":"张三"}]},"tpls":[{"formData":{"testApproval":{"meegoCode":%q,"remark":"回读结果","overviewData":[{"appName":"bp-api-service"}]}}}]}}`, requirementID)
				default:
					t.Errorf("unexpected path %q", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := dragonapi.NewClient(dragonapi.Config{
				BeastURL: server.URL, DragonURL: server.URL, Timeout: time.Second, Token: "test-session", UserID: 4,
				TokenExpiresAt: time.Now().Add(time.Hour).Unix(), CheckAdmission: commandruntime.CheckAdmission,
			})
			if err != nil {
				t.Fatal(err)
			}
			native := Native{Client: func(context.Context, domain.AppConfig) (*dragonapi.Client, error) { return client, nil }}
			blocked := errors.New("approval scope changed")
			admitted := 0
			ctx := commandruntime.WithAdmission(context.Background(), func(context.Context) error {
				if mode == "blocked" && admitted > 0 {
					return blocked
				}
				admitted++
				return nil
			})
			cfg := domain.AppConfig{}
			items, err := native.Query(ctx, cfg, requirementID)
			if mode != "complete" {
				if items != nil || err == nil || (mode == "blocked" && (!errors.Is(err, blocked) || admitted != 1)) {
					t.Fatalf("incomplete query returned %+v and %v", items, err)
				}
				return
			}
			if err != nil || len(items) != 2 || items[0].CreatedAt != createdAt || items[1].ID != "9223372036854775807" {
				t.Fatalf("unexpected query results %+v, %v", items, err)
			}
			detail, err := native.Detail(ctx, cfg, items[0].ID)
			if err != nil || detail.ID != "19001" || detail.URL != server.URL+"/#/workorder/detail/19001" || detail.Meego != requirementID ||
				detail.Remark != "回读结果" || !slices.Equal(detail.Apps, []string{"api"}) || !slices.Equal(detail.Handlers, []string{"张三"}) {
				t.Fatalf("unexpected approval %+v, %v", detail, err)
			}
		})
	}
}
