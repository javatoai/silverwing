package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestProductionReleaseSuggestionPreservesManualBranch(t *testing.T) {
	v := &productionView{}
	v.updateSuggestedBranch("2.3.44")
	if v.Version != "release/2.3.44" {
		t.Fatal("full release branch was not filled")
	}
	v.updateSuggestedBranch("2.3.45")
	if v.Version != "release/2.3.45" {
		t.Fatal("untouched suggestion did not refresh")
	}
	v.Version = "release/2.3.50"
	v.updateSuggestedBranch("2.3.46")
	if v.Version != "release/2.3.50" {
		t.Fatal("refresh overwrote manual branch")
	}
	v.updateSuggestedBranch("")
	if v.Version != "release/2.3.50" {
		t.Fatal("missing suggestion erased manual branch")
	}
	automatic := &productionView{}
	automatic.updateSuggestedBranch("2.4.38")
	automatic.updateSuggestedBranch("")
	if automatic.Version != "" {
		t.Fatal("unconfirmed baseline retained an automatic suggestion")
	}
}

func TestProductionTagConfirmationMatchesFreshPreview(t *testing.T) {
	for _, change := range []string{"none", "production", "main", "release", "runtime", "candidates", "target"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionFixture(t)
			_ = s
			b.TagPlan = &production.TagPlan{Token: "prepared", Preview: *v.Preview, MainSHA: v.Preview.MainSHA, Commit: v.Preview.ReleaseSHA}
			fresh := *v.Preview
			v.Preview = &fresh
			switch change {
			case "production":
				fresh.ProductionSHA = strings.Repeat("f", 40)
			case "main":
				fresh.MainSHA = strings.Repeat("f", 40)
			case "release":
				fresh.ReleaseSHA = strings.Repeat("f", 40)
			case "runtime":
				fresh.RuntimeAvailable = !fresh.RuntimeAvailable
			case "candidates":
				fresh.ProductionCandidates = []string{"2.3.43", "2.3.42"}
			case "target":
				fresh.Target.Remote = "other"
			}
			if productionTagPlanCurrent(v, &b) != (change == "none") {
				t.Fatal("stale build confirmation remained available")
			}
		})
	}
}

func TestProductionInitializationExposesSeparateMergeAndCreate(t *testing.T) {
	s, v, _ := productionFixture(t)
	s.releaseUnit.state.production.batches = nil
	v.BatchID = ""
	v.Preview.Branches = []string{v.Preview.Target.MainBranch, v.Branch}
	v.Preview.ProductionInMain = false
	v.Preview.ProductionSHA = strings.Repeat("f", 40)
	v.Preview.Diff = " example.go | 1 +\n 1 file changed, 1 insertion(+)"
	s.win = &mygo.Window{}
	tester := productionIterationTester(s, 960, false)
	for _, name := range []string{"合并生产 Tag 到 master", "release 分支名", "查看差异"} {
		if _, ok := tester.Find(name); !ok {
			t.Fatalf("missing explicit operation %s", name)
		}
	}
	if err := tester.Click("合并生产 Tag 到 master"); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog == nil || strings.Contains(s.confirmationDialog.Message, "创建 release") {
		t.Fatal("main merge confirmation still chains release creation")
	}
	s.cancelConfirmationDialog()
	v.Preview.ProductionInMain = true
	tester = productionIterationTester(s, 960, false)
	if err := tester.Click("创建 release"); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog == nil || !strings.Contains(s.confirmationDialog.Detail, "release/2.3.44") {
		t.Fatal("create confirmation did not parse full branch")
	}
}

