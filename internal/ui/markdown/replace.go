package markdownui

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"strings"
)

// ReplaceCurrent and ReplaceAll search this editor's normalized original text,
// never another document registered with a shared FindController.
func (s *EditorState) ReplaceCurrent(replacement string) error {
	return s.replaceMatches(replacement, false)
}
func (s *EditorState) ReplaceAll(replacement string) error {
	return s.replaceMatches(replacement, true)
}
func (s *EditorState) replaceMatches(replacement string, all bool) error {
	if s.closed {
		return ErrClosed
	}
	if s.options.ReadOnly || s.Mode() == Preview {
		return mdCommandUnavailable
	}
	if s.native.Composing() {
		return ErrComposing
	}
	source := s.native.Buffer().String()
	find := s.reader.find
	hits := foldedMatches(source, find.Query)
	if len(hits) == 0 {
		return nil
	}
	selected := hits
	if !all {
		a, z := s.native.Selection()
		current := 0
		for i, hit := range hits {
			if hit[0] == min(a, z) && hit[1] == max(a, z) {
				current = i
				break
			}
			if hit[0] >= max(a, z) {
				current = i
				break
			}
		}
		selected = hits[current : current+1]
	}
	edits := make([]editor.Edit, 0, len(selected))
	for _, hit := range selected {
		edits = append(edits, editor.Edit{Start: hit[0], End: hit[1], Text: replacement})
	}
	a, z := s.native.Selection()
	normalized := normalizedReplacement(replacement)
	mapAt := func(at int) int {
		delta := 0
		for _, hit := range selected {
			if at < hit[0] {
				break
			}
			if at <= hit[1] {
				return hit[0] + delta + len(normalized)
			}
			delta += len(normalized) - (hit[1] - hit[0])
		}
		return at + delta
	}
	if err := s.native.ApplyEdits(edits, mapAt(a), mapAt(z), s.Snapshot().Version); err != nil {
		return err
	}
	s.notify()
	return nil
}
func normalizedReplacement(text string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
}

func (s *EditorState) renderReplace(c *ui.Context) {
	find := s.reader.find
	if !find.ReplaceOpen {
		return
	}
	ui.Row(c.Key("markdown-replace")).FillWidth().Gap(6).Padding(4).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("replacement"), &find.Replacement).Label("替换为").Placeholder("替换为（原文，含Markdown标记）").Grow(1).MinWidth(40).Height(30)
		disabled := s.options.ReadOnly || s.native.Composing() || s.Mode() == Preview || find.Query == ""
		if ui.Button(c, "替换").Disabled(disabled).Clicked() {
			s.report(s.ReplaceCurrent(find.Replacement))
		}
		if ui.Button(c, "全部替换").Disabled(disabled).Clicked() {
			s.report(s.ReplaceAll(find.Replacement))
		}
	})
}
