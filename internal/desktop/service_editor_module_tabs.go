package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (state *serviceEditorState) selectedModuleIndex() int {
	for i, module := range state.modules {
		if module.id == state.selectedModuleID {
			return i
		}
	}
	if len(state.modules) > 0 {
		return 0
	}
	return -1
}

func (state *serviceEditorState) selectModule(id string) {
	if state.selectedModuleID != id {
		state.selectedModuleID = id
		state.revealSelectedModule = true
		state.modulePopup = nil
	}
}

func (s *app) renderServiceModuleTabs(c *ui.Context, p palette, state *serviceEditorState, selected int) {
	previous := selected
	labels := s.serviceModuleTabLabels(state.modules)
	ui.ScrollHorizontal(c.Key("service-editor-module-tab-scroll-"+state.session)).FillWidth().MinWidth(0).Height(44).Shrink(0).Padding(2, 2, 8).Children(func() {
		tabs := ui.TabsBase(c.Key("service-editor-module-tabs-"+state.session), &selected, len(labels))
		tabs.List.Gap(4).Label("模块页签").Children(func() {
			for i, module := range state.modules {
				title := labels[i]
				c.Key("service-editor-module-tab-" + module.id)
				tab := tabs.Tab(i).Height(34).MaxWidth(220).Padding(0, 12).Radius(8).Label(title).Tooltip(title).FocusRing(true)
				fg := p.muted
				weight := 500
				if i == selected {
					tab.Background(p.selected)
					fg, weight = p.blue, 600
					if s.dark {
						fg = p.text
					}
				} else if tab.Hovered() {
					tab.Background(p.soft)
				}
				tab.Children(func() { label(c, title, 12, fg).FontWeight(weight).MinWidth(0).SingleLine() })
				if state.revealSelectedModule && module.id == state.selectedModuleID {
					tab.ScrollIntoView()
					state.revealSelectedModule = false
				}
			}
		})
	})
	if selected != previous {
		state.selectModule(state.modules[selected].id)
		c.Invalidate()
	}
}

func (s *app) serviceModuleTabLabels(modules []serviceModuleDraft) []string {
	labels := make([]string, len(modules))
	counts := make(map[string]int, len(modules))
	for i := range modules {
		module := &modules[i]
		name := strings.TrimSpace(*s.serviceEditorModuleText(module, "name", module.name))
		labels[i] = firstNonEmpty(name, "default")
		counts[strings.ToLower(labels[i])]++
	}
	occurrences := make(map[string]int)
	for i, name := range labels {
		key := strings.ToLower(name)
		if counts[key] > 1 {
			occurrences[key]++
			labels[i] = fmt.Sprintf("%s · %d", name, occurrences[key])
		}
	}
	return labels
}
