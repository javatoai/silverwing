package desktop

import (
	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

type markdownBitmapViewerState struct {
	owner        *markdownEditorState
	source       string
	page         string
	settingsPage string
	tab          int
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
	s.markdownBitmapViewer = &markdownBitmapViewerState{
		owner: owner, source: owner.doc.Source(), page: s.page,
		settingsPage: s.settingsPage, tab: s.tab,
		media: &nativeMediaState{
			kind: "image", imageExpanded: true, imageExpandedBitmap: bitmap,
			imageInfo: media.ImageInfo{Width: width, Height: height}, imageZoom: 1,
		},
	}
}

func (s *app) renderMarkdownBitmapViewer(c *ui.Context, p palette) {
	viewer := s.markdownBitmapViewer
	if viewer == nil {
		return
	}
	if viewer.owner.closed || viewer.owner.doc == nil || viewer.owner.doc.Source() != viewer.source ||
		s.page != viewer.page || s.settingsPage != viewer.settingsPage || s.tab != viewer.tab {
		s.markdownBitmapViewer = nil
		return
	}
	s.renderNativeImageViewer(c, p, viewer.media)
	if !viewer.media.imageExpanded {
		s.markdownBitmapViewer = nil
	}
}
