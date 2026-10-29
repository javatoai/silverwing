package desktop

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestProductionConflictDetailsAndUniformActions(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionFixture(t)
				selectedBranch := v.Branch
				b = productionConflictBatch(b, "feature")
				b.Intent.SourceBranch = "feature/开发者_" + strings.Repeat("very-long-branch-name-", 6)
				b.Intent.Merge.Conflicts = nil
				for i := range 24 {
					b.Intent.Merge.Conflicts = append(b.Intent.Merge.Conflicts, fmt.Sprintf("src/main/java/very/long/service/payment/compensation/%02d_退款冲突处理.java", i))
				}
				b.RetainedWorktrees = []string{"C:/synthetic/old workspaces/" + strings.Repeat("a", 96), "C:/synthetic/old workspaces/second"}
				s.releaseUnit.state.production.batches[0] = b
				s.win = &mygo.Window{}
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.sidebar(c, p, true); s.renderProductionPage(c, p) })
					s.renderConfirmationDialog(c, p)
					s.renderProductionWorktreesDialog(c, p)
				}, width, 1300)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
				if tester.HasText(b.Intent.Merge.Worktree) || tester.HasText(b.RetainedWorktrees[0]) || tester.HasText(b.Intent.Merge.Conflicts[0]) {
					t.Fatal("technical details should start collapsed")
				}
				var buttonColor color.RGBA
				for i, name := range []string{"打开 IDE", "校验并继续", "核对远端变化", "撤回合并"} {
					r, ok := tester.Find(name)
					if !ok || r.H != 34 || r.X < 0 || r.X+r.W > float32(width) {
						t.Fatalf("recovery button missing, inconsistent or overflowing: %s %+v", name, r)
					}
					bg := tester.Image().RGBAAt(int(r.X+8), int(r.Y+8))
					if i == 0 {
						buttonColor = bg
					} else if bg != buttonColor {
						t.Fatalf("recovery buttons use different colors: %s %v != %v", name, bg, buttonColor)
					}
				}
				captureProductionSummary(t, tester, fmt.Sprintf("conflict-details-%d-dark-%t-collapsed", width, dark))
				if err := tester.Click("复制冲突信息"); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(tester.Clipboard(), b.Intent.Merge.Conflicts[23]) || !strings.Contains(tester.Clipboard(), b.Intent.Merge.Worktree) {
					t.Fatal("collapsed details changed the complete copy content")
				}
				if err := tester.Click("冲突详情 · 24 个文件"); err != nil {
					t.Fatal(err)
				}
				files, ok := tester.Find("冲突文件列表")
				if !ok || files.H <= 0 || files.H > 200 || files.X+files.W > float32(width) || !tester.HasText(b.Intent.Merge.Worktree) {
					t.Fatalf("expanded file area is missing or unbounded: %+v", files)
				}
				tester.Move(files.X+files.W/2, files.Y+files.H/2)
				tester.Scroll(files.X+files.W/2, files.Y+files.H/2, 0, 4000)
				last, found := tester.Find(b.Intent.Merge.Conflicts[23])
				files, _ = tester.Find("冲突文件列表")
				if !found || last.Y < files.Y || last.Y+last.H > files.Y+files.H+1 {
					t.Fatalf("cannot scroll to the final conflict file: last=%+v viewport=%+v", last, files)
				}
				copyPath, found := tester.Find("复制工作区路径")
				if !found || copyPath.W > 220 {
					t.Fatalf("workspace copy action stretched across the detail panel: %+v", copyPath)
				}
				captureProductionSummary(t, tester, fmt.Sprintf("conflict-details-%d-dark-%t-expanded", width, dark))
				if err := tester.Click("复制工作区路径"); err != nil {
					t.Fatal(err)
				}
				if tester.Clipboard() != b.Intent.Merge.Worktree {
					r, _ := tester.Find("复制工作区路径")
					t.Fatalf("wrong conflict workspace copied: control=%+v clipboard=%q", r, tester.Clipboard())
				}
				if err := tester.Click("冲突详情 · 24 个文件"); err != nil {
					t.Fatal(err)
				}
				tester.Key(0, ui.KeyEnter)
				if !tester.HasText(b.Intent.Merge.Worktree) {
					t.Fatal("keyboard could not expand conflict details")
				}
				tester.Key(0, ui.KeySpace)
				if tester.HasText(b.Intent.Merge.Worktree) {
					t.Fatal("keyboard could not collapse conflict details")
				}
				s.openProductionWorktrees(v, b)
				tester.Frame()
				if !tester.HasText(b.RetainedWorktrees[0]) || !tester.HasText(b.RetainedWorktrees[1]) {
					t.Fatal("retained workspace paths cannot be inspected")
				}
				if err := tester.Click("复制保留工作区路径 2"); err != nil {
					t.Fatal(err)
				}
				if tester.Clipboard() != b.RetainedWorktrees[1] || s.lastMessage != "工作区路径已复制" {
					t.Fatal("retained workspace copy used the wrong record")
				}
				if v.Branch != selectedBranch {
					t.Fatal("expanding or copying details changed the selected branch")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("conflict-details-%d-dark-%t-retained", width, dark))
			})
		}
	}
}

