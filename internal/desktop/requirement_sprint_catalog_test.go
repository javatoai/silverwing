package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type sprintCatalogTestReply struct {
	result application.ActionResult
	err    error
}

type sprintCatalogTestCall struct {
	action application.Action
	ctx    context.Context
	reply  chan sprintCatalogTestReply
}

type sprintCatalogTestBackend struct {
	calls       chan sprintCatalogTestCall
	afterCancel chan struct{}
}

func (*sprintCatalogTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot read")
}

func (*sprintCatalogTestBackend) RequirementIdentity(context.Context) (application.RequirementIdentitySnapshot, error) {
	return application.RequirementIdentitySnapshot{State: application.RequirementIdentityAuthenticated, Identity: "fixture-account-a"}, nil
}

func (b *sprintCatalogTestBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	call := sprintCatalogTestCall{action: action, ctx: ctx, reply: make(chan sprintCatalogTestReply, 1)}
	b.calls <- call
	select {
	case reply := <-call.reply:
		return reply.result, reply.err
	case <-ctx.Done():
		if b.afterCancel != nil {
			<-b.afterCancel
		}
		return application.ActionResult{}, ctx.Err()
	}
}

type sprintCatalogTestWindow struct{ updates chan func() }

func (w *sprintCatalogTestWindow) Update(f func()) { w.updates <- f }
func (*sprintCatalogTestWindow) Invalidate()       {}
func (*sprintCatalogTestWindow) IsDestroyed() bool { return false }

func sprintCatalogFixture(t *testing.T) (*app, *sprintCatalogTestBackend, *sprintCatalogTestWindow) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s := estimateBadgeApp(t)
	s.page = "tasks" // The startup/sidebar path must also refresh the directory.
	s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty = "unsaved material draft", true
	b := &sprintCatalogTestBackend{calls: make(chan sprintCatalogTestCall, 16)}
	w := &sprintCatalogTestWindow{updates: make(chan func(), 16)}
	s.backend, s.ensureRequirementListJobs().window = b, w
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.requirementUnit.closeRequirementListJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, b, w
}

func sprintCatalogOption(project, id, status string) requirementSprint {
	return requirementSprint{ProjectKey: project, ProjectName: project, ID: id, Title: project + " Sprint " + id, Status: status}
}

func sprintCatalogListResult(sprint string, cached bool, stamp int64, options []requirementSprint, ids ...string) application.ActionResult {
	result := estimateBadgeResult(sprint, ids...)
	data := result.Data.(map[string]any)
	data["sprints"], data["sprintsFetchedAt"], data["fromCache"], data["fetchedAt"] = options, stamp, cached, stamp
	return result
}

func sprintCatalogApplyList(t *testing.T, s *app, sprint string, result application.ActionResult) *requirementListJob {
	t.Helper()
	request := estimateBadgeRequest(s, sprint)
	t.Cleanup(request.cancel)
	s.finishRequirementListJob(request, result, nil)
	return request
}

func sprintCatalogNextCall(t *testing.T, b *sprintCatalogTestBackend) sprintCatalogTestCall {
	t.Helper()
	select {
	case call := <-b.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("Sprint worker did not dispatch")
		return sprintCatalogTestCall{}
	}
}

func sprintCatalogFinish(t *testing.T, w *sprintCatalogTestWindow, job *requirementListJob, call sprintCatalogTestCall, result application.ActionResult, err error) {
	t.Helper()
	call.reply <- sprintCatalogTestReply{result: result, err: err}
	select {
	case update := <-w.updates:
		update()
	case <-time.After(time.Second):
		t.Fatal("Sprint worker did not schedule its result")
	}
	select {
	case <-job.done:
	case <-time.After(time.Second):
		t.Fatal("Sprint worker was not joined")
	}
}

func sprintCatalogDirectoryResult(stamp int64, options ...requirementSprint) application.ActionResult {
	return application.ActionResult{Data: map[string]any{"sprints": options, "fetchedAt": stamp}}
}

