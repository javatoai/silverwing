package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

func TestWorkspaceDiffReadingModesStayExclusive(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rows_%v", fallback), func(t *testing.T) {
			state := reviewRendererTestState(fallback)
			state.PreviewMode = workspace.PreviewComparison
			s := &app{}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview)
				})
			}, 1000, 600)
			if err := tester.Click("改动上下文"); err != nil {
				t.Fatalf("context reading mode is not discoverable: %v", err)
			}
			tester.Frame()
			if !state.ContextLinesOnly || state.ChangedLinesOnly {
				t.Fatal("context mode must preserve nearby lines rather than enable strict filtering")
			}
			state.SelectedPath = "other.java"
			tester.Frame()
			if !state.ContextLinesOnly || state.ChangedLinesOnly {
				t.Fatal("switching files must retain the selected context reading mode")
			}
			if err := tester.Click("仅看改动"); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if state.ContextLinesOnly || !state.ChangedLinesOnly {
				t.Fatal("strict changes mode must be exclusive with context mode")
			}
			state.SelectedPath = "third.java"
			tester.Frame()
			if state.ContextLinesOnly || !state.ChangedLinesOnly {
				t.Fatal("switching files must retain strict changes mode")
			}
			if err := tester.Click("查看全部"); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if state.ContextLinesOnly || state.ChangedLinesOnly {
				t.Fatal("all-lines mode must clear both projections")
			}
		})
	}
}

func TestWorkspaceDiffReadingModesFitCompleteReviewPage(t *testing.T) {
	for _, width := range []int{540, 960} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width_%d_dark_%v", width, dark), func(t *testing.T) {
				state := reviewNavigationTestState()
				state.Preview = reviewRendererTestState(false).Preview
				state.PreviewMode = workspace.PreviewComparison
				s := &app{
					page: "tasks", workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}}, taskUnit: taskController{state: taskState{tasks: []task{{id: state.TaskID, path: `C:\synthetic\reading-mode-task`, workspaces: []application.Workspace{{
						ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path, Branch: "feature/reading-modes",
					}}}}}},
				}
				requiredWidths := make(map[string]float32)
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					for _, title := range []string{"左右对比", "HEAD 内容", "查看全部", "改动上下文", "仅看改动"} {
						textWidth, _ := c.MeasureText(0, ui.Span{Text: title, Size: 10})
						requiredWidths[title] = textWidth + 20
					}
					s.renderWorkspaceReviewPage(c, p)
				}, width, 680)
				for _, collapsed := range []bool{false, true} {
					if collapsed {
						if err := tester.Click("收起文件列表"); err != nil {
							t.Fatal(err)
						}
						tester.Frame()
					}
					for _, title := range []string{"左右对比", "HEAD 内容", "查看全部", "改动上下文", "仅看改动"} {
						bounds, found := tester.Find(title)
						if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.W <= 0 {
							t.Errorf("%q escaped complete review page (width=%d, collapsed=%v): %+v (found=%v)", title, width, collapsed, bounds, found)
						}
						if found && bounds.W+1 < requiredWidths[title] {
							t.Errorf("%q caption is clipped (width=%d, collapsed=%v): button=%g, text and padding=%g", title, width, collapsed, bounds.W, requiredWidths[title])
						}
					}
					captureDesktopTestFrame(t, tester, fmt.Sprintf("git-review-page-width-%d-dark-%v-collapsed-%v", width, dark, collapsed))
				}
			})
		}
	}
}

func TestWorkspaceDiffReadingModesStayInPreviewBounds(t *testing.T) {
	for _, width := range []float32{440, 1000} {
		for _, fallback := range []bool{false, true} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("width_%g_rows_%v_dark_%v", width, fallback, dark), func(t *testing.T) {
					state := reviewRendererTestState(fallback)
					state.PreviewMode = workspace.PreviewComparison
					s := &app{}
					tester := ui.NewTester(func(c *ui.Context) {
						ui.Column(c).Fill().Padding(8).Children(func() {
							s.renderWorkspaceFilePreview(c, setTheme(c, dark), state, state.Preview)
						})
					}, int(width), 600)
					for _, mode := range []string{"查看全部", "改动上下文", "仅看改动"} {
						if err := tester.Click(mode); err != nil {
							t.Fatal(err)
						}
						tester.Frame()
						for _, title := range []string{"查看全部", "改动上下文", "仅看改动"} {
							bounds, found := tester.Find(title)
							if !found || bounds.X < 0 || bounds.X+bounds.W > width+1 || bounds.W <= 0 {
								t.Fatalf("%q escaped preview width %g: %+v (found=%v)", title, width, bounds, found)
							}
						}
						captureDesktopTestFrame(t, tester, fmt.Sprintf("git-diff-width-%g-rows-%v-dark-%v-%s", width, fallback, dark, mode))
					}
				})
			}
		}
	}
}
