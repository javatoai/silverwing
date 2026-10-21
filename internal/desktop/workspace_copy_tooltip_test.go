package desktop

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type workspaceCopyTooltipFixture struct {
	s      *app
	task   task
	w      application.Workspace
	scroll ui.ScrollState
}

func newWorkspaceCopyTooltipFixture(t *testing.T, long bool) *workspaceCopyTooltipFixture {
	t.Helper()
	name, path, branch := "example-service", filepath.Join(t.TempDir(), "工作区"), "feature/example"
	if long {
		name += strings.Repeat("-完整项目名称", 12)
		path = filepath.Join(path, strings.Repeat("long-directory-", 14), "完整路径")
		branch += strings.Repeat("-完整分支名称", 12)
	}
	w := application.Workspace{ID: "copy-workspace", ServiceID: "example-service", ModuleID: "example-module", Name: name, Path: path, Branch: branch, State: "READY",
		Record: domain.ServiceWorkspace{ServiceName: name, ModuleName: name}}
	taskItem := task{id: "copy-task", groupID: "default", workspaces: []application.Workspace{w}}
	s := newApp(nil, Options{CapturePath: "isolated-workspace-copy-tooltip"})
	s.loading, s.tasks, s.configJSON = false, []task{taskItem}, `{"tagEnabled":false}`
	s.workspaceGit = &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{
		workspaceHealthPathKey(path): {State: domain.WorkspaceGitReady, ActualBranch: &branch, PushState: domain.LocalPushPushed},
	}}
	return &workspaceCopyTooltipFixture{s: s, task: taskItem, w: w}
}

func (f *workspaceCopyTooltipFixture) tester(width int, dark bool, top float32, right bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Scroll(c).Fill().TrackScroll(&f.scroll).Children(func() {
			ui.Column(c).FillWidth().Padding(12).Gap(8).Children(func() {
				ui.Box(c).Height(top)
				ui.Row(c).FillWidth().Children(func() {
					if right {
						ui.Spacer(c)
					}
					host := ui.Column(c).MinWidth(0).Label("复制卡片容器")
					if right {
						host.Width(420).MaxWidthPercent(100)
					} else {
						host.Grow(1)
					}
					host.Children(func() { f.s.workspaceCard(c, p, f.task, f.w, 0) })
				})
				ui.Box(c).Height(600)
			})
		})
	}, width, 560)
}

func workspaceCopyTooltipTargets(f *workspaceCopyTooltipFixture) []struct{ action, tooltip, value, feedback string } {
	return []struct{ action, tooltip, value, feedback string }{
		{"复制项目名", "点击复制项目名\n" + f.w.Name, f.w.Name, "已复制项目名"},
		{"复制分支名", "点击复制分支名\n" + f.w.Branch, f.w.Branch, "已复制分支名"},
	}
}

