package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/media"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

type markdownLinkFilePreview struct {
	open, loading             bool
	home                      string
	originScope               string
	owner                     *markdownEditorState
	ownerVersion              uint64
	composer                  *requirementCommentComposer
	draftID                   string
	target                    markdownui.LinkTarget
	ctx                       context.Context
	cancel                    context.CancelFunc
	content                   documents.Content
	errorText                 string
	reader                    *markdownui.ReaderState
	code                      nativeCodePreviewState
	positioned                bool
	bitmap                    *ui.Bitmap
	bitmapWidth, bitmapHeight int
	pdf                       *media.PDFDocument
	page, pages               int
	request                   uint64
}

func (s *app) markdownLinkFileCurrent(viewer *markdownLinkFilePreview) bool {
	if viewer == nil || s.materialsUnit.state.markdownLinkFile != viewer || !viewer.open || viewer.ctx.Err() != nil || s.snapshot.Home != viewer.home || viewer.owner == nil || viewer.owner.closed {
		return false
	}
	if viewer.originScope != "" && viewer.originScope != s.markdownLinkOriginScope() {
		return false
	}
	if viewer.owner.doc == nil || viewer.owner.doc.Snapshot().Version != viewer.ownerVersion {
		return false
	}
	if isCommentDraftMarkdownOwner(viewer.owner) {
		state, composers := viewer.composer, s.requirementUnit.state.commentComposers
		return composers != nil && state != nil && composers.active == state && state.open &&
			state.navigation == s.commentComposerNavigation() && viewer.owner.taskID == "comment-draft:"+viewer.draftID &&
			s.commentMarkdownCurrent(state, viewer.owner, viewer.draftID)
	}
	if isSkillMarkdownPreview(viewer.owner) {
		return s.skillMarkdownPreviewCurrent(viewer.owner)
	}
	if !viewer.owner.detached {
		return s.markdownEditorIsCurrent(viewer.owner)
	}
	return true
}

func (s *app) markdownLinkOriginScope() string {
	task := s.taskUnit.currentTask()
	return strings.Join([]string{s.snapshot.Home, s.page, fmt.Sprint(s.tab), s.settingsUnit.state.settingsPage, s.materialsUnit.state.documentPath, task.id, task.createdAt, task.materialsPath}, "\x00")
}

func (s *app) closeMarkdownLinkFile() {
	s.closeMarkdownLinkFilePreview(s.materialsUnit.state.markdownLinkFile)
}

func (s *app) closeMarkdownLinkFilePreview(viewer *markdownLinkFilePreview) {
	if viewer == nil {
		return
	}
	if s.materialsUnit.state.markdownLinkFile == viewer {
		s.materialsUnit.state.markdownLinkFile = nil
	}
	viewer.open = false
	if viewer.cancel != nil {
		viewer.cancel()
	}
	if viewer.reader != nil {
		viewer.reader.Close()
	}
	if viewer.pdf != nil {
		document := viewer.pdf
		viewer.pdf = nil
		go document.Close()
	}
}

