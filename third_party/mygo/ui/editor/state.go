package editor

import (
	"slices"

	"github.com/egoist/mygo/ui"
)

// State is a code editor's state, kept by the app across
// frames: the buffer it edits, its options, and what the editor keeps of
// its own, as its selections, folds and scroll position. Set the options
// before building the editor; zero values are the defaults, those of Zed.
type State struct {
	// Buffer is the text the editor edits; one is made when nil.
	Buffer *Buffer
	// Font is the editor's font: the system's monospaced font of 13 DIPs
	// when zero.
	Font ui.Font
	// LineHeight is the height of lines as a multiple of the font's size,
	// 1.5 when 0.
	LineHeight float32
	// TabSize is how many columns a tab and a level of indentation take:
	// the language's, else 4, when 0.
	TabSize int
	// Indent says whether indenting inserts tabs or spaces: as the
	// language's files usually do (Go's tabs) by default.
	Indent IndentStyle
	// SoftWrap wraps lines at the editor's width, or at WrapColumn
	// columns when narrower.
	SoftWrap   bool
	WrapColumn int
	// LineNumbers shows the numbers of lines in the gutter, absolute by
	// default.
	LineNumbers LineNumbers
	// ReadOnly makes the text selectable and copyable, not editable.
	ReadOnly bool
	// Whitespace says where spaces and tabs show as dots and arrows:
	// within selections by default.
	Whitespace Whitespace
	// WrapGuides draws guides at these columns, as 80 and 100.
	WrapGuides []int
	// NoIndentGuides hides the lines that show indentation levels.
	NoIndentGuides bool
	// NoScrollPastEnd stops scrolling at the last line rather than
	// letting it scroll up to the top.
	NoScrollPastEnd bool
	// NoAutoClose stops closing brackets and quotes as they are typed.
	NoAutoClose bool
	// NoFolding hides the fold markers of the gutter and turns folding
	// off.
	NoFolding bool
	// NoActiveLine stops highlighting the lines with a caret.
	NoActiveLine bool
	// CursorShape is the shape of the carets: bars by default, or blocks
	// over the character after them, as Vim's normal mode has, or
	// underlines, as its pending operators do.
	CursorShape CursorShape
	// FitContent makes the editor as high as its lines, to show in a
	// scroll view or a list, rather than scrolling them itself.
	FitContent bool
	// Placeholder shows while the buffer is empty.
	Placeholder string
	// Label names the editor for assistive technology.
	Label string
	// Theme colors the editor; DefaultTheme of the window's theme
	// when nil.
	Theme *Theme
	// Bindings add key bindings, or replace the default ones for their
	// keys (DefaultBindings).
	Bindings []Binding
	// DiffBase, when set, is the text the buffer's changes are shown
	// against in the gutter, as the version of a file in Git: bars mark
	// lines added and changed, and a mark where lines were deleted.
	DiffBase *Buffer

	// OnCompletion asks the app for completions where the caret is, as
	// a word is typed or Ctrl+Space pressed: answer with ShowCompletions,
	// at once or later.
	OnCompletion func(CompletionRequest)
	// OnHover asks the app what to show for the text under the pointer
	// once it rests there: answer with ShowHover.
	OnHover func(HoverRequest)
	// OnDefinition is called for Cmd+click (Ctrl+click elsewhere) and F12
	// on a word, as to go to its definition.
	OnDefinition func(offset int)
	// OnGutterClick is called for a click on a line's number, as to set a
	// breakpoint.
	OnGutterClick func(line int)
	// ContextMenu adds items to the editor's context menu, after its
	// own.
	ContextMenu func(m *ui.Menu)
	// ReplaceContextMenu uses ContextMenu in place of the editor's default
	// menu, for a projected document whose source owns editing commands.
	ReplaceContextMenu bool
	// ClipboardWrite optionally writes copied text. Returning false aborts a cut
	// before changing text, selections or history. Nil uses ui.Services.
	ClipboardWrite func(text string) bool
	// GutterMarks marks lines in the gutter, as breakpoints or
	// bookmarks: a dot of their color.
	GutterMarks []GutterMark
	// LineBackgrounds fill the rows of lines with colors, under the
	// text and the selections, as the statement a key runs: not the
	// gutter, nor the lines folds hide. They keep their line numbers as
	// the text is edited, unlike diagnostics and inlays, which move with
	// it: compute them again when the buffer's Version changes.
	LineBackgrounds []LineBackground
	// FindHighlights paints externally searched UTF-8 byte ranges with the
	// native match colors, without opening a second find bar or moving carets.
	FindHighlights []FindHighlight
	// OnInput sees each input event before the editor, as keys for a
	// mode of the app's such as Vim's: returning true takes it, and the
	// editor leaves it. Text typed at the carets comes as InputText;
	// what an input method composes, commits or replaces, as the accents
	// of press and hold, goes to the editor alone. The key after the
	// first of a chord of Bindings, as Cmd+K Cmd+0, is the chord's.
	OnInput func(ev ui.InputEvent) bool
	// NoInputMethod turns the system's input methods off while the
	// editor has the keyboard (ui's InputMethod): keys type the
	// characters of the keyboard's layout, which come to OnInput as
	// InputText, and nothing is composed, as Vim's normal mode wants
	// with a Chinese input method on. Browsers cannot turn input methods
	// off: there they compose in the text still.
	NoInputMethod bool
	// OnCompositionChanged runs on the UI thread when native IME composition
	// starts or ends, including commits without an intervening rendered frame.
	OnCompositionChanged func(composing bool)
	// OnLimitExceeded reports an atomic edit rejected by Buffer's source limit.
	OnLimitExceeded func()

	// RenderFindBar, RenderGoToLine, RenderCompletions and RenderHover
	// build the editor's find bar, go to line bar, completions and hovers
	// in place of its own (DefaultFindBar, DefaultGoToLine,
	// DefaultCompletions and DefaultHover, which they can call to wrap
	// them): each is given the element the editor places, as a toolbar
	// over its top right corner or a list below the caret, to style and
	// fill, and a handle to what the part shows and does.
	RenderFindBar     func(c *ui.Context, bar ui.Element, f *FindBar)
	RenderGoToLine    func(c *ui.Context, bar ui.Element, g *GoToLineBar)
	RenderCompletions func(c *ui.Context, panel ui.Element, m *CompletionMenu)
	RenderHover       func(c *ui.Context, panel ui.Element, h *HoverCard)

	ed editor
}

