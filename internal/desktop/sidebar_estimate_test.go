package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type estimateBadgeBackend struct{ cached bool }

func (*estimateBadgeBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("badge fixture must not read a snapshot")
}
func (*estimateBadgeBackend) RequirementIdentity(context.Context) (application.RequirementIdentitySnapshot, error) {
	return application.RequirementIdentitySnapshot{State: application.RequirementIdentityAuthenticated, Identity: "fixture-account-a"}, nil
}
func (b *estimateBadgeBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	if action.Kind != "requirements.list" || action.Payload["force"] == true {
		return application.ActionResult{}, errors.New("background badge must use the normal cache-aware list read")
	}
	result := estimateBadgeResult("p:1", "1.25")
	result.Data.(map[string]any)["cacheHit"] = b.cached
	return result, nil
}

func estimateBadgeApp(t *testing.T) *app {
	t.Helper()
	t.Setenv("USERPROFILE", t.TempDir())
	s := &app{page: "tasks", configJSON: `{"meegleExecutablePath":"fixture-meegle","meegleProjects":[{"project_key":"p","simple_name":"fixture"}],"aiRequirementNamingPrewarmEnabled":false}`,
		snapshot: application.Snapshot{Home: t.TempDir()}, requirementIdentityJobs: &requirementIdentityJobState{confirmed: true, state: application.RequirementIdentityAuthenticated, identity: "fixture-account-a"}}
	s.requirementIdentityJobs.context, s.requirementIdentityJobs.hasContext = s.currentRequirementIdentityContext(), true
	s.observeRequirementListIdentity()
	t.Cleanup(func() {
		_ = s.closeRequirementNamingPreparation(context.Background())
		_ = s.closeRequirementDocumentPreparation(context.Background())
	})
	return s
}

func estimateBadgeResult(sprint string, values ...string) application.ActionResult {
	items := make([]application.Requirement, 0, len(values))
	for _, value := range values {
		value := value
		items = append(items, application.Requirement{ID: value, ProjectKey: "p", WorkItem: domain.ParticipatedWorkItem{MySprintEstimateDays: &value}})
	}
	return application.ActionResult{Data: map[string]any{"items": items, "selectedSprintKey": sprint, "sprints": []requirementSprint{{ProjectKey: "p", ID: strings.TrimPrefix(sprint, "p:")}}, "completedQueries": 1, "failures": []string{}}}
}

func estimateBadgeRequest(s *app, sprint string) *requirementListJob {
	state := s.ensureRequirementListJobs()
	state.generation++
	ctx, cancel := context.WithCancel(context.Background())
	request := &requirementListJob{generation: state.generation, identity: s.currentRequirementListIdentity(), ctx: ctx, cancel: cancel,
		sprintKey: sprint, sameSprint: sprint == s.requirementsSelectedSprintKey, items: append([]application.Requirement(nil), s.requirements...), selectedKey: s.selectedRequirementKey,
		action: application.Action{Kind: "requirements.list", Payload: map[string]any{"expectedIdentity": s.requirementIdentityJobs.identity}}}
	state.identity, state.hasIdentity, state.current = request.identity, true, request
	s.requirementsRequested, s.requirementsLoading = true, true
	return request
}

func estimateBadgeComplete(s *app, sprint string, values ...string) {
	request := estimateBadgeRequest(s, sprint)
	s.finishRequirementListJob(request, estimateBadgeResult(sprint, values...), nil)
}

func TestRequirementEstimateBadgeBootstrapsOutsideRequirements(t *testing.T) {
	for _, cached := range []bool{true, false} {
		name := "cache-miss-first-load"
		if cached {
			name = "cached-cold-start"
		}
		t.Run(name, func(t *testing.T) {
			s := estimateBadgeApp(t)
			s.backend = &estimateBadgeBackend{cached: cached}
			s.requirementIdentityJobs.confirmed = false
			s.observeRequirementIdentityJobs()
			pending := s.requirementIdentityJobs.pending
			if !s.requirementsRequested || pending == nil || pending.refresh || s.page != "tasks" {
				t.Fatal("startup did not queue the cache-aware list read without entering requirements")
			}
			// Finish the account/list boundaries in-process. No real window, CLI,
			// network or user's persistent profile is used by this fixture.
			s.requirementIdentityJobs.confirmed = true
			s.requirementIdentityJobs.pending = nil
			s.observeRequirementListIdentity()
			request := estimateBadgeRequest(s, "")
			result, err := executeRequirementListJob(s.backend, request)
			s.finishRequirementListJob(request, result, err)
			if s.page != "tasks" || s.requirementsEstimateBadge() != "1.25" {
				t.Fatalf("background list did not populate the estimate: page=%s badge=%s", s.page, s.requirementsEstimateBadge())
			}
			for _, page := range []string{"settings", "tasks", "services", "tasks"} {
				s.page = page
				s.observeRequirementIdentityJobs()
				if s.requirementIdentityJobs.pending != nil || s.requirementListJobs.current != nil || s.requirementsEstimateBadge() != "1.25" {
					t.Fatal("ordinary navigation repeated the list request or lost its estimate")
				}
			}
		})
	}
}

