package desktop

import (
	"context"
	"silverwing-go/internal/application"
	"time"
)

// requirementController owns this feature's state. Its zero value is usable in headless tests.
type requirementController struct{ state requirementState }

type requirementState struct {
	commentComposers                  *requirementCommentComposers
	requirementReaders                map[string]*requirementReaderState
	requirementReaderGeneration       uint64
	requirementContentJobs            *requirementContentJobState
	requirementRefreshJobs            *requirementRefreshState
	requirementListJobs               *requirementListJobState
	requirementListProgress           *requirementListProgressState
	requirementIdentityJobs           *requirementIdentityJobState
	requirementNaming                 *requirementNamingPreparationState
	requirementDocumentPreparation    *requirementDocumentPreparationState
	selectedRequirement               int
	selectedRequirementKey            string
	requirementsRequested             bool
	requirementsLoading               bool
	requirementsLoaded                bool
	requirementsFetchedAt             int64
	requirementsLoadError             string
	requirementsFailures              []string
	requirementsCompletedQueries      int
	requirementsSprints               []requirementSprint
	requirementsSelectedSprintKey     string
	requirementsFocusedDetail         bool
	taskRequirementMetadata           map[string]taskRequirementMetadataState
	taskRequirementMetadataCancels    map[string]context.CancelFunc
	taskRequirementMetadataGeneration map[string]uint64
	requirementWidth                  float32
	requirements                      []application.Requirement
}

// These capabilities include backend cache owners as well as the local
// queues. No other settings, task, window or application operation is exposed.
type requirementShutdownPorts struct {
	closeNaming    func(context.Context) error
	closeDocuments func(context.Context) error
	closeRefresh   func(context.Context) error
}

func (s *requirementController) drainFeature(ports requirementShutdownPorts) error {
	err := runFeatureCleanup(
		featureCleanupStep{"", 30 * time.Second, s.closeRequirementCommentComposers},
		featureCleanupStep{"退出前需求身份清理未完成", 30 * time.Second, s.closeRequirementIdentityJobs},
		featureCleanupStep{"退出前需求列表清理未完成", 30 * time.Second, s.closeRequirementListJobs},
		featureCleanupStep{"退出前需求命名清理未完成", 30 * time.Second, ports.closeNaming},
		featureCleanupStep{"退出前需求全文缓存清理未完成", 30 * time.Second, ports.closeDocuments},
		featureCleanupStep{"退出前单项需求刷新清理未完成", 10 * time.Second, ports.closeRefresh},
		featureCleanupStep{"退出前需求读取清理未完成", 10 * time.Second, s.closeRequirementContentJobs},
	)
	s.cancelTaskRequirementReads()
	s.closeRequirementReaders()
	return err
}

func executeRequirementOperation(ctx context.Context, backend application.Backend, action application.Action, progress func(application.RequirementListProgress)) (application.ActionResult, error) {
	if port, ok := backend.(application.RequirementReader); ok {
		switch action.Kind {
		case "requirements.list", "requirements.refresh":
			request, err := application.RequirementListRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.ReadRequirementList(ctx, request, progress))
		case "requirements.read":
			request, err := application.RequirementReadRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.ReadRequirement(ctx, request))
		case "requirements.comments":
			request, err := application.RequirementReadRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.ReadRequirementComments(ctx, request))
		}
	}
	if port, ok := backend.(application.RequirementListProgressBackend); ok && progress != nil && (action.Kind == "requirements.list" || action.Kind == "requirements.refresh") {
		return port.DispatchRequirementList(ctx, action, progress)
	}
	return backend.Dispatch(ctx, action)
}
