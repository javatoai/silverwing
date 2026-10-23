package desktop

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const markdownSansFontFamily = "sans-serif"

type markdownEditorState struct {
	frameworkMarkdown      *frameworkMarkdownState
	contextMenu            func(*ui.Context, *ui.Menu)
	taskID                 string
	path                   string
	linkRoot               string
	onNavigateLocalLink    func(relativePath, fragment string)
	doc                    *editor.Document
	readOnly               bool
	commentReading         bool
	richTextReading        bool // Strip imported HTML span wrappers without changing image ownership.
	closeBitmapViewer      func()
	detached               bool
	closed                 bool
	remoteImages           bool
	trustedImages          map[string]string
	inlineImages           map[string]*markdownInlineImageState
	imageRenderSeen        map[string]bool
	readonlyReader         *readonlyMarkdownReaderState
	composingOwner         string
	zoom                   float32
	sourceMode             bool
	selectionPart          uint64
	selectionFrom          int
	selectionTo            int
	editFields             map[string]markdownEditFieldRef
	nativeFields           map[string]*markdownNativeFieldState
	richFields             map[uint64]*markdownRichFieldState
	structuredRichFields   map[string]*markdownStructuredRichFieldState
	selectedStructuredRich *markdownStructuredRichFieldState
	findScrolls            map[string]*ui.ScrollState
	instructionFind        *markdownInstructionFindState
	partExpanded           map[uint64]bool
	richLink               *markdownRichLinkDialogState
	tableCellBounds        map[markdownTableCellKey]ui.Rect
	tableDrag              *markdownTableDragState
	lastFocusRequest       uint64
	diagrams               map[uint64]*markdownDiagramState
	renderer               *editor.MermaidRenderer
	rendererError          string
	binding                *markdownEditorBinding
}

type markdownDiagramState struct {
	key     string
	source  string
	dark    bool
	bitmap  *ui.Bitmap
	error   string
	loading bool
	cancel  context.CancelFunc
}

type markdownInlineImageState struct {
	key        string
	sourcePath string
	bitmap     *ui.Bitmap
	error      string
	loading    bool
	cancel     context.CancelFunc
}

func (s *app) ensureMarkdownEditor() *markdownEditorState {
	taskID := s.currentTask().id
	if s.markdown != nil && s.markdown.taskID == taskID && s.markdown.path == s.documentPath && s.markdown.doc != nil {
		s.markdown.readOnly = markdownReadingOnly || s.documentReadOnly
		s.configureMarkdownSourceLimit(s.markdown)
		if s.markdown.doc.Source() != s.documentContent {
			s.markdown.doc.SyncExternal(s.documentContent)
		}
		return s.markdown
	}
	s.closeMarkdownEditor()
	s.markdown = &markdownEditorState{
		taskID: taskID, path: s.documentPath, doc: editor.NewDocument(s.documentContent, taskID+"\x00"+s.documentPath),
		zoom:     s.markdownZoom(),
		readOnly: markdownReadingOnly || s.documentReadOnly, remoteImages: true, diagrams: make(map[uint64]*markdownDiagramState), inlineImages: make(map[string]*markdownInlineImageState),
	}
	s.configureMarkdownSourceLimit(s.markdown)
	return s.markdown
}

func (s *app) configureMarkdownSourceLimit(state *markdownEditorState) {
	limit := editor.MaxDocumentBytes
	if ref, ok := s.documentSaveRef(s.documentAutosaveKey(state.path)); ok && ref.hasUTF8BOM {
		limit -= 3
	}
	state.doc.SetMaxSourceBytes(limit)
}

func (s *app) syncMarkdownDocument(path, source string) {
	if documentKindForPath(path) != "markdown" {
		return
	}
	if s.markdown == nil || s.markdown.taskID != s.currentTask().id || s.markdown.path != path || s.markdown.doc == nil {
		s.ensureMarkdownEditor()
	}
	if s.markdown.doc.Source() != source {
		s.markdown.doc.SyncExternal(source)
	}
}

func (s *app) syncMarkdownEditor() {
	if s.markdown == nil || s.markdown.doc == nil || s.markdown.path != s.documentPath {
		return
	}
	s.commitMarkdownEditorSource(s.markdown, s.markdown.doc.Source())
}

func (s *app) closeMarkdownEditor() {
	if s.markdown == nil {
		return
	}
	s.clearMarkdownComposition(s.markdown)
	closeMarkdownPreviewState(s.markdown)
	s.markdown = nil
}

func (s *app) commitMarkdownSource(source string) {
	if s.markdown != nil && (s.markdown.readOnly || s.markdown.detached) {
		return
	}
	if source == s.documentContent {
		return
	}
	s.documentContent = source
	s.documentDirty = true
	s.documentDrafts[s.documentPath] = source
	s.documentDirtyMap[s.documentPath] = true
	s.editMarkdownAutosave(s.documentPath, source)
}

