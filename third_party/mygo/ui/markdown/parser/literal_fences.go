package parser

import "slices"

// SetWithLiteralFenceLines parses the original source while treating opening
// fences on the specified physical line starts as ordinary text. Offsets are
// UTF-8 bytes in src; source and source maps are never rewritten. This is an
// editing projection, not a different Markdown file format. Nil is Set.
// It reports whether source or policy changed.
func (d *Document) SetWithLiteralFenceLines(src string, starts []int) bool {
	return d.SetWithPolicy(src, 0, starts)
}

// SetWithPolicy retains complete source offsets while optionally making the
// host-validated prefix one opaque leaf. Zero preserves standard Markdown.
func (d *Document) SetWithPolicy(src string, prefixEnd int, starts []int) bool {
	if prefixEnd < 0 || prefixEnd > len(src) || prefixEnd > 0 && prefixEnd < len(src) && src[prefixEnd-1] != '\n' && src[prefixEnd-1] != '\r' || prefixEnd > 0 && prefixEnd < len(src) && src[prefixEnd-1] == '\r' && src[prefixEnd] == '\n' {
		prefixEnd = 0
	}
	lines := slices.Clone(starts)
	slices.Sort(lines)
	lines = slices.Compact(lines)
	lines = slices.DeleteFunc(lines, func(at int) bool {
		return at < 0 || at >= len(src) || at > 0 && src[at-1] != '\n' && src[at-1] != '\r'
	})
	changed := d.src != src || prefixEnd != d.sourcePrefixEnd || !slices.Equal(lines, d.literalFenceLines)
	// Prefix edits cannot re-enter ordinary Markdown through incremental resync.
	prefixChanged := prefixEnd != d.sourcePrefixEnd || prefixEnd > 0 && (len(d.src) < prefixEnd || d.src[:prefixEnd] != src[:prefixEnd])
	if prefixChanged || !slices.Equal(lines, d.literalFenceLines) {
		ext, streaming := d.ext, d.streaming
		fresh := New(ext)
		*d = *fresh
		d.inp.d = d
		d.streaming, d.literalFenceLines = streaming, lines
		d.sourcePrefixEnd = prefixEnd
	}
	d.setSource(src)
	return changed
}

func (d *Document) literalFenceLine(at int) bool {
	_, found := slices.BinarySearch(d.literalFenceLines, at)
	return found
}
