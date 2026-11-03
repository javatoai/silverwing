package markdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// ImageInfo identifies one source occurrence, not every use of a destination.
// Coordinates are normalized UTF-8 bytes and valid only at Version.
type ImageInfo struct {
	Start, End, Block       int
	Version                 uint64
	Destination, Alt, Title string
}

type editorImageHit struct {
	info ImageInfo
	rect ui.Rect
}
type imageDrag struct {
	owner *EditorState
	info  ImageInfo
}

func (s *EditorState) blockHasImages(block int) bool {
	for _, in := range s.view.doc.Inlines(block) {
		if in.Kind == parser.Image {
			return true
		}
	}
	return false
}

func (s *EditorState) Images() []ImageInfo {
	s.sync()
	d := s.editingDocument()
	var images []ImageInfo
	for block := 1; block < d.Len(); block++ {
		inlines := d.Inlines(block)
		for i, in := range inlines {
			if in.Kind == parser.Image {
				images = append(images, ImageInfo{Start: in.Start, End: in.End, Block: block, Version: s.snapshot.Version(), Destination: in.Dest, Alt: parser.PlainText(inlines[i+1 : in.Next]), Title: in.Title})
			}
		}
	}
	return images
}

func (s *EditorState) validImage(info ImageInfo) (ImageInfo, error) {
	if s.opts.ReadOnly || s.SourceMode || s.composing {
		return ImageInfo{}, errors.New("当前图片不可编辑")
	}
	if s.buffer.Version() != info.Version {
		return ImageInfo{}, errors.New("文档已变化，请重新选择图片")
	}
	for _, current := range s.Images() {
		if current.Start == info.Start && current.End == info.End && current.Destination == info.Destination {
			return current, nil
		}
	}
	return ImageInfo{}, errors.New("图片引用已变化，请重新选择")
}

func (s *EditorState) SelectedImage() (ImageInfo, bool) {
	if !s.opts.InPlaceImages || s.SourceMode {
		return ImageInfo{}, false
	}
	s.sync()
	if s.anchor == s.caret {
		return ImageInfo{}, false
	}
	for _, info := range s.Images() {
		if min(s.anchor, s.caret) == info.Start && max(s.anchor, s.caret) == info.End {
			return info, true
		}
	}
	return ImageInfo{}, false
}

func (s *EditorState) SelectImage(info ImageInfo) error {
	if !s.EndComposition() {
		return errors.New("请先完成当前输入")
	}
	current, err := s.validImage(info)
	if err != nil {
		return err
	}
	s.SetSelection(current.Start, current.End)
	s.reveal = false
	s.Focus()
	return nil
}

func (s *EditorState) imageLabelSource(info ImageInfo) (string, int, bool) {
	d := s.editingDocument()
	for index, in := range d.Inlines(info.Block) {
		if in.Kind == parser.Image && in.Start == info.Start && in.End == info.End {
			if start, end, ok := d.ImageLabelRange(info.Block, index); ok {
				return s.source[start:end], end, true
			}
		}
	}
	return "", 0, false
}

func imageEscapeLabel(text string) string {
	return strings.NewReplacer("\\", "\\\\", "&", "&amp;", "|", "&#124;", "[", "&#91;", "]", "&#93;", "*", "&#42;", "_", "&#95;", "~", "&#126;", string(rune(96)), "&#96;", "<", "&lt;", ">", "&gt;", "\r\n", " ", "\n", " ", "\r", " ").Replace(text)
}

// ReplaceImage preserves an unchanged raw label/title and patches one use.
// Reference-style uses are detached rather than changing a shared definition.
func (s *EditorState) ReplaceImage(info ImageInfo, target, alt string) error {
	current, err := s.validImage(info)
	if err != nil {
		return err
	}
	if current.Destination == target && current.Alt == alt {
		return nil
	}
	label, close, ok := s.imageLabelSource(current)
	if !ok {
		return errors.New("图片源码无法识别")
	}
	if current.Alt != alt {
		label = imageEscapeLabel(alt)
	}
	title := ""
	if current.Title != "" {
		title = " \"" + strings.NewReplacer("&", "&amp;", "|", "&#124;", "\r", "&#13;", "\n", "&#10;", "\\", "\\\\", "\"", "\\\"").Replace(current.Title) + "\""
	}
	// Preserve the original title spelling when a direct image has one.
	if current.Title != "" && strings.HasPrefix(strings.TrimSpace(s.source[close+1:current.End]), "(") {
		raw := s.source[close+1 : current.End]
		at := len(raw) - 2
		for at > 0 && strings.ContainsRune(" \t\r\n", rune(raw[at])) {
			at--
		}
		if at > 0 && (raw[at] == '"' || raw[at] == '\'') {
			quote := raw[at]
			for begin := at - 1; begin > 0; begin-- {
				if raw[begin] != quote {
					continue
				}
				slashes := 0
				for k := begin - 1; k >= 0 && raw[k] == '\\'; k-- {
					slashes++
				}
				if slashes%2 == 0 {
					title = " " + raw[begin:at+1]
					break
				}
			}
		}
	}
	replacement := "![" + label + "](<" + parser.EscapeLinkDestination(strings.ReplaceAll(strings.TrimSpace(target), "|", "%7C")) + ">" + title + ")"
	err = s.ApplyEdits([]editor.Edit{{Start: current.Start, End: current.End, Text: replacement}}, current.Start, current.Start+len(normalizedText(replacement)), current.Version)
	s.reveal = false
	return err
}

