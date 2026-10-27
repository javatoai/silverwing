package desktop

import (
	"github.com/egoist/mygo/ui"
	"strings"
)

func (s *app) settingsConfigPathRow(c *ui.Context, p palette, key, title, placeholder string, directory bool) {
	value := s.input("setting:"+key, s.configString(key))
	s.settingsPathInput(c, p, title, placeholder, value, directory, func(path string) {
		s.saveSettingsCLIPath(key, path)
	})
}

func (s *app) settingsPathInput(c *ui.Context, p palette, title, placeholder string, value *string, directory bool, onChoose func(string)) {
	ui.Column(c).Gap(5).Children(func() {
		label(c, title, 11, p.text)
		s.settingsPathInputBody(c, p, title, placeholder, value, directory, onChoose, false)
	})
}

// settingsPathInputBody renders only the input and its actions so settings
// pages can place the field beside a separate title and description.
func (s *app) settingsPathInputBody(c *ui.Context, p palette, title, placeholder string, value *string, directory bool, onChoose func(string), stacked bool) {
	observe := func() {
		if onChoose != nil {
			s.autosaveSettingsInput("path:"+title, value, onChoose)
		}
	}
	observe()
	input := func() {
		ui.TextInput(c.Key("settings-path-"+title), value).Grow(1).MinWidth(0).FillWidth().Height(36).Placeholder(placeholder).OnChange(observe).OnSubmit(func() { s.flushSettingsAutosaves(true) })
	}
	browse := func() {
		if iconButton(c, "settings-browse-"+title, "folder", "浏览 · "+title, p).Clicked() {
			s.pickNativePath(placeholder, *value, directory, nil, func(path string) {
				*value = path
				observe()
			})
		}
	}
	if stacked {
		ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
			input()
			ui.Row(c).FillWidth().Gap(7).Justify(ui.End).Children(func() {
				browse()
			})
		})
		return
	}
	ui.Row(c).FillWidth().MinWidth(0).Gap(7).Children(func() {
		input()
		browse()
	})
}

// Ordinary settings save after typing pauses, using the existing validation.
func (s *app) settingsTextInputBody(c *ui.Context, p palette, key, placeholder string, multiline bool, save func(string), stacked bool) {
	value := s.input("setting:"+key, s.configString(key))
	observe := func() { s.autosaveSettingsInput(settingsAutosaveFieldKey(key), value, save) }
	observe()
	input := func() {
		if multiline {
			ui.TextArea(c.Key("settings-text-"+key), value).Grow(1).MinWidth(0).FillWidth().Height(100).Padding(10).Background(p.surface).Border(1, p.line).Radius(8).OnChange(observe)
			return
		}
		ui.TextInput(c.Key("settings-text-"+key), value).Grow(1).MinWidth(0).FillWidth().Height(36).Placeholder(placeholder).OnChange(observe).OnSubmit(func() { s.flushSettingsAutosaves(true) })
	}
	if stacked {
		ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
			input()
		})
		return
	}
	ui.Row(c).FillWidth().MinWidth(0).Gap(7).AlignItems(ui.Center).Children(func() {
		input()
	})
}

func (s *app) settingsTextRow(c *ui.Context, p palette, key, title, placeholder string, multiline bool, save func(string)) {
	ui.Column(c).Gap(5).Children(func() {
		label(c, title, 11, p.text)
		s.settingsTextInputBody(c, p, key, placeholder, multiline, save, false)
	})
}

func (s *app) settingsChoice(c *ui.Context, p palette, key, title string, selected bool, choose func()) {
	b := ui.ButtonBase(c.Key("choice-"+key)).Height(32).Padding(0, 12).Radius(17).Label(title)
	fg := p.muted
	if selected {
		b.Background(p.selected)
		fg = p.blue
	} else {
		b.Border(1, p.line)
	}
	b.Children(func() { label(c, title, 11, fg).NoWrap() })
	if b.Clicked() && !selected {
		choose()
	}
}

func (s *app) settingsSwitch(c *ui.Context, p palette, key, title, description string, fallback bool, changed ...func(bool)) {
	current := s.configBool(key, fallback)
	value, pending := s.settingsConfigSwitchInput(key, current)
	settingsConfigSwitchRow(c, p, key, title, description, value, pending, func() {
		if len(changed) > 0 {
			changed[0](*value)
		} else {
			s.saveConfigField(key, *value)
		}
	})
}

func (s *app) settingsNestedSwitch(c *ui.Context, p palette, objectKey, fieldKey, key, title, description string, fallback bool) {
	current := s.configNestedBool(objectKey, fieldKey, fallback)
	value, pending := s.settingsConfigSwitchInput(key, current)
	settingsConfigSwitchRow(c, p, key, title, description, value, pending, func() {
		s.updateConfigObjectField(objectKey, fieldKey, *value)
	})
}

