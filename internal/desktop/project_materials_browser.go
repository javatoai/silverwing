package desktop

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/storage"
)

type projectMaterialsControls struct {
	syncNow func()
	syncing func() bool
}

// This shell hosts the same materials feature without joining the main task
// list. Requests use the project checkout through the scoped backend.
func (s *app) newProjectMaterialsBrowser(view *projectInstructionsView) *app {
	pane := newApp(view.port, Options{Tab: 1})
	pane.win, pane.mermaidInitializer = s.win, s.mermaidInitializer
	pane.desktopPreferences = s.desktopPreferences
	pane.snapshot.Home, pane.snapshot.ProfileID = view.scope.Home, s.snapshot.ProfileID
	pane.settingsUnit.state.configJSON = s.settingsUnit.state.configJSON
	pane.loading = false
	pane.operationNotifications.prefix = "project-instructions-"
	pane.projectMaterials = &projectMaterialsControls{
		syncNow: func() { s.syncProjectInstructions(view, true) },
		syncing: func() bool { return view.syncing },
	}
	root := filepath.Join(view.scope.Root, view.scope.Subdirectory)
	pane.taskUnit.replaceTasks([]task{{
		id: application.ProjectInstructionsDocumentTaskID(view.scope), path: root,
		materialsPath: root, materialsStatus: "READY",
		materials: documents.MaterialsDescriptor{
			Kind: documents.MaterialsLocal, ReadRoot: root, OwnedRoot: root, CanWrite: true,
		},
	}}, 0)
	// Managed paths keep their cache when the profile moves. Explicit clones
	// with the same folder name keep independent tabs and scroll positions.
	if s.win != nil && (s.desktopPreferences == nil || s.desktopPreferences.enabled) {
		identity := filepath.Base(view.scope.Root)
		if view.scope.BindingID != "" {
			identity = normalizeIdentityPath(view.scope.Root) + "\x00" + view.scope.URL
		}
		id := sha256.Sum256([]byte(identity + "\x00" + view.scope.Branch))
		cache := storage.NewPaths(view.scope.Home).Cache
		pane.initializeReadingSessionAt(view.scope.Home, filepath.Join(cache, fmt.Sprintf("project-instructions-reading-%x.json", id[:16])), false)
	}
	if pane.readingSession == nil {
		pane.readingSession = &desktopReadingSession{home: view.scope.Home, loaded: true, cache: EmptyReadingSnapshot(),
			restoredMaterials: make(map[string]bool), scrollStates: make(map[string]readingScrollBinding)}
	}
	return pane
}

func (s *app) visibleProjectMaterials() *app {
	if (s.page != "tasks" && s.page != "archive" && s.page != "materials") ||
		s.tab != taskProjectInstructionsTab || s.workspaceReviewPageVisible() ||
		!s.projectInstructionsCurrent(s.projectInstructions) {
		return nil
	}
	return s.projectInstructions.browser
}

func (s *app) observeProjectMaterials() {
	view := s.projectInstructions
	if view == nil || view.browser == nil {
		return
	}
	pane := view.browser
	if s.visibleProjectMaterials() == nil {
		if pane.materialsBrowserVisible() {
			pane.observeReadingSession()
			pane.page = ""
			pane.materialsUnit.closeMaterialsLiveRefresh()
			pane.materialsUnit.cancelMaterialsSearch()
			pane.closeMarkdownLinkFile()
			pane.dismissMarkdownBitmapViewer()
			pane.cancelConfirmationDialog()
			pane.dialogOpen = false
		}
		return
	}
	pane.page, pane.dark = "materials", s.dark
	pane.observeMaterialTextInput()
	pane.observeSystemOpenJobs()
	if pane.materialsUnit.state.documentsLoaded {
		pane.observeMaterialsLiveRefresh()
	}
}

func (s *app) renderProjectMaterialsLayers(c *ui.Context, p palette) {
	if pane := s.visibleProjectMaterials(); pane != nil {
		pane.actionDialog(c, p)
		pane.renderMaterialsFileManagementDialogs(c, p)
		pane.renderMarkdownRichLinkDialog(c, p)
		pane.renderMarkdownLinkFile(c, p)
		pane.renderMarkdownBitmapViewer(c, p)
		pane.renderConfirmationDialog(c, p)
		pane.renderOperationNotifications(c)
		pane.observeMaterialTextInput()
		pane.observeReadingSession()
	}
}

func (v *projectInstructionsView) stop() {
	v.cancel()
	v.stopSyncTimer()
	if pane := v.browser; pane != nil {
		pane.page = ""
		pane.closeMarkdownLinkFile()
		pane.dismissMarkdownBitmapViewer()
		pane.materialsUnit.cancelMaterialsSearch()
		pane.materialsUnit.stopFeature()
		pane.stopSystemOpenJobs()
		pane.cancelConfirmationDialog()
		if pane.cancelOperation != nil {
			pane.cancelOperation()
		}
		pane.closeMarkdownEditor()
	}
}

func (v *projectInstructionsView) drain() error {
	if v == nil || v.browser == nil {
		return nil
	}
	pane := v.browser
	return runFeatureClosers(
		pane.materialsUnit.drainFeature,
		pane.closeReadingSession,
		func() error { return pane.materialsUnit.finishFeature(pane) },
		func() error {
			return runFeatureCleanup(featureCleanupStep{"打开项目资料", 10 * time.Second, pane.closeSystemOpenJobs})
		},
	)
}
