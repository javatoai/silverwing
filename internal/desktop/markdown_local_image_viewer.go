package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

func (s *app) openMarkdownLocalImageViewer(owner *markdownEditorState, sourcePath string) {
	if owner == nil || owner.closed || !owner.commentReading || !owner.detached || owner.doc == nil || sourcePath == "" {
		return
	}
	viewer := &markdownBitmapViewerState{
		owner: owner, document: owner.doc, source: owner.doc.Source(), sourcePath: sourcePath,
		page: s.page, settingsPage: s.settingsPage, tab: s.tab,
		media: &nativeMediaState{kind: "image", imageExpanded: true, imageZoom: 1},
	}
	if !s.bindMarkdownBitmapViewerOwner(viewer, owner) {
		return
	}
	s.dismissMarkdownBitmapViewer()
	s.markdownBitmapViewer = viewer
	owner.closeBitmapViewer = func() { s.closeMarkdownBitmapViewerForOwner(owner) }
	s.requestMarkdownLocalImageViewer(viewer)
}

func (s *app) requestMarkdownLocalImageViewer(viewer *markdownBitmapViewerState) {
	if !markdownLocalImageViewerAttached(s, viewer) || viewer.media == nil || !viewer.media.imageExpanded || viewer.media.imageExpandedLoading {
		return
	}
	ctx, generation := beginMarkdownLocalImageViewerRequest(viewer)
	mediaState := viewer.media
	win := s.win
	if win == nil || win.IsDestroyed() {
		viewer.cancel()
		viewer.cancel = nil
		viewer.requestCtx = nil
		mediaState.imageExpandedLoading = false
		mediaState.imageExpandedError = "窗口不可用，无法读取评论原图。"
		mediaState.imageExpandedCancel = nil
		return
	}
	go func() {
		bitmap, info, err := loadMarkdownLocalImageBitmap(ctx, viewer.sourcePath)
		if ctx.Err() != nil {
			return
		}
		win.Update(func() {
			if !markdownLocalImageViewerResultCurrent(s, viewer, generation) {
				return
			}
			if viewer.cancel != nil {
				viewer.cancel()
			}
			viewer.cancel = nil
			viewer.requestCtx = nil
			mediaState.imageExpandedLoading = false
			mediaState.imageExpandedCancel = nil
			if err != nil {
				mediaState.imageExpandedError = err.Error()
				return
			}
			mediaState.imageInfo = info
			mediaState.imageExpandedBitmap = bitmap
		})
	}()
}

