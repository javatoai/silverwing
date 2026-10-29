package desktop

import (
	"time"
)

// settingsController owns this feature's state. Its zero value is usable in headless tests.
type settingsController struct {
	state        settingsState
	previewFiles localPreviewFileResolver
}

type settingsState struct {
	configJSON                  string
	configReads                 desktopConfigReadCache
	autoSync                    bool
	notifications               bool
	settingsTab                 string
	settingsPage                string
	settingsLoaded              string
	settingsOutput              string
	settingsConfigOpen          bool
	settingsConfigText          string
	settingsConfigPath          string
	settingsBackups             []map[string]any
	settingsConfigRecovery      *settingsConfigRecoveryState
	settingsTemplates           []map[string]any
	settingsTemplatesLoaded     bool
	settingsMarketplaces        []map[string]any
	settingsAgentScope          string
	settingsAgentGroupID        string
	settingsAgentContent        string
	settingsAgentPath           string
	settingsAgentLoaded         bool
	settingsAgentContext        string
	settingsSkillsCatalog       string
	settingsExtensions          []map[string]any
	settingsMCPServers          []map[string]any
	settingsMCPPath             string
	settingsMCPWarnings         []string
	settingsMCPTaskScope        bool
	settingsSkillSources        []map[string]any
	settingsLocalSkills         []map[string]any
	settingsSkillRoot           string
	settingsSkillPreviewOpen    bool
	settingsSkillPreviewName    string
	settingsSkillPreviewText    string
	settingsSkillFiles          []map[string]any
	settingsSkillFilesDirectory string
	settingsSkillFilesTruncated bool
	settingsBranchChoices       []string
	settingsExtensionRequests   *settingsExtensionRequestState
	settingsLogEntries          []map[string]any
	settingsMCPConnections      map[string]any
	settingsMCPConnectionError  string
	settingsCLIStatus           map[string]map[string]any
	meegleProjectCatalog        *meegleProjectCatalogState
	skillsBrowser               *skillsBrowserState
	settingsCLISessions         map[string]map[string]any
	settingsAuthPollTimers      map[string]*time.Timer
	settingsTerminalResolution  map[string]any
	settingsGroupID             string
	settingsPickerBusy          bool
	settingsBoolInputs          map[string]*bool
	migrationPlan               any
	migrationPreview            string
	migrationReady              bool
	taskRootChange              *taskRootChangeRequest
	pageLocalRead               *pageLocalReadState
	settingsAutosave            *settingsAutosaveState
}

func (s *settingsController) stopFeature() {
	s.stopSettingsAutosave()
	s.stopPageLocalRead()
}

func (s *settingsController) drainFeature() error {
	err := runFeatureCleanup(
		featureCleanupStep{"退出前本机配置读取清理未完成", 10 * time.Second, s.closePageLocalRead},
		featureCleanupStep{"", 30 * time.Second, s.closeSkillsBrowserReads},
		featureCleanupStep{"退出前设置扩展读取清理未完成", 10 * time.Second, s.closeSettingsExtensionReads},
		featureCleanupStep{"退出前配置读取清理未完成", 10 * time.Second, s.closeSettingsConfigReads},
	)
	if state := s.state.settingsExtensionRequests; state != nil {
		closeMarkdownPreviewState(state.previewMarkdown)
	}
	if state := s.state.skillsBrowser; state != nil {
		closeMarkdownPreviewState(state.markdown)
	}
	for tool := range s.state.settingsAuthPollTimers {
		s.stopCLIAuthPoll(tool)
	}
	return err
}
