package production

import (
	"context"
	"reflect"
	"testing"

	"silverwing-go/internal/domain"
)

func TestGenbuRuntimePodsPreserveObservedDataAndEnvironment(t *testing.T) {
	for _, env := range []string{"uat", "prod"} {
		t.Run(env, func(t *testing.T) {
			wireEnv := "UAT"
			if env == "prod" {
				wireEnv = "PRD"
			}
			r := &genbuRunner{output: `{"service":"canonical-service","environment":"` + wireEnv + `","pods":[{"namespace":"team","pod_name":"p1","app_version":"v1","ready":true,"phase":"Running","created_time":"2026-11-02 10:11:12"},{"namespace":"team","pod_name":"p2","app_version":"v2","ready":false,"phase":"Pending"}]}`}
			pods, err := (CLIGenbu{Runner: r}).Pods(context.Background(), domain.DefaultAppConfig(), env, "alias")
			if err != nil || len(pods.Pods) != 2 || pods.Service != "canonical-service" || !pods.Pods[0].Ready || pods.Pods[1].Ready || pods.Pods[0].CreatedTime != "2026-11-02 10:11:12" {
				t.Fatal(pods, err)
			}
			if !reflect.DeepEqual(r.spec.Arguments, []string{"query-service-pods", "--json", env, "alias"}) {
				t.Fatal(r.spec.Arguments)
			}
		})
	}
}

func TestGenbuRuntimePodsDistinguishEmptyMalformedAndFailed(t *testing.T) {
	for _, test := range []struct {
		name, wire string
		code       int
		wantError  bool
	}{
		{"empty", `{"service":"service","environment":"UAT","pods":[]}`, 0, false},
		{"missing", `{"service":"service","environment":"UAT"}`, 0, true},
		{"null", `{"service":"service","environment":"UAT","pods":null}`, 0, true},
		{"wrong-env", `{"service":"service","environment":"PRD","pods":[]}`, 0, true},
		{"bad-pod", `{"service":"service","environment":"UAT","pods":[{}]}`, 0, true},
		{"failure", `offline`, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := &genbuRunner{output: test.wire, code: test.code}
			pods, err := (CLIGenbu{Runner: r}).Pods(context.Background(), domain.DefaultAppConfig(), "uat", "service")
			if (err != nil) != test.wantError {
				t.Fatal(pods, err)
			}
			if !test.wantError && len(pods.Pods) != 0 {
				t.Fatal(pods)
			}
		})
	}
}

func TestGenbuServiceConfigurationUsesOnlySelectedEnvironmentNamespace(t *testing.T) {
	r := &genbuRunner{output: "key: <redacted>\n"}
	text, err := (CLIGenbu{Runner: r}).ServiceConfig(context.Background(), domain.DefaultAppConfig(), "prod", "alias", "fp")
	if err != nil || text != r.output || !reflect.DeepEqual(r.spec.Arguments, []string{"query-service-config", "prod", "alias", "-namespace", "fp"}) {
		t.Fatal(text, r.spec.Arguments, err)
	}
	if _, err := (CLIGenbu{Runner: r}).ServiceConfig(context.Background(), domain.DefaultAppConfig(), "production", "alias", ""); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