func beginMarkdownLocalImageViewerRequest(viewer *markdownBitmapViewerState) (context.Context, uint64) {
	if viewer.cancel != nil {
		viewer.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	viewer.generation++
	viewer.requestCtx = ctx
	viewer.cancel = cancel
	viewer.media.imageExpandedLoading = true
	viewer.media.imageExpandedError = ""
	viewer.media.imageExpandedCancel = cancel
	return ctx, viewer.generation
}

func markdownLocalImageViewerAttached(s *app, viewer *markdownBitmapViewerState) bool {
	return s != nil && viewer != nil && s.markdownBitmapViewer == viewer && viewer.sourcePath != "" &&
		viewer.owner != nil && !viewer.owner.closed && viewer.owner.commentReading && viewer.owner.detached &&
		viewer.document != nil && viewer.owner.doc == viewer.document && viewer.document.Source() == viewer.source &&
		s.page == viewer.page && s.settingsPage == viewer.settingsPage && s.tab == viewer.tab &&
		s.markdownBitmapViewerCommentContextCurrent(viewer)
}

func markdownLocalImageViewerResultCurrent(s *app, viewer *markdownBitmapViewerState, generation uint64) bool {
	return markdownLocalImageViewerAttached(s, viewer) && viewer.generation == generation &&
		viewer.requestCtx != nil && viewer.requestCtx.Err() == nil && viewer.media != nil && viewer.media.imageExpanded
}

func loadMarkdownLocalImageBitmap(ctx context.Context, sourcePath string) (*ui.Bitmap, media.ImageInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sourcePath == "" {
		return nil, media.ImageInfo{}, errors.New("评论原图缓存路径为空")
	}
	document, err := media.OpenImage(ctx, sourcePath, media.Options{
		MaxImageBytes: media.DefaultMaxImageBytes, MaxImagePixels: media.DefaultMaxImagePixels, CacheBytes: 1,
	})
	if err != nil {
		return nil, media.ImageInfo{}, fmt.Errorf("读取评论原图失败：%w", err)
	}
	defer document.Close()
	info := document.Info()
	rendered, err := document.RenderPNG(ctx, 0)
	if err != nil {
		return nil, media.ImageInfo{}, fmt.Errorf("解码评论原图失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, media.ImageInfo{}, fmt.Errorf("读取评论原图已取消：%w", err)
	}
	bitmap, err := ui.DecodeBitmap(rendered.PNG)
	if err != nil {
		return nil, media.ImageInfo{}, fmt.Errorf("准备评论原图预览失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, media.ImageInfo{}, fmt.Errorf("读取评论原图已取消：%w", err)
	}
	width, height := bitmap.Size()
	if width != info.Width || height != info.Height {
		return nil, media.ImageInfo{}, fmt.Errorf("评论原图尺寸不匹配：原图 %d × %d，预览 %d × %d", info.Width, info.Height, width, height)
	}
	return bitmap, info, nil
}

func (s *app) bindMarkdownBitmapViewerOwner(viewer *markdownBitmapViewerState, owner *markdownEditorState) bool {
	if viewer == nil || owner == nil {
		return false
	}
	if !owner.commentReading {
		return true
	}
	if isCommentDraftMarkdownOwner(owner) {
		if s.commentComposers == nil {
			return false
		}
		viewer.composer = s.commentComposers.active
		return s.commentComposerImageCurrent(viewer)
	}
	reader, readerKey, epoch, taskID, itemKey, ok := s.activeMarkdownCommentReader(owner)
	if !ok {
		return !isRequirementCommentMarkdownOwner(owner)
	}
	viewer.reader = reader
	viewer.readerKey = readerKey
	viewer.readerEpoch = epoch
	viewer.taskID = taskID
	viewer.itemKey = itemKey
	return s.markdownBitmapViewerCommentContextCurrent(viewer)
}

func requirementReaderForCommentMarkdownOwner(s *app, owner *markdownEditorState) *requirementReaderState {
	if s == nil || owner == nil {
		return nil
	}
	for _, reader := range s.requirementReaders {
		for _, state := range reader.commentMarkdown {
			if state == owner {
				return reader
			}
		}
	}
	return nil
}

func (s *app) markdownBitmapViewerCommentContextCurrent(viewer *markdownBitmapViewerState) bool {
	if viewer == nil || viewer.owner == nil || !viewer.owner.commentReading {
		return viewer != nil && viewer.owner != nil
	}
	if viewer.composer != nil || isCommentDraftMarkdownOwner(viewer.owner) {
		return s.commentComposerImageCurrent(viewer)
	}
	reader := viewer.reader
	if reader == nil {
		return !isRequirementCommentMarkdownOwner(viewer.owner)
	}
	if reader == nil || viewer.readerKey != requirementReaderKey(reader.taskID, reader.url) ||
		viewer.readerEpoch != reader.epoch || reader.epoch != s.requirementReaderGeneration ||
		s.requirementReaders[viewer.readerKey] != reader || !requirementReaderOwnsCommentMarkdown(reader, viewer.owner) {
		return false
	}
	switch viewer.page {
	case "tasks", "archive":
		current := s.currentTask()
		return viewer.taskID != "" && current.id == viewer.taskID && reader.taskID == viewer.taskID &&
			sameRequirementReaderLink(reader.url, current.requirementURL)
	case "requirements":
		if viewer.itemKey == "" || s.selectedRequirementKey != viewer.itemKey {
			return false
		}
		selected, ok := s.selectedRequirementByKey(viewer.itemKey)
		return ok && reader.taskID == "" && sameRequirementReaderLink(reader.url, s.requirements[selected].URL)
	default:
		return false
	}
}

func isRequirementCommentMarkdownOwner(owner *markdownEditorState) bool {
	return owner != nil && strings.HasPrefix(owner.taskID, "requirement-comment:")
}

func (s *app) activeMarkdownCommentReader(owner *markdownEditorState) (*requirementReaderState, string, uint64, string, string, bool) {
	reader := requirementReaderForCommentMarkdownOwner(s, owner)
	if reader == nil {
		return nil, "", 0, "", "", false
	}
	readerKey := requirementReaderKey(reader.taskID, reader.url)
	if reader.epoch != s.requirementReaderGeneration || s.requirementReaders[readerKey] != reader {
		return nil, "", 0, "", "", false
	}
	switch s.page {
	case "tasks", "archive":
		current := s.currentTask()
		if current.id == "" || current.id != reader.taskID || !sameRequirementReaderLink(reader.url, current.requirementURL) {
			return nil, "", 0, "", "", false
		}
		return reader, readerKey, reader.epoch, current.id, "", true
	case "requirements":
		if s.selectedRequirementKey == "" {
			return nil, "", 0, "", "", false
		}
		selected, ok := s.selectedRequirementByKey(s.selectedRequirementKey)
		if !ok || reader.taskID != "" || !sameRequirementReaderLink(reader.url, s.requirements[selected].URL) {
			return nil, "", 0, "", "", false
		}
		return reader, readerKey, reader.epoch, "", s.selectedRequirementKey, true
	default:
		return nil, "", 0, "", "", false
	}
}

func requirementReaderOwnsCommentMarkdown(reader *requirementReaderState, owner *markdownEditorState) bool {
	if reader == nil || owner == nil {
		return false
	}
	for _, state := range reader.commentMarkdown {
		if state == owner {
			return true
		}
	}
	return false
}
