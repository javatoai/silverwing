package desktop

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

type serviceModuleCommandsDraft struct {
	commands []serviceCommandValues
	selected int // -1 shows the list.
}

func (s *app) readServiceModuleCommandsDraft(module *serviceModuleDraft) serviceModuleCommandsDraft {
	draft := serviceModuleCommandsDraft{commands: make([]serviceCommandValues, len(module.commands)), selected: -1}
	for index, command := range module.commands {
		draft.commands[index] = s.readServiceModuleCommandValues(module.id, command)
	}
	return draft
}

func serviceModuleCommandValues(command workspaceCommandDraft) serviceCommandValues {
	return serviceCommandValues{
		raw: command.raw, ID: command.id, Name: command.name, Program: command.executable,
		ArgsText: command.arguments, Directory: firstNonEmpty(command.directory, "."),
		TimeoutText: firstNonEmpty(command.timeout, "600"), IconKey: command.iconKey,
		Enabled: mapBool(command.raw, "enabled", true),
	}
}

func serviceModuleCommandTextFields(values *serviceCommandValues) [6]struct {
	key   string
	value *string
} {
	return [6]struct {
		key   string
		value *string
	}{
		{"name", &values.Name}, {"executable", &values.Program}, {"iconKey", &values.IconKey},
		{"arguments", &values.ArgsText}, {"directory", &values.Directory}, {"timeout", &values.TimeoutText},
	}
}

func (s *app) readServiceModuleCommandValues(moduleID string, command workspaceCommandDraft) serviceCommandValues {
	values := serviceModuleCommandValues(command)
	values.raw = cloneConfigMap(values.raw)
	prefix := "module:" + moduleID + ":" + command.id + ":"
	// Read active edits without creating entries in the outer editor's input cache.
	for _, field := range serviceModuleCommandTextFields(&values) {
		if cached := s.inputs[s.serviceEditorKey(prefix+field.key)]; cached != nil {
			*field.value = *cached
		}
	}
	if cached := s.settingsUnit.state.settingsBoolInputs[s.serviceEditorKey(prefix+"enabled")]; cached != nil {
		values.Enabled = *cached
	}
	return values
}

func (s *app) applyServiceModuleCommandsDraft(module *serviceModuleDraft, draft *serviceModuleCommandsDraft) error {
	commands := make([]workspaceCommandDraft, len(draft.commands))
	seen := make(map[string]bool, len(draft.commands))
	for index, values := range draft.commands {
		timeout, err := validateServiceCommandValues(values, serviceCommandForModule)
		if err != nil {
			draft.selected = index
			return err
		}
		idKey := strings.ToLower(values.ID)
		if seen[idKey] {
			draft.selected = index
			return fmt.Errorf("同一模块内快捷命令 ID 不能重复（忽略大小写）")
		}
		seen[idKey] = true
		commands[index] = workspaceCommandDraft{
			raw: serviceCommandConfig(values, serviceCommandForModule, timeout), id: values.ID,
			name: values.Name, executable: values.Program, iconKey: values.IconKey, arguments: values.ArgsText,
			directory: values.Directory, timeout: values.TimeoutText,
		}
	}

	previous := make(map[string]workspaceCommandDraft, len(module.commands))
	for _, command := range module.commands {
		previous[command.id] = command
	}
	owner := "module:" + module.id + ":"
	for index, values := range draft.commands {
		original, exists := previous[values.ID]
		if !exists {
			original = commands[index]
		}
		initial := serviceModuleCommandValues(original)
		prefix := owner + values.ID + ":"
		initialFields := serviceModuleCommandTextFields(&initial)
		for fieldIndex, field := range serviceModuleCommandTextFields(&values) {
			*s.serviceEditorText(prefix+field.key, *initialFields[fieldIndex].value) = *field.value
		}
		*s.serviceEditorBool(prefix+"enabled", initial.Enabled) = values.Enabled
		delete(previous, values.ID)
	}
	for id, command := range previous {
		values := serviceModuleCommandValues(command)
		for _, field := range serviceModuleCommandTextFields(&values) {
			key := s.serviceEditorKey(owner + id + ":" + field.key)
			delete(s.inputs, key)
			delete(s.workspaceUnit.state.serviceEditor.initialText, key)
		}
		key := s.serviceEditorKey(owner + id + ":enabled")
		delete(s.settingsUnit.state.settingsBoolInputs, key)
		delete(s.workspaceUnit.state.serviceEditor.initialBools, key)
	}
	module.commands = commands
	return nil
}

