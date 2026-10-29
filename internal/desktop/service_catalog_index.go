package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type desktopServiceIdentity struct{ group, service string }
type desktopServiceLookup struct {
	value     application.Service
	ambiguous bool
}

// Owned by one published snapshot. Callers borrow read-only values; editable
// configuration maps remain separately decoded by settingsConfigMap.
type desktopServiceCatalog struct {
	home          string
	groups        map[string]application.Group
	services      map[desktopServiceIdentity]desktopServiceLookup
	groupServices map[string][]application.Service
	repositories  map[string]application.Repository
}

func newDesktopServiceCatalog(snapshot application.Snapshot) *desktopServiceCatalog {
	index := &desktopServiceCatalog{home: snapshot.Home, groups: make(map[string]application.Group), services: make(map[desktopServiceIdentity]desktopServiceLookup), groupServices: make(map[string][]application.Service), repositories: make(map[string]application.Repository)}
	fallback := make(map[string][]desktopServiceIdentity)
	for _, group := range snapshot.Groups {
		if _, exists := index.groups[group.ID]; !exists {
			group.ServiceIDs = append([]string(nil), group.ServiceIDs...)
			index.groups[group.ID] = group
		}
	}
	for _, repository := range snapshot.Repositories {
		if _, exists := index.repositories[repository.ID]; !exists {
			index.repositories[repository.ID] = repository
		}
	}
	for _, service := range snapshot.Services {
		key := desktopServiceIdentity{service.GroupID, service.ID}
		fallback[service.GroupID] = append(fallback[service.GroupID], key)
		if previous, exists := index.services[key]; exists {
			previous.ambiguous = true
			index.services[key] = previous
		} else {
			service.Modules = append([]application.Module(nil), service.Modules...)
			index.services[key] = desktopServiceLookup{value: service}
		}
	}
	for id, group := range index.groups {
		if len(group.ServiceIDs) > 0 {
			for _, serviceID := range group.ServiceIDs {
				if service, ok := index.service(id, serviceID); ok {
					index.groupServices[id] = append(index.groupServices[id], service)
				}
			}
		} else {
			for _, key := range fallback[id] {
				if value, ok := index.service(key.group, key.service); ok {
					index.groupServices[id] = append(index.groupServices[id], value)
				}
			}
		}
	}
	return index
}

func (index *desktopServiceCatalog) service(group, id string) (application.Service, bool) {
	entry, found := index.services[desktopServiceIdentity{group, id}]
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
type desktopServiceConfigIndex struct {
	scoped   map[desktopServiceIdentity]desktopServiceConfigLookup
	unscoped map[string]desktopServiceConfigLookup
}

func newDesktopServiceConfigIndex(config map[string]any) *desktopServiceConfigIndex {
	index := &desktopServiceConfigIndex{scoped: make(map[desktopServiceIdentity]desktopServiceConfigLookup), unscoped: make(map[string]desktopServiceConfigLookup)}
	groups, _ := config["groups"].([]any)
	for _, value := range groups {
		group, _ := value.(map[string]any)
		services, _ := group["services"].([]any)
		for _, value := range services {
			service, _ := value.(map[string]any)
			id, valid := service["id"].(string)
			if !valid {
				continue
			}
			key := desktopServiceIdentity{mapString(group, "id"), id}
			if previous, exists := index.scoped[key]; exists {
				previous.ambiguous = true
				index.scoped[key] = previous
			} else {
				index.scoped[key] = desktopServiceConfigLookup{value: service}
			}
			if previous, exists := index.unscoped[id]; exists {
				previous.ambiguous = true
				index.unscoped[id] = previous
			} else {
				index.unscoped[id] = desktopServiceConfigLookup{value: service}
			}
		}
	}
	return index
}

func (index *desktopServiceConfigIndex) find(serviceID string, groupIDs ...string) map[string]any {
	if index == nil {
		return nil
	}
	entry := index.unscoped[serviceID]
	if len(groupIDs) > 0 {
		entry = index.scoped[desktopServiceIdentity{groupIDs[0], serviceID}]
	}
	if entry.ambiguous {
		return nil
	}
	return entry.value
}

type serviceListBrowsingState struct {
	index              *desktopServiceCatalog
	groupID, query     string
	services, filtered []application.Service
	lower              []string
	positions          map[desktopServiceIdentity]int
	list               ui.ListState
}

func (state *serviceListBrowsingState) project(index *desktopServiceCatalog, groupID, query string) []application.Service {
	query = strings.ToLower(strings.TrimSpace(query))
	if state.index != index || state.groupID != groupID {
		var list ui.ListState
		if state.index != nil && state.index.home == index.home && state.groupID == groupID {
			list = state.list
		}
		*state = serviceListBrowsingState{index: index, groupID: groupID, services: index.groupServices[groupID], positions: make(map[desktopServiceIdentity]int), list: list}
		for i, service := range state.services {
			state.positions[desktopServiceIdentity{service.GroupID, service.ID}] = i
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