func recognizedImageTail(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return true
	}
	if !strings.HasPrefix(tail, "<!--") || !strings.HasSuffix(tail, "-->") {
		return false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tail, "<!--"), "-->"))
	if strings.HasPrefix(inner, "image:") {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(strings.TrimPrefix(inner, "image:")), &fields) != nil || len(fields) == 0 {
			return false
		}
		for key := range fields {
			if key != "width" && key != "height" && key != "uuid" {
				return false
			}
		}
		return true
	}
	if len(inner) != 36 {
		return false
	}
	for i, ch := range inner {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
		} else if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}

func (s *EditorState) imageBlock(info ImageInfo) (int, int, bool) {
	d := s.editingDocument()
	b := d.Block(info.Block)
	if b.Kind != parser.Paragraph {
		return info.Start, info.End, false
	}
	start, end := s.blockRange(b)
	// The block content may start after a list/quote prefix.
	if strings.TrimSpace(s.source[b.Start:info.Start]) != "" || !recognizedImageTail(s.source[info.End:min(b.End, len(s.source))]) {
		return info.Start, info.End, false
	}
	return start, end, true
}

func (s *EditorState) DeleteImage(info ImageInfo) error {
	current, err := s.validImage(info)
	if err != nil {
		return err
	}
	a, z := current.Start, current.End
	if _, end, standalone := s.imageBlock(current); standalone {
		z = end
		for z > current.End && s.source[z-1] == '\n' {
			z--
		}
	}
	err = s.ApplyEdits([]editor.Edit{{Start: a, End: z}}, a, a, current.Version)
	s.reveal = false
	return err
}

func (s *EditorState) CanMoveImageTo(info ImageInfo, at int) bool {
	_, _, _, ok := s.imageMoveRange(info, at)
	return ok
}

func (s *EditorState) MoveImageTo(info ImageInfo, at int) error {
	if !s.CanMoveImageTo(info, at) {
		return errors.New("图片只能移动到同级段落之间")
	}
	return s.reorderImage(info, at)
}

func (s *EditorState) MoveImage(info ImageInfo, direction int) error {
	current, err := s.validImage(info)
	if err != nil {
		return err
	}
	info = current
	d := s.editingDocument()
	parent := d.Block(info.Block).Parent
	previous, next := 0, 0
	for block := parent + 1; block < d.Block(parent).Next; block = d.Block(block).Next {
		if d.Block(block).Kind == parser.SourcePrefix {
			continue
		}
		if block < info.Block {
			previous = block
		}
		if block > info.Block {
			next = block
			break
		}
	}
	if direction < 0 && previous > 0 {
		at, _ := s.blockRange(d.Block(previous))
		at = max(at, s.sourcePrefixEnd(), s.documentPrefixEnd(0))
		return s.MoveImageTo(info, at)
	}
	if direction > 0 && next > 0 {
		_, at := s.blockRange(d.Block(next))
		following := d.Block(next).Next
		if following < d.Block(parent).Next {
			at, _ = s.blockRange(d.Block(following))
		}
		return s.MoveImageTo(info, at)
	}
	return errors.New("已经到达当前层级的边界")
}

func (s *EditorState) imageVisiblePosition(at int) int {
	if !s.opts.InPlaceImages || s.SourceMode {
		return at
	}
	for _, info := range s.Images() {
		if at > info.Start && at < info.End {
			if at < s.caret {
				return info.Start
			}
			return info.End
		}
	}
	return at
}

