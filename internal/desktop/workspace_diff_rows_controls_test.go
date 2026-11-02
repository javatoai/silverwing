package desktop

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func workspaceRowsControlsFixture(count int, changed []int, separator string, padding int) *workspaceHistoryState {
	oldLines, newLines := make([]string, count), make([]string, count)
	rows := make([]workspace.WorkspaceFileComparisonRow, count)
	for index := range rows {
		oldLines[index] = fmt.Sprintf("line_%04d %s", index+1, strings.Repeat("x", padding))
		newLines[index] = oldLines[index]
		oldKind, newKind := workspace.PreviewContext, workspace.PreviewContext
		if slices.Contains(changed, index) {
			oldLines[index] += " old"
			newLines[index] += " new"
			oldKind, newKind = workspace.PreviewDeleted, workspace.PreviewAdded
		}
		rows[index] = workspace.WorkspaceFileComparisonRow{
			OldLine: &workspace.WorkspaceFileComparisonLine{LineNumber: index + 1, Text: oldLines[index], Kind: oldKind},
			NewLine: &workspace.WorkspaceFileComparisonLine{LineNumber: index + 1, Text: newLines[index], Kind: newKind},
		}
	}
	oldContent := &workspace.WorkspaceFilePreviewContent{Text: strings.Join(oldLines, separator), Origin: workspace.PreviewOriginHead}
	newContent := &workspace.WorkspaceFilePreviewContent{Text: strings.Join(newLines, separator), Origin: workspace.PreviewOriginWorktree}
	return &workspaceHistoryState{
		TaskSessionKey: "rows-controls", SelectedPath: "fixture.java", SelectedKind: domain.WorkspaceGitModified,
		PreviewMode: workspace.PreviewComparison, SplitRatio: .5,
		Preview: &workspace.WorkspaceGitFilePreview{Language: workspace.FileLanguageJava, Content: newContent,
			Comparison: &workspace.WorkspaceFileComparison{Rows: rows, OldContent: oldContent, NewContent: newContent}},
	}
}

func workspaceRowsControlsTester(state *workspaceHistoryState, width, height int) *ui.Tester {
	s := &app{}
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().MinWidth(0).MinHeight(0).Children(func() {
			s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview)
		})
	}, width, height)
}

func workspaceRowsVisibleSources(view *workspaceDiffRowsView) (sources []int, gaps int) {
	if view == nil {
		return nil, 0
	}
	for _, row := range view.rows {
		if row.sourceIndex < 0 {
			gaps++
			continue
		}
		sources = append(sources, row.sourceIndex)
	}
	return sources, gaps
}

func TestWorkspaceRowsControlsExistForEveryFallbackRoute(t *testing.T) {
	for _, route := range []string{"crlf", "combined-bytes", "combined-lines", "strict-changes"} {
		t.Run(route, func(t *testing.T) {
			state := workspaceRowsControlsFixture(80, []int{8, 25}, "\r\n", 0)
			switch route {
			case "combined-bytes":
				state = workspaceRowsControlsFixture(80, []int{8, 25}, "\n", 1700)
			case "combined-lines":
				state = workspaceRowsControlsFixture(4100, []int{8, 25}, "\n", 0)
			case "strict-changes":
				state = workspaceRowsControlsFixture(80, []int{8, 25}, "\n", 0)
			}
			tester := workspaceRowsControlsTester(state, 1050, 600)
			if route == "strict-changes" {
				state.ChangedLinesOnly = true
				tester.Frame()
			}
			if state.NativeDiff != nil || state.DiffRows == nil {
				t.Fatal("fixture did not reach the numbered rows renderer")
			}
			for _, text := range []string{"查找", "跳到行", "上一处", "下一处", "1 / 2"} {
				if _, found := tester.Find(text); !found {
					t.Fatalf("fallback route %s has no reachable %q control", route, text)
				}
			}
		})
	}
}