func (s *app) applyMarkdownEdit(state *markdownEditorState, edit func(*editor.Document) error) {
	if markdownReadingOnly || state == nil || state.doc == nil || state.readOnly || state.detached ||
		state.binding != nil && !s.markdownEditorIsCurrent(state) {
		return
	}
	if err := edit(state.doc); err != nil {
		s.lastError = err.Error()
		return
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
}

func (s *app) renderMarkdownDocument(c *ui.Context, p palette) {
	state := s.ensureMarkdownEditor()
	s.renderMarkdownPreview(c, p, state)
}

func (s *app) renderMarkdownPreview(c *ui.Context, p palette, state *markdownEditorState) {
	if state == nil || state.doc == nil || state.closed {
		label(c, "Markdown 预览暂不可用。", 13, p.muted)
		return
	}
	parts := state.doc.Parts()
	if len(parts) == 0 {
		s.pruneMarkdownImagePreviews(state, nil)
		label(c, "文档内容为空。", 13, p.muted)
		return
	}
	if reader := state.readonlyReader; reader != nil {
		state.zoom = clampMarkdownZoom(reader.zoom)
	} else if state.detached {
		state.zoom = 1
	} else {
		state.zoom = s.markdownZoom()
	}
	state.imageRenderSeen = make(map[string]bool)
	s.renderFrameworkMarkdown(c, p, state)
	s.pruneMarkdownImagePreviews(state, state.imageRenderSeen)
	state.imageRenderSeen = nil
}

func (s *app) renderMarkdownSourceContent(c *ui.Context, p palette) {
	s.renderDetectedMaterialsText(c, p)
}

func (s *app) markdownZoom() float32 {
	return clampMarkdownZoom(float32(s.readingMarkdownZoom()))
}

func clampMarkdownZoom(zoom float32) float32 {
	if zoom < .7 || zoom > 1.6 {
		return 1
	}
	return zoom
}

func scaleMarkdownSize(size, zoom float32) float32 {
	return size * clampMarkdownZoom(zoom)
}

func (s *app) renderMarkdownEditor(c *ui.Context, p palette, state *markdownEditorState) {
	if state == nil || state.doc == nil || state.closed || state.binding != nil && !s.markdownEditorIsCurrent(state) {
		return
	}
	if markdownReadingOnly || state.readOnly {
		state.readOnly = true
		s.renderMarkdownPreview(c, p, state)
		return
	}
	if state == s.markdown {
		state.zoom = s.markdownZoom()
	} else {
		state.zoom = clampMarkdownZoom(state.zoom)
	}
	s.registerMarkdownEditorShortcuts(c, state)
	s.flushMarkdownNativeFields(state)
	s.pruneMarkdownRichFields(state)
	s.pruneMarkdownDiagramPreviews(state)
	state.editFields = make(map[string]markdownEditFieldRef)
	state.tableCellBounds = make(map[markdownTableCellKey]ui.Rect)
	state.imageRenderSeen = make(map[string]bool)
	defer func() {
		s.pruneMarkdownImagePreviews(state, state.imageRenderSeen)
		state.imageRenderSeen = nil
	}()
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).Height(34).Gap(5).AlignItems(ui.Center).Children(func() {
			label(c, "Markdown 编辑器", 12, p.muted).FontWeight(600).SingleLine().Grow(1).MinWidth(0)
			if iconButton(c, markdownEditorControlKey(state, "markdown-undo"), "undo", "撤销", p).Clicked() {
				s.moveMarkdownEditorHistory(state, false)
			}
			if iconButton(c, markdownEditorControlKey(state, "markdown-redo"), "redo", "重做", p).Clicked() {
				s.moveMarkdownEditorHistory(state, true)
			}
			if action(c, markdownEditorControlKey(state, "markdown-source-mode"), "code", map[bool]string{true: "分块编辑", false: "Markdown 原文"}[state.sourceMode], p, state.sourceMode) {
				s.toggleMarkdownEditorSourceMode(state)
			}
		})
		if state.sourceMode {
			s.renderMarkdownSource(c, p, state)
			return
		}
		s.renderMarkdownFormatBar(c, p, state)
		parts := state.doc.Parts()
		for _, part := range parts {
			s.renderMarkdownPartEditor(c, p, state, part)
		}
		if action(c, markdownEditorControlKey(state, "markdown-insert-paragraph"), "plus", "添加段落", p, false) {
			parts := state.doc.Parts()
			if len(parts) > 0 {
				s.insertMarkdownParagraphAfter(state, parts[len(parts)-1].ID)
			} else {
				s.insertMarkdownParagraphAfter(state, 0)
			}
		}
	})
}

func (s *app) renderMarkdownFormatBar(c *ui.Context, p palette, state *markdownEditorState) {
	ui.ScrollHorizontal(c.Key(markdownEditorControlKey(state, "markdown-format-scroll"))).Height(34).FillWidth().Children(func() {
		ui.Row(c).Height(34).Gap(4).Children(func() {
			for _, item := range []struct {
				key, label string
				command    editor.FormatCommand
			}{
				{"md-bold", "B", editor.FormatBold}, {"md-italic", "I", editor.FormatItalic}, {"md-strike", "S̶", editor.FormatStrike},
				{"md-code", "代码", editor.FormatInlineCode}, {"md-link", "链接", editor.FormatLink}, {"md-bullet", "项目符号", editor.FormatUnorderedList}, {"md-number", "编号", editor.FormatOrderedList},
			} {
				if action(c, markdownEditorControlKey(state, item.key), "", item.label, p, false) {
					s.formatMarkdownSelection(state, item.command, 0)
				}
			}
			miniButton(c, p, markdownEditorControlKey(state, "md-heading"), "chevron", "标题级别", false).Menu(func(menu *ui.Menu) {
				for level := 0; level <= 6; level++ {
					label := "正文"
					if level > 0 {
						label = strings.Repeat("#", level) + " 标题"
					}
					if menu.Item(label).Chosen() {
						s.formatMarkdownSelection(state, editor.FormatHeading, level)
					}
				}
			})
		})
	})
}

func (s *app) formatMarkdownSelection(state *markdownEditorState, command editor.FormatCommand, level int) {
	if state == nil || state.doc == nil || state.selectionPart == 0 {
		s.lastError = "先在某个 Markdown 文本块中选择内容。"
		return
	}
	if state.binding != nil && !s.markdownEditorIsCurrent(state) {
		return
	}
	rich := s.selectedMarkdownStructuredRich(state)
	if rich == nil {
		rich = state.richFields[state.selectionPart]
	}
	if rich != nil && !state.sourceMode {
		if command == editor.FormatLink {
			s.openMarkdownRichLink(state, rich)
		} else {
			s.formatMarkdownRichSelection(state, rich, command, level, "")
		}
		return
	}
	state.doc.ClearSelection()
	s.applyMarkdownEdit(state, func(doc *editor.Document) error {
		return doc.Format(editor.FormatRequest{PartID: state.selectionPart, Start: state.selectionFrom, End: state.selectionTo, Command: command, Level: level})
	})
}

