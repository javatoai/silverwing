package desktop

import (
	"fmt"
	"image/color"
	"math"
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
	for _, text := range []string{"本地未提交 0", "提交历史", "已推送"} {
		rect, found := tester.Find(text)
		if !found || rect.X <= lastX {
			t.Fatalf("card status order incorrect at %q: found=%v rect=%+v previousX=%v", text, found, rect, lastX)
		}
		lastX = rect.X
	}
	if tester.HasText("整体差异") {
		t.Fatal("overall diff entry remains on the workspace card")
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
	if err := tester.Click("提交历史"); err != nil {
		t.Fatal(err)
	}
	if s.workspaceHistory == nil || s.workspaceHistory.Mode != "history" || !s.workspaceHistory.HasHistoryFocus {
		t.Fatal("history entry did not open history or preserve its return focus")
	}
	s.dismissWorkspaceHistory()
}

func TestWorkspaceCardNonzeroLocalChangesStillOpensReview(t *testing.T) {
	item := application.Workspace{ID: "workspace", ServiceID: "service", ModuleID: "module", Path: `C:\synthetic\workspace`, Branch: "feature/review"}
	taskItem := task{id: "task", path: `C:\synthetic\task`, createdAt: "created", groupID: "group", workspaces: []application.Workspace{item}}
	s := &app{
		page: "tasks", tasks: []task{taskItem}, configJSON: `{}`,
		snapshot:     application.Snapshot{Home: `C:\synthetic\home`, Services: []application.Service{{ID: "service", GroupID: "group", Name: "project", MasterBranch: "origin/main", Modules: []application.Module{{ID: "module", Name: "project"}}}}},
		workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {State: domain.WorkspaceGitReady, DirtyFileCount: 3, PushState: domain.LocalPushPushed}}},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() { s.workspaceCard(c, setTheme(c, false), taskItem, item, 0) })
	}, 1200, 800)
	tester.Frame()
	if !tester.HasText("本地未提交 3") {
		t.Fatal("nonzero local change count was not shown")
	}
	if err := tester.Click("本地未提交 3"); err != nil {
		t.Fatal(err)
	}
	if s.workspaceHistory == nil || s.workspaceHistory.CompareMode != workspaceReviewUncommitted {
		t.Fatal("nonzero local count did not open local-only review")
	}
	s.dismissWorkspaceHistory()
}

func TestWorkspaceCardPushStatusIsReadOnlyAndOutsideTabOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health domain.WorkspaceGitHealth
		text   string
	}{
		{name: "pushed", health: domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, PushState: domain.LocalPushPushed}, text: "已推送"},
		{name: "ahead", health: domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, PushState: domain.LocalPushAhead, UnpushedCommitCount: 4}, text: "4 个提交未推送"},
		{name: "remote branch missing", health: domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, PushState: domain.LocalPushRemoteBranchMissing}, text: "未发现远程分支"},
		{name: "no upstream", health: domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, PushState: domain.LocalPushNoUpstream}, text: "未关联远程"},
		{name: "push check failed", health: domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, PushState: domain.LocalPushFailed}, text: "检查失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := application.Workspace{Path: `C:\synthetic\workspace`}
			s := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): tc.health}}}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.workspaceHealthLine(c, setTheme(c, false), task{}, item, "readonly") })
			}, 360, 160)
			tester.Frame()
			if !tester.HasText(tc.text) {
				t.Fatalf("push status %q was not shown", tc.text)
			}
			if err := tester.Click(tc.text); err != nil {
				t.Fatal(err)
			}
			if s.workspaceHistory != nil {
				t.Fatal("clicking the remote push status triggered an action")
			}
			if tester.Cursor() == ui.CursorPointer {
				t.Fatal("remote push status displayed an interactive pointer cursor")
			}
		})
	}

	item := application.Workspace{Path: `C:\synthetic\workspace`}
	s := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {
		State: domain.WorkspaceGitReady, PushState: domain.LocalPushPushed,
	}}}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.workspaceHealthLine(c, setTheme(c, false), task{}, item, "readonly-tab") })
	}, 360, 160)
	tester.SetFocused(true)
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("本地未提交 0") {
		t.Fatal("local diff was not the first keyboard stop")
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("提交历史") {
		t.Fatal("history was not the second keyboard stop")
	}
	tester.Key(0, ui.KeyTab)
	if tester.Focused("已推送") {
		t.Fatal("remote push status entered the keyboard focus order")
	}
}

