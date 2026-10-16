package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	nativeDiffMaxBytes = 256 * 1024
	nativeDiffMaxLines = 8000
)

// diffNativeViewState owns the immutable source pair and MyGO's persistent
// native viewer state. The native viewer is used only for exact comparisons;
// filtered comparisons continue to use the cancellable tools.CompareText
// result so the visible text and copied output share the same filter rules.
type diffNativeViewState struct {
	oldText    string
	newText    string
	view       editor.DiffState
	singlePane bool
	positioned bool

	collapseKnown bool
	collapsed     bool
	contextLines  int
}

func newDiffNativeViewState(oldText, newText string) *diffNativeViewState {
	return &diffNativeViewState{
		oldText:      oldText,
		newText:      newText,
		contextLines: 3,
		view: editor.DiffState{
			Old:          editor.NewBuffer(oldText),
			New:          editor.NewBuffer(newText),
			Layout:       editor.DiffSplit,
			SplitRatio:   .5,
			Inline:       editor.DiffInlineWordAlt,
			Indicators:   editor.DiffClassic,
			ContextLines: 3,
		},
	}
}

// nativeDiffView selects the MyGO viewer only after the matching comparison
// has completed, with no ignore rules and within the synchronous-render bound.
// Larger and filtered inputs keep using the existing cancellable comparison.
func (state *toolPageState) nativeDiffView() *diffNativeViewState {
	if state == nil || !state.diffHasData ||
		state.diffOptions.IgnoreCase || state.diffOptions.IgnoreLineWhitespace || state.diffOptions.IgnoreEmptyLines {
		return nil
	}
	// CompareText treats CR and CRLF as line delimiters. The native buffer
	// splits at LF, so its independent comparison cannot represent that rule.
	if strings.ContainsRune(state.diffLeft, '\r') || strings.ContainsRune(state.diffRight, '\r') {
		return nil
	}
	if len(state.diffLeft)+len(state.diffRight) > nativeDiffMaxBytes ||
		nativeDiffLineCount(state.diffLeft)+nativeDiffLineCount(state.diffRight) > nativeDiffMaxLines {
		return nil
	}
	if state.diffNative == nil || state.diffNative.oldText != state.diffLeft || state.diffNative.newText != state.diffRight {
		state.diffNative = newDiffNativeViewState(state.diffLeft, state.diffRight)
	}
	state.diffNative.contextLines = max(0, state.diffOptions.ContextLines)
	return state.diffNative
}

func nativeDiffLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func (view *diffNativeViewState) render(c *ui.Context, p palette, collapse, wrap bool) ui.Element {
	if view == nil {
		return ui.Box(c)
	}
	if collapse != view.collapsed || !view.collapseKnown {
		if collapse {
			view.view.CollapseAll()
		} else {
			view.view.ExpandAll()
		}
		view.collapsed, view.collapseKnown = collapse, true
	}
	view.view.Layout = editor.DiffSplit
	if view.singlePane {
		view.view.Layout = editor.DiffUnified
	}
	view.view.Inline = editor.DiffInlineWordAlt
	view.view.Indicators = editor.DiffClassic
	view.view.ContextLines = view.contextLines
	if view.view.ContextLines == 0 {
		view.view.ContextLines = -1 // MyGO uses zero for its default of three.
	}
	view.view.ExpansionLines = 100
	view.view.Editable = false
	view.view.HunkActions = false
	view.view.LineSelection = false
	view.view.NoLineNumbers = false
	view.view.Wrap = wrap
	view.view.Font = ui.Font{Family: "monospace", Size: textEditorFontSize}
	view.view.LineHeight = textEditorLineHeight
	view.view.Theme = textEditorTheme(p)
	view.view.RenderSeparator = func(c *ui.Context, sep ui.Element, part *editor.DiffSeparator) {
		renderDiffNativeSeparator(c, sep, part, p)
	}
	return editor.DiffView(c, &view.view).Grow(1).MinHeight(0).FillWidth()
}

func renderDiffNativeSeparator(c *ui.Context, sep ui.Element, part *editor.DiffSeparator, p palette) {
	sep.Padding(4).Children(func() {
		if part.Lines == 0 {
			if part.Header != "" {
				ui.Box(c).FillWidth().Padding(5, 10).Radius(6).Background(p.soft).Children(func() {
					label(c, part.Header, 10, p.muted).Font("Consolas, monospace").SingleLine()
				})
			}
			return
		}
		ui.Row(c).FillWidth().Height(30).Gap(8).Padding(0, 8).AlignItems(ui.Center).Radius(6).Background(p.soft).Children(func() {
			label(c, fmt.Sprintf("%d 行相同内容已折叠", part.Lines), 10, p.muted)
			ui.Spacer(c)
			button := func(title string, expand func()) {
				if ui.Button(c, title).Clicked() {
					expand()
				}
			}
			if !part.First {
				button("向上展开", part.ExpandUp)
			}
			if !part.Last {
				button("向下展开", part.ExpandDown)
			}
			button("展开全部", part.ExpandAll)
		})
	})
}

func (view *diffNativeViewState) hunkCount() int {
	if view == nil {
		return 0
	}
	return len(view.view.Hunks())
}

func (view *diffNativeViewState) goToHunk(index int) {
	if view == nil || index < 0 || index >= view.hunkCount() {
		return
	}
	view.view.GoToHunk(index)
}

func (view *diffNativeViewState) differencesText() string {
	if view == nil || view.view.Old == nil || view.view.New == nil {
		return ""
	}
	var text strings.Builder
	for _, hunk := range view.view.Hunks() {
		oldStart, oldEnd := max(0, hunk.OldStart), min(view.view.Old.LineCount(), hunk.OldEnd)
		newStart, newEnd := max(0, hunk.NewStart), min(view.view.New.LineCount(), hunk.NewEnd)
		for offset := 0; offset < max(oldEnd-oldStart, newEnd-newStart); offset++ {
			if line := oldStart + offset; line < oldEnd {
				text.WriteString("- ")
				text.WriteString(view.view.Old.Line(line))
				text.WriteByte('\n')
			}
			if line := newStart + offset; line < newEnd {
				text.WriteString("+ ")
				text.WriteString(view.view.New.Line(line))
				text.WriteByte('\n')
			}
		}
	}
	return text.String()
}

func (view *diffNativeViewState) stats() (added, removed int) {
	if view == nil {
		return 0, 0
	}
	return view.view.Stats()
}

// The service groups nearby changes using ContextLines. MyGO's model keeps
// each edit block, so map a service group to its first native edit block.
func (view *diffNativeViewState) nativeHunkForComparison(hunk tools.DiffHunk) int {
	if view == nil {
		return -1
	}
	for index, native := range view.view.Hunks() {
		if hunk.LeftStartLine != nil && native.OldStart == *hunk.LeftStartLine-1 ||
			hunk.RightStartLine != nil && native.NewStart == *hunk.RightStartLine-1 {
			return index
		}
	}
	return -1
}

func (view *diffNativeViewState) goToComparisonHunk(hunk tools.DiffHunk) {
	if index := view.nativeHunkForComparison(hunk); index >= 0 {
		view.goToHunk(index)
	}
}