func (s *app) renderMarkdownSource(c *ui.Context, p palette, state *markdownEditorState) {
	content := state.doc.Source()
	field := s.markdownNativeEditor(c.Key(markdownEditorInputKey(state, "source")), p, state, "source", 0, content, "markdown", false, 520)
	field = s.decorateMarkdownEditorFind(c, p, state, "source", content, field, nil)
	snapshot := state.doc.Snapshot()
	if snapshot.FocusRequest != state.lastFocusRequest && snapshot.Cursor != nil && snapshot.Cursor.Part == 0 && snapshot.Cursor.Field == "source" {
		field.Focus()
		markdownNativeSetSelection(&state.nativeFields["source"].view, content, editor.ByteToRuneOffset(content, snapshot.Cursor.Start), editor.ByteToRuneOffset(content, snapshot.Cursor.End))
		state.lastFocusRequest = snapshot.FocusRequest
	}
	if state.editFields == nil {
		state.editFields = make(map[string]markdownEditFieldRef)
	}
	state.editFields["source"] = markdownEditFieldRef{key: "source", partID: 0, value: content, element: field, native: &state.nativeFields["source"].view}
	s.observeMarkdownComposition(state, "source", state.nativeFields["source"].view.Composing())
	if field.Focused() {
		from, to := markdownNativeTextSelection(&state.nativeFields["source"].view)
		s.setMarkdownSelection(state, 0, editor.RuneToByteOffset(content, from), editor.RuneToByteOffset(content, to), true)
	}
}

func (s *app) setMarkdownSelection(state *markdownEditorState, partID uint64, absoluteFrom, absoluteTo int, absolute bool) {
	state.selectedStructuredRich = nil
	parts := state.doc.Parts()
	for _, part := range parts {
		if !absolute && part.ID != partID {
			continue
		}
		start, end := absoluteFrom, absoluteTo
		if absolute {
			if start < part.Range.Start || end > part.Range.End {
				continue
			}
			start -= part.Range.Start
			end -= part.Range.Start
		}
		if start >= 0 && end >= start && end <= len(part.Source) {
			state.selectionPart = part.ID
			state.selectionFrom = start
			state.selectionTo = end
			return
		}
	}
	state.selectionPart = 0
}

func (s *app) renderMarkdownPartEditor(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	expanded := markdownRawPartExpanded(state, part)
	background, border := p.surface, p.line
	if markdownPartInSelection(state.doc, part.ID) {
		background, border = p.soft, p.blue
	}
	ui.Column(c).FillWidth().Padding(12).Gap(7).Background(background).Border(1, border).Radius(9).Children(func() {
		ui.Row(c).Height(24).Gap(8).Children(func() {
			label(c, markdownPartTitle(part.Kind), 11, p.muted).FontWeight(600)
			ui.Spacer(c)
			if markdownFoldablePart(part.Kind) {
				title := markdownPartTitle(part.Kind)
				if part.Kind == editor.PartImage {
					title = "图片链接"
				}
				verb := "展开 "
				if expanded {
					verb = "收起 "
				}
				ui.Box(c).KeepFocus().Children(func() {
					if action(c, markdownEditorControlKey(state, fmt.Sprintf("markdown-fold-%d", part.ID)), "chevron", verb+title, p, false) {
						if s.toggleMarkdownRawPart(state, part) {
							expanded = !expanded
							c.Invalidate()
						}
					}
				})
			}
			if part.Kind == editor.PartCode || part.Kind == editor.PartMermaid {
				if action(c, markdownEditorControlKey(state, "markdown-copy-code-"+fmt.Sprint(part.ID)), "copy", "复制代码", p, false) {
					s.copyTextToClipboard(c.Services(), fenceCode(part.Source), "已复制")
				}
			}
			if action(c, markdownEditorControlKey(state, "markdown-copy-block-"+fmt.Sprint(part.ID)), "copy", "复制块原文", p, false) {
				s.copyTextToClipboard(c.Services(), part.Source, "")
			}
		})
		switch part.Kind {
		case editor.PartRich:
			s.renderMarkdownRichEditor(c, p, state, part)
		case editor.PartTable:
			s.renderMarkdownTableEditor(c, p, state, part)
		case editor.PartTasks:
			if !s.renderMarkdownTaskItemsEditor(c, p, state, part) {
				s.renderMarkdownRawPartEditor(c, p, state, part)
			}
		case editor.PartImage:
			for _, node := range part.Nodes {
				s.renderMarkdownBlock(c, p, state, part, node)
			}
			if expanded {
				s.renderMarkdownRawPartEditor(c, p, state, part)
			}
		case editor.PartCode, editor.PartMermaid:
			fence := editor.FindFence(part.Source)
			code := part.Source
			if fence != nil {
				code = fence.Code(part.Source)
			}
			key, language := fmt.Sprintf("code-%d", part.ID), ""
			if fence != nil {
				language = fence.Language
			}
			field := s.markdownNativeEditor(c.Key(markdownEditorInputKey(state, key)), p, state, key, part.ID, code, language, true, 220)
			field = s.registerMarkdownEditField(c, state, part, fmt.Sprintf("code-%d", part.ID), code, field, -1, -1, false)
			field = s.decorateMarkdownEditorFind(c, p, state, fmt.Sprintf("code-%d", part.ID), code, field, nil)
			s.observeMarkdownComposition(state, key, state.nativeFields[key].view.Composing())
			if field.Focused() {
				from, to := markdownNativeTextSelection(&state.nativeFields[key].view)
				s.setMarkdownSelection(state, part.ID, editor.RuneToByteOffset(code, from), editor.RuneToByteOffset(code, to), false)
			}
			if part.Kind == editor.PartMermaid {
				s.renderMarkdownMermaid(c, p, state, part)
			}
		default:
			if expanded {
				s.renderMarkdownRawPartEditor(c, p, state, part)
			}
		}
	})
}

func markdownFoldablePart(kind editor.PartKind) bool {
	return kind == editor.PartOriginal || kind == editor.PartMetadata || kind == editor.PartDivider || kind == editor.PartImage
}