func TestWorkspaceCardActionControlsShareOutlineAndFitWidths(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{360, 640, 960} {
			theme := "light"
			if dark {
				theme = "dark"
			}
			t.Run(fmt.Sprintf("%s-%d", theme, width), func(t *testing.T) {
				item := application.Workspace{Path: `C:\synthetic\workspace`}
				s := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {
					State: domain.WorkspaceGitReady, DirtyFileCount: 3, PushState: domain.LocalPushAhead, UnpushedCommitCount: 4,
				}}}}
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Children(func() { s.workspaceHealthLine(c, setTheme(c, dark), task{}, item, "styled") })
				}, width, 160)
				tester.Frame()

				texts := []string{"本地未提交 3", "提交历史", "4 个提交未推送"}
				rects := make([]ui.Rect, len(texts))
				for index, text := range texts {
					rect, found := tester.Find(text)
					if !found {
						t.Fatalf("missing control %q", text)
					}
					if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > float32(width) || rect.H != 28 {
						t.Fatalf("control %q has invalid bounds at width %d: %+v", text, width, rect)
					}
					rects[index] = rect
				}
				for index := 0; index < len(rects)-1; index++ {
					gap := rects[index+1].X - rects[index].X - rects[index].W
					if gap != 8 {
						t.Fatalf("control gap is %.1f DIPs, want 8", gap)
					}
				}
				line := colors(dark).line
				linePixel := color.RGBA{R: line.R, G: line.G, B: line.B, A: line.A}
				image := tester.Image()
				for index, rect := range rects {
					x := int(rect.X + rect.W/2)
					leftX, rightX := int(math.Round(float64(rect.X))), int(math.Round(float64(rect.X+rect.W)))-1
					topY, bottomY := int(math.Round(float64(rect.Y))), int(math.Round(float64(rect.Y+rect.H)))-1
					midY := int(rect.Y + rect.H/2)
					if image.RGBAAt(x, topY) != linePixel || image.RGBAAt(x, bottomY) != linePixel ||
						image.RGBAAt(leftX, midY) != linePixel || image.RGBAAt(rightX, midY) != linePixel {
						t.Fatalf("control %q border at %+v: top=%v bottom=%v left=%v right=%v want=%v", texts[index], rect, image.RGBAAt(x, topY), image.RGBAAt(x, bottomY), image.RGBAAt(leftX, midY), image.RGBAAt(rightX, midY), linePixel)
					}
				}
			})
		}
	}

	item := application.Workspace{Path: `C:\synthetic\workspace`}
	s := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {
		State: domain.WorkspaceGitReady, DirtyFileCount: 123456789, PushState: domain.LocalPushAhead, UnpushedCommitCount: 123456789,
	}}}}
	width := 180
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.workspaceHealthLine(c, setTheme(c, false), task{}, item, "wrapped") })
	}, width, 200)
	tester.Frame()
	texts := []string{"本地未提交 123456789", "提交历史", "123456789 个提交未推送"}
	var rects []ui.Rect
	for _, text := range texts {
		rect, found := tester.Find(text)
		if !found {
			t.Fatalf("narrow layout lost control %q", text)
		}
		if rect.X < 0 || rect.X+rect.W > float32(width) {
			t.Fatalf("narrow layout overflowed at control %q: %+v", text, rect)
		}
		rects = append(rects, rect)
	}
	if rects[2].Y <= rects[0].Y {
		t.Fatalf("long status did not wrap after the history button: local=%+v history=%+v status=%+v", rects[0], rects[1], rects[2])
	}
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

func TestWorkspaceCardFeedbackIsSharedAndStatusStaysNeutral(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%t", dark), func(t *testing.T) {
			item := application.Workspace{Path: `C:\synthetic\feedback`}
			state := &app{workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {
				State: domain.WorkspaceGitReady, PushState: domain.LocalPushPushed,
			}}}}
			tester := ui.NewTester(func(context *ui.Context) {
				ui.Column(context).Fill().Children(func() { state.workspaceHealthLine(context, setTheme(context, dark), task{}, item, "feedback") })
			}, 360, 160)
			palette := colors(dark)
			assertFill := func(text string, expected ui.Color) {
				t.Helper()
				rect, found := tester.Find(text)
				if !found {
					t.Fatalf("missing control %q", text)
				}
				actual := tester.Image().RGBAAt(int(math.Round(float64(rect.X)))+4, int(rect.Y+rect.H/2))
				if actual != (color.RGBA{R: expected.R, G: expected.G, B: expected.B, A: expected.A}) {
					t.Fatalf("%q fill=%v, want %v", text, actual, expected)
				}
			}
			for _, text := range []string{"本地未提交 0", "提交历史"} {
				rect, _ := tester.Find(text)
				tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
				assertFill(text, palette.soft)
				tester.Press(rect.X+rect.W/2, rect.Y+rect.H/2)
				assertFill(text, palette.selected)
				tester.Release(359, 159)
			}
			rect, _ := tester.Find("已推送")
			tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
			assertFill("已推送", palette.surface)
			tester.Press(rect.X+rect.W/2, rect.Y+rect.H/2)
			assertFill("已推送", palette.surface)
			tester.Release(rect.X+rect.W/2, rect.Y+rect.H/2)
			state.busy = true
			tester.Frame()
			for _, text := range []string{"本地未提交 0", "提交历史"} {
				if err := tester.Click(text); err != nil {
					t.Fatal(err)
				}
				if state.workspaceHistory != nil {
					t.Fatal("busy workspace control opened a review")
				}
			}
			assertFill("已推送", palette.surface)
		})
	}
}
