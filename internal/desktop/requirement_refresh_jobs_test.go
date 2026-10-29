package desktop

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type refreshTestWindow struct{ updates chan func() }

func (w *refreshTestWindow) Update(f func()) { w.updates <- f }
func (*refreshTestWindow) Invalidate()       {}
func (*refreshTestWindow) IsDestroyed() bool { return false }

type refreshTestBackend struct {
	calls                        chan application.Action
	gate                         chan struct{}
	metadataError, commentsError bool
}

func (*refreshTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *refreshTestBackend) Dispatch(ctx context.Context, a application.Action) (application.ActionResult, error) {
	b.calls <- a
	if b.gate != nil {
		select {
		case <-b.gate:
		case <-ctx.Done():
			return application.ActionResult{}, ctx.Err()
		}
	}
	title, status := "updated B", "Done"
	switch a.Kind {
	case "requirements.metadata":
		data := application.RequirementMetadata{ID: "102", ProjectKey: "project-a", Type: "userstory", Title: &title, Status: &status, FetchedAt: 200, WorkItem: &domain.ParticipatedWorkItem{Title: title, Status: &status, Developers: []domain.RequirementPerson{{Name: "new dev"}}, QCOwners: []domain.RequirementPerson{{Name: "new test"}}}, SprintEstimates: map[string]application.RequirementSprintEstimate{"project-a:900": {Days: refreshTestString("5"), FetchedAt: 200}}}
		if b.metadataError {
			data.CacheError = "disk denied"
		}
		return application.ActionResult{Data: data}, nil
	case "requirements.read":
		return application.ActionResult{Data: application.RequirementContent{Body: "new body", Generation: "new-generation", MarkdownPath: "cache/body.md"}}, nil
	case "requirements.comments":
		if b.commentsError {
			return application.ActionResult{}, errors.New("comments offline")
		}
		return application.ActionResult{Data: application.RequirementComments{FetchedAt: 200, Comments: []domain.RequirementComment{{ID: "comment", Content: "new comment"}}}}, nil
	}
	return application.ActionResult{}, errors.New("unexpected action")
}

