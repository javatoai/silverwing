package desktop

import (
	"github.com/egoist/mygo/ui"
)

func (s *app) view(c *ui.Context) {
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
	s.observeRequirementIdentityJobs()
	s.observeRequirementNamingPreparation()
	if !s.localSkillsViewActive() || s.pageLocalReadReady() {
		s.observeSkillsBrowserNavigation()
	}
	s.observeMaterialsLiveRefresh()
	if s.requirementContentJobs != nil {
		s.requirementContentJobs.visible = ""
	}
	w, _ := c.Size()
	ui.MenuScope(c, s.popupMenuScope())
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() {
		s.sidebar(c, p, w < 1180 || s.navigationCollapsed)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			switch s.page {
			case "tasks", "archive", "materials":
				s.taskWorkspace(c, p)
			case "requirements":
				s.replicaRequirements(c, p)
			case "tools":
				s.renderDeveloperTools(c)
			case "settings":
				s.replicaSettings(c, p)
			}
		})
	})
	s.bindVisibleRequirementReaderShortcuts(c)
	if !s.localSkillsViewActive() || s.pageLocalReadReady() {
		s.observeSkillsBrowserNavigation()
	}
	s.observeTaskRootMigrationNavigation()
	s.actionDialog(c, p)
	s.renderMaterialsFileManagementDialogs(c, p)
	s.renderCodexAI(c, p)
	s.renderMarkdownRichLinkDialog(c, p)
	s.renderMarkdownBitmapViewer(c, p)
	s.observeSettingsExtensionRequests()
	s.observeSettingsConfigRecovery()
	s.observeRequirementContentJobs()
	s.observeRequirementRefreshJobs()
	s.syncDeveloperToolJobVisibility()
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