func sprintCatalogAssertNoCall(t *testing.T, b *sprintCatalogTestBackend) {
	t.Helper()
	select {
	case call := <-b.calls:
		t.Fatalf("unexpected extra dispatch: %+v", call.action)
	default:
	}
}

func TestRequirementSprintCatalogCachedStartupRefreshesOnceWithoutReplacingContent(t *testing.T) {
	s, b, w := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "cached"))
	job := s.requirementUnit.state.requirementListJobs.catalogCurrent
	call := sprintCatalogNextCall(t, b)
	if job == nil || call.action.Kind != "requirements.sprints-refresh" || call.action.Payload["expectedIdentity"] != "fixture-account-a" ||
		len(call.action.Payload) != 1 || job.onProgress != nil || s.requirementUnit.state.requirementListProgress != nil {
		t.Fatal("cached startup did not issue one account-bound directory-only read")
	}
	if s.requirementUnit.state.requirementsLoading || len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != "cached" || s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || s.page != "tasks" {
		t.Fatal("directory refresh delayed or replaced cached sidebar/list content")
	}
	selected := s.requirementUnit.state.selectedRequirementKey
	for range 3 {
		s.startRequirementSprintCatalogRefresh()
		s.observeRequirementIdentityJobs()
	}
	if s.requirementUnit.state.requirementListJobs.catalogCurrent != job {
		t.Fatal("ordinary observations replaced the one-shot directory read")
	}
	sprintCatalogAssertNoCall(t, b)
	fresh := sprintCatalogOption("p", "1", "进行中")
	fresh.Title = "updated directory title"
	sprintCatalogFinish(t, w, job, call, sprintCatalogDirectoryResult(200, fresh), nil)
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, []requirementSprint{fresh}) || s.requirementUnit.state.requirements[0].ID != "cached" || s.requirementUnit.state.selectedRequirementKey != selected ||
		s.requirementUnit.state.requirementListProgress != nil || s.lastError != "" || !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "unsaved material draft" {
		t.Fatal("directory application changed the current items, selection, draft, or opened an error/progress flow")
	}
	// Re-entering a cached Sprint cannot restore an older catalogue or start a
	// second background read within the same confirmed account session.
	sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("p:1", true, 100, append(options, sprintCatalogOption("p", "expired", "未开始")), "cached"))
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, []requirementSprint{fresh}) || s.requirementUnit.state.requirementListJobs.catalogCurrent != nil {
		t.Fatal("an old per-Sprint cache resurrected the old directory")
	}
	sprintCatalogAssertNoCall(t, b)
}

func TestRequirementSprintCatalogLiveCompleteReadNeedsNoBackgroundRefresh(t *testing.T) {
	for _, itemFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete-list", true: "items-failed"}[itemFailure], func(t *testing.T) {
			s, b, _ := sprintCatalogFixture(t)
			options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
			result := sprintCatalogListResult("p:1", false, 100, options, "live")
			if itemFailure {
				result.Data.(map[string]any)["failures"] = []string{"item query unavailable"}
				result.Data.(map[string]any)["completedQueries"] = 0
			}
			sprintCatalogApplyList(t, s, "p:1", result)
			s.startRequirementSprintCatalogRefresh()
			if s.requirementUnit.state.requirementListJobs.catalogCurrent != nil || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 100 {
				t.Fatal("a live complete directory was not shared when the work-item result finished")
			}
			sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("p:1", true, 90, options, "cached"))
			sprintCatalogAssertNoCall(t, b)
		})
	}
}

