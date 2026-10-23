package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionExactText(tester *ui.Tester, text string) bool {
	for _, actual := range tester.Texts() {
		if actual == text {
			return true
		}
	}
	return false
}

func productionCardsTester(s *app, width int, dark bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.MenuScope(c, s.popupMenuScope())
		ui.Row(c).Fill().Background(p.bg).AlignItems(ui.Stretch).Children(func() {
			s.sidebar(c, p, width < 1180)
			s.renderProductionPage(c, p)
		})
		s.renderProductionWorktreesDialog(c, p)
		s.renderProductionBaselineDialog(c, p)
		s.renderConfirmationDialog(c, p)
		ui.ThemedMenus(c, s.popupMenuScope())
	}, width, 1400)
}

var productionInitializationCardTitles = []string{
	"生产 Tag → 主分支",
	"创建 release 分支",
}

func TestProductionCardsGroupStatusesAndRightAlignedActions(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, _, b := productionFixture(t)
				b.Tags = []production.Tag{{Name: "2.3.44", PushedAt: "2026-10-23T00:00:00Z", Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
				b.RetainedWorktrees = []string{"C:/synthetic/retained"}
				s.production.batches[0] = b
				tester := productionCardsTester(s, width, dark)
				var previous ui.Rect
				for _, title := range append(append([]string{}, productionInitializationCardTitles...), "合并分支", "构建生产 Tag") {
					card, found := tester.Find(title + "卡片")
					status, hasStatus := tester.Find(title + "状态")
					header, hasHeader := tester.Find(title + "标题行")
					if !found || !hasStatus || !hasHeader || card.X < 0 || card.X+card.W > float32(width) || math.Abs(float64(status.X+status.W-(card.X+card.W-17))) > 1 || math.Abs(float64(status.Y+status.H/2-(header.Y+header.H/2))) > 1 {
						t.Fatalf("status is not in card top right: %s card=%+v status=%+v", title, card, status)
					}
					if previous.W > 0 && (card.X != previous.X || card.W != previous.W || card.Y-previous.Y-previous.H != 16) {
						t.Fatalf("cards must stack with equal widths and gaps: previous=%+v card=%+v", previous, card)
					}
					previous = card
					button := map[string]string{productionInitializationCardTitles[0]: "无需合并", "合并分支": "合并到 release", "构建生产 Tag": "构建 Tag"}[title]
					if button == "" {
						if _, exists := tester.Find(title + "操作栏"); exists {
							t.Fatal("completed step kept an empty action bar")
						}
						continue
					}
					action, ok := tester.Find(button)
					if !ok || action.W > 220 || action.H != 34 || math.Abs(float64(action.X+action.W-(card.X+card.W-17))) > 1 || math.Abs(float64(action.Y+action.H-(card.Y+card.H-17))) > 1 {
						t.Fatalf("primary action is not at card bottom right: %s %+v / %+v", button, action, card)
					}
				}
				refresh := productionAlignmentRect(t, tester, "刷新构建状态")
				build := productionAlignmentRect(t, tester, "构建 Tag")
				if refresh.Y != build.Y || refresh.H != build.H || build.X-refresh.X-refresh.W != 8 {
					t.Fatalf("refresh must directly precede build in its footer: %+v %+v", refresh, build)
				}
				if tester.HasText("批次初始化") {
					t.Fatal("old initialization wrapper remains")
				}
				if tester.HasText(b.RetainedWorktrees[0]) || tester.HasText("保留的工作区 · 1 个") || tester.HasText("最近 Tag") || tester.HasText("构建 构建中") {
					t.Fatal("main content still exposes recovery paths or duplicate Tag labels")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("cards-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestProductionInitializationFooterFollowsOnlyCurrentOperation(t *testing.T) {
	s, v, _ := productionFixture(t)
	s.production.batches, v.BatchID = nil, ""
	v.Preview.ProductionInMain = false
	tester := productionCardsTester(s, 960, false)
	if _, ok := tester.Find("合并生产 Tag 到 master"); !ok {
		t.Fatal("missing required main merge action")
	}
	if err := tester.Click("创建 release"); err != nil || s.confirmationDialog != nil {
		t.Fatal("create must remain visible and disabled until main contains production")
	}
	v.Version, v.Preview.ProductionInMain = "release/2.3.91", true
	tester.Frame()
	if _, exists := tester.Find("合并生产 Tag 到 master"); exists || !productionExactText(tester, "无需合并") {
		t.Fatal("included main must replace its merge action with a disabled placeholder")
	}
	card, _ := tester.Find("创建 release 分支卡片")
	create, ok := tester.Find("创建 release")
	if !ok || create.X+create.W != card.X+card.W-17 || v.Version != "release/2.3.91" {
		t.Fatal("create footer lost alignment or manual version")
	}
	v.Preview.ProductionInMain = false
	tester.Frame()
	if _, exists := tester.Find("合并生产 Tag 到 master"); !exists || productionExactText(tester, "无需合并") {
		t.Fatal("new inclusion evidence did not restore the required merge action")
	}
}

func TestProductionUninitializedCardsRemainVisibleAndDisabled(t *testing.T) {
	for _, width := range []int{800, 960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"first-read", "needs-main", "create-pending"} {
				t.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, state), func(t *testing.T) {
					s, v, b, backend, _ := productionRequestFixture(t)
					if state == "create-pending" {
						b.InitialSHA, b.ReleaseSHA, b.Phase = "", "", "PAUSED"
						b.Intent = &production.Intent{Kind: "create", Step: "release-create"}
						s.production.batches[0] = b
					} else {
						s.production.batches, v.BatchID = nil, ""
						v.Preview.ProductionInMain = false
						v.Preview.ProductionSHA = strings.Repeat("f", 40)
						v.Preview.Diff = " example.go | 1 +\n 1 file changed, 1 insertion(+)"
						if state == "first-read" {
							v.Preview, v.Busy, v.ActiveStep = nil, true, 1
						}
					}
					tester := productionCardsTester(s, width, dark)
					var previous ui.Rect
					for _, title := range append(append([]string{}, productionInitializationCardTitles...), "合并分支", "构建生产 Tag") {
						card := productionAlignmentRect(t, tester, title+"卡片")
						if previous.W > 0 && (card.X != previous.X || card.W != previous.W || card.Y-previous.Y-previous.H != 16) {
							t.Fatalf("uninitialized cards lost their order or alignment: %+v %+v", previous, card)
						}
						previous = card
					}
					if productionExactText(tester, "可构建") || tester.HasText("1. 对比") || tester.HasText("2. 合并") || tester.HasText("3. 创建") {
						t.Fatal("uninitialized cards show an actionable status or old numbering")
					}
					branch, version, generation := v.Branch, v.Version, v.Generation
					for _, title := range []string{"合并分支", "构建生产 Tag"} {
						card := productionAlignmentRect(t, tester, title+"卡片")
						action := map[string]string{"合并分支": "合并到 release", "构建生产 Tag": "构建 Tag"}[title]
						button := productionAlignmentRect(t, tester, action)
						if button.X+button.W != card.X+card.W-17 || button.Y+button.H != card.Y+card.H-17 {
							t.Fatalf("disabled action escaped the card footer: %+v %+v", button, card)
						}
					}
					// The first card has another diff action; click the merge card's
					// footer by position to check the intended disabled control.
					footer := productionAlignmentRect(t, tester, "合并分支操作栏")
					merge := productionAlignmentRect(t, tester, "合并到 release")
					tester.ClickAt(merge.X-24, footer.Y+footer.H/2)
					for _, action := range []string{"合并到 release", "构建 Tag", "待合并的单个远端分支"} {
						if err := tester.Click(action); err != nil {
							t.Fatal(err)
						}
						if tester.Focused(action) {
							t.Fatalf("disabled control received focus: %s", action)
						}
					}
					if len(backend.calls) != 0 || v.Generation != generation || s.confirmationDialog != nil || v.Branch != branch || v.Version != version {
						t.Fatal("disabled controls changed drafts or dispatched an operation")
					}
					for i := 0; i < 20; i++ {
						tester.Key(0, ui.KeyTab)
						if tester.Focused("合并到 release") || tester.Focused("构建 Tag") || tester.Focused("待合并的单个远端分支") {
							t.Fatal("disabled release controls entered keyboard navigation")
						}
					}
					captureProductionSummary(t, tester, fmt.Sprintf("waiting-release-%d-dark-%t-%s", width, dark, state))
				})
			}
		}
	}
}