// ScrollPosition is a code editor's independent horizontal and vertical
// reading position, in DIPs.
type ScrollPosition struct {
	X float32
	Y float64
}

// IndentStyle is what indenting inserts.
type IndentStyle uint8

const (
	IndentAuto IndentStyle = iota
	IndentSpaces
	IndentTabs
)

// LineNumbers is how the gutter numbers lines.
type LineNumbers uint8

const (
	LineNumbersOn LineNumbers = iota
	LineNumbersRelative
	LineNumbersOff
)

// Whitespace is where spaces and tabs show.
type Whitespace uint8

const (
	WhitespaceSelection Whitespace = iota
	WhitespaceNone
	WhitespaceAll
	WhitespaceTrailing
	// WhitespaceBoundary shows whitespace but single spaces between
	// words.
	WhitespaceBoundary
)

// CursorShape is the shape of an editor's carets, as Zed's CursorShape
// and the terminal's CursorStyle.
type CursorShape uint8

const (
	// CursorBar is a bar before the character at the caret.
	CursorBar CursorShape = iota
	// CursorBlock covers the character after the caret, a space's width
	// at the end of a line, translucent so that the character shows. An
	// editor without the keyboard focus outlines it, as Vim's and Zed's
	// do.
	CursorBlock
	// CursorUnderline underlines the character after the caret.
	CursorUnderline
)