func TestRequirementSprintCatalogKeepsValidManualSprintAcrossBackgroundResult(t *testing.T) {
	s, b, w := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中"), sprintCatalogOption("p", "2", "未开始")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "first"))
	background, backgroundCall := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
	s.requestRequirements("p:2", false)
	manual, manualCall := s.requirementUnit.state.requirementListJobs.current, sprintCatalogNextCall(t, b)
	if manualCall.action.Kind != "requirements.list" || manualCall.action.Payload["sprintKey"] != "p:2" {
		t.Fatal("manual selection did not use the normal cache-aware list read")
	}
	sprintCatalogFinish(t, w, manual, manualCall, sprintCatalogListResult("p:2", true, 100, options, "manual"), nil)
	selected := s.requirementUnit.state.selectedRequirementKey
	sprintCatalogFinish(t, w, background, backgroundCall, sprintCatalogDirectoryResult(200, options...), nil)
	if s.requirementUnit.state.requirementsSelectedSprintKey != "p:2" || len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != "manual" || s.requirementUnit.state.selectedRequirementKey != selected || s.requirementUnit.state.requirementListProgress != nil {
		t.Fatal("directory refresh replaced a still-valid manual Sprint or its shown items")
	}
	sprintCatalogAssertNoCall(t, b)
}

func TestRequirementSprintCatalogExpiredSelectionResolvesAndLoadsItsOwnItems(t *testing.T) {
	for _, test := range []struct {
		name, defaultProject, want string
		options                    []requirementSprint
	}{
		{name: "global-unique", want: "p:2", options: []requirementSprint{sprintCatalogOption("p", "2", "进行中"), sprintCatalogOption("p", "3", "未开始")}},
		{name: "global-unique-outside-default", defaultProject: "p", want: "other:9", options: []requirementSprint{sprintCatalogOption("other", "9", "进行中"), sprintCatalogOption("p", "2", "未开始")}},
		{name: "default-project-unique", defaultProject: "p", want: "p:2", options: []requirementSprint{sprintCatalogOption("other", "9", "进行中"), sprintCatalogOption("p", "2", "进行中")}},
		{name: "ambiguous-default", defaultProject: "p", options: []requirementSprint{sprintCatalogOption("p", "2", "进行中"), sprintCatalogOption("p", "3", "进行中")}},
		{name: "ambiguous-without-default", options: []requirementSprint{sprintCatalogOption("p", "2", "进行中"), sprintCatalogOption("other", "3", "进行中")}},
		{name: "only-not-started", options: []requirementSprint{sprintCatalogOption("p", "2", "未开始")}},
		{name: "complete-empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, b, w := sprintCatalogFixture(t)
			if test.defaultProject != "" {
				s.settingsUnit.state.configJSON = strings.TrimSuffix(s.settingsUnit.state.configJSON, "}") + `,"meegleDefaultSprintProjectKey":"` + test.defaultProject + `"}`
				s.observeRequirementListIdentity()
			}
			sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, []requirementSprint{sprintCatalogOption("p", "1", "进行中")}, "expired-items"))
			job, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
			sprintCatalogFinish(t, w, job, call, sprintCatalogDirectoryResult(200, test.options...), nil)
			if s.requirementUnit.state.requirementsSelectedSprintKey != test.want || len(s.requirementUnit.state.requirements) != 0 || s.requirementUnit.state.selectedRequirementKey != "" || s.requirementUnit.state.requirementListProgress != nil || !slices.Equal(s.requirementUnit.state.requirementsSprints, test.options) {
				t.Fatal("expired selection retained/relabelled old items or chose an unsafe Sprint")
			}
			if test.want == "" {
				if s.requirementUnit.state.requirementListJobs.current != nil || s.requirementUnit.state.requirementsLoading || !s.requirementUnit.state.requirementsLoaded {
					t.Fatal("ambiguous/empty directory started an automatic item query")
				}
				sprintCatalogAssertNoCall(t, b)
				return
			}
			list, listCall := s.requirementUnit.state.requirementListJobs.current, sprintCatalogNextCall(t, b)
			if listCall.action.Kind != "requirements.list" || listCall.action.Payload["sprintKey"] != test.want || listCall.action.Payload["expectedIdentity"] != "fixture-account-a" {
				t.Fatal("replacement Sprint bypassed the normal list request path")
			}
			sprintCatalogFinish(t, w, list, listCall, sprintCatalogListResult(test.want, false, 201, test.options, "replacement-items"), nil)
			if len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != "replacement-items" || s.requirementUnit.state.requirementsSelectedSprintKey != test.want {
				t.Fatal("replacement list did not apply its own Sprint items")
			}
		})
	}
}

