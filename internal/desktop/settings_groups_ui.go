package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

type settingsGroupDrag struct {
	Home, GroupID string
}

var settingsGroupGripIcon = func() *ui.SVG {
	image, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round"><circle cx="9" cy="5" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="9" cy="12" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="19" r="1"/></svg>`))
	if err != nil {
		panic(err)
	}
	return image
}()

func (s *app) settingsGroups(c *ui.Context, p palette) {

	settingsHelpCard(c, p, "", "", func() {
		if len(s.snapshot.Groups) == 0 {
			settingsMuted(c, p, "当前配置没有项目组。")
		}
		for _, group := range s.snapshot.Groups {
			groupCopy := group
			key := s.snapshot.Home + ":" + groupCopy.ID
			row := ui.Column(c.Key("settings-group-" + key)).FillWidth().Padding(12).Gap(9).Background(p.soft).Radius(10).Label("项目组 " + groupCopy.Name)
			// The group body inserts after it; its header inserts before it.
			// Separate native drop targets retain the release destination even
			// if the pointer moves again before the next frame.
			after := s.settingsGroupDropTarget(row, groupCopy.ID, true)
			row.Children(func() {
				header := ui.Row(c.Key("settings-group-header-" + key)).FillWidth().Gap(8).AlignItems(ui.Center)
				before := s.settingsGroupDropTarget(header, groupCopy.ID, false)
				header.Children(func() {
					if len(s.snapshot.Groups) > 1 {
						grip := ui.Box(c.Key("settings-group-drag-" + key)).Width(20).Height(26).Shrink(0).Center().Label("拖动排序 " + groupCopy.Name).Tooltip("拖动调整项目组顺序").Disabled(s.busy)
						if !s.busy {
							grip.Drag(settingsGroupDrag{Home: s.snapshot.Home, GroupID: groupCopy.ID}).Cursor(ui.CursorGrab)
						}
						if grip.Hovered() {
							grip.Background(p.selected).Radius(5)
						}
						if grip.Dragging() {
							grip.Cursor(ui.CursorGrabbing)
							row.Opacity(0.55)
						}
						grip.Children(func() { ui.Icon(c, settingsGroupGripIcon).Width(14).Height(14).TextColor(p.muted) })
					}
					label(c, groupCopy.Name, 13, p.text).FontWeight(600).Grow(1).SingleLine()
					label(c, fmt.Sprintf("%d 个任务", s.qaGroupTaskCount(groupCopy.ID)), 10, p.muted).SingleLine()
					if iconButton(c, "settings-group-rename-"+groupCopy.ID, "edit", "重命名项目组", p).Clicked() {
						s.openForm("settings.group.rename", "重命名项目组", "", groupCopy.ID, "", "", formField{key: "name", label: "项目组名称", value: groupCopy.Name})
					}
					if len(s.snapshot.Groups) > 1 && s.qaGroupTaskCount(groupCopy.ID) == 0 && iconButton(c, "settings-group-remove-"+groupCopy.ID, "delete", "删除空项目组", p).Clicked() {
						s.confirmNative("删除项目组", "只允许删除没有任务的项目组。", groupCopy.Name, func() { s.removeConfigGroup(groupCopy.ID) })
					}
				})
				if before || after {
					line := ui.Box(c).Absolute().Left(8).Right(8).Height(2).Background(p.blue).Radius(1)
					if before {
						line.Top(0)
					} else {
						line.Bottom(0)
					}
				}
			})
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if action(c, "settings-group-add", "plus", "新建项目组", p, false) {
				s.openForm("settings.group.add", "新建项目组", "", "", "", "", formField{key: "name", label: "项目组名称"})
			}
		})
	})
}

func (s *app) settingsGroupDropTarget(target ui.Element, groupID string, after bool) bool {
	if s.busy || len(s.snapshot.Groups) < 2 {
		return false
	}
	valid := func(drag settingsGroupDrag) bool {
		if drag.Home != s.snapshot.Home || drag.GroupID == "" || drag.GroupID == groupID {
			return false
		}
		_, ok := settingsGroupInsertPosition(groupPosition(s.snapshot.Groups, drag.GroupID), groupPosition(s.snapshot.Groups, groupID), len(s.snapshot.Groups), after)
		return ok
	}
	if drag, dropped := ui.Drop[settingsGroupDrag](target); dropped && valid(drag) {
		s.dropConfigGroup(drag, groupID, after)
	}
	drag, over := ui.DragOver[settingsGroupDrag](target)
	return over && valid(drag)
}

// Indices refer to the current order before the dragged group is removed.
func settingsGroupInsertPosition(from, target, count int, after bool) (int, bool) {
	if from < 0 || target < 0 || from >= count || target >= count || from == target {
		return 0, false
	}
	next := target
	if after {
		next++
	}
	if from < next {
		next--
	}
	return next, next != from
}

func (s *app) dropConfigGroup(drag settingsGroupDrag, targetID string, after bool) {
	if s.busy || drag.Home != s.snapshot.Home || drag.GroupID == "" || drag.GroupID == targetID {
		return
	}
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	groups, _ := config["groups"].([]any)
	from, target := -1, -1
	seen := make(map[string]bool, len(groups))
	for index, raw := range groups {
		id := mapString(mapData(raw), "id")
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		if id == drag.GroupID {
			from = index
		}
		if id == targetID {
			target = index
		}
	}
	next, changed := settingsGroupInsertPosition(from, target, len(groups), after)
	if !changed {
		return
	}
	moved := groups[from]
	if from < next {
		copy(groups[from:next], groups[from+1:next+1])
	} else {
		copy(groups[next+1:from+1], groups[next:from])
	}
	groups[next] = moved
	config["groups"] = groups
	s.saveSettingsConfigMap(config, "groups")
}
