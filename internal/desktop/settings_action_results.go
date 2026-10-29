package desktop

import (
	"silverwing-go/internal/application"
	"strings"
)

// applySettingsActionResult applies only settings feature results on the UI thread.
func (s *app) applySettingsActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "workspace.tools-options":
		if !settingsExtensionRead(action) {
			return false
		}
		s.applySettingsWorkspaceTools(action, result)
	case "config.materials-root.save":
		input := s.input("setting:requirementMaterialsRoot", "")
		if !s.settingsAutosaveInputPending(input) {
			*input = result.Path
		}
	case "config.task-root.save":
		*s.input("setting:taskRootTarget", "") = result.Path
		s.settingsUnit.state.migrationPlan, s.settingsUnit.state.migrationReady, s.settingsUnit.state.migrationPreview = nil, false, ""
	case "migration.preview":
		s.applyTaskRootMigrationPreview(action, result)
	case "migration.apply":
		s.settingsUnit.state.migrationPlan = nil
		s.settingsUnit.state.migrationReady = false
		s.settingsUnit.state.migrationPreview = firstNonEmpty(result.Message, "任务目录已迁移并切换。")
		if result.Path != "" {
			*s.input("setting:taskRootTarget", s.configString("taskRoot")) = result.Path
		}
		s.settingsUnit.state.settingsOutput = s.settingsUnit.state.migrationPreview
	case "migration.recover":
		s.settingsUnit.state.migrationPreview = firstNonEmpty(result.Message, "迁移恢复状态已读取。")
		s.settingsUnit.state.settingsOutput = ""
	case "config.backups":
		s.applySettingsConfigBackups(result.Data)
	case "config.view":
		s.settingsUnit.state.settingsConfigPath = result.Path
		s.settingsUnit.state.settingsConfigText = result.Text
		if s.settingsUnit.state.settingsConfigText == "" {
			s.settingsUnit.state.settingsConfigText = resultText(result)
		}
		s.settingsUnit.state.settingsConfigOpen = true
	case "config.save", "config.fields.save":
		if s.page == "settings" && s.settingsUnit.state.settingsPage == "dev-tools" {
			s.act("development-tools/snapshot", "", "", "", "", "", nil)
		}
	case "config.import-preview":
		s.applySettingsConfigPreview(result.Data)
	case "config.export":
		s.settingsUnit.state.settingsOutput = "配置已导出。"
		if result.Path != "" {
			s.act("system.reveal", "", "", "", result.Path, "", nil)
		}
	case "config.import":
		s.materialsUnit.state.importPreview = false
		s.settingsUnit.state.settingsOutput = "配置已导入。"
	case "config.restore-backup":
		s.settingsUnit.state.settingsOutput = "配置备份已恢复。"
	case "instructions.templates.list":
		s.settingsUnit.state.settingsTemplates = mapsFromData(result.Data)
		s.settingsUnit.state.settingsTemplatesLoaded = true
	case "instructions.templates.save", "instructions.templates.remove":
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("instructions.templates.list", "", "", "", "", "", nil)
	case "codex.extensions/snapshot":
		s.settingsUnit.state.settingsMarketplaces = mapsFromData(dataValue(result.Data, "marketplaces"))
		s.settingsUnit.state.settingsSkillSources = mapsFromData(dataValue(result.Data, "skillSources"))
		s.settingsUnit.state.settingsOutput = ""
	case "codex.extensions/marketplaces/add-source", "codex.extensions/marketplaces/refresh-source", "codex.extensions/marketplaces/remove-source":
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("codex.extensions/snapshot", "", "", "", "", "", nil)
	case "codex.extensions/plugin/install", "codex.extensions/plugin/uninstall":
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("codex.extensions/snapshot", "", "", "", "", "", nil)
	case "codex.extensions/mcp/status":
		s.settingsUnit.state.settingsMCPServers = mapsFromData(dataValue(result.Data, "servers"))
		s.settingsUnit.state.settingsMCPPath = mapString(mapData(result.Data), "userFile")
		s.settingsUnit.state.settingsMCPWarnings = mapsString(dataValue(result.Data, "warnings"))
		// A status read is config-only; do not leave older live connection results visible.
		s.settingsUnit.state.settingsMCPConnections = nil
		s.settingsUnit.state.settingsMCPConnectionError = mapString(mapData(result.Data), "connectionError")
		s.settingsUnit.state.settingsOutput = ""
	case "codex.extensions/mcp/check":
		// Connection checks return only live status and retain the configuration list.
		s.settingsUnit.state.settingsMCPConnections = mapData(dataValue(result.Data, "connections"))
		s.settingsUnit.state.settingsMCPConnectionError = mapString(mapData(result.Data), "connectionError")
		s.settingsUnit.state.settingsOutput = ""
	case "codex.extensions/mcp/add", "codex.extensions/mcp/edit", "codex.extensions/mcp/set", "codex.extensions/mcp/remove":
		if action.Kind == "codex.extensions/mcp/set" && !s.settingsMCPSwitchResultCurrent(action) {
			return true
		}
		s.settingsUnit.state.settingsMCPConnections = nil
		if snapshot := dataValue(result.Data, "snapshot"); snapshot != nil {
			s.settingsUnit.state.settingsMCPServers = mapsFromData(dataValue(snapshot, "servers"))
			s.settingsUnit.state.settingsMCPPath = mapString(mapData(snapshot), "userFile")
			s.settingsUnit.state.settingsMCPWarnings = mapsString(dataValue(snapshot, "warnings"))
		}
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
	case "codex.ai/mcp/authenticate":
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
	case "git.environment", "git.environment/snapshot":
		s.settingsUnit.state.settingsGitEnvironment = mapData(result.Data)
		s.settingsUnit.state.settingsOutput = ""
	case "development-tools/snapshot", "development-tools/detect":
		s.settingsUnit.state.settingsDevelopmentToolsSnapshot = mapData(result.Data)
		s.settingsUnit.state.settingsTerminalResolution = mapData(dataValue(result.Data, "terminalResolution"))
		s.settingsUnit.state.settingsOutput = ""
	case "development-tools/paths/existing":
		s.settingsUnit.state.settingsOutput = strings.Join(mapsString(dataValue(result.Data, "existingTypes")), "\n")
	case "terminal.resolve":
		s.settingsUnit.state.settingsTerminalResolution = mapData(result.Data)
		s.settingsUnit.state.settingsOutput = ""
	case "cli.auth.start", "cli.auth.session", "cli.auth.poll", "cli.auth.cancel":
		s.applyCLIAuthResult(action, result)
	case "cli.auth.logout":
		tool := strings.ToLower(payloadString(action.Payload, "tool"))
		if tool == "meegle" {
			s.clearMeegleSessionViews()
		}
		s.settingsUnit.stopCLIAuthPoll(tool)
		if s.settingsUnit.state.settingsCLISessions != nil {
			delete(s.settingsUnit.state.settingsCLISessions, tool)
		}
		if status := mapData(dataValue(result.Data, "status")); status != nil {
			if s.settingsUnit.state.settingsCLIStatus == nil {
				s.settingsUnit.state.settingsCLIStatus = make(map[string]map[string]any)
			}
			s.settingsUnit.state.settingsCLIStatus[tool] = status
			s.refreshWorkspaceGitDisplayContext()
		}
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("cli.status", "", "", "", "", "", map[string]any{"tool": tool})
	case "skills.sources/catalog":
		if sourceID := payloadString(action.Payload, "sourceId"); sourceID != "" {
			s.settingsUnit.state.settingsSkillSources = upsertSkillSourceStatus(s.settingsUnit.state.settingsSkillSources, sourceID, result.Data)
		} else {
			s.settingsUnit.state.settingsSkillSources = mapsFromData(dataValue(result.Data, "sources"))
		}
		s.settingsUnit.state.settingsOutput = ""
	case "skills.sources/save", "skills.sources/remove":
		s.settingsUnit.state.settingsOutput = result.Message
		s.act("skills.sources/catalog", "", "", "", "", "", nil)
	case "skills.catalog":
		if !s.settingsExtensionActionRelevant(action) {
			return true
		}
		s.settingsUnit.state.settingsLocalSkills = mapsFromData(dataValue(result.Data, "skills"))
		s.settingsUnit.state.settingsSkillRoot = mapString(mapData(result.Data), "root")
		s.settingsUnit.state.settingsSkillsCatalog = ""
	case "skills.files/list":
		if !s.settingsExtensionActionRelevant(action) {
			return true
		}
		s.settingsUnit.state.settingsSkillFiles = mapsFromData(dataValue(result.Data, "files"))
		s.settingsUnit.state.settingsSkillFilesDirectory = payloadString(action.Payload, "directoryName")
		s.settingsUnit.state.settingsSkillFilesTruncated = dataBool(result.Data, "truncated")
	case "skills.files/preview":
		s.applySettingsExtensionPreview(action, result)
	case "skills.preview":
		s.applySettingsExtensionPreview(action, result)
	case "skills.preview/path":
		s.settingsUnit.state.settingsOutput = resultText(result)
	case "skills.install", "skills.update", "skills.remove":
		s.settingsUnit.state.settingsOutput = result.Message
		s.invalidateLocalSkillsCatalog()
		s.act("skills.sources/catalog", "", "", "", "", "", nil)
	case "skills.sources/branches/list", "codex.extensions/branches/list":
		if !s.settingsExtensionActionRelevant(action) {
			return true
		}
		s.settingsUnit.state.settingsBranchChoices = mapsString(result.Data)
		ref := s.input("form:ref", "")
		if strings.TrimSpace(*ref) == "" {
			for _, preferred := range []string{"master", "main"} {
				for _, branch := range s.settingsUnit.state.settingsBranchChoices {
					if branch == preferred {
						*ref = branch
						break
					}
				}
				if *ref != "" {
					break
				}
			}
		}
		if s.settingsUnit.state.settingsExtensionRequests != nil {
			s.settingsUnit.state.settingsExtensionRequests.branchLoading = false
			s.settingsUnit.state.settingsExtensionRequests.branchError = ""
		}
	case "codex.extensions/plugin/preview", "codex.extensions/plugin/skill-preview":
		s.applySettingsExtensionPreview(action, result)
	case "cli.status", "proxy.test", "git.version", "diagnostics.export", "logs.list", "logs.read", "update.check":
		s.settingsUnit.state.settingsOutput = platformResultSummary(action.Kind, result)
		if action.Kind == "cli.status" {
			tool := strings.ToLower(payloadString(action.Payload, "tool"))
			if s.settingsUnit.state.settingsCLIStatus == nil {
				s.settingsUnit.state.settingsCLIStatus = make(map[string]map[string]any)
			}
			s.settingsUnit.state.settingsCLIStatus[tool] = mapData(result.Data)
			s.refreshWorkspaceGitDisplayContext()
		}
		if action.Kind == "logs.list" {
			s.settingsUnit.state.settingsLogEntries = mapsFromData(dataValue(result.Data, "entries"))
			if s.settingsUnit.state.settingsLogEntries == nil {
				s.settingsUnit.state.settingsLogEntries = mapsFromData(result.Data)
			}
			s.settingsUnit.state.settingsOutput = ""
		}
	default:
		return false
	}
	return true
}
