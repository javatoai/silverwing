package documents

import (
	"context"

	"silverwing-go/internal/domain"
)

// RequirementReference is the identity needed to verify a new materials target.
// Identity is absent for a numeric input; an existing local directory can still
// be reused without contacting a provider.
type RequirementReference struct {
	WorkItemID string
	Identity   *RequirementIdentity
	ProjectKey *string
}

// RequirementProvider supplies source facts. Preparation owns local planning,
// identity validation, locking and file writes, but no remote CLI protocol.
type RequirementProvider interface {
	Resolve(context.Context, domain.AppConfig, RequirementReference) (RequirementIdentity, error)
	Title(context.Context, domain.AppConfig, RequirementReference) (string, error)
}

// RequirementUnmatchedError distinguishes a clean source lookup with no unique
// match from a failed lookup, preserving the preview's existing failure text.
type RequirementUnmatchedError struct{ Reason string }

func (e *RequirementUnmatchedError) Error() string { return e.Reason }

func requirementReference(id string, link *domain.FeishuWorkItemLink) RequirementReference {
	ref := RequirementReference{WorkItemID: id}
	if link != nil {
		ref.Identity = &RequirementIdentity{Space: link.Space, Kind: link.Kind, WorkItemID: link.WorkItemID}
		ref.ProjectKey = link.ProjectKey
	}
	return ref
}
