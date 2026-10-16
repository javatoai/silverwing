package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
	"strings"
	"testing"
)

func addedDiffPreviewFixture(text string) *workspace.WorkspaceGitFilePreview {
	content := &workspace.WorkspaceFilePreviewContent{Text: text, Origin: workspace.PreviewOriginWorktree}
	comparison := &workspace.WorkspaceFileComparison{NewContent: content}
	for index, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		comparison.Rows = append(comparison.Rows, workspace.WorkspaceFileComparisonRow{NewLine: &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewAdded, LineNumber: index + 1, Text: line}})
	}
	return &workspace.WorkspaceGitFilePreview{Language: workspace.FileLanguageJava, Content: content, Comparison: comparison}
}
func assertAddedPreviewControlsHidden(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for _, label := range []string{"左右对比", "文件内容", "仅看改动", "查看全部", "上一处", "下一处", "1 / 1", "+3", "−0", "工作区（当前）"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("added file retained %q: %q", label, tt.Texts())
		}
	}
}
func TestWorkspaceAddedPreviewHidesControlsInNativeAndRows(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("rows-%v-dark-%v", fallback, dark), func(t *testing.T) {
				source := "package fixture\n\t// 中文新增\nvar value = 1\n"
				preview := addedDiffPreviewFixture(source)
				if fallback {
					preview = addedDiffPreviewFixture(source + strings.Repeat("// long\n", nativeDiffMaxLines+1))
				}
				state := &workspaceHistoryState{TaskSessionKey: "fixture", SelectedPath: "new.java", SelectedKind: domain.WorkspaceGitAdded, Preview: preview, PreviewMode: workspace.PreviewContent, ChangedLinesOnly: true, SplitRatio: .37}
				backend := &headerSyncFake{}
				s := &app{backend: backend}
				tt := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Children(func() { s.renderWorkspaceFilePreview(c, p, state, preview) })
				}, 900, 500)
				assertAddedPreviewControlsHidden(t, tt)
				if state.PreviewMode != workspace.PreviewContent || !state.ChangedLinesOnly || math.Abs(float64(state.SplitRatio-.37)) > .001 {
					t.Fatal("added preview changed modified-file preferences")
				}
				if fallback {
					if state.NativeDiff != nil || state.DiffRows == nil || !state.DiffRows.singlePane || state.DiffRows.changedOnly || state.DiffRows.newScroll.Y != 0 {
						t.Fatal("added fallback routing or first line")
					}
					if err := tt.Click("var value = 1"); err != nil {
						t.Fatal(err)
					}
					tt.Command("selectAll")
					tt.Command("copy")
					if tt.Clipboard() != "var value = 1" {
						t.Fatalf("row copy %q", tt.Clipboard())
					}
					first, ok := tt.Find("package fixture")
					if !ok || first.Y > 8 {
						t.Fatalf("fallback left toolbar space %+v", first)
					}
				} else {
					if state.NativeDiff == nil || !state.NativeDiff.singlePane || state.NativeDiff.view.Layout != editor.DiffUnified || state.NativeDiff.view.ScrollPosition().Y != 0 {
						t.Fatal("native added routing or first line")
					}
					if state.NativeDiff.view.New.Language() == nil {
						t.Fatal("syntax language lost")
					}
					tt.ClickAt(200, 12)
					tt.Command("selectAll")
					tt.Command("copy")
					if tt.Clipboard() != source {
						t.Fatalf("native source copy %q", tt.Clipboard())
					}
				}
				if len(backend.calls) != 0 {
					t.Fatal("render/hide queried backend")
				}
				if out := os.Getenv("SILVERWING_ADDED_PREVIEW_EVIDENCE"); out != "" {
					f, err := os.Create(filepath.Join(out, fmt.Sprintf("added-rows-%v-dark-%v.png", fallback, dark)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, tt.Image())
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
func TestWorkspaceAddedPreviewSwitchRestoresModifiedReading(t *testing.T) {
	modified := addedDiffPreviewFixture("package fixture\nvar value = 2\n")
	modified.Comparison.OldContent = &workspace.WorkspaceFilePreviewContent{Text: "package fixture\nvar value = 1\n"}
	modified.Comparison.Rows[0].OldLine = &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewContext, LineNumber: 1, Text: "package fixture"}
	modified.Comparison.Rows[0].NewLine.Kind = workspace.PreviewContext
	modified.Comparison.Rows[1].OldLine = &workspace.WorkspaceFileComparisonLine{Kind: workspace.PreviewDeleted, LineNumber: 2, Text: "var value = 1"}
	added := addedDiffPreviewFixture("package fixture\nvar value = 1\n")
	state := &workspaceHistoryState{TaskSessionKey: "fixture", SelectedPath: "modified.java", SelectedKind: domain.WorkspaceGitModified, Preview: modified, PreviewMode: workspace.PreviewComparison, SplitRatio: .37}
	backend := &headerSyncFake{}
	s := &app{backend: backend}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview) })
	}, 900, 500)
	for _, label := range []string{"左右对比", "文件内容", "仅看改动", "上一处", "下一处"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("modified missing %q", label)
		}
	}
	state.DiffHunkIndex = 0
	state.DiffHunkSelected = true
	before := state.SplitRatio
	state.SelectedPath = "added.java"
	state.SelectedKind = domain.WorkspaceGitAdded
	state.Preview = added
	tt.Frame()
	assertAddedPreviewControlsHidden(t, tt)
	state.NativeDiff.view.SetScrollPosition(editor.DiffScrollPosition{Y: 10})
	state.saveActiveWorkspaceDiffRenderer()
	state.SelectedPath = "modified.java"
	state.SelectedKind = domain.WorkspaceGitModified
	state.Preview = modified
	tt.Frame()
	if state.NativeDiff == nil || state.NativeDiff.view.Layout != editor.DiffSplit || state.SplitRatio != before || !state.DiffHunkSelected || state.DiffHunkIndex != 0 {
		t.Fatal("modified split/navigation not restored")
	}
	state.ChangedLinesOnly = true
	state.PreviewMode = workspace.PreviewContent
	state.SelectedPath = "added.java"
	state.SelectedKind = domain.WorkspaceGitAdded
	state.Preview = added
	tt.Frame()
	assertAddedPreviewControlsHidden(t, tt)
	if !state.ChangedLinesOnly || state.PreviewMode != workspace.PreviewContent {
		t.Fatal("added reset content/filter preference")
	}
	state.SelectedPath = "modified.java"
	state.SelectedKind = domain.WorkspaceGitModified
	state.Preview = modified
	tt.Frame()
	if !state.ChangedLinesOnly || state.PreviewMode != workspace.PreviewContent {
		t.Fatal("modified preference not retained")
	}
	if len(backend.calls) != 0 {
		t.Fatal("switch issued Git query")
	}
}
func TestWorkspaceEmptyOldOrUnavailableDoesNotInferAdded(t *testing.T) {
	for _, kind := range []domain.WorkspaceGitFileChangeKind{domain.WorkspaceGitModified, ""} {
		preview := addedDiffPreviewFixture("var value = 1\n")
		state := &workspaceHistoryState{TaskSessionKey: "fixture", SelectedPath: "modified.java", SelectedKind: kind, Preview: preview, PreviewMode: workspace.PreviewComparison}
		s := &app{}
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, preview) })
		}, 900, 500)
		if _, ok := tt.Find("左右对比"); !ok {
			t.Fatal("missing old treated as added")
		}
		preview.Comparison.OldContent = &workspace.WorkspaceFilePreviewContent{}
		tt.Frame()
		if _, ok := tt.Find("左右对比"); !ok {
			t.Fatal("empty old treated as added")
		}
	}
	preview := &workspace.WorkspaceGitFilePreview{UnavailableReason: "二进制文件无法预览"}
	state := &workspaceHistoryState{SelectedPath: "binary.bin", SelectedKind: domain.WorkspaceGitAdded, Preview: preview}
	s := &app{}
	tt := ui.NewTester(func(c *ui.Context) { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, preview) }, 900, 500)
	if !tt.HasText(preview.UnavailableReason) || state.NativeDiff != nil || state.DiffRows != nil || state.ContentPreview.view != nil {
		t.Fatal("unavailable added synthesized code")
	}
	assertAddedPreviewControlsHidden(t, tt)
}

