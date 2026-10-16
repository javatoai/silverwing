package application

import (
	"context"

	"silverwing-go/internal/domain"
)

// MaterialsSyncProgressBackend is an optional application capability for
// desktop callers that need the same sync operation with UI progress events.
// Ordinary CLI and Backend.Dispatch callers keep using the synchronous result.
type MaterialsSyncProgressBackend interface {
	DispatchMaterialsSync(context.Context, Action, func(domain.MaterialsSyncProgress)) (ActionResult, error)
}

type MaterialsSyncExecution struct {
	Result                  domain.MaterialsSyncResult `json:"result"`
	RequirementDirectoryURL string                     `json:"requirementDirectoryUrl,omitempty"`
}

type MaterialsSyncServiceBranch struct {
	ServiceName string `json:"serviceName"`
	Branch      string `json:"branch"`
}

type MaterialsSyncCommentRequest struct {
	TargetURL        string                     `json:"targetUrl"`
	RequirementLink  string                     `json:"requirementLink"`
	RequirementID    string                     `json:"requirementId"`
	RequirementTitle string                     `json:"requirementTitle"`
	WriteRoot        string                     `json:"writeRoot"`
	Subdirectory     string                     `json:"subdirectory"`
	DirectoryURL     string                     `json:"directoryUrl"`
	Services         []MaterialsSyncServiceBranch `json:"services"`
}
