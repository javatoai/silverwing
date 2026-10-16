package desktop

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"

	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderRequirementURLLink(c *ui.Context, p palette, taskID, value string, size float32) {
	link := ui.RichText(c, ui.Span{Text: value, Color: p.blue}).FontSize(size).MinWidth(40).Shrink(1).SingleLine().Tooltip(value)
	link.Cursor(ui.CursorPointer).OnClick(func() {
		s.act("system.open", taskID, "", "", value, "", nil)
	})
}

const requirementReaderInactiveFindBlock = "\x00requirement-reader-inactive"

var requirementCommentsIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><path d="M5 4h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H9l-6 4V6a2 2 0 0 1 2-2Z"/><path d="M7 9h10M7 13h7"/></svg>`))

func ensureRequirementReaderController(reader *requirementReaderState) *readonlyMarkdownReaderState {
	if reader.sharedReader == nil {
		reader.sharedReader = &readonlyMarkdownReaderState{zoom: 1}
	}
	if reader.sharedReader.zoom == 0 {
		reader.sharedReader.zoom = 1
	}
	return reader.sharedReader
}

func requirementReaderControlKey(reader *requirementReaderState) string {
	identity := requirementReaderKey(reader.taskID, reader.url)
	return fmt.Sprintf("requirement-reader-%x", sha256.Sum256([]byte(identity)))
}

func requirementReaderFindIdentity(reader *requirementReaderState, sourceMode bool) string {
	mode := "rendered"
	if sourceMode {
		mode = "source"
	}
	return "requirement-reader\x00" + requirementReaderKey(reader.taskID, reader.url) + "\x00" + mode
}

func requirementMarkdownFindBlocks(state *markdownEditorState, sourceMode bool) []documentFindBlock {
	if state == nil || state.doc == nil || state.closed {
		return nil
	}
	if sourceMode {
		return readonlyMarkdownSourceFindBlocks(state.doc.Source())
	}
	return frameworkMarkdownFindBlocks(state)
}

func namespacedRequirementFindBlocks(section string, blocks []documentFindBlock) []documentFindBlock {
	result := make([]documentFindBlock, 0, len(blocks))
	for _, block := range blocks {
		block.key = section + "\x00" + block.key
		result = append(result, block)
	}
	return result
}

func requirementFindBlocksForReader(reader *requirementReaderState, sourceMode bool) []documentFindBlock {
	if reader == nil {
		return nil
	}
	blocks := namespacedRequirementFindBlocks("body", requirementMarkdownFindBlocks(reader.bodyMarkdown, sourceMode))
	if sourceMode {
		return append(blocks, namespacedRequirementFindBlocks("comments", requirementMarkdownFindBlocks(reader.commentsMarkdown, true))...)
	}
	if reader.comments != nil {
		for index, comment := range reader.comments.Comments {
			key := requirementCommentStateKey(comment, index)
			section := requirementCommentFindSection(key)
			blocks = append(blocks, namespacedRequirementFindBlocks(section, requirementMarkdownFindBlocks(reader.commentMarkdown[key], false))...)
		}
	}
	return blocks
}

func (s *app) syncRequirementMarkdownDocument(reader *requirementReaderState, controller *readonlyMarkdownReaderState, section string, state *markdownEditorState) {
	if state == nil {
		return
	}
	local := ensureReadonlyMarkdownReader(state)
	if local == nil {
		return
	}
	state.contextMenu = reader.contextMenu
	state.sourceMode, state.zoom = controller.sourceMode, controller.zoom
	local.sourceMode, local.zoom, local.findOpen = controller.sourceMode, controller.zoom, controller.findOpen
	local.externalScroll = readonlyMarkdownReaderScroll(controller, controller.sourceMode)
	identity := requirementReaderFindIdentity(reader, controller.sourceMode) + "\x00" + section
	blocks := requirementMarkdownFindBlocks(state, controller.sourceMode)
	local.find.hits = withoutRequirementInactiveFindBlock(local.find.hits)
	local.find.set(identity, controller.find.query, blocks)
	local.find.viewport = controller.find.viewport
	local.find.navigation = controller.find.navigation
	local.find.active = 0

	active, ok := controller.find.current()
	prefix := section + "\x00"
	if !ok || !strings.HasPrefix(active.block, prefix) || len(local.find.hits) == 0 {
		if controller.find.query != "" && len(local.find.hits) > 0 {
			local.find.hits = append(local.find.hits, documentFindHit{block: requirementReaderInactiveFindBlock})
			local.find.active = len(local.find.hits) - 1
		}
		return
	}
	key := strings.TrimPrefix(active.block, prefix)
	for index, hit := range local.find.hits {
		if hit.block == key && hit.start == active.start && hit.end == active.end {
			local.find.active = index
			return
		}
	}
	local.find.hits = append(local.find.hits, documentFindHit{block: requirementReaderInactiveFindBlock})
	local.find.active = len(local.find.hits) - 1
}

func withoutRequirementInactiveFindBlock(hits []documentFindHit) []documentFindHit {
	if len(hits) > 0 && hits[len(hits)-1].block == requirementReaderInactiveFindBlock {
		return hits[:len(hits)-1]
	}
	return hits
}

func (s *app) prepareRequirementMarkdownReader(reader *requirementReaderState) *readonlyMarkdownReaderState {
	if reader == nil {
		return nil
	}
	controller := ensureRequirementReaderController(reader)
	// The unified reader reuses the requirement's previously persisted body
	// scroll slot; comments now flow through that same vertical position.
	controller.externalScroll = s.requirementReaderScroll(reader, false)
	identity := requirementReaderFindIdentity(reader, controller.sourceMode)
	controller.find.set(identity, controller.find.query, requirementFindBlocksForReader(reader, controller.sourceMode))
	s.syncRequirementMarkdownDocument(reader, controller, "body", reader.bodyMarkdown)
	// Keep the aggregated source document bound to the shared position so
	// source mode and existing reader state continue to round-trip. Rendered
	// mode only draws the per-comment documents below.
	s.syncRequirementMarkdownDocument(reader, controller, "comments", reader.commentsMarkdown)
	if !controller.sourceMode && reader.comments != nil {
		for index, comment := range reader.comments.Comments {
			key := requirementCommentStateKey(comment, index)
			s.syncRequirementMarkdownDocument(reader, controller, requirementCommentFindSection(key), reader.commentMarkdown[key])
		}
	}
	return controller
}

func (s *app) openRequirementMarkdownFind(c *ui.Context, reader *requirementReaderState) {
	controller := s.prepareRequirementMarkdownReader(reader)
	if controller == nil {
		return
	}
	controller.findOpen = true
	controller.findDraft = controller.find.query
	controller.findFocusPending = true
	controller.find.navigation++
	c.Invalidate()
}

func (s *app) moveRequirementMarkdownFind(reader *requirementReaderState, delta int) {
	controller := s.prepareRequirementMarkdownReader(reader)
	if controller == nil || !controller.findOpen {
		return
	}
	controller.find.move(delta)
	s.prepareRequirementMarkdownReader(reader)
}

func (s *app) closeRequirementMarkdownFind(reader *requirementReaderState) {
	controller := ensureRequirementReaderController(reader)
	controller.findOpen = false
	controller.findDraft = ""
	controller.findFocusPending = false
	controller.find.close()
	s.prepareRequirementMarkdownReader(reader)
}

// bindVisibleRequirementReaderShortcuts runs from the window-root context after
// the active view has rendered. Registering from the nested reader element
// would require a focused descendant, so Ctrl+F and F3 would otherwise stop
// working when focus is on another control or nowhere in the pane.
func (s *app) bindVisibleRequirementReaderShortcuts(c *ui.Context) {
	if s == nil || c == nil || s.requirementContentJobs == nil {
		return
	}
	key := s.requirementContentJobs.visible
	if key == "" {
		return
	}
	reader := s.requirementReaders[key]
	if reader == nil || reader.epoch != s.requirementReaderGeneration {
		return
	}
	c.OnShortcut(ui.Ctrl, ui.KeyF, func() { s.openRequirementMarkdownFind(c, reader) })
	c.OnShortcut(ui.Super, ui.KeyF, func() { s.openRequirementMarkdownFind(c, reader) })
	c.OnShortcut(0, ui.KeyF3, func() { s.moveRequirementMarkdownFind(reader, 1) })
	c.OnShortcut(ui.Shift, ui.KeyF3, func() { s.moveRequirementMarkdownFind(reader, -1) })
}

func (s *app) renderRequirementMarkdownFindBar(c *ui.Context, p palette, reader *requirementReaderState, controller *readonlyMarkdownReaderState, key string) {
	f := &controller.find
	input := ui.TextInput(c.Key(key+"-query"), &controller.findDraft).Grow(1).MinWidth(90).MaxWidth(250).Height(30).Padding(7).
		Placeholder("查找正文和评论").Label("查找正文和评论").Background(p.bg).Border(1, p.line).Radius(6).Bind(&f.focus)
	input.OnChange(func() {
		f.set(requirementReaderFindIdentity(reader, controller.sourceMode), controller.findDraft, requirementFindBlocksForReader(reader, controller.sourceMode))
		s.prepareRequirementMarkdownReader(reader)
	})
	input.OnSubmit(func() { s.moveRequirementMarkdownFind(reader, 1) })
	input.OnShortcut(0, ui.KeyEscape, func() { s.closeRequirementMarkdownFind(reader) })
	if iconButtonDisabled(c, key+"-previous", "chevron-left", "上一个结果 · Shift+F3", p, len(f.hits) == 0).Clicked() {
		s.moveRequirementMarkdownFind(reader, -1)
	}
	if iconButtonDisabled(c, key+"-next", "chevron", "下一个结果 · F3", p, len(f.hits) == 0).Clicked() {
		s.moveRequirementMarkdownFind(reader, 1)
	}
	label(c, documentFindStatus(f), 11, p.muted).NoWrap()
	if f.truncated {
		label(c, "仅统计前 10,000 处", 10, p.amber).NoWrap()
	}
	ui.Spacer(c)
	if iconButton(c, key+"-close", "close", "关闭查找", p).Clicked() {
		s.closeRequirementMarkdownFind(reader)
	}
}

// renderRequirementReaderActions lets the requirement page keep reader
// controls beside its heading while the Markdown itself starts immediately.
// A caller may supply its combined metadata/body/comment refresh action.
func (s *app) renderRequirementReaderActions(c *ui.Context, p palette, reader *requirementReaderState, extra func(*overflowMenuBuilder)) {
	controller := s.prepareRequirementMarkdownReader(reader)
	if controller == nil {
		return
	}
	home := s.snapshot.Home
	reader.contextMenu = func(c *ui.Context, native *ui.Menu) {
		if home != s.snapshot.Home || reader.epoch != s.requirementReaderGeneration || s.requirementReaders[requirementReaderKey(reader.taskID, reader.url)] != reader || s.requirementContentJobs != nil && s.requirementContentJobs.visible != requirementReaderKey(reader.taskID, reader.url) {
			return
		}
		native.Separator()
		menu := &overflowMenuBuilder{native: native}
		if menu.Item("find", "查找正文和评论 · Ctrl+F").Icon("search").Chosen() {
			s.openRequirementMarkdownFind(c, reader)
		}
		mode := "查看 Markdown 源码"
		if controller.sourceMode {
			mode = "查看 Markdown 预览"
		}
		if menu.Item("toggle-markdown-mode", mode).Icon("code").Chosen() {
			controller.sourceMode = !controller.sourceMode
			s.prepareRequirementMarkdownReader(reader)
		}
		if extra != nil {
			menu.Separator()
			extra(menu)
			return
		}
		menu.Separator()
		if menu.Item("refresh-reader", "刷新正文和评论").Icon("refresh").Disabled(requirementReaderLoading(reader)).Chosen() {
			s.refreshRequirementReader(reader, nil)
		}
	}
}

func (s *app) renderRequirementReadonlyMarkdownReader(c *ui.Context, p palette, reader *requirementReaderState) {
	if reader == nil {
		label(c, "Markdown 预览暂不可用。", 13, p.muted)
		return
	}
	controller := s.prepareRequirementMarkdownReader(reader)
	if controller == nil {
		return
	}
	if reader.contextMenu == nil {
		s.renderRequirementReaderActions(c, p, reader, nil)
	}
	key := requirementReaderControlKey(reader)

	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		if controller.findOpen {
			ui.Row(c).FillWidth().Shrink(0).Gap(8).Padding(6, 10).AlignItems(ui.Center).Children(func() {
				s.renderRequirementMarkdownFindBar(c, p, reader, controller, key)
			})
			if controller.findFocusPending {
				focused := controller.find.focus.Focused(c)
				controller.find.focus.Focus(c)
				if focused {
					controller.findFocusPending = false
				}
			}
		}
		scroll := readonlyMarkdownReaderScroll(controller, controller.sourceMode)
		// Preview text wraps to the available reading width. Wide tables and
		// original/code blocks own their horizontal scrolling; source mode keeps
		// the whole document's horizontal scroll for unwrapped Markdown lines.
		var preview ui.Element
		if controller.sourceMode {
			preview = ui.ScrollBoth(c)
		} else {
			preview = ui.Scroll(c)
		}
		preview = preview.TrackScroll(scroll).FillWidth().MinWidth(0).Grow(1).MinHeight(0)
		preview.ContextMenu(func(menu *ui.Menu) {
			menu.EditItems()
			if reader.contextMenu != nil {
				reader.contextMenu(c, menu)
			}
		})
		preview.Draw(func(_ *ui.Painter, rect ui.Rect) {
			controller.find.viewport = rect
			if reader.bodyMarkdown != nil && reader.bodyMarkdown.readonlyReader != nil {
				reader.bodyMarkdown.readonlyReader.find.viewport = rect
			}
			if reader.commentsMarkdown != nil && reader.commentsMarkdown.readonlyReader != nil {
				reader.commentsMarkdown.readonlyReader.find.viewport = rect
			}
			for _, state := range reader.commentMarkdown {
				if state != nil && state.readonlyReader != nil {
					state.readonlyReader.find.viewport = rect
				}
			}
		})
		preview = preview.HandleInput(func(event ui.InputEvent) bool {
			if event.Kind != ui.InputScroll || event.Mods&ui.Ctrl == 0 || event.DY == 0 {
				return false
			}
			step := float32(-.1)
			if event.DY < 0 {
				step = .1
			}
			controller.zoom = float32(math.Max(.7, math.Min(1.6, math.Round(float64(controller.zoom+step)*10)/10)))
			s.prepareRequirementMarkdownReader(reader)
			return true
		})
		preview.Padding(18, 26, 32, 26).Children(func() {
			fetching := reader.body == nil && reader.comments == nil &&
				(reader.bodyLoading || reader.commentsLoading)
			if fetching {
				label(c, "获取中", 13, p.muted)
			}
			showBody := reader.body != nil || reader.bodyError != "" || !reader.bodyLoading && !fetching
			if showBody {
				s.renderRequirementMarkdownSection(c, p, reader, "body")
			}
			showComments := reader.comments != nil || reader.commentsError != ""
			if showComments {
				ui.Column(c).FillWidth().Padding(28, 0, 14, 0).Gap(12).Label("评论分区").Children(func() {
					ui.Divider(c)
					ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, requirementCommentsIcon).Size(15, 15).TextColor(p.muted)
						label(c, "评论", scaleMarkdownSize(15, controller.zoom), p.text).FontWeight(600)
						if reader.comments != nil {
							label(c, fmt.Sprint(len(reader.comments.Comments)), scaleMarkdownSize(12, controller.zoom), p.muted)
						}
					})
				})
				s.renderRequirementMarkdownSection(c, p, reader, "comments")
			}
		})
	})
}

func (s *app) renderRequirementMarkdownSection(c *ui.Context, p palette, reader *requirementReaderState, section string) {
	s.renderRequirementSectionStatus(c, p, reader, section)
	var state *markdownEditorState
	switch section {
	case "body":
		state = reader.bodyMarkdown
		if state == nil {
			switch {
			case reader.bodyLoading:
				// The reader owns a single, shared "获取中" message.
			case reader.bodyError != "":
				return
			default:
				// The section status owns the one missing-content message and retry.
			}
			return
		}
		if state.doc == nil || strings.TrimSpace(state.doc.Source()) == "" {
			label(c, "该工作项没有可读取的正文。", 13, p.muted)
			return
		}
	case "comments":
		controller := ensureRequirementReaderController(reader)
		if !controller.sourceMode {
			if reader.comments == nil || reader.commentsMarkdown == nil {
				return
			}
			s.renderRequirementCommentList(c, p, reader)
			return
		}
		state = reader.commentsMarkdown
		if state == nil {
			switch {
			case reader.commentsLoading:
				// Comment loading is represented by the reader's shared message.
			case reader.commentsError != "":
				return
			default:
				// Empty comments are hidden until the result or an error is ready.
			}
			return
		}
		if state.doc == nil || reader.comments == nil || len(reader.comments.Comments) == 0 {
			label(c, "暂无评论。", 13, p.muted)
			return
		}
	default:
		return
	}
	local := ensureReadonlyMarkdownReader(state)
	if local.sourceMode {
		renderReadonlyMarkdownSource(ui.Column(c).FillWidth(), c, p, state, local, 0)
		return
	}
	s.renderMarkdownPreview(c, p, state)
}

func (s *app) renderRequirementCommentList(c *ui.Context, p palette, reader *requirementReaderState) {
	if reader == nil || reader.comments == nil || len(reader.comments.Comments) == 0 {
		label(c, "暂无评论。", 13, p.muted)
		return
	}
	byID := make(map[string]domain.RequirementComment, len(reader.comments.Comments))
	for _, comment := range reader.comments.Comments {
		byID[comment.ID] = comment
	}
	for index, comment := range reader.comments.Comments {
		if index > 0 {
			ui.Divider(c).Padding(9, 0)
		}
		key := requirementCommentStateKey(comment, index)
		state := reader.commentMarkdown[key]
		if state == nil || state.closed || state.doc == nil {
			continue
		}
		indent := float32(0)
		var parentLabel string
		if comment.ParentID != nil {
			indent = 14
			parentLabel = "回复 " + firstNonEmpty(byID[*comment.ParentID].AuthorName, *comment.ParentID)
		}
		ui.Column(c.Key(requirementReaderControlKey(reader)+"-comment-"+key)).FillWidth().MinWidth(0).Gap(8).Padding(0, 0, 0, indent).Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, firstNonEmpty(comment.AuthorName, comment.AuthorKey, "未知用户"), 12, p.text).FontWeight(600).SingleLine().Tooltip(firstNonEmpty(comment.AuthorName, comment.AuthorKey, "未知用户"))
				label(c, firstNonEmpty(comment.CreatedAt, "时间未知"), 11, p.muted).SingleLine()
				if parentLabel != "" {
					ui.Spacer(c)
					label(c, parentLabel, 10, p.muted).SingleLine().Tooltip(parentLabel)
				}
			})
			ui.Column(c).FillWidth().MinWidth(0).Label("评论正文 " + firstNonEmpty(comment.AuthorName, comment.AuthorKey, "未知用户")).Gap(3).Children(func() {
				s.renderMarkdownPreview(c, p, state)
			})
			for _, resource := range reader.comments.CommentResources {
				if resource.CommentID != comment.ID || resource.Path != "" {
					continue
				}
				resourceKey := commentResourceKey(resource)
				failure := firstNonEmpty(reader.commentResourceErrors[resourceKey], resource.Error)
				if failure == "" {
					continue
				}
				ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
					label(c, "图片加载失败", 11, p.amber).Grow(1).MinWidth(0).SingleLine().Tooltip(failure)
					disabled := reader.commentsLoading || reader.commentResourceLoading != ""
					if iconButtonDisabled(c, requirementReaderControlKey(reader)+"-retry-comment-image-"+resourceKey, "refresh", "重试评论图片", p, disabled).Clicked() {
						s.requestRequirementCommentResource(reader, resource, true)
					}
				})
			}
		})
	}
}

func (s *app) renderRequirementSectionStatus(c *ui.Context, p palette, reader *requirementReaderState, section string) {
	if reader == nil {
		return
	}
	var messages []string
	var retry, loading bool
	switch section {
	case "body":
		body := reader.body
		if reader.bodyError != "" {
			if body != nil {
				messages = append(messages, "正文刷新失败，已保留当前内容："+reader.bodyError)
			} else {
				messages = append(messages, "正文读取失败："+reader.bodyError)
			}
			retry = true
		}
		if body != nil {
			if body.CacheError != "" {
				messages = append(messages, "正文缓存失败："+body.CacheError)
				retry = true
			}
			missing := 0
			for _, resource := range body.Resources {
				if resource.Path != "" {
					continue
				}
				missing++
				failure := firstNonEmpty(reader.resourceErrors[resource.URL], resource.Error)
				if failure != "" {
					messages = append(messages, "图片缓存失败："+failure)
					retry = true
				}
			}
			if missing > 0 && reader.resourceLoading == "" && !retry {
				messages = append(messages, fmt.Sprintf("%d 张需求图片尚未缓存。", missing))
				retry = true
			}
		}
		if reader.resourceLoading != "" {
			loading = true
		}
		loading = loading || reader.bodyLoading
		if body == nil && reader.bodyError == "" && !reader.bodyLoading {
			messages = append(messages, "正文尚未读取。")
			retry = true
		}
	case "comments":
		comments := reader.comments
		if reader.commentsError != "" {
			if comments != nil {
				messages = append(messages, "评论刷新失败，已保留当前内容："+reader.commentsError)
			} else {
				messages = append(messages, "评论读取失败："+reader.commentsError)
			}
			retry = true
		}
		if comments != nil {
			if comments.CacheError != "" {
				messages = append(messages, "评论缓存失败："+comments.CacheError)
				retry = true
			}
			messages = append(messages, comments.Warnings...)
		}
		loading = reader.commentsLoading
		if comments == nil && reader.commentsError == "" && !reader.commentsLoading {
			messages = append(messages, "评论尚未读取。")
			retry = true
		}
	default:
		return
	}
	if len(messages) == 0 {
		return
	}
	color := p.muted
	if retry {
		color = p.amber
		if section == "body" && reader.body == nil || section == "comments" && reader.comments == nil {
			color = p.danger
		}
	}
	key := requirementReaderControlKey(reader) + "-retry-" + section
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, strings.Join(messages, "\n"), 12, color).Grow(1).MinWidth(0).Wrap().Selectable()
		if retry {
			if iconButtonDisabled(c, key, "refresh", "重试"+map[string]string{"body": "正文", "comments": "评论"}[section], p, loading).Clicked() {
				s.requestRequirementContent(reader, section == "comments", true)
			}
		}
	})
}