func TestWorkspaceAddedPreviewKeepsPerFileReading(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			lines := 200
			if fallback {
				lines = nativeDiffMaxLines + 1
			}
			added := addedDiffPreviewFixture("package fixture\n" + strings.Repeat("// reading position\n", lines))
			modified := addedDiffPreviewFixture("package fixture\nvar value = 2\n")
			modified.Comparison.OldContent = &workspace.WorkspaceFilePreviewContent{Text: "package fixture\nvar value = 1\n"}
			state := &workspaceHistoryState{TaskSessionKey: "scroll-fixture", SelectedPath: "added.java", SelectedKind: domain.WorkspaceGitAdded, Preview: added, PreviewMode: workspace.PreviewComparison, SplitRatio: .37}
			s := &app{}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview) })
			}, 900, 400)
			if fallback {
				state.DiffRows.newScroll.Y = 480
				state.DiffRows.oldScroll.Y = 480
				state.DiffRows.lastSharedY = 480
			} else {
				state.NativeDiff.view.SetScrollPosition(editor.DiffScrollPosition{Y: 480})
			}
			tt.Frame()
			state.saveActiveWorkspaceDiffRenderer()
			state.SelectedPath = "modified.java"
			state.SelectedKind = domain.WorkspaceGitModified
			state.Preview = modified
			tt.Frame()
			state.SelectedPath = "added.java"
			state.SelectedKind = domain.WorkspaceGitAdded
			state.Preview = added
			tt.Frame()
			var y float64
			if fallback {
				y = float64(state.DiffRows.newScroll.Y)
			} else {
				y = state.NativeDiff.view.ScrollPosition().Y
			}
			if math.Abs(y-480) > 1 {
				t.Fatalf("added reading position lost: %v", y)
			}
			assertAddedPreviewControlsHidden(t, tt)
		})
	}
}