func TestRequirementSprintCatalogSnapshotsAndLegacyResultsCannotRestoreOlderChoices(t *testing.T) {
	s, b, _ := sprintCatalogFixture(t)
	old := []requirementSprint{sprintCatalogOption("p", "1", "进行中"), sprintCatalogOption("p", "2", "未开始"), sprintCatalogOption("p", "old", "未开始")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", false, 100, old, "one"))
	s.activateRequirementSprint("p:2")
	sprintCatalogApplyList(t, s, "p:2", sprintCatalogListResult("p:2", true, 100, old, "two"))
	fresh := old[:2]
	sprintCatalogApplyList(t, s, "p:2", sprintCatalogListResult("p:2", false, 200, fresh, "two-live"))
	s.activateRequirementSprint("p:1")
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, fresh) || s.requirementUnit.state.requirements[0].ID != "one" {
		t.Fatal("restoring a per-Sprint view restored its old directory")
	}
	legacy := sprintCatalogListResult("p:1", false, 0, old, "legacy-items")
	legacy.Data.(map[string]any)["failures"] = []string{"catalogue partial"}
	sprintCatalogApplyList(t, s, "p:1", legacy)
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, fresh) {
		t.Fatal("partial/legacy catalogue replaced the latest complete choices")
	}
	sprintCatalogAssertNoCall(t, b)
}

func TestRequirementSprintCatalogGenericResultsAndCompleteEmptyShareLatestDirectory(t *testing.T) {
	s, b, _ := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", false, 100, options, "shown"))
	fresh := []requirementSprint{options[0], sprintCatalogOption("p", "2", "未开始")}
	s.applyRequirementsResult(sprintCatalogListResult("p:1", false, 200, fresh, "generic-live"))
	if !slices.Equal(s.requirementSprintCatalog(), fresh) || s.requirementUnit.state.requirements[0].ID != "generic-live" {
		t.Fatal("generic action results bypassed the shared directory")
	}
	s.applyRequirementsResult(sprintCatalogListResult("", false, 300, nil))
	if s.requirementUnit.state.requirementsSelectedSprintKey != "" || len(s.requirementUnit.state.requirements) != 0 || len(s.requirementSprintCatalog()) != 0 ||
		s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 300 || s.requirementUnit.state.requirementsLoadError != "" || !s.requirementUnit.state.requirementsLoaded {
		t.Fatal("complete empty catalogue was confused with a partial failure")
	}
	sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("p:1", true, 100, options, "expired-items"))
	if s.requirementUnit.state.requirementsSelectedSprintKey != "" || len(s.requirementUnit.state.requirements) != 0 || len(s.requirementUnit.state.requirementsSprints) != 0 {
		t.Fatal("an old per-Sprint cache revived items after an authoritative empty catalogue")
	}
	sprintCatalogAssertNoCall(t, b)
}

func TestRequirementSprintCatalogLiveListRetiresOlderBackgroundAndTimestamp(t *testing.T) {
	s, b, _ := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "cached"))
	background, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
	fresh := []requirementSprint{options[0], sprintCatalogOption("p", "2", "未开始")}
	sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("p:1", false, 300, fresh, "live"))
	if call.ctx.Err() == nil || s.requirementUnit.state.requirementListJobs.catalogCurrent != nil {
		t.Fatal("a complete live list left redundant directory work running")
	}
	s.finishRequirementSprintCatalogJob(background, sprintCatalogDirectoryResult(400, options...), nil)
	// Even a still-current live response cannot regress an independently newer
	// complete catalogue just because it reached the UI later.
	sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("p:1", false, 200, options, "later-items"))
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, fresh) || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 300 || s.requirementUnit.state.requirements[0].ID != "later-items" {
		t.Fatal("an older list/catalogue response regressed the shared directory")
	}
	sprintCatalogAssertNoCall(t, b)
}

