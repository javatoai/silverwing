package desktop

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func compactFilePreviewTester(state *workspaceHistoryState, width int, dark bool) *ui.Tester {
	s := &app{}
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(8).Gap(7).Children(func() {
			s.renderWorkspaceReviewPreview(c, setTheme(c, dark), state, "compact-preview-test")
		})
	}, width, 540)
}

func TestWorkspaceFileDiffHeaderUsesTwoWideRowsAndKeepsNarrowActions(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, width := range []int{480, 1000} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("rows-%t-%d-dark-%t", fallback, width, dark), func(t *testing.T) {
					state := reviewRendererTestState(fallback)
					state.SelectedPath = "src/main/java/" + strings.Repeat("riskcontrol/", 10) + "RiskControlDynamicServiceImpl.java"
					state.PreviewMode = workspace.PreviewComparison
					state.DiffBaseLabel = "release/" + strings.Repeat("long-source-", 6)
					state.DiffTargetLabel = "源提交 · release/" + strings.Repeat("long-target-", 6)
					view := compactFilePreviewTester(state, width, dark)
					identity, found := view.Find("文件差异信息栏")
					toolbar, hasToolbar := view.Find("差异阅读操作栏")
					if !found || identity.H > 28.5 || !hasToolbar {
						t.Fatalf("missing compact file header: identity=%+v toolbar=%+v", identity, toolbar)
					}
					for _, name := range []string{"左右对比", "HEAD 内容", "查看全部", "改动上下文", "仅看改动", "上一处", "下一处", "查找", "跳到行"} {
						r, ok := view.Find(name)
						if !ok || r.W <= 0 || r.H <= 0 || r.X < 8 || r.X+r.W > float32(width)-7 {
							t.Errorf("file action %q is clipped: %+v found=%t", name, r, ok)
						}
						if width >= 760 && (name == "左右对比" || name == "仅看改动") && (r.Y < identity.Y || r.Y+r.H > identity.Y+identity.H) {
							t.Errorf("view/projection action %q still occupies its own row: %+v", name, r)
						}
					}
					if width >= 760 && (toolbar.H > 30.5 || toolbar.Y+toolbar.H > identity.Y+60) {
						t.Fatalf("wide file header still consumes more than two rows: identity=%+v toolbar=%+v", identity, toolbar)
					}
					oldTitle, newTitle := workspaceDiffSideLabels(state, state.Preview.Comparison)
					old, oldFound := view.Find(oldTitle)
					current, currentFound := view.Find(newTitle)
					if !oldFound || !currentFound || old.W < 40 || current.W < 40 || old.X+old.W > current.X {
						t.Fatalf("version identities no longer occupy their source halves: old=%+v current=%+v", old, current)
					}
					captureDesktopTestFrame(t, view, fmt.Sprintf("compact-file-%d-rows-%t-dark-%t", width, fallback, dark))
					if err := view.Click("仅看改动"); err != nil {
						t.Fatal(err)
					}
					if !state.ChangedLinesOnly || state.ContextLinesOnly || state.DiffRows == nil {
						t.Fatal("relocated projection did not use the existing row renderer")
					}
					if err := view.Click("改动上下文"); err != nil {
						t.Fatal(err)
					}
					if !state.ContextLinesOnly || state.ChangedLinesOnly {
						t.Fatal("relocated projection failed to preserve exclusivity")
					}
				})
			}
		}
	}
}

// Transparent action slots expose any text that the caption paints beyond
// its allocated width; normal button backgrounds could hide that overdraw.
func TestWorkspaceCompactSourceCaptionsDoNotPaintIntoActions(t *testing.T) {
	state := &workspaceHistoryState{DiffBaseLabel: "release/" + strings.Repeat("m", 45), DiffTargetLabel: "release/" + strings.Repeat("n", 65)}
	var background ui.Color
	var requiredWidth float32
	view := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		background = p.soft
		requiredWidth, _ = c.MeasureText(0, ui.Span{Text: state.DiffBaseLabel, Size: 10})
		ui.Column(c).Fill().Background(p.surface).Children(func() {
			workspaceDiffHeader(c, p, state, nil, .5, func(float32) {
				ui.Box(c).Width(110).Height(24).Shrink(0).Label("导航空位")
			}, func() {
				ui.Box(c).Width(110).Height(24).Shrink(0).Label("阅读空位")
			})
		})
	}, 1000, 120)
	caption, _ := view.Find(state.DiffBaseLabel)
	if requiredWidth <= caption.W || requiredWidth >= 488 {
		t.Fatalf("fixture must fit the former source half but overflow the new caption budget: text=%v available=%v", requiredWidth, caption.W)
	}
	frame := view.Image()
	want := color.RGBA{R: background.R, G: background.G, B: background.B, A: 255}
	for _, name := range []string{"导航空位", "阅读空位"} {
		r, found := view.Find(name)
		if !found {
			t.Fatal("reserved action slot missing")
		}
		for y := int(r.Y) + 4; y < int(r.Y+r.H)-4; y++ {
			for x := int(r.X) + 2; x < int(r.X+r.W)-2; x++ {
				if got := frame.RGBAAt(x, y); got != want {
					t.Fatalf("source caption painted into %s at (%d,%d): %v, want background %v", name, x, y, got, want)
				}
			}
		}
	}
}

func TestWorkspaceCompactHeaderKeepsSingleFileAndPendingBranches(t *testing.T) {
	state := reviewRendererTestState(false)
	state.SelectedKind = domain.WorkspaceGitAdded
	state.Preview.Comparison.OldContent = nil
	view := compactFilePreviewTester(state, 1000, false)
	for _, name := range []string{"左右对比", "仅看改动", "上一处", "差异阅读操作栏"} {
		if _, found := view.Find(name); found {
			t.Fatalf("added file regained an empty comparison control %q", name)
		}
	}
	if _, found := view.Find("新增"); !found || state.NativeDiff == nil || !state.NativeDiff.singlePane {
		t.Fatal("added file lost its identity or single source")
	}
	state.SelectedOldPath, state.SelectedPath = "old.java", "new.java"
	state.SelectedKind = domain.WorkspaceGitRenamed
	state.PreviewLoading = true
	view.Frame()
	if !view.HasText("old.java → new.java") || !view.HasText("正在读取 Diff…") || view.HasText("左右对比") {
		t.Fatal("pending renamed file lost its identity or exposed stale view controls")
	}
}
