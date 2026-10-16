package ui

import (
	"strconv"

	"github.com/egoist/mygo/internal/platform"
)

// ThemedMenus opts the window into in-window Menu and ContextMenu popups,
// including default text editing menus. Call once at the end of every view,
// after its controls and dialogs. Changing scope closes an earlier entity's
// menu. Collection, Chosen and editing commands keep their existing behavior.
// Windows that do not opt in continue to use platform menus.
func ThemedMenus(c *Context, scope string) {
	MenuScope(c, scope)
	if raw := c.build(); raw != nil {
		raw.rt.buildThemedMenu(raw)
	}
}

// MenuScope sets the current menu owner scope before building controls.
// Use it with ThemedMenus at the view's end so a late choice from a previous
// page cannot reach a reused control's Menu callback.
func MenuScope(c *Context, scope string) {
	if raw := c.build(); raw != nil {
		mr := &raw.rt.menu
		if mr.scope != scope {
			mr.popup, mr.pending, mr.chosen, mr.asked = nil, nil, 0, 0
		}
		mr.themed, mr.scope = true, scope
	}
}

type menuPopup struct {
	shown *shownMenu
	scope string
	path  []*platform.MenuItem
	focus bool
}

type menuDecoration struct {
	icon   *SVG
	danger bool
}

func newMenuPopup(shown *shownMenu, scope string) *menuPopup {
	return &menuPopup{shown: shown, scope: scope, focus: true}
}

func menuOwner(e *node, id uint64) *node {
	if e == nil {
		return nil
	}
	if e.flags&(flagInvisible|flagInert) != 0 || e.leaving != 0 {
		return nil
	}
	if e.id == id {
		return e
	}
	for child := e.first; child != nil; child = child.next {
		if found := menuOwner(child, id); found != nil {
			return found
		}
	}
	return nil
}

func topMenuModal(e *node) uint64 {
	if e == nil || e.flags&(flagInvisible|flagInert) != 0 || e.leaving != 0 {
		return 0
	}
	var modal uint64
	if e.flags&flagModal != 0 {
		modal = e.id
	}
	for child := e.first; child != nil; child = child.next {
		if next := topMenuModal(child); next != 0 {
			modal = next
		}
	}
	return modal
}

func menuOwnerModal(e *node) uint64 {
	for count := 0; e != nil && count < 1000; count++ {
		if e.flags&flagModal != 0 {
			return e.id
		}
		if e.popover != nil {
			e = e.popover
		} else {
			e = e.parent
		}
	}
	return 0
}

