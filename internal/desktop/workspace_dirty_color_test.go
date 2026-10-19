package desktop

import (
	"fmt"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestWorkspaceDirtySummaryColorsByCount(test *testing.T) {
	for _, dark := range []bool{false, true} {
		themeName := "light"
		if dark {
			themeName = "dark"
		}
		test.Run(themeName, func(test *testing.T) {
			state := workspaceDirtyColorApp([]int{0, 1, 12})
			tester := workspaceDirtyColorTester(state, dark, 640, []int{0, 1, 12})
			palette := colors(dark)
			for _, count := range []int{0, 1, 12} {
				text := fmt.Sprintf("本地未提交 %d", count)
				background, foreground := palette.surface, palette.text
				if count > 0 {
					background, foreground = palette.amberBg, palette.amber
				}
				assertWorkspaceControlColors(test, tester, text, background, foreground)
			}
			assertWorkspaceControlColors(test, tester, "提交历史", palette.surface, palette.text)
			assertWorkspaceControlColors(test, tester, "已推送", palette.surface, palette.text)
			saveFrameworkReadingFixture(test, tester, "workspace-dirty-colors-"+themeName)
		})
	}
}

func TestWorkspaceDirtySummaryInteractionColors(test *testing.T) {
	for _, dark := range []bool{false, true} {
		test.Run(fmt.Sprintf("dark-%t", dark), func(test *testing.T) {
			state := workspaceDirtyColorApp([]int{1})
			tester := workspaceDirtyColorTester(state, dark, 640, []int{1})
			palette := colors(dark)
			text := "本地未提交 1"
			rect, found := tester.Find(text)
			if !found {
				test.Fatal("missing local diff button")
			}
			tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
			assertWorkspaceControlColors(test, tester, text, palette.soft, palette.amber)
			tester.Press(rect.X+rect.W/2, rect.Y+rect.H/2)
			assertWorkspaceControlColors(test, tester, text, palette.selected, palette.amber)
			tester.Release(639, 359)
			state.busy = true
			tester.Frame()
			rect, _ = tester.Find("本地未提交 1")
			disabledForeground := ui.RGB(
				uint8((int(palette.surface.R)+int(palette.muted.R))/2),
				uint8((int(palette.surface.G)+int(palette.muted.G))/2),
				uint8((int(palette.surface.B)+int(palette.muted.B))/2),
			)
			assertWorkspaceControlColors(test, tester, "本地未提交 1", palette.surface, disabledForeground)
			tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
			assertWorkspaceControlColors(test, tester, "本地未提交 1", palette.surface, disabledForeground)
			tester.Press(rect.X+rect.W/2, rect.Y+rect.H/2)
			assertWorkspaceControlColors(test, tester, "本地未提交 1", palette.surface, disabledForeground)
			tester.Release(rect.X+rect.W/2, rect.Y+rect.H/2)
			if err := tester.Click("本地未提交 1"); err != nil {
				test.Fatal(err)
			}
			if state.workspaceHistory != nil {
				test.Fatal("busy local diff button opened the review")
			}
		})
	}
}

func TestWorkspaceDirtySummaryRefreshAndReviewFocusReturn(test *testing.T) {
	item := application.Workspace{ID: "workspace", ServiceID: "service", ModuleID: "module", Path: `C:\synthetic\dirty-refresh`}
	taskItem := task{id: "task", path: `C:\synthetic\task`, createdAt: "created", groupID: "group", workspaces: []application.Workspace{item}}
	state := &app{
		page: "tasks", selected: 0, tasks: []task{taskItem}, configJSON: `{}`,
		snapshot: application.Snapshot{Home: `C:\synthetic\home`},
		workspaceGit: &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{workspaceHealthPathKey(item.Path): {
			State: domain.WorkspaceGitReady, DirtyFileCount: 1, PushState: domain.LocalPushPushed,
		}}},
	}
	tester := ui.NewTester(func(context *ui.Context) {
		palette := setTheme(context, false)
		ui.Column(context).Fill().Children(func() {
			state.workspaceHealthLine(context, palette, taskItem, item, "refresh-focus")
			state.restoreWorkspaceReviewFocus(context)
		})
	}, 640, 160)
	assertWorkspaceControlColors(test, tester, "本地未提交 1", colors(false).amberBg, colors(false).amber)
	if err := tester.Click("本地未提交 1"); err != nil {
		test.Fatal(err)
	}
	review := state.workspaceHistory
	if review == nil || review.CompareMode != workspaceReviewUncommitted {
		test.Fatal("nonzero local count did not open local-only review")
	}
	state.backWorkspaceReview(review)
	tester.Frame()
	tester.Frame()
	if state.workspaceReviewFocusReturn != nil || !tester.Focused("本地未提交 1") {
		test.Fatal("back did not restore focus to the local diff button")
	}

	health := state.workspaceGit.health[workspaceHealthPathKey(item.Path)]
	health.DirtyFileCount = 0
	state.workspaceGit.health[workspaceHealthPathKey(item.Path)] = health
	tester.Frame()
	if tester.HasText("本地未提交 1") {
		test.Fatal("refresh retained the stale local change count")
	}
	tester.Move(639, 159)
	assertWorkspaceControlColors(test, tester, "本地未提交 0", colors(false).surface, colors(false).text)
}