func TestRequirementSprintCatalogEmptySelectionCanManuallyRetryWithoutProgressDialog(t *testing.T) {
	s, _, _ := sprintCatalogFixture(t)
	s.page = "requirements"
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("", false, 100, nil))
	holdRequirementProgressIdentityProbe(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		s.requirementList(c, setTheme(c, false), true)
	}, 360, 480)
	if err := tester.Click("刷新 Sprint 列表"); err != nil {
		t.Fatal(err)
	}
	pending := s.requirementUnit.state.requirementIdentityJobs.pending
	if pending == nil || !pending.refresh || pending.sprintKey != "" || s.requirementUnit.state.requirementListProgress != nil {
		t.Fatal("an empty catalogue has no account-aware manual retry, or opened a progress dialog")
	}
}

func TestRequirementSprintCatalogMenuUsesCanonicalChoices(t *testing.T) {
	s, _, _ := sprintCatalogFixture(t)
	current := sprintCatalogOption("p", "1", "进行中")
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", false, 200, []requirementSprint{current}, "shown"))
	// An older rendering snapshot must not supply the option menu.
	s.requirementUnit.state.requirementsSprints = []requirementSprint{current, sprintCatalogOption("p", "expired", "未开始")}
	tester := ui.NewTester(func(c *ui.Context) {
		s.requirementList(c, setTheme(c, false), true)
	}, 360, 480)
	if err := tester.Click("选择 Sprint"); err != nil {
		t.Fatal(err)
	}
	if options := tester.Menu(); len(options) != 1 || strings.Contains(options[0], "expired") {
		t.Fatalf("Sprint menu rendered an older catalogue: %v", options)
	}
}

func TestRequirementSprintCatalogBackgroundFailureIsSilentAndKeepsCurrentContent(t *testing.T) {
	for _, mode := range []string{"error", "partial", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s, b, w := sprintCatalogFixture(t)
			options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
			sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "cached"))
			job, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
			result, err := sprintCatalogDirectoryResult(200, sprintCatalogOption("other", "2", "进行中")), error(nil)
			switch mode {
			case "error":
				err = errors.New("directory timeout")
			case "partial":
				result.Data.(map[string]any)["fetchedAt"] = int64(0)
				result.Data.(map[string]any)["failures"] = []string{"project directory timeout"}
			case "malformed":
				result.Data.(map[string]any)["sprints"] = "invalid"
			}
			sprintCatalogFinish(t, w, job, call, result, err)
			if s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || s.requirementUnit.state.requirements[0].ID != "cached" || !slices.Equal(s.requirementUnit.state.requirementsSprints, options) ||
				s.requirementUnit.state.requirementsLoadError != "" || len(s.requirementUnit.state.requirementsFailures) != 0 || s.lastError != "" || s.requirementUnit.state.requirementListProgress != nil {
				t.Fatal("background failure replaced current content or opened a new failure flow")
			}
			s.startRequirementSprintCatalogRefresh()
			sprintCatalogAssertNoCall(t, b)
		})
	}
}

func TestRequirementSprintCatalogIncompleteListKeepsKnownValidSelectionAndItems(t *testing.T) {
	for _, unboundSprint := range []string{"", "other:9"} {
		t.Run("unbound-"+unboundSprint, func(t *testing.T) {
			s, b, _ := sprintCatalogFixture(t)
			options := []requirementSprint{sprintCatalogOption("p", "1", "进行中"), sprintCatalogOption("other", "9", "未开始")}
			sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", false, 100, options, "valid-current-items"))
			selected := s.requirementUnit.state.selectedRequirementKey
			partial := sprintCatalogListResult(unboundSprint, false, 0, options, "unbound-wrong-items")
			if unboundSprint == "" {
				delete(partial.Data.(map[string]any), "selectedSprintKey")
			}
			partial.Data.(map[string]any)["failures"] = []string{"project directory timeout"}
			partial.Data.(map[string]any)["completedQueries"] = 1
			sprintCatalogApplyList(t, s, "p:1", partial)
			if s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != "valid-current-items" || s.requirementUnit.state.selectedRequirementKey != selected ||
				!slices.Equal(s.requirementUnit.state.requirementsSprints, options) || !strings.Contains(s.requirementUnit.state.requirementsLoadError, "project directory timeout") || s.requirementUnit.state.requirementsCompletedQueries != 0 {
				t.Fatal("an incomplete directory discarded a confirmed valid view or rebound foreign items to it")
			}
			sprintCatalogAssertNoCall(t, b)
		})
	}
}