func TestProductionDiffReadIsIndependentAndRejectsLateResults(t *testing.T) {
	for _, change := range []string{"none", "close", "page", "batch", "config", "task-root", "home", "preview", "service"} {
		t.Run(change, func(t *testing.T) {
			s, v, _ := productionFixture(t)
			s.taskUnit.state.tasks = []task{{id: "selected-task", title: "kept"}}
			s.taskUnit.state.notes[0] = "unsaved draft"
			backend := &productionTestBackend{calls: make(chan application.Action, 2), replies: make(chan productionTestAnswer, 2)}
			window := &refreshTestWindow{updates: make(chan func(), 8)}
			s.backend, s.releaseUnit.state.production.window = backend, window
			s.openProductionDiff(v, "生产 Tag → 主分支", "2.3.43", "origin/master", v.Preview.ProductionSHA, v.Preview.MainSHA, true)
			d := s.releaseUnit.state.production.diffDialog
			action := <-backend.calls
			if action.Kind != "production.diff-files" || action.TaskID != "" || v.Busy {
				t.Fatal("diff adopted task/write request state")
			}
			if action.Payload["target"] != v.Preview.Target || action.Payload["preview"] != nil {
				t.Fatal("diff must send its immutable target without unrelated release status")
			}
			sha := v.Preview.ProductionSHA
			if change == "close" {
				s.closeProductionDiff()
			}
			if change == "page" {
				s.page = "tasks"
			}
			if change == "batch" {
				v.BatchID = "another"
			}
			if change == "config" {
				s.settingsUnit.state.configJSON = `{"tagEnabled":false}`
			}
			if change == "task-root" {
				s.settingsUnit.state.configJSON = `{"taskRoot":"D:/another-task-root"}`
			}
			if change == "home" {
				s.snapshot.Home = "changed"
			}
			if change == "preview" {
				fresh := *v.Preview
				fresh.ProductionSHA = strings.Repeat("f", 40)
				v.Preview = &fresh
			}
			if change == "service" {
				s.releaseUnit.state.production.selected = "other"
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.DiffResult{Files: workspace.CompareFilesResult{BaseCommit: sha, TargetCommit: sha}, Commits: "immutable commits"}}}
			select {
			case apply := <-window.updates:
				apply()
			case <-time.After(time.Second):
				t.Fatal("diff completion missing")
			}
			if (d.Commits == "immutable commits") != (change == "none") {
				t.Fatal("late difference result entered another context")
			}
			if s.taskUnit.state.tasks[0].id != "selected-task" || s.taskUnit.state.notes[0] != "unsaved draft" || v.Busy {
				t.Fatal("diff changed selected task/draft/busy state")
			}
			s.closeProductionDiff()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.releaseUnit.closeProductionJobs(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductionDiffDialogReusesFileViewerAndEsc(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-%t", width, dark), func(t *testing.T) {
				s, v, _ := productionFixture(t)
				s.openProductionDiff(v, "生产 Tag → 主分支", "2.3.43", "origin/master", v.Preview.ProductionSHA, v.Preview.MainSHA, true)
				d := s.releaseUnit.state.production.diffDialog
				r := &d.Review
				r.FilesLoading = false
				r.Files = []workspace.CompareChange{{Path: "src/service.go", Kind: string(domain.WorkspaceGitModified)}}
				r.SelectedPath, r.SelectedKind = "src/service.go", domain.WorkspaceGitModified
				r.BaseCommit, r.TargetCommit = v.Preview.MainSHA, v.Preview.ProductionSHA
				old := &workspace.WorkspaceFilePreviewContent{Text: "package service\n\nconst Version = 1\n", Origin: workspace.PreviewOriginSelectedCommits}
				current := &workspace.WorkspaceFilePreviewContent{Text: "package service\n\nconst Version = 2\n", Origin: workspace.PreviewOriginSelectedCommits}
				r.Preview = &workspace.WorkspaceGitFilePreview{Content: current, Language: workspace.WorkspaceFileLanguageFromPath(r.SelectedPath), Comparison: &workspace.WorkspaceFileComparison{OldContent: old, NewContent: current}}
				r.Preview.Comparison.Rows = []workspace.WorkspaceFileComparisonRow{
					{OldLine: &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewContext, LineNumber: 1, Text: "package service"}, NewLine: &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewContext, LineNumber: 1, Text: "package service"}},
					{OldLine: &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewDeleted, LineNumber: 3, Text: "const Version = 1"}, NewLine: &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewAdded, LineNumber: 3, Text: "const Version = 2"}},
				}
				d.Commits = "> abc123 incoming production change\n< def456 retained main change"
				tester := productionIterationTester(s, width, dark)
				for _, name := range []string{"文件差异", "提交记录", "变更文件", "src/service.go"} {
					if !tester.HasText(name) {
						t.Fatalf("missing shared diff component %s", name)
					}
				}
				if r.NativeDiff == nil && r.DiffRows == nil {
					t.Fatal("shared code comparison renderer was not activated")
				}
				if _, ok := tester.Find("拖动调整文件列表宽度"); !ok {
					t.Fatal("production diff is missing the task review splitter")
				}
				if width == 1440 && !dark {
					var resizedFileListWidth float32
					for _, size := range [][2]int{{2240, 1250}, {960, 680}} {
						tester.SetSize(size[0], size[1])
						panel, ok := tester.Find("Git 差异")
						if !ok || panel.X != 12 || panel.Y != 12 || panel.W != float32(size[0]-24) || panel.H != float32(size[1]-24) {
							t.Fatalf("diff did not follow the window at %v: %+v", size, panel)
						}
						if r.SelectedPath != "src/service.go" {
							t.Fatalf("resizing lost the selected file: %q", r.SelectedPath)
						}
						if resizedFileListWidth == 0 {
							splitter, _ := tester.Find("拖动调整文件列表宽度")
							x, y := splitter.X+splitter.W/2, splitter.Y+splitter.H/2
							tester.Press(x, y)
							tester.Move(x+320, y)
							tester.Release(x+320, y)
							resizedFileListWidth = r.LeftWidth
							if resizedFileListWidth <= 260 {
								t.Fatal("drag did not resize the shared file list")
							}
						} else if r.LeftWidth != resizedFileListWidth {
							t.Fatal("shrinking the dialog overwrote the file list preference")
						}
					}
					if err := tester.Click("收起文件列表"); err != nil || !r.LeftCollapsed {
						t.Fatalf("could not collapse the shared file list: %v", err)
					}
					if err := tester.Click("展开文件列表"); err != nil || r.LeftCollapsed {
						t.Fatalf("could not expand the shared file list: %v", err)
					}
					if err := tester.Click("更多审阅操作"); err != nil {
						t.Fatal(err)
					}
					if err := tester.Click("按相对路径平铺"); err != nil || r.FileView != "flat" {
						t.Fatalf("could not use the shared file view menu: %v", err)
					}
				}
				if dir := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); dir != "" {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("manual-diff-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					if err = png.Encode(f, tester.Image()); err != nil {
						t.Fatal(err)
					}
					if err = f.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if err := tester.Click("提交记录"); err != nil {
					t.Fatal(err)
				}
				if !tester.HasText(d.Commits) {
					t.Fatal("commit view not displayed")
				}
				tester.Key(0, ui.KeyEscape)
				if s.releaseUnit.state.production.diffDialog != nil {
					t.Fatal("Esc left diff dialog open")
				}
			})
		}
	}
}
