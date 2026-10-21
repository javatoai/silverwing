package desktop

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestWorkspaceRefreshRetainsStatusControlsWhileLoading(t *testing.T) {
	item := application.Workspace{ID: "stable", Path: "C:/synthetic/stable", Branch: "feature/stable"}
	branch := "feature/stable"
	s := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {
		State: domain.WorkspaceGitReady, ActualBranch: &branch, DirtyFileCount: 3, PushState: domain.LocalPushPushed,
	}}}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.workspaceCard(c, setTheme(c, false), task{}, item, 0) })
	}, 1100, 400)
	before, ok := tester.Find("本地未提交 3")
	if !ok {
		t.Fatal("missing initial status")
	}
	s.workspaceGit.loading = true
	tester.Frame()
	after, ok := tester.Find("本地未提交 3")
	if !ok || before != after || tester.HasText("检查中") {
		t.Fatalf("refresh replaced/reflowed status controls: before=%+v after=%+v present=%t", before, after, ok)
	}
}

func workspaceRefreshFixture() (*app, map[string]domain.WorkspaceGitHealth) {
	s := newApp(nil, Options{CapturePath: "isolated-workspace-refresh"})
	s.loading, s.page, s.configJSON = false, "tasks", `{}`
	s.snapshot.Home = "C:/synthetic/refresh-home"
	t := task{id: "refresh-task", title: "示例任务", path: "C:/synthetic/refresh-task", createdAt: "created", groupID: "group"}
	statuses := make(map[string]domain.WorkspaceGitHealth)
	for i := 0; i < 6; i++ {
		name, branch := fmt.Sprintf("project-%d", i), fmt.Sprintf("feature/project-%d", i)
		w := application.Workspace{ID: name, ServiceID: name, ModuleID: "default", Name: name, Path: "C:/synthetic/" + name, Branch: branch, State: "READY",
			Record: domain.ServiceWorkspace{ServiceName: name, ModuleName: "default"}}
		t.workspaces = append(t.workspaces, w)
		statuses[w.Path] = domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, ActualBranch: &branch, DirtyFileCount: i + 3, PushState: domain.LocalPushPushed}
	}
	s.tasks = []task{t}
	return s, statuses
}

func workspaceRefreshTester(s *app, width int, dark bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Background(p.bg).Gap(10).Children(func() {
			ui.Row(c).FillWidth().Height(34).Children(func() { workspaceGitRefreshButton(c, p, s.workspaceGitStatusBusy()) })
			ui.Scroll(c.Key("refresh-cards")).Grow(1).MinHeight(0).TrackScroll(s.taskWorkspaceDetailScroll(s.currentTask())).Children(func() {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					t := s.currentTask()
					for i, w := range t.workspaces {
						ui.Column(c).FillWidth().Label("卡片 " + w.ID).Children(func() { s.workspaceCard(c, p, t, w, i) })
					}
				})
			})
		})
	}, width, 500)
}

func captureWorkspaceRefresh(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	if directory := os.Getenv("SILVERWING_WORKSPACE_REFRESH_EVIDENCE_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr, closeErr := png.Encode(file, tester.Image()), file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatalf("capture: %v %v", encodeErr, closeErr)
		}
	}
}

func TestWorkspaceRefreshLayoutAndPixelsStayStable(t *testing.T) {
	for _, width := range []int{360, 960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, statuses := workspaceRefreshFixture()
				s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
				tester := workspaceRefreshTester(s, width, dark)
				before := image.NewRGBA(tester.Image().Bounds())
				draw.Draw(before, before.Bounds(), tester.Image(), image.Point{}, draw.Src)
				rects := map[string]ui.Rect{}
				for _, text := range []string{"本地未提交 3", "提交历史", "已推送", "卡片 project-1"} {
					rect, ok := tester.Find(text)
					if !ok {
						t.Fatalf("missing %s", text)
					}
					rects[text] = rect
				}
				prefix := fmt.Sprintf("refresh-%d-dark-%t", width, dark)
				captureWorkspaceRefresh(t, tester, prefix+"-before")
				s.workspaceGit.loading = true
				tester.Frame()
				for text, rect := range rects {
					if got, ok := tester.Find(text); !ok || got != rect {
						t.Fatalf("loading moved %s: %v -> %v", text, rect, got)
					}
				}
				if tester.HasText("检查中") || !tester.HasText("刷新中") {
					t.Fatal("loading presentation is not limited to toolbar")
				}
				after := tester.Image()
				for y := 40; y < after.Bounds().Dy(); y++ {
					for x := 0; x < after.Bounds().Dx(); x++ {
						if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
							t.Fatalf("card pixels changed while refreshing at %d,%d", x, y)
						}
					}
				}
				captureWorkspaceRefresh(t, tester, prefix+"-loading")
				s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
				s.workspaceGit.loading = false
				tester.Frame()
				for text, rect := range rects {
					if got, ok := tester.Find(text); !ok || got != rect {
						t.Fatalf("same-result completion moved %s", text)
					}
				}
				captureWorkspaceRefresh(t, tester, prefix+"-after")
			})
		}
	}
}