func TestRequirementSprintCatalogCompleteReselectSettlesManualProgress(t *testing.T) {
	s, b, _ := sprintCatalogFixture(t)
	s.page = "requirements"
	old := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", false, 100, old, "expired"))
	p := s.newRequirementListProgress("p:1")
	request := estimateBadgeRequest(s, "p:1")
	t.Cleanup(request.cancel)
	request.action.Kind = "requirements.refresh"
	s.bindRequirementListProgress(request)
	next := []requirementSprint{sprintCatalogOption("p", "2", "进行中")}
	s.finishRequirementListJob(request, sprintCatalogListResult("", false, 200, next), nil)
	if p.running || p.open || s.requirementUnit.state.requirementsSelectedSprintKey != "p:2" || len(s.requirementUnit.state.requirements) != 0 {
		t.Fatal("complete catalogue reselect left the original manual progress running or retained expired items")
	}
	if call := sprintCatalogNextCall(t, b); call.action.Kind != "requirements.list" || call.action.Payload["sprintKey"] != "p:2" {
		t.Fatal("complete manual refresh did not load the new Sprint through the ordinary request path")
	}
}

func TestRequirementSprintCatalogPartialRefreshAfterLegacyCachedStartupKeepsView(t *testing.T) {
	for _, returnedSprint := range []string{"", "other:9"} {
		t.Run("returned-"+returnedSprint, func(t *testing.T) {
			s, b, _ := sprintCatalogFixture(t)
			options := []requirementSprint{sprintCatalogOption("p", "1", "进行中"), sprintCatalogOption("other", "9", "未开始")}
			sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 0, options, "cached-current-items"))
			_ = sprintCatalogNextCall(t, b)
			s.retireRequirementSprintCatalogRequest(s.ensureRequirementListJobs())
			if s.hasRequirementSprintCatalog() {
				t.Fatal("legacy item cache was promoted to an authoritative directory")
			}
			selected := s.requirementUnit.state.selectedRequirementKey
			partial := sprintCatalogListResult(returnedSprint, false, 0, options[1:], "foreign-items")
			partial.Data.(map[string]any)["failures"] = []string{"p directory timeout"}
			partial.Data.(map[string]any)["completedQueries"] = 1
			request := estimateBadgeRequest(s, "p:1")
			t.Cleanup(request.cancel)
			request.action.Kind = "requirements.refresh"
			s.finishRequirementListJob(request, partial, nil)
			if s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != "cached-current-items" || s.requirementUnit.state.selectedRequirementKey != selected ||
				!slices.Equal(s.requirementUnit.state.requirementsSprints, options) || !strings.Contains(s.requirementUnit.state.requirementsLoadError, "p directory timeout") || s.requirementUnit.state.requirementsCompletedQueries != 0 {
				t.Fatal("partial refresh replaced a usable legacy cached view or rebound another Sprint's items")
			}
			if s.hasRequirementSprintCatalog() || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 0 {
				t.Fatal("retaining cached choices fabricated directory completeness")
			}
			sprintCatalogAssertNoCall(t, b)
		})
	}
}

