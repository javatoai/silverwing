package desktop

import (
	"context"
	"silverwing-go/internal/application"
	"time"
)

// workspaceController owns this feature's state. Its zero value is usable in headless tests.
type workspaceController struct{ state workspaceState }

type workspaceState struct {
	serviceCatalog             *desktopServiceCatalog
	serviceCatalogRevision     uint64
	serviceList                serviceListBrowsingState
	workspaceReviewFocusReturn *workspaceReviewFocusReturn
	repositoryTab              string
	workspaceGit               *workspaceGitState
	workspaceRemoval           *workspaceRemovalState
	workspaceRepair            *workspaceRepairState
	workspaceHistory           *workspaceHistoryState
	workspaceReviewSessions    map[string]*workspaceReviewSession
	workspaceHistoryJobs       *workspaceHistoryJobState
	workspaceMerge             *workspaceMergeUIState
	workspaceMergeBatch        *workspaceMergeBatchUIState
	workspaceCommand           *workspaceCommandUIState
	workspaceCommandJobs       *workspaceCommandJobsState
	serviceGroupID             string
	serviceReorderMode         bool
	repositoryBatchOpen        bool
	repositoryBatchGroupID     string
	repositoryBatchPaths       []string
	repositoryBatchPickerBusy  bool
	repositoryBatchGeneration  uint64
	repositoryBatchResultOpen  bool
	repositoryBatchResult      application.RepositoryBatchResult
	serviceEditor              *serviceEditorState
	serviceRepositoryChoices   *serviceRepositoryChoiceJobs
	selectedService            string
	selectedModule             string
	workspaceAppend            *taskAppendUIState
	workspaceAddServiceID      string
	workspaceAddModuleID       string
}

type workspaceShutdownPorts struct {
	closeServiceReads func(context.Context) error
	closeDiffSessions func(context.Context) error
}

func (s *workspaceController) drainFeature(ports workspaceShutdownPorts) error {
	err := runFeatureCleanup(
		featureCleanupStep{"退出前服务仓库读取清理未完成", 10 * time.Second, ports.closeServiceReads},
		featureCleanupStep{"退出前工作区命令清理未完成", 30 * time.Second, s.closeWorkspaceCommandJobs},
		featureCleanupStep{"退出前工作区预览清理未完成", 10 * time.Second, s.closeWorkspaceHistoryJobs},
		featureCleanupStep{"退出前 Git 差异快照清理未完成", 10 * time.Second, ports.closeDiffSessions},
	)
	s.cancelWorkspaceGitStatus()
	return err
}

type workspaceSelectionView interface {
	dismissWorkspaceHistory()
	clearWorkspaceCommandSelection()
}

func (s *workspaceController) leaveTaskSelection(transition TaskTransition, view workspaceSelectionView) {
	if transition.origin == taskTransitionSnapshot {
		view.dismissWorkspaceHistory()
		view.clearWorkspaceCommandSelection()
	} else {
		view.clearWorkspaceCommandSelection()
		if transition.indexChanged {
			view.dismissWorkspaceHistory()
		}
	}
	s.clearWorkspaceMergeSelection()
	s.cancelWorkspaceGitStatus()
	s.cancelWorkspaceGitFlow()
}