func (s *app) openMarkdownFileLink(owner *markdownEditorState, target markdownui.LinkTarget) {
	if owner == nil || owner.closed || owner.doc == nil {
		return
	}
	if owner.live != nil {
		if err := s.captureMarkdownEditorInput(owner); err != nil {
			s.lastError = err.Error()
			return
		}
	}
	s.closeMarkdownLinkFile()
	ctx, cancel := context.WithCancel(context.Background())
	viewer := &markdownLinkFilePreview{open: true, loading: true, home: s.snapshot.Home, owner: owner, ownerVersion: owner.doc.Snapshot().Version, target: target, ctx: ctx, cancel: cancel}
	viewer.originScope = s.markdownLinkOriginScope()
	if isCommentDraftMarkdownOwner(owner) && s.requirementUnit.state.commentComposers != nil {
		viewer.composer = s.requirementUnit.state.commentComposers.active
		if viewer.composer != nil {
			viewer.draftID = viewer.composer.draft.ID
		}
	}
	s.materialsUnit.state.markdownLinkFile = viewer
	if !s.markdownLinkFileCurrent(viewer) {
		s.closeMarkdownLinkFilePreview(viewer)
		return
	}
	action := application.Action{Kind: "document.link-preview", Path: target.Path, Payload: map[string]any{"sourcePath": owner.path}}
	if !owner.detached && owner.binding == nil && owner.taskID == s.taskUnit.currentTask().id {
		task := s.taskUnit.currentTask()
		action.TaskID = task.id
		action.Payload["draftTaskPath"] = task.path
		action.Payload["draftMaterialsRoot"] = task.materialsPath
		action.Payload["desktopRenameTaskCreatedAt"] = task.createdAt
		action.Payload["materialsHome"] = s.snapshot.Home
	}
	backend, window := s.backend, s.win
	if backend == nil || window == nil || window.IsDestroyed() {
		viewer.loading = false
		viewer.errorText = "窗口或文件读取服务不可用"
		return
	}
	go func() {
		result, err := backend.Dispatch(ctx, action)
		if ctx.Err() != nil || window.IsDestroyed() {
			return
		}
		window.Update(func() {
			if !s.markdownLinkFileCurrent(viewer) {
				return
			}
			viewer.loading = false
			if err != nil {
				viewer.errorText = err.Error()
				return
			}
			encoded, encodeErr := json.Marshal(result.Data)
			if encodeErr != nil {
				viewer.errorText = encodeErr.Error()
				return
			}
			if err := json.Unmarshal(encoded, &viewer.content); err != nil {
				viewer.errorText = err.Error()
				return
			}
			viewer.content.ReadOnly = true
			if viewer.content.Kind == "markdown" {
				viewer.reader = markdownui.NewReaderState(markdownui.Document{ID: "link-file:" + viewer.content.Path, Path: viewer.content.Path, Source: viewer.content.Text})
				viewer.reader.RevealLocation(viewer.target.Line, viewer.target.Fragment)
			}
			if viewer.content.Kind == "image" || viewer.content.Kind == "pdf" {
				s.loadMarkdownLinkMedia(viewer)
			}
		})
	}()
}

func (s *app) loadMarkdownLinkMedia(viewer *markdownLinkFilePreview) {
	if !s.markdownLinkFileCurrent(viewer) || s.win == nil || viewer.loading {
		return
	}
	viewer.loading = true
	viewer.request++
	generation := viewer.request
	window, ctx := s.win, viewer.ctx
	pdf, page, path := viewer.pdf, viewer.page, viewer.content.Path
	go func() {
		var bitmap *ui.Bitmap
		var err error
		var width, height, pages int
		opened := false
		if viewer.content.Kind == "image" {
			bitmap, err = decodeNativeImagePreview(ctx, path, 1400)
			if err == nil {
				document, openErr := media.OpenImage(ctx, path, media.Options{})
				if openErr == nil {
					info := document.Info()
					width, height = info.Width, info.Height
					_ = document.Close()
				}
			}
		} else {
			if pdf == nil {
				pdf, err = media.OpenPDF(ctx, path, media.Options{PDFiumPath: nativePDFiumPath()})
				opened = err == nil
			}
			if err == nil {
				pages = pdf.Info().PageCount
				var rendered media.RenderedPage
				rendered, err = pdf.RenderPagePNG(ctx, page, 1200)
				if err == nil {
					bitmap, err = ui.DecodeBitmap(rendered.PNG)
					if config, _, e := image.DecodeConfig(bytes.NewReader(rendered.PNG)); e == nil {
						width, height = config.Width, config.Height
					}
				}
			}
		}
		if ctx.Err() != nil || window.IsDestroyed() {
			if opened && pdf != nil {
				_ = pdf.Close()
			}
			return
		}
		window.Update(func() {
			if !s.markdownLinkFileCurrent(viewer) || viewer.request != generation {
				if opened && pdf != nil {
					go pdf.Close()
				}
				return
			}
			viewer.loading = false
			if opened {
				viewer.pdf = pdf
			}
			if err != nil {
				viewer.errorText = err.Error()
				return
			}
			viewer.bitmap, viewer.bitmapWidth, viewer.bitmapHeight = bitmap, width, height
			viewer.pages = pages
		})
	}()
}

func (s *app) renderMarkdownLinkFile(c *ui.Context, p palette) {
	if viewer := s.materialsUnit.state.markdownLinkFile; viewer != nil && viewer.composer != nil {
		return // The owning composer is drawn later in the frame.
	}
	s.renderMarkdownLinkFileLayer(c, p)
}

func (s *app) renderCommentDraftMarkdownLinkFile(c *ui.Context, p palette) {
	if viewer := s.materialsUnit.state.markdownLinkFile; viewer != nil && viewer.composer != nil {
		s.renderMarkdownLinkFileLayer(c, p)
	}
}

