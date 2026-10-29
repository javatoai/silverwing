package desktop

import (
	"fmt"
	"slices"
	"testing"

	"silverwing-go/internal/application"
)

func TestTaskCatalogRecoversUnpublishedSliceStructureChanges(t *testing.T) {
	for _, change := range []string{"splice", "truncate", "rebase", "grow", "replace", "empty"} {
		t.Run(change, func(t *testing.T) {
			s := taskListPerformanceFixture(5)
			items := s.taskUnit.state.tasks
			items[4].archived = true
			s.taskUnit.replaceTasks(items, 0)
			active, archived := s.taskIndexBrowsingFor(false), s.taskIndexBrowsingFor(true)
			s.taskIndexRows(false)
			s.taskIndexRows(true)
			active.search = "task"
			s.taskIndexRows(false) // Also warm the search projection.
			before, revision := s.taskUnit.taskCatalog(), s.taskUnit.state.tasksRevision
			switch change {
			case "splice":
				s.taskUnit.state.tasks = append(items[:1], items[2:]...)
			case "truncate":
				s.taskUnit.state.tasks = items[:3]
			case "rebase":
				s.taskUnit.state.tasks = items[1:]
			case "grow":
				s.taskUnit.state.tasks = append(items, task{id: "added", title: "Task added", createdAt: "new"})
			case "replace":
				s.taskUnit.state.tasks = append([]task(nil), items...)
				s.taskUnit.state.tasks[0].id = "replacement"
			case "empty":
				s.taskUnit.state.tasks = nil
			}
			// These legacy callers bypass publication. Structural checks must run
			// before returning rows, even when the revision key itself is unchanged.
			for _, category := range []bool{false, true} {
				rows := s.taskIndexRows(category)
				var want []int
				for i, item := range s.taskUnit.state.tasks {
					if item.archived == category {
						want = append(want, i)
					}
				}
				if !slices.Equal(rows, want) {
					t.Fatalf("category %v retained stale row indices: %v, want %v", category, rows, want)
				}
			}
			if s.taskUnit.taskCatalog() == before || s.taskUnit.state.tasksRevision != revision+1 {
				t.Fatal("one structure change did not cause exactly one invalidation")
			}
			if !active.projection.valid || !archived.projection.valid || len(s.taskUnit.state.taskSearch.text) != len(s.taskUnit.state.tasks) {
				t.Fatal("structural refresh did not rebuild both categories and search")
			}
			for _, item := range s.taskUnit.state.tasks {
				if got := s.taskUnit.taskByID(item.id); got.id != item.id || got.createdAt != item.createdAt {
					t.Fatalf("lookup returned another task: %+v, want %+v", got, item)
				}
			}
		})
	}
}

func TestTaskLookupRebuildsInvalidCandidateAfterSameLengthReorder(t *testing.T) {
	for _, change := range []string{"reorder", "out-of-range"} {
		t.Run(change, func(t *testing.T) {
			s := taskListPerformanceFixture(5)
			s.taskIndexRows(false)
			catalog, revision := s.taskUnit.taskCatalog(), s.taskUnit.state.tasksRevision
			target := s.taskUnit.state.tasks[1]
			if change == "reorder" {
				s.taskUnit.state.tasks[1], s.taskUnit.state.tasks[3] = s.taskUnit.state.tasks[3], s.taskUnit.state.tasks[1]
			} else {
				catalog.byID[target.id] = len(s.taskUnit.state.tasks)
			}
			if got := s.taskUnit.taskByID(target.id); got.id != target.id || got.title != target.title {
				t.Fatalf("invalid cached candidate returned another identity: %+v", got)
			}
			if s.taskUnit.taskCatalog() == catalog || s.taskUnit.state.tasksRevision != revision+1 {
				t.Fatal("invalid candidate did not invalidate the dependent projection")
			}
			s.taskIndexRows(false)
			fresh := s.taskUnit.taskCatalog()
			for position, index := range s.taskIndexBrowsingFor(false).projection.rows {
				if s.taskIndexBrowsingFor(false).projection.keyPositions[taskNavigationKey(s.taskUnit.state.tasks[index])] != position {
					t.Fatal("recovered lookup retained old row identities")
				}
			}
			if s.taskUnit.taskByID("missing").id != "" || s.taskUnit.taskCatalog() != fresh {
				t.Fatal("an ordinary missing lookup rebuilt a valid catalog")
			}
		})
	}
}

func TestTaskStructureRefreshRejectsOldMenuAndReconcilesVisibleHandles(t *testing.T) {
	s := taskListPerformanceFixture(10)
	tester := taskNavigationTester(s, 260, 400)
	state := s.taskIndexBrowsingFor(false)
	target := s.taskUnit.state.tasks[1]
	scope, valid := s.taskMenuScopeFor(target)
	if !valid {
		t.Fatal("could not capture target scope")
	}
	key := taskNavigationKey(target)
	state.pendingFocusKey, state.revealTaskKey = key, key
	s.taskUnit.state.tasks = append(s.taskUnit.state.tasks[:1], s.taskUnit.state.tasks[2:]...)
	if s.taskMenuScopeCurrent(scope) || s.taskUnit.taskByID(target.id).id != "" {
		t.Fatal("removed task's menu followed the task shifted into its former index")
	}
	if !s.taskLifecycleResultBelongsToContext(application.Action{Kind: "task.delete", TaskID: target.id, Path: target.path}, scope, taskMenuSnapshotForTest(s)) {
		t.Fatal("fresh delete reconciliation rejected its own removed target")
	}
	tester.Frame()
	if len(state.visibleRows) != 9 || state.rowHandles[key] != nil || state.pendingFocusKey != "" || state.revealTaskKey != "" || state.handlesRevision != s.taskUnit.state.tasksRevision {
		t.Fatal("structure refresh retained deleted rows, handles or focus/reveal state")
	}
	replacement := target
	replacement.createdAt += "-replacement"
	s.taskUnit.replaceTasks(append(s.taskUnit.state.tasks, replacement), 0)
	if s.taskMenuScopeCurrent(scope) {
		t.Fatal("old menu accepted a freshly reconciled replacement task")
	}
}

func TestTaskCatalogWarmStructureChecksDoNotAllocateOrRebuild(t *testing.T) {
	for _, n := range []int{10, 1000, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := taskListPerformanceFixture(n)
			rows := s.taskIndexRows(false)
			catalog, revision := s.taskUnit.taskCatalog(), s.taskUnit.state.tasksRevision
			id := s.taskUnit.state.tasks[n-1].id
			allocations := testing.AllocsPerRun(100, func() {
				_ = s.taskUnit.taskByID(id)
				_ = s.taskIndexRows(false)
			})
			if allocations != 0 || s.taskUnit.taskCatalog() != catalog || s.taskUnit.state.tasksRevision != revision || &rows[0] != &s.taskIndexRows(false)[0] {
				t.Fatalf("warm checks allocated %v times or rebuilt unchanged state", allocations)
			}
		})
	}
}

var taskLookupBenchmark task

func BenchmarkTaskCatalogWarmLookup(b *testing.B) {
	for _, n := range []int{10, 1000, 5000} {
		s := taskListPerformanceFixture(n)
		id := s.taskUnit.state.tasks[n-1].id
		s.taskUnit.taskByID(id)
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				taskLookupBenchmark = s.taskUnit.taskByID(id)
			}
		})
	}
}