func TestRequirementSprintCatalogRetainedIncompleteChoicesYieldToCompleteDirectory(t *testing.T) {
	s, b, _ := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中"), sprintCatalogOption("other", "9", "未开始")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 0, options, "cached-current-items"))
	_ = sprintCatalogNextCall(t, b)
	s.retireRequirementSprintCatalogRequest(s.ensureRequirementListJobs())
	partial := sprintCatalogListResult("other:9", false, 0, options[1:], "foreign-items")
	partial.Data.(map[string]any)["failures"] = []string{"p directory timeout"}
	sprintCatalogApplyList(t, s, "p:1", partial)
	if s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || s.hasRequirementSprintCatalog() {
		t.Fatal("partial directory did not retain the usable view without claiming completeness")
	}
	next := []requirementSprint{sprintCatalogOption("other", "9", "进行中")}
	sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("", false, 200, next))
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, next) || s.requirementUnit.state.requirementsSelectedSprintKey != "other:9" || len(s.requirementUnit.state.requirements) != 0 ||
		s.requirementUnit.state.selectedRequirementKey != "" || !s.hasRequirementSprintCatalog() || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 200 {
		t.Fatal("retained incomplete choices blocked a complete directory or relabelled expired items")
	}
	if call := sprintCatalogNextCall(t, b); call.action.Kind != "requirements.list" || call.action.Payload["sprintKey"] != "other:9" {
		t.Fatal("complete directory did not request replacement Sprint items through the normal list path")
	}
}

func TestRequirementSprintCatalogIncompleteSameBindingKeepsAllCachedChoices(t *testing.T) {
	for _, priorItems := range []bool{false, true} {
		t.Run(map[bool]string{false: "newly-readable-items", true: "retained-items"}[priorItems], func(t *testing.T) {
			s, b, _ := sprintCatalogFixture(t)
			options := []requirementSprint{sprintCatalogOption("p", "1", "进行中"), sprintCatalogOption("other", "9", "未开始")}
			ids := []string(nil)
			if priorItems {
				ids = []string{"cached-current-items"}
			}
			sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 0, options, ids...))
			_ = sprintCatalogNextCall(t, b)
			s.retireRequirementSprintCatalogRequest(s.ensureRequirementListJobs())
			partial := sprintCatalogListResult("p:1", false, 0, options[:1], "new-current-items")
			partial.Data.(map[string]any)["failures"] = []string{"other directory timeout"}
			sprintCatalogApplyList(t, s, "p:1", partial)
			wantItem := "new-current-items"
			if priorItems {
				wantItem = "cached-current-items"
			}
			if !slices.Equal(s.requirementUnit.state.requirementsSprints, options) || s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || len(s.requirementUnit.state.requirements) != 1 || s.requirementUnit.state.requirements[0].ID != wantItem ||
				len(s.requirementUnit.state.requirementsFailures) != 1 || s.requirementUnit.state.requirementsFailures[0] != "other directory timeout" {
				t.Fatal("same-binding partial directory removed cached options or changed the existing item failure policy")
			}
			if s.hasRequirementSprintCatalog() || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 0 {
				t.Fatal("retaining partial directory choices fabricated an authoritative timestamp")
			}
			sprintCatalogAssertNoCall(t, b)
		})
	}
}

func TestRequirementSprintCatalogManualRefreshCancelsLateBackgroundBeforeIdentityProbe(t *testing.T) {
	s, b, _ := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "cached"))
	background, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
	holdRequirementProgressIdentityProbe(t, s)
	s.requestRequirements("p:1", true)
	if call.ctx.Err() == nil || s.requirementUnit.state.requirementListJobs.catalogCurrent != nil || s.requirementUnit.state.requirementIdentityJobs.pending == nil || !s.requirementUnit.state.requirementIdentityJobs.pending.refresh {
		t.Fatal("manual refresh did not retire background work before its account check")
	}
	fresh := []requirementSprint{options[0], sprintCatalogOption("p", "new", "未开始")}
	sprintCatalogApplyList(t, s, "p:1", sprintCatalogListResult("p:1", false, 300, fresh, "manual"))
	s.finishRequirementSprintCatalogJob(background, sprintCatalogDirectoryResult(400, options...), nil)
	if !slices.Equal(s.requirementUnit.state.requirementsSprints, fresh) || s.requirementUnit.state.requirements[0].ID != "manual" || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 300 {
		t.Fatal("a late background result regressed a newer manual catalogue")
	}
}

