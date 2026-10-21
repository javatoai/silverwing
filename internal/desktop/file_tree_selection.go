package desktop

import "github.com/egoist/mygo/ui"

func fileTreeSelection(rows []FileTreeRow, selected map[string]bool, anchor, target string, modifiers ui.Modifiers) (map[string]bool, string) {
	ctrl, shift := modifiers&(ui.Ctrl|ui.Super) != 0, modifiers&ui.Shift != 0
	next := make(map[string]bool)
	if ctrl {
		for key, value := range selected {
			if value {
				next[key] = true
			}
		}
	}
	if shift && anchor != "" {
		from, to := -1, -1
		for index, row := range rows {
			if row.ID == anchor {
				from = index
			}
			if row.ID == target {
				to = index
			}
		}
		if from >= 0 && to >= 0 {
			for _, row := range rows[min(from, to) : max(from, to)+1] {
				next[row.ID] = true
			}
			return next, anchor
		}
	}
	if ctrl && !shift && next[target] {
		delete(next, target)
	} else {
		next[target] = true
	}
	return next, target
}
