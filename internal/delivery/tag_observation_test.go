package delivery

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/production"
)

func TestObserveUATRuntimeQueriesEachEligibleServiceOnce(t *testing.T) {
	runner := &tagRuntimeRunner{results: map[string]process.Result{
		"service-a": {Stdout: `{"service":"service-a","environment":"UAT","pods":[{"namespace":"team","pod_name":"a","app_version":"v2","ready":true}]}`},
		"service-b": {Stdout: `{"service":"service-b","environment":"UAT","pods":[{"namespace":"team","pod_name":"b","app_version":"v3","ready":true}]}`},
	}}
	task := newTagRuntimeTask(t, runner)
	rows := []TagObservation{
		newTagRuntimeObservation("service-a", "v2"),
		newTagRuntimeObservation("service-a", "v1"),
		newTagRuntimeObservation("service-b", "v3"),
		newTagRuntimeObservation("git-mismatch", "v2"),
		newTagRuntimeObservation("probe-error", "v2"),
		newTagRuntimeObservation("not-found", "v2"),
		newTagRuntimeObservation("still-deploying", "v2"),
		newTagRuntimeObservation("missing-tag", ""),
		newTagRuntimeObservation("", "v2"),
	}
	rows[3].Git.State = "MISMATCH"
	rows[4].Operation.GenbuStatus.FailureReason = stringPointer("query failed")
	rows[5].Operation.GenbuStatus.NotFound = true
	rows[6].Operation.GenbuStatus.UAT = domain.GenbuBuilding
	task.ObserveUATRuntime(context.Background(), rows, "")
	wantCalls := [][]string{{"query-service-pods", "--json", "uat", "service-a"}, {"query-service-pods", "--json", "uat", "service-b"}}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("Pod queries = %v, want %v", runner.calls, wantCalls)
	}
	for i, want := range []TagRuntimeState{TagRuntimeReady, TagRuntimeNotReady, TagRuntimeReady} {
		if got := rows[i].Runtime; got == nil || got.State != want || got.Namespace != "team" {
			t.Fatalf("row %d runtime = %+v, want %s in team", i, got, want)
		}
	}
	for _, row := range rows[3:] {
		if row.Runtime != nil {
			t.Fatalf("ineligible service %q got runtime %+v", row.GenbuService, row.Runtime)
		}
	}
}

func TestObserveUATRuntimeRejectsUnverifiedPodResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		result process.Result
		want   string
	}{
		{"wrong service", process.Result{Stdout: `{"service":"other","environment":"uat","pods":[]}`}, "other"},
		{"wrong environment", process.Result{Stdout: `{"service":"svc","environment":"prod","pods":[]}`}, "环境不匹配"},
		{"failed query", process.Result{ExitCode: 1, Stderr: "offline"}, "offline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &tagRuntimeRunner{results: map[string]process.Result{"svc": test.result}}
			task := newTagRuntimeTask(t, runner)
			rows := []TagObservation{newTagRuntimeObservation("svc", "v2"), newTagRuntimeObservation("svc", "v1")}
			task.ObserveUATRuntime(context.Background(), rows, "")
			if len(runner.calls) != 1 {
				t.Fatalf("failed Pod query was repeated: %v", runner.calls)
			}
			for _, row := range rows {
				if got := row.Runtime; got == nil || got.State != TagRuntimeUnknown || !strings.Contains(got.Error, test.want) {
					t.Fatalf("runtime = %+v, want UNKNOWN with %q", got, test.want)
				}
			}
		})
	}
}

func TestSummarizeTagRuntimeRequiresAllSelectedPodsToMatchAndBeReady(t *testing.T) {
	ready := production.RuntimePod{Namespace: "team", Version: "v2", Ready: true}
	older := production.RuntimePod{Namespace: "team", Version: "v1", Ready: true}
	pending := production.RuntimePod{Namespace: "team", Version: "v2"}
	unknown := production.RuntimePod{Namespace: "team", Ready: true}
	otherReady := production.RuntimePod{Namespace: "other", Version: "v2", Ready: true}
	otherOld := production.RuntimePod{Namespace: "other", Version: "v1", Ready: true}
	for _, test := range []struct {
		name, namespace string
		pods            []production.RuntimePod
		want            TagRuntimeState
		wantNamespace   string
	}{
		{"auto select", "", []production.RuntimePod{ready}, TagRuntimeReady, "team"},
		{"mixed versions", "", []production.RuntimePod{ready, older}, TagRuntimeNotReady, "team"},
		{"unready pod", "", []production.RuntimePod{ready, pending}, TagRuntimeNotReady, "team"},
		{"unknown version", "", []production.RuntimePod{ready, unknown}, TagRuntimeUnknown, "team"},
		{"no pods", "", nil, TagRuntimeNotReady, ""},
		{"all namespaces ready", "", []production.RuntimePod{ready, otherReady}, TagRuntimeReady, ""},
		{"ambiguous namespaces", "", []production.RuntimePod{ready, otherOld}, TagRuntimeAmbiguous, ""},
		{"selected namespace ready", "team", []production.RuntimePod{ready, otherOld}, TagRuntimeReady, "team"},
		{"selected namespace missing", "missing", []production.RuntimePod{ready, otherOld}, TagRuntimeNotReady, "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := summarizeTagRuntime(test.pods, "v2", test.namespace)
			if got.State != test.want || got.Namespace != test.wantNamespace || (got.Error == "") != (test.want == TagRuntimeReady) {
				t.Fatalf("runtime = %+v, want %s in %q", got, test.want, test.wantNamespace)
			}
		})
	}
	got := summarizeTagRuntime([]production.RuntimePod{ready, older, otherOld, pending, unknown}, "v2", "")
	want := []TagRuntimeNamespace{
		{Namespace: "other", Versions: []string{"v1"}, PodCount: 1, ReadyPods: 1},
		{Namespace: "team", Versions: []string{"v1", "v2"}, PodCount: 4, ReadyPods: 3, MatchingPods: 2, UnknownVersionPods: 1},
	}
	if !reflect.DeepEqual(got.Namespaces, want) || !strings.Contains(got.Error, "--namespace") {
		t.Fatalf("ambiguous evidence = %+v, want %+v and selector hint", got, want)
	}
}

type tagRuntimeRunner struct {
	results map[string]process.Result
	calls   [][]string
}

func (r *tagRuntimeRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	if spec.Executable == "/bin/zsh" { // macOS login-shell PATH discovery
		return process.Result{Stdout: "/usr/bin\n"}, ctx.Err()
	}
	r.calls = append(r.calls, slices.Clone(spec.Arguments))
	return r.results[spec.Arguments[len(spec.Arguments)-1]], ctx.Err()
}

func newTagRuntimeTask(t *testing.T, runner *tagRuntimeRunner) *TagTask {
	t.Helper()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.GenbuExecutablePath = &command
	return &TagTask{service: &Service{runner: runner}, task: resolvedTask{config: config}}
}

func newTagRuntimeObservation(service, tag string) TagObservation {
	operation := domain.TagOperation{
		State: domain.TagOperationSuccess,
		Tag:   &tag,
		GenbuStatus: domain.GenbuTagProbeStatus{
			UAT:       domain.GenbuSuccess,
			CheckedAt: stringPointer("2026-11-06 12:00:00"),
		},
	}
	return TagObservation{TagOperationReport: TagOperationReport{Operation: operation}, Git: TagPushStatus{State: "PUSHED"}, GenbuService: service}
}
