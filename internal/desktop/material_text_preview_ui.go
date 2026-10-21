package desktop

import (
	"path/filepath"
	"strings"

	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

// All source previews share one native read-only editor and its selection,
// finder and scroll viewport. Rendered Markdown and valid CSV keep their views.
func (s *app) materialsTextPreviewVisible() bool {
	if s.documentPath == "" || s.documentEditing {
		return false
	}
	switch s.documentKind {
	case "unsupported", "pdf", "image":
		return false
	}
	if s.materialsSearchSourcePreviewActive() {
		return true
	}
	if documentKindForPath(s.documentPath) == "markdown" {
		return s.readingMode() == "SOURCE"
	}
	if strings.EqualFold(filepath.Ext(s.documentPath), ".csv") {
		find := s.ensureDocumentFindState()
		find.preparePreview(s.documentPath, s.documentKind, s.documentContent)
		return find.csvErr != ""
	}
	return true
}

func (s *app) materialsTextPreviewIdentity() string {
	task := s.currentTask()
	mode := "text"
	if documentKindForPath(s.documentPath) == "markdown" {
		mode = s.readingMode()
	}
	return strings.Join([]string{s.snapshot.Home, task.id, task.path, task.materialsPath, s.documentPath, mode}, "\x00")
}

func (s *app) currentMaterialsTextPreview() *codeeditor.State {
	if s.materialsTextEditingVisible() {
		if state := s.currentMaterialTextEditor(); state != nil {
			return state.view
		}
	}
	find := s.documentFind
	if find == nil || find.nativePreview == nil || !s.materialsTextPreviewVisible() {
		return nil
	}
	owner := find.nativePreview
	if owner.identity != s.materialsTextPreviewIdentity() || owner.source != s.documentContent {
		return nil
	}
	return owner.view
}

func (s *app) renderDetectedMaterialsText(c *ui.Context, p palette) *nativeCodePreviewState {
	host := ui.Column(c.Key("materials-detected-text")).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Padding(12)
	owner := ui.Local(host, "preview", func() nativeCodePreviewState { return nativeCodePreviewState{} })
	identity := s.materialsTextPreviewIdentity()
	fresh := owner.view == nil || owner.identity != identity || owner.source != s.documentContent
	view := owner.bind(identity, filepath.Base(s.documentPath), s.documentContent)
	language := workspace.WorkspaceFileLanguageFromPath(s.documentPath)
	verticalID, horizontalID := "text", "code-horizontal"
	fontSize := textEditorFontSize
	markdownSource := documentKindForPath(s.documentPath) == "markdown"
	if markdownSource {
		verticalID, horizontalID = "source-vertical", "source-horizontal"
		fontSize *= s.markdownZoom()
		language = workspace.FileLanguagePlainText
	}
	vertical, horizontal := s.readingScrollState(verticalID), s.readingScrollState(horizontalID)
	tracked := ui.Local(host, "scroll-position", func() codeeditor.ScrollPosition { return codeeditor.ScrollPosition{} })
	position := *tracked
	if fresh {
		position = codeeditor.ScrollPosition{}
	}
	if vertical != nil {
		position.Y = float64(vertical.Y)
	}
	if horizontal != nil {
		position.X = horizontal.X
	}
	if fresh {
		view.Buffer = codeeditor.NewBufferPreservingLineEndings(s.documentContent)
		if !markdownSource {
			view.Buffer.SetLanguage(workspaceFilePreviewLanguage(s.documentPath, language))
		}
		view.NoFolding = true
		view.TabSize = 4
	}
	view.SoftWrap = !markdownSource && documentKindForPath(s.documentPath) != "unsupported" &&
		(language == workspace.FileLanguagePlainText || language == workspace.FileLanguageJSON)
	view.Font = ui.Font{Family: "monospace", Size: fontSize}
	view.LineHeight = textEditorLineHeight
	view.Theme = &codeeditor.Theme{Background: p.surface, Text: p.text, LineNumber: p.muted}
	view.ClipboardWrite = func(text string) bool { return s.copyTextToClipboard(c.Services(), text, "") }
	// Reading state can arrive after the first frame. Restore only external
	// changes; native navigation writes back its new position after drawing.
	if fresh || position != *tracked {
		view.SetScrollPosition(position)
	}
	find := s.ensureDocumentFindState()
	find.nativePreview = owner
	if markdownSource {
		state := s.ensureMarkdownEditor()
		s.bindMaterialsReadingContextMenu(state)
		view.ContextMenu = func(menu *ui.Menu) { state.contextMenu(c, menu) }
	} else {
		view.ContextMenu = nil
	}
	if hit, ok := s.materialsSearchSourcePreviewHit(); ok && s.materialsPicker.sourcePreviewReveal {
		// Search results use source UTF-16 offsets; only the native caret uses
		// LF byte offsets. The source-preserving clipboard stays untouched.
		start := materialsTextPreviewOffset(s.documentContent, hit.Offset)
		view.Select(start, start)
		view.Find(strings.ReplaceAll(hit.Query, "\r\n", "\n"))
		s.materialsPicker.sourcePreviewReveal = false
	}
	host.Children(func() {
		switch strings.ToLower(filepath.Ext(s.documentPath)) {
		case ".json":
			if !find.jsonValid && s.documentContent != "" {
				label(c, "JSON 解析失败，显示原文。", 12, p.amber).Shrink(0)
			}
		case ".csv":
			if find.csvErr != "" {
				label(c, "CSV 解析失败，显示原文。", 12, p.amber).Shrink(0)
			}
		}
		renderNativeCodePreview(c, p, owner, identity, filepath.Base(s.documentPath), s.documentContent, "materials-text-code").Grow(1).DrawOver(func(_ *ui.Painter, _ ui.Rect) {
			position := view.ScrollPosition()
			*tracked = position
			if vertical != nil {
				vertical.Y = float32(position.Y)
				tracked.Y = float64(vertical.Y)
			}
			if horizontal != nil {
				horizontal.X = position.X
			}
		})
	})
	return owner
}

func materialsTextPreviewOffset(source string, utf16Offset int) int {
	end := utf16OffsetToByte(source, utf16Offset)
	if end > 0 && end < len(source) && source[end-1] == '\r' && source[end] == '\n' {
		end--
	}
	return end - strings.Count(source[:end], "\r\n")
}