func (rt *engine) buildThemedMenu(c *context) {
	mr := &rt.menu
	// showMenu activates pending menus after endPass has cleared the input
	// that opened them. In particular, a right-button down on platforms that
	// open immediately must not be mistaken for a click outside a new popup.
	p := mr.popup
	if p == nil {
		return
	}
	owner := menuOwner(c.root, p.shown.id)
	if owner == nil {
		owner = menuOwner(c.overlay, p.shown.id)
	}
	if p.scope != mr.scope || owner == nil || owner.disabled() || topMenuModal(c.overlay) != menuOwnerModal(owner) {
		mr.popup = nil
		return
	}
	// A point anchor positions cursor menus without losing the real owner's
	// dialog scope. No frame's nodes are retained in menuPopup.
	var anchor *node
	coreOverlay(c, func() {
		c.nextKey, c.keySet = "themed-menu-position", true
		anchor = coreBox(c).Absolute().Left(p.shown.x).Top(p.shown.y).Size(0, 0)
		anchor.popover = owner
	})
	if p.shown.button {
		anchor = owner
	}
	open, choice := true, 0
	var first *node
	maxHeight := max(float32(1), min(float32(480), c.h-24))
	t := c.theme
	c.nextKey, c.keySet = "themed-menu-popup", true
	panel := corePopoverBase(c, anchor, &open, func(panel *node) {
		panel.Width(min(float32(292), max(float32(1), c.w-20))).MaxHeight(maxHeight+12).
			Padding(5).Background(t.Surface).Border(1, t.Border).Radius(12).
			Shadow(0, 6, 20, 0, RGBA(0, 0, 0, .18))
		coreScroll(c).FillWidth().MaxHeight(maxHeight).Children(func() {
			coreColumn(c).Gap(2).FocusGroup(Vertical).Role(RoleMenu).Label("Menu").Children(func() {
				items := p.shown.menu.Items
				if n := len(p.path); n != 0 {
					parent := p.path[n-1]
					back := themedMenuRow(c, -parent.ID, "返回 · "+parent.Label, "‹", "", false, false, false, menuDecoration{})
					first = back
					if back.Clicked() {
						p.path = p.path[:n-1]
						p.focus, rt.consumed = true, true
					}
					coreBox(c).Height(1).FillWidth().Margin(4, 8).Background(t.Border)
					items = parent.Submenu.Items
				}
				for _, item := range items {
					if !item.Visible {
						continue
					}
					if item.Type == platform.MenuItemSeparator {
						coreBox(c).Height(1).FillWidth().Margin(4, 8).Background(t.Border)
						continue
					}
					mark := ""
					if item.Checked {
						mark = "✓"
					}
					suffix := item.Accelerator
					if item.Submenu != nil {
						suffix = "›"
					}
					checkable := item.Type == platform.MenuItemCheckbox || item.Type == platform.MenuItemRadio
					row := themedMenuRow(c, item.ID, item.Label, mark, suffix, !item.Enabled, checkable, item.Checked, p.shown.decorations[item.ID])
					if first == nil && item.Enabled {
						first = row
					}
					if !item.Enabled {
						continue
					}
					if row.Clicked() || item.Submenu != nil && row.Shortcut(0, KeyRight) {
						if item.Submenu != nil {
							p.path = append(p.path, item)
							p.focus, rt.consumed = true, true
						} else {
							choice, open = item.ID, false
						}
					}
				}
			})
		})
	})
	if panel != nil && len(p.path) != 0 && panel.OverlayShortcut(0, KeyLeft) {
		p.path = p.path[:len(p.path)-1]
		p.focus, rt.consumed = true, true
	}
	// PopoverBase must record the opener before focus moves into the menu.
	if open && p.focus && first != nil {
		focused := first.Focused()
		first.Focus()
		if focused {
			p.focus = false
		} else {
			rt.consumed = true
		}
	}
	if !open {
		mr.popup = nil
		if choice != 0 {
			rt.menuChosen(p.shown, choice)
			rt.consumed = true
		}
	}
}

func themedMenuRow(c *context, id int, label, mark, suffix string, disabled, checkable, checked bool, decoration menuDecoration) *node {
	c.nextKey, c.keySet = "themed-menu-item-"+strconv.Itoa(id), true
	t := c.theme
	row := coreButtonBase(c).Row().FillWidth().Height(34).Gap(9).Padding(0, 9).Radius(8).
		AlignItems(Center).Role(RoleMenuItem).Label(label).Tooltip(label).Disabled(disabled)
	if checkable {
		row.Role(RoleMenuItemCheckBox).Checked(checked)
	}
	background, foreground := t.Surface, t.Text
	if decoration.danger {
		foreground = t.Danger
	}
	if disabled {
		foreground = t.TextMuted
	} else if row.Focused() {
		background = t.SurfacePressed
		row.Border(1, t.Accent)
	} else if row.Hovered() {
		background = t.SurfaceHover
	}
	row.Background(background).Children(func() {
		coreBox(c).Size(16, 16).AlignItems(Center).Justify(Center).Children(func() {
			if mark != "" {
				coreText(c, mark).FontSize(14).TextColor(t.Accent).NoWrap()
			} else if decoration.icon != nil {
				coreIcon(c, decoration.icon).Size(16, 16).TextColor(foreground)
			}
		})
		coreText(c, label).FontSize(12).TextColor(foreground).Grow(1).MinWidth(0).SingleLine()
		if suffix != "" {
			coreText(c, suffix).FontSize(11).TextColor(t.TextMuted).NoWrap()
		}
	})
	return row
}
