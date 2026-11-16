package desktop

import (
	"github.com/egoist/mygo/ui"
)

func (s *app) renderDeveloperTools(c *ui.Context) {
	p := setTheme(c, s.dark)
	state := s.ensureToolState()
	tabs := s.visibleDeveloperToolTabs()
	previousTab := state.activeTab
	state.activeTab = reconcileDeveloperToolTab(state.activeTab, tabs)
	revealTab := state.activeTab != previousTab
	if _, ok := developerToolPrimaryActionForTab(state.activeTab); ok {
		c.OnShortcut(ui.Cmd, ui.KeyEnter, func() {
			s.runDeveloperToolPrimaryAction(state, state.activeTab)
		})
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(16, 0, 0, 0).Gap(0).Children(func() {
		if len(tabs) == 0 {
			ui.Column(c).Grow(1).MinWidth(0).Padding(24).Children(func() {
				label(c, "所有研发工具均已关闭。可在“设置 → 外观与交互 → 导航与快捷工具”中重新开启。", 13, p.muted).Wrap()
			})
			return
		}
		s.renderDeveloperToolTabs(c, p, state, tabs, revealTab)
		ui.Divider(c)
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(12, 20).Children(func() {
			switch state.activeTab {
			case "日期时间":
				s.renderTimeTool(c, p, state)
			case "文本对比":
				s.renderDiffTool(c, p, state)
			case "端口":
				s.renderPortsTool(c, p, state)
			case "Base64":
				s.renderBase64Tool(c, p, state)
			case "二维码":
				s.renderQRTool(c, p, state)
			case "文字计数":
				s.renderTextCountTool(c, p, state)
			default:
				s.renderJSONTool(c, p, state)
			}
		})
	})
}

type developerToolTabScrollKey struct{}

func (s *app) renderDeveloperToolTabs(c *ui.Context, p palette, state *toolPageState, tabs []developerToolTab, revealTab bool) {
	scroll := ui.Local(c.Root(), developerToolTabScrollKey{}, func() ui.ScrollState { return ui.ScrollState{} })
	ui.Row(c).FillWidth().Height(46).Shrink(0).Padding(0, 16).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.ScrollHorizontal(c.Key("native-tool-tabs")).Grow(1).MinWidth(0).Height(46).Padding(0, 0, 6).TrackScroll(scroll).Label("研发工具标签").Children(func() {
			for _, tab := range tabs {
				selected := state.activeTab == tab.key
				ui.Column(c).Height(40).MinWidth(90).Shrink(0).AlignItems(ui.Center).Children(func() {
					b := ui.ButtonBase(c.Key("native-tool-tab-"+tab.key)).FillWidth().Height(37).Padding(0, 16).Radius(0).Label(tab.title).Checked(selected)
					fg := p.muted
					if selected {
						fg = p.blue
					} else if b.Hovered() {
						b.Background(p.soft)
					}
					b.Children(func() { label(c, tab.title, 14, fg).FontWeight(500).NoWrap() })
					if b.Clicked() {
						state.activeTab = tab.key
						b.ScrollIntoView()
					} else if selected && revealTab {
						b.ScrollIntoView()
					}
					if selected {
						width, _ := c.MeasureText(0, ui.Span{Text: tab.title, Size: 14, Weight: 500})
						ui.Box(c).Height(3).Width(max(24, width)).Radius(1.5).Background(p.blue)
					} else {
						ui.Box(c).Height(3)
					}
				})
			}
		})
		if status := s.developerToolStatus(state.activeTab); status != "" {
			label(c, status, 12, p.muted).Shrink(0).Padding(0, 0, 6).NoWrap()
		}
	})
}

type developerToolTab struct {
	key, title, visibilityKey, description string
	visibleByDefault                       bool
}

var developerToolTabs = []developerToolTab{
	{key: "JSON", title: "JSON", visibilityKey: "json", description: "格式化、压缩与转义 JSON。", visibleByDefault: true},
	{key: "日期时间", title: "日期时间", visibilityKey: "dateTime", description: "时间戳与日期转换。", visibleByDefault: true},
	{key: "文本对比", title: "文本对比", visibilityKey: "textDiff", description: "查看两段文本的差异。", visibleByDefault: true},
	{key: "端口", title: "杀端口", visibilityKey: "killPort", description: "查找并结束占用端口的进程。"},
	{key: "Base64", title: "Base64", visibilityKey: "base64", description: "编码和解码 UTF-8 文本。", visibleByDefault: true},
	{key: "二维码", title: "二维码", visibilityKey: "qrCode", description: "生成二维码，从图片识别二维码内容。", visibleByDefault: true},
	{key: "文字计数", title: "文字计数", visibilityKey: "textCount", description: "统计字符数、汉字数、行数与 UTF-8 字节数。", visibleByDefault: true},
}

func (s *app) visibleDeveloperToolTabs() []developerToolTab {
	visible := make([]developerToolTab, 0, len(developerToolTabs))
	for _, tab := range developerToolTabs {
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
	currentIndex := -1
	for i, tab := range developerToolTabs {
		if tab.key == current {
			currentIndex = i
			break
		}
	}
	for offset := 1; offset <= len(developerToolTabs); offset++ {
		index := (currentIndex + offset) % len(developerToolTabs)
		candidate := developerToolTabs[index].key
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
