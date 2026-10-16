package desktop

import "github.com/egoist/mygo/ui"

func (f *nativeRichTextField) handleInput(c *ui.Context, event ui.InputEvent) bool {
	return f.handleInputWithClipboard(c, event, nativeRichTextClipboard{
		read: func() string {
			if c == nil {
				return ""
			}
			return c.ReadClipboard()
		},
		write: func(value string) {
			if c != nil {
				c.WriteClipboard(value)
			}
		},
	})
}

type nativeRichTextClipboard struct {
	read  func() string
	write func(string)
}

func (f *nativeRichTextField) handleInputWithClipboard(c *ui.Context, event ui.InputEvent, clipboard nativeRichTextClipboard) bool {
	if event.Kind == ui.InputCommand || event.Kind == ui.InputKeyDown {
		if f.onCommand != nil && f.onCommand(c, event) {
			return true
		}
	}
	switch event.Kind {
	case ui.InputPointerDown:
		if event.Button != 0 || f.layout == nil {
			return false
		}
		if f.onPointerDown != nil {
			f.onPointerDown()
		}
		position := f.layout.PositionAt(ui.Point{X: event.X - nativeRichTextInsetX, Y: event.Y - nativeRichTextInsetY})
		anchor := position
		if event.Mods&ui.Shift != 0 {
			anchor = f.anchorPosition()
		}
		f.pointerDown = true
		f.setPositions(anchor, position)
		return true
	case ui.InputPointerMove:
		if !f.pointerDown || event.Button != 0 || f.layout == nil {
			return false
		}
		position := f.layout.PositionAt(ui.Point{X: event.X - nativeRichTextInsetX, Y: event.Y - nativeRichTextInsetY})
		f.setPositions(f.anchorPosition(), position)
		return true
	case ui.InputPointerUp:
		if event.Button == 0 && f.pointerDown {
			f.pointerDown = false
			return true
		}
		return false
	case ui.InputCommand:
		return f.handleCommand(event.Text, clipboard)
	case ui.InputKeyDown:
		return f.handleKey(event, clipboard)
	}
	return false
}

func (f *nativeRichTextField) handleCommand(command string, clipboard nativeRichTextClipboard) bool {
	switch command {
	case "selectAll":
		f.setSelection(ui.TextInputSelection{Range: ui.TextInputRange{Start: 0, End: ui.UTF16Len(f.draftText)}})
		return true
	case "copy":
		return f.copySelection(clipboard)
	case "cut":
		return f.cutSelection(clipboard)
	case "paste":
		if clipboard.read == nil {
			return false
		}
		f.replaceSelection(clipboard.read())
		return true
	case "delete":
		if f.selection.Range.Start == f.selection.Range.End {
			return false
		}
		f.replaceSelection("")
		return true
	default:
		return false
	}
}

func (f *nativeRichTextField) handleKey(event ui.InputEvent, clipboard nativeRichTextClipboard) bool {
	if event.Key == ui.KeyEscape && f.composing {
		f.cancelComposition()
		return true
	}
	if f.composing {
		// The native input method owns the remaining keys while a preedit is
		// active, including candidate navigation and deletion.
		return false
	}
	if f.handleAdvancedNavigation(event) {
		return true
	}
	if event.Mods&ui.Cmd != 0 {
		switch event.Key {
		case ui.KeyA:
			f.setSelection(ui.TextInputSelection{Range: ui.TextInputRange{Start: 0, End: ui.UTF16Len(f.draftText)}})
			return true
		case ui.KeyC:
			return f.copySelection(clipboard)
		case ui.KeyX:
			return f.cutSelection(clipboard)
		case ui.KeyV:
			if clipboard.read == nil {
				return false
			}
			f.replaceSelection(clipboard.read())
			return true
		}
	}
	shift := event.Mods&ui.Shift != 0
	switch event.Key {
	case ui.KeyLeft, ui.KeyRight:
		direction := -1
		if event.Key == ui.KeyRight {
			direction = 1
		}
		f.moveHorizontal(direction, shift)
		return true
	case ui.KeyUp, ui.KeyDown:
		direction := -1
		if event.Key == ui.KeyDown {
			direction = 1
		}
		f.moveVertical(direction, shift)
		return true
	case ui.KeyHome, ui.KeyEnd:
		f.moveLineEdge(event.Key == ui.KeyEnd, event.Mods&ui.Cmd != 0, shift)
		return true
	case ui.KeyBackspace, ui.KeyDelete:
		f.deleteByKey(event.Key == ui.KeyDelete)
		return true
	case ui.KeyEnter:
		f.replaceSelection("\n")
		return true
	default:
		return false
	}
}

