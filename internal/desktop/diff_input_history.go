package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/tools"
)

// The official editor owns transactions for buttons, keys and Edit-menu
// commands. Accepted source values retain their original line endings; the
// bounded source journal is also used to recover after an oversized edit.
type diffInputHistory struct {
	values      []string
	index       int
	search      diffInputSearch
	view        editor.State
	source      string
	version     uint64
	canRedo     bool
	decorations *diffEditableDecorations
}

func (h *diffInputHistory) sync(value string) {
	if len(h.values) == 0 {
		h.values, h.index = []string{value}, 0
		h.source = value
		h.view.Buffer = editor.NewBufferPreservingLineEndings(value)
		h.view.Buffer.SetMaxSourceBytes(tools.MaxTextBytes)
		return
	}
	if h.source != value {
		h.append(value)
		h.view.Buffer.Set(value)
		h.source, h.version = value, h.view.Buffer.Version()
		h.canRedo = h.view.Buffer.CanRedo()
	}
}

func diffInputLF(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

// pull is called before rendering controls so a native command is published
// before another user action (including swapping sides) can change the source.
func (s *app) pullDiffInput(state *toolPageState, value *string, h *diffInputHistory) {
	if h.view.Buffer == nil || h.version == h.view.Buffer.Version() {
		return
	}
	next := h.view.Buffer.OriginalText()
	if developerToolInputTooLarge(next) {
		s.rejectDiffToolInput(state)
		h.restoreAcceptedBuffer()
		return
	}
	index := -1
	if h.view.Buffer.CanRedo() {
		for i := h.index - 1; i >= 0; i-- {
			if h.values[i] == next {
				index = i
				break
			}
		}
	} else if h.canRedo {
		for i := h.index + 1; i < len(h.values); i++ {
			if h.values[i] == next {
				index = i
				break
			}
		}
	}
	if index >= 0 {
		h.index = index
		next = h.values[index]
	} else {
		h.append(next)
	}
	h.source, h.version, h.canRedo = next, h.view.Buffer.Version(), h.view.Buffer.CanRedo()
	if s.acceptDiffToolInput(state, value, next) {
		s.queueDiff()
	}
}

func (h *diffInputHistory) restoreAcceptedBuffer() {
	h.view.Buffer = editor.NewBufferPreservingLineEndings(h.values[0])
	h.view.Buffer.SetMaxSourceBytes(tools.MaxTextBytes)
	for _, value := range h.values[1:] {
		h.view.Buffer.Set(value)
	}
	for i := len(h.values) - 1; i > h.index; i-- {
		h.view.Buffer.Undo()
	}
	h.version, h.canRedo = h.view.Buffer.Version(), h.view.Buffer.CanRedo()
}

func (h *diffInputHistory) append(value string) {
	if len(h.values) == 0 {
		h.values, h.index = []string{value}, 0
		return
	}
	if len(h.values) > 0 && h.values[h.index] == value {
		return
	}
	clear(h.values[h.index+1:])
	h.values = append(h.values[:h.index+1], value)
	h.index++
	bytes := 0
	for _, text := range h.values {
		bytes += len(text)
	}
	for len(h.values) > 2 && (len(h.values) > 200 || bytes > 8*1024*1024) {
		bytes -= len(h.values[0])
		h.values[0] = ""
		h.values = h.values[1:]
		h.index--
	}
}

func (h *diffInputHistory) canMove(step int) bool {
	if h.view.Buffer == nil {
		return false
	}
	if step < 0 {
		return h.view.Buffer.CanUndo()
	}
	return h.view.Buffer.CanRedo()
}

func (h *diffInputHistory) move(step int) (string, bool) {
	if !h.canMove(step) {
		return "", false
	}
	command := editor.Undo
	if step > 0 {
		command = editor.Redo
	}
	if !h.view.Run(command) {
		return "", false
	}
	return h.view.Buffer.OriginalText(), true
}

func (s *app) moveDiffInputHistory(value *string, history *diffInputHistory, step int) {
	if _, ok := history.move(step); ok {
		s.pullDiffInput(s.ensureToolState(), value, history)
	}
}

func (s *app) bindDiffInputHistory(state *toolPageState, value *string, history *diffInputHistory, element ui.Element) {
	// Leave the editor's native handler intact. Its transactions service
	// both keyboard and system Edit-menu commands. Only find is a tool UI.
	if element.Shortcut(ui.Cmd, ui.KeyF) {
		history.search.toggle()
	}
}
