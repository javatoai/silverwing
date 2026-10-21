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
		s.migrationPlan, s.migrationReady, s.migrationPreview = nil, false, ""
	case "migration.preview":
		s.applyTaskRootMigrationPreview(action, result)
	case "migration.apply":
		s.migrationPlan = nil
		s.migrationReady = false
		s.migrationPreview = firstNonEmpty(result.Message, "任务目录已迁移并切换。")
		if result.Path != "" {
			*s.input("setting:taskRootTarget", s.configString("taskRoot")) = result.Path
		}
		s.settingsOutput = s.migrationPreview
	case "migration.recover":
		s.migrationPreview = firstNonEmpty(result.Message, "迁移恢复状态已读取。")
		s.settingsOutput = ""
	case "config.backups":
		s.applySettingsConfigBackups(result.Data)
	case "config.view":
		s.settingsConfigPath = result.Path
		s.settingsConfigText = result.Text
		if s.settingsConfigText == "" {
			s.settingsConfigText = resultText(result)
		}
		s.settingsConfigOpen = true
	case "config.save", "config.fields.save":
		if s.page == "settings" && s.settingsPage == "dev-tools" {
			s.act("development-tools/snapshot", "", "", "", "", "", nil)
		}
	case "config.import-preview":
		s.applySettingsConfigPreview(result.Data)
	case "config.export":
		s.settingsOutput = "配置已导出。"
		if result.Path != "" {
			s.act("system.reveal", "", "", "", result.Path, "", nil)
		}
	case "config.import":
		s.importPreview = false
		s.settingsOutput = "配置已导入。"
	case "config.restore-backup":
		s.settingsOutput = "配置备份已恢复。"
	case "instructions.templates.list":
		s.settingsTemplates = mapsFromData(result.Data)
		s.settingsTemplatesLoaded = true
	case "instructions.templates.save", "instructions.templates.remove":
		s.settingsOutput = result.Message
		s.act("instructions.templates.list", "", "", "", "", "", nil)
	case "codex.extensions/snapshot":
		s.settingsMarketplaces = mapsFromData(dataValue(result.Data, "marketplaces"))
		s.settingsSkillSources = mapsFromData(dataValue(result.Data, "skillSources"))
		s.settingsOutput = ""
	case "codex.extensions/marketplaces/add-source", "codex.extensions/marketplaces/refresh-source", "codex.extensions/marketplaces/remove-source":
		s.settingsOutput = result.Message
		s.act("codex.extensions/snapshot", "", "", "", "", "", nil)
	case "codex.extensions/plugin/install", "codex.extensions/plugin/uninstall":
		s.settingsOutput = result.Message
		s.act("codex.extensions/snapshot", "", "", "", "", "", nil)
	case "codex.extensions/mcp/status":
		s.settingsMCPServers = mapsFromData(dataValue(result.Data, "servers"))
		s.settingsMCPPath = mapString(mapData(result.Data), "userFile")
		s.settingsMCPWarnings = mapsString(dataValue(result.Data, "warnings"))
		// A status read is config-only; do not leave older live connection results visible.
		s.settingsMCPConnections = nil
		s.settingsMCPConnectionError = mapString(mapData(result.Data), "connectionError")
		s.settingsOutput = ""
	case "codex.extensions/mcp/check":
		// Connection checks return only live status and retain the configuration list.
		s.settingsMCPConnections = mapData(dataValue(result.Data, "connections"))
		s.settingsMCPConnectionError = mapString(mapData(result.Data), "connectionError")
		s.settingsOutput = ""
	case "codex.extensions/mcp/add", "codex.extensions/mcp/edit", "codex.extensions/mcp/set", "codex.extensions/mcp/remove":
		if action.Kind == "codex.extensions/mcp/set" && !s.settingsMCPSwitchResultCurrent(action) {
			return true
		}
		s.settingsMCPConnections = nil
		if snapshot := dataValue(result.Data, "snapshot"); snapshot != nil {
			s.settingsMCPServers = mapsFromData(dataValue(snapshot, "servers"))
			s.settingsMCPPath = mapString(mapData(snapshot), "userFile")
			s.settingsMCPWarnings = mapsString(dataValue(snapshot, "warnings"))
		}
		s.settingsOutput = result.Message
		s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
	case "codex.ai/mcp/authenticate":
		s.settingsOutput = result.Message
		s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
	case "git.environment", "git.environment/snapshot":
		s.settingsGitEnvironment = mapData(result.Data)
		s.settingsOutput = ""
	case "development-tools/snapshot", "development-tools/detect":
		s.settingsDevelopmentToolsSnapshot = mapData(result.Data)
		s.settingsTerminalResolution = mapData(dataValue(result.Data, "terminalResolution"))
		s.settingsOutput = ""
	case "development-tools/paths/existing":
		s.settingsOutput = strings.Join(mapsString(dataValue(result.Data, "existingTypes")), "\n")
	case "terminal.resolve":
		s.settingsTerminalResolution = mapData(result.Data)
		s.settingsOutput = ""
	case "cli.auth.start", "cli.auth.session", "cli.auth.poll", "cli.auth.cancel":
		s.applyCLIAuthResult(action, result)
	case "cli.auth.logout":
		tool := strings.ToLower(payloadString(action.Payload, "tool"))
		if tool == "meegle" {
			s.clearMeegleSessionViews()
		}
		s.stopCLIAuthPoll(tool)
		if s.settingsCLISessions != nil {
			delete(s.settingsCLISessions, tool)
		}
		if status := mapData(dataValue(result.Data, "status")); status != nil {
			if s.settingsCLIStatus == nil {
				s.settingsCLIStatus = make(map[string]map[string]any)
			}
			s.settingsCLIStatus[tool] = status
			s.refreshWorkspaceGitDisplayContext()
		}
		s.settingsOutput = result.Message
		s.act("cli.status", "", "", "", "", "", map[string]any{"tool": tool})
	case "skills.sources/catalog":
		if sourceID := payloadString(action.Payload, "sourceId"); sourceID != "" {
			s.settingsSkillSources = upsertSkillSourceStatus(s.settingsSkillSources, sourceID, result.Data)
		} else {
			s.settingsSkillSources = mapsFromData(dataValue(result.Data, "sources"))
		}
		s.settingsOutput = ""
	case "skills.sources/save", "skills.sources/remove":
		s.settingsOutput = result.Message
		s.act("skills.sources/catalog", "", "", "", "", "", nil)
	case "skills.catalog":
		if !s.settingsExtensionActionRelevant(action) {
			return true
		}
		s.settingsLocalSkills = mapsFromData(dataValue(result.Data, "skills"))
		s.settingsSkillRoot = mapString(mapData(result.Data), "root")
		s.settingsSkillsCatalog = ""
	case "skills.files/list":
		if !s.settingsExtensionActionRelevant(action) {
			return true
		}
		s.settingsSkillFiles = mapsFromData(dataValue(result.Data, "files"))
		s.settingsSkillFilesDirectory = payloadString(action.Payload, "directoryName")
		s.settingsSkillFilesTruncated = dataBool(result.Data, "truncated")
	case "skills.files/preview":
		s.applySettingsExtensionPreview(action, result)
	case "skills.preview":
		s.applySettingsExtensionPreview(action, result)
	case "skills.preview/path":
		s.settingsOutput = resultText(result)
	case "skills.install", "skills.update", "skills.remove":
		s.settingsOutput = result.Message
		s.invalidateLocalSkillsCatalog()
		s.act("skills.sources/catalog", "", "", "", "", "", nil)
	case "skills.sources/branches/list", "codex.extensions/branches/list":
		if !s.settingsExtensionActionRelevant(action) {
			return true
		}
		s.settingsBranchChoices = mapsString(result.Data)
		ref := s.input("form:ref", "")
		if strings.TrimSpace(*ref) == "" {
			for _, preferred := range []string{"master", "main"} {
				for _, branch := range s.settingsBranchChoices {
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
		if s.settingsExtensionRequests != nil {
			s.settingsExtensionRequests.branchLoading = false
			s.settingsExtensionRequests.branchError = ""
		}
	case "codex.extensions/plugin/preview", "codex.extensions/plugin/skill-preview":
		s.applySettingsExtensionPreview(action, result)
	case "cli.status", "proxy.test", "git.version", "diagnostics.export", "logs.list", "logs.read", "update.check":
		s.settingsOutput = platformResultSummary(action.Kind, result)
		if action.Kind == "cli.status" {
			tool := strings.ToLower(payloadString(action.Payload, "tool"))
			if s.settingsCLIStatus == nil {
				s.settingsCLIStatus = make(map[string]map[string]any)
			}
			s.settingsCLIStatus[tool] = mapData(result.Data)
			s.refreshWorkspaceGitDisplayContext()
		}
		if action.Kind == "logs.list" {
			s.settingsLogEntries = mapsFromData(dataValue(result.Data, "entries"))
			if s.settingsLogEntries == nil {
				s.settingsLogEntries = mapsFromData(result.Data)
			}
			s.settingsOutput = ""
		}
	default:
		return false
	}
	return true
}