func (s *app) settingsGroupSwitch(c *ui.Context, p palette, groupID, fieldKey, key, title, description string, fallback bool) {
	config, err := s.readSettingsConfig()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	current := fallback
	if groups, ok := config["groups"].([]any); ok {
		for _, raw := range groups {
			group, _ := raw.(map[string]any)
			if mapString(group, "id") == groupID {
				if value, ok := group[fieldKey].(bool); ok {
					current = value
				}
				break
			}
		}
	}
	value, pending := s.settingsConfigSwitchInput(key, current)
	settingsConfigSwitchRow(c, p, key, title, description, value, pending, func() {
		if err := s.updateConfigGroup(groupID, func(group map[string]any) { group[fieldKey] = *value }); err != nil {
			s.lastError = err.Error()
		}
	})
}

func (s *app) settingsLocalSwitch(c *ui.Context, p palette, key, title, description string, value *bool) {
	old := *value
	settingsSwitchRow(c, p, key, title, description, value)
	s.persistLocalSwitchChange(key, old, value)
}

func (s *app) settingsLocalSwitchPlain(c *ui.Context, p palette, key, title, description string, value *bool) {
	old := *value
	settingsPlainSwitchRow(c, p, key, title, description, value)
	s.persistLocalSwitchChange(key, old, value)
}

func (s *app) persistLocalSwitchChange(key string, old bool, value *bool) {
	if old != *value {
		if key == "workspace-navigation-collapsed" {
			if err := s.saveNavigationPreference(); err != nil {
				*value = old
				s.lastError = err.Error()
			}
		}
		if s.win != nil {
			s.win.Invalidate()
		}
	}
}

func settingsSwitchRow(c *ui.Context, p palette, key, title, description string, value *bool) {
	settingsSwitchRowWithControl(c, p, title, description, func() {
		ui.Switch(c.Key("settings-switch-"+key), value).Label(title)
	})
}

func settingsConfigSwitchRow(c *ui.Context, p palette, key, title, description string, value *bool, pending bool, onChange func()) {
	settingsSwitchRowWithControl(c, p, title, description, func() {
		ui.Switch(c.Key("settings-switch-"+key), value).Label(title).Disabled(pending).OnChange(onChange)
	})
}

func settingsSwitchRowWithControl(c *ui.Context, p palette, title, description string, control func()) {
	ui.Row(c).FillWidth().Padding(11, 13).Gap(14).AlignItems(ui.Center).Background(p.soft).Radius(9).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			settingsHelpLabel(c, p, title, description, 12, 500)
		})
		control()
	})
}

func settingsPlainSwitchRow(c *ui.Context, p palette, key, title, description string, value *bool) {
	ui.Row(c).FillWidth().MinHeight(48).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			settingsHelpLabel(c, p, title, description, 14, 600)
		})
		ui.Switch(c.Key("settings-switch-"+key), value).Width(52).Height(32).Radius(16).Label(title)
	})
}

func (s *app) settingsBoolInput(key string, initial bool) *bool {
	if s.settingsBoolInputs == nil {
		s.settingsBoolInputs = make(map[string]*bool)
	}
	if value, ok := s.settingsBoolInputs[key]; ok {
		return value
	}
	value := initial
	s.settingsBoolInputs[key] = &value
	return &value
}

func (s *app) settingsConfigSwitchInput(key string, current bool) (*bool, bool) {
	value := s.settingsBoolInput(key, current)
	pending := s.busy && s.operationKind == "config.fields.save"
	if !pending {
		*value = current
	}
	return value, pending
}

func (s *app) localSettingBool(key string, initial bool) *bool {
	return s.settingsBoolInput("local:"+key, initial)
}

func (s *app) developmentToolPath(kind string) string {
	for _, item := range mapsFromArray(s.configArray("developmentTools")) {
		if strings.EqualFold(mapString(item, "type"), kind) {
			return mapString(item, "path")
		}
	}
	return ""
}

func (s *app) saveDevelopmentToolPath(kind, value string) {
	items := mapsFromArray(s.configArray("developmentTools"))
	found := false
	for _, item := range items {
		if strings.EqualFold(mapString(item, "type"), kind) {
			found = true
			if strings.TrimSpace(value) == "" {
				items = removeMapValue(items, item)
			} else {
				item["path"] = strings.TrimSpace(value)
			}
			break
		}
	}
	if !found && strings.TrimSpace(value) != "" {
		items = append(items, map[string]any{"type": kind, "path": strings.TrimSpace(value)})
	}
	s.saveConfigField("developmentTools", items)
}
