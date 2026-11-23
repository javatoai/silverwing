package desktop

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type desktopServiceLookup struct {
	value     application.Service
	ambiguous bool
}

// Owned by one published snapshot. Callers borrow read-only values; editable
// configuration maps remain separately decoded by settingsConfigMap.
type desktopServiceCatalog struct {
	home         string
	services     map[string]desktopServiceLookup
	ordered      []application.Service
	repositories map[string]application.Repository
}

func newDesktopServiceCatalog(snapshot application.Snapshot) *desktopServiceCatalog {
	index := &desktopServiceCatalog{home: snapshot.Home, services: make(map[string]desktopServiceLookup), repositories: make(map[string]application.Repository)}
	for _, repository := range snapshot.Repositories {
		if _, exists := index.repositories[repository.ID]; !exists {
			index.repositories[repository.ID] = repository
		}
	}
	for _, service := range snapshot.Services {
		if previous, exists := index.services[service.ID]; exists {
			previous.ambiguous = true
			index.services[service.ID] = previous
		} else {
			service.Modules = append([]application.Module(nil), service.Modules...)
			index.services[service.ID] = desktopServiceLookup{value: service}
		}
	}
	for _, item := range snapshot.Services {
		if service, ok := index.service(item.ID); ok {
			index.ordered = append(index.ordered, service)
		}
	}
	return index
}

func (index *desktopServiceCatalog) service(id string) (application.Service, bool) {
	entry, found := index.services[id]
	if !found || entry.ambiguous {
		return application.Service{}, false
	}
	return entry.value, true
}

func (s *app) serviceCatalog() *desktopServiceCatalog {
	if s.workspaceUnit.state.serviceCatalog == nil {
		s.workspaceUnit.state.serviceCatalog = newDesktopServiceCatalog(s.snapshot)
	}
	return s.workspaceUnit.state.serviceCatalog
}

// The catalog is a detached display snapshot. Scope capture and async result
// guards must read the current UI snapshot instead, including legacy callers
// that replace a repository root before publishing the next display catalog.
// These low-frequency reads preserve repositoryDetails' original first match.
func (s *app) currentRepositoryDetails(repositoryID string) (name, root string) {
	for _, repository := range s.snapshot.Repositories {
		if repository.ID == repositoryID {
			return repository.Name, repository.RootPath
		}
	}
	return repositoryID, ""
}

func (s *workspaceController) publishServiceCatalog(index *desktopServiceCatalog) {
	s.state.serviceCatalog = index
	s.state.serviceCatalogRevision++
}

type desktopServiceConfigLookup struct {
	value     map[string]any
	ambiguous bool
}
type desktopServiceConfigIndex map[string]desktopServiceConfigLookup

func newDesktopServiceConfigIndex(config map[string]any) desktopServiceConfigIndex {
	index := make(desktopServiceConfigIndex)
	services, _ := config["services"].([]any)
	for _, value := range services {
		service, _ := value.(map[string]any)
		id, valid := service["id"].(string)
		if !valid {
			continue
		}
		if previous, exists := index[id]; exists {
			previous.ambiguous = true
			index[id] = previous
		} else {
			index[id] = desktopServiceConfigLookup{value: service}
		}
	}
	return index
}

func (index desktopServiceConfigIndex) find(serviceID string) map[string]any {
	entry := index[serviceID]
	if entry.ambiguous {
		return nil
	}
	return entry.value
}

type serviceListBrowsingState struct {
	index              *desktopServiceCatalog
	configSource       string
	query              string
	services, filtered []application.Service
	enabledCount       int
	lower              []string
	positions          map[string]int
	list               ui.ListState
	canvas             ui.Handle
	windowWidth        float32
}

func (state *serviceListBrowsingState) project(index *desktopServiceCatalog, configSource, query string, enabled func(string) bool) []application.Service {
	query = strings.ToLower(strings.TrimSpace(query))
	if state.index != index || state.configSource != configSource {
		var list ui.ListState
		if state.index != nil && state.index.home == index.home {
			list = state.list
		}
		services := make([]application.Service, len(index.ordered))
		enabledCount, disabledStart := 0, len(services)
		for _, service := range index.ordered {
			if enabled(service.ID) {
				services[enabledCount] = service
				enabledCount++
			} else {
				disabledStart--
				services[disabledStart] = service
			}
		}
		// Restore the original order of the disabled items filled from the end.
		slices.Reverse(services[disabledStart:])
		*state = serviceListBrowsingState{
			index: index, configSource: configSource, services: services, enabledCount: enabledCount,
			positions: make(map[string]int, len(services)), list: list, canvas: state.canvas, windowWidth: state.windowWidth,
		}
		for i, service := range state.services {
			state.positions[service.ID] = i
		}
		state.query = "\x00" // an empty first query must still publish the result
	}
	if state.query == query {
		return state.filtered
	}
	state.query = query
	if query == "" {
		state.filtered = state.services
		return state.filtered
	}
	if state.lower == nil {
		state.lower = make([]string, len(state.services))
		for i, service := range state.services {
			repository := index.repositories[service.RepositoryID]
			name := firstNonEmpty(repository.Name, service.RepositoryID)
			state.lower[i] = strings.ToLower(strings.Join([]string{service.Name, service.ID, name, repository.RootPath}, " "))
		}
	}
	state.filtered = make([]application.Service, 0)
	for i, service := range state.services {
		if strings.Contains(state.lower[i], query) {
			state.filtered = append(state.filtered, service)
		}
	}
	return state.filtered
}