func TestWorkspaceRowsHunkNavigationUsesSourceBlocks(t *testing.T) {
	state := workspaceRowsControlsFixture(140, []int{10, 11, 92}, "\r\n", 0)
	tester := workspaceRowsControlsTester(state, 1000, 500)
	if !tester.HasText("1 / 2") {
		t.Fatalf("consecutive changes must be one block: %v", tester.Texts())
	}
	if err := tester.Click("下一处"); err != nil {
		t.Fatal(err)
	}
	if !state.DiffHunkSelected || state.DiffHunkIndex != 1 || !tester.HasText("2 / 2") {
		t.Fatal("next block did not select the second source block")
	}
	if _, found := tester.Find(state.Preview.Comparison.Rows[92].NewLine.Text); !found {
		t.Fatal("next block did not reveal its original source row")
	}
	if err := tester.Click(state.Preview.Comparison.Rows[92].NewLine.Text); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Alt|ui.Shift, ui.KeyF5)
	if state.DiffHunkIndex != 0 {
		t.Fatal("Shift+Alt+F5 did not navigate from the rows pane")
	}
	tester.Key(ui.Alt, ui.KeyF5)
	if state.DiffHunkIndex != 1 {
		t.Fatal("Alt+F5 did not navigate from the rows pane")
	}
	state.ChangedLinesOnly = true
	tester.Frame()
	sources, gaps := workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Equal(sources, []int{10, 11, 92}) || gaps != 0 || !tester.HasText("2 / 2") {
		t.Fatal("strict projection changed block count, selected block, or included context")
	}
}

func TestWorkspaceRowsFindRevealsCollapsedSourceAndRetainsText(t *testing.T) {
	state := workspaceRowsControlsFixture(180, []int{70, 140}, "\r\n", 0)
	const matchText = "前缀🙂 NEEDLE 中间 needle 末尾"
	for _, sourceIndex := range []int{4, 100} {
		state.Preview.Comparison.Rows[sourceIndex].OldLine.Text = matchText
		state.Preview.Comparison.Rows[sourceIndex].NewLine.Text = matchText
	}
	state.ContextLinesOnly = true
	tester := workspaceRowsControlsTester(state, 1050, 540)
	sources, _ := workspaceRowsVisibleSources(state.DiffRows)
	if slices.Contains(sources, 4) || slices.Contains(sources, 100) {
		t.Fatal("fixture source matches should start inside folded gaps")
	}
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	if !tester.Focused("查找差异文本") {
		t.Fatal("visible find control did not focus its input")
	}
	tester.Type("needle")
	selectedBlock := state.DiffHunkIndex
	tester.Key(ui.Alt, ui.KeyF5)
	if state.DiffHunkIndex != selectedBlock || !tester.Focused("查找差异文本") {
		t.Fatal("change-block shortcuts escaped the editable find input")
	}
	tester.Key(0, ui.KeyEnter)
	sources, _ = workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Contains(sources, 4) || !tester.HasText("1 / 4 处") {
		t.Fatal("find should explicitly reveal the first folded source match")
	}
	tester.Key(0, ui.KeyF3)
	if !tester.HasText("2 / 4 处") {
		t.Fatal("F3 skipped the second occurrence on the same source line")
	}
	tester.Key(0, ui.KeyF3)
	sources, _ = workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Contains(sources, 100) || !tester.HasText("3 / 4 处") {
		t.Fatal("F3 did not reveal a match in a separate folded region")
	}
	tester.Key(ui.Shift, ui.KeyF3)
	if !tester.HasText("2 / 4 处") {
		t.Fatal("Shift+F3 did not return to the previous source occurrence")
	}
	if !state.ContextLinesOnly || state.ChangedLinesOnly {
		t.Fatal("find changed the user's projection preference")
	}
	for _, sourceIndex := range []int{4, 100} {
		if state.Preview.Comparison.Rows[sourceIndex].NewLine.Text != matchText {
			t.Fatal("case-insensitive find changed the displayed source text")
		}
	}
}