func TestProductionTagRecordsCopyAndHistoryRemainScoped(t *testing.T) {
	s, v, b := productionFixture(t)
	failureReason := "synthetic compilation failed"
	b.Tags = []production.Tag{
		{Name: "2.3.44", Commit: strings.Repeat("a", 40), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed, FailureReason: &failureReason}},
		{Name: "2.3.44.1", Commit: strings.Repeat("a", 40), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}},
	}
	s.production.batches[0] = b
	tester := productionCardsTester(s, 960, false)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	if tester.HasText("可到平台重新编译此 Tag") || !tester.HasText(failureReason) {
		t.Fatal("redundant rebuild hint is visible or the actual build error was hidden")
	}
	if !productionExactText(tester, "示例服务：2.3.44.1") || !productionExactText(tester, "示例服务：2.3.44") || tester.HasText(shortProductionSHA(b.Tags[0].Commit)) || tester.HasText("历史 Tag") {
		t.Fatal("Tag records must all be visible without a history disclosure or hashes")
	}
	first := productionAlignmentRect(t, tester, "Tag 服务与版本 2.3.44")
	second := productionAlignmentRect(t, tester, "Tag 服务与版本 2.3.44.1")
	if first.Y >= second.Y {
		t.Fatal("Tag records are not in saved build order")
	}
	captureProductionSummary(t, tester, "tag-record-latest")
	v.Busy = true
	tester.Frame()
	if err := tester.Click("复制 Tag 记录 2.3.44.1"); err != nil || tester.Clipboard() != "示例服务：2.3.44.1" || s.lastMessage != "Tag 记录已复制" {
		t.Fatalf("copy must remain available during refresh: %v %q", err, tester.Clipboard())
	}
	if !tester.HasText("构建失败") || !tester.HasText("构建中") {
		t.Fatal("individual Tag statuses were mixed")
	}
	captureProductionSummary(t, tester, "tag-record-history")
	if err := tester.Click("复制 Tag 记录 2.3.44"); err != nil || tester.Clipboard() != "示例服务：2.3.44" {
		t.Fatalf("wrong historical copy: %v %q", err, tester.Clipboard())
	}
	v.Busy = false
	b.Tags = append(b.Tags, production.Tag{Name: "2.3.44.2", Commit: b.Tags[0].Commit})
	s.production.batches[0] = b
	tester.Frame()
	if !tester.HasText("示例服务：2.3.44.2") || !tester.HasText("构建状态待查询") {
		t.Fatal("saved push was hidden until a platform build result")
	}
	third := productionAlignmentRect(t, tester, "Tag 服务与版本 2.3.44.2")
	if third.Y <= second.Y || !productionExactText(tester, "示例服务：2.3.44") {
		t.Fatal("new Tag did not append below existing records")
	}
	b.Ended = true
	s.production.batches[0] = b
	tester.Frame()
	if err := tester.Click("复制 Tag 记录 2.3.44.2"); err != nil || tester.Clipboard() != "示例服务：2.3.44.2" {
		t.Fatalf("ended batch lost record copy: %v", err)
	}
	v.BatchID = "different-batch"
	if s.productionDisplayedBatchCurrent(v, b) {
		t.Fatal("old copy context survived a batch switch")
	}
}

