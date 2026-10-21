package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func productionAssertTextColor(t *testing.T, tester *ui.Tester, text string, want ui.Color) {
	t.Helper()
	r, ok := tester.Find(text)
	if !ok {
		t.Fatalf("missing text %q", text)
	}
	frame := tester.Image()
	count := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			pixel := frame.RGBAAt(x, y)
			if pixel.R == want.R && pixel.G == want.G && pixel.B == want.B {
				count++
			}
		}
	}
	if count < 3 {
		t.Fatalf("%q did not render in its semantic color: matched %d pixels", text, count)
	}
}

func TestProductionBaselineDiffReflectsIncomingFileChanges(t *testing.T) {
	for _, state := range []string{"unknown", "incomplete", "same-commit", "main-ahead", "same-tree", "remote-only", "changed", "changed-and-remote"} {
		t.Run(state, func(t *testing.T) {
			s, v, _ := productionFixture(t)
			v.Preview.ProductionSHA = strings.Repeat("f", 40)
			v.Preview.ProductionInMain = false
			want, enabled := "无差异", true
			switch state {
			case "unknown":
				v.Preview = nil
				want, enabled = "查看差异", false
			case "incomplete":
				v.Preview.MainSHA = ""
				want, enabled = "查看差异", false
			case "same-commit":
				v.Preview.ProductionSHA = v.Preview.MainSHA
			case "main-ahead":
				v.Preview.ProductionInMain = true
				v.Preview.Commits = "主分支已有额外提交，全部保留"
			case "remote-only":
				v.Preview.Diff = "\n\n远端 release 相对本地批次的文件变化：\n other.go | 2 ++"
			case "changed", "changed-and-remote":
				want = "查看差异"
				v.Preview.Diff = " example.go | 1 +\n 1 file changed, 1 insertion(+)"
				if state == "changed-and-remote" {
					v.Preview.Diff += "\n\n远端 release 相对本地批次的文件变化：\n other.go | 2 ++"
				}
			}
			tester := productionIterationTester(s, 960, false)
			if _, ok := tester.Find(want); !ok {
				t.Fatalf("missing %q for %s", want, state)
			}
			if err := tester.Click(want); err != nil {
				t.Fatal(err)
			}
			d := s.production.diffDialog
			if (d != nil) != enabled {
				t.Fatal("unknown comparison opened, or known read-only comparison was lost")
			}
			if enabled && (d.SourceSHA != v.Preview.ProductionSHA || d.TargetSHA != v.Preview.MainSHA || !d.Incoming || v.Busy || s.confirmationDialog != nil) {
				t.Fatal("diff did not retain its exact incoming read-only comparison")
			}
		})
	}
}

func TestProductionInitializationFeedbackIsConciseAndColored(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, changes := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-dark-%t-changes-%t", width, dark, changes), func(t *testing.T) {
					s, v, _ := productionFixture(t)
					v.Steps = map[int]productionStepView{1: {State: "complete", Message: "生产 Tag 与主分支差异已就绪"}}
					if changes {
						v.Preview.ProductionInMain = false
						v.Preview.ProductionSHA = strings.Repeat("f", 40)
						v.Preview.Diff = " example.go | 1 +\n 1 file changed, 1 insertion(+)"
					}
					tester := productionIterationTester(s, width, dark)
					tester.SetSize(width, 1600)
					if tester.HasText("生产发布 成功") || tester.HasText("差异已就绪") {
						t.Fatal("redundant baseline status or success message remains")
					}
					p := colors(dark)
					productionAssertTextColor(t, tester, "已完成", p.green)
					if changes {
						productionAssertTextColor(t, tester, "查看差异", p.amber)
					} else {
						productionAssertTextColor(t, tester, "无需合并", p.green)
						productionAssertTextColor(t, tester, "无差异", p.text)
					}
					captureProductionSummary(t, tester, fmt.Sprintf("feedback-%d-dark-%t-changes-%t", width, dark, changes))
				})
			}
		}
	}
}

func TestProductionBaselineFeedbackRetainsActivityAndFailures(t *testing.T) {
	s, v, b := productionFixture(t)
	v.ActiveStep = 1
	v.Steps = map[int]productionStepView{1: {State: "complete", Message: "生产 Tag 与主分支差异已就绪"}}
	if got := s.productionSection(v, &b, 1); got.Message != "" {
		t.Fatal("completed baseline repeated its success message")
	}
	v.Busy, v.ActiveAction = true, "production.inspect"
	if got := s.productionSection(v, &b, 1); got.Activity == "" || got.Message != "" {
		t.Fatal("refresh lost its local activity or echoed old success")
	}
	v.Busy, v.Error = false, "读取远端失败，请重试"
	if got := s.productionSection(v, &b, 1); got.State != "failed" || got.Message != v.Error {
		t.Fatal("successful baseline display hid the latest refresh error")
	}
	v.Error = ""
	v.Steps[1] = productionStepView{State: "warning", Message: "线上存在多个版本，请确认基线"}
	if got := s.productionSection(v, &b, 1); got.Message == "" || got.State != "warning" {
		t.Fatal("baseline warning was hidden with success messages")
	}
}

func TestProductionBaselineDiffLabelUpdatesWithoutReplacingTarget(t *testing.T) {
	s, v, _ := productionFixture(t)
	s.notes[0] = "unsaved draft"
	tester := productionIterationTester(s, 960, false)
	if _, ok := tester.Find("无差异"); !ok {
		t.Fatal("initial incoming comparison should be empty")
	}
	v.Preview.ProductionSHA, v.Preview.ProductionInMain = strings.Repeat("f", 40), false
	v.Preview.Diff = " changed.go | 1 +\n 1 file changed, 1 insertion(+)"
	tester.Frame()
	productionAssertTextColor(t, tester, "查看差异", colors(false).amber)
	if err := tester.Click("查看差异"); err != nil {
		t.Fatal(err)
	}
	d := s.production.diffDialog
	if d == nil || d.SourceSHA != strings.Repeat("f", 40) || d.TargetSHA != strings.Repeat("a", 40) || s.notes[0] != "unsaved draft" || v.Busy {
		t.Fatal("updated comparison used an old target or disturbed the draft")
	}
}
