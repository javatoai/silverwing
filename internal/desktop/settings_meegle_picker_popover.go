package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) observeMeegleProjectPicker() {
	if state := s.meegleProjectCatalog; state != nil && state.pickerOpen && (s.page != "settings" || s.settingsPage != "cli-meegle") {
		s.closeMeegleProjectPicker()
	}
	if state := s.meegleProjectCatalog; state != nil && state.pickerOpen &&
		(state.context.home != s.snapshot.Home || state.context.executable != strings.TrimSpace(s.cliConfiguredPath("meegle"))) {
		s.syncMeegleProjectCatalogContext(state)
		s.closeMeegleProjectPicker()
	}
}

func (s *app) onMeegleProjectPickerWindowFocused() {
	if state := s.meegleProjectCatalog; state != nil && state.pickerOpen && !s.syncMeegleProjectCatalogContext(state) {
		s.closeMeegleProjectPicker()
	}
}

func (s *app) renderMeegleProjectPicker(c *ui.Context, p palette, anchor ui.Element) {
	state := s.meegleProjectCatalog
	if state == nil || !state.pickerOpen {
		return
	}
	authenticated := s.syncMeegleProjectCatalogIdentity(state)
	var search ui.Element
	ui.PopoverBase(c.Key("meegle-project-picker"), anchor, &state.pickerOpen, func(panel ui.Element) {
		width, height := c.Size()
		panel.Width(min(440, max(1, width-24))).MaxHeight(max(1, height-24)).Margin(4, 0, 0, 0).
			Padding(8).Gap(6).Background(p.surface).Border(1, p.line).Radius(10).
			Shadow(0, 5, 18, 0, ui.RGBA(0, 0, 0, .16)).Label("选择 Meegle 项目空间")
		panel.Children(func() {
			options := filterMeegleProjectPickerOptions(s.meegleProjectPickerOptions(state), state.pickerQuery)
			choose := func() {
				if state.pickerHighlight >= 0 && state.pickerHighlight < len(options) && state.pickerPendingSave == 0 && !s.busy {
					option := options[state.pickerHighlight]
					state.pickerSelection = option.value
					s.submitMeegleProjectPickerSelection(state, option.project)
				}
			}
			ui.Row(c).FillWidth().Gap(4).Children(func() {
				search = ui.TextInput(c.Key("meegle-project-picker-search"), &state.pickerQuery).Grow(1).MinWidth(0).Height(34).
					Placeholder("搜索名称、短名或 Key").Label("搜索 Meegle 项目空间").ReadOnly(state.pickerPendingSave != 0)
				search.OnChange(func() { state.pickerHighlight = 0; state.pickerSelection = "" })
				search.OnShortcut(0, ui.KeyDown, func() { state.pickerHighlight = min(state.pickerHighlight+1, len(options)-1) })
				search.OnShortcut(0, ui.KeyUp, func() { state.pickerHighlight = max(state.pickerHighlight-1, 0) })
				search.OnSubmit(choose)
				if iconButtonDisabled(c, "meegle-project-picker-refresh", "refresh", "刷新项目目录", p, !authenticated || state.loading || state.pickerPendingSave != 0).Clicked() {
					s.requestMeegleProjectCatalog(true)
				}
			})
			switch {
			case !dataBool(s.settingsCLIStatus["meegle"], "installed"):
				label(c, "请先配置可用的 Meegle CLI。", 11, p.amber).Wrap()
			case !authenticated:
				label(c, "请先登录 Meegle CLI。", 11, p.muted).Wrap()
			default:
				if state.loading {
					label(c, "正在加载项目目录…", 11, p.muted)
				}
				if state.error != "" {
					label(c, "读取失败："+state.error, 11, p.danger).Wrap()
				}
				if state.loaded && len(options) == 0 {
					message := "没有匹配的项目。"
					if state.pickerQuery == "" {
						message = s.meegleProjectPickerEmptyMessage(state)
					}
					label(c, message, 11, p.muted).Wrap()
				}
				ui.Scroll(c.Key("meegle-project-picker-options")).FillWidth().MaxHeight(max(1, min(300, height-150))).Children(func() {
					ui.Column(c).FillWidth().Gap(2).FocusGroup(ui.Vertical).Role(ui.RoleList).Children(func() {
						for index, option := range options {
							row := ui.ButtonBase(c.Key("meegle-project-option-"+option.project.ProjectKey)).FillWidth().Padding(8, 10).Radius(7).
								Role(ui.RoleListItem).Label(option.value).Disabled(state.pickerPendingSave != 0 || s.busy)
							if index == state.pickerHighlight || row.Focused() || row.Hovered() {
								row.Background(p.selected)
							}
							if index == state.pickerHighlight && state.pickerShown != option.value {
								row.ScrollIntoView()
								state.pickerShown = option.value
							}
							row.Children(func() {
								ui.Column(c).FillWidth().Gap(2).Children(func() {
									label(c, firstNonEmpty(option.project.Name, option.project.SimpleName), 12, p.text).SingleLine()
									label(c, option.project.SimpleName+" · "+option.project.ProjectKey, 10, p.muted).SingleLine().Tooltip(option.value)
								})
							})
							if row.Clicked() {
								state.pickerSelection = option.value
								s.submitMeegleProjectPickerSelection(state, option.project)
							}
						}
					})
				})
			}
			if state.pickerPendingSave != 0 {
				label(c, "正在添加…", 11, p.muted)
			}
			if state.pickerSaveError != "" {
				label(c, state.pickerSaveError, 11, p.danger).Wrap()
			}
		})
	})
	if state.pickerOpen && state.pickerFocusPending {
		focused := search.Focused()
		search.Focus()
		if focused {
			state.pickerFocusPending = false
		}
	}
	if !state.pickerOpen {
		s.closeMeegleProjectPicker()
	}
}
