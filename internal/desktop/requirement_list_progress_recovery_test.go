package desktop

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

// A pending probe keeps these tests at the UI intent boundary. No window,
// worker, account read, or list query is started.
func holdRequirementProgressIdentityProbe(t *testing.T, s *app) *requirementIdentityJob {
	t.Helper()
	state := s.ensureRequirementIdentityJobs()
	ctx, cancel := context.WithCancel(context.Background())
	request := &requirementIdentityJob{generation: state.generation, context: state.context, ctx: ctx, cancel: cancel}
	state.current = request
	t.Cleanup(cancel)
	return request
}

func TestRequirementListProgressInitialFailurePanelRetry(t *testing.T) {
	s := estimateBadgeApp(t)
	s.page, s.backend = "requirements", &estimateBadgeBackend{}
	s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoadError = true, "读取 Sprint 目录超时"
	probe := holdRequirementProgressIdentityProbe(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			s.requirementFailurePanel(c, setTheme(c, false), []string{s.requirementUnit.state.requirementsLoadError}, true)
		})
	}, 640, 360)
	if err := tester.Click("重试需求查询"); err != nil {
		t.Fatal(err)
	}
	pending := s.requirementUnit.state.requirementIdentityJobs.pending
	if pending == nil || !pending.refresh || pending.sprintKey != "" || pending.progress != nil || s.requirementUnit.state.requirementListProgress != nil {
		t.Fatalf("initial failure did not retry the catalogue without a dialog: %+v", pending)
	}
	generation := s.requirementUnit.state.requirementListJobs.generation
	for range 3 {
		s.refreshRequirementListWithProgress("")
	}
	if s.requirementUnit.state.requirementIdentityJobs.pending != pending || s.requirementUnit.state.requirementIdentityJobs.current != probe ||
		s.requirementUnit.state.requirementListJobs.generation != generation || s.requirementUnit.state.requirementListJobs.current != nil {
		t.Fatal("repeated initial retry duplicated or replaced the pending read")
	}
	s.refreshRequirementListWithProgress("p:2")
	if s.requirementUnit.state.requirementIdentityJobs.pending != pending || s.requirementUnit.state.requirementListProgress != nil {
		t.Fatal("a stale selected-Sprint refresh changed the initial retry")
	}
}

func missingRequirementSprintResult(failure string) application.ActionResult {
	return application.ActionResult{Data: map[string]any{
		"items": []application.Requirement{}, "failures": []string{failure},
		"completedQueries": 0, "selectedSprintKey": "",
		"sprints": []requirementSprint{{ProjectKey: "other", ID: "2", Title: "Other active Sprint", Status: "进行中"}},
	}}
}

func TestRequirementListProgressCatalogueFailureRetryEntrances(t *testing.T) {
	for _, failure := range []string{"p 读取 Sprint 目录超时", "所选 Sprint 已不可选，请重新选择"} {
		for _, entrance := range []struct {
			name, button string
			visible      bool
		}{
			{name: "visible-dialog", button: "重试", visible: true},
			{name: "closed-dialog-panel", button: "重试需求查询", visible: true},
			{name: "hidden-failure-panel", button: "重试需求查询", visible: false},
		} {
			t.Run(failure+"/"+entrance.name, func(t *testing.T) {
				s, p, request := requirementProgressFixture(t)
				old := application.Requirement{ID: "old", Title: "上次结果"}
				s.requirementUnit.state.requirements, request.items = []application.Requirement{old}, []application.Requirement{old}
				p.open = entrance.visible
				s.finishRequirementListJob(request, missingRequirementSprintResult(failure), nil)
				s.observeRequirementListProgress()
				if s.requirementUnit.state.requirementListProgress != p || !s.requirementListProgressCurrent(p) || p.running ||
					p.open != entrance.visible || len(p.failures) == 0 || !strings.Contains(p.notificationError, "未完成") {
					t.Fatalf("catalogue outcome lost the settled session or visibility: %+v", p)
				}
				if p.sprintKey != "p:1" || p.viewSprintKey != "" || s.requirementUnit.state.requirementsSelectedSprintKey != "" ||
					len(s.requirementUnit.state.requirements) != 0 || s.requirementUnit.state.requirementListJobs.current != nil {
					t.Fatal("progress changed the existing missing-Sprint selection/list policy or automatically queried another Sprint")
				}
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Children(func() {
						s.requirementFailurePanel(c, setTheme(c, false), []string{s.requirementUnit.state.requirementsLoadError}, true)
					})
					s.renderRequirementListProgressDialog(c, setTheme(c, false))
				}, 800, 640)
				_, dialogVisible := tester.Find("需求刷新进度弹窗")
				if dialogVisible != entrance.visible {
					t.Fatal("a hidden failure reopened its dialog, or a visible error lost its controls")
				}
				if entrance.visible && (!tester.HasText("重试") || !tester.HasText("关闭")) {
					t.Fatal("visible catalogue failure has no actionable retry/close")
				}
				s.backend = &estimateBadgeBackend{}
				probe := holdRequirementProgressIdentityProbe(t, s)
				if entrance.visible && entrance.button == "重试需求查询" {
					if err := tester.Click("关闭"); err != nil {
						t.Fatal(err)
					}
				}
				if err := tester.Click(entrance.button); err != nil {
					t.Fatal(err)
				}
				pending, next := s.requirementUnit.state.requirementIdentityJobs.pending, s.requirementUnit.state.requirementListProgress
				if pending == nil || pending.sprintKey != "p:1" || !pending.refresh || pending.progress != next ||
					next == nil || next == p || next.sprintKey != "p:1" || s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" {
					t.Fatalf("retry lost the original Sprint intent: pending=%+v progress=%+v", pending, next)
				}
				generation := s.requirementUnit.state.requirementListJobs.generation
				for range 3 {
					s.refreshRequirementListWithProgress("p:1")
				}
				if s.requirementUnit.state.requirementIdentityJobs.pending != pending || s.requirementUnit.state.requirementIdentityJobs.current != probe ||
					s.requirementUnit.state.requirementListJobs.generation != generation || s.requirementUnit.state.requirementListJobs.current != nil {
					t.Fatal("reopening the retried session restarted identity/list work")
				}
				if !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "未保存的资料草稿" {
					t.Fatal("catalogue failure/retry changed an unrelated draft")
				}
			})
		}
	}
}

