package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func reviewRendererTestState(fallback bool) *workspaceHistoryState {
	oldLines, newLines := make([]string, 240), make([]string, 240)
	rows := make([]workspace.WorkspaceFileComparisonRow, 240)
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("line_%03d %s", i, strings.Repeat("long_source_", 20))
		newLines[i] = oldLines[i]
		kindOld, kindNew := workspace.PreviewContext, workspace.PreviewContext
		if i == 120 {
			newLines[i] += " changed"
			kindOld, kindNew = workspace.PreviewDeleted, workspace.PreviewAdded
		}
		rows[i] = workspace.WorkspaceFileComparisonRow{
			OldLine: &workspace.WorkspaceFileComparisonLine{LineNumber: i + 1, Text: oldLines[i], Kind: kindOld},
			NewLine: &workspace.WorkspaceFileComparisonLine{LineNumber: i + 1, Text: newLines[i], Kind: kindNew},
		}
	}
	separator := "\n"
	if fallback {
		separator = "\r\n"
	}
	oldContent := &workspace.WorkspaceFilePreviewContent{Text: strings.Join(oldLines, separator), Origin: workspace.PreviewOriginMainline}
	newContent := &workspace.WorkspaceFilePreviewContent{Text: strings.Join(newLines, separator), Origin: workspace.PreviewOriginHead}
	return &workspaceHistoryState{
		CompareMode: workspaceReviewMainline, TaskSessionKey: "task-scope", SelectedPath: "first.java", SelectedKind: domain.WorkspaceGitModified, SplitRatio: .5,
		Preview: &workspace.WorkspaceGitFilePreview{Language: workspace.FileLanguageJava, Content: newContent,
			Comparison: &workspace.WorkspaceFileComparison{OldContent: oldContent, NewContent: newContent, Rows: rows}},
	}
}

func TestWorkspaceReviewRenderersKeepDividerAndReadingPosition(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback_%v", fallback), func(t *testing.T) {
			state := reviewRendererTestState(fallback)
			s := &app{}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(8).Children(func() { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview) })
			}, 1100, 720)
			if !fallback && (state.NativeDiff == nil || state.NativeDiff.view.ScrollPosition().Y <= 0) {
				t.Fatal("native preview did not start at its first changed hunk")
			}
			if fallback && (state.DiffRows == nil || state.DiffRows.newScroll.Y <= 0) {
				t.Fatal("fallback preview did not start at its first changed hunk")
			}
			divider, found := tester.Find("Divider")
			if !found {
				t.Fatal("modified file has no draggable divider")
			}
			x, y := divider.X+divider.W/2, divider.Y+divider.H/2
			tester.Press(x, y)
			tester.Move(x+110, y)
			tester.Release(x+110, y)
			tester.Frame()
			if state.SplitRatio < .55 {
				t.Fatalf("workspace divider did not retain dragging: %v", state.SplitRatio)
			}
			ratio := state.SplitRatio
			tester.Frame()
			if math.Abs(float64(state.SplitRatio-ratio)) > .002 {
				t.Fatal("workspace divider snapped back after rebuild")
			}
			if fallback {
				state.DiffRows.oldScroll.X, state.DiffRows.newScroll.X = 20, 70
				state.DiffRows.oldScroll.Y, state.DiffRows.newScroll.Y, state.DiffRows.lastSharedY = 400, 400, 400
			} else {
				state.NativeDiff.view.SetScrollPosition(editor.DiffScrollPosition{OldX: 20, NewX: 70, Y: 400})
			}
			tester.Frame()
			state.resetWorkspaceDiffView()
			state.DiffRows = nil
			state.SelectedPath = "second.java"
			tester.Frame()
			state.resetWorkspaceDiffView()
			state.DiffRows = nil
			state.SelectedPath = "first.java"
			tester.Frame()
			oldX, newX, restoredY := float32(0), float32(0), float64(0)
			if fallback {
				oldX, newX, restoredY = state.DiffRows.oldScroll.X, state.DiffRows.newScroll.X, float64(state.DiffRows.newScroll.Y)
			} else {
				position := state.NativeDiff.view.ScrollPosition()
				oldX, newX, restoredY = position.OldX, position.NewX, position.Y
			}
			if math.Abs(restoredY-400) > 1 || oldX != 20 || newX != 70 {
				t.Fatalf("file reading position was not restored: oldX=%v newX=%v y=%v", oldX, newX, restoredY)
			}
		})
	}
}

func TestWorkspaceReviewAddedFileUsesOnePaneWithoutLosingSplitRatio(t *testing.T) {
	state := reviewRendererTestState(false)
	state.SelectedKind = domain.WorkspaceGitAdded
	state.Preview.Comparison.OldContent = nil
	state.SplitRatio = .7
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview) })
	}, 1100, 720)
	if _, found := tester.Find("Divider"); found {
		t.Fatal("added file still contains the empty old pane")
	}
	if state.SplitRatio != .7 {
		t.Fatal("single-pane file discarded the saved split ratio")
	}
	if state.NativeDiff == nil || state.NativeDiff.view.ScrollPosition().Y != 0 {
		t.Fatal("added file should start at its first line")
	}
	state.NativeDiff.view.SetScrollPosition(editor.DiffScrollPosition{NewX: 35, Y: 400})
	tester.Frame()
	state.resetWorkspaceDiffView()
	state.SelectedPath = "other-added.java"
	tester.Frame()
	state.resetWorkspaceDiffView()
	state.SelectedPath = "first.java"
	tester.Frame()
	if position := state.NativeDiff.view.ScrollPosition(); math.Abs(position.Y-400) > 1 || position.NewX != 35 {
		t.Fatalf("single-pane file reading position was not restored: %+v", position)
	}
	state.resetWorkspaceDiffView()
	state.SelectedKind = domain.WorkspaceGitModified
	state.Preview.Comparison.OldContent = &workspace.WorkspaceFilePreviewContent{Text: "", Origin: workspace.PreviewOriginMainline}
	tester.Frame()
	if _, found := tester.Find("Divider"); !found {
		t.Fatal("an existing empty file was incorrectly treated as a new file")
	}
}
