package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestWorkspaceCardReviewEntryOrderAndZeroLocalChanges(t *testing.T) {
	item := application.Workspace{ID: "workspace", ServiceID: "service", ModuleID: "module", Path: `C:\synthetic\workspace`, Branch: "feature/review"}
	taskItem := task{id: "task", path: `C:\synthetic\task`, createdAt: "created", groupID: "group", workspaces: []application.Workspace{item}}
	s := &app{
		page: "tasks", tasks: []task{taskItem}, configJSON: `{}`,
		snapshot:     application.Snapshot{Home: `C:\synthetic\home`, Services: []application.Service{{ID: "service", GroupID: "group", Name: "project", MasterBranch: "origin/main", Modules: []application.Module{{ID: "module", Name: "project"}}}}},
		workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {State: domain.WorkspaceGitReady, PushState: domain.LocalPushPushed}}},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() { s.workspaceCard(c, setTheme(c, false), taskItem, item, 0) })
	}, 1200, 800)
	tester.Frame()
	lastX := float32(-1)
	for _, text := range []string{"本地未提交 0", "已推送", "整体差异", "提交历史"} {
		rect, found := tester.Find(text)
		if !found || rect.X <= lastX {
			t.Fatalf("card status order incorrect at %q: found=%v rect=%+v previousX=%v", text, found, rect, lastX)
		}
		lastX = rect.X
	}
	for _, text := range tester.Texts() {
		if strings.Contains(text, "个文件变更") {
			t.Fatalf("duplicate diff count remains: %q", text)
		}
	}
	if err := tester.Click("本地未提交 0"); err != nil {
		t.Fatal(err)
	}
	if s.workspaceHistory == nil || s.workspaceHistory.CompareMode != workspaceReviewUncommitted {
		t.Fatal("zero local count did not open local-only review")
	}
	s.backWorkspaceReview(s.workspaceHistory)
	tester.Frame()
	if err := tester.Click("整体差异"); err != nil {
		t.Fatal(err)
	}
	if s.workspaceHistory == nil || s.workspaceHistory.CompareMode != workspaceReviewMainline {
		t.Fatal("overall entry did not open committed mainline review")
	}
	s.dismissWorkspaceHistory()
}

func TestWorkspaceCardUnknownHealthDoesNotDisplayZero(t *testing.T) {
	for _, state := range []domain.WorkspaceGitHealthState{domain.WorkspaceGitChecking, domain.WorkspaceGitFailed} {
		t.Run(string(state), func(t *testing.T) {
			item := application.Workspace{Path: `C:\synthetic\workspace`}
			s := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {State: state}}}}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.workspaceHealthLine(c, setTheme(c, false), task{}, item, "unknown") })
			}, 900, 300)
			tester.Frame()
			if tester.HasText("本地未提交 0") || tester.HasText("0 个文件变更") {
				t.Fatal("unavailable health was represented as zero")
			}
		})
	}
}
