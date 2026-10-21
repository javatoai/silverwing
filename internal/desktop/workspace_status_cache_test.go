package desktop

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestWorkspaceStatusCacheRestoresTaskWithoutRestoringOperationalHealth(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	a := s.currentTask()
	b := a
	b.id, b.path, b.createdAt = "task-b", "C:/synthetic/task-b", "created-b"
	s.tasks = append(s.tasks, b)
	s.commitWorkspaceGitStatuses(a, statuses)
	s.selected = 1
	s.bindWorkspaceGitObservation(b, s.workspaceGitStatusScope())
	if _, ok := s.workspaceDisplayHealth(b.workspaces[0]); ok {
		t.Fatal("task B borrowed task A's display")
	}
	statuses[b.workspaces[0].Path] = domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, DirtyFileCount: 18, PushState: domain.LocalPushAhead}
	s.commitWorkspaceGitStatuses(b, statuses)
	s.selected = 0
	s.bindWorkspaceGitObservation(a, s.workspaceGitStatusScope())
	if shown, ok := s.workspaceDisplayHealth(a.workspaces[0]); !ok || shown.DirtyFileCount != 3 {
		t.Fatalf("returning to task A lost its last result: %+v / %t", shown, ok)
	}
	if _, ok := s.workspaceHealth(a.workspaces[0].Path); ok {
		t.Fatal("presentation cache was restored as operational Git health")
	}
}

func TestWorkspaceStatusCacheRetainsOnlyDetachedSummaries(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	w := s.currentTask().workspaces[0]
	branch := "feature/original"
	health := statuses[w.Path]
	health.ActualBranch, health.DirtyFiles = &branch, []domain.WorkspaceGitFileChange{{Path: "original.go"}}
	statuses[w.Path] = health
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	branch = "feature/mutated"
	shown, _ := s.workspaceDisplayHealth(w)
	raw, _ := s.workspaceHealth(w.Path)
	if shown.ActualBranch == nil || *shown.ActualBranch != "feature/original" || len(shown.DirtyFiles) != 0 || len(raw.DirtyFiles) != 1 {
		t.Fatal("display retained mutable branch data or a full change list, or stripped operational data")
	}
}

func TestWorkspaceStatusCacheBoundsAndPromotesRecentVisits(t *testing.T) {
	s, _ := workspaceRefreshFixture()
	base := s.currentTask()
	s.tasks = nil
	for i := 0; i < 33; i++ {
		item := base
		item.id, item.path, item.createdAt = fmt.Sprintf("task-%d", i), fmt.Sprintf("C:/synthetic/task-%d", i), fmt.Sprint(i)
		item.workspaces = append([]application.Workspace(nil), base.workspaces[:1]...)
		s.tasks = append(s.tasks, item)
	}
	for i := 0; i < 32; i++ {
		s.selected = i
		s.commitWorkspaceGitStatuses(s.currentTask(), map[string]domain.WorkspaceGitHealth{base.workspaces[0].Path: {
			State: domain.WorkspaceGitReady, DirtyFileCount: i, PushState: domain.LocalPushPushed,
		}})
	}
	s.selected = 0
	s.bindWorkspaceGitObservation(s.currentTask(), s.workspaceGitStatusScope())
	s.selected = 32
	s.bindWorkspaceGitObservation(s.currentTask(), s.workspaceGitStatusScope())
	cache := &s.workspaceGit.displayCache
	if len(cache.entries) != 32 || len(cache.recent) != 32 || cache.entries[workspaceHealthTaskKey(s.tasks[1])] != nil || cache.entries[workspaceHealthTaskKey(s.tasks[0])] == nil {
		t.Fatal("cache is not bounded to the 32 most recently visited tasks")
	}
	s.selected = 1
	s.bindWorkspaceGitObservation(s.currentTask(), s.workspaceGitStatusScope())
	if _, ok := s.workspaceDisplayHealth(s.currentTask().workspaces[0]); ok {
		t.Fatal("evicted task retained a result outside the cache")
	}
}