func markdownRawPartExpanded(state *markdownEditorState, part editor.Part) bool {
	if expanded, ok := state.partExpanded[part.ID]; ok {
		return expanded
	}
	return part.Kind != editor.PartMetadata && part.Kind != editor.PartImage
}

func (s *app) toggleMarkdownRawPart(state *markdownEditorState, part editor.Part) bool {
	if !s.markdownEditorIsCurrent(state) || state.readOnly || state.detached {
		return false
	}
	if state.doc.Composing() {
		s.lastError = editor.ErrComposing.Error()
		return false
	}
	for _, rich := range allMarkdownRichFields(state) {
		if rich.input.composing || rich.input.dirty || rich.input.errorText != "" {
			s.lastError = "编辑内容尚未提交，请处理编辑错误后再折叠；当前草稿已保留。"
			return false
		}
	}
	if !state.doc.Flush() {
		s.lastError = state.doc.Snapshot().Error
		return false
	}
	if state.doc.Composing() {
		s.lastError = editor.ErrComposing.Error()
		return false
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
	expanded := !markdownRawPartExpanded(state, part)
	if state.partExpanded == nil {
		state.partExpanded = make(map[uint64]bool)
	}
	state.partExpanded[part.ID] = expanded
	if cursor := state.doc.Snapshot().Cursor; expanded && cursor != nil && cursor.Part == part.ID {
		state.doc.RequestFocusCursor(cursor)
	}
	return true
}

func (s *app) renderMarkdownRawPartEditor(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	text := part.Source
	key := fmt.Sprintf("part-%d", part.ID)
	field := s.markdownNativeEditor(c.Key(markdownEditorInputKey(state, key)), p, state, key, part.ID, text, "markdown", false, markdownEditorHeight(text))
	field = s.registerMarkdownEditField(c, state, part, key, text, field, -1, -1, false)
	field = s.decorateMarkdownEditorFind(c, p, state, key, text, field, nil)
	s.observeMarkdownComposition(state, key, state.nativeFields[key].view.Composing())
	if field.Focused() {
		from, to := markdownNativeTextSelection(&state.nativeFields[key].view)
		s.setMarkdownSelection(state, part.ID, editor.RuneToByteOffset(text, from), editor.RuneToByteOffset(text, to), false)
	}
}

func (s *app) renderMarkdownTableEditor(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	if !s.renderMarkdownStructuredTableEditor(c, p, state, part) {
		s.renderMarkdownRawPartEditor(c, p, state, part)
	}
}

func fenceCode(source string) string {
	if fence := editor.FindFence(source); fence != nil {
		return fence.Code(source)
	}
	return source
}

func markdownEditorHeight(text string) float32 {
	lines := strings.Count(text, "\n") + 1
	return float32(minInt(maxInt(lines, 3), 12)*23 + 20)
}

func markdownPartTitle(kind editor.PartKind) string {
	return map[editor.PartKind]string{
		editor.PartRich: "文本块", editor.PartCode: "代码块", editor.PartMermaid: "Mermaid 图表",
		editor.PartTable: "表格", editor.PartTasks: "任务列表", editor.PartImage: "图片引用",
		editor.PartOriginal: "原始 Markdown", editor.PartMetadata: "Front matter", editor.PartDivider: "分隔线",
	}[kind]
}

func (s *app) renderMarkdownBlock(c *ui.Context, p palette, state *markdownEditorState, part editor.Part, node editor.Node) {
	switch node.Kind {
	case "heading":
		size := scaleMarkdownSize(markdownHeadingSize(node.Level), state.zoom)
		key := markdownInlineFindKey(part.ID, node.Range)
		ui.Row(c).FillWidth().Wrap().Children(func() {
			s.renderMarkdownInlineNodes(c, p, state, part, node.Children, size, 700, false, false, false, key, new(int), p.text, false)
		})
	case "paragraph":
		key := markdownInlineFindKey(part.ID, node.Range)
		ui.Row(c).FillWidth().Wrap().Children(func() {
			s.renderMarkdownInlineNodes(c, p, state, part, node.Children, scaleMarkdownSize(15, state.zoom), 400, false, false, false, key, new(int), p.text, false)
		})
	case "list":
		ui.Column(c).FillWidth().Padding(5, 0, 5, 15).Children(func() {
			for index, child := range node.Children {
				if child.Kind == "task-item" {
					s.renderMarkdownTaskItem(c, p, state, part, child)
				} else {
					key := markdownInlineFindKey(part.ID, child.Range)
					ui.Row(c).FillWidth().Gap(7).Children(func() {
						marker := "•"
						if node.Ordered {
							marker = fmt.Sprintf("%d.", index+1)
						}
						label(c, marker, scaleMarkdownSize(15, state.zoom), p.text).Width(20)
						s.renderMarkdownInlineNodes(c, p, state, part, child.Children, scaleMarkdownSize(15, state.zoom), 400, false, false, false, key, new(int), p.text, false)
					})
				}
			}
		})
	case "task-list":
		ui.Column(c).FillWidth().Padding(8, 0).Gap(5).Children(func() {
			for index, child := range node.Children {
				if child.Kind == "task-item" {
					s.renderMarkdownTaskItem(c, p, state, part, child)
					continue
				}
				key := markdownInlineFindKey(part.ID, child.Range)
				ui.Row(c).FillWidth().Gap(7).Children(func() {
					marker := "•"
					if node.Ordered {
						marker = fmt.Sprintf("%d.", index+1)
					}
					label(c, marker, scaleMarkdownSize(15, state.zoom), p.text).Width(20)
					s.renderMarkdownInlineNodes(c, p, state, part, child.Children, scaleMarkdownSize(15, state.zoom), 400, false, false, false, key, new(int), p.text, false)
				})
			}
		})
	case "blockquote":
		ui.Row(c).FillWidth().Gap(10).Padding(4, 0).Children(func() {
			ui.Box(c).Width(3).FillHeight().Radius(2).Background(p.blue)
			ui.Column(c).Grow(1).Gap(7).Children(func() {
				for _, child := range node.Children {
					s.renderMarkdownBlock(c, p, state, part, child)
				}
			})
		})
	case "divider":
		ui.Divider(c).Padding(6, 0)
	case "code-fence":
		s.renderMarkdownCode(c, p, part, state)
	default:
		if len(node.Children) > 0 {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				for _, child := range node.Children {
					s.renderMarkdownBlock(c, p, state, part, child)
				}
			})
		} else if strings.TrimSpace(node.Text) != "" {
			label(c, node.Text, scaleMarkdownSize(15, state.zoom), p.text).Wrap().Selectable()
		}
	}
}

