package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	fileTreeRowHeight = float32(24)
	fileTreeIndent    = float32(14)
	fileTreeFontSize  = float32(13)
	fileTreeIconSize  = float32(14)
	fileTreeWidth     = float32(248)
	fileTreeTipDelay  = 500 * time.Millisecond
)

// FileTreeRow is a presentation row. Hosts provide their existing order, with
// parents before children; no path is inspected or read by FileTree.
type FileTreeRow struct {
	ID, Name, Path string
	Depth          int
	Directory      bool
}

type FileTreeInputKind uint8

const (
	FileTreeActivate FileTreeInputKind = iota
	FileTreeSelect
	FileTreeToggle
	FileTreeContext
	FileTreeDrop
)

// FileTreeInput describes one gesture. The host decides whether it selects,
// opens, pins, copies or imports; one gesture produces one input.
type FileTreeInput struct {
	Kind       FileTreeInputKind
	Row        FileTreeRow
	Modifiers  ui.Modifiers
	ClickCount int
	Files      []string
}

// FileTreeState contains transient view state only. Expansion, selection and
// the opened document are independent values supplied by the host.
type FileTreeState struct {
	rootCollapsed bool
	rowHandles    map[string]*ui.Handle
	hoverID       string
	hoverSince    time.Time
	suppressID    string
	scrollY       float32
	scroll        ui.ScrollState
}

type FileTreeProps struct {
	Key             string
	Root            FileTreeRow
	Rows            []FileTreeRow
	Expanded        map[string]bool
	Selected        map[string]bool
	Opened          string
	State           *FileTreeState
	Scroll          *ui.ScrollState
	Empty           string
	AcceptFiles     bool
	SuppressTooltip bool
	OnInput         func(FileTreeInput)
	ContextMenu     func(FileTreeRow, *ui.Menu)
	Shortcuts       func(ui.Element)
	RootActions     func()
	RootActionWidth float32
	RowActions      func(FileTreeRow)
	RowActionWidth  float32
}

