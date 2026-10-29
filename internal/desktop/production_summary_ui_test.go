package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			for _, state := range []string{"first", "initial", "busy", "error", "completed", "ended"} {
				t.Run(fmt.Sprintf("%d-%t-%s", width, dark, state), func(t *testing.T) {
					s, v, b := productionFixture(t)
					anchor := "生产 Tag → 主分支"
					switch state {
					case "first", "initial", "busy", "error":
						s.releaseUnit.state.production.batches, v.BatchID = nil, ""
						if state == "first" {
							v.Preview = nil
						} else if state == "busy" {
							v.Busy, v.ActiveStep = true, 1
							v.Steps = map[int]productionStepView{1: {State: "running"}}
						} else if state == "error" {
							v.ActiveStep, v.Error = 3, "版本已被占用，请修改版本号。"
						}
					case "ended":
						b.Ended = true
						s.releaseUnit.state.production.batches[0] = b
						anchor = "正在查看已结束批次：" + b.ReleaseBranch
					}
					tester := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.Row(c).Fill().Background(p.bg).AlignItems(ui.Stretch).Children(func() {
							s.sidebar(c, p, true)
							s.renderProductionPage(c, p)
						})
					}, width, 900)
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
						if v.Preview != nil {
							baseline, present := tester.Find("生产基线摘要")
							tag, hasTag := tester.Find(v.Preview.ProductionTag)
							main, hasMain := tester.Find("origin/master")
							if !present || baseline.W > 600 || !hasTag || !hasMain || tag.X != main.X || tag.Y >= main.Y {
								t.Errorf("baseline values are not compact and aligned: baseline=%+v tag=%+v main=%+v", baseline, tag, main)
							}
							if tester.HasText("master 已包含生产 Tag " + v.Preview.ProductionTag + "。") {
								t.Error("included relation still repeats the heading in body copy")
							}
						}
						if tester.HasText("自动计算分支名，可修改版本部分；确认后仅创建并推送该分支。") {
							t.Error("obsolete release instructions are still visible")
						}
						if state == "error" && !tester.HasText(v.Error) {
							t.Error("compact layout hid the validation error")
						}
					}
					captureProductionSummary(t, tester, fmt.Sprintf("summary-%d-dark-%t-%s", width, dark, state))
				})
			}
		}
	}
}

func TestProductionIncludedRelationsDisableMergeButtons(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("initial-%t-busy-%t", initial, busy), func(t *testing.T) {
				s, v, _, backend, _ := productionRequestFixture(t)
				v.Preview.ProductionInMain, v.Preview.MainInRelease, v.Busy = true, true, busy
				if initial {
					s.releaseUnit.state.production.batches, v.BatchID = nil, ""
				}
				tester := productionIterationTester(s, 960, false)
				for _, button := range []string{"合并生产 Tag 到 master", "合并主分支到 release"} {
					if _, exists := tester.Find(button); exists {
						t.Errorf("already-included relation retained button %q", button)
					}
				}
				if s.productionInitializationStepStatus(v, s.productionBatch(v), 2).Text != "已完成" || !productionExactText(tester, "无需合并") {
					t.Fatal("missing completed status or disabled no-merge action")
				}
				generation := v.Generation
				if err := tester.Click("无需合并"); err != nil {
					t.Fatal(err)
				}
				if s.confirmationDialog != nil || v.Generation != generation {
					t.Fatal("disabled no-merge action triggered a confirmation or request")
				}
				select {
				case action := <-backend.calls:
					t.Fatalf("disabled no-merge action dispatched %s", action.Kind)
				default:
				}
				for i := 0; i < 20; i++ {
					tester.Key(0, ui.KeyTab)
					if tester.Focused("生产 Tag → 主分支状态") || tester.Focused("无需合并") {
						t.Fatal("inclusion status or disabled no-merge action entered keyboard focus")
					}
				}
				if initial && !busy {
					// Finish the keyboard probe before testing pointer hover. A focused
					// tooltip can otherwise win when both events share a clock tick.
					tester.ClickAt(1, 1)
					status, found := tester.Find("生产 Tag → 主分支状态")
					if !found {
						t.Fatal("missing inclusion status")
					}
					tester.Move(status.X+status.W/2, status.Y+status.H/2)
					time.Sleep(650 * time.Millisecond)
					tester.Frame()
					if !tester.HasText("master 已包含生产 Tag " + v.Preview.ProductionTag + "。") {
						t.Fatal("inclusion relation was lost from the status tooltip")
					}
					captureProductionSummary(t, tester, "inclusion-tooltip")
					tester.Key(0, ui.KeyEscape)
					if tester.HasText("master 已包含生产 Tag " + v.Preview.ProductionTag + "。") {
						t.Fatal("Esc did not dismiss the inclusion tooltip")
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
				for _, visible := range []string{v.Preview.Target.ServiceName, v.Preview.Target.Repository, "origin/master", "origin/release/2.3.44", "详细信息"} {
					if !tester.HasText(visible) {
						t.Errorf("missing default summary %q", visible)
					}
				}
				for _, hidden := range []string{v.Preview.MainSHA, shortProductionSHA(v.Preview.MainSHA), "确认创建并推送新分支？", "仓库标识：", "项目组：", "synthetic technical commit details"} {
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
				for _, button := range []string{"创建并推送", "取消"} {
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
			if s.productionInitializationStepStatus(v, s.productionBatch(v), 2).Text == "已完成" {
				t.Fatal("unknown relation was shown as included")
			}
			if _, ok := tester.Find("合并生产 Tag 到 master"); !ok {
				t.Fatal("merge action missing for unresolved relation")
			}
		})
	}
}
