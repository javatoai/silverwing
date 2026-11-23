package markdown

import (
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/internal/l10n"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// Options are how views show Markdown: their theme, what links and images
// do, how blocks render, and how text being written comes in. Keep one in
// the app's state and give it to every view of a kind, as the messages of
// a chat.
type Options struct {
	// Theme is the look: DefaultTheme of the window's theme when nil,
	// whose values those the theme leaves zero take.
	Theme *Theme
	// Extensions is the syntax read beyond CommonMark: Default when zero.
	Extensions parser.Extensions

	// LinkMenu, when set, builds the context menu of a link, given its
	// destination, as a chat's Copy Path and Reveal in Finder for a link
	// to a file. The link's menu replaces the one around the view, as its
	// Selectable container's, since the innermost element with a context
	// menu gets the click; m.EditItems adds the text's Copy and Select
	// All to it. Links to #a-heading, footnote references and a link
	// whose destination is still coming keep the menu around them.
	LinkMenu func(m *ui.Menu, url string)
	// OnLink, when set, handles a click on a link, given its destination.
	// Without it, links to http, https and mailto addresses open as a
	// ui.Link opens them, a path without a scheme goes there in the
	// router around the view, a link to #a-heading scrolls to that
	// heading, and links of other schemes do nothing.
	OnLink func(url string)
	// LinkIcon, when set, makes an object at the start of a link's text,
	// from its destination and the size of the text around it in DIPs:
	// one on the baseline, inside the link, which stays on the line of
	// the link's first word and copies as nothing. When it returns false,
	// the link has none; a link still being written has none yet. It is
	// asked again whenever the paragraph changes: keep what it makes.
	LinkIcon func(dest string, size float32) (InlineObject, bool)
	// Anchor scrolls to the heading of this slug, with or without its "#",
	// or to the block AnchorBlock finds for it, when the document first
	// shows, when another document replaces it, and when Anchor changes:
	// a docs reader opens guide.md#install at its heading. An OnLink that
	// takes #a-heading links sets it.
	Anchor string
	// AnchorVersion scrolls to Anchor again when it changes, for the same
	// heading asked for twice.
	AnchorVersion uint64
	// AnchorBlock, when set, finds the block an anchor names, given the
	// view's document and the anchor without its "#", for Anchor and for
	// the links to #an-anchor the view follows itself: a notes app finds
	// the paragraph that ends in ^block-id. It returns the block's index,
	// or -1 to look for the heading of that slug, as without it. A row or
	// a cell of a table scrolls the table into view. The document is the
	// view's: read it, never change it.
	AnchorBlock func(d *parser.Document, name string) int
	// OnTask, when set, makes the check boxes of task list items controls
	// of the app's: a click on one, or Space or Enter while it has the
	// focus, calls it with the item's line in the view's source, from 1,
	// and the state asked for, the other one. The box keeps showing what
	// the source says, so the app writes the change into its Markdown,
	// which the view then shows. Without it, the boxes only show whether
	// their tasks are done.
	OnTask func(line int, checked bool)
	// Resolve, when set, makes mentions (@octocat) and references to
	// issues and pull requests (#123) links: it returns where one goes,
	// which OnLink, or else the router or a ui.Link, opens, as a path of
	// the app's to open it in place, or false to leave it text, as
	// GitHub does for a person or an issue it does not know. Without it,
	// they show as text. It is asked for each one shown whenever the view
	// builds, so it looks up what the app has at hand, as in a map, and
	// paragraphs follow when its answers change.
	Resolve func(in parser.Inline) (dest string, ok bool)
	// CodeLink, when set, makes code within text a link where it returns
	// one: a chat links `src/app.go:12` to the file it names. It is asked
	// for each piece of code shown whenever the view builds, as Resolve
	// is, and paragraphs follow when its answers change. The code keeps
	// its look; its source is untouched, so a document streaming in
	// parses as it would without.
	CodeLink func(code string) (dest string, ok bool)
	// Preview, when set, returns what shows while the pointer rests on a
	// link to dest, a mention's or a reference's included: a function
	// filling the tip, as ui.TooltipBase does, or nil for the address
	// alone. It runs as the view builds, so it shows what the app has at
	// hand, fetching the rest in the background.
	Preview func(c *ui.Context, dest string) func(tip ui.Element)
	// LoadImage loads the image at url, on a goroutine of its own, for the
	// views to show; until it has, they show its description. Without
	// it, images show as their descriptions. FileImages loads files.
	LoadImage func(url string) (ui.ImageSource, error)

	// CodeBlock, when set, builds code blocks in place of DefaultCodeBlock,
	// which it can call to wrap it; Code builds the code blocks of the
	// languages it lists, as "mermaid", in place of either.
	CodeBlock func(c *ui.Context, b *CodeBlockView)
	Code      map[string]func(c *ui.Context, b *CodeBlockView)
	// WrapCode wraps the lines of code blocks rather than scrolling them
	// sideways.
	WrapCode bool
	// Math, when set, builds blocks of display math from their TeX, which
	// show as their TeX without it; math within text shows as its TeX.
	Math func(c *ui.Context, tex string)
	// InlineMath, when set, makes math within text an object of the
	// text, from its TeX and the size of the text around it in DIPs: one
	// on the baseline, which wraps with the words and copies as its
	// source, as $x_i$. When it returns false, the math shows as its
	// TeX. It is asked again whenever the paragraph changes, as every
	// frame of a paragraph streaming in: keep what it makes.
	InlineMath func(tex string, size float32) (InlineObject, bool)
	// HTML, when set, builds HTML blocks from their HTML, which show as
	// their HTML without it; within text, the tags of formatting (<b>,
	// <i>, <code>, <br>, <mark>, <sub>, …) apply, and other tags show as
	// text.
	HTML func(c *ui.Context, html string)
	// Block, when set, is asked first for each block, and builds it in
	// its place when it returns true.
	Block func(c *ui.Context, d *parser.Document, i int) bool
	// SourcePrefix returns the end of a host-validated opaque metadata prefix.
	// It is evaluated against the current source, including undo/redo versions.
	SourcePrefix func(source string) int

	// Reveal is how Stream shows text as it comes: DefaultReveal when nil.
	Reveal *Reveal
	// ShareSelection leaves the selection to a Selectable container around
	// the view, as one across a chat's messages, rather than giving the
	// view one of its own. Within another view, as a block Block builds,
	// it shares that view's, whose Copy as Markdown copies its text as
	// the Markdown of its own source.
	ShareSelection bool

	// Silverwing adapters leave parsing, layout and selection in the framework.
	TransformText func(block, offset int, spans []ui.Span) []ui.Span
	OnText        func(c *ui.Context, element ui.Element, block, offset int, spans []ui.Span)
	Image         func(c *ui.Context, block, start, end int, url, alt string)
	// ImageContent draws only the image for an editable image object. The
	// editor owns clicks, selection and its actions. Readers keep using Image.
	ImageContent  func(c *ui.Context, block, start, end int, url, alt string)
	OnImageOpen   func(ImageInfo)
	ValidateImage func(target string) error
	Link          func(c *ui.Context, in *parser.Inline, block, offset int) bool
	// LinkView can wrap the native formatted label in a host-owned popover.
	// Returning false retains the regular link and its selection geometry.
	// In an Editor with RenderedLinks, use EditorState.LinkLabel for the
	// children and combine LinkClicked with the element's Clicked.
	LinkView   func(c *ui.Context, in *parser.Inline, block, offset int, children func()) bool
	BlockLink  func(in *parser.Inline) bool
	CopySource func(start, end int) string
	// CopyCode keeps the default code block's layout and copy feedback while
	// allowing the host to copy its original source through its clipboard.
	// Return false on failure to suppress the copied indicator. Nil retains
	// the default clipboard path.
	CopyCode func(c *ui.Context, block *CodeBlockView) bool
	// ContextMenu appends document actions after the built-in selection commands.
	ContextMenu func(c *ui.Context, menu *ui.Menu)

	images sync.Map // url → *imageEntry
}

// InlineObject is math within text, as Options.InlineMath makes it, or a
// link's icon, as Options.LinkIcon does: a box Width wide, Height above the baseline and Depth below it, in DIPs,
// which Draw paints in r, its box in the window. Draw only paints: it
// may run more than once a frame.
type InlineObject struct {
	Width, Height, Depth float32
	Draw                 func(p *ui.Painter, r ui.Rect)
}

// View shows src, finished Markdown, as it reads: CommonMark and what
// Options.Extensions add, parsed again only where src changed since the
// last frame. Its text is selectable, copied as text, as HTML, which
// keeps its formatting where it is pasted (a rich text editor, mail, a
// word processor), and as Markdown (transfer.Markdown), or, with the
// context menu's Copy as Markdown (Shift+Cmd+C, Shift+Ctrl+C on Linux and
// Windows), as Markdown's text. o may be nil.
//
//	markdown.View(c, app.readme, &app.md)
func View(c *ui.Context, src string, o *Options) ui.Element {
	return build(c, src, o, false)
}

// Stream shows src, Markdown still being written, as a language model's
// reply: text appended to src since the last frame is parsed alone, the
// end shows what its half-written syntax will most likely become (see
// Document.SetStreaming), and text comes in as Options.Reveal says,
// smoothly, fading in. Show the message with View once it is complete,
// in the same place, so that its parse carries on; what is still being
// revealed then still comes in.
//
//	if msg.done {
//		markdown.View(c, msg.text, &app.md)
//	} else {
//		markdown.Stream(c, msg.text, &app.md)
//	}
func Stream(c *ui.Context, src string, o *Options) ui.Element {
	return build(c, src, o, true)
}

// Revealed reports whether el, a view View or Stream returned this frame,
// shows all of its source, done fading in. A chat shows a reply's time
// and actions once its reply is complete and Revealed, not as its last
// bytes arrive, while words still come in:
//
//	var md ui.Element
//	if msg.done {
//		md = markdown.View(c, msg.text, &app.md)
//	} else {
//		md = markdown.Stream(c, msg.text, &app.md)
//	}
//	if msg.done && markdown.Revealed(md) {
//		ui.Text(c, msg.time)
//	}
//
// It tells what this frame shows, so call it after View or Stream, and
// build what it decides after them. The view asks for a frame each
// refresh while it reveals or fades, so a caller that reads it every
// frame sees it turn true without other input, once the last unit shows
// and its fade ends. Text that shows at once (AtOnce, a View given text
// no Stream left to reveal, an edit) is revealed in the frame it comes;
// without a fade (Reveal.Fade negative, or the desktop's Reduce Motion),
// text is revealed once its last unit shows. It turns false again when
// more text comes. Of a Stream, it tells only that the text that came so
// far shows: whether more is coming is the caller's to know, and text
// held back for a sentence, a line or a paragraph to end keeps it false
// until View shows it. It is false for an element neither returned.
func Revealed(el ui.Element) bool {
	st := ui.Local(el, stateKey{}, func() *viewState { return nil })
	return st != nil && *st != nil && (*st).reveal.revealed()
}

// stateKey keys a view's state among its root's.
type stateKey struct{}

// viewState is what a view keeps from frame to frame.
type viewState struct {
	// editor observes display geometry; only its root owns text input and selection.
	editor *EditorState
	o      *Options
	doc    *parser.Document
	ext    parser.Extensions
	uiTh   ui.Theme
	def    *Theme
	th     Theme
	paras  []paraCache
	// tasks name the check boxes of tasks, by their items' blocks, while
	// Options.OnTask makes them controls.
	tasks []taskLabel
	// elems notes, for copying, the block each paragraph's element shows,
	// and, for a code block's, its first line.
	elems  []elemInfo
	reveal revealState
	codes  map[int]*codeState
	tables map[int]*tableState
	// scrollTo is a block to scroll into view, -1 for none.
	scrollTo      int
	anchor        string
	anchorVersion uint64
	notes         map[string]int
	sb            spanBuilder
	fade          func(src int) float32
	// nested are the views with ShareSelection built within this one's
	// blocks, whose text this one's selection holds, for copying it as
	// Markdown; cur is the block being built, which a nested view is
	// noted in.
	nested []nestedView
	cur    int
}

// nestedView is a view sharing the selection of the view around it,
// built in its block.
type nestedView struct {
	st    *viewState
	block int
}

// building maps a window's context to the view whose blocks are being
// built in it, for the views within it that share its selection to note
// themselves in it.
// A plain map under a lock rather than a sync.Map, whose stores
// allocate, so a steady frame stays free of allocations.
var building struct {
	sync.Mutex
	m map[*ui.Context]*viewState
}

// outerView returns the view whose blocks are being built in c.
func outerView(c *ui.Context) *viewState {
	building.Lock()
	defer building.Unlock()
	return building.m[c]
}

// setBuilding notes st as the view whose blocks are being built in c,
// or that none is when st is nil.
func setBuilding(c *ui.Context, st *viewState) {
	building.Lock()
	defer building.Unlock()
	if st == nil {
		delete(building.m, c)
		return
	}
	if building.m == nil {
		building.m = map[*ui.Context]*viewState{}
	}
	building.m[c] = st
}

type paraCache struct {
	start, end int
	kind       parser.Kind
	rev        uint32
	base       spanStyle
	ok         bool
	p          para
}

type elemInfo struct {
	id         uint64
	block      int
	line       int
	runeOffset int
}

func newState() *viewState { return &viewState{scrollTo: -1} }

func build(c *ui.Context, src string, o *Options, streaming bool) ui.Element {
	if o == nil {
		o = &defaultOptions
	}
	root := ui.Column(c)
	st := *ui.Local(root, stateKey{}, newState)
	st.o = o
	ext := o.Extensions
	if ext == 0 {
		ext = parser.Default
	}
	documentChanged := st.doc == nil || st.ext != ext
	if documentChanged {
		st.doc, st.ext = parser.New(ext), ext
		st.paras, st.tasks = st.paras[:0], st.tasks[:0]
	}
	st.theme(c)
	th := &st.th

	shown, revealing := st.reveal.step(c, src, streaming, o.Reveal)
	st.doc.SetStreaming(streaming || revealing)
	if !strings.HasPrefix(src[:shown], st.doc.Source()) {
		// Another text, not more of it: blocks where blocks were may
		// show other text.
		st.forget()
		documentChanged = true
	}
	prefix := 0
	if o.SourcePrefix != nil {
		prefix = o.SourcePrefix(src[:shown])
	}
	if st.doc.SourcePrefixEnd() != prefix {
		st.forget()
		documentChanged = true
	}
	st.doc.SetWithPolicy(src[:shown], prefix, nil)
	if o.Anchor != "" && (documentChanged || st.anchor != o.Anchor || st.anchorVersion != o.AnchorVersion) {
		st.scrollTo = st.anchorNamed(strings.TrimPrefix(o.Anchor, "#"))
	}
	st.anchor, st.anchorVersion = o.Anchor, o.AnchorVersion
	st.fade = st.reveal.fader(c, o.Reveal)

	root.FontSize(th.FontSize).LineHeight(th.LineHeight).TextColor(th.Text)
	if th.Font != "" {
		root.Font(th.Font)
	}
	if o.ShareSelection {
		if outer := outerView(c); outer != nil {
			outer.nested = append(outer.nested, nestedView{st, outer.cur})
		}
	} else {
		root.Selectable()
		root.CopyData(st.copyData)
		root.ContextMenu(func(m *ui.Menu) {
			m.EditItems()
			if m.Item(l10n.In(c.Locale()).CopyAsMarkdown()).Chosen() {
				c.Services().WriteClipboardData(markdownData(st.copyMarkdown(root.SelectedTexts())))
			}
			if o.ContextMenu != nil {
				o.ContextMenu(c, m)
			}
		})
		if root.FocusWithin() && root.Shortcut(ui.Cmd|ui.Shift, ui.KeyC) {
			c.Services().WriteClipboardData(markdownData(st.copyMarkdown(root.SelectedTexts())))
		}
	}
	st.elems = st.elems[:0]
	clear(st.nested)
	st.nested, st.cur = st.nested[:0], 0
	st.numberNotes()
	root.Children(func() {
		prev := outerView(c)
		setBuilding(c, st)
		defer setBuilding(c, prev)
		st.blocks(c, 0, false, spanStyle{})
		st.footnotes(c)
	})
	if revealing || st.reveal.fading() {
		c.AnimationFrame()
	}
	return root
}

var defaultOptions Options

// theme resolves the theme of the frame.
func (st *viewState) theme(c *ui.Context) {
	t := c.Theme()
	if st.def == nil || st.uiTh != *t {
		st.uiTh = *t
		st.def = DefaultTheme(t)
	}
	th := resolve(st.o.Theme, st.def)
	if th != st.th {
		st.th = th
		clear(st.paras)
		for _, cs := range st.codes {
			cs.chunks = cs.chunks[:0]
		}
	}
	st.sb.d, st.sb.th = st.doc, &st.th
}

// forget drops what the view made of its blocks.
func (st *viewState) forget() {
	clear(st.paras)
	clear(st.codes)
	clear(st.tables)
	clear(st.tasks)
}

func (st *viewState) note(id uint64, block, line int) {
	st.elems = append(st.elems, elemInfo{id: id, block: block, line: line})
}

// blocks builds the children of block parent, spaced; tight tells the
// items of a tight list.
func (st *viewState) blocks(c *ui.Context, parent int, tight bool, base spanStyle) {
	d := st.doc
	th := &st.th
	first := true
	prevHeading := 0
	for j := parent + 1; j < d.Block(parent).Next; j = d.Block(j).Next {
		b := d.Block(j)
		if b.Kind == parser.FootnoteDefinition || b.Kind == parser.Paragraph && d.LineCount(j) == 0 {
			// Footnotes show at the end, and an alert's marker not at all.
			continue
		}
		top := float32(0)
		if !first {
			top = th.em(th.BlockSpacing)
			if tight {
				top = th.em(th.ItemSpacing)
			}
			if prevHeading > 0 {
				top = th.em(th.Headings[prevHeading-1].Bottom)
			}
			if b.Kind == parser.Heading {
				top = max(top, th.em(th.Headings[b.Level-1].Top))
			}
		}
		el := st.block(c, j, tight, base)
		st.fadeIn(el, b)
		if top > 0 {
			el.Margin(top, 0, 0, 0)
		}
		if st.scrollTo == j {
			el.ScrollIntoView()
			st.scrollTo = -1
		}
		prevHeading = 0
		if b.Kind == parser.Heading {
			prevHeading = b.Level
		}
		first = false
	}
}

// block builds block i.
func (st *viewState) block(c *ui.Context, i int, tight bool, base spanStyle) ui.Element {
	d := st.doc
	o := st.o
	th := &st.th
	b := d.Block(i)
	defer func(prev int) { st.cur = prev }(st.cur)
	st.cur = i
	if o.Block != nil {
		box := ui.Column(c)
		done := false
		box.Children(func() { done = o.Block(c, d, i) })
		if done {
			return box
		}
		return box.Children(func() { st.defaultBlock(c, i, tight, base) })
	}
	_ = b
	_ = th
	return st.defaultBlock(c, i, tight, base)
}

func (st *viewState) defaultBlock(c *ui.Context, i int, tight bool, base spanStyle) ui.Element {
	d := st.doc
	th := &st.th
	b := d.Block(i)
	switch b.Kind {
	case parser.SourcePrefix:
		return st.text(c, i, 0, []ui.Span{{Text: d.Source()[b.Start:b.End]}}).Font(th.CodeFont).TextColor(th.Muted)
	case parser.Paragraph:
		if imagesOnly(d.Inlines(i)) {
			return st.images(c, i)
		}
		return st.paragraph(c, i, base)
	case parser.Heading:
		h := th.Headings[b.Level-1]
		s := base
		s.weight, s.size, s.color = h.Weight, th.em(h.Size), h.Color
		if st.editor != nil {
			// Source and rendered headings share the same font metrics and
			// external rule box; decoration does not enlarge the text run.
			box := ui.Column(c).AlignSelf(ui.Stretch).Role(ui.RoleHeading).Level(b.Level)
			if h.Rule {
				box.BorderWidth(0, 0, 1, 0).BorderColor(th.Rule).Padding(0, 0, th.em(.3), 0)
			}
			return box.Children(func() {
				st.paragraph(c, i, s).FontSize(s.size).LineHeight(th.LineHeight).MinHeight(s.size * th.LineHeight)
			})
		}
		el := st.paragraph(c, i, s).Role(ui.RoleHeading).Level(b.Level)
		if h.Rule {
			el.BorderWidth(0, 0, 1, 0).BorderColor(th.Rule).Padding(0, 0, th.em(0.3), 0)
		}
		return el
	case parser.ThematicBreak:
		return ui.Box(c).Height(1).Background(th.Rule).AlignSelf(ui.Stretch).Margin(th.em(0.25), 0)
	case parser.CodeBlock:
		return st.codeBlock(c, i)
	case parser.HTMLBlock:
		html := strings.TrimRight(d.Lines(i), "\n")
		if st.o.HTML != nil {
			box := ui.Column(c)
			return box.Children(func() { st.o.HTML(c, html) })
		}
		el := st.text(c, i, 0, []ui.Span{{Text: html}}).Font(th.CodeFont).FontSize(th.em(th.CodeSize)).TextColor(th.Muted)
		st.note(el.ID(), i, 0)
		return el
	case parser.MathBlock:
		tex := strings.TrimRight(d.Lines(i), "\n")
		if st.o.Math != nil {
			box := ui.Column(c)
			return box.Children(func() { st.o.Math(c, tex) })
		}
		box := ui.ScrollHorizontal(c).AlignSelf(ui.Stretch)
		return box.Children(func() {
			el := st.text(c, i, 0, []ui.Span{{Text: tex}}).Font(th.CodeFont).FontSize(th.em(th.CodeSize)).TextColor(th.Muted).NoWrap().Margin(0, ui.Auto)
			st.note(el.ID(), i, 0)
		})
	case parser.Quote:
		if st.editor == nil && b.Alert != parser.NoAlert {
			return st.alert(c, i, b.Alert, base)
		}
		s := base
		s.color = th.QuoteText
		// The bar on the side lines start: the right right to left.
		q := ui.Column(c).BorderStartWidth(th.QuoteBarWidth).BorderColor(th.QuoteBar).PaddingStart(th.em(0.85)).TextColor(th.QuoteText)
		return q.Children(func() { st.blocks(c, i, false, s) })
	case parser.List:
		return st.list(c, i, base)
	case parser.Table:
		return st.table(c, i, base)
	}
	return ui.Box(c)
}

// alert builds a GitHub alert: a quote whose bar, icon and title take
// its kind's color, its text the color of text.
func (st *viewState) alert(c *ui.Context, i int, a parser.Alert, base spanStyle) ui.Element {
	th := &st.th
	color := th.Alerts[a-1]
	q := ui.Column(c).BorderStartWidth(th.QuoteBarWidth).BorderColor(color).PaddingStart(th.em(0.85))
	return q.Children(func() {
		title := ui.Row(c).AlignItems(ui.Center).Gap(th.em(0.5)).TextColor(color).Margin(0, 0, th.em(th.ItemSpacing*2), 0)
		title.Children(func() {
			if icon := alertIcon(a); icon != nil {
				ui.Icon(c, icon)
			}
			label := st.text(c, i, 0, []ui.Span{{Text: a.String(), Weight: th.StrongWeight}})
			st.note(label.ID(), i, -1)
		})
		st.blocks(c, i, false, base)
	})
}

// fadeIn fades in a block revealed a moment ago: text fades by its
// spans, and the rest of blocks, as a code block's box or a table's
// lines, by their opacity.
func (st *viewState) fadeIn(el ui.Element, b *parser.Block) {
	if st.fade == nil {
		return
	}
	switch b.Kind {
	case parser.Paragraph, parser.Heading, parser.List:
		return
	}
	if a := st.fade(b.Start); a < 1 {
		el.Opacity(a)
	}
}

// para returns the paragraph of block i in style base, built anew only
// when it changed.
func (st *viewState) para(i int, base spanStyle) *para {
	for len(st.paras) < st.doc.Len() {
		st.paras = append(st.paras, paraCache{})
	}
	b := st.doc.Block(i)
	st.doc.Inlines(i) // their revision
	pc := &st.paras[i]
	fading := st.fade != nil && st.fade(max(b.End-1, b.Start)) < 1
	if pc.ok && pc.start == b.Start && pc.end == b.End && pc.kind == b.Kind && pc.rev == b.Revision() && pc.base == base && !pc.p.fading && !fading && !b.Healed() && st.sameResolved(i, &pc.p) {
		return &pc.p
	}
	pc.start, pc.end, pc.kind, pc.rev, pc.base, pc.ok = b.Start, b.End, b.Kind, b.Revision(), base, true
	sb := &st.sb
	sb.block = i
	sb.fade = st.fade
	sb.notes = st.noteNumber
	sb.math = st.o.InlineMath
	// Editor ownership is stable across focus, source and read-only modes,
	// so their paragraph caches keep the same physical line projection.
	sb.preserveSoftBreaks = st.editor != nil
	sb.linkIcon = st.o.LinkIcon
	sb.resolve, sb.resolvers = nil, st.o.resolvers()
	if sb.resolvers != 0 {
		sb.resolve = st.o.resolveInline
	}
	sb.build(&pc.p, base)
	return &pc.p
}

// resolvers says which of Resolve and CodeLink are set, as bits.
func (o *Options) resolvers() uint8 {
	var r uint8
	if o.Resolve != nil {
		r |= 1
	}
	if o.CodeLink != nil {
		r |= 2
	}
	return r
}

// resolveInline asks where an inline goes: Resolve for a mention or a
// reference, CodeLink for code.
func (o *Options) resolveInline(in parser.Inline) (string, bool) {
	switch in.Kind {
	case parser.Mention, parser.Reference:
		if o.Resolve != nil {
			return o.Resolve(in)
		}
	case parser.Code:
		if o.CodeLink != nil {
			return o.CodeLink(in.Text)
		}
	}
	return "", false
}

// sameResolved reports whether the mentions, references and code of block i
// resolve where they did as p was built, Options.Resolve and CodeLink set
// or not alike.
func (st *viewState) sameResolved(i int, p *para) bool {
	if len(p.refs) == 0 {
		return true
	}
	if st.o.resolvers() != p.resolvers {
		return false
	}
	if p.resolvers == 0 {
		return true
	}
	in := st.doc.Inlines(i)
	for k, x := range p.refs {
		dest, ok := st.o.resolveInline(in[x])
		if !ok {
			dest = ""
		}
		if dest != p.resolved[k] {
			return false
		}
	}
	return true
}

// paragraph builds the text of block i: a paragraph, a heading or a cell.
func (st *viewState) paragraph(c *ui.Context, i int, base spanStyle) ui.Element {
	p := st.para(i, base)
	firstRun := 0
	if st.editor != nil {
		firstRun = len(st.editor.runs)
	}
	if st.editor != nil && (!st.editor.opts.RenderedLinks || !st.editor.blockHasLinks(i)) {
		// Keep an ordinary paragraph as one shaped text run. Its links retain
		// their styles; activation belongs to the editor's source selection.
		var spans []ui.Span
		mixed := false
		for _, pc := range p.pieces {
			mixed = mixed || st.blockPiece(i, &pc)
			spans = append(spans, pc.spans...)
		}
		if !mixed {
			el := st.text(c, i, 0, spans)
			paragraphObjects(el, p)
			st.note(el.ID(), i, -1)
			return el
		}
	}
	for _, pc := range p.pieces {
		if st.blockPiece(i, &pc) {
			return st.mixedParagraph(c, i, p.pieces)
		}
	}
	ps := p.pieces
	continuous := len(ps) > 0 && ps[0].link == -1 && !ps[0].image
	var el ui.Element
	offset := 0
	switch {
	case len(ps) == 0:
		el = ui.Text(c, "")
	case ps[0].link == -1 && !ps[0].image:
		el = st.text(c, i, 0, ps[0].spans)
		offset = spanBytes(ps[0].spans)
		ps = ps[1:]
	default:
		// An empty RichText has no shaped line to host its inline children.
		// A paragraph beginning with a link still needs a real layout box.
		el = ui.Row(c).Wrap().MinWidth(0).AlignItems(ui.Center)
	}
	if len(ps) > 0 {
		el.Children(func() {
			for k := range ps {
				if st.editor != nil && !st.editor.projectionCurrent() {
					return
				}
				st.piece(c, i, &ps[k], offset)
				offset += spanBytes(ps[k].spans)
			}
		})
	}
	if continuous && st.editor != nil && st.editor.opts.RenderedLinks && st.editor.projectionCurrent() {
		// A RichText paragraph paints all inline descendants as one shaped
		// paragraph. Its full source mapping must live on the same element.
		var spans []ui.Span
		caretStart, caretEnd := st.editor.runs[firstRun].caretStart, st.editor.runs[firstRun].caretEnd
		for _, run := range st.editor.runs[firstRun:] {
			spans = append(spans, run.spans...)
			caretStart, caretEnd = min(caretStart, run.caretStart), max(caretEnd, run.caretEnd)
		}
		st.editor.runs = st.editor.runs[:firstRun]
		st.editor.observeText(el, i, 0, spans)
		run := st.editor.runs[len(st.editor.runs)-1]
		run.caretStart, run.caretEnd = caretStart, caretEnd
	}
	paragraphObjects(el, p)
	st.note(el.ID(), i, -1)
	return el
}

func paragraphObjects(el ui.Element, p *para) {
	if objects := p.objects; len(objects) > 0 {
		el.DrawObjects(func(pa *ui.Painter, at int, r ui.Rect) {
			if k, ok := slices.BinarySearchFunc(objects, at, func(o mathObject, at int) int { return o.rune - at }); ok && objects[k].draw != nil {
				objects[k].draw(pa, r)
			}
		})
	}
}

func (st *viewState) blockPiece(block int, pc *piece) bool {
	if pc.image && st.editor != nil && st.editor.opts.InPlaceImages {
		return true
	}
	if pc.image && st.o.Image != nil {
		return true
	}
	if pc.link >= 0 && st.o.BlockLink != nil {
		in := st.doc.Inlines(block)[pc.link]
		return st.o.BlockLink(&in)
	}
	return false
}

// Native text only accepts text children. Business media/card adapters live
// between complete framework paragraphs; their ordinary inline runs still
// share one shaped text, including emphasis/link boundaries.
func (st *viewState) mixedParagraph(c *ui.Context, block int, ps []piece) ui.Element {
	row := ui.Row(c).Wrap().AlignItems(ui.Start).AlignSelf(ui.Stretch)
	return row.Children(func() {
		offset, runes := 0, 0
		for k := 0; k < len(ps); {
			if st.blockPiece(block, &ps[k]) {
				st.piece(c, block, &ps[k], offset)
				for _, sp := range ps[k].spans {
					if !ps[k].image || st.editor != nil {
						offset += len(sp.Text)
					}
					runes += utf8.RuneCountInString(sp.Text)
				}
				k++
				continue
			}
			end := k + 1
			for end < len(ps) && !st.blockPiece(block, &ps[end]) {
				end++
			}
			group := ui.RichText(c)
			st.note(group.ID(), block, -1)
			st.elems[len(st.elems)-1].runeOffset = runes
			group.Children(func() {
				for j := k; j < end; j++ {
					st.piece(c, block, &ps[j], offset)
					for _, sp := range ps[j].spans {
						offset += len(sp.Text)
						runes += utf8.RuneCountInString(sp.Text)
					}
				}
			})
			k = end
		}
	})
}

// piece builds a link, or text after one, within a paragraph.
func spanBytes(spans []ui.Span) int {
	n := 0
	for _, s := range spans {
		n += len(s.Text)
	}
	return n
}

func (st *viewState) text(c *ui.Context, block, offset int, spans []ui.Span) ui.Element {
	if st.o.TransformText != nil {
		spans = st.o.TransformText(block, offset, spans)
	}
	el := ui.RichText(c, spans...)
	if st.o.OnText != nil {
		st.o.OnText(c, el, block, offset, spans)
	}
	if st.editor != nil {
		st.editor.observeText(el, block, offset, spans)
	}
	return el
}

func (st *viewState) piece(c *ui.Context, block int, pc *piece, offset int) {
	if pc.image && (st.o.Image != nil || st.editor != nil && st.editor.opts.InPlaceImages) {
		in := st.doc.Inlines(block)[pc.imageIndex]
		if st.editor != nil && st.editor.opts.InPlaceImages {
			st.editor.imageView(c, block, &in, parser.PlainText(st.doc.Inlines(block)[pc.imageIndex+1:in.Next]))
			return
		}
		st.o.Image(c, block, in.Start, in.End, in.Dest, parser.PlainText(st.doc.Inlines(block)[pc.imageIndex+1:in.Next]))
		return
	}
	switch {
	case pc.link == -1:
		st.text(c, block, offset, pc.spans)
	case pc.link < -1:
		// A footnote reference: it scrolls to the footnote.
		in := st.doc.Inlines(block)[-2-pc.link]
		l := ui.Link(c, "", "")
		l.Children(func() { st.text(c, block, offset, pc.spans) })
		if l.Clicked() {
			if i, ok := st.doc.Footnote(in.Text); ok {
				st.scrollTo = i
			}
		}
	default:
		in := st.doc.Inlines(block)[pc.link]
		if pc.dest != "" {
			// A mention or a reference, which Options.Resolve made a link,
			// or code, which Options.CodeLink did.
			in.Dest, in.Auto = pc.dest, true
		}
		st.link(c, &in, block, offset, func() { st.text(c, block, offset, pc.spans) })
	}
}

// link builds a link to in.Dest whose text children builds.
func (st *viewState) link(c *ui.Context, in *parser.Inline, block, offset int, children func()) {
	if st.o.LinkView != nil && st.o.LinkView(c, in, block, offset, children) {
		return
	}
	if st.o.Link != nil && st.o.Link(c, in, block, offset) {
		return
	}
	dest := in.Dest
	rootClick := false
	if st.editor != nil && st.editor.opts.RenderedLinks {
		_, rootClick = st.editor.LinkClicked(in)
	}
	var l ui.Element
	switch {
	case in.Pending:
		l = ui.Link(c, "", "")
	case st.o.OnLink != nil:
		l = ui.Link(c, "", "")
		if l.Clicked() || rootClick {
			st.o.OnLink(dest)
		}
	case strings.HasPrefix(dest, "#"):
		l = ui.Link(c, "", "")
		if l.Clicked() || rootClick {
			st.scrollTo = st.anchorNamed(dest[1:])
		}
	case linkAllowed(dest, c.Router() != nil):
		l = ui.Link(c, "", dest)
		if rootClick {
			if router := c.Router(); router != nil && !strings.Contains(dest, ":") {
				router.Push(dest)
			} else {
				c.OpenURL(dest)
			}
		}
	default:
		l = ui.Link(c, "", "")
	}
	if st.o.LinkMenu != nil && !in.Pending && dest != "" && !strings.HasPrefix(dest, "#") {
		l.ContextMenu(func(m *ui.Menu) { st.o.LinkMenu(m, dest) })
	}
	l.TextColor(st.th.Link)
	if st.editor != nil && st.editor.opts.RenderedLinks {
		st.editor.LinkLabel(l, children, in)
	} else {
		l.Children(children)
	}
	// What the tip says, which describes the link to assistive technology.
	tip := in.Title
	if tip == "" && !in.Auto && !in.Pending && dest != "" && !strings.HasPrefix(dest, "#") {
		tip = dest
	}
	if st.o.Preview != nil && !in.Pending && dest != "" {
		if fill := st.o.Preview(c, dest); fill != nil {
			if tip != "" {
				l.Description(tip)
			}
			ui.TooltipBase(c, l, fill)
			return
		}
	}
	if tip != "" {
		l.Tooltip(tip)
	}
}

// linkAllowed reports whether a link's destination opens without
// OnLink: one ui.SafeToOpen allows, or, in a router, a path without a
// scheme, which goes there. Outside a router, a path would open as a file
// or a program, so it does nothing.
func linkAllowed(dest string, router bool) bool {
	if strings.ContainsAny(dest, "\\\x00") || strings.HasPrefix(dest, "//") {
		return false
	}
	if i := strings.IndexAny(dest, ":/?#"); i < 0 || dest[i] != ':' {
		return router
	}
	return ui.SafeToOpen(dest)
}

// anchorNamed returns the block to scroll to for the anchor name: the one
// Options.AnchorBlock finds, else the heading of that slug, or -1.
func (st *viewState) anchorNamed(name string) int {
	if find := st.o.AnchorBlock; find != nil {
		d := st.doc
		if i := find(d, name); i > 0 && i < d.Len() {
			// A table shows its rows and cells itself.
			for k := d.Block(i).Kind; k == parser.TableRow || k == parser.TableCell; k = d.Block(i).Kind {
				i = d.Block(i).Parent
			}
			return i
		}
	}
	return st.headingNamed(name)
}

// headingNamed returns the heading whose slug, as GitHub makes it, is
// name, or -1.
func (st *viewState) headingNamed(name string) int {
	d := st.doc
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.Heading && Slug(parser.PlainText(d.Inlines(i))) == strings.ToLower(name) {
			return i
		}
	}
	return -1
}