// FileTree is the shared native sidebar for materials and local Skills. It
// draws supplied rows and reports input; all filesystem and business actions
// stay in its host.
func FileTree(c *ui.Context, p palette, props FileTreeProps) ui.Element {
	state := props.State
	if state == nil {
		panic("FileTree requires its own view state")
	}
	if state.rowHandles == nil {
		state.rowHandles = make(map[string]*ui.Handle)
	}
	scroll := props.Scroll
	if scroll == nil {
		scroll = &state.scroll
	}
	if scroll.Y != state.scrollY {
		state.hoverID, state.hoverSince = "", time.Time{}
		state.scrollY = scroll.Y
	}
	rows := fileTreeVisibleRows(props.Rows, props.Expanded)
	ancestors := fileTreeOpenedAncestors(props.Rows, props.Opened)
	container := ui.Column(c.Key(props.Key)).FillWidth().Grow(1).MinHeight(0).MinWidth(0).Background(p.side).Font("system-ui")
	showRootActions := container.Hovered() || container.FocusWithin()
	container.Children(func() {
		root := ui.Row(c.Key(props.Key+"-root")).Height(fileTreeRowHeight).Shrink(0).FillWidth().Padding(0, 6).Gap(2).AlignItems(ui.Center)
		var rootButton ui.Element
		root.Children(func() {
			b := ui.ButtonBase(c.Key(props.Key + "-root-toggle")).Row().Grow(1).MinWidth(0).Height(fileTreeRowHeight).Gap(5).AlignItems(ui.Center).Label(props.Root.Name)
			rootButton = b
			b.Children(func() {
				arrow, folder := "chevron", "folder-open"
				if state.rootCollapsed {
					arrow, folder = "chevron-right", "folder"
				}
				icon(c, arrow, 12, p.muted)
				icon(c, folder, fileTreeIconSize, p.muted)
				label(c, props.Root.Name, fileTreeFontSize, p.text).FontWeight(600).SingleLine().Grow(1).MinWidth(0)
			})
			if b.Clicked() {
				state.rootCollapsed = !state.rootCollapsed
			}
			if props.RootActions != nil {
				ui.Row(c).Width(props.RootActionWidth).Shrink(0).Height(fileTreeRowHeight).Gap(0).AlignItems(ui.Center).Children(func() {
					if showRootActions {
						props.RootActions()
					}
				})
			}
		})
		if props.ContextMenu != nil {
			root.ContextMenu(func(m *ui.Menu) {
				state.suppressID = state.hoverID
				props.ContextMenu(props.Root, m)
			})
		}
		if props.AcceptFiles {
			if root.FileDragOver() {
				root.Background(p.blue.Alpha(.08))
			}
			if files := root.DroppedFiles(); len(files) > 0 {
				fileTreeEmit(props, FileTreeInput{Kind: FileTreeDrop, Row: props.Root, Files: files})
			}
		}
		fileTreeTooltip(c, p, rootButton, props, state, props.Root)
		tree := ui.Scroll(c.Key(props.Key+"-rows")).TrackScroll(scroll).Grow(1).MinHeight(0).FillWidth().Padding(2, 4, 8, 4).Gap(0).Focusable().Role(ui.RoleTree).Label(props.Root.Name + "文件树")
		if props.Shortcuts != nil {
			props.Shortcuts(tree)
		}
		if props.ContextMenu != nil {
			tree.ContextMenu(func(m *ui.Menu) {
				state.suppressID = state.hoverID
				props.ContextMenu(props.Root, m)
			})
		}
		if props.AcceptFiles && tree.FileDragOver() {
			state.suppressID = state.hoverID
			tree.Background(p.blue.Alpha(.06))
		}
		focused := tree.Focused()
		for _, handle := range state.rowHandles {
			if handle.Focused(c) {
				focused = true
				break
			}
		}
		hovered := rootButton.Hovered()
		tree.Children(func() {
			if state.rootCollapsed {
				return
			}
			for index, row := range rows {
				if fileTreeRow(c, p, props, state, focused, rows, index, row, ancestors) {
					hovered = true
				}
			}
			if len(rows) == 0 && props.Empty != "" {
				label(c, props.Empty, 12, p.muted).FillWidth().Padding(12, 10).Wrap()
			}
		})
		if !hovered {
			state.hoverID, state.suppressID = "", ""
		}
		if props.AcceptFiles {
			if files := tree.DroppedFiles(); len(files) > 0 {
				fileTreeEmit(props, FileTreeInput{Kind: FileTreeDrop, Row: props.Root, Files: files})
			}
		}
	})
	return container
}

