package production

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

func TestNativeGenbuServiceConfigReturnsNamespaceChoicesAndRedactedText(t *testing.T) {
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("env") != "PRD" {
			t.Errorf("unexpected configuration query: %s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case genbuapi.AppEnvNamespacePath:
			writeGenbuResponse(w, map[string]any{"response": []string{"zeta", "alpha"}})
		case genbuapi.ConfigMapListPath:
			if r.URL.Query().Get("namespace") != "alpha" {
				t.Errorf("unexpected namespace: %s", r.URL)
			}
			writeGenbuResponse(w, map[string]any{"response": []map[string]any{
				{"update_time": "2026-11-19 15:00:00", "encoded_data": []map[string]any{
					{"content": "spring.datasource.password=database-secret\nlabel=fixture-token\nsecret: |"},
					{"content": "  private-fragment\nvisible.setting=ready"},
				}},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	_, err := client.ServiceConfig(ctx, cfg, "prod", "PC", "")
	var choices *ServiceConfigNamespaceError
	if !errors.As(err, &choices) || !reflect.DeepEqual(choices.Candidates, []string{"alpha", "zeta"}) {
		t.Fatalf("ServiceConfig() did not return validated namespace choices: %v", err)
	}
	content, err := client.ServiceConfig(ctx, cfg, "prod", "PC", "alpha")
	if err != nil || !strings.Contains(content, "visible.setting=ready") ||
		strings.Contains(content, "database-secret") || strings.Contains(content, "fixture-token") ||
		strings.Contains(content, "private-fragment") {
		t.Error("ServiceConfig() failed to return redacted text", err)
	}
}