// Slug returns the anchor of a heading of text, as GitHub makes it: in
// lower case, spaces as hyphens, without punctuation.
func Slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || isAlnum(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// list builds a list.
func (st *viewState) list(c *ui.Context, i int, base spanStyle) ui.Element {
	d := st.doc
	th := &st.th
	b := d.Block(i)
	depth := 0
	for p := b.Parent; p > 0; p = d.Block(p).Parent {
		if d.Block(p).Kind == parser.List {
			depth++
		}
	}
	width := th.em(th.ListIndent)
	if b.Ordered {
		last := b.Number + max(0, st.countChildren(i)-1)
		w, _ := c.MeasureText(0, ui.Span{Text: itoa(last) + string(b.Marker), Font: th.Font, Size: th.FontSize})
		width = max(width, w+th.em(0.5))
	}
	col := ui.Column(c).Role(ui.RoleList)
	return col.Children(func() {
		n := b.Number
		first := true
		for j := i + 1; j < d.Block(i).Next; j = d.Block(j).Next {
			row := ui.Row(c).AlignItems(ui.Start).Role(ui.RoleListItem)
			if !first {
				gap := th.BlockSpacing
				if b.Tight {
					gap = th.ItemSpacing
				}
				row.Margin(th.em(gap), 0, 0, 0)
			}
			if st.scrollTo == j {
				// An item an anchor named (Options.AnchorBlock).
				row.ScrollIntoView()
				st.scrollTo = -1
			}
			num := n
			row.Children(func() {
				st.marker(c, b, j, num, depth, width)
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() { st.blocks(c, j, b.Tight, base) })
			})
			n++
			first = false
		}
	})
}

