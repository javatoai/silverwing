package desktop

const workspaceDiffContextLines = 3
const workspaceDiffContextExpansion = 20

type workspaceDiffSourceRange struct{ start, end int }
type workspaceDiffRowsHunk struct{ start, end int }

// Source indices always refer to service comparison rows. A gap carries a
// half-open range in that same coordinate system and never impersonates a line.
func (view *workspaceDiffRowsView) projectRows() {
	count := len(view.sourceRows)
	visible := make([]bool, count)
	if !view.changedOnly && view.contextOnly && !view.singlePane {
		for _, hunk := range view.hunks {
			for source := max(0, hunk.start-workspaceDiffContextLines); source < min(count, hunk.end+workspaceDiffContextLines); source++ {
				visible[source] = true
			}
		}
		for _, expanded := range view.expanded {
			for source := max(0, expanded.start); source < min(count, expanded.end); source++ {
				visible[source] = true
			}
		}
	}
	view.rows = make([]workspaceDiffDisplayRow, 0, count)
	view.sourceToDisplay = make([]int, count)
	for source := 0; source < count; {
		row := view.sourceRows[source]
		if view.changedOnly && !row.changed {
			view.sourceToDisplay[source] = -1
			source++
			continue
		}
		if !view.changedOnly && view.contextOnly && !view.singlePane && !visible[source] {
			start := source
			for source < count && !visible[source] {
				view.sourceToDisplay[source] = -1
				source++
			}
			view.rows = append(view.rows, workspaceDiffDisplayRow{sourceIndex: -1, gapStart: start, gapEnd: source})
			continue
		}
		view.sourceToDisplay[source] = len(view.rows)
		view.rows = append(view.rows, row)
		source++
	}
}

func (view *workspaceDiffRowsView) sourceAnchor() (source int, fraction float32) {
	if len(view.rows) == 0 {
		return 0, 0
	}
	view.syncScroll()
	index := bounded(int(view.newScroll.Y/workspaceDiffRowHeight), 0, len(view.rows)-1)
	row := view.rows[index]
	source = row.sourceIndex
	if source < 0 {
		source = row.gapStart
	}
	return source, max(0, view.newScroll.Y-float32(index*workspaceDiffRowHeight))
}

func (view *workspaceDiffRowsView) restoreSourceAnchor(source int, fraction float32) {
	if len(view.rows) == 0 {
		view.setScrollY(0)
		return
	}
	index := -1
	if source >= 0 && source < len(view.sourceToDisplay) {
		index = view.sourceToDisplay[source]
	}
	if index < 0 {
		for candidate, row := range view.rows {
			if row.sourceIndex >= source || row.sourceIndex < 0 && source >= row.gapStart && source < row.gapEnd {
				index = candidate
				break
			}
		}
	}
	if index < 0 {
		index = len(view.rows) - 1
	}
	view.setScrollY(float32(index*workspaceDiffRowHeight) + fraction)
}

func (view *workspaceDiffRowsView) setProjection(changedOnly, contextOnly bool) {
	contextOnly = contextOnly && !changedOnly && !view.singlePane
	if view.changedOnly == changedOnly && view.contextOnly == contextOnly {
		return
	}
	source, fraction := view.sourceAnchor()
	view.changedOnly, view.contextOnly = changedOnly, contextOnly
	view.projectRows()
	view.restoreSourceAnchor(source, fraction)
}

func (view *workspaceDiffRowsView) expandGap(start, end, direction int) {
	if !view.contextOnly || view.changedOnly || start >= end {
		return
	}
	source, fraction := view.sourceAnchor()
	if direction > 0 {
		end = min(end, start+workspaceDiffContextExpansion)
	} else if direction < 0 {
		start = max(start, end-workspaceDiffContextExpansion)
	}
	view.expanded = append(view.expanded, workspaceDiffSourceRange{start, end})
	view.projectRows()
	view.restoreSourceAnchor(source, fraction)
}

func (view *workspaceDiffRowsView) revealSource(source int) bool {
	if source < 0 || source >= len(view.sourceRows) || view.changedOnly && !view.sourceRows[source].changed {
		return false
	}
	if view.sourceToDisplay[source] < 0 && view.contextOnly {
		view.expanded = append(view.expanded, workspaceDiffSourceRange{
			start: max(0, source-workspaceDiffContextLines), end: min(len(view.sourceRows), source+workspaceDiffContextLines+1),
		})
		view.projectRows()
	}
	index := view.sourceToDisplay[source]
	if index < 0 {
		return false
	}
	view.setScrollY(max(0, float32(index*workspaceDiffRowHeight)-view.viewportHeight/3))
	return true
}

func (view *workspaceDiffRowsView) setScrollY(y float32) {
	view.oldScroll.Y, view.newScroll.Y, view.lastSharedY = y, y, y
}

func (view *workspaceDiffRowsView) goToHunk(index int) bool {
	if index < 0 || index >= len(view.hunks) {
		return false
	}
	return view.revealSource(view.hunks[index].start)
}

func (view *workspaceDiffRowsView) moveHunk(state *workspaceHistoryState, direction int) {
	count := len(view.hunks)
	if count == 0 {
		return
	}
	index := state.DiffHunkIndex + direction
	if !state.DiffHunkSelected {
		index = 0
		if direction < 0 {
			index = count - 1
		}
	}
	index = (index%count + count) % count
	if view.goToHunk(index) {
		state.DiffHunkIndex, state.DiffHunkSelected = index, true
		// A source RichText can leave the virtualized viewport after moving.
		// Keep subsequent keys on its stable, read-only pane instead.
		view.controls.returnFocus = true
	}
}
