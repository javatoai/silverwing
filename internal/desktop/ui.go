package desktop

import (
	"github.com/egoist/mygo/ui"
)

func (s *app) view(c *ui.Context) {
	defer s.observeProductionBuildNavigation()
	s.observeMaterialTextInput()
	s.observeMaterialsGitSync()
	s.observeMaterialsGitBranches()
	defer s.observeMaterialTextInput()
	s.beginOverflowMenuFrame()
	defer s.endOverflowMenuFrame()
	p := s.applyThemeFrame(c)
	s.reconcileSidebarNavigation()
	s.observePageLocalRead()
	s.observeMeegleProjectPicker()
	s.observeSystemOpenJobs()
	s.observeTaskCreationPreflight()
	s.observeRequirementIdentityJobs()
	s.observeRequirementNamingPreparation()
	s.observeTagJobs()
	if !s.localSkillsViewActive() || s.pageLocalReadReady() {
		s.observeSkillsBrowserNavigation()
	}
	s.observeMaterialsLiveRefresh()
	if s.requirementUnit.state.requirementContentJobs != nil {
		s.requirementUnit.state.requirementContentJobs.visible = ""
	}
	w, _ := c.Size()
	ui.MenuScope(c, s.popupMenuScope())
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() {
		s.sidebar(c, p, w < 1180 || s.navigationCollapsed)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			switch s.page {
			case "production":
				s.renderProductionPage(c, p)
			case "tasks", "archive", "materials":
				s.taskWorkspace(c, p)
			case "requirements":
				s.replicaRequirements(c, p)
			case "repositories", "services":
				s.replicaRepositories(c, p)
			case "tools":
				s.renderDeveloperTools(c)
			case "settings":
				s.replicaSettings(c, p)
			}
		})
	})
	s.bindVisibleRequirementReaderShortcuts(c)
	s.restoreWorkspaceReviewFocus(c)
	if !s.localSkillsViewActive() || s.pageLocalReadReady() {
		s.observeSkillsBrowserNavigation()
	}
	s.observeTaskRootMigrationNavigation()
	s.actionDialog(c, p)
	s.renderTaskDeleteDialog(c, p)
	s.renderMaterialsFileManagementDialogs(c, p)
	s.repositoryBatchDialog(c, p)
	s.repositoryBatchResultDialog(c, p)
	s.renderWorkspaceRemovalDialog(c, p)
	s.renderWorkspaceRepairDialog(c, p)
	s.renderWorkspaceGitDialogs(c, p)
	s.renderWorkspaceMergeBatchDialog(c, p)
	s.renderWorkspaceHistoryDialog(c, p)
	s.renderTagBuildDialog(c, p)
	s.renderTagTargetDialog(c, p)
	s.renderProductionBaselineDialog(c, p)
	s.renderProductionDiffDialog(c, p)
	s.renderProductionWorktreesDialog(c, p)
	s.renderProductionHistoryDialog(c, p)
	s.renderTaskBranchInfoDialog(c, p)
	if workspaceCommandDialogVisible(s.page, s.tab) {
		s.renderWorkspaceCommandDialog(c, p)
	}
	s.renderCodexAI(c, p)
	s.renderMarkdownRichLinkDialog(c, p)
	s.renderMarkdownBitmapViewer(c, p)
	s.observeSettingsExtensionRequests()
	s.observeSettingsConfigRecovery()
	s.observeTagGenbuAutoProbe()
	s.observeRequirementContentJobs()
	s.observeRequirementRefreshJobs()
	s.observeWorkspaceHistoryJobs()
	s.syncDeveloperToolJobVisibility()
	s.observeWorkspaceCommandJobIdentity()
	s.observeRequirementListIdentity()
	s.renderRequirementListProgressDialog(c, p)
	s.observeRequirementCommentComposers()
	s.renderRequirementCommentComposer(c, p)
	s.renderCommentDraftBitmapViewer(c, p)
	s.observeRequirementDocumentPreparation()
	s.observeMaterialsLiveRefresh()
	s.renderConfirmationDialog(c, p)
	ui.ThemedMenus(c, s.popupMenuScope())
	s.renderOperationNotifications(c)
	s.renderSettingsAutosaveNotification(c)
}

func workspaceCommandDialogVisible(page string, tab int) bool {
	return (page == "tasks" || page == "archive") && tab == 0
}
