package desktop

import "github.com/egoist/mygo/ui"

// overflowMenuState owns the one open overflow menu in the application. The
// menu key is cleared when its trigger leaves the rendered page, so returning
// to that page never unexpectedly reopens a popup.
type overflowMenuState struct {
	openKey         string
	pendingFocusKey string
	seen            bool
}

func (s *app) ensureOverflowMenuState() *overflowMenuState {
	if s.overflowMenus == nil {
		s.overflowMenus = &overflowMenuState{}
	}
	return s.overflowMenus
}

func (s *app) beginOverflowMenuFrame() {
	if s.overflowMenus != nil {
		s.overflowMenus.seen = false
	}
}

func (s *app) endOverflowMenuFrame() {
	state := s.overflowMenus
	if state != nil && state.openKey != "" && !state.seen {
		state.openKey = ""
	}
	if state != nil && state.pendingFocusKey != "" && (state.pendingFocusKey != state.openKey || !state.seen) {
		state.pendingFocusKey = ""
	}
}

// overflowMenu is the shared native overflow control for infrequent actions.
// MyGO's PopoverBase supplies light dismissal and focus restoration; the
// attached panel chooses the side that fits the window.
func (s *app) overflowMenu(c *ui.Context, p palette, key, title string, disabled bool, build func(*overflowMenuBuilder)) {
	s.overflowMenuOwned(c, p, key, key, title, disabled, build)
}

// overflowMenuOwned keeps an existing trigger key while giving state an
// entity-specific owner key when the menu belongs to changing list content.
func (s *app) overflowMenuOwned(c *ui.Context, p palette, triggerKey, ownerKey, title string, disabled bool, build func(*overflowMenuBuilder)) {
	s.overflowMenuOwnedAt(c, p, triggerKey, ownerKey, title, disabled, ui.AnchorBottomRight, ui.AnchorTopRight, build)
}

func (s *app) overflowMenuOwnedAt(c *ui.Context, p palette, triggerKey, ownerKey, title string, disabled bool, at, self ui.Anchor, build func(*overflowMenuBuilder)) {
	stateKey := "overflow-" + ownerKey
	state := s.ensureOverflowMenuState()
	if disabled && state.openKey == stateKey {
		state.openKey = ""
		if state.pendingFocusKey == stateKey {
			state.pendingFocusKey = ""
		}
	}
	trigger := ui.ButtonBase(c.Key(triggerKey)).Width(34).Height(34).Radius(20).Center().
		Role(ui.RoleMenuButton).Label(title).Tooltip(title).Disabled(disabled)
	focused, hovered := trigger.Focused(), trigger.Hovered()
	triggerColor := p.text
	if disabled {
		triggerColor = p.muted
	} else if state.openKey == stateKey || focused {
		trigger.Background(p.selected)
		if focused {
			trigger.Border(1, p.blue)
		}
	} else if hovered {
		trigger.Background(p.soft)
	}
	trigger.Children(func() { icon(c, "more", 18, triggerColor) })
	trigger.OnShortcut(0, ui.KeyDown, func() {
		state.openKey = stateKey
		state.pendingFocusKey = stateKey
	})
	trigger.OnShortcut(0, ui.KeyEscape, func() {
		if state.openKey == stateKey {
			state.openKey = ""
		}
		if state.pendingFocusKey == stateKey {
			state.pendingFocusKey = ""
		}
	})
	clicked := trigger.Clicked()
	if !disabled && clicked {
		if state.openKey == stateKey {
			state.openKey = ""
			if state.pendingFocusKey == stateKey {
				state.pendingFocusKey = ""
			}
		} else {
			state.openKey = stateKey
			state.pendingFocusKey = stateKey
		}
	}
	open := !disabled && state.openKey == stateKey
	if open {
		state.seen = true
		trigger.Background(p.selected)
		width, height := c.Size()
		maxWidth := max(float32(1), width-20)
		panelWidth := min(float32(280), max(float32(160), maxWidth))
		maxHeight := max(float32(1), min(float32(480), height-24))
		var menu *overflowMenuBuilder
		ui.PopoverBase(c, trigger, &open, func(panel ui.Element) {
			panel.AttachTo(trigger, at, self).Margin(4, 0, 0, 0).
				Width(panelWidth).MaxWidth(maxWidth).MaxHeight(maxHeight+12).
				Padding(5).Background(p.surface).Border(1, p.line).Radius(12).
				Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18)).
				Children(func() {
					ui.Scroll(c).FillWidth().MaxHeight(maxHeight).Children(func() {
						menu = &overflowMenuBuilder{
							app: s, c: c, p: p, key: stateKey, open: &open,
							firstFocusable: true,
						}
						ui.Column(c).Gap(2).FocusGroup(ui.Vertical).Role(ui.RoleMenu).Label(title).Children(func() {
							if build != nil {
								build(menu)
							}
						})
					})
				})
		})
		// PopoverBase records its opener while it builds the overlay. Focus the
		// first menu item only after it returns, or the item would be recorded
		// as the opener and MyGO could not restore focus when the menu closes.
		if open && state.pendingFocusKey == stateKey && menu != nil {
			if menu.hasFirstFocus {
				focused := menu.firstFocus.Focused()
				menu.firstFocus.Focus()
				if focused {
					state.pendingFocusKey = ""
				}
			}
		}
		if !open && state.openKey == stateKey {
			state.openKey = ""
		}
		if !open && state.pendingFocusKey == stateKey {
			state.pendingFocusKey = ""
		}
	}
}

