package desktop

import (
	"context"

	"silverwing-go/internal/editor"
	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

type markdownBitmapViewerState struct {
	owner        *markdownEditorState
	document     *editor.Document
	reader       *requirementReaderState
	composer     *requirementCommentComposer
	readerKey    string
	readerEpoch  uint64
	taskID       string
	itemKey      string
	source       string
	sourcePath   string
	page         string
	settingsPage string
	tab          int
	generation   uint64
	requestCtx   context.Context
	cancel       context.CancelFunc
	media        *nativeMediaState
}

// The document's decoded bitmap is shared with the native viewer. Expanding
// an image or diagram does not reload the file or start another renderer.
func (s *app) openMarkdownBitmapViewer(owner *markdownEditorState, bitmap *ui.Bitmap) {
	if owner == nil || owner.closed || owner.doc == nil || bitmap == nil {
		return
	}
	width, height := bitmap.Size()
	if width <= 0 || height <= 0 {
		return
	}
	viewer := &markdownBitmapViewerState{
		owner: owner, document: owner.doc, source: owner.doc.Source(), page: s.page,
		settingsPage: s.settingsUnit.state.settingsPage, tab: s.tab,
		media: &nativeMediaState{
			kind: "image", imageExpanded: true, imageExpandedBitmap: bitmap,
			imageInfo: media.ImageInfo{Width: width, Height: height}, imageZoom: 1,
		},
	}
	if !s.bindMarkdownBitmapViewerOwner(viewer, owner) {
		return
	}
	s.dismissMarkdownBitmapViewer()
	s.materialsUnit.state.markdownBitmapViewer = viewer
	owner.closeBitmapViewer = func() { s.closeMarkdownBitmapViewerForOwner(owner) }
}

func (s *app) renderMarkdownBitmapViewer(c *ui.Context, p palette) {
	if viewer := s.materialsUnit.state.markdownBitmapViewer; viewer != nil && viewer.composer != nil {
		return // Its owner dialog is drawn later; render this viewer above it.
	}
	s.renderMarkdownBitmapViewerLayer(c, p)
}

func (s *app) renderCommentDraftBitmapViewer(c *ui.Context, p palette) {
	if viewer := s.materialsUnit.state.markdownBitmapViewer; viewer != nil && viewer.composer != nil {
		s.renderMarkdownBitmapViewerLayer(c, p)
	}
}

func (s *app) renderMarkdownBitmapViewerLayer(c *ui.Context, p palette) {
	viewer := s.materialsUnit.state.markdownBitmapViewer
	if viewer == nil {
		return
	}
	if viewer.owner == nil || viewer.owner.closed || viewer.owner.doc == nil || viewer.owner.doc.Source() != viewer.source ||
		viewer.owner.doc != viewer.document || s.page != viewer.page || s.settingsUnit.state.settingsPage != viewer.settingsPage || s.tab != viewer.tab ||
		viewer.owner.commentReading && !s.markdownBitmapViewerCommentContextCurrent(viewer) {
		s.dismissMarkdownBitmapViewer()
		return
	}
	s.renderNativeImageViewer(c, p, viewer.media)
	if !viewer.media.imageExpanded {
		s.dismissMarkdownBitmapViewer()
		c.Invalidate()
	}
}

func (s *app) dismissMarkdownBitmapViewer() {
	if s == nil || s.materialsUnit.state.markdownBitmapViewer == nil {
		return
	}
	viewer := s.materialsUnit.state.markdownBitmapViewer
	if viewer.composer != nil && s.commentComposerImageCurrent(viewer) {
		viewer.composer.restoreImageFocus = true
	}
	s.materialsUnit.state.markdownBitmapViewer = nil
	if viewer.owner != nil {
		viewer.owner.closeBitmapViewer = nil
	}
	closeMarkdownBitmapViewer(viewer)
}

func (s *app) closeMarkdownBitmapViewerForOwner(owner *markdownEditorState) {
	if s != nil && s.materialsUnit.state.markdownBitmapViewer != nil && s.materialsUnit.state.markdownBitmapViewer.owner == owner {
		s.dismissMarkdownBitmapViewer()
	}
}

func closeMarkdownBitmapViewer(viewer *markdownBitmapViewerState) {
	if viewer == nil {
		return
	}
	viewer.generation++
	if viewer.cancel != nil {
		viewer.cancel()
		viewer.cancel = nil
	} else if viewer.media != nil && viewer.media.imageExpandedCancel != nil {
		viewer.media.imageExpandedCancel()
	}
	viewer.requestCtx = nil
	if viewer.media != nil {
		viewer.media.imageExpanded = false
		viewer.media.imageExpandedLoading = false
		viewer.media.imageExpandedBitmap = nil
		viewer.media.imageExpandedError = ""
		viewer.media.imageExpandedCancel = nil
		viewer.media.imageDragging = false
		viewer.media.imageOffsetX, viewer.media.imageOffsetY = 0, 0
	}
}