func fileTreeRow(c *ui.Context, p palette, props FileTreeProps, state *FileTreeState, focused bool, rows []FileTreeRow, index int, row FileTreeRow, ancestors map[string]bool) bool {
	if state.rowHandles[row.ID] == nil {
		state.rowHandles[row.ID] = &ui.Handle{}
	}
	b := ui.ButtonBase(c.Key(props.Key+"-row-"+row.ID)).Bind(state.rowHandles[row.ID]).Row().FillWidth().MinWidth(0).Height(fileTreeRowHeight).Shrink(0).
		Padding(0, 5).Gap(5).Radius(3).AlignItems(ui.Center).Label(row.Name).Description(row.Path).Role(ui.RoleTreeItem)
	if props.Shortcuts != nil {
		props.Shortcuts(b)
	}
	selected, opened := props.Selected[row.ID], props.Opened == row.ID
	b.Selected(selected).Level(row.Depth + 1)
	if row.Directory {
		b.Expanded(props.Expanded[row.ID])
	}
	fg := p.text
	if selected {
		fill := p.blue.Alpha(.10)
		if focused {
			fill = p.blue.Alpha(.16)
		}
		b.Background(fill)
	} else if b.Hovered() {
		b.Background(p.blue.Alpha(.045))
	}
	if opened {
		fg = p.blue
	}
	if props.AcceptFiles && b.FileDragOver() {
		state.suppressID = row.ID
		b.Background(p.blue.Alpha(.14))
	}
	if row.Depth > 0 {
		b.Draw(func(pt *ui.Painter, r ui.Rect) {
			parents := fileTreeRowParents(rows, index)
			for depth, parent := range parents {
				col := p.line
				if ancestors[parent.ID] {
					col = p.blue.Alpha(.30)
				}
				x := r.X + 16 + float32(depth)*fileTreeIndent
				pt.Line(x, r.Y, x, r.Y+r.H, 1, col)
			}
		})
	}
	b.Children(func() {
		ui.Box(c).Width(float32(row.Depth) * fileTreeIndent).Shrink(0)
		if row.Directory {
			arrow := ui.ButtonBase(c.Key(props.Key+"-disclosure-"+row.ID)).Size(12, fileTreeRowHeight).Center().KeepFocus().Label("展开或收起 " + row.Name)
			arrow.Children(func() {
				name := "chevron-right"
				if props.Expanded[row.ID] {
					name = "chevron"
				}
				icon(c, name, 12, p.muted)
			})
			if arrow.Clicked() {
				state.suppressID = row.ID
				fileTreeEmit(props, FileTreeInput{Kind: FileTreeToggle, Row: row, Modifiers: arrow.ClickModifiers(), ClickCount: 1})
			}
		} else {
			ui.Box(c).Width(12).Shrink(0)
		}
		image, tint := fileTreeIcon(row, props.Expanded[row.ID], p)
		icon(c, image, fileTreeIconSize, tint)
		name := label(c, row.Name, fileTreeFontSize, fg).SingleLine().Grow(1).MinWidth(0)
		if ancestors[row.ID] {
			name.FontWeight(500)
		}
		if props.RowActions != nil && !row.Directory {
			ui.Row(c).Width(props.RowActionWidth).Height(fileTreeRowHeight).Shrink(0).AlignItems(ui.Center).Children(func() {
				if b.Hovered() || b.FocusWithin() {
					props.RowActions(row)
				}
			})
		}
	})
	if props.ContextMenu != nil {
		b.ContextMenu(func(m *ui.Menu) {
			state.suppressID = row.ID
			fileTreeEmit(props, FileTreeInput{Kind: FileTreeContext, Row: row})
			props.ContextMenu(row, m)
		})
	}
	if b.DoubleClicked() {
		state.suppressID = row.ID
		fileTreeEmit(props, FileTreeInput{Kind: FileTreeActivate, Row: row, Modifiers: b.ClickModifiers(), ClickCount: 2})
	} else if b.Clicked() {
		state.suppressID = row.ID
		fileTreeEmit(props, FileTreeInput{Kind: FileTreeActivate, Row: row, Modifiers: b.ClickModifiers(), ClickCount: 1})
	}
	if props.AcceptFiles {
		if files := b.DroppedFiles(); len(files) > 0 {
			fileTreeEmit(props, FileTreeInput{Kind: FileTreeDrop, Row: row, Files: files})
		}
	}
	fileTreeRowKeys(b, props, state, rows, index)
	fileTreeTooltip(c, p, b, props, state, row)
	return b.Hovered()
}

func fileTreeEmit(props FileTreeProps, input FileTreeInput) {
	if props.OnInput != nil {
		props.OnInput(input)
	}
}

func fileTreeVisibleRows(rows []FileTreeRow, expanded map[string]bool) []FileTreeRow {
	visible := make([]FileTreeRow, 0, len(rows))
	hiddenBelow := -1
	for _, row := range rows {
		if hiddenBelow >= 0 && row.Depth > hiddenBelow {
			continue
		}
		hiddenBelow = -1
		visible = append(visible, row)
		if row.Directory && !expanded[row.ID] {
			hiddenBelow = row.Depth
		}
	}
	return visible
}