func workspaceDirtyColorApp(counts []int) *app {
	health := make(map[string]domain.WorkspaceGitHealth, len(counts))
	for index, count := range counts {
		item := application.Workspace{Path: fmt.Sprintf(`C:\synthetic\dirty-%d`, index)}
		health[workspaceHealthPathKey(item.Path)] = domain.WorkspaceGitHealth{
			State: domain.WorkspaceGitReady, DirtyFileCount: count, PushState: domain.LocalPushPushed,
		}
	}
	return &app{workspaceGit: &workspaceGitState{health: health}}
}

func workspaceDirtyColorTester(state *app, dark bool, width int, counts []int) *ui.Tester {
	return ui.NewTester(func(context *ui.Context) {
		palette := setTheme(context, dark)
		ui.Column(context).Fill().Padding(12).Gap(8).Children(func() {
			for index := range counts {
				item := application.Workspace{Path: fmt.Sprintf(`C:\synthetic\dirty-%d`, index)}
				ui.Column(context).FillWidth().Gap(6).Padding(8).Border(1, palette.line).Radius(8).Background(palette.surface).Children(func() {
					label(context, fmt.Sprintf("工作区 %d", index+1), 12, palette.text)
					state.workspaceHealthLine(context, palette, task{}, item, fmt.Sprintf("dirty-%d", index))
				})
			}
		})
	}, width, 360)
}

func assertWorkspaceControlColors(test *testing.T, tester *ui.Tester, text string, background, foreground ui.Color) {
	test.Helper()
	rect, found := tester.Find(text)
	if !found {
		test.Fatalf("missing workspace control %q", text)
	}
	column := int(math.Round(float64(rect.X))) + 4
	row := int(math.Round(float64(rect.Y + rect.H/2)))
	actualBackground := tester.Image().RGBAAt(column, row)
	wantBackground := color.RGBA{R: background.R, G: background.G, B: background.B, A: background.A}
	if actualBackground != wantBackground {
		test.Fatalf("%q background=%v, want %v", text, actualBackground, wantBackground)
	}
	if !workspaceControlHasForeground(tester, rect, foreground) {
		test.Fatalf("%q does not render foreground color %v", text, foreground)
	}
}

func workspaceControlHasForeground(tester *ui.Tester, rect ui.Rect, foreground ui.Color) bool {
	frame := tester.Image()
	expected := color.RGBA{R: foreground.R, G: foreground.G, B: foreground.B, A: foreground.A}
	for column := int(math.Round(float64(rect.X))) + 9; column < int(math.Round(float64(rect.X+rect.W)))-9; column++ {
		for row := int(math.Round(float64(rect.Y))) + 1; row < int(math.Round(float64(rect.Y+rect.H)))-1; row++ {
			pixel := frame.RGBAAt(column, row)
			if pixel.A == expected.A &&
				math.Abs(float64(pixel.R)-float64(expected.R)) <= 1 &&
				math.Abs(float64(pixel.G)-float64(expected.G)) <= 1 &&
				math.Abs(float64(pixel.B)-float64(expected.B)) <= 1 {
				return true
			}
		}
	}
	return false
}
