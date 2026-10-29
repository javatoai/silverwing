package desktop

import (
	"context"
	"fmt"
	"silverwing-go/internal/application"
	"silverwing-go/internal/editor"
	"sync"
	"time"
)

// materialsController owns this feature's state. Its zero value is usable in headless tests.
type materialsController struct{ state materialsState }

type materialsState struct {
	materialsGit                  *materialsGitDesktopState
	materialsGitBranches          *materialsGitBranchesState
	materialsGitCapture           bool
	materialsFileManagement       *materialsFileManagementState
	materialsFileJobs             *materialsFileJobState
	materialsLiveRefreshLifecycle *materialsLiveRefreshLifecycle
	pendingTaskRootMigration      *markdownTaskRootMigrationLease
	documents                     []application.Document
	documentContent               string
	documentPath                  string
	documentLinkFragment          string
	documentKind                  string
	documentReadOnly              bool
	documentHash                  string
	documentReadPending           bool
	documentDirty                 bool
	documentEditing               bool
	documentTabs                  []string
	documentDrafts                map[string]string
	documentDirtyMap              map[string]bool
	markdownAutosave              *editor.Autosave
	materialTextEditors           map[string]*materialTextEditorState
	materialTextActive            *materialTextEditorState
	documentClose                 *documentCloseState
	documentSaveMu                sync.Mutex
	documentSaveRefs              map[string]documentSaveRef
	markdown                      *markdownEditorState
	nativeMedia                   *nativeMediaState
	markdownBitmapViewer          *markdownBitmapViewerState
	documentSearch                string
	documentFindOpen              bool
	documentFind                  *documentFindState
	materialsPicker               *materialsPickerState
	documentsCollapsed            bool
	documentsRequested            bool
	documentsLoaded               bool
	importPreview                 bool
	importPath                    string
}

func (s *materialsController) stopFeature() {
	s.stopMaterialsGitSync()
	s.abandonPendingTaskRootMigration()
	s.stopMaterialsFileJobs()
	s.closeMaterialsPathCopy()
	s.closeMaterialsLiveRefresh()
	s.closeNativeMedia()
}

func (s *materialsController) drainFeature() error {
	return runFeatureCleanup(
		featureCleanupStep{"退出前资料 Git 同步收尾未完成", 10 * time.Second, s.closeMaterialsGitSync},
		featureCleanupStep{"退出前资料文件操作清理未完成", 10 * time.Second, s.closeMaterialsFileJobs},
		featureCleanupStep{"退出前资料目录监听清理未完成", 10 * time.Second, s.joinMaterialsLiveRefresh},
	)
}

func (s *materialsController) finishFeature(view interface{ dismissMarkdownBitmapViewer() }) error {
	view.dismissMarkdownBitmapViewer()
	s.cancelMaterialsSearch()
	if s.state.markdownAutosave == nil {
		return nil
	}
	return runFeatureCleanup(featureCleanupStep{"", 30 * time.Second, func(ctx context.Context) error {
		failures := s.documentClosePendingFailures(s.state.markdownAutosave.Close(ctx))
		if len(failures) > 0 {
			return fmt.Errorf("退出前仍有 %d 个编辑文档未保存：%s", len(failures), failures[0].Message)
		}
		return nil
	}})
}

// Ports expose only editor/view work that still needs the native desktop bridge.
// Controllers never retain an app, a window, or a universal command dispatcher.
type materialsSelectionInput interface {
	captureMaterialTextInput() error
	captureMarkdownEditorInput(*markdownEditorState) error
	syncMarkdownEditor()
}

type materialsSelectionView interface {
	deactivateInstructionEditor()
	dismissMarkdownBitmapViewer()
	observeMaterialTextInput()
	closeMarkdownEditor()
}

func (s *materialsController) prepareTaskSelection(transition TaskTransition, input materialsSelectionInput) error {
	if transition.origin == taskTransitionSnapshot {
		input.syncMarkdownEditor()
		return nil
	}
	if !transition.indexChanged {
		return nil
	}
	if err := input.captureMaterialTextInput(); err != nil {
		return err
	}
	return input.captureMarkdownEditorInput(s.state.markdown)
}

func (s *materialsController) leaveTaskSelection(transition TaskTransition, view materialsSelectionView) {
	view.deactivateInstructionEditor()
	s.cancelMaterialsSearch()
	if transition.origin == taskTransitionUser {
		view.dismissMarkdownBitmapViewer()
	}
}

func (s *materialsController) resetTaskPresentation(transition TaskTransition, view materialsSelectionView) {
	if transition.origin == taskTransitionSnapshot {
		view.observeMaterialTextInput()
	}
	s.closeNativeMedia()
	view.closeMarkdownEditor()
	s.resetPresentedMaterials()
}

// Reset only the selected task's presentation. Autosave drafts, save leases,
// canceled workers, and pending root migrations keep their independent owners.
func (s *materialsController) resetPresentedMaterials() {
	s.state.documents = nil
	s.state.documentsLoaded = false
	s.state.documentsRequested = false
	s.state.documentTabs = nil
	s.state.documentReadPending = false
	s.state.documentDrafts = make(map[string]string)
	s.state.documentDirtyMap = make(map[string]bool)
	s.state.documentPath, s.state.documentContent = "", ""
	s.state.documentHash = ""
	s.state.documentDirty, s.state.documentEditing = false, false
}