func (s *app) renderServiceModuleCommandsPopup(c *ui.Context, p palette, popup *serviceModulePopup) {
	draft := &popup.commands
	if draft.selected >= 0 && draft.selected < len(draft.commands) {
		s.renderServiceModuleCommandForm(c, p, popup)
		return
	}
	var add bool
	ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
		add = action(c, popup.key+"-add-command", "plus", "添加命令", p, false)
	})
	if add {
		draft.commands = append(draft.commands, serviceCommandValues{
			ID: newDesktopEditorID("command"), Name: "Maven 命令", Program: "mvn", IconKey: "maven",
			Directory: ".", TimeoutText: "1800", Enabled: true,
		})
		draft.selected, popup.err = len(draft.commands)-1, ""
		c.Invalidate()
		return
	}
	if len(draft.commands) == 0 {
		label(c, "暂无快捷命令", 12, p.muted)
	}
	for index, command := range draft.commands {
		name := firstNonEmpty(command.Name, "未命名命令")
		key := fmt.Sprintf("%s-command-row-%d-%s", popup.key, index, command.ID)
		row := ui.ButtonBase(c.Key(key)).Row().FillWidth().MinWidth(0).Height(40).Padding(0, 10).
			Gap(8).AlignItems(ui.Center).Radius(8).Border(1, p.line).Label(name).Tooltip(name)
		if row.Hovered() {
			row.Background(p.soft)
		}
		status, color := "已停用", p.muted
		if command.Enabled {
			status, color = "已启用", p.green
		}
		row.Children(func() {
			label(c, name, 13, p.text).Grow(1).MinWidth(0).SingleLine()
			label(c, status, 11, color).Shrink(0).NoWrap()
			icon(c, "chevron-right", 15, p.muted)
		})
		if row.Clicked() {
			draft.selected, popup.err = index, ""
			c.Invalidate()
		}
	}
}

func (s *app) renderServiceModuleCommandForm(c *ui.Context, p palette, popup *serviceModulePopup) {
	draft := &popup.commands
	index := draft.selected
	command := &draft.commands[index]
	key := fmt.Sprintf("%s-command-form-%d-%s", popup.key, index, command.ID)
	var back, remove bool
	ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
		back = action(c, key+"-back", "chevron-left", "返回列表", p, false)
		remove = action(c, key+"-remove", "delete", "删除快捷命令", p, false)
	})
	if back || remove {
		if remove {
			draft.commands = slices.Delete(draft.commands, index, index+1)
		}
		draft.selected, popup.err = -1, ""
		c.Invalidate()
		return
	}
	serviceModuleCommandField(c, p, key+"-name", "命令名称", &command.Name)
	serviceModuleCommandField(c, p, key+"-executable", "可执行程序", &command.Program)
	ui.Column(c).Gap(4).Children(func() {
		label(c, "图标", 10, p.muted)
		icons := []string{"build", "maven", "maven-clean", "maven-install", "maven-deploy", "play", "terminal", "package", "upload", "refresh", "code", "database", "settings"}
		selected := firstNonEmpty(command.IconKey, "build")
		if ui.Select(c.Key(key+"-icon"), &selected, icons).FillWidth().MinWidth(0).Height(36).Label("图标").Changed() {
			command.IconKey = selected
		}
	})
	const argsLabel = "参数（每行一个；不经过 Shell）"
	label(c, argsLabel, 10, p.muted)
	ui.TextArea(c.Key(key+"-arguments"), &command.ArgsText).Label(argsLabel).FillWidth().Height(76).Padding(9).Background(p.surface).Border(1, p.line).Radius(7)
	serviceModuleCommandField(c, p, key+"-directory", "工作目录", &command.Directory)
	serviceModuleCommandField(c, p, key+"-timeout", "超时（秒）", &command.TimeoutText)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, "启用", 11, p.text).Grow(1)
		ui.Switch(c.Key(key+"-enabled"), &command.Enabled).Label("启用")
	})
	preview := strings.Join(append([]string{command.Program}, nonBlankLines(command.ArgsText)...), " ")
	label(c, "预览："+preview, 10, p.muted).SingleLine().Tooltip(preview)
}

func serviceModuleCommandField(c *ui.Context, p palette, key, title string, value *string) {
	ui.Column(c).Gap(4).Children(func() {
		label(c, title, 10, p.muted)
		ui.TextInput(c.Key(key), value).FillWidth().Height(36).Label(title).Placeholder(title)
	})
}
