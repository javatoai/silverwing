package workspace

import (
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// TaskCreationDefaultsService updates the global defaults used by new task forms.
// Group configuration remains responsible for service and module membership.
type TaskCreationDefaultsService struct{ Config *storage.ConfigStore }

func NewTaskCreationDefaultsService(config *storage.ConfigStore) *TaskCreationDefaultsService {
	if config == nil {
		config = storage.NewConfigStore(storage.NewPaths("."))
	}
	return &TaskCreationDefaultsService{Config: config}
}

func (s *TaskCreationDefaultsService) UpdateBranchPrefix(raw string) (domain.AppConfig, error) {
	return s.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		config.DefaultBranchPrefix = strings.TrimSpace(raw)
		return config, nil
	})
}

func (s *TaskCreationDefaultsService) UpdateWorkspaceToolIDs(raw []string) (domain.AppConfig, error) {
	ids := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return s.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		config.DefaultWorkspaceToolIDs = ids
		return config, nil
	})
}