func (s *app) renderMarkdownTaskList(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	for _, node := range part.Nodes {
		s.renderMarkdownBlock(c, p, state, part, node)
	}
}

func (s *app) renderMarkdownTaskItem(c *ui.Context, p palette, state *markdownEditorState, part editor.Part, node editor.Node) {
	key := markdownInlineFindKey(part.ID, node.Range)
	checked := node.Checked != nil && *node.Checked
	ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
		if state.readOnly {
			label(c, map[bool]string{true: "☑", false: "☐"}[checked], 14, p.muted)
		} else {
			before := checked
			ui.Checkbox(c.Key(markdownEditorControlKey(state, fmt.Sprintf("markdown-tasklist-%d-%d", part.ID, node.Range.Start))), &checked, "")
			if checked != before {
				taskIndex := taskIndexBefore(part.Source, node.Range.Start-part.Range.Start)
				s.applyMarkdownEdit(state, func(doc *editor.Document) error { return doc.SetTaskChecked(part.ID, taskIndex, checked) })
			}
		}
		s.renderMarkdownInlineNodes(c, p, state, part, node.Children, scaleMarkdownSize(15, state.zoom), 400, false, false, false, key, new(int), p.text, false)
	})
}

func (s *app) renderMarkdownTable(c *ui.Context, p palette, state *markdownEditorState, partID uint64, rows [][]string) {
	if state.readOnly {
		if part, ok := markdownEditorPart(state, partID); ok {
			s.renderReadonlyMarkdownTable(c, p, state, part)
		}
		return
	}
	find := s.markdownFindStateFor(state)
	if find == nil {
		renderMarkdownTable(c, p, rows, state.zoom)
		return
	}
	if len(rows) == 0 {
		return
	}
	ui.Column(c).FillWidth().Border(1, p.line).Radius(8).Children(func() {
		for rowIndex, row := range rows {
			line := ui.Row(c).FillWidth().Padding(9, 12).Gap(10)
			if rowIndex == 0 {
				line.Background(p.soft)
			} else {
				line.Border(1, p.line).BorderWidth(1, 0, 0, 0)
			}
			line.Children(func() {
				for column, cell := range row {
					color := p.muted
					if rowIndex == 0 {
						color = p.text
					}
					key := markdownTableFindKey(partID, rowIndex, column)
					spans := find.spans(key, cell, 0, color, nil)
					content := ui.RichText(c, spans...).Grow(1).FontSize(scaleMarkdownSize(15, state.zoom)).Selectable()
					if column == 0 {
						content.FontWeight(500)
					}
					if find.shouldReveal(key) {
						content.ScrollIntoView()
					}
				}
			})
		}
	})
}

func (s *app) renderMarkdownCode(c *ui.Context, p palette, part editor.Part, state *markdownEditorState) {
	zoom := state.zoom
	fence := editor.FindFence(part.Source)
	code := part.Source
	language := ""
	if fence != nil {
		code, language = fence.Code(part.Source), fence.Language
	}
	ui.Column(c).FillWidth().Margin(5, 0).Background(p.soft).Radius(6).Children(func() {
		ui.Row(c).FillWidth().Height(28).Padding(0, 4, 0, 12).AlignItems(ui.Center).Children(func() {
			label(c, firstNonEmpty(language, "代码"), scaleMarkdownSize(11, zoom), p.muted).FontWeight(600)
			ui.Spacer(c)
			copyButton := ui.ButtonBase(c.Key("markdown-copy-code-" + fmt.Sprint(part.ID))).Width(28).Height(28).Radius(14).Center().Label("复制代码")
			if copyButton.Hovered() {
				copyButton.Background(p.surface)
			}
			copyButton.Children(func() { icon(c, "copy", 15, p.text) })
			if copyButton.Clicked() {
				s.copyTextToClipboard(c.Services(), code, "")
			}
		})
		renderReadonlyTextEditor(c.Key("markdown-native-code-"+fmt.Sprint(part.ID)), p, markdownEditorHostID(state)+"-"+fmt.Sprint(part.ID),
			language, code, "代码只读预览", zoom, s.markdownFindStateFor(state), markdownCodeFindKey(part.ID))
	})
}

func markdownHeadingSize(level int) float32 {
	// The source RichEditor sizes headings relative to its 15sp body text.
	sizes := [...]float32{0, 30, 22.5, 17.55, 16.8, 12.45, 11.25}
	if level < 1 {
		level = 1
	}
	if level >= len(sizes) {
		level = len(sizes) - 1
	}
	return sizes[level]
}

func (s *app) markdownFindStateFor(state *markdownEditorState) *documentFindState {
	if state == nil {
		return nil
	}
	if state.readonlyReader != nil || state.detached {
		reader := state.readonlyReader
		if reader == nil || !reader.findOpen {
			return nil
		}
		return &reader.find
	}
	if !s.documentFindOpen || s.markdown != state {
		return nil
	}
	return s.ensureDocumentFindState()
}

