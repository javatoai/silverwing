package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

func requirementProgressFixture(t *testing.T) (*app, *requirementListProgressState, *requirementListJob) {
	t.Helper()
	s := estimateBadgeApp(t)
	s.page, s.requirementsSelectedSprintKey = "requirements", "p:1"
	s.requirementsSprints = []requirementSprint{{ProjectKey: "p", ID: "1", Title: "Sprint 示例", Status: "进行中"}}
	s.documentContent, s.documentDirty = "未保存的资料草稿", true
	p := s.newRequirementListProgress("p:1")
	request := estimateBadgeRequest(s, "p:1")
	request.action.Kind = "requirements.refresh"
	s.bindRequirementListProgress(request)
	t.Cleanup(request.cancel)
	if request.progress != p {
		t.Fatal("manual progress was not bound to its list request")
	}
	return s, p, request
}

func TestRequirementListProgressReopensAndSurvivesNavigation(t *testing.T) {
	s, p, request := requirementProgressFixture(t)
	p.open = false
	generation := s.requirementListJobs.generation
	for range 3 {
		s.refreshRequirementListWithProgress("p:1")
	}
	if !p.open || s.requirementListProgress != p || s.requirementListJobs.current != request || s.requirementListJobs.generation != generation {
		t.Fatal("reopening progress restarted a list read")
	}
	s.page = "tasks"
	s.observeRequirementListProgress()
	if p.open || request.ctx.Err() != nil {
		t.Fatal("leaving the page did not hide the dialog, or canceled the request")
	}
	s.page = "requirements"
	s.requestRequirements("p:1", false)
	if s.requirementListJobs.current != request || s.requirementListJobs.generation != generation {
		t.Fatal("page re-entry replaced the manual refresh")
	}
	s.refreshRequirementListWithProgress("p:1")
	if !p.open || !s.documentDirty || s.documentContent != "未保存的资料草稿" {
		t.Fatal("reopening lost the progress or the unrelated draft")
	}
}

type listProgressBackendFixture struct{ regular, progress int }

func (*listProgressBackendFixture) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}
func (b *listProgressBackendFixture) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	b.regular++
	return application.ActionResult{}, errors.New("unexpected duplicate or fallback dispatch")
}
func (b *listProgressBackendFixture) DispatchRequirementList(_ context.Context, action application.Action, progress func(application.RequirementListProgress)) (application.ActionResult, error) {
	b.progress++
	if action.Kind != "requirements.refresh" || action.Payload["expectedIdentity"] != "fixture-account-a" {
		return application.ActionResult{}, errors.New("wrong refresh target")
	}
	progress(application.RequirementListProgress{Sequence: 1, Phase: "account", Status: "done"})
	return estimateBadgeResult("p:1"), nil
}

func TestRequirementListProgressDispatchesOnceThroughOptionalCapability(t *testing.T) {
	s, p, request := requirementProgressFixture(t)
	backend := &listProgressBackendFixture{}
	s.backend = backend
	for range 4 {
		p.open = false
		s.refreshRequirementListWithProgress("p:1")
	}
	result, err := executeRequirementListJob(backend, request)
	if err != nil {
		t.Fatal(err)
	}
	s.finishRequirementListJob(request, result, nil)
	if backend.progress != 1 || backend.regular != 0 || p.running || p.open {
		t.Fatal("progress viewing initiated another dispatch")
	}
}

func TestRequirementListProgressEventsAndLateScopes(t *testing.T) {
	for _, change := range []string{"home", "project", "sprint", "account", "profile", "new-request"} {
		t.Run(change, func(t *testing.T) {
			s, p, request := requirementProgressFixture(t)
			event := application.RequirementListProgress{Sequence: 2, Phase: "items", Status: "running", WorkItemType: "bug", Label: "缺陷", Completed: 2, Total: 4}
			s.applyRequirementListProgress(request, event)
			if p.steps["items:bug"].Sequence != 2 {
				t.Fatal("real progress event was ignored")
			}
			event.Sequence, event.Status = 1, "done"
			s.applyRequirementListProgress(request, event)
			if p.steps["items:bug"].Status != "running" {
				t.Fatal("an out-of-order event regressed progress")
			}
			switch change {
			case "home":
				s.snapshot.Home += "-other"
			case "project":
				s.configJSON = strings.ReplaceAll(s.configJSON, `"p"`, `"other-project"`)
			case "sprint":
				s.activateRequirementSprint("p:2")
			case "account":
				s.requirementIdentityJobs.identity = "different-account"
			case "profile":
				s.requirementIdentityJobs.context.profileModified++
			case "new-request":
				s.newRequirementListProgress("p:1")
			}
			event.Sequence, event.Status = 3, "done"
			s.applyRequirementListProgress(request, event)
			s.finishRequirementListJob(request, estimateBadgeResult("p:1", "99"), nil)
			if p.steps["items:bug"].Status != "running" || p.message != "" {
				t.Fatal("a late result updated the old progress session")
			}
			s.observeRequirementListProgress()
			if change != "new-request" && s.requirementListProgress != nil {
				t.Fatal("changed scope left its old dialog available")
			}
		})
	}
}

func TestRequirementListProgressCompletionAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "empty", "partial", "error", "format-error", "hidden-error"} {
		t.Run(mode, func(t *testing.T) {
			s, p, request := requirementProgressFixture(t)
			old := application.Requirement{ID: "old", Title: "保留的需求"}
			s.requirements, request.items = []application.Requirement{old}, []application.Requirement{old}
			result := estimateBadgeResult("p:1", "2")
			var err error
			switch mode {
			case "empty":
				result = estimateBadgeResult("p:1")
			case "partial":
				result.Data.(map[string]any)["failures"] = []string{"缺陷查询超时"}
			case "error", "hidden-error":
				err = errors.New("读取超时")
				p.open = mode != "hidden-error"
			case "format-error":
				result.Data = map[string]any{"items": "invalid"}
			}
			s.finishRequirementListJob(request, result, err)
			if p.running {
				t.Fatal("finished read left progress running")
			}
			if mode == "success" || mode == "empty" {
				if p.open || p.message == "" || p.notificationError != "" || p.steps["apply"].Status != "done" {
					t.Fatal("successful application did not close and notify")
				}
				if mode == "empty" && p.message != "刷新完成，暂无工作项" {
					t.Fatal("empty response was not a successful refresh")
				}
			} else if len(p.failures) == 0 || p.notificationError == "" || p.message != "" || p.open != (mode != "hidden-error") || s.requirements[0].ID != "old" {
				t.Fatal("failure lost the error, previous list, or hidden state")
			}
			if !s.documentDirty || s.documentContent != "未保存的资料草稿" {
				t.Fatal("list refresh changed the document draft")
			}
		})
	}
}

func TestRequirementListProgressIdentityProbeFailureAndAutomaticReads(t *testing.T) {
	s := estimateBadgeApp(t)
	s.page, s.requirementsSelectedSprintKey = "requirements", "p:1"
	p := s.newRequirementListProgress("p:1")
	state := s.requirementIdentityJobs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := &requirementIdentityJob{generation: state.generation, context: state.context, ctx: ctx, cancel: cancel}
	state.current = request
	state.pending = &requirementIdentityListRequest{sprintKey: "p:1", refresh: true, progress: p}
	s.finishRequirementIdentityProbe(state, request, application.RequirementIdentitySnapshot{}, errors.New("offline"))
	if p.running || !p.open || len(p.failures) != 1 || s.requirementsLoading {
		t.Fatal("identity failure left the manual progress pending")
	}
	automatic := estimateBadgeApp(t)
	automatic.page, automatic.requirementsSelectedSprintKey = "requirements", "p:1"
	autoRequest := estimateBadgeRequest(automatic, "p:1")
	defer autoRequest.cancel()
	automatic.bindRequirementListProgress(autoRequest)
	if automatic.requirementListProgress != nil || autoRequest.onProgress != nil {
		t.Fatal("automatic read created a progress dialog")
	}
}

func TestRequirementListProgressRejectsStaleRetryTarget(t *testing.T) {
	s := estimateBadgeApp(t)
	s.page, s.requirementsSelectedSprintKey = "requirements", "p:2"
	s.refreshRequirementListWithProgress("p:1")
	if s.requirementListProgress != nil || s.requirementsLoading {
		t.Fatal("stale retry started another Sprint")
	}
	s.configJSON = `{"meegleProjects":[]}`
	s.refreshRequirementListWithProgress("p:2")
	if s.requirementListProgress != nil || s.requirementsLoading {
		t.Fatal("removed project left a stalled progress session")
	}
}

func TestRequirementListProgressDialogLayoutsAndDismissal(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, failed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-dark-%t-failed-%t", width, dark, failed), func(t *testing.T) {
					s, p, request := requirementProgressFixture(t)
					p.sprintTitle = strings.Repeat("长 Sprint 名称与项目说明 ", 8)
					s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 1, Phase: "account", Status: "done"})
					s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 2, Phase: "sprints", Status: "done", Completed: 1, Total: 1})
					s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 3, Phase: "items", Status: "done", WorkItemType: "userstory", Completed: 1, Total: 4})
					s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 4, Phase: "items", Status: "done", WorkItemType: "technical", Completed: 2, Total: 4})
					s.applyRequirementListProgress(request, application.RequirementListProgress{Sequence: 5, Phase: "items", Status: "running", WorkItemType: "bug", Label: "缺陷", Completed: 2, Total: 4})
					if failed {
						s.failRequirementListProgress(p, []string{strings.Repeat("缺陷查询超时，请重试。", 30)})
						s.requirementsLoading = false
					}
					tester := ui.NewTester(func(c *ui.Context) {
						ui.Column(c).Fill().Background(setTheme(c, dark).bg)
						s.renderRequirementListProgressDialog(c, setTheme(c, dark))
					}, width, 800)
					panel, found := tester.Find("需求刷新进度弹窗")
					if !found || panel.W > 600 || panel.X < 0 || panel.Y < 0 || panel.Y+panel.H > 800 {
						t.Fatalf("progress panel escaped viewport: %+v", panel)
					}
					buttonName := "收起"
					if failed {
						buttonName = "关闭"
						if !tester.HasText("重试") {
							t.Fatal("failed refresh has no retry")
						}
					}
					button, found := tester.Find(buttonName)
					if !found || button.Y+button.H > panel.Y+panel.H {
						t.Fatal("progress footer is not visible")
					}
					captureDesktopTestFrame(t, tester, fmt.Sprintf("list-progress-%d-dark-%t-failed-%t", width, dark, failed))
					if !failed {
						for _, way := range []string{"button", "escape", "backdrop"} {
							p.open = true
							tester.Frame()
							switch way {
							case "button":
								if err := tester.Click("收起"); err != nil {
									t.Fatal(err)
								}
							case "escape":
								tester.Key(0, ui.KeyEscape)
							case "backdrop":
								tester.ClickAt(2, 2)
							}
							tester.Frame()
							if p.open || !p.running || request.ctx.Err() != nil {
								t.Fatalf("%s canceled the refresh or did not hide", way)
							}
						}
					} else {
						if err := tester.Click("重试"); err != nil {
							t.Fatal(err)
						}
						if next := s.requirementListProgress; next == p || next == nil || next.sprintKey != "p:1" {
							t.Fatal("retry did not start a fresh session for the same Sprint")
						}
					}
				})
			}
		}
	}
}
