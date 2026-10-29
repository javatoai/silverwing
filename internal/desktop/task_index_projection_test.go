package desktop

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func taskListPerformanceFixture(n int) *app {
	s := newApp(nil, Options{CapturePath: "isolated-task-list"})
	s.loading, s.page = false, "tasks"
	s.snapshot.Groups = []application.Group{{ID: "g", Name: "Group"}}
	s.taskUnit.publishTaskGroups(s.snapshot.Groups)
	items := make([]task, n)
	for i := range items {
		items[i] = task{id: fmt.Sprintf("task-%05d", i), title: fmt.Sprintf("Task row %05d", i), groupID: "g", createdAt: "created", status: "进行中"}
	}
	s.taskUnit.replaceTasks(items, 0)
	return s
}

func TestTaskVirtualListFixedViewportAndWarmProjectionWork(t *testing.T) {
	for _, n := range []int{10, 1000, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := taskListPerformanceFixture(n)
			tester := taskNavigationTester(s, 260, 400)
			state := s.taskIndexBrowsingFor(false)
			if built := len(state.rowHandles); built == 0 || built > 40 {
				t.Fatalf("viewport allocated %d row handles for %d tasks", built, n)
			}
			if s.taskUnit.state.taskSearch.valid {
				t.Fatal("empty search built full lowercase task text")
			}
			rows, catalog := state.projection.rows, s.taskUnit.taskCatalog()
			for i := 0; i < 4; i++ {
				tester.Frame()
			}
			if s.taskUnit.taskCatalog() != catalog || &state.projection.rows[0] != &rows[0] || len(state.rowHandles) > 40 {
				t.Fatal("unchanged frames rebuilt identities/projection or allocated offscreen handles")
			}
		})
	}
}

func TestTaskVirtualListKeyboardRevealFilterAndContextPin(t *testing.T) {
	s := taskListPerformanceFixture(1000)
	tester := taskNavigationTester(s, 260, 400)
	if err := tester.Click("搜索任务"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeyEnd)
	target := s.taskUnit.state.tasks[999]
	if !tester.Focused(target.title) {
		t.Fatal("End did not reveal/focus an offscreen row")
	}
	navigationChooseTaskMenu(t, tester, target.title, "置顶任务")
	if !s.taskIsPinned(target) || s.taskIndexRows(false)[0] != 999 {
		t.Fatal("virtual row context menu targeted another task")
	}
	state := s.taskIndexBrowsingFor(false)
	state.search = "task-00999"
	tester.Frame()
	if !slices.Equal(state.visibleRows, []int{999}) || !tester.HasText(target.title) {
		t.Fatal("filter lost the offscreen selected identity")
	}
	s.revealTaskInIndex(s.taskUnit.state.tasks[700])
	tester.Frame()
	if state.revealTaskKey != "" || !tester.HasText(s.taskUnit.state.tasks[700].title) || state.search != "" {
		t.Fatal("explicit reveal did not escape the filter and consume once")
	}
	position := state.scroll.Y
	tester.Frame()
	if state.scroll.Y != position {
		t.Fatal("consumed reveal moved a later frame")
	}
}

func TestTaskProjectionRevisionsMetadataPointerAndSameSlicePublication(t *testing.T) {
	s := taskListPerformanceFixture(2)
	items := s.taskUnit.state.tasks
	items[0].requirementURL = "https://project.feishu.cn/p/story/detail/1"
	s.taskUnit.replaceTasks(items, 0)
	state := s.taskIndexBrowsingFor(false)
	state.search = "first metadata"
	if got := s.taskIndexRows(false); len(got) != 0 {
		t.Fatal("unexpected pre-metadata match")
	}
	title := "First metadata"
	value := taskRequirementMetadataState{URL: items[0].requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{ID: "meta", Title: &title}}
	s.setTaskRequirementMetadata(items[0].id, value)
	if got := s.taskIndexRows(false); !slices.Equal(got, []int{0}) {
		t.Fatal("async metadata result did not invalidate search")
	}
	revision := s.taskUnit.state.metadataRevision
	s.setTaskRequirementMetadata(items[0].id, value)
	if s.taskUnit.state.metadataRevision != revision {
		t.Fatal("equal metadata invalidated the projection")
	}
	title = "Changed metadata"
	s.setTaskRequirementMetadata(items[0].id, value) // same pointer, now a changed value
	if got := s.taskIndexRows(false); len(got) != 0 || s.taskUnit.state.metadataRevision == revision {
		t.Fatal("borrowed Title pointer hid a changed metadata value")
	}
	state.search = "updated title"
	items[1].title = "Updated title"
	s.taskUnit.replaceTasks(items, 0)
	if got := s.taskIndexRows(false); !slices.Equal(got, []int{1}) {
		t.Fatal("same-slice task publication did not invalidate search")
	}
	state.search = "renamed group"
	s.snapshot.Groups[0].Name = "Renamed group"
	s.taskUnit.publishTaskGroups(s.snapshot.Groups)
	if got := s.taskIndexRows(false); len(got) != 2 {
		t.Fatal("group metadata revision did not invalidate task text")
	}
	s.deleteTaskRequirementMetadata(items[0].id)
	state.search = "changed metadata"
	if got := s.taskIndexRows(false); len(got) != 0 {
		t.Fatal("metadata deletion retained a stale search value")
	}
}

