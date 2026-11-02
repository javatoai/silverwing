package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func reviewToolbarTestPage(state *workspaceHistoryState, width int, dark bool) (*app, *ui.Tester) {
	item := application.Workspace{ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path, Branch: "feature/" + strings.Repeat("long-branch-name-", 8)}
	s := &app{page: "tasks", workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}}, taskUnit: taskController{state: taskState{tasks: []task{{id: state.TaskID, path: `C:\synthetic\toolbar-task`, workspaces: []application.Workspace{item}}}}}}
	return s, ui.NewTester(func(c *ui.Context) {
		s.beginOverflowMenuFrame()
		s.renderWorkspaceReviewPage(c, setTheme(c, dark))
		s.endOverflowMenuFrame()
	}, width, 680)
}

func TestWorkspaceReviewToolbarStaysTopRightAndSingleRow(t *testing.T) {
	for _, width := range []int{600, 1100} {
		for _, dark := range []bool{false, true} {
			for _, mode := range []string{workspaceReviewMainline, workspaceReviewCommits} {
				t.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, mode), func(t *testing.T) {
					state := reviewNavigationTestState()
					state.CompareMode = mode
					state.BaselineDisplayRef = "upstream/" + strings.Repeat("long-reference-name/", 7) + "main"
					state.CommitsLoaded = true
					if mode == workspaceReviewCommits {
						state.StartCommit, state.EndCommit = strings.Repeat("c", 40), strings.Repeat("d", 40)
						state.CommitHashes = []string{state.StartCommit, state.EndCommit}
						state.SimulateOnMainline = true
					}
					_, tester := reviewToolbarTestPage(state, width, dark)
					toolbar, found := tester.Find("差异比较工具栏")
					if !found || toolbar.X+toolbar.W > float32(width)-7 || toolbar.Y+toolbar.H > 53 {
						t.Fatalf("toolbar does not occupy the top-right header row: %+v", toolbar)
					}
					controls := []string{"比较范围", "刷新差异", "更多审阅操作"}
					if width < 1050 {
						controls = append(controls, "比较模式")
					} else {
						controls = append(controls, "整体差异", "本地未提交", "提交范围")
					}
					if mode == workspaceReviewCommits {
						controls = append(controls, "提交范围：cccccccc → dddddddd · 2 次")
					}
					for _, name := range controls {
						r, ok := tester.Find(name)
						if !ok || r.W <= 0 || r.X < toolbar.X || r.X+r.W > float32(width)-7 || r.Y < toolbar.Y || r.Y+r.H > 53 {
							t.Errorf("top-right control %q does not fit: found=%t bounds=%+v toolbar=%+v", name, ok, r, toolbar)
						}
					}
					files, found := tester.Find("变更文件")
					if !found || files.Y > 100 {
						t.Fatalf("old comparison rows still consume diff height: %+v", files)
					}
					if mode == workspaceReviewCommits {
						captureDesktopTestFrame(t, tester, fmt.Sprintf("review-toolbar-%d-dark-%t", width, dark))
					}
					scope, _ := tester.Find("比较范围")
					tester.Move(scope.X+scope.W/2, scope.Y+scope.H/2)
					tester.Advance(650 * time.Millisecond)
					_, fullTooltip := workspaceReviewComparisonSummary(state)
					if _, found := tester.Find(fullTooltip); !found {
						t.Fatal("the clipped comparison scope lost its complete tooltip")
					}
				})
			}
		}
	}
}

func TestWorkspaceReviewCompactModeAndCommitPickerRemainKeyboardReachable(t *testing.T) {
	state := reviewNavigationTestState()
	state.CommitsLoaded = true
	state.Commits = []domain.WorkspaceGitCommit{{FullHash: "a", ShortHash: "a", Message: "first"}}
	s, tester := reviewToolbarTestPage(state, 600, false)
	t.Cleanup(func() { s.cancelWorkspaceReviewDebounce(state) })
	if err := tester.Click("比较模式"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEnd)
	tester.Key(0, ui.KeyEnter)
	if state.CompareMode != workspaceReviewCommits || !state.CommitPickerOpen {
		t.Fatal("compact native selector did not open commit range selection")
	}
	if err := tester.Click("a first"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("完成"); err != nil {
		t.Fatal(err)
	}
	if len(state.CommitHashes) != 1 || state.CommitHashes[0] != "a" || !tester.Focused("比较模式") {
		t.Fatal("commit selection or native opener focus changed in the compact toolbar")
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("提交范围：提交 a") {
		t.Fatal("Tab cannot reach the relocated commit range trigger")
	}
	tester.Key(0, ui.KeyEnter)
	if !state.CommitPickerOpen {
		t.Fatal("the relocated commit trigger cannot be reopened with the keyboard")
	}
	tester.Key(0, ui.KeyEscape)
	if state.CommitPickerOpen || !state.Open || !tester.Focused("提交范围：提交 a") {
		t.Fatal("Escape no longer closes only the commit picker")
	}
}

func TestSettingsHelpUsesKeyboardFocusRingWithoutPointerBlueBorder(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%t", dark), func(t *testing.T) {
			var focusColor ui.Color
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, dark)
				focusColor = c.Theme().Focus.Over(p.bg)
				ui.Column(c).Fill().Padding(32).Gap(16).Children(func() {
					settingsHelpLabel(c, p, "功能", "仅查看说明", 12, 600)
					ui.Button(c, "下一项")
				})
			}, 400, 240)
			if err := tester.Click("功能说明"); err != nil {
				t.Fatal(err)
			}
			button, _ := tester.Find("功能说明")
			countColor := func(want ui.Color) int {
				near := func(a, b uint8) bool { return max(a, b)-min(a, b) <= 1 }
				frame, count := tester.Image(), 0
				for y := max(0, int(math.Floor(float64(button.Y)))-4); y < min(frame.Bounds().Max.Y, int(button.Y+button.H)+4); y++ {
					for x := max(0, int(math.Floor(float64(button.X)))-4); x < min(frame.Bounds().Max.X, int(button.X+button.W)+4); x++ {
						pixel := frame.RGBAAt(x, y)
						if near(pixel.R, want.R) && near(pixel.G, want.G) && near(pixel.B, want.B) && pixel.A == 255 {
							count++
						}
					}
				}
				return count
			}
			if !tester.Focused("功能说明") || countColor(colors(dark).blue) != 0 || countColor(focusColor) != 0 {
				t.Fatal("pointer focus still paints a blue help border or focus ring")
			}
			if !dark {
				captureDesktopTestFrame(t, tester, "settings-help-pointer-focus")
			}
			// A pointer click dismisses this anchor's tooltip until the pointer
			// leaves. Start a fresh keyboard focus path, as the native help test.
			tester.Move(390, 230)
			tester.Frame()
			tester.Key(0, ui.KeyTab)
			tester.Key(ui.Shift, ui.KeyTab)
			if !tester.Focused("功能说明") || countColor(focusColor) < 4 || !tester.HasText("仅查看说明") {
				t.Fatal("keyboard Tab lost native focus indication or help")
			}
			if !dark {
				captureDesktopTestFrame(t, tester, "settings-help-keyboard-focus")
			}
		})
	}
}