func TestProductionWorktreesMenuDialogAndStaleContext(t *testing.T) {
	for _, change := range []string{"escape", "batch", "service", "home", "config", "removed"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionFixture(t)
			path := "C:/synthetic/保留目录/" + strings.Repeat("long-path-", 24)
			b.RetainedWorktrees = []string{path, "C:/synthetic/second"}
			s.production.batches[0] = b
			tester := productionCardsTester(s, 960, true)
			if err := tester.Click("更多生产发布操作"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("保留的工作区 · 2 个"); err != nil {
				t.Fatal(err)
			}
			d := s.production.worktreesDialog
			if d == nil || !tester.HasText(path) || tester.HasText("删除工作区") {
				t.Fatal("missing read-only scoped worktree dialog")
			}
			if err := tester.Click("复制保留工作区路径 1"); err != nil || tester.Clipboard() != path {
				t.Fatalf("path copy changed bytes: %v %q", err, tester.Clipboard())
			}
			if change == "escape" {
				captureProductionSummary(t, tester, "retained-worktrees-dialog")
			}
			switch change {
			case "escape":
				tester.Key(0, ui.KeyEscape)
			case "batch":
				other := b
				other.ID, other.Ended = "other", true
				s.production.batches = append(s.production.batches, other)
				v.selectProductionBatch(other.ID)
			case "service":
				s.production.selected = "other"
			case "home":
				s.snapshot.Home += "-other"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "removed":
				s.production.batches = nil
			}
			tester.Frame()
			if s.production.worktreesDialog != nil {
				t.Fatal("dialog remained after cancellation or identity change")
			}
		})
	}
}

