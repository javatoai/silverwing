package parser

// LinkLabelRange returns the source bytes inside an explicit link's first
// pair of brackets. Literal inline children own their complete source ranges,
// so brackets inside code, math, HTML and images cannot end the label.
func (d *Document) LinkLabelRange(block, index int) (start, end int, ok bool) {
	if block < 0 || block >= d.Len() {
		return 0, 0, false
	}
	inlines := d.Inlines(block)
	if index < 0 || index >= len(inlines) {
		return 0, 0, false
	}
	in := inlines[index]
	if in.Kind != Link || in.Auto || in.Pending || in.Start < 0 || in.Start >= in.End || in.End > len(d.src) || d.src[in.Start] != '[' {
		return 0, 0, false
	}
	literal := make(map[int]int)
	for _, child := range inlines[index+1 : in.Next] {
		if child.Kind == Code || child.Kind == Math || child.Kind == HTML || child.Kind == Image {
			literal[child.Start] = max(literal[child.Start], child.End)
		}
	}
	depth := 0
	for at := in.Start; at < in.End; at++ {
		if stop, found := literal[at]; found && stop > at {
			at = stop - 1
			continue
		}
		switch d.src[at] {
		case '\\':
			at++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return in.Start + 1, at, true
			}
		}
	}
	return 0, 0, false
}

// LinkLabelSource returns an explicit link's label from its owning source.
// The whole document is parsed so reference definitions retain their scope.
func LinkLabelSource(source string, link Inline) (string, bool) {
	if link.Kind != Link || link.Auto || link.Start < 0 || link.Start >= link.End || link.End > len(source) || source[link.Start] != '[' {
		return "", false
	}
	d := Parse(source)
	for block := 1; block < d.Len(); block++ {
		for index, in := range d.Inlines(block) {
			if in.Kind == Link && in.Start == link.Start && in.End == link.End {
				if start, end, ok := d.LinkLabelRange(block, index); ok {
					return source[start:end], true
				}
			}
		}
	}
	return "", false
}
