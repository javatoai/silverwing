package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/asyncjob"
	"silverwing-go/internal/platform"
)

// settingsController owns this feature's state. Its zero value is usable in headless tests.
type settingsController struct {
	state        settingsState
	previewFiles localPreviewFileResolver
}

type settingsState struct {
	configJSON                       string
	configReads                      desktopConfigReadCache
	autoSync                         bool
	notifications                    bool
	settingsTab                      string
	settingsLastPages                map[string]string
	settingsSearchQuery              string
	settingsSearchTarget             string
	settingsSearchPending            bool
	settingsSearchEpoch              uint64
	settingsContentScrolls           map[string]*ui.ScrollState
	settingsPage                     string
	settingsCLITool                  string
	settingsLoaded                   string
	settingsOutput                   string
	settingsConfigRecovery           *settingsConfigRecoveryState
	settingsAgentContent             string
	settingsAgentPath                string
	settingsAgentLoaded              bool
	settingsAgentContext             string
	settingsSkillsCatalog            string
	settingsSkillSources             []map[string]any
	settingsLocalSkills              []map[string]any
	settingsSkillRoot                string
	settingsSkillPreviewOpen         bool
	settingsSkillPreviewName         string
	settingsSkillPreviewText         string
	settingsSkillFiles               []map[string]any
	settingsSkillFilesDirectory      string
	settingsSkillFilesTruncated      bool
	settingsBranchChoices            []string
	settingsExtensionRequests        *settingsExtensionRequestState
	settingsLogEntries               []map[string]any
	settingsGitEnvironment           map[string]any
	settingsDevelopmentToolsSnapshot map[string]any
	settingsCLIStatus                map[string]map[string]any
	settingsCLIBundleStatus          *platform.CLIBundleStatus
	settingsCLIErrors                map[string]string
	meegleProjectCatalog             *meegleProjectCatalogState
	skillsBrowser                    *skillsBrowserState
	settingsCLISessions              map[string]map[string]any
	settingsAuthPollTimers           map[string]*time.Timer
	settingsTerminalResolution       map[string]any
	settingsPickerBusy               bool
	settingsBoolInputs               map[string]*bool
	migrationPlan                    any
	migrationPreview                 string
	migrationReady                   bool
	taskRootChange                   *taskRootChangeRequest
	pageLocalRead                    *pageLocalReadState
	settingsAutosave                 *settingsAutosaveState
	platformConnections              *desktopPlatformConnections
	platformJobs                     asyncjob.Group
}

func (s *settingsController) stopFeature() {
	s.stopSettingsAutosave()
	s.stopPageLocalRead()
	s.state.platformJobs.Close()
	if state := s.state.platformConnections; state != nil {
		state.close()
	}
}

func (s *settingsController) drainFeature() error {
	err := runFeatureCleanup(
		featureCleanupStep{"退出前本机配置读取清理未完成", 10 * time.Second, s.closePageLocalRead},
		featureCleanupStep{"", 30 * time.Second, s.closeSkillsBrowserReads},
		featureCleanupStep{"退出前设置扩展读取清理未完成", 10 * time.Second, s.closeSettingsExtensionReads},
		featureCleanupStep{"退出前配置读取清理未完成", 10 * time.Second, s.closeSettingsConfigReads},
		featureCleanupStep{"退出前平台连接操作收尾未完成", 30 * time.Second, func(ctx context.Context) error {
			s.state.platformJobs.Close()
			return s.state.platformJobs.Wait(ctx)
		}},
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
