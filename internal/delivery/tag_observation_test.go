package delivery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

func TestObserveUATRuntimeQueriesEachEligibleServiceOnce(t *testing.T) {
	client := &tagRuntimeClient{results: map[string]production.ServicePods{
		"service-a": {Service: "service-a", Environment: "uat", Pods: []production.RuntimePod{{Namespace: "team", Version: "v2", Ready: true}}},
		"service-b": {Service: "service-b", Environment: "uat", Pods: []production.RuntimePod{{Namespace: "team", Version: "v3", Ready: true}}},
	}}
	task := newTagRuntimeTask(client)
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
	wantCalls := []string{"uat:service-a", "uat:service-b"}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("Pod queries = %v, want %v", client.calls, wantCalls)
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
		result production.ServicePods
		err    error
		want   string
	}{
		{"wrong service", production.ServicePods{Service: "other", Environment: "uat"}, nil, "other"},
		{"failed query", production.ServicePods{}, errors.New("offline"), "offline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &tagRuntimeClient{results: map[string]production.ServicePods{"svc": test.result}, err: test.err}
			task := newTagRuntimeTask(client)
			rows := []TagObservation{newTagRuntimeObservation("svc", "v2"), newTagRuntimeObservation("svc", "v1")}
			task.ObserveUATRuntime(context.Background(), rows, "")
			if len(client.calls) != 1 {
				t.Fatalf("failed Pod query was repeated: %v", client.calls)
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

type tagRuntimeClient struct {
	results map[string]production.ServicePods
	calls   []string
	err     error
}

func (c *tagRuntimeClient) Pods(_ context.Context, _ domain.AppConfig, env, service string) (production.ServicePods, error) {
	c.calls = append(c.calls, env+":"+service)
	return c.results[service], c.err
}

func (*tagRuntimeClient) Status(context.Context, domain.AppConfig, string, string) (domain.GenbuTagProbeStatus, error) {
	return domain.GenbuTagProbeStatus{}, errors.New("unexpected Tag status query")
}

func (*tagRuntimeClient) CheckScope(ctx context.Context) error { return ctx.Err() }

func newTagRuntimeTask(client *tagRuntimeClient) *TagTask {
	return &TagTask{service: &Service{genbuClient: func(context.Context) (TagGenbu, error) { return client, nil }}}
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
