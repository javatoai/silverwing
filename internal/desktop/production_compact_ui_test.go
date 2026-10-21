package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestProductionActionsKeepContentWidth(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"initial", "prepared", "diff-error"} {
				t.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, state), func(t *testing.T) {
					s, v, b := productionFixture(t)
					controls := []string{"查看差异", "合并生产 Tag 到 master", "创建 release"}
					switch state {
					case "initial":
						s.production.batches = nil
						v.BatchID, v.Preview.ProductionInMain = "", false
						v.Preview.ProductionSHA = strings.Repeat("f", 40)
						v.Preview.Diff = " example.go | 1 +\n 1 file changed, 1 insertion(+)"
					case "prepared":
						b.TagPlan = &production.TagPlan{Tag: "2.3.44", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
						s.production.batches[0] = b
						controls = []string{"合并到 release", "构建 Tag"}
					case "diff-error":
						s.openProductionDiff(v, "生产 Tag → 主分支", "2.3.43", "origin/master", v.Preview.ProductionSHA, v.Preview.MainSHA, true)
						s.production.diffDialog.Review.Error = "读取失败，请重试"
						controls = []string{"重试", "关闭"}
					}
					tester := productionIterationTester(s, width, dark)
					height := float32(900)
					if state == "prepared" {
						height = 1600
						tester.SetSize(width, int(height))
					}
					for _, text := range controls {
						r, ok := tester.Find(text)
						if !ok || r.W < 40 || r.W > 250 || r.H != 34 || r.X < 0 || r.X+r.W > float32(width) || r.Y < 0 || r.Y+r.H > height {
							t.Errorf("action %q must be content-sized and visible, got %+v (found %t)", text, r, ok)
						}
					}
					if dir := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); dir != "" {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(dir, fmt.Sprintf("compact-%d-dark-%t-%s.png", width, dark, state)))
						if err != nil {
							t.Fatal(err)
						}
						imageErr, closeErr := png.Encode(f, tester.Image()), f.Close()
						if imageErr != nil || closeErr != nil {
							t.Fatalf("capture: %v %v", imageErr, closeErr)
						}
					}
				})
			}
		}
	}
}

func TestProductionCompactMainMergeKeyboardKeepsConfirmation(t *testing.T) {
	s, v, _ := productionFixture(t)
	s.production.batches = nil
	v.BatchID, v.Preview.ProductionInMain = "", false
	s.win = &mygo.Window{}
	tester := productionIterationTester(s, 960, false)
	const merge = "合并生产 Tag 到 master"
	for i := 0; i < 20 && !tester.Focused(merge); i++ {
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused(merge) {
		t.Fatal("compact merge button is not keyboard reachable")
	}
	tester.Key(0, ui.KeyEnter)
	if s.confirmationDialog == nil || s.confirmationDialog.Open == false {
		t.Fatal("keyboard merge bypassed or failed to open confirmation")
	}
	tester.Key(0, ui.KeyEscape)
	if s.confirmationDialog != nil && s.confirmationDialog.Open {
		t.Fatal("Esc did not cancel merge confirmation")
	}
	v.Preview.ProductionInMain = true
	tester.Frame()
	if _, found := tester.Find(merge); found || !tester.HasText("无需合并") {
		t.Fatal("already-contained merge must be a clear noninteractive status")
	}
	if s.confirmationDialog != nil && s.confirmationDialog.Open {
		t.Fatal("already-contained disabled merge opened a confirmation")
	}
}