func (s *EditorState) imageKey(key ui.Key, mods ui.Modifiers) bool {
	if !s.opts.InPlaceImages || s.SourceMode || s.opts.ReadOnly || s.composing {
		return false
	}
	if info, selected := s.SelectedImage(); selected {
		if mods == ui.Alt && (key == ui.KeyUp || key == ui.KeyDown) {
			direction := 1
			if key == ui.KeyUp {
				direction = -1
			}
			s.report(s.MoveImage(info, direction))
			return true
		}
		if mods != 0 {
			return false
		}
		switch key {
		case ui.KeyDelete, ui.KeyBackspace:
			s.report(s.DeleteImage(info))
			s.Focus()
			return true
		case ui.KeyEnter:
			s.ImageParagraph(info, true)
			s.client.ReplaceText(nil, "\n")
			return true
		case ui.KeyEscape:
			s.SetSelection(info.End, info.End)
			s.reveal = false
			return true
		case ui.KeyLeft, ui.KeyUp:
			s.imagePosition(info, false, false)
			return true
		case ui.KeyRight, ui.KeyDown:
			s.imagePosition(info, true, false)
			return true
		}
	}
	if mods == 0 && s.anchor == s.caret && (key == ui.KeyBackspace || key == ui.KeyDelete) {
		for _, info := range s.Images() {
			a, z, standalone := s.imageBlock(info)
			if key == ui.KeyBackspace && (s.caret == info.End || standalone && s.caret == z || s.imageAdjacentParagraph(info, s.caret, true)) || key == ui.KeyDelete && (s.caret == info.Start || standalone && s.caret == a || s.imageAdjacentParagraph(info, s.caret, false)) {
				s.report(s.SelectImage(info))
				return true
			}
		}
	}
	return false
}

func (s *EditorState) imageAdjacentParagraph(info ImageInfo, at int, after bool) bool {
	if _, _, standalone := s.imageBlock(info); !standalone {
		return false
	}
	start, end := at, info.Start
	if after {
		start, end = info.End, at
	}
	if start < 0 || end < start || end > len(s.source) {
		return false
	}
	marker := strings.TrimSpace(s.imageContinuation(info.Block))
	for _, line := range strings.Split(s.source[start:end], "\n") {
		if text := strings.TrimSpace(line); text != "" && (marker == "" || text != marker) {
			return false
		}
	}
	d := s.editingDocument()
	parent := d.Block(info.Block).Parent
	for block := 1; block < d.Len(); block++ {
		b := d.Block(block)
		if b.Parent == parent && (b.Kind == parser.Paragraph || b.Kind == parser.Heading) && b.Start <= at && at <= b.End {
			return true
		}
	}
	return false
}

// ImageParagraph selects a virtual writing position without changing source.
func (s *EditorState) ImageParagraph(info ImageInfo, after bool) {
	s.imagePosition(info, after, true)
}

func (s *EditorState) imagePosition(info ImageInfo, after, paragraph bool) {
	if _, err := s.validImage(info); err != nil {
		s.report(err)
		return
	}
	gap := s.imageGap(info, after)
	at := gap.start
	s.SetSelection(at, at)
	if _, _, standalone := s.imageBlock(info); standalone || paragraph {
		gap.paragraph = paragraph && !standalone
		s.focusCodeGap(gap)
	}
	s.reveal = false
	s.Focus()
}

func (s *EditorState) imageGap(info ImageInfo, after bool) *editorCodeGap {
	at := info.Start
	before, next := 0, info.Block
	if after {
		at = info.End
		if _, end, ok := s.imageBlock(info); ok {
			at = end
			if at > info.End && s.source[at-1] == '\n' {
				at--
			}
		}
		before, next = info.Block, 0
	}
	return &editorCodeGap{image: true, start: at, end: at, before: before, after: next, version: info.Version, imageStart: info.Start, imageEnd: info.End}
}

func (s *EditorState) imageContinuation(block int) string {
	b := s.editingDocument().Block(block)
	return codeContinuationPrefix(s.source[max(sourceLineStart(s.source, b.Start), s.documentPrefixEnd(0)):b.Start])
}