func (s *app) renderMarkdownLinkFileLayer(c *ui.Context, p palette) {
	viewer := s.materialsUnit.state.markdownLinkFile
	if viewer == nil {
		return
	}
	if !s.markdownLinkFileCurrent(viewer) {
		s.closeMarkdownLinkFilePreview(viewer)
		return
	}
	width, height := c.Size()
	ui.DialogBase(c.Key("markdown-file-link-preview"), &viewer.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .32))
		panel.Width(max(float32(240), min(float32(1150), width-48))).Height(max(float32(200), height-70)).Padding(16).Gap(10).Background(p.surface).Radius(12)
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					label(c, filepath.Base(viewer.target.Path)+" · 只读", 15, p.text).FontWeight(600).SingleLine()
					label(c, viewer.target.Path, 11, p.muted).SingleLine().Tooltip(viewer.target.Path)
				})
				if action(c, "link-file-return", "chevron-left", "返回原文", p, false) {
					viewer.open = false
				}
			})
			if viewer.loading {
				label(c, "正在读取文件…", 13, p.muted)
			}
			if viewer.errorText != "" {
				label(c, viewer.errorText, 13, p.danger).Wrap()
			}
			if !viewer.loading && viewer.errorText == "" {
				switch viewer.content.Kind {
				case "markdown":
					markdownui.MarkdownReader(c, viewer.reader, markdownui.ReaderOptions{Theme: &md.Theme{Font: markdownSansFontFamily, FontSize: 15, Text: p.text, Muted: p.muted, Link: p.blue, CodeFont: "monospace", TableBorder: p.line, TableHeader: p.soft}, OnNavigate: func(target markdownui.LinkTarget) {
						if target.URL != "" {
							s.act("system.open", "", "", "", target.URL, "", nil)
						} else {
							s.openMarkdownFileLink(viewer.owner, target)
						}
					}, OnError: func(err error) { viewer.errorText = err.Error() }}).Grow(1)
				case "text", "json", "table":
					view := viewer.code.bind("link-file:"+viewer.content.Path, viewer.content.Path, viewer.content.Text)
					if !viewer.positioned && viewer.target.Line > 0 {
						at := markdownLinkLocationOffset(viewer.content.Text, viewer.target.Line, viewer.target.Column)
						view.Select(at, at)
						viewer.positioned = true
					}
					renderNativeCodePreview(c, p, &viewer.code, "link-file:"+viewer.content.Path, viewer.content.Path, viewer.content.Text, "link-file-code").Grow(1)
				case "image", "pdf":
					if viewer.pages > 0 {
						ui.Row(c).Gap(8).Children(func() {
							if ui.Button(c, "上一页").Disabled(viewer.page == 0).Clicked() {
								viewer.page--
								s.loadMarkdownLinkMedia(viewer)
							}
							label(c, fmt.Sprintf("%d / %d", viewer.page+1, viewer.pages), 12, p.text)
							if ui.Button(c, "下一页").Disabled(viewer.page+1 >= viewer.pages).Clicked() {
								viewer.page++
								s.loadMarkdownLinkMedia(viewer)
							}
						})
					}
					if viewer.bitmap != nil {
						displayWidth := min(float32(1050), width-100)
						displayHeight := displayWidth
						if viewer.bitmapWidth > 0 {
							displayHeight = displayWidth * float32(viewer.bitmapHeight) / float32(viewer.bitmapWidth)
						}
						ui.ScrollBoth(c).Grow(1).Children(func() { ui.Image(c, viewer.bitmap).Width(displayWidth).Height(displayHeight) })
					}
				default:
					label(c, "此格式暂不支持应用内预览。", 13, p.muted)
				}
			}
			if viewer.errorText != "" || viewer.content.Kind == "unsupported" {
				if action(c, "link-file-system-open", "external-link", "使用系统程序打开", p, false) {
					s.act("system.open", "", "", "", viewer.target.Path, "", nil)
				}
			}
		})
	})
	if !viewer.open {
		s.closeMarkdownLinkFilePreview(viewer)
	}
}

func markdownLinkLocationOffset(source string, line, column int) int {
	source = strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
	at := 0
	for current := 1; current < line; current++ {
		next := strings.IndexByte(source[at:], '\n')
		if next < 0 {
			return len(source)
		}
		at += next + 1
	}
	for current := 1; current < column && at < len(source) && source[at] != '\n'; current++ {
		_, size := utf8.DecodeRuneInString(source[at:])
		at += size
	}
	return at
}