// GutterMark marks a line in the gutter.
type GutterMark struct {
	Line  int
	Color ui.Color
}

// LineBackground fills lines First to Last, from 0, with Color. The lines
// are those of the text when it is set: an edit adding or removing lines
// above them doesn't move them.
type LineBackground struct {
	First, Last int
	Color       ui.Color
}

type FindHighlight struct {
	Start, End int
	Current    bool
}

func (st *State) options() options {
	return options{
		readOnly: st.ReadOnly, font: st.Font, lineHeight: st.LineHeight, tabSize: st.TabSize, indent: st.Indent,
		wrap: st.SoftWrap, wrapColumn: st.WrapColumn, lineNumbers: st.LineNumbers, whitespace: st.Whitespace,
		wrapGuides: st.WrapGuides, indentGuides: !st.NoIndentGuides, scrollPastEnd: !st.NoScrollPastEnd && !st.FitContent,
		autoClose: !st.NoAutoClose, folding: !st.NoFolding, activeLine: !st.NoActiveLine, cursorShape: st.CursorShape, fitContent: st.FitContent,
		placeholder: st.Placeholder, label: st.Label, theme: st.Theme, bindings: st.Bindings, gutterMarks: st.GutterMarks,
		backgrounds: st.LineBackgrounds, findHighlights: st.FindHighlights, diffBase: st.DiffBase, onCompletion: st.OnCompletion, onHover: st.OnHover, onDefinition: st.OnDefinition,
		onGutterClick: st.OnGutterClick, contextMenu: st.ContextMenu, replaceContextMenu: st.ReplaceContextMenu, clipboardWrite: st.ClipboardWrite, onInput: st.OnInput, noInputMethod: st.NoInputMethod, onCompositionChanged: st.OnCompositionChanged, onLimitExceeded: st.OnLimitExceeded, minDigits: 3,
		renderFindBar: st.RenderFindBar, renderGoToLine: st.RenderGoToLine,
		renderCompletions: st.RenderCompletions, renderHover: st.RenderHover,
	}
}

// Selections returns the editor's selections, in order.
func (st *State) Selections() []Selection {
	st.ensure()
	out := make([]Selection, len(st.ed.sels))
	for i, s := range st.ed.sels {
		out[i] = Selection{Anchor: s.anchor, Head: s.head}
	}
	return out
}

// Selection returns the newest selection, the one input methods and
// scrolling follow.
func (st *State) Selection() Selection {
	st.ensure()
	p := st.ed.primary()
	return Selection{Anchor: p.anchor, Head: p.head}
}

// SetSelections sets the selections, the last the newest, and scrolls
// it into view.
func (st *State) SetSelections(sels ...Selection) {
	st.ensure()
	if len(sels) == 0 {
		return
	}
	n := st.ed.view.text.Len()
	out := make([]sel, len(sels))
	for i, s := range sels {
		out[i] = st.ed.newSel(max(0, min(s.Anchor, n)), max(0, min(s.Head, n)))
	}
	st.ed.setSels(out, revealFit)
}

// Select selects the text from start to end, a caret alone at end when
// they are equal.
func (st *State) Select(start, end int) { st.SetSelections(Selection{Anchor: start, Head: end}) }

// Cursor returns the offset of the newest caret.
func (st *State) Cursor() int { return st.Selection().Head }

// CursorPos returns the line and column of the newest caret.
func (st *State) CursorPos() Pos {
	st.ensure()
	return posOf(st.ed.view.text, st.ed.primary().head)
}

// SelectedText returns the text of the selections, joined by newlines.
func (st *State) SelectedText() string {
	st.ensure()
	return st.ed.selectedText()
}

// Insert replaces each selection with text, as typing or pasting it.
func (st *State) Insert(text string) {
	st.ensure()
	st.ed.retry(func() bool { st.ed.paste(text); return true })
}