func TestWorkspaceRowsGotoPreservesStrictFilterAndExplainsRange(t *testing.T) {
	state := workspaceRowsControlsFixture(100, []int{40, 80}, "\r\n", 0)
	tester := workspaceRowsControlsTester(state, 1000, 500)
	state.ChangedLinesOnly = true
	tester.Frame()
	if err := tester.Click("跳到行"); err != nil {
		t.Fatal(err)
	}
	if !tester.Focused("跳到原始行") {
		t.Fatal("go-to-line did not focus its original-line input")
	}
	tester.Type("5")
	tester.Key(0, ui.KeyEnter)
	if !state.ChangedLinesOnly || !tester.HasText("该行不在仅看改动范围内") {
		t.Fatal("jump to filtered-out source must explain the strict range without changing the filter")
	}
	sources, gaps := workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Equal(sources, []int{40, 80}) || gaps != 0 {
		t.Fatal("go-to-line silently exposed strict-filter context")
	}
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	tester.Type("line_0005")
	tester.Key(0, ui.KeyEnter)
	if !tester.HasText("仅在改动行中查找") || !tester.HasText("0 / 0 处") {
		t.Fatal("strict find must communicate its source range and exclude unchanged hits")
	}
}

func TestWorkspaceRowsShortcutsDoNotEscapeExternalInput(t *testing.T) {
	state := workspaceRowsControlsFixture(140, []int{10, 92}, "\r\n", 0)
	s := &app{}
	draft := "保留输入内容"
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.TextInput(c.Key("outside-diff"), &draft).Label("外部输入").Height(30)
			s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview)
		})
	}, 1000, 540)
	if err := tester.Click("外部输入"); err != nil {
		t.Fatal(err)
	}
	before, selected := state.DiffHunkIndex, state.DiffHunkSelected
	for _, shortcut := range []struct {
		mods ui.Modifiers
		key  ui.Key
	}{{ui.Ctrl, ui.KeyF}, {ui.Ctrl, ui.KeyG}, {0, ui.KeyF3}, {ui.Shift, ui.KeyF3}, {ui.Alt, ui.KeyF5}} {
		tester.Key(shortcut.mods, shortcut.key)
	}
	if state.DiffHunkIndex != before || state.DiffHunkSelected != selected || !tester.Focused("外部输入") {
		t.Fatal("diff shortcuts stole focus or navigation from another editable input")
	}
	if _, found := tester.Find("查找差异文本"); found {
		t.Fatal("Ctrl+F in another input opened the diff's find control")
	}
	if _, found := tester.Find("跳到原始行"); found {
		t.Fatal("Ctrl+G in another input opened the diff's line input")
	}
	if draft != "保留输入内容" {
		t.Fatal("navigation changed unrelated draft text")
	}
}

func TestWorkspaceRowsFindKeepsUnicodeSourceByteRangesAndSides(t *testing.T) {
	state := workspaceRowsControlsFixture(8, []int{2}, "\r\n", 0)
	const source = "前缀🙂 K K k 结尾"
	state.Preview.Comparison.Rows[2].NewLine.Text = source
	tester := workspaceRowsControlsTester(state, 1000, 420)
	view := state.DiffRows
	if !view.find("k", 1) || len(view.controls.matches) != 3 {
		t.Fatal("Unicode case folding should find Kelvin sign and both Latin cases")
	}
	var found []string
	for _, match := range view.controls.matches {
		if match.sourceIndex != 2 || !utf8.ValidString(source[:match.start]) || !utf8.ValidString(source[:match.end]) {
			t.Fatalf("search offsets no longer address original UTF-8 boundaries: %+v", match)
		}
		found = append(found, source[match.start:match.end])
	}
	if !slices.Equal(found, []string{"K", "K", "k"}) {
		t.Fatalf("case folding changed the source ranges: %q", found)
	}
	view.controls.old = true
	if view.find("k", 1) || len(view.controls.matches) != 0 {
		t.Fatal("search crossed into the other version's text")
	}
	view.controls.old = false
	view.openSearch()
	view.find("k", 1)
	tester.Frame()
	var reconstructed strings.Builder
	for _, span := range workspaceDiffRowsMatchSpans(view.sourceRows[2].new.spans, view.controls.matches[0], colors(false)) {
		reconstructed.WriteString(span.Text)
	}
	if reconstructed.String() != source {
		t.Fatal("find highlighting changed or split source text incorrectly")
	}
}

