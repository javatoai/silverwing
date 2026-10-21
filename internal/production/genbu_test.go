package production

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type genbuRunner struct {
	output string
	code   int
	spec   process.Spec
}

func (r *genbuRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	r.spec = spec
	return process.Result{Stdout: r.output, ExitCode: r.code}, ctx.Err()
}

func TestGenbuRuntimeCLIUsesValidatedJSONAndSharedEnvironment(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := domain.DefaultAppConfig()
	c.GenbuExecutablePath = &command
	c.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetGenbu}
	proxy := "http://127.0.0.1:9999"
	c.CommandProxyURL = &proxy
	r := &genbuRunner{output: `{"service":"synthetic","environment":"prod","pods":[{"app_version":"2.3.44"},{"app_version":"2.3.44"},{"app_version":"2.3.43"}]}`}
	g := CLIGenbu{Runner: r}
	versions, err := g.Versions(context.Background(), c, "synthetic")
	if err != nil || !reflect.DeepEqual(versions, []string{"2.3.43", "2.3.44"}) {
		t.Fatalf("versions=%v, %v", versions, err)
	}
	if !reflect.DeepEqual(r.spec.Arguments, []string{"query-service-pods", "--json", "prod", "synthetic"}) || r.spec.Timeout != 20*time.Second || r.spec.Env["HTTPS_PROXY"] != proxy {
		t.Fatal("CLI arguments/environment/timeout drifted")
	}
	for _, output := range []string{`{}`, `not json`, `{"pods":[{}]}`, `{"environment":"UAT","pods":[{"app_version":"2.3.44"}]}`} {
		r.output = output
		if _, err = g.Versions(context.Background(), c, "synthetic"); err == nil {
			t.Errorf("accepted malformed runtime output %s", output)
		}
	}
	r.output = `{"environment":"PRD","pods":[]}`
	if versions, err = g.Versions(context.Background(), c, "synthetic"); err != nil || len(versions) != 0 {
		t.Fatal("empty Pods must remain an explicit unknown baseline")
	}
	r.output = "synthetic authentication failure"
	r.code = 1
	if _, err = g.Versions(context.Background(), c, "synthetic"); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatal("CLI failure lost actual cause")
	}
}