func TestWorkspaceStatusCacheSnapshotRemovesDeletedAndChangedTasks(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	a := s.currentTask()
	b := a
	b.id, b.path = "task-b", "C:/synthetic/task-b"
	s.tasks = append(s.tasks, b)
	s.commitWorkspaceGitStatuses(a, statuses)
	s.selected = 1
	s.commitWorkspaceGitStatuses(b, statuses)
	s.tasks, s.selected = []task{b}, 0
	s.reconcileWorkspaceGitDisplayCache()
	if s.workspaceGit.displayCache.entries[workspaceHealthTaskKey(a)] != nil || len(s.workspaceGit.displayCache.entries) != 1 {
		t.Fatal("snapshot kept a deleted task or removed an unrelated task")
	}
	s.tasks[0].workspaces = append([]application.Workspace(nil), b.workspaces...)
	s.tasks[0].workspaces[0].Branch = "feature/replaced"
	if !s.reconcileWorkspaceGitDisplayCache() || s.workspaceGit.observation != nil || len(s.workspaceGit.health) != 0 || len(s.workspaceGit.displayCache.entries) != 0 {
		t.Fatal("changed workspace identity retained display or operational state")
	}
}

func TestWorkspaceStatusCacheInvalidationCannotResurrectOldResults(t *testing.T) {
	for _, change := range []string{"account", "home", "root", "git", "created", "group", "workspace", "repository", "remote", "origin"} {
		t.Run(change, func(t *testing.T) {
			s, statuses := workspaceRefreshFixture()
			original := s.currentTask()
			original.workspaces = append([]application.Workspace(nil), original.workspaces...)
			home, config := s.snapshot.Home, s.configJSON
			s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
			switch change {
			case "account":
				s.settingsCLIStatus = map[string]map[string]any{"meegle": {"accountId": "other"}}
			case "home":
				s.snapshot.Home = "C:/synthetic/other-home"
			case "root":
				s.configJSON = `{"taskRoot":"C:/synthetic/other-root"}`
			case "git":
				s.configJSON = `{"gitExecutablePath":"C:/synthetic/other-git.exe"}`
			case "created":
				s.tasks[0].createdAt = "rebuilt"
			case "group":
				s.tasks[0].groupID = "other-group"
			case "workspace":
				s.tasks[0].workspaces[0].ID = "replaced"
			case "repository":
				s.tasks[0].workspaces[0].Record.RepositoryPath = "C:/synthetic/replaced-repository"
			case "remote":
				s.tasks[0].workspaces[0].PushRemote = "other-remote"
			case "origin":
				origin := "https://synthetic.invalid/other.git"
				s.tasks[0].workspaces[0].Record.OriginURL = &origin
			}
			s.reconcileWorkspaceGitDisplayCache()
			s.tasks[0], s.snapshot.Home, s.configJSON, s.settingsCLIStatus = original, home, config, nil
			s.reconcileWorkspaceGitDisplayCache()
			s.bindWorkspaceGitObservation(s.currentTask(), s.workspaceGitStatusScope())
			if _, ok := s.workspaceDisplayHealth(s.currentTask().workspaces[0]); ok {
				t.Fatal("restoring old configuration/identity resurrected invalidated display")
			}
		})
	}
}

func TestWorkspaceStatusCacheRejectsLateResultsAndRetainsFailedRefresh(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	taskSelectionGitFinish(s, first, taskSelectionGitReply{result: taskSelectionGitResult(s, 4)})
	taskSelectionGitClick(t, tester, "task-b")
	second := taskSelectionGitWaitRead(t, backend)
	oldRequest, oldResult := s.workspaceGit.current, taskSelectionGitResult(s, 99)
	taskSelectionGitClick(t, tester, "task-a")
	if shown, ok := s.workspaceDisplayHealth(s.currentTask().workspaces[0]); !ok || shown.DirtyFileCount != 4 {
		t.Fatal("task switch did not restore cached display before the pending read")
	}
	taskSelectionGitFinish(s, second, taskSelectionGitReply{result: oldResult})
	third := taskSelectionGitWaitRead(t, backend)
	s.finishWorkspaceGitStatus(s.workspaceGit, oldRequest, oldResult, nil)
	taskSelectionGitFinish(s, third, taskSelectionGitReply{err: errors.New("synthetic refresh failed")})
	if shown, ok := s.workspaceDisplayHealth(s.currentTask().workspaces[0]); !ok || shown.DirtyFileCount != 4 {
		t.Fatal("late or failed result replaced the last valid display")
	}
	taskSelectionGitClick(t, tester, "task-b")
	fourth := taskSelectionGitWaitRead(t, backend)
	if _, ok := s.workspaceDisplayHealth(s.currentTask().workspaces[0]); ok {
		t.Fatal("discarded task B result was written into its presentation cache")
	}
	taskSelectionGitFinish(s, fourth, taskSelectionGitReply{result: taskSelectionGitResult(s, 7)})
}

