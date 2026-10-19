// Package integrations connects Silverwing's typed application API to the
// configured Meegle and Lark CLIs. It does not perform remote work at startup.
package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type Service struct {
	paths                     storage.Paths
	config                    *storage.ConfigStore
	runner                    *process.Runner
	prep                      *documents.PreparationService
	mu                        sync.Mutex
	aiCacheMu                 sync.Mutex
	identityStateMu           sync.Mutex
	identityClosed            bool
	identityProbe             uint64
	identityEpochCounter      uint64
	identityConfirmed         bool
	identityState             string
	confirmedIdentity         string
	confirmedIdentityRaw      string
	identityContext           string
	identitySessionGeneration uint64
	identitySessions          map[string]requirementIdentitySession
	identityRequests          map[string]*requirementIdentityRequest
	naming                    *requirementNamingCoordinator
	requirementDocuments      *requirementDocumentCoordinator
	requirementReads          requirementReadOrder
	active                    map[string]context.CancelFunc
	lastSync                  any
}

func NewService(paths storage.Paths, config *storage.ConfigStore, runner *process.Runner) *Service {
	if config == nil {
		config = storage.NewConfigStore(paths)
	}
	if runner == nil {
		runner = process.NewRunner()
	}
	return &Service{
		paths: paths, config: config, runner: runner,
		prep:   documents.NewPreparationService(paths, runner),
		active: map[string]context.CancelFunc{},
	}
}

// Execute is the JSON-shaped boundary used by the desktop bridge. Each action
// returns source-compatible lower-camel DTOs; command failures stay errors.
func (s *Service) Execute(ctx context.Context, kind string, payload map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := s.config.Load()
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(kind) {
	case "meegle.status":
		return s.meegleStatus(ctx, config)
	case "meegle.projects":
		return s.meegleProjectsForIdentity(ctx, config, payload)
	case "requirements.list":
		return s.listRequirements(ctx, config, payload, false)
	case "requirements.refresh":
		return s.listRequirements(ctx, config, payload, true)
	case "requirements.read":
		return s.readRequirement(ctx, config, payload)
	case "requirements.metadata":
		return s.requirementMetadata(ctx, config, payload)
	case "requirements.comments":
		return s.readRequirementComments(ctx, config, payload)
	case "requirements.comment":
		return s.writeRequirementComment(ctx, config, payload)
	case "requirements.attachment":
		return s.downloadRequirementAttachment(ctx, config, payload)
	case "requirements.ai-name":
		return s.requirementAINaming(ctx, config, payload)
	case "materials.ensure":
		return s.ensureMaterials(ctx, config, payload)
	case "materials.resolve-directory":
		return s.resolveMaterialsDirectory(ctx, config, payload)
	case "materials.resolve-entry":
		return s.resolveMaterialsEntry(ctx, config, payload)
	case "materials.plan":
		return s.planMaterials(ctx, config, payload)
	case "materials.confirm":
		return s.confirmMaterials(ctx, config, payload)
	case "materials.sync":
		return s.syncMaterials(ctx, config, payload)
	case "materials.status":
		return s.materialsStatus(payload)
	case "materials.cancel":
		return s.cancelMaterials(payload)
	case "materials.recover":
		return s.recoverMaterials(ctx, config, payload)
	case "materials.comment":
		return s.commentMaterials(ctx, config, payload)
	default:
		return nil, fmt.Errorf("未知 integration action：%s", kind)
	}
}

func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func boolValue(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func stringSlice(payload map[string]any, key string) []string {
	value, ok := payload[key]
	if !ok {
		return nil
	}
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func (s *Service) configValue() (domain.AppConfig, error) { return s.config.Load() }

var errIntegrationNotImplemented = errors.New("integration action is not implemented")