type overflowMenuBuilder struct {
	native         *ui.Menu
	app            *app
	c              *ui.Context
	p              palette
	key            string
	open           *bool
	firstFocusable bool
	firstFocus     ui.Element
	hasFirstFocus  bool
	items          int
	separator      bool
}

type overflowMenuItem struct {
	menu      *overflowMenuBuilder
	key       string
	label     string
	icon      string
	disabled  bool
	danger    bool
	checkable bool
	checked   bool
}

func (m *overflowMenuBuilder) Item(key, label string) *overflowMenuItem {
	return &overflowMenuItem{menu: m, key: key, label: label}
}

func (m *overflowMenuBuilder) Separator() {
	if m.items == 0 || m.separator {
		return
	}
	if m.native != nil {
		m.native.Separator()
	} else {
		ui.Box(m.c).Height(1).FillWidth().Margin(4, 8).Background(m.p.line)
	}
	m.separator = true
}

func (item *overflowMenuItem) Icon(name string) *overflowMenuItem {
	item.icon = name
	return item
}

func (item *overflowMenuItem) Disabled(disabled bool) *overflowMenuItem {
	item.disabled = disabled
	return item
}

func (item *overflowMenuItem) Dangerous() *overflowMenuItem {
	item.danger = true
	return item
}

func (item *overflowMenuItem) Checked(checked bool) *overflowMenuItem {
	item.checkable = true
	item.checked = checked
	return item
}

// Chosen draws the item and reports its activation, matching MyGO's Menu DSL.
func (item *overflowMenuItem) Chosen() bool {
	m := item.menu
	if m.native != nil {
		native := m.native.Item(item.label).Disabled(item.disabled)
		if svg := icons[item.icon]; svg != nil {
			native.Icon(svg)
		}
		if item.danger {
			native.Dangerous()
		}
		if item.checkable {
			native.Checked(item.checked)
		}
		m.items++
		m.separator = false
		return native.Chosen()
	}
	role := ui.RoleMenuItem
	if item.checkable {
		role = ui.RoleMenuItemCheckBox
	}
	button := ui.ButtonBase(m.c.Key(m.key+"-item-"+item.key)).
		Row().FillWidth().Height(34).Gap(9).Padding(0, 9).Radius(8).
		AlignItems(ui.Center).Role(role).Label(item.label).Tooltip(item.label).
		Disabled(item.disabled)
	if item.checked {
		button.Checked(true)
	}
	background, foreground := m.p.surface, m.p.text
	if item.danger {
		foreground = m.p.danger
	}
	if item.disabled {
		foreground = m.p.muted
	} else if button.Focused() {
		background = m.p.selected
		button.Border(1, m.p.blue)
	} else if button.Hovered() {
		background = m.p.soft
	}
	button.Background(background)
	if !item.disabled && m.firstFocusable {
		m.firstFocus = button
		m.hasFirstFocus = true
		m.firstFocusable = false
	}
	button.Children(func() {
		ui.Box(m.c).Width(16).Height(16).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if _, ok := icons[item.icon]; item.icon != "" && ok {
				icon(m.c, item.icon, 16, foreground)
			}
		})
		label(m.c, item.label, 12, foreground).Grow(1).MinWidth(0).SingleLine().Tooltip(item.label)
		if item.checked {
			icon(m.c, "check", 15, m.p.blue)
		}
	})
	chosen := button.Clicked()
	if button.Shortcut(0, ui.KeyEscape) {
		*m.open = false
		if m.app.overflowMenus != nil {
			if m.app.overflowMenus.openKey == m.key {
				m.app.overflowMenus.openKey = ""
			}
			if m.app.overflowMenus.pendingFocusKey == m.key {
				m.app.overflowMenus.pendingFocusKey = ""
			}
		}
	}
	m.items++
	m.separator = false
	if chosen {
		*m.open = false
		if m.app.overflowMenus != nil && m.app.overflowMenus.openKey == m.key {
			m.app.overflowMenus.openKey = ""
		}
		if m.app.overflowMenus != nil && m.app.overflowMenus.pendingFocusKey == m.key {
			m.app.overflowMenus.pendingFocusKey = ""
		}
	}
	return chosen
}