func TestRequirementListProgressResolvedFailureRejectsChangedScope(t *testing.T) {
	for _, change := range []string{"sprint", "home", "project", "account", "profile"} {
		t.Run(change, func(t *testing.T) {
			s, p, request := requirementProgressFixture(t)
			s.finishRequirementListJob(request, missingRequirementSprintResult("p 读取 Sprint 超时"), nil)
			s.backend = &estimateBadgeBackend{}
			holdRequirementProgressIdentityProbe(t, s)
			switch change {
			case "sprint":
				s.activateRequirementSprint("other:2")
			case "home":
				s.snapshot.Home += "-other"
			case "project":
				s.settingsUnit.state.configJSON = strings.ReplaceAll(s.settingsUnit.state.configJSON, `"p"`, `"changed-project"`)
			case "account":
				s.requirementUnit.state.requirementIdentityJobs.identity = "other-account"
			case "profile":
				s.requirementUnit.state.requirementIdentityJobs.context.profileModified++
			}
			s.retryRequirementListProgress(p)
			s.observeRequirementListProgress()
			if s.requirementUnit.state.requirementIdentityJobs.pending != nil || s.requirementUnit.state.requirementListProgress != nil || p.running {
				t.Fatal("an old failure session was resurrected after a user scope change")
			}
		})
	}
}

func TestRequirementListProgressPendingIdentitySurvivesNavigation(t *testing.T) {
	s := estimateBadgeApp(t)
	s.page, s.backend, s.requirementUnit.state.requirementsSelectedSprintKey = "requirements", &estimateBadgeBackend{}, "p:1"
	s.requirementUnit.state.requirementIdentityJobs.page = "requirements"
	probe := holdRequirementProgressIdentityProbe(t, s)
	s.refreshRequirementListWithProgress("p:1")
	p, pending := s.requirementUnit.state.requirementListProgress, s.requirementUnit.state.requirementIdentityJobs.pending
	if p == nil || p.request != nil || pending == nil || pending.progress != p || !pending.refresh {
		t.Fatal("manual list intent was not attached to its pending identity check")
	}
	generation := s.requirementUnit.state.requirementListJobs.generation
	s.page = "tasks"
	s.observeRequirementIdentityJobs()
	s.observeRequirementListProgress()
	if p.open || probe.ctx.Err() != nil {
		t.Fatal("leaving the page canceled the pending identity check instead of hiding progress")
	}
	s.page = "requirements"
	s.observeRequirementIdentityJobs()
	s.requestRequirements("", false)
	s.refreshRequirementListWithProgress("p:1")
	if !p.open || s.requirementUnit.state.requirementListProgress != p || s.requirementUnit.state.requirementIdentityJobs.pending != pending ||
		!pending.refresh || pending.sprintKey != "p:1" || pending.progress != p ||
		s.requirementUnit.state.requirementIdentityJobs.current != probe || s.requirementUnit.state.requirementListJobs.generation != generation || s.requirementUnit.state.requirementListJobs.current != nil {
		t.Fatal("page re-entry/background read replaced the manual pending identity/list intent")
	}
}

func TestRequirementListProgressIncompleteEstimatesKeepsAppliedList(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(map[bool]string{false: "hidden", true: "visible"}[visible], func(t *testing.T) {
			s, p, request := requirementProgressFixture(t)
			p.open = visible
			warning := "个人估分有 1 条未完成：排期读取超时"
			s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 1, Phase: "account", Status: "done"})
			s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 2, Phase: "estimates", Status: "failed", Message: warning, Total: 1})
			result := estimateBadgeResult("p:1", "new")
			items := result.Data.(map[string]any)["items"].([]application.Requirement)
			items[0].WorkItem.MySprintEstimateDays = nil
			items[0].WorkItem.MetadataWarnings = []string{warning}
			s.finishRequirementListJob(request, result, nil)
			if len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != "new" ||
				s.requirementUnit.state.requirements[0].WorkItem.MySprintEstimateDays != nil || len(s.requirementUnit.state.requirements[0].WorkItem.MetadataWarnings) != 1 ||
				s.requirementUnit.state.requirementsLoadError != "" || !s.requirementUnit.state.requirementsLoaded {
				t.Fatal("optional estimate failure discarded the newly readable list")
			}
			if p.running || p.open != visible || p.message != "" || p.notificationError == "" ||
				p.steps["apply"].Status != "done" || p.steps["estimates"].Status != "failed" ||
				p.steps["estimates"].Completed != 0 || len(p.failures) != 1 || p.failures[0] != warning {
				t.Fatalf("incomplete metadata was reported as full success or list application failure: %+v", p)
			}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill()
				s.renderRequirementListProgressDialog(c, setTheme(c, false))
			}, 800, 640)
			_, shown := tester.Find("需求刷新进度弹窗")
			if shown != visible || visible && !tester.HasText(warning) {
				t.Fatal("incomplete estimate details/hidden state were lost")
			}
		})
	}
}