func TestRequirementEstimateBadgePreservesCompleteValue(t *testing.T) {
	s := estimateBadgeApp(t)
	estimateBadgeComplete(s, "p:1", "1.25", "1.5")
	refresh := estimateBadgeRequest(s, "p:1")
	if got := s.requirementsEstimateBadge(); got != "2.75" {
		t.Fatalf("refresh blinked the confirmed value: %s", got)
	}
	s.finishRequirementListJob(refresh, application.ActionResult{}, errors.New("fixture unavailable"))
	if s.requirementsEstimateBadge() != "2.75" || !strings.Contains(s.requirementsLoadError, "fixture unavailable") {
		t.Fatal("failed refresh lost the valid estimate or its existing error")
	}
	partial := estimateBadgeResult("p:1", "999")
	partial.Data.(map[string]any)["failures"] = []string{"fixture partial"}
	s.finishRequirementListJob(estimateBadgeRequest(s, "p:1"), partial, nil)
	if s.requirementsEstimateBadge() != "2.75" {
		t.Fatal("partial refresh replaced the confirmed estimate with incomplete data")
	}
	estimateBadgeComplete(s, "p:1", "3.125")
	if s.requirementsEstimateBadge() != "3.125" {
		t.Fatal("successful refresh did not replace the old estimate")
	}
	unknown := estimateBadgeResult("p:1", "1")
	unknown.Data.(map[string]any)["items"].([]application.Requirement)[0].WorkItem.MySprintEstimateDays = nil
	s.finishRequirementListJob(estimateBadgeRequest(s, "p:1"), unknown, nil)
	if s.requirementsEstimateBadge() != "—" {
		t.Fatal("a completed result with genuinely unknown estimate retained a stale number")
	}
}

func TestRequirementEstimateBadgeScopeAndLateResults(t *testing.T) {
	for _, scope := range []string{"account", "project", "sprint"} {
		t.Run(scope, func(t *testing.T) {
			s := estimateBadgeApp(t)
			estimateBadgeComplete(s, "p:1", "2.5")
			late := estimateBadgeRequest(s, "p:1")
			switch scope {
			case "account":
				s.requirementIdentityJobs.identity = "fixture-account-b"
			case "project":
				s.configJSON = strings.ReplaceAll(s.configJSON, `"p"`, `"other-project"`)
			case "sprint":
				s.activateRequirementSprint("p:2")
				estimateBadgeRequest(s, "p:2")
			}
			if s.requirementsEstimateBadge() != "—" {
				t.Fatal("a different account/project/Sprint displayed the previous estimate")
			}
			if scope != "sprint" {
				s.observeRequirementListIdentity()
			}
			s.finishRequirementListJob(late, estimateBadgeResult("p:1", "999"), nil)
			if s.requirementsEstimateBadge() != "—" {
				t.Fatal("a late result restored another scope's estimate")
			}
		})
	}
	s := estimateBadgeApp(t)
	estimateBadgeComplete(s, "p:1", "2.5")
	s.activateRequirementSprint("p:2")
	estimateBadgeComplete(s, "p:2", "8")
	s.activateRequirementSprint("p:1")
	s.requirementsLoading = true
	if s.requirementsEstimateBadge() != "2.5" {
		t.Fatal("returning to a cached Sprint lost its scoped confirmed estimate")
	}
}

func TestRequirementEstimateBadgeKeepsUnknownDistinctFromZero(t *testing.T) {
	s := estimateBadgeApp(t)
	if s.requirementsEstimateBadge() != "—" {
		t.Fatal("an unloaded estimate became zero")
	}
	request := estimateBadgeRequest(s, "p:1")
	s.finishRequirementListJob(request, application.ActionResult{}, errors.New("first load failed"))
	if s.requirementsEstimateBadge() != "—" {
		t.Fatal("a failed first load became zero")
	}
	partial := estimateBadgeResult("p:2", "5")
	partial.Data.(map[string]any)["failures"] = []string{"incomplete"}
	s.finishRequirementListJob(estimateBadgeRequest(s, "p:2"), partial, nil)
	if s.requirementsEstimateBadge() != "—" {
		t.Fatal("a partial first load became an accurate total")
	}
	estimateBadgeComplete(s, "p:3")
	if s.requirementsEstimateBadge() != "0" {
		t.Fatal("a complete empty Sprint did not show zero")
	}
}