func TestWorkspaceStatusCacheRestoresWhileSnapshotReadIsPending(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	taskSelectionGitFinish(s, first, taskSelectionGitReply{result: taskSelectionGitResult(s, 4)})
	s.loading = true
	taskSelectionGitClick(t, tester, "task-b")
	taskSelectionGitClick(t, tester, "task-a")
	if shown, ok := s.workspaceDisplayHealth(s.currentTask().workspaces[0]); !ok || shown.DirtyFileCount != 4 || !s.workspaceGit.afterLoad {
		t.Fatal("snapshot loading blocked immediate display restoration or lost the forced read")
	}
}

func TestWorkspaceStatusCacheColdButtonsDispatchIndependentReads(t *testing.T) {
	for _, item := range []struct {
		label, action string
		failed        bool
	}{{"本地未提交", "workspace.diff-files", false}, {"提交历史", "workspace.history", false},
		{"本地未提交", "workspace.diff-files", true}, {"提交历史", "workspace.history", true}} {
		t.Run(fmt.Sprintf("%s-after-failure-%t", item.label, item.failed), func(t *testing.T) {
			s, backend, _ := taskSelectionGitFixture(t)
			w := &s.tasks[0].workspaces[0]
			w.ServiceID, w.ModuleID = "service", "default"
			s.snapshot.Services = []application.Service{{ID: "service", MasterBranch: "origin/master"}}
			s.requestWorkspaceGitStatus(true)
			statusRead := taskSelectionGitWaitRead(t, backend)
			t.Cleanup(func() { s.cancelWorkspaceHistoryRead(); s.cancelWorkspaceGitStatus() })
			if item.failed {
				taskSelectionGitFinish(s, statusRead, taskSelectionGitReply{err: errors.New("synthetic first inspection failure")})
			}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.workspaceCard(c, setTheme(c, false), s.currentTask(), *w, 0) })
			}, 960, 400)
			if tester.HasText("本地未提交 0") || tester.HasText("本地未提交 —") {
				t.Fatal("first visit displayed a fake count or disabled placeholder")
			}
			if err := tester.Click(item.label); err != nil {
				t.Fatal(err)
			}
			select {
			case read := <-backend.reads:
				if read.action.Kind != item.action || read.action.TaskID != s.currentTask().id || read.action.Path != w.Path || read.action.ServiceID != w.ServiceID || read.action.ModuleID != w.ModuleID {
					t.Fatalf("cold button dispatched the wrong target/action: %+v", read.action)
				}
				result := application.ActionResult{Data: []domain.WorkspaceGitCommit{{FullHash: "fresh-commit", Message: "fresh history"}}}
				if item.action == "workspace.diff-files" {
					result.Data = workspace.CompareFilesResult{SessionID: "cold-session", TargetCommit: "fresh-target"}
				}
				request := s.workspaceHistoryJobs.current
				read.reply <- taskSelectionGitReply{result: result}
				s.finishWorkspaceHistoryJob(request, result, nil)
				state := s.workspaceHistory
				if state.Error != "" || state.HistoryLoading || state.FilesLoading {
					t.Fatalf("independent result did not finish: %+v", state)
				}
				if item.action == "workspace.history" && (len(state.Commits) != 1 || state.Commits[0].FullHash != "fresh-commit") ||
					item.action == "workspace.diff-files" && state.TargetCommit != "fresh-target" {
					t.Fatal("read-only entry did not apply freshly returned data")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("read-only button waited for summary or failed to dispatch")
			}
			if s.busy || !item.failed && (statusRead.ctx.Err() != nil || !s.workspaceGitStatusBusy()) {
				t.Fatal("independent read cancelled summary or changed the global busy state")
			}
		})
	}
}

