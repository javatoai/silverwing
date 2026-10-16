package desktop

import (
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/tools"
)

// JSON retains its source projection and source history. Only this boundary
// translates their rune offsets to MyGO's LF-projected UTF-8 byte offsets.
type jsonNativeLines struct{ view *codeeditor.State }

func (lines *jsonNativeLines) Count() int {
	if lines.view == nil || lines.view.Buffer == nil {
		return 0
	}
	return lines.view.Buffer.LineCount()
}
func (lines *jsonNativeLines) Top(line int) float32 {
	if lines.view == nil {
		return 0
	}
	if line >= lines.Count() {
		top, height, _ := lines.view.LineGeometry(max(0, lines.Count()-1))
		return float32(top) + height
	}
	top, _, _ := lines.view.LineGeometry(line)
	return float32(top)
}
func (lines *jsonNativeLines) At(y float32) int {
	if lines.view == nil {
		return 0
	}
	return lines.view.LineAt(float64(y))
}

func (e *jsonToolState) syncNative() {
	if e.native.Buffer == nil {
		e.native.Buffer = codeeditor.NewBufferPreservingLineEndings(e.projection.text)
		e.native.Buffer.SetLanguage(codeeditor.LanguageNamed("JSON"))
		e.native.Buffer.SetMaxSourceBytes(tools.MaxTextBytes)
		e.nativeSource = e.projection.text
	}
	if e.nativeSource != e.projection.text {
		e.native.Buffer.Set(e.projection.text)
		e.nativeSource = e.projection.text
	}
	e.nativeVersion = e.native.Buffer.Version()
	e.lines.view = &e.native
	e.native.Label, e.native.Placeholder = "JSON 源码输入", "粘贴或输入 JSON"
	e.native.LineNumbers = codeeditor.LineNumbersOff
	e.native.NoFolding, e.native.NoAutoClose, e.native.NoScrollPastEnd = true, true, true
	e.native.SoftWrap = true
	position := e.native.ScrollPosition()
	e.scroll.X, e.scroll.Y = position.X, float32(position.Y)
}

func (s *app) pullJSONNativeInput(state *toolPageState) {
	e := &state.jsonEditor
	if e.native.Buffer == nil || e.nativeVersion == e.native.Buffer.Version() || e.native.Composing() {
		return
	}
	e.viewSource = e.native.Buffer.OriginalText()
	start, end := e.nativeSelection()
	if developerToolInputTooLarge(jsonEditorCandidateSource(state, e.viewSource)) {
		e.nativeSource = e.viewSource
		s.rejectJSONToolInput(state)
		e.syncNative()
		return
	}
	e.nativeVersion, e.nativeSource = e.native.Buffer.Version(), e.viewSource
	s.applyJSONEditorChangeAt(state, start, end)
	state.syncJSONEditor()
	e.syncNative()
	if e.pendingSelection {
		e.selectNative(e.selectionStart, e.selectionEnd)
	}
}

func (e *jsonToolState) nativeSelection() (int, int) {
	selection := e.native.Selection()
	source := e.native.Buffer.OriginalText()
	return textEditorSourceRuneOffset(source, selection.Anchor), textEditorSourceRuneOffset(source, selection.Head)
}

func (e *jsonToolState) selectNative(start, end int) {
	e.native.Select(textEditorProjectedByteOffset(e.projection.text, start), textEditorProjectedByteOffset(e.projection.text, end))
}

func textEditorSourceRuneOffset(source string, projectedByte int) int {
	bytes, runes := 0, 0
	for index := 0; index < len(source) && bytes < projectedByte; {
		if source[index] == '\r' && index+1 < len(source) && source[index+1] == '\n' {
			index, bytes, runes = index+2, bytes+1, runes+2
			continue
		}
		_, width := utf8.DecodeRuneInString(source[index:])
		index, bytes, runes = index+width, bytes+width, runes+1
	}
	return runes
}

func textEditorProjectedByteOffset(source string, sourceRune int) int {
	bytes, runes := 0, 0
	for index := 0; index < len(source) && runes < sourceRune; {
		if source[index] == '\r' && index+1 < len(source) && source[index+1] == '\n' {
			index, bytes, runes = index+2, bytes+1, runes+2
			continue
		}
		_, width := utf8.DecodeRuneInString(source[index:])
		index, bytes, runes = index+width, bytes+width, runes+1
	}
	return bytes
}

func (s *app) jsonNativeInput(c *ui.Context, state *toolPageState) func(ui.InputEvent) bool {
	services := c.Services()
	return func(event ui.InputEvent) bool {
		s.pullJSONNativeInput(state)
		command := ""
		if event.Kind == ui.InputCommand {
			command = event.Text
		}
		if event.Kind == ui.InputKeyDown {
			switch {
			case event.Mods == ui.Cmd:
				switch event.Key {
				case ui.KeyA:
					command = "selectAll"
				case ui.KeyC:
					command = "copy"
				case ui.KeyX:
					command = "cut"
				case ui.KeyV:
					command = "paste"
				case ui.KeyZ:
					command = "undo"
				case ui.KeyY:
					command = "redo"
				case ui.KeyF:
					state.jsonEditor.finding, state.jsonEditor.findChanged = true, true
					services.Invalidate()
					return true
				}
			case event.Mods == ui.Cmd|ui.Shift && event.Key == ui.KeyZ:
				command = "redo"
			}
		}
		return s.runJSONNativeCommand(state, services, command)
	}
}

func (s *app) runJSONNativeCommand(state *toolPageState, services ui.Services, command string) bool {
	e := &state.jsonEditor
	start, end := e.nativeSelection()
	switch command {
	case "selectAll":
		e.native.Run(codeeditor.SelectAll)
	case "undo", "redo":
		s.moveJSONEditorHistory(state, command == "redo")
	case "copy", "cut":
		if start != end && s.copyTextToClipboard(services, jsonEditorSelectedSource(state, start, end), "") && command == "cut" {
			s.replaceJSONEditorSelection(state, "", min(start, end), max(start, end))
		}
	case "paste", "pasteAndMatchStyle":
		text := services.ReadClipboard()
		if !s.rejectJSONToolPaste(state, text) {
			s.applyJSONEditorPaste(state, text, min(start, end), max(start, end))
		}
	default:
		return false
	}
	state.syncJSONEditor()
	e.syncNative()
	if e.pendingSelection {
		e.selectNative(e.selectionStart, e.selectionEnd)
	}
	services.Invalidate()
	return true
}