func TestWorkspaceRowsGotoUsesVersionLineNumbersAcrossDeletion(t *testing.T) {
	state := workspaceRowsControlsFixture(8, []int{2}, "\r\n", 0)
	rows := state.Preview.Comparison.Rows
	rows[2].NewLine = nil
	for source := 3; source < len(rows); source++ {
		rows[source].NewLine.LineNumber--
	}
	workspaceRowsControlsTester(state, 1000, 420)
	view := state.DiffRows
	if !view.goToLine(true, 3) || view.sourceRows[2].old.line.LineNumber != 3 {
		t.Fatal("deleted original line cannot be targeted on the old side")
	}
	if !view.goToLine(false, 3) || view.newLineSources[3] != 3 {
		t.Fatal("new-side go-to-line used comparison row numbers after deletion")
	}
	view.setProjection(true, false)
	if view.goToLine(false, 3) || view.controls.message != "该行不在仅看改动范围内" {
		t.Fatal("a context row after deletion bypassed strict filtering")
	}
	if view.goToLine(false, 99) || view.controls.message != "该原始行号不在已加载范围内" {
		t.Fatal("an unavailable original line was treated as loaded")
	}
}

func TestWorkspaceRowsAddedFileKeepsCompleteSinglePaneWithOnDemandFind(t *testing.T) {
	state := workspaceRowsControlsFixture(100, []int{20}, "\r\n", 0)
	state.SelectedKind = domain.WorkspaceGitAdded
	state.ContextLinesOnly, state.ChangedLinesOnly = true, true
	state.Preview.Comparison.OldContent = nil
	for _, row := range state.Preview.Comparison.Rows {
		row.NewLine.Kind = workspace.PreviewAdded
	}
	tester := workspaceRowsControlsTester(state, 1000, 420)
	view := state.DiffRows
	sources, gaps := workspaceRowsVisibleSources(view)
	if len(sources) != 100 || gaps != 0 || !view.singlePane || view.contextOnly || view.changedOnly || view.newScroll.Y != 0 {
		t.Fatal("an added file inherited comparison filters or lost its first line")
	}
	if _, found := tester.Find("Divider"); found {
		t.Fatal("an added file acquired an empty old-side pane")
	}
	if _, found := tester.Find("查找"); found {
		t.Fatal("an added file acquired a permanent comparison toolbar")
	}
	if err := tester.Click(state.Preview.Comparison.Rows[0].NewLine.Text); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd, ui.KeyF)
	if !tester.Focused("查找差异文本") {
		t.Fatal("an added file cannot open find from its single source pane")
	}
	tester.Type("line_0060")
	tester.Key(0, ui.KeyEnter)
	if !tester.HasText("1 / 1 处") || !state.ContextLinesOnly || !state.ChangedLinesOnly {
		t.Fatal("on-demand added-file search lost the user's comparison preferences")
	}
	tester.Key(0, ui.KeyEscape)
	if _, found := tester.Find("查找差异文本"); found {
		t.Fatal("find control did not close back to the single source pane")
	}
}

func TestWorkspaceRowsFindFromSourceRetainsFocusAcrossVirtualizedMatches(t *testing.T) {
	state := workspaceRowsControlsFixture(360, []int{10, 340}, "\r\n", 0)
	for _, source := range []int{10, 130, 300} {
		text := fmt.Sprintf("needle at original row %d", source+1)
		state.Preview.Comparison.Rows[source].OldLine.Text = "old " + text
		state.Preview.Comparison.Rows[source].NewLine.Text = text
	}
	tester := workspaceRowsControlsTester(state, 1050, 500)
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	tester.Type("needle")
	tester.Key(0, ui.KeyEnter)
	tester.Key(0, ui.KeyEscape)
	if err := tester.Click(state.Preview.Comparison.Rows[10].NewLine.Text); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyF3)
	if !tester.HasText("2 / 3 处") {
		t.Fatal("F3 from source did not reach the second offscreen match")
	}
	if _, found := tester.Find(state.Preview.Comparison.Rows[10].NewLine.Text); found {
		t.Fatal("the first source row should now be outside the virtualized viewport")
	}
	tester.Key(0, ui.KeyF3)
	if !tester.HasText("3 / 3 处") {
		t.Fatal("find lost its focus scope after the originating code row left the viewport")
	}
	tester.Key(ui.Shift, ui.KeyF3)
	if !tester.HasText("2 / 3 处") {
		t.Fatal("reverse find lost navigation after another viewport transition")
	}
}
