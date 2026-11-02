package markdownui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type linkEdit struct {
	open                                bool
	version                             uint64
	start, end                          int
	label, url, originalLabel, rawLabel string
	originalURL                         string
	errorText                           string
}

// MarkdownEditor uses one native Buffer for live editing and source editing.
// Preview reads that same version through the shared reader.
func MarkdownEditor(c *ui.Context, s *EditorState, options EditorOptions) ui.Element {
	if s == nil || s.closed {
		return ui.Text(c, "Markdown 编辑暂不可用。")
	}
	s.options = options
	s.reader.propertiesInteraction = s.native.MarkUserInteraction
	services := c.Services()
	s.reader.onModeChanged = func(mode Mode) { s.report(s.SetMode(mode)) }
	document := s.document
	document.Source = s.native.Buffer().Snapshot().String()
	s.reader.SetDocument(document)
	if s.Mode() != Preview {
		s.reader.SetMode(Source)
	} else {
		s.reader.SetMode(Preview)
	}
	root := ui.Column(c.Key("markdown-editor-" + s.document.ID)).FillWidth().MinWidth(0).MinHeight(0)
	root.Draw(func(_ *ui.Painter, r ui.Rect) {
		if r.W > 0 && s.reader.properties.viewportWidth != r.W {
			s.reader.properties.viewportWidth = r.W
			c.Invalidate()
		}
	})
	if !options.ReadOnly && s.Mode() == Live && options.OnImportResources != nil {
		if files := root.DroppedFiles(); len(files) > 0 {
			s.importResources(ImportRequest{Files: files})
		}
	}
	root.OnShortcut(ui.Cmd, ui.KeyH, func() {
		if !options.ReadOnly && s.Mode() != Preview {
			s.native.MarkUserInteraction()
			s.reader.find.Open = true
			s.reader.find.ReplaceOpen = true
			s.reader.focusFind = true
		}
	})
	root.OnShortcut(ui.Cmd, ui.KeyK, func() { s.report(s.Execute(EditorCommand{Kind: InsertLink})) })
	root.OnShortcut(ui.Cmd, ui.KeyBackquote, func() { s.report(s.Execute(EditorCommand{Kind: InlineCode})) })
	if !options.ContentOnly {
		root.AlignSelf(ui.Stretch)
	}
	save := func() {
		if s.closed || options.ReadOnly {
			return
		}
		if snapshot, err := s.CaptureInput(); err != nil {
			s.report(err)
		} else if options.OnSave != nil {
			options.OnSave(snapshot)
		}
	}
	root.OnShortcut(ui.Cmd, ui.KeyS, save)
	if options.Native == nil || options.Native.ContextMenu == nil {
		root.OnShortcut(ui.Cmd, ui.KeyF, func() { s.native.MarkUserInteraction(); s.reader.find.Open = true; s.reader.focusFind = true })
		root.OnShortcut(0, ui.KeyF3, func() { s.reader.moveFind(c, 1) })
		root.OnShortcut(ui.Shift, ui.KeyF3, func() { s.reader.moveFind(c, -1) })
	}
	if s.Mode() == Preview {
		readerOptions := options.ReaderOptions
		baseMenu := readerOptions.ContextMenu
		readerOptions.ContextMenu = func(ctx *ui.Context, menu *ui.Menu) {
			if baseMenu != nil {
				baseMenu(ctx, menu)
			}
			s.modeMenu(menu)
		}
		return root.Children(func() { MarkdownReader(c, s.reader, readerOptions).Grow(1) })
	}
	readerOptions := options.ReaderOptions
	readerOptions.ContentOnly = true
	native := s.reader.prepare(c, readerOptions)
	if !options.ReadOnly && s.Mode() == Live {
		s.reader.configureProperties(native, s.ApplyTagChange, func() uint64 { return s.Snapshot().Version })
	}
	baseLinkView := native.LinkView
	native.LinkView = func(ctx *ui.Context, in *parser.Inline, block, offset int, children func()) bool {
		if baseLinkView != nil && baseLinkView(ctx, in, block, offset, children) {
			return true
		}
		if options.ReadOnly {
			return false
		}
		return s.linkView(ctx, in, children, native)
	}
	baseMenu := native.ContextMenu
	native.ContextMenu = func(ctx *ui.Context, menu *ui.Menu) {
		if baseMenu != nil {
			baseMenu(ctx, menu)
		}
		s.commandMenu(menu)
		menu.Separator()
		if (options.Native == nil || options.Native.ContextMenu == nil) && menu.Item("查找正文 · Ctrl+F").Chosen() {
			s.reader.find.Open = true
			s.reader.focusFind = true
		}
		s.modeMenu(menu)
	}
	root.Children(func() {
		if s.reader.find.Open {
			s.reader.renderFind(c)
			s.renderReplace(c)
		}
		surface := md.Editor(c.Key("native-editor"), s.native, md.EditorOptions{
			Options: native, Label: options.Label, ReadOnly: options.ReadOnly, InPlaceTables: true, RenderedLinks: true, InPlaceCode: true, ContinueBlocks: true, InteractiveTasks: true, SmartEditing: true, ConfirmCodeFenceOnEnter: true,
			SourceHighlights: s.sourceFindHighlights(document.Source),
			OnChange:         s.notify, OnError: s.report, OnCompositionChanged: options.OnCompositionChanged,
			OnPaste: func(plain bool) bool { return s.pasteContent(services, plain) },
			OnSave:  save,
		}).Grow(1).FillWidth().MinHeight(0)
		s.floatingToolbar(c)
		s.insertPopover(c, surface)
		if dialog := s.insert; dialog != nil && !dialog.open && pendingInsertInput(dialog) {
			// Dismissing a popover outside its buttons cannot discard a draft.
			// Explicit cancel/apply clear s.insert in the popover itself.
			dialog.open = true
			c.Invalidate()
		}
	})
	s.notify()
	return root
}

