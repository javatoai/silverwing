package workspace

import (
	"testing"

	"silverwing-go/internal/domain"
)

func TestTaskBranchInfoFormats(t *testing.T) {
	task := domain.TaskManifest{RequirementLink: " https://fixture.invalid/requirement ", Services: []domain.ServiceWorkspace{
		{ServiceName: "api-service", Branch: "feature/api"},
		{ServiceName: "job-manager", Branch: "feature/job"},
	}}
	for _, check := range []struct {
		name           string
		link, branches bool
		want           string
	}{
		{"services without link", false, false, "api-service\njob-manager"},
		{"services with link", true, false, "需求链接：https://fixture.invalid/requirement\n\napi-service\njob-manager"},
		{"branches without link", false, true, "api-service：feature/api\njob-manager：feature/job"},
		{"branches with link", true, true, "需求链接：https://fixture.invalid/requirement\n\napi-service：feature/api\njob-manager：feature/job"},
	} {
		t.Run(check.name, func(t *testing.T) {
			got := FormatTaskServices(task, check.link)
			if check.branches {
				got = FormatTaskBranchInfo(task, check.link)
			}
			if got != check.want {
				t.Fatalf("got %q, want %q", got, check.want)
			}
		})
	}
	task.RequirementLink = "  "
	if got := FormatTaskBranchInfo(task, true); got != "api-service：feature/api\njob-manager：feature/job" {
		t.Fatalf("blank link heading: %q", got)
	}
	if got := FormatTaskServices(task, true); got != "api-service\njob-manager" {
		t.Fatalf("blank link heading: %q", got)
	}
	task.Services[1].ServiceName = "api-service"
	if got := FormatTaskServices(task, false); got != "api-service\napi-service" {
		t.Fatalf("task module order or repeated names changed: %q", got)
	}
	if got := FormatTaskBranchInfo(domain.TaskManifest{}, true); got != "" {
		t.Fatalf("empty task: %q", got)
	}
}
