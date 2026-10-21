package desktop

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Paragraph navigation follows Compose's LF paragraph boundaries rather than
// the visual rows produced by text wrapping. Platform command-arrow mappings
// remain separate from ordinary grapheme and visual-line movement.
func (f *nativeRichTextField) handleAdvancedNavigation(event ui.InputEvent) bool {
	return f.handleAdvancedNavigationForPlatform(event, runtime.GOOS)
}

func (f *nativeRichTextField) handleAdvancedNavigationForPlatform(event ui.InputEvent, goos string) bool {
	mods := event.Mods &^ ui.Shift
	extend := event.Mods&ui.Shift != 0
	if goos == "windows" && mods == ui.Ctrl {
		switch event.Key {
		case ui.KeyLeft, ui.KeyRight:
			f.moveWord(event.Key == ui.KeyRight, extend)
			return true
		case ui.KeyBackspace, ui.KeyDelete:
			// Compose maps Ctrl-Shift-Backspace, while Ctrl-Shift-Delete
			// has no deletion command.
			if event.Key != ui.KeyDelete || !extend {
				f.deleteWord(event.Key == ui.KeyDelete)
			}
			return true
		}
	}
	// Compose Desktop 1.12.0's Mac mapping uses Option (Alt) for words.
	// Both Option-Delete and Option-Shift-Delete delete the next word.
	if goos == "darwin" && mods == ui.Alt {
		switch event.Key {
		case ui.KeyLeft, ui.KeyRight:
			f.moveWord(event.Key == ui.KeyRight, extend)
			return true
		case ui.KeyBackspace, ui.KeyDelete:
			f.deleteWord(event.Key == ui.KeyDelete)
			return true
		}
	}
	if goos == "darwin" && mods == ui.Ctrl|ui.Alt && (event.Key == ui.KeyB || event.Key == ui.KeyF) {
		f.moveWord(event.Key == ui.KeyF, extend)
		return true
	}
	if goos == "darwin" && mods == ui.Super {
		switch event.Key {
		case ui.KeyLeft, ui.KeyRight:
			f.moveLineEdge(event.Key == ui.KeyRight, false, extend)
			return true
		case ui.KeyUp, ui.KeyDown:
			f.moveLineEdge(event.Key == ui.KeyDown, true, extend)
			return true
		}
	}
	if goos != "darwin" && mods == ui.Ctrl && (event.Key == ui.KeyUp || event.Key == ui.KeyDown) {
		f.moveParagraph(event.Key == ui.KeyDown, extend)
		return true
	}
	return false
}

func (f *nativeRichTextField) moveParagraph(forward, extend bool) {
	index := f.selection.Range.Start
	if forward {
		index = f.selection.Range.End
	}
	byteIndex := ui.UTF16ByteOffset(f.draftText, index)
	target := 0
	if forward {
		target = nativeRichParagraphEnd(f.draftText, byteIndex)
		if target == byteIndex && byteIndex < len(f.draftText) {
			target = nativeRichParagraphEnd(f.draftText, byteIndex+1)
		}
	} else {
		target = strings.LastIndexByte(f.draftText[:byteIndex], '\n') + 1
		if target == byteIndex && byteIndex > 0 {
			target = strings.LastIndexByte(f.draftText[:byteIndex-1], '\n') + 1
		}
	}
	position := ui.TextCaretPosition{Index: ui.UTF16Len(f.draftText[:target]), Affinity: ui.TextDownstream}
	anchor := f.anchorPosition()
	if !extend {
		anchor = position
	}
	f.setPositions(anchor, position)
	f.hasPreferredX = false
}

func nativeRichParagraphEnd(text string, start int) int {
	if relative := strings.IndexByte(text[start:], '\n'); relative >= 0 {
		return start + relative
	}
	return len(text)
}
