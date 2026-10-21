package editor

import "github.com/egoist/mygo/internal/diff"

// SetPreparedHunks seeds a full-text viewer with changes already computed by
// its caller. Ranges are zero-based and end-exclusive. Buffers, text selection,
// syntax highlighting and expandable context remain available. Invalid ranges
// or unequal context return false without changing the current model.
// Replacing or editing either buffer invalidates the seed.
func (d *DiffState) SetPreparedHunks(hunks []DiffHunk) bool {
	if d.Old == nil || d.New == nil {
		return false
	}
	oldText, oldVer := d.Old.snapshot()
	newText, newVer := d.New.snapshot()
	nOld, nNew := oldText.LineCount(), newText.LineCount()
	if oldText.Len() == 0 {
		nOld = 0
	}
	if newText.Len() == 0 {
		nNew = 0
	}
	a, b := 0, 0
	contextEqual := func(toA, toB int) bool {
		if toA-a != toB-b {
			return false
		}
		for a < toA {
			if oldText.Line(a) != newText.Line(b) {
				return false
			}
			a++
			b++
		}
		return true
	}
	seed := make([]diff.Hunk, 0, len(hunks))
	for _, h := range hunks {
		if h.OldStart < a || h.NewStart < b || h.OldEnd < h.OldStart || h.NewEnd < h.NewStart ||
			h.OldEnd > nOld || h.NewEnd > nNew || h.OldStart == h.OldEnd && h.NewStart == h.NewEnd ||
			!contextEqual(h.OldStart, h.NewStart) {
			return false
		}
		seed = append(seed, diff.Hunk{A0: h.OldStart, A1: h.OldEnd, B0: h.NewStart, B1: h.NewEnd})
		a, b = h.OldEnd, h.NewEnd
	}
	if !contextEqual(nOld, nNew) || d.Old.Version() != oldVer || d.New.Version() != newVer {
		return false
	}
	d.model.set(d.Old, d.New)
	d.model.oldText, d.model.newText = oldText, newText
	d.model.oldVer, d.model.newVer = oldVer, newVer
	d.model.hunks, d.model.words = seed, nil
	d.model.seededHunks = true
	d.model.version++
	d.partial = nil
	d.prepared = diffPrepared{}
	return true
}