// Run runs a command as its key would, and reports whether it did
// something.
func (st *State) Run(cmd Command) bool {
	st.ensure()
	return st.ed.retry(func() bool { return st.ed.run(cmd) })
}

// GoTo puts the caret at a line and column, from 0, and scrolls it to
// the middle of the editor.
func (st *State) GoTo(p Pos) {
	st.ensure()
	off := offsetOf(st.ed.view.text, p)
	st.ed.setSels([]sel{st.ed.newSel(off, off)}, revealCenter)
}

// ScrollTo scrolls a line to the top of the editor.
func (st *State) ScrollTo(line int) {
	st.ensure()
	st.ed.setScroll(st.ed.view.heights.top(line))
}

// Find opens the find bar with query, and selects its first match after
// the caret.
func (st *State) Find(query string) {
	st.ensure()
	f := &st.ed.find
	f.open, f.query, f.stale = true, query, true
	f.step(&st.ed, 1)
}

// FindMatches returns how many matches the find bar has, and which is
// chosen, from 0.
func (st *State) FindMatches() (count, current int) {
	st.ensure()
	f := &st.ed.find
	if !f.open {
		return 0, 0
	}
	f.search(&st.ed)
	return len(f.matches), f.current
}

// Fold folds the lines indented under line, or up to its closing
// bracket, as the gutter's marker does; Unfold unfolds them.
func (st *State) Fold(line int) {
	st.ensure()
	if !st.ed.isFolded(line) {
		st.ed.toggleFoldLine(line)
	}
}

func (st *State) Unfold(line int) {
	st.ensure()
	if st.ed.isFolded(line) {
		st.ed.toggleFoldLine(line)
	}
}

// Folded returns the lines folds start at.
func (st *State) Folded() []int {
	var out []int
	for _, f := range st.ed.folds {
		out = append(out, f.start)
	}
	slices.Sort(out)
	return out
}

// SetDiagnostics shows diagnostics of the text at version, the buffer's
// Version when they were made: those of an older version move with the
// edits since, as a language server's that arrive late.
func (st *State) SetDiagnostics(version uint64, ds []Diagnostic) {
	st.ensure()
	ed := &st.ed
	ed.sync()
	ds = slices.Clone(ds)
	if version != ed.view.version {
		// Moved through the edits since, up to the text the editor shows.
		entries, _, ok := st.Buffer.entriesSince(version)
		if !ok {
			return
		}
		for _, e := range entries {
			if e.version > ed.view.version {
				break
			}
			for i := range ds {
				d := &ds[i]
				d.Start, d.End = mapOffset(d.Start, e.changes, biasRight), mapOffset(d.End, e.changes, biasLeft)
				d.End = max(d.End, d.Start)
			}
		}
	}
	slices.SortStableFunc(ds, func(a, b Diagnostic) int { return a.Start - b.Start })
	ed.diags = ds
}

// Diagnostics returns the diagnostics shown, where the edits since moved
// them.
func (st *State) Diagnostics() []Diagnostic { return slices.Clone(st.ed.diags) }

// ShowCompletions shows completions of the word before the caret, which
// starts at offset start (the request's Offset less its Prefix), or -1
// for the word there now. Typing filters them; an empty list closes them.
func (st *State) ShowCompletions(start int, items []Completion) {
	st.ensure()
	st.ed.sync()
	st.ed.showCompletions(start, items)
}

// ShowHover shows what the text from start to end is: text in the
// editor's font, and what build adds, as a hover of a language server.
func (st *State) ShowHover(start, end int, text string, build func(c *ui.Context)) {
	st.ensure()
	h := &st.ed.hover
	h.shown, h.start, h.end, h.text, h.build = true, start, end, text, build
	if h.diags == nil {
		h.diags = st.ed.diagnosticsAt(start)
	}
}

// SetInlays shows inlay hints in the lines, as types and parameter
// names: they move with the edits until set again.
func (st *State) SetInlays(inlays []Inlay) {
	st.ensure()
	st.ed.setInlays(inlays)
}