func (st *viewState) countChildren(i int) int {
	n := 0
	for j := i + 1; j < st.doc.Block(i).Next; j = st.doc.Block(j).Next {
		n++
	}
	return n
}

// marker builds the marker of item j of list: a bullet, its number, or its
// check box.
func (st *viewState) marker(c *ui.Context, list *parser.Block, j, n, depth int, width float32) {
	th := &st.th
	item := st.doc.Block(j)
	box := ui.Row(c).Width(width).Shrink(0).Unselectable()
	if st.fade != nil {
		if a := st.fade(item.Start); a < 1 {
			box.Opacity(a)
		}
	}
	box.Children(func() {
		switch {
		case item.Task != parser.NoTask:
			size := th.em(0.95)
			lineH := th.FontSize * th.LineHeight * 1.2
			checked := item.Task == parser.Checked
			cb := ui.Box(c).Size(size, size).Radius(max(3, size/5)).Margin(max(0, (lineH-size)/2-1), 0, 0, 0).Role(ui.RoleCheckBox).Checked(checked)
			if onTask := st.o.OnTask; onTask != nil {
				// A control, named by its task, which asks the app for
				// the other state: the source decides what it shows.
				cb.Focusable().Label(st.taskName(j))
				if cb.Clicked() {
					onTask(lineAt(st.doc.Source(), item.Start), !checked)
				}
			} else {
				cb.ReadOnly(true)
			}
			if checked {
				cb.Background(th.Checkbox).Draw(func(p *ui.Painter, r ui.Rect) {
					path := &ui.Path{}
					path.MoveTo(r.X+r.W*0.25, r.Y+r.H*0.52)
					path.LineTo(r.X+r.W*0.43, r.Y+r.H*0.7)
					path.LineTo(r.X+r.W*0.76, r.Y+r.H*0.32)
					p.StrokePath(path, max(1.5, r.W/9), ui.RGB(255, 255, 255))
				})
			} else {
				cb.Border(1.5, th.Muted)
			}
		case list.Ordered:
			ui.Text(c, itoa(n)+string(list.Marker)).TextColor(th.Muted).Width(width - th.em(0.4)).TextAlign(ui.End).FontFeatures("tnum")
		default:
			bullets := [...]string{"•", "◦", "▪"}
			ui.Text(c, bullets[min(depth, 2)]).TextColor(th.Muted).Width(width - th.em(0.5)).TextAlign(ui.Center)
		}
	})
}