func TestWorkspaceRefreshFailurePreservesDisplayButUpdatesRawHealth(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	w := s.currentTask().workspaces[0]
	before := workspaceRefreshTester(s, 960, false)
	rect, _ := before.Find("本地未提交 3")
	s.failWorkspaceGitStatus(s.currentTask(), errors.New("synthetic read failure"))
	shown, ok := s.workspaceDisplayHealth(w)
	raw, rawOK := s.workspaceHealth(w.Path)
	if !ok || shown.DirtyFileCount != 3 || !rawOK || raw.State != domain.WorkspaceGitFailed || !strings.Contains(s.workspaceGit.failure, "当前显示上次结果") {
		t.Fatalf("failed read corrupted display or hid operational failure: shown=%+v raw=%+v", shown, raw)
	}
	before.Frame()
	captureWorkspaceRefresh(t, before, "refresh-960-dark-false-warm-failure")
	if got, ok := before.Find("本地未提交 3"); !ok || got != rect {
		t.Fatal("failure reflowed old result")
	}
	if s.workspaceNeedsRepair(s.currentTask(), w) {
		t.Fatal("transient refresh failure manufactured a repair action")
	}
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	if s.workspaceGit.failure != "" {
		t.Fatal("successful read retained failure notice")
	}
}

func TestWorkspaceRefreshColdReadAndFailureKeepUnknownSlots(t *testing.T) {
	s, _ := workspaceRefreshFixture()
	s.bindWorkspaceGitObservation(s.currentTask(), s.workspaceGitStatusScope())
	s.workspaceGit.loading = true
	tester := workspaceRefreshTester(s, 360, true)
	for _, text := range []string{"本地未提交", "提交历史", "推送状态未知"} {
		if !tester.HasText(text) {
			t.Fatalf("missing unknown slot %s", text)
		}
	}
	if tester.HasText("本地未提交 0") || tester.HasText("检查中") {
		t.Fatal("unknown was shown as checking or zero")
	}
	captureWorkspaceRefresh(t, tester, "refresh-360-dark-true-cold-loading")
	rect, _ := tester.Find("本地未提交")
	s.failWorkspaceGitStatus(s.currentTask(), errors.New("cold failure"))
	s.workspaceGit.loading = false
	tester.Frame()
	if got, ok := tester.Find("本地未提交"); !ok || got != rect {
		t.Fatal("cold failure replaced placeholder layout")
	}
	captureWorkspaceRefresh(t, tester, "refresh-360-dark-true-cold-failure")
}

func TestWorkspaceRefreshPartialFailureUpdatesTogether(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	w0, w1 := s.currentTask().workspaces[0], s.currentTask().workspaces[1]
	statuses[w0.Path] = domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, DirtyFileCount: 18, PushState: domain.LocalPushPushed}
	delete(statuses, w1.Path)
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	good, _ := s.workspaceDisplayHealth(w0)
	retained, _ := s.workspaceDisplayHealth(w1)
	raw, _ := s.workspaceHealth(w1.Path)
	if good.DirtyFileCount != 18 || retained.DirtyFileCount != 4 || raw.Issue != domain.WorkspaceGitIssueInspectionFailed {
		t.Fatalf("partial result was not atomically projected: %+v %+v %+v", good, retained, raw)
	}
}

