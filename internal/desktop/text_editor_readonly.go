package desktop

import (
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

// Embedded sources use the same editor without introducing another document or
// save path. Each owning block retains only its own native reading state.
func renderReadonlyTextEditor(c *ui.Context, p palette, identity, language, source, label string, zoom float32, find *documentFindState, findKey string) ui.Element {
	root := ui.Box(c).FillWidth().MinWidth(0)
	owner := ui.Local(root, "native-source", func() nativeCodePreviewState { return nativeCodePreviewState{} })
	previous := owner.view
	view := owner.bind(identity, language, source)
	if previous != view {
		lang := textEditorLanguage("snippet." + language)
		if lang == nil {
			lang = codeeditor.LanguageNamed(language)
		}
		view.Buffer.SetLanguage(lang)
	}
	view.Label, view.FitContent, view.SoftWrap = label, true, true
	view.Font.Size = textEditorFontSize * max(float32(.5), zoom)
	view.LineHeight = textEditorLineHeight
	view.FindHighlights = nil
	if find != nil {
		for index, hit := range find.hits {
			if hit.block != findKey {
				continue
			}
			start := textEditorProjectedByteOffset(source, utf8.RuneCountInString(source[:min(hit.start, len(source))]))
			end := textEditorProjectedByteOffset(source, utf8.RuneCountInString(source[:min(hit.end, len(source))]))
			view.FindHighlights = append(view.FindHighlights, codeeditor.FindHighlight{Start: start, End: end, Current: index == find.active})
		}
	}
	root.Children(func() {
		TextEditor(c, p, view).FillWidth().MinWidth(0)
		if find != nil && find.shouldReveal(findKey) {
			if hit, ok := find.activeInBlock(findKey); ok {
				start := textEditorProjectedByteOffset(source, utf8.RuneCountInString(source[:min(hit.start, len(source))]))
				top, _, _ := view.LineGeometry(view.Buffer.Pos(start).Line)
				ui.Box(c).Absolute().Top(float32(top)).Left(0).Width(1).Height(20 * zoom).ScrollIntoView()
			}
		}
	})
	return root
}