// CaptureInput settles input before the host submits, replaces or closes a
// draft. Source comes directly from the shared Buffer, including original line
// endings. Unconfirmed popover edits stay with this owner until explicitly
// applied or cancelled by the user.
func (s *EditorState) CaptureInput() (Snapshot, error) {
	if s == nil || s.closed {
		return Snapshot{}, ErrClosed
	}
	if !s.native.EndComposition() || s.native.Composing() {
		return s.Snapshot(), ErrComposing
	}
	s.notify()
	if pendingLinkInput(s.link) {
		return s.Snapshot(), fmt.Errorf("请先保存或取消当前链接编辑，再继续")
	}
	if pendingInsertInput(s.insert) {
		return s.Snapshot(), fmt.Errorf("请先插入或取消当前链接或图片，再继续")
	}
	if pendingPropertyTagInput(s.reader.properties.dialog) {
		return s.Snapshot(), fmt.Errorf("请先保存或取消当前标签编辑，再继续")
	}
	return s.Snapshot(), nil
}

func pendingLinkInput(dialog *linkEdit) bool {
	return dialog != nil && (dialog.errorText != "" || dialog.label != dialog.originalLabel || dialog.url != dialog.originalURL)
}

func pendingInsertInput(dialog *insertDialog) bool {
	return dialog != nil && (dialog.errorText != "" || dialog.text != dialog.originalText || strings.TrimSpace(dialog.target) != "")
}

// ImportResources lets a host's explicit image action use the same insertion
// point, history and stale-completion checks as paste and the insert popover.
func (s *EditorState) ImportResources(request ImportRequest) {
	if s == nil || s.closed || s.options.ReadOnly || s.options.OnImportResources == nil {
		return
	}
	if s.Mode() != Live {
		s.report(fmt.Errorf("请先切换到实时编辑再插入图片"))
		return
	}
	if _, err := s.CaptureInput(); err != nil {
		s.report(err)
		return
	}
	s.importResources(request)
}

func (s *EditorState) modeMenu(menu *ui.Menu) {
	for _, item := range []struct {
		label string
		mode  Mode
	}{{"实时编辑", Live}, {"只读预览", Preview}, {"查看 Markdown 源码", Source}} {
		if menu.Item(item.label).Disabled(s.Mode() == item.mode || s.options.ReadOnly && item.mode == Live).Chosen() {
			if _, err := s.CaptureInput(); err != nil {
				s.report(err)
			} else {
				s.report(s.SetMode(item.mode))
			}
		}
	}
}