func TestRequirementSprintCatalogRejectsAndCancelsChangedIdentityOrProfile(t *testing.T) {
	for _, change := range []string{"account", "project", "profile"} {
		t.Run(change, func(t *testing.T) {
			s, b, _ := sprintCatalogFixture(t)
			options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
			sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "old-account"))
			job, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
			switch change {
			case "account":
				s.requirementUnit.state.requirementIdentityJobs.identity = "fixture-account-b"
				s.observeRequirementListIdentity()
			case "project":
				s.settingsUnit.state.configJSON = strings.ReplaceAll(s.settingsUnit.state.configJSON, `"p"`, `"another"`)
				s.observeRequirementListIdentity()
			case "profile":
				next := s.requirementUnit.state.requirementIdentityJobs.context
				next.profileModified++
				s.resetRequirementIdentityContext(s.requirementUnit.state.requirementIdentityJobs, next)
			}
			if call.ctx.Err() == nil || s.requirementUnit.state.requirementListJobs.catalogCurrent != nil || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 0 || len(s.requirementUnit.state.requirements) != 0 {
				t.Fatal("changed scope retained the old catalogue worker/content")
			}
			s.finishRequirementSprintCatalogJob(job, sprintCatalogDirectoryResult(200, options...), nil)
			if len(s.requirementUnit.state.requirementsSprints) != 0 || s.requirementUnit.state.requirementsSelectedSprintKey != "" || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 0 {
				t.Fatal("late directory result restored a previous account/config/profile")
			}
			select {
			case <-job.done:
			case <-time.After(time.Second):
				t.Fatal("invalidated directory worker did not finish")
			}
		})
	}
}

func TestRequirementSprintCatalogResultChecksProfileWithoutFocusEvent(t *testing.T) {
	s, b, w := sprintCatalogFixture(t)
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "cached"))
	job, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
	profile := s.requirementUnit.state.requirementIdentityJobs.context.profile
	if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte(`{"fixture":"profile changed during directory read"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sprintCatalogFinish(t, w, job, call, sprintCatalogDirectoryResult(200, options...), nil)
	if s.requirementUnit.state.requirementIdentityJobs.confirmed || s.requirementUnit.state.requirementListJobs.catalogCurrent != nil || s.requirementUnit.state.requirementListJobs.catalogFetchedAt != 0 || len(s.requirementUnit.state.requirements) != 0 {
		t.Fatal("directory completion trusted an account profile changed without a focus event")
	}
}

func TestRequirementSprintCatalogCloseCancelsAndJoinsWorkerBeforeReturning(t *testing.T) {
	s, b, w := sprintCatalogFixture(t)
	b.afterCancel = make(chan struct{})
	options := []requirementSprint{sprintCatalogOption("p", "1", "进行中")}
	sprintCatalogApplyList(t, s, "", sprintCatalogListResult("p:1", true, 100, options, "cached"))
	job, call := s.requirementUnit.state.requirementListJobs.catalogCurrent, sprintCatalogNextCall(t, b)
	closed := make(chan error, 1)
	go func() { closed <- s.requirementUnit.closeRequirementListJobs(context.Background()) }()
	select {
	case <-call.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("close did not cancel the directory worker")
	}
	select {
	case err := <-closed:
		t.Fatalf("close returned before the worker completed: %v", err)
	default:
	}
	close(b.afterCancel)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not join its cancelled directory worker")
	}
	select {
	case <-job.done:
	default:
		t.Fatal("close left the directory worker running")
	}
	for len(w.updates) > 0 {
		(<-w.updates)()
	}
	if s.requirementUnit.state.requirementsSelectedSprintKey != "p:1" || s.requirementUnit.state.requirements[0].ID != "cached" {
		t.Fatal("queued cancelled completion mutated the closed view")
	}
}
