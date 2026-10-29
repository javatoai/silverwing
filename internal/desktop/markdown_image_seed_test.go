package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// Seed the current AST's ready pixels explicitly. Headless rendering does not
// retain pending image nodes without a native window; these UI fixtures test
// reading/lightbox behavior independently from asynchronous image loading.
func seedMarkdownReadyImages(t *testing.T, s *app, state *markdownEditorState, bitmap *ui.Bitmap) {
	t.Helper()
	if state == nil || state.closed || state.doc == nil || bitmap == nil {
		t.Fatal("ready-image fixture needs an open document and decoded pixels")
	}
	native := ensureFrameworkMarkdown(state)
	pool := s.markdownImagePoolFor(state)
	if state.inlineImages == nil {
		state.inlineImages = make(map[string]*markdownInlineImageState)
	}
	count := 0
	for block := 1; block < native.document.Len(); block++ {
		for _, inline := range native.document.Inlines(block) {
			if inline.Kind != parser.Image {
				continue
			}
			part := native.partAt(state, inline.Start)
			node := editor.Node{Kind: "image", Destination: inline.Dest, Range: editor.SourceRange{
				Start: native.projection.OriginalOffset(inline.Start), End: native.projection.OriginalOffset(inline.End)}}
			nodeKey := markdownImageNodeKey(part, node)
			if previous := state.inlineImages[nodeKey]; previous != nil && previous.cancel != nil {
				previous.cancel()
			}
			link := strings.TrimSpace(node.Destination)
			ready := &markdownInlineImageState{key: s.markdownImageStateKey(state, pool, link), bitmap: bitmap}
			if state.commentReading {
				ready.sourcePath = strings.TrimSpace(state.trustedImages[link])
			}
			state.inlineImages[nodeKey] = ready
			count++
		}
	}
	if count == 0 {
		t.Fatal("ready-image fixture did not contain an image node")
	}
}

func drainMarkdownPreviewTestJobs(t *testing.T, s *app) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.materialsUnit.closeMarkdownPreviewJobs(ctx); err != nil {
		t.Errorf("Markdown preview fixture cleanup: %v", err)
	}
}