func (s *EditorState) imageView(c *ui.Context, block int, in *parser.Inline, alt string) {
	info := ImageInfo{Start: in.Start, End: in.End, Block: block, Version: s.snapshot.Version(), Destination: in.Dest, Alt: alt, Title: in.Title}
	_, _, standalone := s.imageBlock(info)
	render := func() {
		if standalone {
			s.codeGapBlock(c, s.imageGap(info, false))
		} else {
			s.imageInlineGap(c, info.Start, "在图片前插入内容")
		}
		box := ui.Column(c.Key(fmt.Sprintf("image-object-%d", info.Start))).MinWidth(0).MaxWidthPercent(100).AlignSelf(ui.Start).Role(ui.RoleImage).Label("图片：" + alt)
		hit := &editorImageHit{info: info}
		s.imageHits = append(s.imageHits, hit)
		box.Draw(func(_ *ui.Painter, r ui.Rect) { hit.rect = r })
		box.HandleInput(func(event ui.InputEvent) bool {
			if event.Kind == ui.InputPointerDown && (event.Button == 0 || event.Button == 1) {
				if s.opts.ReadOnly {
					if event.Button == 0 && s.view.o.OnImageOpen != nil {
						s.view.o.OnImageOpen(info)
					}
					return false
				}
				if event.Button == 1 {
					s.SetSelection(info.Start, info.End)
					s.reveal = false
					return false
				}
				s.report(s.SelectImage(info))
				if event.Button == 0 && event.Clicks == 2 && s.view.o.OnImageOpen != nil {
					s.focusRequest = false
					s.view.o.OnImageOpen(info)
				}
				return false
			}
			return false
		})
		box.ContextMenu(func(menu *ui.Menu) {
			if current, selected := s.SelectedImage(); !selected || current.Start != info.Start {
				s.SetSelection(info.Start, info.End)
				s.reveal = false
			}
			s.menu(menu)
			if s.view.o.ContextMenu != nil {
				s.view.o.ContextMenu(c, menu)
			}
		})
		selected := min(s.anchor, s.caret) == info.Start && max(s.anchor, s.caret) == info.End
		if selected {
			box.Border(1, s.view.th.Link).Radius(4)
		}
		box.Children(func() {
			if s.view.o.ImageContent != nil {
				s.view.o.ImageContent(c, block, info.Start, info.End, info.Destination, alt)
			} else if s.view.o.Image != nil {
				s.view.o.Image(c, block, info.Start, info.End, info.Destination, alt)
			} else {
				source := s.view.image(c, info.Destination)
				if source != nil {
					ui.Image(c, source).MaxWidthPercent(100).Label(alt)
				} else {
					ui.Text(c, firstImageLabel(alt)).Padding(12).Background(s.view.th.CodeBackground)
				}
			}
			if selected && s.opts.ImageActions != nil {
				s.opts.ImageActions(c, info, box)
			}
		})
		if standalone {
			s.codeGapBlock(c, s.imageGap(info, true))
		} else {
			s.imageInlineGap(c, info.End, "在图片后插入内容")
		}
	}
	if standalone {
		ui.Column(c.Key(fmt.Sprintf("image-block-%d", info.Start))).WidthPercent(100).MinWidth(0).Children(render)
	} else {
		render()
	}
}

func (s *EditorState) imageInlineGap(c *ui.Context, at int, label string) {
	if gap := s.focusedCodeGap(); gap != nil && gap.image && gap.paragraph && gap.start == at {
		drawn := false
		for _, shown := range s.codeGaps {
			if shown == gap {
				drawn = true
				break
			}
		}
		if !drawn {
			ui.Column(c.Key("image-paragraph-position")).WidthPercent(100).Children(func() { s.codeGapBlock(c, gap) })
			return
		}
	}
	th := &s.view.th
	font := ui.Font{Family: th.Font, Size: th.FontSize}
	spans := []ui.Span{{Text: " ", Font: font.Family, Size: font.Size, Color: th.Text}}
	el := ui.RichText(c.Key(fmt.Sprintf("image-edge-%d-%s", at, label)), spans...).Width(8).MinHeight(font.Size * th.LineHeight).LineHeight(th.LineHeight).Label(label)
	point := s.virtualOffset(at, false)
	s.observe(el, spans, []int{point, point}, []int{point, point}, font, th.LineHeight, false)
}

func firstImageLabel(alt string) string {
	if alt != "" {
		return alt
	}
	return "图片"
}

var imageMoveIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3"><path d="M6 3h.01M10 3h.01M6 8h.01M10 8h.01M6 13h.01M10 13h.01" stroke-linecap="round" stroke-width="2.5"/></svg>`))

func (s *EditorState) ImageMoveHandle(c *ui.Context, info ImageInfo) ui.Element {
	element := ui.ButtonBase(c).Width(28).Height(28).Label("拖动图片").Tooltip("拖动图片；Alt+↑／↓ 上下移动")
	element.Children(func() { ui.Icon(c, imageMoveIcon).Width(16).Height(16) })
	if _, _, ok := s.imageBlock(info); ok {
		element.Drag(imageDrag{owner: s, info: info})
	} else {
		element.Disabled(true)
	}
	return element
}

func (s *EditorState) imageDropTargets() {
	if !s.opts.InPlaceImages || s.SourceMode || s.opts.ReadOnly {
		return
	}
	for _, block := range s.blocks {
		target := max(block.start, s.sourcePrefixEnd(), s.documentPrefixEnd(0))
		if data, over := ui.DragOver[imageDrag](block.element); over && data.owner == s && s.CanMoveImageTo(data.info, target) {
			block.element.BorderWidth(2, 0, 0, 0).BorderColor(s.view.th.Link)
		}
		if data, dropped := ui.Drop[imageDrag](block.element); dropped && data.owner == s && s.CanMoveImageTo(data.info, target) {
			s.report(s.MoveImageTo(data.info, target))
			return
		}
	}
}