func refreshTestString(s string) *string { return &s }
func refreshTestFixture(t *testing.T) (*app, *refreshTestBackend, *refreshTestWindow) {
	t.Helper()
	s := newApp(nil, Options{CapturePath: "isolated-requirement-refresh"})
	s.loading, s.page = false, "requirements"
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{"meegleProjects":[{"project_key":"project-a","simple_name":"space"}]}`
	a, b := "https://project.feishu.cn/space/userstory/detail/101", "https://project.feishu.cn/space/userstory/detail/102"
	s.requirementUnit.state.requirements = []application.Requirement{{ID: "101", Title: "A", URL: a, ProjectKey: "project-a", WorkItem: domain.ParticipatedWorkItem{ID: "101", ProjectKey: "project-a", Type: "userstory"}}, {ID: "102", Title: "B", URL: b, ProjectKey: "project-a", WorkItem: domain.ParticipatedWorkItem{ID: "102", ProjectKey: "project-a", Type: "userstory"}}}
	s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 0, requirementKey(s.requirementUnit.state.requirements[0])
	s.requirementUnit.state.requirementsSelectedSprintKey = "project-a:900"
	s.taskUnit.state.tasks = []task{{id: "task-a", requirementURL: a}, {id: "task-b", requirementURL: b}, {id: "task-b2", requirementURL: b + "?source=task"}}
	s.taskUnit.state.selected = 0
	s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty = "unsaved draft", true
	bk := &refreshTestBackend{calls: make(chan application.Action, 20), gate: make(chan struct{})}
	w := &refreshTestWindow{updates: make(chan func(), 5)}
	s.backend = bk
	s.ensureRequirementRefreshJobs().window = w
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeRequirementRefreshJobs(ctx); err != nil {
			t.Error(err)
		}
		s.requirementUnit.closeRequirementReaders()
	})
	return s, bk, w
}
func finishRefreshTest(t *testing.T, w *refreshTestWindow, job *requirementRefreshJob) {
	t.Helper()
	select {
	case update := <-w.updates:
		update()
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not complete")
	}
	select {
	case <-job.done:
	case <-time.After(time.Second):
		t.Fatal("worker not joined")
	}
}

func TestRequirementRefreshContextMenuCoalescesAndSurvivesPageSwitch(t *testing.T) {
	s, b, w := refreshTestFixture(t)
	for _, taskID := range []string{"", "task-b", "task-b2"} {
		r := s.requirementReader(taskID, s.requirementUnit.state.requirements[1].URL)
		s.updateRequirementReaderBody(r, application.RequirementContent{Body: "old body", Generation: "old"})
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			for _, item := range s.requirementUnit.state.requirements {
				s.requirementListItem(c, setTheme(c, false), item, false)
			}
		})
	}, 240, 260)
	if err := tester.RightClick("B"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tester.Menu(), []string{"刷新"}) {
		t.Fatal(tester.Menu())
	}
	if err := tester.ChooseMenuItem("刷新"); err != nil {
		t.Fatal(err)
	}
	job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(s.requirementUnit.state.requirements[1].URL)]
	if job == nil {
		t.Fatal("no refresh job")
	}
	if err := tester.RightClick("B"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tester.Menu(), []string{"刷新中…"}) {
		t.Fatal(tester.Menu())
	}
	if err := tester.ChooseMenuItem("刷新中…"); err == nil {
		t.Fatal("running refresh was enabled")
	}
	tester.Key(0, ui.KeyEscape)
	s.startRequirementRefresh(s.requirementUnit.state.requirements[1].URL + "?duplicate=true")
	s.page = "tasks"
	s.observeRequirementRefreshJobs()
	s.observeRequirementContentJobs()
	if job.ctx.Err() != nil {
		t.Fatal("page switch cancelled refresh")
	}
	close(b.gate)
	finishRefreshTest(t, w, job)
	if len(b.calls) != 3 {
		t.Fatalf("duplicate requests not coalesced: %d", len(b.calls))
	}
	for len(b.calls) > 0 {
		a := <-b.calls
		if a.TaskID != "" || payloadString(a.Payload, "url") != s.requirementUnit.state.requirements[1].URL || a.Payload["force"] != true {
			t.Fatalf("wrong refresh target: %+v", a)
		}
	}
	if s.taskUnit.state.selected != 0 || s.requirementUnit.state.selectedRequirement != 0 || s.requirementUnit.state.selectedRequirementKey != requirementKey(s.requirementUnit.state.requirements[0]) || s.requirementUnit.state.requirementsSelectedSprintKey != "project-a:900" || s.materialsUnit.state.documentContent != "unsaved draft" || !s.materialsUnit.state.documentDirty {
		t.Fatal("refresh changed navigation/draft")
	}
	if s.requirementUnit.state.requirements[1].Title != "updated B" || s.requirementUnit.state.requirements[1].Status != "Done" || s.lastError != "" || s.lastMessage == "" {
		t.Fatalf("refresh not applied: %+v %s", s.requirementUnit.state.requirements[1], s.lastError)
	}
	for _, r := range s.requirementUnit.state.requirementReaders {
		if r.body.Body != "new body" || r.comments.Comments[0].Content != "new comment" {
			t.Fatal("linked reader not updated")
		}
	}
	for _, id := range []string{"task-b", "task-b2"} {
		if got := s.requirementUnit.state.taskRequirementMetadata[id]; got.Metadata.Title == nil || *got.Metadata.Title != "updated B" || len(got.Metadata.Participants.QCOwners) != 1 {
			t.Fatalf("linked task metadata stale: %+v", got)
		}
	}
	old := []application.Requirement{{Title: "old B", URL: s.requirementUnit.state.requirements[1].URL}}
	s.overlayRefreshedRequirements(old, "project-a:900", 100)
	if old[0].Title != "updated B" || *old[0].WorkItem.MySprintEstimateDays != "5" {
		t.Fatal("late list overwrote single-item refresh")
	}
}

func TestRequirementRefreshPartialFailureRetainsValidValues(t *testing.T) {
	s, b, w := refreshTestFixture(t)
	b.metadataError, b.commentsError = true, true
	url := s.requirementUnit.state.requirements[1].URL
	title := "old metadata"
	s.requirementUnit.state.taskRequirementMetadata = map[string]taskRequirementMetadataState{"task-b": {URL: url, Status: requirementMetadataLoaded, Metadata: requirementMetadata{Title: &title}}}
	r := s.requirementReader("task-b", url)
	s.updateRequirementReaderComments(r, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "old", Content: "old comment"}}})
	s.startRequirementRefresh(url)
	job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(url)]
	close(b.gate)
	finishRefreshTest(t, w, job)
	if s.requirementUnit.state.requirements[1].Title != "B" || *s.requirementUnit.state.taskRequirementMetadata["task-b"].Metadata.Title != title || r.comments.Comments[0].Content != "old comment" || r.body.Body != "new body" || s.lastError == "" || s.lastMessage != "" {
		t.Fatal("partial failure discarded valid data or claimed full success")
	}
}

func TestRequirementRefreshRejectsConfigurationAndAccountChanges(t *testing.T) {
	for _, change := range []string{"config", "account", "home"} {
		t.Run(change, func(t *testing.T) {
			s, b, w := refreshTestFixture(t)
			s.startRequirementRefresh(s.requirementUnit.state.requirements[1].URL)
			job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(s.requirementUnit.state.requirements[1].URL)]
			switch change {
			case "config":
				s.settingsUnit.state.configJSON = `{"meegleProjects":[{"project_key":"different","simple_name":"space"}]}`
			case "account":
				s.settingsUnit.state.settingsCLIStatus = map[string]map[string]any{"meegle": {"userKey": "different"}}
			case "home":
				s.snapshot.Home = "different-home"
			}
			s.observeRequirementRefreshJobs()
			close(b.gate)
			select {
			case <-job.done:
			case <-time.After(time.Second):
				t.Fatal("cancel not joined")
			}
			select {
			case update := <-w.updates:
				update()
			default:
			}
			if s.requirementUnit.state.requirements[1].Title != "B" || s.lastMessage != "" {
				t.Fatal("stale result applied")
			}
		})
	}
}

func TestRequirementRefreshSprintSnapshotsKeepSelectionAndIsolateEstimate(t *testing.T) {
	s, b, w := refreshTestFixture(t)
	list := s.ensureRequirementListJobs()
	list.identity, list.hasIdentity = s.currentRequirementListIdentity(), true
	s.requirementUnit.state.requirementsLoaded, s.requirementUnit.state.requirementsFetchedAt = true, 100
	s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 1, requirementKey(s.requirementUnit.state.requirements[1])
	s.saveRequirementListSnapshot(list.identity, "project-a:900")
	other := append([]application.Requirement(nil), s.requirementUnit.state.requirements...)
	other[1].WorkItem.MySprintEstimateDays = refreshTestString("9")
	list.snapshots[requirementListSnapshotKey{identity: list.identity, sprint: "project-a:901"}] = requirementListViewSnapshot{items: other, selected: requirementKey(other[0]), loaded: true, fetchedAt: 100}
	s.startRequirementRefresh(s.requirementUnit.state.requirements[1].URL)
	job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(s.requirementUnit.state.requirements[1].URL)]
	s.activateRequirementSprint("project-a:901")
	close(b.gate)
	finishRefreshTest(t, w, job)
	if s.requirementUnit.state.requirementsSelectedSprintKey != "project-a:901" || s.requirementUnit.state.selectedRequirement != 0 || *s.requirementUnit.state.requirements[1].WorkItem.MySprintEstimateDays != "9" || s.requirementUnit.state.requirements[1].Title != "updated B" {
		t.Fatal("refresh changed current Sprint or leaked estimate")
	}
	s.activateRequirementSprint("project-a:900")
	if s.requirementUnit.state.selectedRequirement != 1 || s.requirementUnit.state.selectedRequirementKey != requirementKey(s.requirementUnit.state.requirements[1]) || *s.requirementUnit.state.requirements[1].WorkItem.MySprintEstimateDays != "5" || s.requirementUnit.state.requirements[1].Title != "updated B" {
		t.Fatal("returning to Sprint lost selection or refreshed data")
	}
}

func TestRequirementRefreshOlderMetadataCannotReplaceNewerList(t *testing.T) {
	s, b, w := refreshTestFixture(t)
	s.startRequirementRefresh(s.requirementUnit.state.requirements[1].URL)
	job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(s.requirementUnit.state.requirements[1].URL)]
	// A list started later and completed while the single refresh read comments.
	s.requirementUnit.state.requirementsFetchedAt = 300
	s.requirementUnit.state.requirements[1].Title = "newer list title"
	close(b.gate)
	finishRefreshTest(t, w, job)
	if s.requirementUnit.state.requirements[1].Title != "newer list title" {
		t.Fatal("older single-item metadata replaced newer list")
	}
}

func TestRequirementRefreshQueuedCompletionChecksRelevantConfig(t *testing.T) {
	for _, change := range []string{"proxy", "theme"} {
		t.Run(change, func(t *testing.T) {
			s, b, w := refreshTestFixture(t)
			s.startRequirementRefresh(s.requirementUnit.state.requirements[1].URL)
			job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(s.requirementUnit.state.requirements[1].URL)]
			close(b.gate)
			var update func()
			select {
			case update = <-w.updates:
			case <-time.After(time.Second):
				t.Fatal("no queued completion")
			}
			field := `,"commandProxyUrl":"http://127.0.0.1:9000"}`
			if change == "theme" {
				field = `,"theme":"DARK"}`
			}
			s.settingsUnit.state.configJSON = strings.TrimSuffix(s.settingsUnit.state.configJSON, "}") + field
			update()
			select {
			case <-job.done:
			case <-time.After(time.Second):
				t.Fatal("worker not joined")
			}
			if change == "proxy" && s.requirementUnit.state.requirements[1].Title != "B" {
				t.Fatal("queued result ignored relevant config change")
			}
			if change == "theme" && s.requirementUnit.state.requirements[1].Title != "updated B" {
				t.Fatal("unrelated theme change discarded valid refresh")
			}
		})
	}
}