func fileTreeRowParents(rows []FileTreeRow, index int) []FileTreeRow {
	depth := rows[index].Depth
	parents := make([]FileTreeRow, depth)
	for previous := index - 1; previous >= 0 && depth > 0; previous-- {
		if rows[previous].Depth < depth {
			depth = rows[previous].Depth
			parents[depth] = rows[previous]
		}
	}
	return parents
}

func fileTreeOpenedAncestors(rows []FileTreeRow, opened string) map[string]bool {
	ancestors := make(map[string]bool)
	for index, row := range rows {
		if row.ID == opened {
			for _, parent := range fileTreeRowParents(rows, index) {
				ancestors[parent.ID] = true
			}
			break
		}
	}
	return ancestors
}

func fileTreeRowKeys(b ui.Element, props FileTreeProps, state *FileTreeState, rows []FileTreeRow, index int) {
	move := func(target int, mods ui.Modifiers) {
		if target < 0 || target >= len(rows) {
			return
		}
		row := rows[target]
		if handle := state.rowHandles[row.ID]; handle != nil {
			handle.Focus()
		}
		if mods&(ui.Ctrl|ui.Super) != 0 && mods&ui.Shift == 0 {
			return
		}
		fileTreeEmit(props, FileTreeInput{Kind: FileTreeSelect, Row: row, Modifiers: mods})
	}
	for _, mods := range []ui.Modifiers{0, ui.Shift, ui.Ctrl, ui.Ctrl | ui.Shift, ui.Super, ui.Super | ui.Shift} {
		modifiers := mods
		b.OnShortcut(modifiers, ui.KeyUp, func() { move(index-1, modifiers) })
		b.OnShortcut(modifiers, ui.KeyDown, func() { move(index+1, modifiers) })
		b.OnShortcut(modifiers, ui.KeyHome, func() { move(0, modifiers) })
		b.OnShortcut(modifiers, ui.KeyEnd, func() { move(len(rows)-1, modifiers) })
	}
	b.OnShortcut(0, ui.KeyRight, func() {
		row := rows[index]
		if row.Directory && !props.Expanded[row.ID] {
			fileTreeEmit(props, FileTreeInput{Kind: FileTreeToggle, Row: row})
		} else if row.Directory && index+1 < len(rows) && rows[index+1].Depth > row.Depth {
			move(index+1, 0)
		}
	})
	b.OnShortcut(0, ui.KeyLeft, func() {
		row := rows[index]
		if row.Directory && props.Expanded[row.ID] {
			fileTreeEmit(props, FileTreeInput{Kind: FileTreeToggle, Row: row})
			return
		}
		for previous := index - 1; previous >= 0; previous-- {
			if rows[previous].Depth < row.Depth {
				move(previous, 0)
				return
			}
		}
	})
	// Keyboard buttons already activate on Enter and Space. Do not dispatch a
	// second open callback for those keys here.
}

func fileTreeTooltip(c *ui.Context, p palette, b ui.Element, props FileTreeProps, state *FileTreeState, row FileTreeRow) {
	if !b.Hovered() || row.Path == "" {
		return
	}
	if state.hoverID != row.ID {
		state.hoverID, state.hoverSince, state.suppressID = row.ID, c.Now(), ""
	}
	if b.Pressed() {
		state.suppressID = row.ID
	}
	if props.SuppressTooltip || state.suppressID == row.ID {
		return
	}
	if wait := fileTreeTipDelay - c.Now().Sub(state.hoverSince); wait > 0 {
		c.After(wait)
		return
	}
	ui.Overlay(c, func() {
		ui.Box(c.Key(props.Key+"-path-tooltip")).Absolute().PassThrough().Role(ui.RoleTooltip).AttachTo(b, ui.AnchorRight, ui.AnchorLeft).MarginStart(6).
			MaxWidth(520).Padding(6, 9).Radius(5).Background(p.surface).Border(1, p.line).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, .12)).Children(func() {
			label(c, row.Path, 12, p.text).Wrap()
		})
	})
}