func (s *app) renderMarkdownInlineNodes(c *ui.Context, p palette, state *markdownEditorState, part editor.Part,
	nodes []editor.Node, size float32, weight int, italic, strike, code bool, findKey string, textOffset *int, color ui.Color, underline bool) {
	if markdownInlineTextOnly(nodes) {
		// Inline runs share one shaped paragraph. Separate row children lose
		// the advance of trailing spaces at emphasis/link boundaries.
		paragraph := ui.RichText(c).FillWidth().FontSize(size).Wrap()
		if !state.readOnly || part.Kind != editor.PartTable {
			paragraph.Selectable()
		}
		paragraph.Children(func() {
			s.renderMarkdownInlineNodesWithLink(c, p, state, part, nodes, size, weight, italic, strike, code, findKey, textOffset, color, underline, "")
		})
		return
	}
	for _, group := range markdownInlineGroups(nodes) {
		if !markdownInlineTextOnly(group) {
			s.renderMarkdownInlineNodesWithLink(c, p, state, part, group, size, weight, italic, strike, code, findKey, textOffset, color, underline, "")
			continue
		}
		font := ui.Font{Family: markdownSansFontFamily, Size: size, Weight: weight, Italic: italic}
		if code {
			font.Family = "Consolas, monospace"
		}
		space, _ := markdownInlineEndSpace(group, font)
		// The last space of a wrapped text element has no layout advance.
		// Reserve it outside the paragraph so an adjacent bitmap keeps the
		// original separation, without changing displayed/copied text.
		paragraph := ui.RichText(c).FontSize(size).Wrap().Margin(0, space, 0, 0)
		if !state.readOnly || part.Kind != editor.PartTable {
			paragraph.Selectable()
		}
		paragraph.Children(func() {
			s.renderMarkdownInlineNodesWithLink(c, p, state, part, group, size, weight, italic, strike, code, findKey, textOffset, color, underline, "")
		})
	}
}

func markdownInlineTextOnly(nodes []editor.Node) bool {
	for _, node := range nodes {
		if node.Kind == "image" || node.Kind == "link-preview" || !markdownInlineTextOnly(node.Children) {
			return false
		}
	}
	return true
}

// Split only at image nodes; nested emphasis/link wrappers remain attached to
// their text. Each text group can then share one native shaped paragraph.
func markdownInlineGroups(nodes []editor.Node) [][]editor.Node {
	var groups [][]editor.Node
	appendGroup := func(group []editor.Node) {
		if len(groups) > 0 && markdownInlineTextOnly(group) && markdownInlineTextOnly(groups[len(groups)-1]) {
			groups[len(groups)-1] = append(groups[len(groups)-1], group...)
		} else {
			groups = append(groups, group)
		}
	}
	for _, node := range nodes {
		if node.Kind == "image" || node.Kind == "link-preview" || markdownInlineTextOnly(node.Children) {
			appendGroup([]editor.Node{node})
			continue
		}
		for _, group := range markdownInlineGroups(node.Children) {
			wrapper := node
			wrapper.Children = group
			appendGroup([]editor.Node{wrapper})
		}
	}
	return groups
}

// The bool reports a non-space terminator, so trailing whitespace spanning
// several differently styled nodes can be measured with their actual fonts.
func markdownInlineEndSpace(nodes []editor.Node, font ui.Font) (float32, bool) {
	var width float32
	for index := len(nodes) - 1; index >= 0; index-- {
		node := nodes[index]
		style := font
		switch node.Kind {
		case "strong":
			style.Weight = maxInt(style.Weight, 650)
		case "emphasis":
			style.Italic = true
		case "code":
			style.Family = "Consolas, monospace"
		}
		if len(node.Children) > 0 && !(node.Kind == "code" && node.Text != "") {
			spaces, ended := markdownInlineEndSpace(node.Children, style)
			width += spaces
			if ended {
				return width, true
			}
			continue
		}
		text := node.Text
		if node.Attributes["softLineBreak"] == "true" || node.Attributes["hardLineBreak"] == "true" {
			text += "\n"
		}
		trimmed := strings.TrimRight(text, " \t")
		if trailing := text[len(trimmed):]; trailing != "" {
			spaces, _ := ui.ShapeText(trailing, style, 0).Size()
			width += spaces
		}
		if trimmed != "" {
			return width, true
		}
	}
	return width, false
}

func (s *app) renderMarkdownInlineNodesWithLink(c *ui.Context, p palette, state *markdownEditorState, part editor.Part,
	nodes []editor.Node, size float32, weight int, italic, strike, code bool, findKey string, textOffset *int, color ui.Color, underline bool, linkTarget string) {
	find := s.markdownFindStateFor(state)
	for _, node := range nodes {
		switch node.Kind {
		case "text":
			text := node.Text
			if node.Attributes["softLineBreak"] == "true" || node.Attributes["hardLineBreak"] == "true" {
				text += "\n"
			}
			if text != "" {
				s.renderMarkdownInlineText(c, p, state, part, text, size, weight, italic, strike, code, findKey, *textOffset, color, underline, find, linkTarget)
				*textOffset += len(text)
			}
		case "emphasis":
			s.renderMarkdownInlineNodesWithLink(c, p, state, part, node.Children, size, weight, true, strike, code, findKey, textOffset, color, underline, linkTarget)
		case "strong":
			s.renderMarkdownInlineNodesWithLink(c, p, state, part, node.Children, size, maxInt(weight, 650), italic, strike, code, findKey, textOffset, color, underline, linkTarget)
		case "strike":
			s.renderMarkdownInlineNodesWithLink(c, p, state, part, node.Children, size, weight, italic, true, code, findKey, textOffset, color, underline, linkTarget)
		case "code":
			if node.Text != "" {
				s.renderMarkdownInlineText(c, p, state, part, node.Text, size, weight, italic, strike, true, findKey, *textOffset, color, underline, find, linkTarget)
				*textOffset += len(node.Text)
			} else {
				s.renderMarkdownInlineNodesWithLink(c, p, state, part, node.Children, size, weight, italic, strike, true, findKey, textOffset, color, underline, linkTarget)
			}
		case "link":
			linkTarget := node.Destination
			if len(node.Children) > 0 {
				s.renderMarkdownInlineNodesWithLink(c, p, state, part, node.Children, size, weight, italic, strike, code, findKey, textOffset, p.blue, true, linkTarget)
			} else {
				s.renderMarkdownInlineText(c, p, state, part, node.Text, size, weight, italic, strike, code, findKey, *textOffset, p.blue, true, find, linkTarget)
				*textOffset += len(node.Text)
			}
		case "link-preview":
			s.renderMarkdownLinkPreview(c, p, state, part, node, size, findKey, textOffset, find)
		case "image":
			s.renderMarkdownImage(c, p, state, part, node, size)
		default:
			if len(node.Children) > 0 {
				s.renderMarkdownInlineNodesWithLink(c, p, state, part, node.Children, size, weight, italic, strike, code, findKey, textOffset, color, underline, linkTarget)
			} else if node.Text != "" {
				s.renderMarkdownInlineText(c, p, state, part, node.Text, size, weight, italic, strike, code, findKey, *textOffset, color, underline, find, linkTarget)
				*textOffset += len(node.Text)
			}
		}
	}
}

