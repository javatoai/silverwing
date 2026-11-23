package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) setActiveTaskCreationService(serviceID string) {
	state := s.taskUnit.state.taskCreation
	if state.activeServiceID == serviceID {
		return
	}
	state.closeTaskCreationModulePicker(state.activeServiceID, state.selectedModuleIDs[state.activeServiceID])
	state.activeServiceID = serviceID
	state.revealModuleTab = true
}

func (s *app) selectTaskCreationModule(serviceID, moduleID string) {
	state := s.taskUnit.state.taskCreation
	previous := state.selectedModuleIDs[serviceID]
	if previous == moduleID || state.moduleData[serviceID][moduleID] == nil {
		return
	}
	state.closeTaskCreationModulePicker(serviceID, previous)
	if state.selectedModuleIDs == nil {
		state.selectedModuleIDs = make(map[string]string)
	}
	state.selectedModuleIDs[serviceID] = moduleID
	if state.activeServiceID == serviceID {
		state.revealModuleTab = true
	}
}

func (state *taskCreationState) closeTaskCreationModulePicker(serviceID, moduleID string) {
	if draft := state.moduleData[serviceID][moduleID]; draft != nil {
		picker := &draft.repositoryPicker
		if picker.branchOpen {
			cancelTaskRepositoryBranchRead(picker, firstNonEmpty(strings.TrimSpace(draft.BaseRemote), taskBranchRemote(draft.BaseRef)))
		}
		if picker.remoteOpen {
			cancelTaskRepositoryRemoteRead(picker)
		}
		picker.branchOpen = false
		picker.remoteOpen = false
		picker.branchFocusPending = false
	}
}

func (s *app) renderTaskCreationModuleTabs(c *ui.Context, p palette, serviceID string, selected int) {
	state := s.taskUnit.state.taskCreation
	ids, drafts := state.moduleOrder[serviceID], state.moduleData[serviceID]
	labels := taskCreationModuleTabLabels(ids, drafts)
	previous := selected
	var tabs ui.TabsParts
	ui.ScrollHorizontal(c.Key("task-create-module-tab-scroll")).FillWidth().MinWidth(0).Height(44).Shrink(0).Padding(2, 2, 8).Children(func() {
		tabs = ui.TabsBase(c.Key("task-create-module-tabs"), &selected, len(ids))
		tabs.List.Gap(4).Label("模块页签").Children(func() {
			for i, id := range ids {
				title := labels[i]
				c.Key("task-create-module-tab-" + id)
				tab := tabs.Tab(i).Height(34).MaxWidth(220).Padding(0, 12).Radius(8).Label(title).Tooltip(title).FocusRing(true)
				foreground, weight := p.muted, 500
				if i == selected {
					tab.Background(p.selected)
					foreground, weight = p.blue, 600
					if s.dark {
						foreground = p.text
					}
				} else if tab.Hovered() {
					tab.Background(p.soft)
				}
				tab.Children(func() { label(c, title, 12, foreground).FontWeight(weight).MinWidth(0).SingleLine() })
				if state.revealModuleTab && i == previous {
					tab.ScrollIntoView()
					state.revealModuleTab = false
				}
			}
		})
	})
	if tabs.List.Changed() && selected != previous {
		s.syncTaskCreationModuleDraft(serviceID, drafts[ids[previous]])
		s.selectTaskCreationModule(serviceID, ids[selected])
		c.Invalidate()
	}
}

func taskCreationModuleTabLabels(ids []string, drafts map[string]*taskCreationModuleDraft) []string {
	labels := make([]string, len(ids))
	counts := make(map[string]int, len(ids))
	for i, id := range ids {
		name := id
		if draft := drafts[id]; draft != nil {
			name = strings.TrimSpace(draft.Name)
		}
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