func (f *nativeRichTextField) copySelection(clipboard nativeRichTextClipboard) bool {
	if clipboard.write == nil || f.selection.Range.Start == f.selection.Range.End {
		return false
	}
	text, _ := f.TextForRange(f.selection.Range)
	clipboard.write(text)
	return true
}

func (f *nativeRichTextField) cutSelection(clipboard nativeRichTextClipboard) bool {
	if f.selection.Range.Start == f.selection.Range.End {
		return false
	}
	if clipboard.write != nil {
		f.copySelection(clipboard)
	}
	f.replaceSelection("")
	return true
}

func (f *nativeRichTextField) replaceSelection(value string) {
	r := normalizeNativeRichTextRange(f.draftText, f.selection.Range)
	f.ReplaceText(&r, value)
}

func (f *nativeRichTextField) deleteByKey(forward bool) {
	r := f.selection.Range
	if r.Start == r.End {
		if forward {
			r.End = f.layout.NextBoundary(r.End)
		} else {
			r.Start = f.layout.PreviousBoundary(r.Start)
		}
	}
	if r.Start != r.End {
		f.ReplaceText(&r, "")
	}
}

func (f *nativeRichTextField) moveHorizontal(direction int, extend bool) {
	caret := f.caretPosition()
	if !extend && f.selection.Range.Start != f.selection.Range.End {
		index := f.selection.Range.End
		if direction < 0 {
			index = f.selection.Range.Start
		}
		position := ui.TextCaretPosition{Index: index, Affinity: ui.TextDownstream}
		f.setPositions(position, position)
		f.hasPreferredX = false
		return
	}
	anchor := f.anchorPosition()
	if !extend {
		anchor = caret
	}
	next := f.layout.MoveCaret(caret, direction)
	f.setPositions(anchor, next)
	f.hasPreferredX = false
}

func (f *nativeRichTextField) moveVertical(direction int, extend bool) {
	caret := f.caretPosition()
	anchor := f.anchorPosition()
	if !extend {
		anchor = caret
	}
	box := f.layout.CaretAt(caret)
	if !f.hasPreferredX {
		f.preferredX = box.X
		f.hasPreferredX = true
	}
	position := f.layout.PositionAt(ui.Point{X: f.preferredX, Y: box.Y + box.H/2 + float32(direction)*max(box.H, float32(1))})
	f.setPositions(anchor, position)
}

func (f *nativeRichTextField) moveLineEdge(end, document, extend bool) {
	caret := f.caretPosition()
	var position ui.TextCaretPosition
	if document {
		index := 0
		if end {
			index = ui.UTF16Len(f.draftText)
		}
		position = ui.TextCaretPosition{Index: index, Affinity: ui.TextDownstream}
	} else {
		box := f.layout.CaretAt(caret)
		x := float32(0)
		if end {
			width, _ := f.layout.Size()
			x = width + 2
		}
		position = f.layout.PositionAt(ui.Point{X: x, Y: box.Y + box.H/2})
	}
	anchor := f.anchorPosition()
	if !extend {
		anchor = position
	}
	f.setPositions(anchor, position)
	f.hasPreferredX = false
}

func (f *nativeRichTextField) setPositions(anchor, caret ui.TextCaretPosition) {
	anchor.Index = max(0, min(anchor.Index, ui.UTF16Len(f.draftText)))
	caret.Index = max(0, min(caret.Index, ui.UTF16Len(f.draftText)))
	f.selection = ui.TextInputSelection{
		Range:    ui.TextInputRange{Start: min(anchor.Index, caret.Index), End: max(anchor.Index, caret.Index)},
		Reversed: caret.Index < anchor.Index,
	}
	f.anchorAffinity, f.caretAffinity = anchor.Affinity, caret.Affinity
	f.notifySelection()
}

func (f *nativeRichTextField) anchorPosition() ui.TextCaretPosition {
	if f.selection.Reversed {
		return ui.TextCaretPosition{Index: f.selection.Range.End, Affinity: f.anchorAffinity}
	}
	return ui.TextCaretPosition{Index: f.selection.Range.Start, Affinity: f.anchorAffinity}
}

func (f *nativeRichTextField) caretPosition() ui.TextCaretPosition {
	index := f.selection.Range.End
	if f.selection.Reversed {
		index = f.selection.Range.Start
	}
	return ui.TextCaretPosition{Index: index, Affinity: f.caretAffinity}
}

func (f *nativeRichTextField) notifySelection() {
	if f.onSelection != nil {
		f.onSelection(f.selection.Range.Start, f.selection.Range.End)
	}
}