// taskLabel is the name of a task's check box: the text of its item's
// first paragraph, kept while that paragraph stays as it was, so that a
// steady frame does not make it again.
type taskLabel struct {
	start, end int
	rev        uint32
	ok         bool
	text       string
}

// taskName returns the name of the check box of item j.
func (st *viewState) taskName(j int) string {
	d := st.doc
	k := j + 1
	if k >= d.Block(j).Next || d.Block(k).Kind != parser.Paragraph {
		return ""
	}
	for len(st.tasks) < d.Len() {
		st.tasks = append(st.tasks, taskLabel{})
	}
	in := d.Inlines(k) // their revision
	b := d.Block(k)
	tn := &st.tasks[j]
	if !tn.ok || tn.start != b.Start || tn.end != b.End || tn.rev != b.Revision() {
		*tn = taskLabel{b.Start, b.End, b.Revision(), true, parser.PlainText(in)}
	}
	return tn.text
}

// lineAt returns the line of src that byte off is on, from 1. Lines end
// with \n, \r\n or \r, as CommonMark's do.
func lineAt(src string, off int) int {
	s := src[:off]
	n := 1 + strings.Count(s, "\n")
	if strings.IndexByte(s, '\r') >= 0 {
		n += strings.Count(s, "\r") - strings.Count(s, "\r\n")
		if s[len(s)-1] == '\r' && off < len(src) && src[off] == '\n' {
			n-- // off is within the line's end
		}
	}
	return n
}