func (s *app) renderMarkdownLinkPreview(c *ui.Context, p palette, state *markdownEditorState, part editor.Part, node editor.Node, size float32, findKey string, textOffset *int, find *documentFindState) {
	target := strings.TrimSpace(node.Destination)
	if target == "" {
		return
	}
	title := strings.TrimSpace(node.Text)
	if title == "" || title == target {
		title = "飞书文档"
	}
	host := target
	if parsed, err := url.Parse(target); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	key := fmt.Sprintf("markdown-link-preview-%d-%d", part.ID, node.Range.Start)
	button := ui.ButtonBase(c.Key(key)).Row().FillWidth().MinHeight(46).Padding(7, 11).Gap(9).Radius(8).Border(1, p.line).Background(p.soft).TextColor(p.text).Label("打开文档：" + title).Tooltip(target)
	if button.Hovered() {
		button.Background(p.surface)
	}
	button.Children(func() {
		ui.Box(c).Width(30).Height(30).Center().Radius(7).Background(p.surface).Children(func() { icon(c, "file", 16, p.blue) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			spans := []ui.Span{{Text: title, Color: p.text}}
			if find != nil && node.Text == title {
				spans = find.spans(findKey, node.Text, *textOffset, p.text, nil)
			}
			for index := range spans {
				spans[index].Size = scaleMarkdownSize(13, state.zoom)
				spans[index].Weight = 500
			}
			element := ui.RichText(c, spans...).FillWidth().SingleLine().Tooltip(target)
			font := ui.Font{Family: c.Theme().Font, Size: scaleMarkdownSize(13, state.zoom), Weight: 500}
			var scroll *ui.ScrollState
			if find != nil {
				if state.readonlyReader != nil {
					scroll = readonlyMarkdownReaderScroll(state.readonlyReader, false)
				} else {
					scroll = s.readingScrollState("markdown-native")
				}
			}
			if node.Text == title {
				revealDocumentFindRichText(c, element, spans, font, findKey, node.Text, *textOffset, find, scroll)
			}
			detail := host
			if node.Text != "" && node.Text != title {
				detail = node.Text
			}
			detailSpans := []ui.Span{{Text: detail, Color: p.muted}}
			if find != nil && detail == node.Text {
				detailSpans = find.spans(findKey, detail, *textOffset, p.muted, nil)
			}
			for index := range detailSpans {
				detailSpans[index].Size = scaleMarkdownSize(10, state.zoom)
			}
			detailElement := ui.RichText(c, detailSpans...).FillWidth().SingleLine().Tooltip(target)
			if find != nil && detail == node.Text && node.Text != title {
				font := ui.Font{Family: c.Theme().Font, Size: scaleMarkdownSize(10, state.zoom)}
				var scroll *ui.ScrollState
				if state.readonlyReader != nil {
					scroll = readonlyMarkdownReaderScroll(state.readonlyReader, false)
				} else {
					scroll = s.readingScrollState("markdown-native")
				}
				revealDocumentFindRichText(c, detailElement, detailSpans, font, findKey, detail, *textOffset, find, scroll)
			}
		})
		icon(c, "launch", 14, p.blue)
	})
	if button.Clicked() {
		s.openMarkdownLink(state, target)
	}
	if node.Text != "" {
		*textOffset += len(node.Text)
	}
}

func (s *app) renderMarkdownInlineText(c *ui.Context, p palette, state *markdownEditorState, part editor.Part, text string, size float32, weight int,
	italic, strike, code bool, key string, baseOffset int, color ui.Color, underline bool, find *documentFindState, linkTarget string) {
	spans := []ui.Span{{Text: text, Color: color}}
	if find != nil {
		spans = find.spans(key, text, baseOffset, color, nil)
	}
	for index := range spans {
		spans[index].Size = size
		spans[index].Weight = weight
		spans[index].Italic = italic
		spans[index].Strikethrough = strike
		spans[index].Underline = underline
		if code {
			spans[index].Font = "Consolas, monospace"
			if spans[index].Background.A == 0 {
				spans[index].Background = p.soft
			}
		}
	}
	element := ui.RichText(c, spans...).FontSize(size).Wrap()
	if !state.readOnly || part.Kind != editor.PartTable {
		element.Selectable()
	}
	if code {
		element.Font("Consolas, monospace")
	}
	if linkTarget != "" {
		element.Cursor(ui.CursorPointer).OnClick(func() { s.openMarkdownLink(state, linkTarget) })
	}
	font := ui.Font{Family: c.Theme().Font, Size: size}
	if code {
		font.Family = "Consolas, monospace"
	}
	var scroll *ui.ScrollState
	if find != nil {
		if state.readonlyReader != nil {
			scroll = readonlyMarkdownReaderScroll(state.readonlyReader, false)
		} else {
			scroll = s.readingScrollState("markdown-native")
		}
	}
	revealDocumentFindRichText(c, element, spans, font, key, text, baseOffset, find, scroll)
}

func plainInlineText(nodes []editor.Node) string {
	var out strings.Builder
	for _, node := range nodes {
		if node.Text != "" {
			out.WriteString(node.Text)
		}
		out.WriteString(plainInlineText(node.Children))
	}
	return strings.TrimSpace(out.String())
}

