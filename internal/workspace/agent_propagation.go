package workspace

import (
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type AgentInstructionScopeKind string

const (
	AgentInstructionGlobalScope AgentInstructionScopeKind = "GLOBAL"
	AgentInstructionGroupScope  AgentInstructionScopeKind = "GROUP"
)

type AgentInstructionScope struct {
	Kind    AgentInstructionScopeKind `json:"kind"`
	GroupID string                    `json:"groupId,omitempty"`
}

type AgentPropagationResult struct {
	UpdatedTaskDirectories []string          `json:"updatedTaskDirectories"`
	Failures               map[string]string `json:"failures"`
}

// Shared rules are referenced for every task layout. Saving a global/group
// document therefore never rewrites task-local files or scans task roots.
type AgentDocumentPropagationService struct {
	Manifests *storage.ManifestStore
	Documents *AgentDocumentService
	Locks     *OperationLocks
}

func NewAgentDocumentPropagationService(manifests *storage.ManifestStore, documents *AgentDocumentService, locks *OperationLocks) *AgentDocumentPropagationService {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if documents == nil {
		documents = NewAgentDocumentService(storage.NewPaths(""), manifests)
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &AgentDocumentPropagationService{Manifests: manifests, Documents: documents, Locks: locks}
}

func (s *AgentDocumentPropagationService) Propagate(_ domain.AppConfig, _ AgentInstructionScope) AgentPropagationResult {
	return AgentPropagationResult{UpdatedTaskDirectories: []string{}, Failures: map[string]string{}}
}
