package genbuapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUATReleaseEnablesAndVerifiesBeforeDispatch(t *testing.T) {
	const tag = "2.15.85.beta-8"
	for _, tc := range []struct {
		name            string
		initial         any
		verified        any
		switchHTTP      int
		releaseHTTP     int
		externalEnable  bool
		changedSettings bool
		changedVersion  bool
		changedBuild    bool
		cancel          bool
		wantEnable      string
		wantStatus      string
		wantFlow        string
		wantErr         error
	}{
		{name: "already enabled", initial: "1", wantStatus: "submitted", wantFlow: "admit release"},
		{name: "became enabled between preview and submit", initial: 0, externalEnable: true, wantStatus: "submitted", wantFlow: "admit release"},
		{name: "enable disabled version", initial: 0, verified: 1, wantEnable: "enabled", wantStatus: "submitted", wantFlow: "admit enable verify settings release"},
		{name: "ambiguous receipt confirmed by fresh read", initial: "0", switchHTTP: 502, verified: "1", wantEnable: "enabled", wantStatus: "submitted", wantFlow: "admit enable verify settings release"},
		{name: "ambiguous receipt and still off", initial: 0, switchHTTP: 502, verified: 0, wantEnable: "unknown", wantStatus: "not-sent", wantFlow: "admit enable verify"},
		{name: "accepted but still off", initial: 0, verified: 0, wantEnable: "rejected", wantStatus: "not-sent", wantFlow: "admit enable verify"},
		{name: "rejected by Genbu", initial: 0, switchHTTP: 401, wantEnable: "rejected", wantStatus: "not-sent", wantFlow: "admit enable"},
		{name: "verification unavailable", initial: 0, wantEnable: "unknown", wantStatus: "not-sent", wantFlow: "admit enable verify"},
		{name: "stale release settings after enable", initial: 0, verified: 1, changedSettings: true, wantEnable: "enabled", wantStatus: "not-sent", wantFlow: "admit enable verify settings", wantErr: ErrUATPodPreviewStale},
		{name: "exact Tag points to a new version after enable", initial: 0, verified: 1, changedVersion: true, wantEnable: "enabled", wantStatus: "not-sent", wantFlow: "admit enable verify", wantErr: ErrUATPodPreviewStale},
		{name: "build regresses after enable", initial: 0, verified: 1, changedBuild: true, wantEnable: "enabled", wantStatus: "not-sent", wantFlow: "admit enable verify"},
		{name: "canceled by admission", initial: 0, cancel: true, wantEnable: "required", wantStatus: "not-sent", wantFlow: "admit", wantErr: context.Canceled},
		{name: "actual release result unknown", initial: 0, verified: 1, releaseHTTP: 502, wantEnable: "enabled", wantStatus: "unknown", wantFlow: "admit enable verify settings release", wantErr: ErrUATPodSubmitUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var trace []string
			var traceMu sync.Mutex
			record := func(step string) {
				traceMu.Lock()
				defer traceMu.Unlock()
				trace = append(trace, step)
			}
			var enableSent, previewDone atomic.Bool
			group := uatRedeployTestGroupRow()
			probe := map[string]any{"http_get": map[string]any{"path": "/health", "port": 8080}}
			client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("App-Id") != "10010003" || r.Header.Get("System-Code") != systemCode ||
					r.Header.Get("X-Token") != "uat-pod-test-token" {
					t.Errorf("incorrect scope on %s %s", r.Method, r.URL.Path)
				}
				switch {
				case r.URL.Path == versionPath:
					id := 42
					if enableSent.Load() && tc.changedVersion {
						id = 43
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": []map[string]any{{"version_id": id, "version_name": tag}}})
				case r.URL.Path == baseInfoPath && r.Method == http.MethodGet:
					if r.URL.Query().Get("version_id") != "42" {
						t.Errorf("queried unexpected version %s", r.URL.RawQuery)
					}
					flag, build := tc.initial, 1
					if previewDone.Load() && tc.externalEnable {
						flag = 1
					}
					if enableSent.Load() {
						record("verify")
						if tc.verified == nil {
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						flag = tc.verified
						if tc.changedBuild {
							build = 2
						}
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": []map[string]any{
						{"step": buildStepNumber, "status": build},
						{"step": uatStepNumber, "step_name": "UAT", "enabled": flag, "status": 0},
					}})
				case r.URL.Path == uatReleaseEnablePath && r.Method == http.MethodPost:
					record("enable")
					enableSent.Store(true)
					var got map[string]any
					want := map[string]any{"version_id": float64(42), "env": "UAT", "enable": float64(1)}
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, want) || r.URL.RawQuery != "" {
						t.Errorf("enable sent %v (%v), want %v", got, err, want)
					}
					if tc.switchHTTP != 0 {
						w.WriteHeader(tc.switchHTTP)
						return
					}
					writeTestJSON(w, map[string]any{"code": 20000})
				case r.URL.Path == uatReleaseGroupsPath && r.Method == http.MethodGet:
					writeTestJSON(w, map[string]any{"code": 20000, "response": []map[string]any{group}})
				case r.URL.Path == uatDeployInfoPath && r.Method == http.MethodGet:
					podCount := 2
					if enableSent.Load() {
						record("settings")
						if tc.changedSettings {
							podCount = 3
						}
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": map[string]any{
						"group_id": 7, "env": "UAT", "k8s_namespace": "mp", "pod_num": podCount,
						"auto_rollback": false, "mount_filebeat": false,
						"live_check_form": probe, "ready_check_form": probe, "startup_check_form": probe,
					}})
				case r.URL.Path == uatReleasePath && r.Method == http.MethodPost:
					record("release")
					if tc.releaseHTTP != 0 {
						w.WriteHeader(tc.releaseHTTP)
						return
					}
					writeTestJSON(w, map[string]any{"code": 20000})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := UATPodOperationRequest{Action: "release", Service: "PC", Tag: tag}
			preview, err := client.PreviewUATPodOperation(ctx, request)
			wantPreview := "required"
			if tc.initial == "1" {
				wantPreview = ""
			}
			if err != nil || preview.EnableStatus != wantPreview || preview.Status != "preview" {
				t.Fatalf("preview = %+v, err = %v", preview, err)
			}
			previewDone.Store(true)
			result, err := client.SubmitUATPodOperation(ctx, request, preview.PreviewID, func(_ context.Context, target UATPodOperationResult) error {
				record("admit")
				if target.PreviewID != preview.PreviewID {
					t.Errorf("admitted preflight changed to %s", target.PreviewID)
				}
				if tc.cancel {
					cancel()
				}
				return nil
			})
			traceMu.Lock()
			flow := strings.Join(trace, " ")
			traceMu.Unlock()
			if flow != tc.wantFlow || result.Status != tc.wantStatus || result.EnableStatus != tc.wantEnable {
				t.Fatalf("flow = %q, result = %+v, err = %v", flow, result, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			switch tc.wantStatus {
			case "submitted":
				if err != nil {
					t.Fatal(err)
				}
			case "not-sent":
				var notSent RequestNotSentError
				if !errors.As(err, &notSent) || errors.Is(err, ErrUATPodSubmitUnknown) || errors.Is(err, ErrUATPodSubmitRejected) {
					t.Fatalf("switch failure was confused with actual release dispatch: %v", err)
				}
			}
		})
	}
}
