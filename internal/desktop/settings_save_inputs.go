package desktop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Keep cached controls bound to the configuration that was actually applied.
// An unrelated refresh must not replace an unsaved draft, and updating a value
// must preserve its pointer so the native input keeps its existing binding.
func (s *app) syncSettingsConfigInputs(previous, next string) {
	var before, after map[string]any
	_ = json.Unmarshal([]byte(previous), &before)
	if json.Unmarshal([]byte(next), &after) != nil || after == nil {
		return
	}
	// Native inputs can change before their next render observes the draft.
	// Capture those edits before a background refresh updates cached controls.
	if state := s.settingsAutosave; state != nil && state.home == s.snapshot.Home {
		for key, entry := range state.entries {
			if entry.latest != nil {
				if value := entry.latest(); value != entry.value {
					s.observeSettingsAutosave(key, value, entry.save, entry.latest)
				}
			}
		}
	}
	beforeText, beforeBool := settingsConfigInputValues(before)
	afterText, afterBool := settingsConfigInputValues(after)
	// Project drafts are indexed rows. A changed list replaces the whole row,
	// even when a reordered project's key or short name matches the old value.
	projectsChanged := !reflect.DeepEqual(before["meegleProjects"], after["meegleProjects"])
	projectsDraft := s.settingsAutosaveDraftPending("settings:meegle-projects")
	proxyDraft := s.settingsAutosaveDraftPending("settings:proxy-form")
	for key, draft := range s.inputs {
		projectRowChanged := projectsChanged && (strings.HasPrefix(key, "setting:meegle-project-key:") || strings.HasPrefix(key, "setting:meegle-project-name:"))
		if s.settingsAutosaveInputPending(draft) || projectRowChanged && projectsDraft || proxyDraft && strings.HasPrefix(key, "setting:proxy-") {
			continue
		}
		if draft == nil || (!projectRowChanged && reflect.DeepEqual(beforeText[key], afterText[key])) {
			continue
		}
		if !projectRowChanged && *draft != settingsConfigInputText(beforeText[key]) && *draft != settingsConfigInputText(afterText[key]) {
			continue
		}
		*draft = settingsConfigInputText(afterText[key])
	}
	for key, draft := range s.settingsBoolInputs {
		if proxyDraft && (strings.HasPrefix(key, "proxy-target:") || key == "local:proxy-auth-enabled") {
			continue
		}
		if draft == nil || reflect.DeepEqual(beforeBool[key], afterBool[key]) {
			continue
		}
		value, _ := afterBool[key].(bool)
		*draft = value
	}
	if !reflect.DeepEqual(before["showTaskAreaCopyIcons"], after["showTaskAreaCopyIcons"]) || !reflect.DeepEqual(before["showTaskAreaRequirementCopyIcons"], after["showTaskAreaRequirementCopyIcons"]) {
		if draft := s.settingsBoolInputs["showTaskAreaRequirementCopyIcons"]; draft != nil {
			legacy, legacyOK := after["showTaskAreaCopyIcons"].(bool)
			requirement, requirementOK := after["showTaskAreaRequirementCopyIcons"].(bool)
			*draft = (!legacyOK || legacy) && (!requirementOK || requirement)
		}
	}
	s.rebaseSettingsAutosaveInputs()
}

func settingsConfigInputValues(config map[string]any) (map[string]any, map[string]any) {
	text, booleans := map[string]any{}, map[string]any{}
	for key, value := range config {
		switch value.(type) {
		case string, float64, nil:
			text["setting:"+key] = value
		case bool:
			if key != "showTaskAreaRequirementCopyIcons" {
				booleans[key] = value
			}
		}
	}
	text["setting:taskRootTarget"] = config["taskRoot"]
	proxy := proxyParts(mapString(config, "commandProxyUrl"))
	for index, key := range []string{"scheme", "host", "port"} {
		text["setting:proxy-"+key] = proxy[index]
	}
	for field, key := range map[string]string{"commandProxyNoProxy": "no-proxy", "commandProxyUsername": "username", "commandProxyPassword": "password"} {
		text["setting:proxy-"+key] = config[field]
	}
	booleans["local:proxy-auth-enabled"] = mapString(config, "commandProxyUsername") != "" && mapString(config, "commandProxyPassword") != ""
	targets := stringArray(mustArray(config["commandProxyTargets"]))
	for _, target := range []string{"CODEX", "GIT", "WORKSPACE_COMMAND", "MEEGLE", "LARK"} {
		booleans["proxy-target:"+target] = containsString(targets, target)
	}
	visibility, _ := config["toolVisibility"].(map[string]any)
	for key, value := range visibility {
		booleans["tool-visibility-"+key] = value
	}
	for index, project := range mapsFromArray(mustArray(config["meegleProjects"])) {
		text[fmt.Sprintf("setting:meegle-project-key:%d", index)] = project["project_key"]
		text[fmt.Sprintf("setting:meegle-project-name:%d", index)] = project["simple_name"]
	}
	for _, tool := range mapsFromArray(mustArray(config["developmentTools"])) {
		text["setting:development-tool:"+mapString(tool, "type")] = tool["path"]
	}
	return text, booleans
}

func settingsConfigInputText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case float64:
		return fmt.Sprint(value)
	default:
		return ""
	}
}