func TestTaskVirtualListRecreatedIDDoesNotInheritPinOrFocus(t *testing.T) {
	s := taskListPerformanceFixture(20)
	s.setTaskPinned(s.taskUnit.state.tasks[0], true)
	tester := taskNavigationTester(s, 260, 400)
	if err := tester.Click("搜索任务"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyDown)
	oldKey := s.taskUnit.taskCatalog().keys[0]
	oldDetail := s.taskWorkspaceDetailScroll(s.taskUnit.state.tasks[0])
	oldDetail.Y = 123
	items := s.taskUnit.state.tasks
	items[0].createdAt = "recreated"
	s.taskUnit.replaceTasks(items, 0)
	tester.Frame()
	state := s.taskIndexBrowsingFor(false)
	if s.taskIsPinned(items[0]) || state.rowHandles[oldKey] != nil || tester.Focused(items[0].title) {
		t.Fatal("same ID with new creation identity inherited pin/focus")
	}
	if detail := s.taskWorkspaceDetailScroll(items[0]); detail == oldDetail || detail.Y != 0 {
		t.Fatal("recreated task inherited the former task's detail position")
	}
}

func TestTaskMetadataDiscoveryGateTracksLoadingFailureAndIdentityClear(t *testing.T) {
	s := taskListPerformanceFixture(2)
	items := s.taskUnit.state.tasks
	items[0].requirementURL = "https://project.feishu.cn/p/userstory/detail/1"
	s.taskUnit.replaceTasks(items, 0)
	s.ensureTaskRequirementMetadata()
	key := s.taskUnit.state.metadataDiscovery
	s.ensureTaskRequirementMetadata()
	if !s.taskUnit.state.metadataDiscoveryValid || s.taskUnit.state.metadataDiscovery != key {
		t.Fatal("unchanged metadata discovery did not settle")
	}
	item := s.taskUnit.state.tasks[0]
	s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: item.requirementURL, Status: requirementMetadataLoading})
	s.ensureTaskRequirementMetadata()
	loading := s.taskUnit.state.metadataDiscovery
	s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: item.requirementURL, Status: requirementMetadataFailed})
	s.ensureTaskRequirementMetadata()
	if s.taskUnit.state.metadataDiscovery.metadata == loading.metadata {
		t.Fatal("loading/failure publication did not trigger discovery")
	}
	s.invalidateTaskRequirementIdentityMetadata()
	if s.taskUnit.state.metadataDiscoveryValid {
		t.Fatal("account identity clear retained discovery gate")
	}
}

func TestTaskMetadataDiscoveryRetriesOnlyDeferredRefreshIdentities(t *testing.T) {
	s := taskListPerformanceFixture(1000)
	items := s.taskUnit.state.tasks
	items[0].requirementURL = "https://project.feishu.cn/p/userstory/detail/1"
	s.taskUnit.replaceTasks(items, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	url := items[0].requirementURL
	s.requirementUnit.state.requirementRefreshJobs = &requirementRefreshState{current: map[string]*requirementRefreshJob{requirementRefreshKey(url): {ctx: ctx, identity: s.currentRequirementListIdentity()}}}
	s.ensureTaskRequirementMetadata()
	if !s.taskUnit.state.metadataDiscoveryValid || len(s.taskUnit.state.metadataDiscoveryRetry) != 1 {
		t.Fatal("active force-refresh did not retain one deferred identity")
	}
	s.ensureTaskRequirementMetadata()
	if len(s.taskUnit.state.metadataDiscoveryRetry) != 1 {
		t.Fatal("unchanged active refresh lost its deferred retry")
	}
	cancel()
	s.ensureTaskRequirementMetadata()
	if len(s.taskUnit.state.metadataDiscoveryRetry) != 0 {
		t.Fatal("finished/cancelled force-refresh blocked metadata discovery retry")
	}
}

var taskIndexBenchmark []int

func BenchmarkTaskIndexProjection(b *testing.B) {
	for _, n := range []int{10, 1000, 5000} {
		s := taskListPerformanceFixture(n)
		s.taskIndexRows(false)
		b.Run(fmt.Sprintf("empty-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				taskIndexBenchmark = s.taskIndexRows(false)
			}
		})
		s.taskIndexBrowsingFor(false).search = "__missing__"
		s.taskIndexRows(false)
		b.Run(fmt.Sprintf("search-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				taskIndexBenchmark = s.taskIndexRows(false)
			}
		})
	}
}