func taskIndexBefore(source string, relativeOffset int) int {
	if relativeOffset < 0 {
		relativeOffset = 0
	}
	if relativeOffset > len(source) {
		relativeOffset = len(source)
	}
	prefix := source[:relativeOffset]
	return strings.Count(strings.ToLower(prefix), "[x]") + strings.Count(prefix, "[ ]")
}

func (s *app) renderMarkdownMermaid(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	source := part.Source
	if fence := editor.FindFence(part.Source); fence != nil {
		source = fence.Code(part.Source)
	}
	if state.diagrams == nil {
		state.diagrams = make(map[uint64]*markdownDiagramState)
	}
	diagram := state.diagrams[part.ID]
	if diagram == nil {
		diagram = &markdownDiagramState{}
		state.diagrams[part.ID] = diagram
	}
	key := source
	if s.dark {
		key += "\x00dark"
	}
	if diagram.key != key {
		s.renderMermaidDiagram(state, part.ID, source, key, diagram)
	}
	ui.Column(c).FillWidth().Padding(8, 11, 11, 11).Gap(2).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
		ui.Row(c).FillWidth().Height(34).Gap(2).AlignItems(ui.Center).Children(func() {
			label(c, "Mermaid 图表", scaleMarkdownSize(10, state.zoom), p.muted).FontWeight(600)
			ui.Spacer(c)
			if diagram.bitmap != nil && iconButton(c, markdownEditorControlKey(state, "mermaid-expand-"+fmt.Sprint(part.ID)), "expand", "放大查看", p).Width(30).Height(30).Clicked() {
				s.openMarkdownBitmapViewer(state, diagram.bitmap)
			}
			s.overflowMenu(c, p, markdownEditorControlKey(state, "mermaid-more-"+fmt.Sprint(part.ID)), "更多图表操作", false, func(menu *overflowMenuBuilder) {
				if menu.Item("copy-source", "复制图表源码").Icon("copy").Chosen() {
					s.copyTextToClipboard(c.Services(), source, "")
				}
			})
		})
		if diagram.bitmap != nil {
			width, height := diagram.bitmap.Size()
			if width < 1 || height < 1 {
				width, height = 640, 360
			}
			ui.Image(c.Key(markdownEditorControlKey(state, "mermaid-preview-"+fmt.Sprint(part.ID))), diagram.bitmap).Width(minFloat32(float32(width), 1000)).MaxWidthPercent(100).AspectRatio(float32(width) / float32(height)).MaxHeight(700).Fit(ui.Contain).Label("Mermaid 图表预览").OnClick(func() {
				s.openMarkdownBitmapViewer(state, diagram.bitmap)
			})
		}
		if diagram.loading {
			label(c, "正在离线渲染图表…", scaleMarkdownSize(11, state.zoom), p.muted)
		}
		if diagram.error != "" {
			message := diagram.error
			if diagram.bitmap != nil {
				message = "图表更新失败，仍显示上一次有效结果：" + message
			}
			label(c, message, scaleMarkdownSize(11, state.zoom), p.amber).Wrap()
		}
	})
}

func (s *app) renderMermaidDiagram(state *markdownEditorState, partID uint64, source, key string, diagram *markdownDiagramState) {
	if state.renderer == nil && state.rendererError == "" {
		state.renderer, state.rendererError = loadMermaidRenderer()
	}
	if state.rendererError != "" {
		diagram.key, diagram.source, diagram.error = key, source, state.rendererError
		return
	}
	if diagram.cancel != nil {
		diagram.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	dark := s.dark
	diagram.cancel, diagram.key, diagram.source, diagram.dark, diagram.loading = cancel, key, source, dark, true
	diagram.error = ""
	renderer := state.renderer
	win := s.win
	go func() {
		result := renderer.RenderDebounced(ctx, source, dark)
		var bitmap *ui.Bitmap
		var err error
		if result.Success() {
			bitmap, err = ui.DecodeBitmap(result.PNG)
		} else if result.Error != "" {
			err = fmt.Errorf("%s", result.Error)
		}
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if !markdownMermaidResultRelevant(state, s.markdown, diagram, partID, key, ctx, s.documentPath) {
				return
			}
			diagram.loading = false
			if err != nil {
				diagram.error = err.Error()
				return
			}
			diagram.bitmap = bitmap
		})
	}()
}

// markdownMermaidResultRelevant is the production stale-result gate shared by
// Mermaid completion and the MAT04 manual proof. It deliberately checks the
// captured document state, diagram identity, source/theme key, and cancellation
// before allowing an asynchronous render to replace the last valid bitmap.
func markdownMermaidResultRelevant(state, currentState *markdownEditorState, diagram *markdownDiagramState, partID uint64, key string, ctx context.Context, currentPath string) bool {
	if state == nil || diagram == nil || ctx == nil || state.closed {
		return false
	}
	if state.binding != nil {
		if state.binding.isCurrent == nil || !state.binding.isCurrent() {
			return false
		}
	} else if !state.detached && (currentState != state || state.path != currentPath) {
		return false
	}
	return state.diagrams[partID] == diagram && diagram.key == key && ctx.Err() == nil
}

func loadMermaidRenderer() (*editor.MermaidRenderer, string) {
	platform, err := editor.NativeMermaidPlatform()
	if err != nil {
		return nil, err.Error()
	}
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil, err.Error()
	}
	path := filepath.Join(resources, "native-mermaid", platform.Key, platform.ExecutableName)
	resource, err := os.Open(path)
	if err != nil {
		return nil, fmt.Sprintf("缺少离线 Mermaid 渲染器：%s", path)
	}
	defer resource.Close()
	cache, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		return nil, err.Error()
	}
	executable, err := editor.InstallMermaidExecutable(resource, cache, platform)
	if err != nil {
		return nil, err.Error()
	}
	return editor.NewMermaidRenderer(executable, nil), ""
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
