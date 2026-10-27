package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

func parseMaterialsExpansionDepth(text string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("展开层级必须为非负整数，0 表示全部折叠")
	}
	return value, nil
}

func (s *app) saveMaterialsExpansionDepth(text string) {
	depth, err := parseMaterialsExpansionDepth(text)
	if err != nil {
		s.lastError = err.Error()
		return
	}
	state := s.desktopPreferences
	if state == nil || state.store == nil {
		s.lastError = "桌面偏好尚未加载，无法保存目录展开层级"
		return
	}
	value, err := state.store.Update(func(value *DesktopPreferences) {
		if value.MaterialsExpansionDepth != depth {
			value.MaterialsExpansionDepth = depth
			value.MaterialsExpansionRevision++
			if value.MaterialsExpansionRevision == 0 {
				value.MaterialsExpansionRevision = 1
			}
		}
	})
	if err != nil {
		s.lastError = "保存目录展开层级失败：" + err.Error()
		return
	}
	state.value.MaterialsExpansionDepth = value.MaterialsExpansionDepth
	state.value.MaterialsExpansionRevision = value.MaterialsExpansionRevision
	s.initializeMaterialsTreeExpansion()
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) settingsMaterialsExpansionDepth(c *ui.Context, p palette) {
	input := s.input("setting:materialsExpansionDepth", strconv.Itoa(s.materialsExpansionDepth()))
	settingsAppearanceRow(c, p, "默认展开目录层级", "0 表示全部折叠；修改后重新应用，之后继续记住手工展开状态。", func(stacked bool) {
		observe := func() { s.autosaveSettingsInput("materials-expansion-depth", input, s.saveMaterialsExpansionDepth) }
		observe()
		ui.TextInput(c.Key("settings-materials-expansion-depth"), input).Width(120).MinWidth(0).Height(36).Label("默认展开目录层级").OnChange(observe).OnSubmit(func() { s.flushSettingsAutosaves(true) })
	})
	if _, err := parseMaterialsExpansionDepth(*input); err != nil {
		label(c, err.Error(), 11, p.danger).Wrap()
	}
}