// SetSuggestion shows text at offset, muted, as an edit prediction:
// Tab inserts it, and an edit or a move of the caret drops it. An empty
// text takes it away.
func (st *State) SetSuggestion(offset int, text string) {
	st.ensure()
	st.ed.setSuggestion(offset, text)
}

// Focused reports whether the editor had the keyboard focus in the last
// frame.
func (st *State) Focused() bool { return st.ed.focused }

// Composing reports whether the input method has an uncommitted composition.
// Owners can defer save/snapshot publication until it is committed without
// replacing the buffer or interrupting the native input method.
func (st *State) Composing() bool { return st.ed.markedActive }

// SyntaxReady reports whether the most recent visible paint used final syntax
// tokens for the current buffer version and language. It stays false when a
// background job has finished but those tokens have not been painted yet.
//
// Before the first paint it is false, including for plain text; painted plain
// text is ready immediately. This query never starts or waits for syntax work.
// It describes the last completed paint during a build. Capture callers should
// check it again after that frame is painted and query only their active views.
func (st *State) SyntaxReady() bool {
	ed, b := &st.ed, st.Buffer
	if b == nil || !ed.syntaxVisible || !ed.syntaxFinal || ed.syntaxPaintedAt.IsZero() || ed.syntaxBuffer != b {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return ed.syntaxVersion == b.version && ed.syntaxLanguage == b.language
}

// SyntaxVisible reports whether the most recently built view actually painted
// a nonempty visible area. It is false before painting or when that build was
// clipped offscreen. A cached State left out of the current UI keeps its last
// value, so callers must first select only the current active document/views.
func (st *State) SyntaxVisible() bool {
	ed := &st.ed
	return ed.syntaxVisible && !ed.syntaxPaintedAt.IsZero() && !ed.syntaxPaintedAt.Before(ed.builtAt)
}

// ScrollPosition reports how far the editor is scrolled left and up.
func (st *State) ScrollPosition() ScrollPosition {
	st.ensure()
	return ScrollPosition{X: st.ed.scrollX, Y: st.ed.scrollY}
}

// SetScrollPosition restores a reading position. The next layout clamps it
// to the current content and viewport.
func (st *State) SetScrollPosition(position ScrollPosition) {
	st.ensure()
	ed := &st.ed
	ed.scrollX = max(0, position.X)
	ed.scrollY = max(0, position.Y)
	if ed.view.heights.n > 0 {
		line := ed.view.heights.lineAt(ed.scrollY)
		ed.anchorLine = line
		ed.anchorOff = ed.scrollY - ed.view.heights.top(line)
	} else {
		ed.anchorLine = 0
		ed.anchorOff = ed.scrollY
	}
}

// VisibleLines returns the first and the last line, from 0, that the
// last frame showed, those hidden by folds among them.
func (st *State) VisibleLines() (first, last int) { return st.ed.shown[0], st.ed.shown[1] }

// LineGeometry reports a logical line's top and height in content DIPs, using
// the same soft-wrap/fold layout as the native text and caret. Hidden lines have
// visible false. The last frame's geometry is available between builds.
func (st *State) LineGeometry(line int) (top float64, height float32, visible bool) {
	st.ensure()
	if line < 0 || line >= st.ed.view.text.LineCount() {
		return 0, 0, false
	}
	g := st.ed.view.heights.geometry(line)
	return g.rows, float32(g.below - g.rows), !g.hidden
}

// LineAt returns the logical line at y in content DIPs.
func (st *State) LineAt(y float64) int {
	st.ensure()
	return st.ed.view.heights.lineAt(y)
}

// ensure readies the state for methods called before the editor is
// built.
func (st *State) ensure() {
	if st.Buffer == nil {
		st.Buffer = NewBuffer("")
	}
	ed := &st.ed
	if ed.view.buf != st.Buffer || ed.sels == nil {
		ed.init(st.Buffer, st.options(), ui.LightTheme())
	}
	ed.sync()
}