func captureWorkspaceCopyTooltip(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	directory := os.Getenv("SILVERWING_WORKSPACE_COPY_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(directory, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	encodeErr, closeErr := png.Encode(file, tester.Image()), file.Close()
	if encodeErr != nil || closeErr != nil {
		t.Fatalf("screenshot: %v / %v", encodeErr, closeErr)
	}
}

func assertWorkspaceCopyTooltip(t *testing.T, tester *ui.Tester, action, tooltip string, width int) {
	t.Helper()
	anchor, ok := tester.Find(action)
	if !ok || anchor.W <= 0 || anchor.H <= 0 {
		t.Fatalf("missing copy anchor %q: %+v", action, anchor)
	}
	tester.Move(anchor.X+anchor.W/2, anchor.Y+anchor.H/2)
	time.Sleep(650 * time.Millisecond)
	tester.Frame()
	tip, ok := tester.Find(tooltip)
	if !ok {
		t.Fatalf("complete tooltip missing for %s; texts=%q", action, tester.Texts())
	}
	// Find reports the tooltip text inside its native 8 DIP horizontal padding.
	wantCenter := max(12+tip.W/2, min(anchor.X+anchor.W/2, float32(width)-12-tip.W/2))
	if math.Abs(float64(tip.X+tip.W/2-wantCenter)) > 1 || tip.X < 11 || tip.X+tip.W > float32(width)-11 ||
		tip.Y < 4 || tip.Y+tip.H > 556 || !(tip.Y+tip.H <= anchor.Y || tip.Y >= anchor.Y+anchor.H) {
		t.Fatalf("tooltip detached/overflowed: anchor=%+v tip=%+v center=%v", anchor, tip, wantCenter)
	}
	t.Logf("%s: anchor=%+v tooltipText=%+v", action, anchor, tip)
	captureWorkspaceCopyTooltip(t, tester, strings.ReplaceAll(t.Name()+"-"+action, "/", "-"))
	tester.Key(0, ui.KeyEscape)
	if tester.HasText(tooltip) {
		t.Fatal("Escape did not dismiss tooltip")
	}
	tester.Move(float32(width)-2, 558)
	tester.Frame()
}

func TestWorkspaceCardCopyTooltipContentBoundsAndScale(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		for _, scenario := range []struct {
			width      int
			long, dark bool
		}{{1100, false, false}, {360, true, true}} {
			t.Run(fmt.Sprintf("%d-scale-%.1f", scenario.width, scale), func(t *testing.T) {
				f := newWorkspaceCopyTooltipFixture(t, scenario.long)
				tester := f.tester(scenario.width, scenario.dark, 180, false)
				tester.SetScale(scale)
				tester.Frame()
				host, _ := tester.Find("复制卡片容器")
				var previous ui.Rect
				for index, target := range workspaceCopyTooltipTargets(f) {
					anchor, _ := tester.Find(target.action)
					text, _ := tester.Find(target.value)
					if anchor.X < host.X || anchor.X+anchor.W > host.X+host.W-12 || text.W > anchor.W || text.H > 23 ||
						!scenario.long && anchor.W >= host.W-70 || index > 0 && math.Abs(float64(anchor.X-previous.X)) > .1 {
						t.Fatalf("copy area stretched, overflowed or wrapped: host=%+v anchor=%+v text=%+v", host, anchor, text)
					}
					previous = anchor
					assertWorkspaceCopyTooltip(t, tester, target.action, target.tooltip, scenario.width)
					if err := tester.Click(target.action); err != nil {
						t.Fatal(err)
					}
					if tester.Clipboard() != target.value || f.s.lastMessage != target.feedback {
						t.Fatal("copy changed original value or success feedback")
					}
				}
				tester.Key(0, ui.KeyTab)
				if tester.Image().Bounds().Dx() != int(float32(scenario.width)*scale) {
					t.Fatal("device scale was not applied to native raster output")
				}
			})
		}
	}
}

func TestWorkspaceCardCopyTooltipScrollAndViewportEdges(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		top, scroll float32
		right       bool
	}{
		{"top-left", 0, 0, false}, {"right", 180, 0, true}, {"scrolled", 320, 260, false}, {"bottom", 430, 0, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newWorkspaceCopyTooltipFixture(t, false)
			tester := f.tester(1100, false, scenario.top, scenario.right)
			if scenario.scroll > 0 {
				before, _ := tester.Find("复制项目名")
				f.scroll.Y = scenario.scroll
				tester.Frame()
				after, _ := tester.Find("复制项目名")
				if math.Abs(float64(before.Y-after.Y-scenario.scroll)) > 1 {
					t.Fatalf("fixture did not actually scroll: before=%+v after=%+v scroll=%+v", before, after, f.scroll)
				}
			}
			for _, target := range workspaceCopyTooltipTargets(f) {
				assertWorkspaceCopyTooltip(t, tester, target.action, target.tooltip, 1100)
			}
		})
	}
}

func TestWorkspaceCardCopyButtonKeyboardAndDisabled(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		copied := false
		tester := ui.NewTester(func(c *ui.Context) {
			p := setTheme(c, false)
			ui.Column(c).Fill().Padding(20).Children(func() {
				button := workspaceCardCopyButton(c, "copy", "复制测试值", "完整提示", disabled)
				button.Children(func() { label(c, "完整测试值", 13, p.text).SingleLine() })
				if button.Clicked() {
					copied = true
				}
			})
		}, 400, 200)
		tester.Key(0, ui.KeyTab)
		if tester.Focused("复制测试值") == disabled {
			t.Fatal("copy button lost keyboard focus or disabled guard")
		}
		tester.Key(0, ui.KeyEnter)
		if copied == disabled {
			t.Fatal("Enter copy activation ignored disabled state")
		}
	}
}