func TestProductionNavigationStaysInBottomGroup(t *testing.T) {
	for _, tools := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			for _, compact := range []bool{false, true} {
				s, _, _ := productionFixture(t)
				s.configJSON = fmt.Sprintf(`{"tagEnabled":%t,"toolVisibility":{"json":%t,"dateTime":%t,"textDiff":%t,"killPort":false}}`, enabled, tools, tools, tools)
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { s.sidebar(c, setTheme(c, false), compact) })
				}, 240, 900)
				production, shown := tester.Find("生产发布")
				if shown != enabled {
					t.Fatal("global Tag gate changed")
				}
				if !enabled {
					continue
				}
				services, _ := tester.Find("服务")
				if production.Y < 600 || production.Y+production.H >= services.Y {
					t.Fatalf("production did not move above services in lower group: %+v %+v", production, services)
				}
				if tools {
					tool, ok := tester.Find("研发工具")
					if !ok || production.Y-tool.Y-tool.H != 4 {
						t.Fatal("production is not immediately below developer tools")
					}
				}
			}
		}
	}
}

func TestProductionRecordRefreshKeepsExactBatchAndNoWrite(t *testing.T) {
	for _, state := range []string{"active", "pending-sync", "pending-tag", "ended"} {
		t.Run(state, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			b.Tags = []production.Tag{{Name: "2.3.44"}}
			cardTitle := "构建生产 Tag"
			switch state {
			case "pending-sync":
				b = productionConflictBatch(b, "release-sync")
			case "pending-tag":
				b.Phase, b.Intent = "PAUSED", &production.Intent{Kind: "tag", Step: "tag", Tag: "2.3.44.1"}
			case "ended":
				b.Ended, b.Phase = true, "ENDED"
				cardTitle = "生产 Tag 记录"
			}
			s.production.batches[0] = b
			tester := productionCardsTester(s, 1440, false)
			footer := productionAlignmentRect(t, tester, cardTitle+"操作栏")
			refresh := productionAlignmentRect(t, tester, "刷新构建状态")
			records := productionAlignmentRect(t, tester, "Tag 记录")
			if refresh.Y < footer.Y || refresh.Y+refresh.H > footer.Y+footer.H || refresh.Y < records.Y+records.H {
				t.Fatal("refresh is not in the current card footer")
			}
			v.Busy = true
			tester.Frame()
			generation := v.Generation
			if err := tester.Click("刷新构建状态"); err != nil || v.Generation != generation {
				t.Fatalf("busy refresh bypassed operation exclusion: %v", err)
			}
			v.Busy = false
			tester.Frame()
			if err := tester.Click("刷新构建状态"); err != nil {
				t.Fatal(err)
			}
			if call := productionTestCall(t, backend); call.Kind != "production.refresh-build" || call.Payload["batchId"] != b.ID {
				t.Fatalf("record action dispatched wrong request: %+v", call)
			}
			b.Tags[0].Status.Build = domain.GenbuSuccess
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
			productionTestUpdate(t, window)
			tester.Frame()
			if !tester.HasText("构建成功") || s.confirmationDialog != nil || len(backend.calls) != 0 {
				t.Fatal("refresh failed to update the record or started an extra operation")
			}
			captureProductionSummary(t, tester, "refresh-footer-"+state)
		})
	}
}

