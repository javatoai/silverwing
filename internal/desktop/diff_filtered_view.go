package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/tools"
	"strconv"
)

func (s *app) renderDiffRows(c *ui.Context, p palette, state *toolPageState) {
	rows := state.diffResult.Rows
	hunkByRow := make(map[int]int)
	for index, hunk := range state.diffResult.Hunks {
		for row := hunk.StartRow; row < hunk.EndRowExclusive; row++ {
			if _, exists := hunkByRow[row]; !exists {
				hunkByRow[row] = index
			}
		}
	}
	for index := 0; index < len(rows); {
		row := rows[index]
		if state.diffCollapseEqual && row.Kind == "EQUAL" {
			end := index + 1
			for end < len(rows) && rows[end].Kind == "EQUAL" {
				end++
			}
			if state.diffExpandedEqual[index] {
				for visible := index; visible < end; visible++ {
					hunk, exists := hunkByRow[visible]
					if !exists {
						hunk = -1
					}
					s.renderDiffRow(c, p, rows[visible], visible, state, hunk)
				}
				index = end
				continue
			}
			context := min(max(0, state.diffOptions.ContextLines), len(rows))
			if end-index > context*2+1 && !state.diffExpandedEqual[index] {
				for visible := index; visible < index+context; visible++ {
					s.renderDiffRow(c, p, rows[visible], visible, state, -1)
				}
				folded := end - index - context*2
				b := ui.ButtonBase(c.Key(fmt.Sprintf("diff-fold-%d", index))).FillWidth().Height(27).Padding(0, 10).Radius(3).Label(fmt.Sprintf("… %d 行相同内容已折叠，点击展开", folded))
				if b.Hovered() {
					b.Background(p.soft)
				}
				b.Children(func() {
					label(c, fmt.Sprintf("… %d 行相同内容已折叠，点击展开", folded), 10, p.muted).Font("Consolas")
				})
				if b.Clicked() {
					state.diffExpandedEqual[index] = true
					c.Invalidate()
				}
				for visible := end - context; visible < end; visible++ {
					s.renderDiffRow(c, p, rows[visible], visible, state, -1)
				}
				index = end
				continue
			}
		}
		hunk, exists := hunkByRow[index]
		if !exists {
			hunk = -1
		}
		s.renderDiffRow(c, p, row, index, state, hunk)
		index++
	}
}

func (s *app) renderDiffRow(c *ui.Context, p palette, row tools.DiffRow, index int, state *toolPageState, hunk int) {
	b := ui.Row(c.Key(fmt.Sprintf("diff-row-%d", index))).FillWidth().MinHeight(25).AlignItems(ui.Stretch)
	if hunk >= 0 && hunk == state.diffActiveHunk {
		b.BorderWidth(0, 0, 0, 2).BorderColor(p.blue)
	}
	if state.diffScrollPending && index == state.diffScrollRow {
		b.ScrollIntoView()
		state.diffScrollPending = false
	}
	b.Children(func() {
		leftKind, rightKind := "EQUAL", "EQUAL"
		if row.Kind == "REMOVED" || row.Kind == "CHANGED" {
			leftKind = "REMOVED"
		}
		if row.Kind == "ADDED" || row.Kind == "CHANGED" {
			rightKind = "ADDED"
		}
		renderFilteredDiffCell(c, p, row.Left, leftKind, state.diffWrap)
		ui.Box(c).Width(1).Shrink(0).Background(p.line)
		renderFilteredDiffCell(c, p, row.Right, rightKind, state.diffWrap)
	})
}

func renderFilteredDiffCell(c *ui.Context, p palette, line *tools.DiffLine, kind string, wrap bool) {
	bg, marker := p.surface, " "
	if kind == "REMOVED" {
		bg, marker = p.danger.Alpha(.09), "−"
	}
	if kind == "ADDED" {
		bg, marker = p.greenBg, "+"
	}
	ui.Row(c).Grow(1).Basis(0).MinWidth(0).Padding(3, 6).Gap(5).Background(bg).AlignItems(ui.Start).Children(func() {
		number, text := "", ""
		var spans []tools.TextSpan
		if line != nil {
			number, text, spans = strconv.Itoa(line.Number), line.Text, line.ChangedSpans
		}
		label(c, number, 10, p.muted).Width(40).Shrink(0).Font("Consolas").TextAlign(ui.End)
		label(c, marker, 11, diffMarkerColor(kind, p)).Width(12).Shrink(0)
		renderDiffText(c, text, spans, kind, p, wrap)
	})
}

func renderDiffText(c *ui.Context, text string, spans []tools.TextSpan, kind string, p palette, wrap bool) {
	parts := make([]ui.Span, 0, len(spans)*2+1)
	cursor := 0
	for _, span := range spans {
		start := utf16OffsetToByte(text, span.Start)
		end := utf16OffsetToByte(text, span.EndExclusive)
		start = bounded(start, cursor, len(text))
		end = bounded(end, start, len(text))
		if start > cursor {
			parts = append(parts, ui.Span{Text: text[cursor:start], Color: p.text})
		}
		if end > start {
			bg := p.selected
			fg := p.blue
			if kind == "ADDED" {
				bg, fg = p.green.Alpha(.22), p.text
			} else if kind == "REMOVED" {
				bg, fg = p.danger.Alpha(.22), p.text
			}
			parts = append(parts, ui.Span{Text: text[start:end], Color: fg, Background: bg})
		}
		cursor = end
	}
	if cursor < len(text) {
		parts = append(parts, ui.Span{Text: text[cursor:], Color: p.text})
	}
	if len(parts) == 0 {
		parts = append(parts, ui.Span{Text: " ", Color: p.text})
	}
	e := ui.RichText(c, parts...).Grow(1).MinWidth(0).Font("Consolas, monospace").FontSize(12).Selectable()
	if wrap {
		e.Wrap()
	} else {
		e.NoWrap()
	}
}

func diffMarkerColor(kind string, p palette) ui.Color {
	switch kind {
	case "ADDED":
		return p.green
	case "REMOVED":
		return p.danger
	case "CHANGED":
		return p.blue
	default:
		return p.muted
	}
}