func TestProductionConflictDisclosureIdentityAndRefresh(t *testing.T) {
	for _, change := range []string{"refresh", "selection", "batch", "merge", "source", "before", "path", "target", "source-ref", "target-ref", "local-branch", "home", "config", "completed"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionFixture(t)
			b = productionConflictBatch(b, "feature")
			s.releaseUnit.state.production.batches[0] = b
			b.RetainedWorktrees = []string{"C:/synthetic/retained"}
			d := s.productionDisclosures(v, &b)
			d.conflictOpen = true
			switch change {
			case "refresh":
				b.UpdatedAt, b.Message, v.Generation = "later", "fresh status", v.Generation+1
			case "selection":
				v.Selection.ServiceID = "other"
			case "batch":
				b.ID = "another batch"
			case "merge":
				b.Intent.Merge.ID = "another merge"
			case "source":
				b.Intent.Merge.SourceSHA = strings.Repeat("f", 40)
			case "before":
				b.Intent.Merge.BeforeSHA = strings.Repeat("f", 40)
			case "path":
				b.Intent.Merge.Worktree += "-other"
			case "target":
				b.Target.Remote = "other"
			case "source-ref":
				b.Intent.SourceBranch = "feature/other"
			case "target-ref":
				b.Intent.Merge.TargetBranch = "release/other"
			case "local-branch":
				b.Intent.Merge.Branch = "sw-production/other"
			case "home":
				s.snapshot.Home += "-other"
			case "config":
				s.settingsUnit.state.configJSON = `{"tagEnabled":false}`
			case "completed":
				b.Intent = nil
			}
			d = s.productionDisclosures(v, &b)
			if d.conflictOpen != (change == "refresh") {
				t.Fatal("expanded details did not follow record identity")
			}
		})
	}
	// Switching away and back is a fresh visible context, even if A's data is cached.
	s, v, b := productionFixture(t)
	b = productionConflictBatch(b, "feature")
	s.productionDisclosures(v, &b).conflictOpen = true
	other := *v
	other.Selection.ServiceID = "other"
	s.productionDisclosures(&other, nil)
	if s.productionDisclosures(v, &b).conflictOpen {
		t.Fatal("A -> B -> A restored expanded details from another visible context")
	}
}

func TestProductionConflictBuildReasonAndFailureFeedback(t *testing.T) {
	for _, phase := range []string{"CONFLICT", "ABORTING"} {
		s, v, b := productionFixture(t)
		b = productionConflictBatch(b, "feature")
		b.Phase, b.Branches = phase, nil
		b.Tags = []production.Tag{{Name: "2.3.44.1", Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
		v.ActiveStep = 4
		v.Steps = map[int]productionStepView{4: {State: "failed", Message: "当前操作失败：需要人工核对"}}
		s.releaseUnit.state.production.batches[0] = b
		want := "请先解决或撤回当前合并。"
		if phase == "ABORTING" {
			want = "请先完成当前合并的撤回核对。"
		}
		if got := s.productionReleaseModel(v, &b); got.BuildReason != want || got.Ready || got.CanBuild {
			t.Fatalf("wrong conflict build state: %+v", got)
		}
		tester := productionIterationTester(s, 960, false)
		if !tester.HasText(want) || !tester.HasText(v.Steps[4].Message) || tester.HasText("请先合并至少一个") {
			t.Fatal("conflict reason or the actual operation failure was hidden")
		}
		if !tester.HasText(productionTagRecordText(b, b.Tags[0])) || !tester.HasText("构建中") {
			t.Fatal("current conflict hid the previous Tag build result")
		}
		v.Busy, v.ActiveAction = true, "production.abort-merge"
		tester.Frame()
		if !tester.HasText("正在撤回合并…") || !tester.HasText("请等待当前合并撤回完成。") {
			t.Fatal("aborting operation still presented as an idle conflict")
		}
	}
}