func TestWorkspaceStatusCacheSwitchRenderingKeepsCachedCards(t *testing.T) {
	for _, width := range []int{360, 960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, statuses := workspaceRefreshFixture()
				a := s.currentTask()
				b := a
				b.id, b.path = "task-b", "C:/synthetic/task-b"
				s.tasks = append(s.tasks, b)
				s.commitWorkspaceGitStatuses(a, statuses)
				tester := workspaceRefreshTester(s, width, dark)
				before := image.NewRGBA(tester.Image().Bounds())
				draw.Draw(before, before.Bounds(), tester.Image(), image.Point{}, draw.Src)
				prefix := fmt.Sprintf("switch-%d-dark-%t", width, dark)
				captureWorkspaceRefresh(t, tester, prefix+"-a-before")
				s.selected = 1
				s.bindWorkspaceGitObservation(b, s.workspaceGitStatusScope())
				s.workspaceGit.loading = true
				tester.Frame()
				for _, text := range []string{"本地未提交", "提交历史", "推送状态未知"} {
					if !tester.HasText(text) {
						t.Fatalf("cold card is missing normal control %s", text)
					}
				}
				captureWorkspaceRefresh(t, tester, prefix+"-b-cold")
				s.selected = 0
				s.bindWorkspaceGitObservation(a, s.workspaceGitStatusScope())
				tester.Frame()
				after := tester.Image()
				for y := 40; y < after.Bounds().Dy(); y++ {
					for x := 0; x < after.Bounds().Dx(); x++ {
						if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
							t.Fatalf("returning to task A changed cached card pixels at %d,%d", x, y)
						}
					}
				}
				captureWorkspaceRefresh(t, tester, prefix+"-a-restored-loading")
			})
		}
	}
}

func TestWorkspaceStatusCacheAccountEventsClearEvenUnchangedStatus(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	s.clearMeegleIdentityBoundViews()
	if len(s.workspaceGit.displayCache.entries) != 0 || s.workspaceGit.observation != nil || len(s.workspaceGit.health) != 0 {
		t.Fatal("account invalidation retained results while its visible status was unchanged")
	}
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	s.applySettingsActionResult(application.Action{Kind: "cli.status", Payload: map[string]any{"tool": "meegle"}},
		application.ActionResult{Data: map[string]any{"accountId": "new-account"}})
	if len(s.workspaceGit.displayCache.entries) != 0 || s.workspaceGit.observation != nil {
		t.Fatal("CLI account status change did not retire the old context")
	}
}

func TestWorkspaceStatusCacheKeepsReadProtectionAndNoPersistentState(t *testing.T) {
	for _, mode := range []string{"busy", "missing", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			s, statuses := workspaceRefreshFixture()
			w := s.currentTask().workspaces[0]
			switch mode {
			case "busy":
				s.busy = true
			case "missing":
				statuses[w.Path] = domain.WorkspaceGitHealth{State: domain.WorkspaceGitMissing, Issue: domain.WorkspaceGitIssueMissing}
			case "mismatch":
				statuses[w.Path] = domain.WorkspaceGitHealth{State: domain.WorkspaceGitFailed, Issue: domain.WorkspaceGitIssueBranchMismatch}
			}
			s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
			tester := workspaceRefreshTester(s, 960, false)
			_ = tester.Click("提交历史")
			if s.workspaceHistory != nil {
				t.Fatal("known unavailable/busy workspace opened a read-only action")
			}
		})
	}
	s, statuses := workspaceRefreshFixture()
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	s.configJSON = `{"theme":"DARK"}`
	if s.reconcileWorkspaceGitDisplayCache() {
		t.Fatal("unrelated theme preference invalidated Git presentation")
	}
	restarted, _ := workspaceRefreshFixture()
	if _, ok := restarted.workspaceDisplayHealth(restarted.currentTask().workspaces[0]); ok {
		t.Fatal("new app instance restored a process-local Git cache")
	}
}
