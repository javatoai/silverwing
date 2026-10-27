package desktop

import (
	"github.com/egoist/mygo/ui"
)

func (s *app) renderDeveloperTools(c *ui.Context) {
	p := setTheme(c, s.dark)
	state := s.ensureToolState()
	tabs := s.visibleDeveloperToolTabs()
	state.activeTab = reconcileDeveloperToolTab(state.activeTab, tabs)
	if _, ok := developerToolPrimaryActionForTab(state.activeTab); ok {
		c.OnShortcut(ui.Cmd, ui.KeyEnter, func() {
			s.runDeveloperToolPrimaryAction(state, state.activeTab)
		})
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(16, 0, 0, 0).Gap(0).Children(func() {
		if len(tabs) == 0 {
			ui.Column(c).Grow(1).MinWidth(0).Padding(24).Children(func() {
				label(c, "所有实用工具均已关闭。可在“设置 → 快捷工具”中重新开启。", 13, p.muted).Wrap()
			})
			return
		}
		ui.Row(c).Height(40).Shrink(0).Padding(0, 16).Children(func() {
			for _, tab := range tabs {
				selected := state.activeTab == tab.key
				ui.Column(c).Height(40).MinWidth(90).Shrink(0).AlignItems(ui.Center).Children(func() {
					b := ui.ButtonBase(c.Key("native-tool-tab-"+tab.key)).FillWidth().Height(37).Padding(0, 16).Radius(0).Label(tab.title)
					fg := p.muted
					if selected {
						fg = p.blue
					} else if b.Hovered() {
						b.Background(p.soft)
					}
					b.Children(func() { label(c, tab.title, 14, fg).FontWeight(500).NoWrap() })
					if b.Clicked() {
						state.activeTab = tab.key
					}
					if selected {
						width, _ := c.MeasureText(0, ui.Span{Text: tab.title, Size: 14, Weight: 500})
						ui.Box(c).Height(3).Width(max(24, width)).Radius(1.5).Background(p.blue)
					} else {
						ui.Box(c).Height(3)
					}
				})
			}
			ui.Spacer(c)
			if status := s.developerToolStatus(state.activeTab); status != "" {
				label(c, status, 12, p.muted).NoWrap()
			}
		})
		ui.Divider(c)
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(12, 20).Children(func() {
			switch state.activeTab {
			case "日期时间":
				s.renderTimeTool(c, p, state)
			case "文本对比":
				s.renderDiffTool(c, p, state)
			case "端口":
				s.renderPortsTool(c, p, state)
			default:
				s.renderJSONTool(c, p, state)
			}
		})
	})
}

type developerToolTab struct {
	key, title, visibilityKey string
	visibleByDefault          bool
}

func (s *app) visibleDeveloperToolTabs() []developerToolTab {
	all := []developerToolTab{
		{key: "JSON", title: "JSON", visibilityKey: "json", visibleByDefault: true},
		{key: "日期时间", title: "日期时间", visibilityKey: "dateTime", visibleByDefault: true},
		{key: "文本对比", title: "文本对比", visibilityKey: "textDiff", visibleByDefault: true},
		{key: "端口", title: "杀端口", visibilityKey: "killPort", visibleByDefault: false},
	}
	visible := make([]developerToolTab, 0, len(all))
	for _, tab := range all {
		if s.configNestedBool("toolVisibility", tab.visibilityKey, tab.visibleByDefault) {
			visible = append(visible, tab)
		}
	}
	return visible
}

func reconcileDeveloperToolTab(current string, visible []developerToolTab) string {
	if len(visible) == 0 {
		return ""
	}
	for _, tab := range visible {
		if tab.key == current {
			return current
		}
	}
	order := []string{"JSON", "日期时间", "文本对比", "端口"}
	currentIndex := -1
	for i, key := range order {
		if key == current {
			currentIndex = i
			break
		}
	}
	for offset := 1; offset <= len(order); offset++ {
		index := (currentIndex + offset) % len(order)
		if index < 0 {
			index += len(order)
		}
		candidate := order[index]
		for _, tab := range visible {
			if tab.key == candidate {
				return candidate
			}
		}
	}
	return visible[0].key
}

func toolButton(c *ui.Context, key, text string, p palette, primary bool) bool {
	b := ui.ButtonBase(c.Key(key)).Row().Height(32).Padding(0, 10).Gap(6).Radius(7).Label(text)
	fg := p.blue
	if primary {
		fg = ui.Hex("#FFFFFF")
		b.Padding(0, 12).Background(p.blue)
	}
	if b.Hovered() {
		if primary {
			b.Background(ui.Hex("#285CD3"))
		} else {
			b.Background(p.soft)
		}
	}
	b.Children(func() { label(c, text, 14, fg).FontWeight(500).NoWrap() })
	return b.Clicked()
}

func toolButtonWithEnabled(c *ui.Context, key, text string, p palette, primary, enabled bool) bool {
	b := ui.ButtonBase(c.Key(key)).Row().Height(32).Padding(0, 10).Gap(6).Radius(7).Label(text).Disabled(!enabled)
	fg := p.blue
	if primary {
		fg = ui.Hex("#FFFFFF")
		b.Padding(0, 12).Background(p.blue)
	}
	if !enabled {
		fg = p.muted
		if primary {
			b.Background(p.soft)
		}
	} else if b.Hovered() {
		if primary {
			b.Background(ui.Hex("#285CD3"))
		} else {
			b.Background(p.soft)
		}
	}
	b.Children(func() { label(c, text, 14, fg).FontWeight(500).NoWrap() })
	return enabled && b.Clicked()
}

func toolToggle(c *ui.Context, key, text string, checked bool, p palette) bool {
	b := ui.ButtonBase(c.Key(key)).Row().Height(29).Padding(0, 9).Gap(6).Radius(7).Label(text)
	fg, bg := p.muted, p.surface
	if checked {
		fg, bg = p.blue, p.selected
	}
	if b.Hovered() && !checked {
		bg = p.soft
	}
	b.Background(bg).Children(func() {
		mark := "□"
		if checked {
			mark = "✓"
		}
		label(c, mark, 11, fg)
		label(c, text, 10, fg).NoWrap()
	})
	return b.Clicked()
}

func bounded(value, lower, upper int) int {
	if value < lower {
		return lower
	}
	if value > upper {
		return upper
	}
	return value
}
