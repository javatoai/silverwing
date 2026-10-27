package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func decodeWithDefaults[T any](data []byte, defaults T, required ...string) (T, error) {
	value := defaults
	if err := DecodeStrict(data, &value, required...); err != nil {
		return value, err
	}
	return value, nil
}

func (v *ThemePreference) UnmarshalJSON(data []byte) error {
	x, e := decodeEnum(data, "SYSTEM", "LIGHT", "DARK")
	*v = ThemePreference(x)
	return e
}

func (v *CommandProxyTarget) UnmarshalJSON(data []byte) error {
	x, e := decodeEnum(data, "CODEX", "GIT", "MEEGLE", "LARK")
	*v = CommandProxyTarget(x)
	return e
}
func (v *TaskLifecycleStatus) UnmarshalJSON(data []byte) error {
	x, e := decodeEnum(data, "ACTIVE", "ARCHIVED")
	*v = TaskLifecycleStatus(x)
	return e
}

func (v *RequirementMaterialsStatus) UnmarshalJSON(data []byte) error {
	x, e := decodeEnum(data, "NOT_REQUESTED", "READY", "FAILED")
	*v = RequirementMaterialsStatus(x)
	return e
}

func (v *AgentDocumentLayout) UnmarshalJSON(data []byte) error {
	x, e := decodeEnum(data, "INLINE_V1", "REFERENCED_V2")
	*v = AgentDocumentLayout(x)
	return e
}
func (v *WorkspaceToolLaunchStatus) UnmarshalJSON(data []byte) error {
	x, e := decodeEnum(data, "PENDING", "OPENED", "FAILED")
	*v = WorkspaceToolLaunchStatus(x)
	return e
}

func (v *MeegleProjectConfig) UnmarshalJSON(data []byte) error {
	type plain MeegleProjectConfig
	x, e := decodeWithDefaults(data, plain{}, "project_key", "simple_name")
	*v = MeegleProjectConfig(x)
	return e
}
func (v *CodexPluginMarketplaceSource) UnmarshalJSON(data []byte) error {
	type plain CodexPluginMarketplaceSource
	x, e := decodeWithDefaults(data, plain{MarketplaceDirectory: "plugins"}, "id", "name", "repositoryUrl")
	*v = CodexPluginMarketplaceSource(x)
	return e
}
func (v *SkillSource) UnmarshalJSON(data []byte) error {
	type plain SkillSource
	x, e := decodeWithDefaults(data, plain{}, "id", "name", "repositoryUrl")
	*v = SkillSource(x)
	return e
}

func (v *GroupConfig) UnmarshalJSON(data []byte) error {
	type plain GroupConfig
	x, e := decodeWithDefaults(data, plain{}, "id", "name")
	*v = GroupConfig(x)
	return e
}
func (v *ToolVisibilitySettings) UnmarshalJSON(data []byte) error {
	type plain ToolVisibilitySettings
	x, e := decodeWithDefaults(data, plain{JSON: true, DateTime: true, TextDiff: true, KillPort: true})
	*v = ToolVisibilitySettings(x)
	return e
}
func (v *SidebarVisibilitySettings) UnmarshalJSON(data []byte) error {
	type plain SidebarVisibilitySettings
	x, e := decodeWithDefaults(data, plain{Requirements: true, Tools: true})
	*v = SidebarVisibilitySettings(x)
	return e
}
func (v *AppConfig) UnmarshalJSON(data []byte) error {
	type plain AppConfig
	x, e := decodeWithDefaults(data, plain(DefaultAppConfig()), "edition")
	if e == nil {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(data, &fields)
		if _, present := fields["commandProxyTargets"]; !present {
			x.CommandProxyTargets = DefaultCommandProxyTargets(x.CommandProxyURL)
		}
	}
	*v = AppConfig(x)
	return e
}
func (v *RequirementMaterialsDirectory) UnmarshalJSON(data []byte) error {
	type plain RequirementMaterialsDirectory
	x, e := decodeWithDefaults(data, plain{Status: MaterialsNotRequested})
	*v = RequirementMaterialsDirectory(x)
	return e
}

func (v *TaskManifest) UnmarshalJSON(data []byte) error {
	type plain TaskManifest
	x, e := decodeWithDefaults(data, plain(DefaultTaskManifest()), "edition", "folderName", "taskDirectoryName", "createdAt", "updatedAt")
	*v = TaskManifest(x)
	return e
}
func (v *AgentTaskContext) UnmarshalJSON(data []byte) error {
	type plain AgentTaskContext
	x, e := decodeWithDefaults(data, plain{ProtocolVersion: "2", HandoffRelativePath: ".workspace/HANDOFF.md"}, "protocolVersion", "documentationDirectory")
	if e == nil {
		e = AgentTaskContext(x).Validate()
	}
	*v = AgentTaskContext(x)
	return e
}
func (v *WorkspaceToolLaunch) UnmarshalJSON(data []byte) error {
	type plain WorkspaceToolLaunch
	x, e := decodeWithDefaults(data, plain{}, "toolId", "status", "updatedAt")
	*v = WorkspaceToolLaunch(x)
	return e
}

func (v *AgentTaskTemplate) UnmarshalJSON(data []byte) error {
	type plain AgentTaskTemplate
	// The user-editable template library uses ignoreUnknownKeys in Source,
	// unlike the strict AppConfig shards. Its four fields remain required.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"id", "name", "content", "updatedAt"} {
		raw, exists := fields[name]
		if !exists {
			return fmt.Errorf("missing required field %q", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("null is not valid for template field %q", name)
		}
	}
	var x plain
	if err := json.Unmarshal(data, &x); err != nil {
		return err
	}
	if strings.TrimSpace(x.Name) == "" {
		return fmt.Errorf("模板名称不能为空")
	}
	if strings.TrimSpace(x.Content) == "" {
		return fmt.Errorf("模板内容不能为空")
	}
	*v = AgentTaskTemplate(x)
	return nil
}
