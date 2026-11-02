package markdown

import "github.com/egoist/mygo/ui/editor"

func (s *EditorState) sourcePrefixEnd() int {
	return s.sourcePrefixFor(s.source)
}

func (s *EditorState) sourcePrefixFor(source string) int {
	if s.SourceMode || s.opts.Options == nil || s.opts.Options.SourcePrefix == nil {
		return 0
	}
	end := s.opts.Options.SourcePrefix(source)
	if end < 0 || end > len(source) {
		return 0
	}
	return end
}

// ApplyMetadataEdits uses the ordinary transaction/history without revealing
// a distant text caret after a host-owned property control changes metadata.
func (s *EditorState) ApplyMetadataEdits(edits []editor.Edit, anchor, caret int, version uint64) error {
	if err := s.ApplyEdits(edits, anchor, caret, version); err != nil {
		return err
	}
	s.reveal = false
	s.focusRequest = true
	s.metadataViewportVersion = s.snapshot.Version()
	return nil
}
