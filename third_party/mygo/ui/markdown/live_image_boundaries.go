package markdown

// An image has no editable character positions in live mode. Source mode and
// explicit ApplyEdits remain raw; user input and restored positions are atomic.
func (s *EditorState) protectImageRange(a, z int) (int, int) {
	if !s.opts.InPlaceImages || s.SourceMode {
		return a, z
	}
	for _, image := range s.Images() {
		if a == z && a > image.Start && a < image.End {
			at := image.Start
			if a-image.Start >= image.End-a {
				at = image.End
			}
			return at, at
		}
		if a > image.Start && a < image.End {
			a = image.Start
		}
		if z > image.Start && z < image.End {
			z = image.End
		}
	}
	return a, z
}

func (s *EditorState) normalizeImageSelection() {
	if !s.opts.InPlaceImages || s.SourceMode || s.composing {
		return
	}
	if s.anchor <= s.caret {
		s.anchor, s.caret = s.protectImageRange(s.anchor, s.caret)
	} else {
		s.caret, s.anchor = s.protectImageRange(s.caret, s.anchor)
	}
}