func TestWorkspaceRefreshDisplayRejectsIdentityAndContextChanges(t *testing.T) {
	for _, change := range []string{"task", "created", "path", "branch", "module", "home", "account", "root", "git"} {
		t.Run(change, func(t *testing.T) {
			s, statuses := workspaceRefreshFixture()
			s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
			switch change {
			case "task":
				s.tasks[0].id = "replacement"
			case "created":
				s.tasks[0].createdAt = "recreated"
			case "path":
				s.tasks[0].workspaces[0].Path = "C:/synthetic/replaced"
			case "branch":
				s.tasks[0].workspaces[0].Branch = "feature/replaced"
			case "module":
				s.tasks[0].workspaces[0].ModuleID = "replaced"
			case "home":
				s.snapshot.Home = "C:/synthetic/other-home"
			case "account":
				s.settingsCLIStatus = map[string]map[string]any{"meegle": {"accountId": "other"}}
			case "root":
				s.configJSON = `{"taskRoot":"C:/other-root"}`
			case "git":
				s.configJSON = `{"gitExecutablePath":"C:/other-git.exe"}`
			}
			w := s.currentTask().workspaces[0]
			if _, ok := s.workspaceDisplayHealth(w); ok {
				t.Fatal("foreign cached display accepted")
			}
			if _, ok := s.workspaceHealth(w.Path); ok {
				t.Fatal("foreign raw observation accepted")
			}
		})
	}
}

func TestWorkspaceRefreshRetainsFocusScrollAndDraftOnResultChange(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	s.notes[0] = "unsaved note"
	s.documentContent = "unsaved document"
	s.documentDirty = true
	tester := workspaceRefreshTester(s, 960, false)
	for i := 0; i < 20 && !tester.Focused("本地未提交 3"); i++ {
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused("本地未提交 3") {
		t.Fatal("could not focus stable count control")
	}
	scroll := s.taskWorkspaceDetailScroll(s.currentTask())
	scroll.Y = 30
	tester.Frame()
	oldY := scroll.Y
	s.workspaceGit.loading = true
	tester.Frame()
	if !tester.Focused("本地未提交 3") || scroll.Y != oldY {
		t.Fatal("loading lost focus or reading position")
	}
	w := s.currentTask().workspaces[0]
	health := statuses[w.Path]
	branch := "feature/new-actual"
	health.DirtyFileCount, health.ActualBranch = 18, &branch
	statuses[w.Path] = health
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	s.workspaceGit.loading = false
	tester.Frame()
	if !tester.Focused("本地未提交 18") || scroll.Y != oldY || s.notes[0] != "unsaved note" || s.documentContent != "unsaved document" || !s.documentDirty {
		t.Fatal("result update lost focus, scroll or draft")
	}
	if !tester.HasText(branch) {
		t.Fatal("newly verified branch was not displayed")
	}
}

func TestWorkspaceRefreshQueuedReadsKeepLastCompletedDisplay(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	s.commitWorkspaceGitStatuses(s.currentTask(), taskSelectionGitResult(s, 3).Data.(map[string]domain.WorkspaceGitHealth))
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	for i := 0; i < 10; i++ {
		taskSelectionGitClick(t, tester, "task-a")
	}
	taskSelectionGitFinish(s, first, taskSelectionGitReply{result: taskSelectionGitResult(s, 99)})
	second := taskSelectionGitWaitRead(t, backend)
	w := s.currentTask().workspaces[0]
	if shown, ok := s.workspaceDisplayHealth(w); !ok || shown.DirtyFileCount != 3 || !s.workspaceGitStatusBusy() {
		t.Fatal("superseded result flashed or old display disappeared")
	}
	taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 4)})
	if shown, ok := s.workspaceDisplayHealth(w); !ok || shown.DirtyFileCount != 4 || s.workspaceGitStatusBusy() {
		t.Fatal("latest queued refresh did not publish")
	}
}

func TestWorkspaceRefreshFailureNotificationIsScopedAndClears(t *testing.T) {
	s, statuses := workspaceRefreshFixture()
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	s.failWorkspaceGitStatus(s.currentTask(), errors.New("synthetic toast failure"))
	tester := ui.NewTester(func(c *ui.Context) { setTheme(c, false); s.renderOperationNotifications(c) }, 640, 400)
	if !strings.Contains(s.operationNotifications.shown["workspace-git-refresh"], "当前显示上次结果") {
		t.Fatal("refresh failure did not use existing notification channel")
	}
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
	tester.Frame()
	if s.operationNotifications.shown["workspace-git-refresh"] != "" {
		t.Fatal("successful refresh retained failure notification")
	}
	s.failWorkspaceGitStatus(s.currentTask(), errors.New("another failure"))
	tester.Frame()
	s.settingsCLIStatus = map[string]map[string]any{"meegle": {"accountId": "different"}}
	tester.Frame()
	if s.operationNotifications.shown["workspace-git-refresh"] != "" {
		t.Fatal("previous account's failure leaked into new account")
	}
}
