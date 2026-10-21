package desktop

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Search offsets count source runes. Selection maps them to the official
// editor's byte offsets, accounting for its CRLF projection. Matching the
// source itself keeps case folding out of the displayed/copied text.
type diffInputSearch struct {
	open, focusPending, returnFocus bool
	query, matchedQuery, source     string
	matches                         [][2]int
	movePending                     int
}

func (search *diffInputSearch) toggle() {
	search.open = !search.open
	search.focusPending = search.open
	search.returnFocus = !search.open
	search.movePending = 0
}

func (search *diffInputSearch) refresh(source string) {
	if search.source == source && search.matchedQuery == search.query {
		return
	}
	search.source, search.matchedQuery = source, search.query
	search.matches = nil
	if search.query == "" {
		return
	}
	pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(search.query))
	if err != nil {
		return
	}
	byteCursor, runeCursor := 0, 0
	for _, match := range pattern.FindAllStringIndex(source, -1) {
		runeCursor += utf8.RuneCountInString(source[byteCursor:match[0]])
		start := runeCursor
		runeCursor += utf8.RuneCountInString(source[match[0]:match[1]])
		search.matches = append(search.matches, [2]int{start, runeCursor})
		byteCursor = match[1]
	}
}

func (search *diffInputSearch) locate(source string, start, end, direction int) (int, int, bool) {
	search.refresh(source)
	if len(search.matches) == 0 {
		return 0, 0, false
	}
	index := 0
	if direction < 0 {
		index = len(search.matches) - 1
		for candidate := len(search.matches) - 1; candidate >= 0; candidate-- {
			if search.matches[candidate][0] < start {
				index = candidate
				break
			}
		}
	} else {
		for candidate, match := range search.matches {
			if match[0] >= end {
				index = candidate
				break
			}
		}
	}
	match := search.matches[index]
	return match[0], match[1], true
}

func (search *diffInputSearch) applySelection(input ui.Element, view *editor.State, source string, state *toolPageState) {
	if search.returnFocus {
		input.Focus()
		search.returnFocus = false
	}
	if search.movePending == 0 {
		return
	}
	selection := view.Selection()
	start := diffInputSourceRuneOffset(source, min(selection.Anchor, selection.Head))
	end := diffInputSourceRuneOffset(source, max(selection.Anchor, selection.Head))
	if start, end, ok := search.locate(source, start, end, search.movePending); ok {
		view.Select(diffInputNativeByteOffset(source, start), diffInputNativeByteOffset(source, end))
		input.Focus()
		// A find action reveals unchanged content without editing its source.
		state.diffCollapseEqual = false
	}
	search.movePending = 0
}

func diffInputNativeByteOffset(source string, runeOffset int) int {
	byteOffset := len(source)
	count := 0
	for offset := range source {
		if count == runeOffset {
			byteOffset = offset
			break
		}
		count++
	}
	return len(strings.ReplaceAll(source[:byteOffset], "\r\n", "\n"))
}

func diffInputSourceRuneOffset(source string, nativeOffset int) int {
	native, runes := 0, 0
	for offset, r := range source {
		if native >= nativeOffset {
			return runes
		}
		runes++
		if r == '\r' && offset+1 < len(source) && source[offset+1] == '\n' {
			continue
		}
		native += utf8.RuneLen(r)
	}
	return runes
}

func (s *app) renderDiffInputSearch(c *ui.Context, p palette, history *diffInputHistory, source, key string) {
	search := &history.search
	if !search.open {
		return
	}
	search.refresh(source)
	ui.Row(c).Height(30).Shrink(0).Padding(2, 6).Gap(4).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key(key+"-search"), &search.query).Grow(1).MinWidth(0).FontSize(11).Label("查找文本").Placeholder("查找文本")
		input.OnChange(func() { search.refresh(source) })
		input.HandleInput(func(event ui.InputEvent) bool {
			if event.Kind != ui.InputKeyDown {
				return false
			}
			if event.Key == ui.KeyEnter && (event.Mods == 0 || event.Mods == ui.Shift) {
				search.movePending = 1
				if event.Mods == ui.Shift {
					search.movePending = -1
				}
				return true
			}
			if event.Key == ui.KeyEscape && event.Mods == 0 {
				search.toggle()
				return true
			}
			return false
		})
		if search.focusPending {
			input.Focus()
			search.focusPending = false
		}
		label(c, fmt.Sprintf("%d 处", len(search.matches)), 10, p.muted).NoWrap()
		if toolButtonWithEnabled(c, key+"-find-prev", "上一个", p, false, len(search.matches) > 0) {
			search.movePending = -1
		}
		if toolButtonWithEnabled(c, key+"-find-next", "下一个", p, false, len(search.matches) > 0) {
			search.movePending = 1
		}
	})
}