func TestProductionCardRefreshRejectsStaleContextAndHidesWithoutTags(t *testing.T) {
	for _, change := range []string{"no-tags", "service", "batch", "home", "config", "target", "removed"} {
		t.Run(change, func(t *testing.T) {
			s, v, b, backend, _ := productionRequestFixture(t)
			b.Tags = []production.Tag{{Name: "2.3.44"}}
			s.production.batches[0] = b
			switch change {
			case "no-tags":
				b.Tags = nil
			case "service":
				s.production.selected = "other"
			case "batch":
				v.BatchID = "other"
			case "home":
				s.snapshot.Home += "-other"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "target":
				s.production.batches[0].Target.Repository = "C:/synthetic/other"
			case "removed":
				s.production.batches = nil
			}
			tester := ui.NewTester(func(c *ui.Context) { s.renderProductionBuildRefresh(c, setTheme(c, false), v, b) }, 500, 120)
			if change == "no-tags" {
				if tester.HasText("刷新构建状态") {
					t.Fatal("refresh is visible without a saved Tag")
				}
				return
			}
			if err := tester.Click("刷新构建状态"); err != nil {
				t.Fatal(err)
			}
			if v.Busy || v.Generation != 0 || len(backend.calls) != 0 {
				t.Fatal("stale footer started a batch request")
			}
		})
	}
}

func TestProductionInitializationCompletionUsesCurrentEvidence(t *testing.T) {
	s, v, b := productionFixture(t)
	v.Steps = map[int]productionStepView{1: {State: "complete"}, 2: {State: "complete"}, 3: {State: "complete"}}
	if status := s.productionInitializationStepStatus(v, &b, 2); status.Text != "已完成" || !strings.Contains(status.Tooltip, v.Preview.ProductionTag) {
		t.Fatal("verified containment lost its completion or tooltip")
	}
	v.Preview.ProductionSHA, v.Preview.ProductionTag, v.Preview.ProductionInMain = strings.Repeat("f", 40), "2.3.45", false
	if s.productionInitializationStepStatus(v, &b, 2).Text == "已完成" {
		t.Fatal("old successful main synchronization overrides a new production commit")
	}
	v.Preview.ProductionInMain, v.Preview.MainSHA = true, ""
	for _, step := range []int{1, 2} {
		if s.productionInitializationStepStatus(v, &b, step).Text == "已完成" {
			t.Fatalf("step %d completed with an unknown target", step)
		}
	}
	if s.productionInitializationStepStatus(v, &b, 3).Text != "已完成" {
		t.Fatal("a new comparison erased the saved release creation result")
	}
	b.InitialSHA = ""
	if s.productionInitializationStepStatus(v, &b, 3).Text == "已完成" {
		t.Fatal("historical presentation completed a new release")
	}
	v.ActiveStep, v.Error = 1, "synthetic read failure"
	if s.productionInitializationStepStatus(v, &b, 1).State != "failed" {
		t.Fatal("first-read error was hidden by the old completion")
	}
}

func TestProductionInitializationRecoveryStaysInOwningCard(t *testing.T) {
	for _, step := range []int{2, 3} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			s, v, b := productionFixture(t)
			if step == 2 {
				b = productionConflictBatch(b, "main")
			} else {
				b.InitialSHA, b.Phase = "", "PAUSED"
				b.Intent = &production.Intent{Kind: "create", Step: "release-create"}
				v.Version = "release/9.9.9"
			}
			s.production.batches[0] = b
			tester := productionCardsTester(s, 960, false)
			title := "生产 Tag → 主分支"
			if step == 3 {
				title = "创建 release 分支"
			}
			card := productionAlignmentRect(t, tester, title+"卡片")
			resume := productionAlignmentRect(t, tester, "校验并继续")
			if resume.Y < card.Y || resume.Y+resume.H > card.Y+card.H-16 || s.productionInitializationStepStatus(v, &b, step).Text == "已完成" {
				t.Fatal("recovery escaped its card or was misreported as complete")
			}
			if step == 3 {
				if !tester.HasText(b.ReleaseBranch) || tester.HasText(v.Version) {
					t.Fatal("release recovery shows an editable or unrelated branch")
				}
				if err := tester.Click("release 分支名"); err != nil {
					t.Fatal(err)
				}
				tester.Key(ui.Cmd, ui.KeyA)
				tester.Type("release/9.9.10")
				if v.Version != "release/9.9.9" {
					t.Fatal("recovery input changed the stored draft")
				}
			}
			captureProductionSummary(t, tester, fmt.Sprintf("initialization-recovery-%d", step))
		})
	}
}
