package desktop

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (state *codexAIState) resetFilePreview() {
	closeMarkdownPreviewState(state.fileMarkdown)
	state.fileMarkdown = nil
	state.fileCodePreview = nativeCodePreviewState{}
}

func (state *codexAIState) markdownFilePreview() *markdownEditorState {
	extension := strings.ToLower(filepath.Ext(state.filePath))
	if extension != ".md" && extension != ".markdown" {
		closeMarkdownPreviewState(state.fileMarkdown)
		state.fileMarkdown = nil
		return nil
	}
	identity := "codex-file:" + state.identity.key() + "\x00" + normalizeIdentityPath(state.filePath)
	if state.fileMarkdown == nil || state.fileMarkdown.taskID != identity || state.fileMarkdown.closed {
		closeMarkdownPreviewState(state.fileMarkdown)
		state.fileMarkdown = newReadonlyMarkdownState(identity, state.fileContent, nil)
		state.fileMarkdown.path = state.filePath
	} else if state.fileMarkdown.doc.Source() != state.fileContent {
		updateReadonlyMarkdownState(state.fileMarkdown, state.fileContent, nil)
	}
	return state.fileMarkdown
}

func (s *app) renderCodexAIFileContent(c *ui.Context, p palette, state *codexAIState) {
	markdown := state.markdownFilePreview()
	if markdown != nil {
		s.renderReadonlyMarkdownReaderContent(c, p, markdown)
		return
	}
	// The source tab retains MyGO's editor navigation, selection, and search.
	// Clipboard copying uses fileContent above, never the editor's buffer text.
	renderNativeCodePreview(c, p, &state.fileCodePreview,
		state.identity.key()+"\x00"+normalizeIdentityPath(state.filePath), state.filePath, state.fileContent,
		"codex-ai-file-content-"+codexAIPathKey(state.filePath)).Grow(1).FillWidth()
}