// numberNotes numbers the footnotes by the order of their definitions.
func (st *viewState) numberNotes() {
	clear(st.notes)
	n := 0
	for i := 1; i < st.doc.Len(); i++ {
		if b := st.doc.Block(i); b.Kind == parser.FootnoteDefinition {
			if st.notes == nil {
				st.notes = map[string]int{}
			}
			key := parser.NormalizeLabel(b.Label())
			if _, ok := st.notes[key]; !ok {
				n++
				st.notes[key] = n
			}
		}
	}
}

func (st *viewState) noteNumber(label string) int { return st.notes[parser.NormalizeLabel(label)] }

// footnotes builds the footnotes, below a rule, in order.
func (st *viewState) footnotes(c *ui.Context) {
	if len(st.notes) == 0 {
		return
	}
	d := st.doc
	th := &st.th
	ui.Box(c).Height(1).Background(th.Rule).AlignSelf(ui.Stretch).Margin(th.em(1.2), 0, th.em(0.6), 0)
	base := spanStyle{color: th.Muted, size: th.em(0.875)}
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.FootnoteDefinition {
			continue
		}
		num := st.notes[parser.NormalizeLabel(b.Label())]
		row := ui.Row(c).AlignItems(ui.Start).Margin(th.em(0.25), 0, 0, 0)
		if st.scrollTo == i {
			row.ScrollIntoView()
			st.scrollTo = -1
		}
		row.Children(func() {
			ui.Text(c, itoa(num)+".").TextColor(th.Muted).FontSize(th.em(0.875)).Width(th.em(1.6)).Shrink(0).Unselectable()
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).TextColor(th.Muted).Children(func() { st.blocks(c, i, false, base) })
		})
	}
}
