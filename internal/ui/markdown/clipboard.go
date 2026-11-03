package markdownui

import (
	"strings"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	sourceeditor "silverwing-go/internal/editor"
)

// ClipboardContent is an incoming payload. Hosts read platform data; components
// decide how it is inserted into the current Markdown context.
type ClipboardContent struct {
	Text, HTML string
	Files      []string
	ImagePNG   []byte
}
type ImportRequest struct {
	DocumentID, DocumentPath string
	Version                  uint64
	Anchor, Caret            int
	Text                     string
	Files                    []string
	ImagePNG                 []byte
	Pick                     bool
}
type ImportedResource struct {
	Target, Text string
	Image        bool
}
type ImportResult struct{ Resources []ImportedResource }

func (s *EditorState) pasteContent(services ui.Services, plain bool) bool {
	if plain || s.closed || s.options.ReadOnly || s.Mode() != Live {
		return false
	}
	if s.native.SelectionContext() != md.ContextProse {
		return false
	}
	content := ClipboardContent{Text: services.ReadClipboard()}
	if s.options.ReadClipboard != nil {
		incoming, err := s.options.ReadClipboard(s.reader.Context())
		if err != nil {
			s.report(err)
			return false
		}
		content = incoming
		if content.Text == "" {
			content.Text = services.ReadClipboard()
		}
	}
	if len(content.Files) > 0 || len(content.ImagePNG) > 0 {
		if s.options.OnImportResources == nil {
			return false
		}
		s.importResources(ImportRequest{Files: content.Files, ImagePNG: content.ImagePNG})
		return true
	}
	a, z := s.native.Selection()
	if a != z && strings.TrimSpace(content.Text) == content.Text {
		if target, err := ResolveLink(s.document.Path, content.Text); err == nil && target.URL != "" {
			s.report(s.Execute(EditorCommand{Kind: InsertLink, Target: content.Text}))
			return true
		}
	}
	if content.HTML != "" {
		markdown, err := sourceeditor.ClipboardHTMLToMarkdown(content.HTML)
		if err == nil && markdown != "" {
			s.report(s.native.InsertMarkdown(min(a, z), max(a, z), markdown, s.Snapshot().Version))
			s.notify()
			return true
		}
	}
	return false
}

// Completion must be delivered on the UI thread. Late callbacks never write a
// closed, replaced, read-only, or differently-versioned editor.
func (s *EditorState) importResources(request ImportRequest) {
	if s.options.OnImportResources == nil || s.closed || s.options.ReadOnly {
		return
	}
	s.native.MarkUserInteraction()
	request.DocumentID, request.DocumentPath = s.document.ID, s.document.Path
	request.Files = append([]string(nil), request.Files...)
	request.ImagePNG = append([]byte(nil), request.ImagePNG...)
	request.Version = s.Snapshot().Version
	request.Anchor, request.Caret = s.native.Selection()
	point := s.native.CaptureInsertionPoint()
	ownerID, ownerPath := s.document.ID, s.document.Path
	s.options.OnImportResources(s.reader.Context(), request, func(result ImportResult, err error) {
		if err != nil {
			s.report(err)
			return
		}
		if s.closed || s.options.ReadOnly || s.Mode() != Live || s.document.ID != ownerID || s.document.Path != ownerPath || s.Snapshot().Version != request.Version || s.native.Composing() {
			s.report(ErrStale)
			return
		}
		if len(result.Resources) == 0 {
			return
		}
		var parts []string
		for _, resource := range result.Resources {
			if _, err := ResolveLink(s.document.Path, resource.Target); err != nil {
				s.report(err)
				return
			}
			text := resource.Text
			if request.Text != "" {
				text = request.Text
			}
			prefix := ""
			if resource.Image {
				prefix = "!"
			}
			parts = append(parts, prefix+linkSource(text, resource.Target))
		}
		anyImage := false
		for _, resource := range result.Resources {
			anyImage = anyImage || resource.Image
		}
		if anyImage {
			s.report(s.native.InsertImageParagraphsAt(point, parts))
		} else {
			s.report(s.native.InsertMarkdownAt(point, strings.Join(parts, s.native.Buffer().LineEnding())))
		}
		s.native.Focus()
		s.notify()
	})
}
