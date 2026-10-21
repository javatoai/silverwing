package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func captureProductionSummary(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	imageErr, closeErr := png.Encode(f, tester.Image()), f.Close()
	if imageErr != nil || closeErr != nil {
		t.Fatalf("capture: %v %v", imageErr, closeErr)
	}
}

func TestProductionCheckEntryBelongsToContent(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"first", "initial", "completed", "ended"} {
				t.Run(fmt.Sprintf("%d-%t-%s", width, dark, state), func(t *testing.T) {
					s, v, b := productionFixture(t)
					anchor := "1. 对比生产 Tag 与主分支差异"
					switch state {
					case "first", "initial":
						s.production.batches, v.BatchID = nil, ""
						if state == "first" {
							v.Preview = nil
						}
					case "ended":
						b.Ended = true
						s.production.batches[0] = b
						anchor = "正在查看已结束批次：" + b.ReleaseBranch
					}
					tester := productionIterationTester(s, width, dark)
					check, found := tester.Find("重新检查")
					section, ok := tester.Find(anchor)
					if !found || !ok || check.Y < section.Y+section.H || check.W > 180 || check.H != 34 {
						t.Errorf("check is outside content section: check=%+v section=%+v found=%t/%t", check, section, found, ok)
					}
					if state != "ended" {
						diffText := "无差异"
						if state == "first" {
							diffText = "查看差异"
						}
						diff, ok := tester.Find(diffText)
						if !ok || diff.Y != check.Y || diff.X <= check.X+check.W {
							t.Errorf("check/diff are not compact siblings: check=%+v diff=%+v", check, diff)
						}
					}
					captureProductionSummary(t, tester, fmt.Sprintf("summary-%d-dark-%t-%s", width, dark, state))
				})
			}
		}
	}
}

func TestProductionIncludedRelationsReplaceMergeButtons(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("initial-%t-busy-%t", initial, busy), func(t *testing.T) {
				s, v, _ := productionFixture(t)
				v.Preview.ProductionInMain, v.Preview.MainInRelease, v.Busy = true, true, busy
				if initial {
					s.production.batches, v.BatchID = nil, ""
				}
				tester := productionIterationTester(s, 960, false)
				for _, button := range []string{"合并生产 Tag 到 master", "合并主分支到 release"} {
					if _, exists := tester.Find(button); exists {
						t.Errorf("already-included relation retained button %q", button)
					}
				}
				if !tester.HasText("无需合并") {
					t.Fatal("missing clear noninteractive inclusion explanation")
				}
				for i := 0; i < 20; i++ {
					tester.Key(0, ui.KeyTab)
					if tester.Focused("无需合并") {
						t.Fatal("inclusion status entered keyboard focus")
					}
				}
			})
		}
	}
}

func TestProductionCreateConfirmationHasCollapsedTechnicalDetail(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-%t", width, dark), func(t *testing.T) {
				s, v, _ := productionFixture(t)
				s.win = &mygo.Window{}
				v.Preview.Commits = strings.Repeat("synthetic technical commit details\n", 80)
				s.confirmProductionCreate(v)
				d := s.confirmationDialog
				if d == nil || !strings.Contains(d.Detail, v.Preview.MainSHA) || d.DefaultIndex != d.CancelIndex {
					t.Fatal("confirmation lost full identity or safe default")
				}
				tester := productionIterationTester(s, width, dark)
				for _, visible := range []string{v.Preview.Target.ServiceName, v.Preview.Target.Repository, "origin/master · " + shortProductionSHA(v.Preview.MainSHA), "origin/release/2.3.44", "详细信息"} {
					if !tester.HasText(visible) {
						t.Errorf("missing default summary %q", visible)
					}
				}
				for _, hidden := range []string{v.Preview.MainSHA, "仓库标识：", "项目组：", "synthetic technical commit details"} {
					if tester.HasText(hidden) {
						t.Errorf("technical detail visible by default: %q", hidden)
					}
				}
				captureProductionSummary(t, tester, fmt.Sprintf("confirm-%d-dark-%t-collapsed", width, dark))
				if err := tester.Click("详细信息"); err != nil {
					t.Fatal(err)
				}
				if !tester.HasText(v.Preview.MainSHA) || !tester.HasText("synthetic technical commit details") {
					t.Fatal("expanded detail lost full original content")
				}
				for _, button := range []string{"继续", "取消"} {
					r, ok := tester.Find(button)
					if !ok || r.Y < 0 || r.Y+r.H > 900 {
						t.Fatalf("details displaced footer button %s: %+v", button, r)
					}
				}
				captureProductionSummary(t, tester, fmt.Sprintf("confirm-%d-dark-%t-expanded", width, dark))
				text, ok := tester.Find(d.Detail)
				if !ok {
					t.Fatal("expanded technical detail is not selectable text")
				}
				tester.ClickAt(text.X+12, text.Y+7)
				tester.Key(ui.Cmd, ui.KeyA)
				tester.Key(ui.Cmd, ui.KeyC)
				if tester.Clipboard() != d.Detail {
					t.Fatal("expanded detail cannot be copied in full")
				}
				tester.Key(0, ui.KeyEscape)
				if s.confirmationDialog != nil {
					t.Fatal("Esc retained production confirmation")
				}
				s.confirmNative("Other confirmation", "Other action", "legacy immediately visible detail", nil)
				tester.Frame()
				if !tester.HasText("legacy immediately visible detail") || tester.HasText("详细信息") {
					t.Fatal("legacy confirmation presentation changed")
				}
			})
		}
	}
}

func TestProductionUnknownRelationsDoNotClaimIncluded(t *testing.T) {
	for _, mode := range []string{"no-preview", "incomplete", "needs-merge", "busy"} {
		t.Run(mode, func(t *testing.T) {
			s, v, _ := productionFixture(t)
			v.Preview.ProductionInMain, v.Preview.MainInRelease = false, false
			switch mode {
			case "no-preview":
				v.Preview = nil
			case "incomplete":
				v.Preview.ProductionInMain, v.Preview.MainInRelease = true, true
				v.Preview.MainSHA = ""
			case "busy":
				v.Busy = true
			}
			tester := productionIterationTester(s, 960, false)
			if tester.HasText("无需合并") {
				t.Fatal("unknown relation was shown as included")
			}
			if _, ok := tester.Find("合并生产 Tag 到 master"); !ok {
				t.Fatal("merge action missing for unresolved relation")
			}
		})
	}
}
