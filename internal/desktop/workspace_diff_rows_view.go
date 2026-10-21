package desktop

import (
	"context"
	"math"
	"strconv"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/tools"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

const workspaceDiffRowHeight = 24

type workspaceDiffRowCell struct {
	line  *workspace.WorkspaceFileComparisonLine
	spans []ui.Span
}

type workspaceDiffDisplayRow struct {
	sourceIndex int
	old, new    workspaceDiffRowCell
}

// A row view projects the service's rows; it never runs another line diff.
// Its immutable text, syntax, inline spans and width are built once per input.
type workspaceDiffRowsView struct {
	identity             workspaceDiffIdentity
	changedOnly          bool
	singlePane           bool
	palette              palette
	rows                 []workspaceDiffDisplayRow
	contentWidth         float32
	oldScroll, newScroll ui.ScrollState
	lastSharedY          float32
}

func (state *workspaceHistoryState) workspaceDiffRows(c *ui.Context, p palette, comparison *workspace.WorkspaceFileComparison, changedOnly bool) *workspaceDiffRowsView {
	if state == nil || comparison == nil || state.NativeDiffIdentity == nil {
		return nil
	}
	identity := *state.NativeDiffIdentity // workspaceNativeDiffView binds it first.
	key := workspaceDiffRendererKey(state)
	saved := state.activateWorkspaceDiffRenderer(key)
	singlePane := state.SelectedKind == domain.WorkspaceGitAdded
	view := state.DiffRows
	if view != nil && view.identity.matches(identity) && view.changedOnly == changedOnly && view.singlePane == singlePane && view.palette == p {
		return view
	}
	view = &workspaceDiffRowsView{identity: identity, changedOnly: changedOnly, singlePane: singlePane, palette: p, contentWidth: 80}
	if saved != nil && saved.HasRowsScroll {
		view.oldScroll.X, view.newScroll.X = saved.RowsOldX, saved.RowsNewX
		view.oldScroll.Y, view.newScroll.Y = saved.RowsScrollY, saved.RowsScrollY
		view.lastSharedY = saved.RowsScrollY
	}
	language := workspaceFilePreviewLanguage(identity.path, identity.language)
	theme := textEditorTheme(p)
	oldSyntax := workspaceDiffSourceSyntax(comparison.OldContent, comparison.Rows, true, language, theme, p)
	newSyntax := workspaceDiffSourceSyntax(comparison.NewContent, comparison.Rows, false, language, theme, p)
	for index, row := range comparison.Rows {
		if changedOnly && !workspaceComparisonRowChanged(row) {
			continue
		}
		var oldChanged, newChanged []tools.TextSpan
		if row.OldLine != nil && row.NewLine != nil && row.OldLine.Kind == workspace.PreviewDeleted && row.NewLine.Kind == workspace.PreviewAdded {
			oldChanged, newChanged, _ = tools.RowChangedSpans(context.Background(), row.OldLine.Text, row.NewLine.Text)
		} else {
			if row.OldLine != nil && row.OldLine.Kind == workspace.PreviewDeleted {
				oldChanged = []tools.TextSpan{{Start: 0, EndExclusive: tools.UTF16Length(row.OldLine.Text)}}
			}
			if row.NewLine != nil && row.NewLine.Kind == workspace.PreviewAdded {
				newChanged = []tools.TextSpan{{Start: 0, EndExclusive: tools.UTF16Length(row.NewLine.Text)}}
			}
		}
		display := workspaceDiffDisplayRow{sourceIndex: index}
		display.old = workspaceDiffMakeCell(row.OldLine, oldSyntax, oldChanged, p)
		display.new = workspaceDiffMakeCell(row.NewLine, newSyntax, newChanged, p)
		view.rows = append(view.rows, display)
		for _, cell := range []workspaceDiffRowCell{display.old, display.new} {
			width, _ := c.MeasureText(0, cell.spans...)
			view.contentWidth = max(view.contentWidth, width+76)
		}
	}
	if !singlePane && (saved == nil || !saved.HasRowsScroll) {
		firstChanged := -1
		displayedIndex := 0
		for _, row := range comparison.Rows {
			if changedOnly && !workspaceComparisonRowChanged(row) {
				continue
			}
			if workspaceComparisonRowChanged(row) {
				firstChanged = displayedIndex
				break
			}
			displayedIndex++
		}
		if firstChanged > 0 {
			_, viewportHeight := c.Size()
			y := max(float32(0), float32(firstChanged*workspaceDiffRowHeight)-viewportHeight/2)
			view.oldScroll.Y, view.newScroll.Y, view.lastSharedY = y, y, y
		}
	}
	state.DiffRows = view
	return view
}

type workspaceDiffSyntaxLine struct {
	text  string
	spans []ui.Span
}

// Tokenize before filtering, in original source order. The tokenizer's state
// must cross context lines that CHANGED_LINES will later hide.
func workspaceDiffSourceSyntax(content *workspace.WorkspaceFilePreviewContent, rows []workspace.WorkspaceFileComparisonRow, old bool, language *codeeditor.Language, theme *codeeditor.Theme, p palette) map[int]workspaceDiffSyntaxLine {
	result := make(map[int]workspaceDiffSyntaxLine)
	var lexicalState uint64
	appendLine := func(number int, text string) {
		var tokens []codeeditor.Token
		if language != nil && language.Tokenizer != nil {
			tokens, lexicalState = language.Tokenizer.Tokenize(text, lexicalState, nil)
		}
		spans := make([]ui.Span, 0, len(tokens)+1)
		start := 0
		for _, token := range tokens {
			end := bounded(token.End, start, len(text))
			if end > start {
				color := theme.Tokens[token.Kind].Color
				if color == (ui.Color{}) {
					color = theme.Text
				}
				spans = append(spans, workspaceDiffTextSpan(text[start:end], color))
			}
			start = end
		}
		if start < len(text) || len(spans) == 0 {
			spans = append(spans, workspaceDiffTextSpan(text[start:], theme.Text))
		}
		result[number] = workspaceDiffSyntaxLine{text: text, spans: spans}
	}
	if content != nil {
		for index, line := range strings.Split(content.Text, "\n") {
			appendLine(index+1, strings.TrimSuffix(line, "\r"))
		}
	} else {
		for _, row := range rows {
			line := row.NewLine
			if old {
				line = row.OldLine
			}
			if line != nil {
				appendLine(line.LineNumber, line.Text)
			}
		}
	}
	return result
}

func workspaceDiffTextSpan(text string, color ui.Color) ui.Span {
	return ui.Span{Text: text, Color: color, Font: "monospace", Size: textEditorFontSize}
}

func workspaceDiffMakeCell(line *workspace.WorkspaceFileComparisonLine, syntax map[int]workspaceDiffSyntaxLine, changed []tools.TextSpan, p palette) workspaceDiffRowCell {
	cell := workspaceDiffRowCell{line: line}
	if line == nil {
		return cell
	}
	base := syntax[line.LineNumber]
	if base.text != line.Text || len(base.spans) == 0 {
		base.spans = []ui.Span{workspaceDiffTextSpan(line.Text, p.text)}
	}
	bg := ui.Color{}
	if line.Kind == workspace.PreviewDeleted {
		bg = p.danger.Alpha(.24)
	} else if line.Kind == workspace.PreviewAdded {
		bg = p.green.Alpha(.24)
	}
	byteSpans := make([]tools.TextSpan, 0, len(changed))
	for _, span := range changed {
		byteSpans = append(byteSpans, tools.TextSpan{Start: utf16OffsetToByte(line.Text, span.Start), EndExclusive: utf16OffsetToByte(line.Text, span.EndExclusive)})
	}
	// Split each lexical span at inline boundaries while retaining its color.
	offset := 0
	for _, part := range base.spans {
		end := offset + len(part.Text)
		cursor := offset
		for _, span := range byteSpans {
			start := max(cursor, span.Start)
			stop := min(end, span.EndExclusive)
			if stop <= start {
				continue
			}
			if start > cursor {
				plain := part
				plain.Text = line.Text[cursor:start]
				cell.spans = append(cell.spans, plain)
			}
			highlight := part
			highlight.Text, highlight.Background = line.Text[start:stop], bg
			cell.spans = append(cell.spans, highlight)
			cursor = stop
		}
		if cursor < end || len(part.Text) == 0 {
			plain := part
			plain.Text = line.Text[cursor:end]
			cell.spans = append(cell.spans, plain)
		}
		offset = end
	}
	return cell
}

func (view *workspaceDiffRowsView) syncScroll() {
	y := view.lastSharedY
	if view.oldScroll.Y != y {
		y = view.oldScroll.Y
	} else if view.newScroll.Y != y {
		y = view.newScroll.Y
	}
	view.oldScroll.Y, view.newScroll.Y, view.lastSharedY = y, y, y
}

func renderWorkspaceDiffRows(c *ui.Context, p palette, state *workspaceHistoryState, comparison *workspace.WorkspaceFileComparison, changedOnly bool) {
	view := state.workspaceDiffRows(c, p, comparison, changedOnly)
	if view == nil {
		label(c, "差异行暂不可用。", 11, p.muted)
		return
	}
	view.syncScroll()
	ui.Column(c).Grow(1).MinHeight(0).Background(p.surface).Radius(7).Children(func() {
		if !view.singlePane {
			oldTitle, newTitle := workspaceDiffSideLabels(state, comparison)
			added, removed := workspaceDiffRowsStats(view)
			ui.Row(c).FillWidth().MinWidth(0).Gap(0).Padding(0, 6).AlignItems(ui.Center).Background(p.soft).Children(func() {
				if view.singlePane {
					label(c, newTitle, 10, p.muted).Grow(1).MinWidth(0).NoWrap().Tooltip(workspaceDiffSideTooltip(state, comparison, false))
				} else {
					ratio := firstNonZeroSplitRatio(state.SplitRatio, .5)
					label(c, oldTitle, 10, p.muted).WidthPercent(ratio * 100).MinWidth(0).Shrink(0).NoWrap().Tooltip(workspaceDiffSideTooltip(state, comparison, true))
					ui.Box(c.Key("workspace-diff-rows-title-divider")).Width(1).Shrink(0).Background(p.line)
					label(c, newTitle, 10, p.muted).Grow(1).Basis(0).MinWidth(0).NoWrap().Tooltip(workspaceDiffSideTooltip(state, comparison, false))
				}
				ui.Row(c).Gap(5).Shrink(0).AlignItems(ui.Center).Children(func() {
					label(c, "+"+formatWorkspaceDiffCount(added), 10, p.green).NoWrap()
					label(c, "−"+formatWorkspaceDiffCount(removed), 10, p.danger).NoWrap()
				})
			})
		}
		if len(view.rows) == 0 {
			label(c, "没有可显示的差异行。", 11, p.muted).Padding(8)
			return
		}
		if view.singlePane {
			renderWorkspaceDiffRowsPane(c, p, view, false)
		} else {
			ui.SplitRatio(c, &state.SplitRatio, func() {
				renderWorkspaceDiffRowsPane(c, p, view, true)
			}, func() {
				renderWorkspaceDiffRowsPane(c, p, view, false)
			}).Grow(1).MinHeight(0).FillWidth()
		}
		state.saveActiveWorkspaceDiffRenderer()
	})
}

func workspaceDiffRowsStats(view *workspaceDiffRowsView) (added, removed int) {
	if view == nil {
		return 0, 0
	}
	for _, row := range view.rows {
		if row.old.line != nil && row.old.line.Kind == workspace.PreviewDeleted {
			removed++
		}
		if row.new.line != nil && row.new.line.Kind == workspace.PreviewAdded {
			added++
		}
	}
	return added, removed
}

func renderWorkspaceDiffRowsPane(c *ui.Context, p palette, view *workspaceDiffRowsView, old bool) {
	side, title, scroll := "new", "当前差异", &view.newScroll
	if old {
		side, title, scroll = "old", "修改前差异", &view.oldScroll
	}
	// Fixed-height rows need only their viewport plus the same two-row
	// overscan as MyGO List. Spacers preserve the exact full scroll extent.
	bounds := scroll.Bounds(c)
	_, height := c.Size()
	if bounds.H > 0 {
		height = bounds.H
	}
	y := max(0, min(scroll.Y, float32(len(view.rows)*workspaceDiffRowHeight)))
	first := max(0, int(y/workspaceDiffRowHeight)-2)
	last := min(len(view.rows), int(math.Ceil(float64((y+height)/workspaceDiffRowHeight)))+2)
	ui.ScrollBoth(c.Key("workspace-diff-rows-" + side)).Grow(1).Basis(0).MinWidth(0).MinHeight(0).TrackScroll(scroll).Label(title).Draw(func(p *ui.Painter, actual ui.Rect) {
		if actual.H != bounds.H {
			p.AnimationFrame() // settle the initial/changed measured viewport.
		}
	}).Children(func() {
		ui.Column(c).FillWidth().MinWidth(view.contentWidth).Shrink(0).Children(func() {
			if first > 0 {
				ui.Box(c.Key("workspace-diff-rows-top")).Height(float32(first * workspaceDiffRowHeight)).Shrink(0)
			}
			for _, row := range view.rows[first:last] {
				cell := row.new
				if old {
					cell = row.old
				}
				theme := textEditorTheme(p)
				bg, ink, marker, number := theme.Background, theme.LineNumber, " ", ""
				if cell.line != nil {
					number = strconv.Itoa(cell.line.LineNumber)
					if cell.line.Kind == workspace.PreviewDeleted {
						bg, ink, marker = p.danger.Alpha(.09), p.danger, "−"
					} else if cell.line.Kind == workspace.PreviewAdded {
						bg, ink, marker = p.greenBg, p.green, "+"
					}
				}
				ui.Row(c.Key("workspace-diff-row-"+side+"-"+strconv.Itoa(row.sourceIndex))).FillWidth().Height(workspaceDiffRowHeight).Shrink(0).Padding(2, 6).Gap(5).AlignItems(ui.Center).Background(bg).Children(func() {
					label(c, number, 12, theme.LineNumber).Width(42).Shrink(0).Font("monospace").TextAlign(ui.End).NoWrap()
					label(c, marker, 11, ink).Width(12).Shrink(0).NoWrap()
					if cell.line != nil {
						ui.RichText(c, cell.spans...).Font("monospace").FontSize(textEditorFontSize).LineHeight(textEditorLineHeight).NoWrap().Selectable().Shrink(0)
					}
				})
			}
			if last < len(view.rows) {
				ui.Box(c.Key("workspace-diff-rows-bottom")).Height(float32((len(view.rows) - last) * workspaceDiffRowHeight)).Shrink(0)
			}
		})
	})
}