func (s *EditorState) linkView(c *ui.Context, in *parser.Inline, children func(), native *md.Options) bool {
	link := ui.Link(c, "", "").Tooltip(in.Dest)
	if native.Theme != nil {
		link.TextColor(native.Theme.Link)
	}
	s.native.LinkLabel(link, children, in)
	modifiers, clicked := s.native.LinkClicked(in)
	if link.Clicked() {
		modifiers, clicked = link.ClickModifiers(), true
	}
	if clicked {
		if modifiers&(ui.Ctrl|ui.Super) != 0 {
			if native.OnLink != nil {
				native.OnLink(in.Dest)
			}
		} else if pendingLinkInput(s.link) {
			s.link.open = true
			s.report(fmt.Errorf("请先保存或取消当前链接编辑，再继续"))
		} else if s.native.EndComposition() {
			snapshot := s.native.Buffer().Snapshot()
			source := snapshot.String()
			label := PlainLinkLabel(source, *in)
			rawLabel := escapeLabel(label)
			if raw, ok := parser.LinkLabelSource(source, *in); ok {
				rawLabel = snapshot.OriginalSlice(in.Start+1, in.Start+1+len(raw))
			}
			s.link = &linkEdit{open: true, version: s.native.Buffer().Version(), start: in.Start, end: in.End, label: label, originalLabel: label, rawLabel: rawLabel, url: in.Dest, originalURL: in.Dest}
		}
	}
	dialog := s.link
	if dialog == nil || !dialog.open || dialog.start != in.Start || dialog.end != in.End {
		return true
	}
	ui.Popover(c, link, &dialog.open, func() {
		ui.Column(c.Key("link-editor")).Width(350).MaxWidth(450).Padding(12).Gap(8).Children(func() {
			ui.Text(c, "编辑链接").FontWeight(600)
			ui.TextInput(c.Key("label"), &dialog.label).Label("显示文字").Placeholder("显示文字").FillWidth().Height(34)
			ui.TextInput(c.Key("url"), &dialog.url).Label("链接地址").Placeholder("网址或文件路径").FillWidth().Height(34)
			if dialog.errorText != "" {
				ui.Text(c, dialog.errorText).TextColor(ui.Hex("#c43d3d")).FontSize(12)
			}
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c, "打开").Clicked() {
					if native.OnLink != nil {
						native.OnLink(dialog.url)
					}
				}
				if ui.Button(c, "移除链接").Clicked() {
					s.applyLink(dialog, true)
				}
				ui.Spacer(c)
				if ui.Button(c, "取消").Clicked() {
					dialog.open = false
					s.link = nil
				}
				if ui.Button(c, "保存").Clicked() {
					s.applyLink(dialog, false)
				}
			})
		})
	})
	if s.link == dialog && !dialog.open {
		if pendingLinkInput(dialog) {
			dialog.open = true
			c.Invalidate()
		} else {
			s.link = nil
		}
	}
	return true
}

func (s *EditorState) applyLink(dialog *linkEdit, remove bool) {
	if s.closed || s.options.ReadOnly {
		dialog.errorText = "当前文档只读"
		return
	}
	if s.native.Buffer().Version() != dialog.version {
		dialog.errorText = ErrStale.Error()
		return
	}
	label := dialog.rawLabel
	if !remove && dialog.label == dialog.originalLabel && dialog.url == dialog.originalURL {
		dialog.open = false
		s.link = nil
		s.native.Focus()
		return
	}
	if dialog.label != dialog.originalLabel {
		label = escapeLabel(dialog.label)
	}
	replacement := label
	start, end := dialog.start, dialog.end
	if remove {
		edit, err := unlinkInContext(s.native.Buffer().Snapshot(), start, end, label)
		if err != nil {
			dialog.errorText = err.Error()
			return
		}
		start, end, replacement = edit.start, edit.end, edit.text
	} else {
		if _, err := ResolveLink(s.document.Path, dialog.url); err != nil {
			dialog.errorText = err.Error()
			return
		}
		replacement = "[" + label + "](<" + parser.EscapeLinkDestination(strings.ReplaceAll(strings.TrimSpace(dialog.url), "|", "%7C")) + ">)"
	}
	if err := s.native.ReplaceRange(start, end, replacement, dialog.version); err != nil {
		dialog.errorText = fmt.Sprint(err)
		return
	}
	dialog.open = false
	s.link = nil
	s.native.Focus()
	s.notify()
}

// A removed link may have a URL, email or www label. Empty inline tags split
// only the auto-linked text nodes, preserving the visible label and its
// Markdown emphasis/code styling without letting GFM create another link.
func unlinkLabel(label string) string {
	const prefix = "x " // keep leading label punctuation inside a paragraph
	d := parser.Parse(prefix + label)
	var breaks []int
	for block := 1; block < d.Len(); block++ {
		for _, in := range d.Inlines(block) {
			if in.Kind != parser.Link || !in.Auto || in.Start < len(prefix) || in.End > len(prefix)+len(label) {
				continue
			}
			start, end := in.Start-len(prefix), in.End-len(prefix)
			raw := label[start:end]
			before := len(breaks)
			addBreak := func(at int) {
				for at > 0 && raw[at-1] == '\\' {
					at--
				}
				if at > 0 && at < len(raw) {
					breaks = append(breaks, start+at)
				}
			}
			for i := 0; i < len(raw); i++ {
				if raw[i] == '@' || raw[i] == ':' {
					addBreak(i)
				}
				if strings.HasPrefix(raw[i:], "www.") {
					addBreak(i + 1)
				}
				if raw[i] == '&' {
					if n, value := parser.Entity(raw[i:]); n > 0 {
						if value == "@" {
							addBreak(i)
						}
						i += n - 1
					}
				}
			}
			if len(breaks) == before {
				_, at := utf8.DecodeRuneInString(raw)
				addBreak(at)
			}
		}
	}
	sort.Ints(breaks)
	for i := len(breaks) - 1; i >= 0; i-- {
		if i == len(breaks)-1 || breaks[i] != breaks[i+1] {
			at := breaks[i]
			label = label[:at] + "<i></i>" + label[at:]
		}
	}
	return label
}
