package desktop

import (
	"silverwing-go/internal/application"
)

func (s *app) availableModules(t task, service application.Service) []application.Module {
	available := make([]application.Module, 0, len(service.Modules))
	for _, module := range service.Modules {
		used := false
		for _, workspace := range t.workspaces {
			if workspace.ServiceID == service.ID && workspace.ModuleID == module.ID {
				used = true
				break
			}
		}
		if !used {
			available = append(available, module)
		}
	}
	return available
}

func (s *app) setWorkspaceAddSelection(serviceID, moduleID string) {
	s.workspaceUnit.state.workspaceAddServiceID = serviceID
	s.workspaceUnit.state.workspaceAddModuleID = moduleID
	service, ok := s.serviceByID(s.taskUnit.taskByID(s.form.taskID).groupID, serviceID)
	if !ok {
		return
	}
	baseRef, strategy := service.MasterBranch, service.Strategy
	for _, module := range service.Modules {
		if module.ID == moduleID {
			if module.MasterBranch != "" {
				baseRef = module.MasterBranch
			}
			if module.Strategy != "" {
				strategy = module.Strategy
			}
			break
		}
	}
	if strategy == "" {
		strategy = "STANDARD_WORKTREE"
	}
	for i := range s.form.fields {
		field := &s.form.fields[i]
		if field.key == "baseRef" {
			field.value = baseRef
		} else if field.key == "strategy" {
			field.value = strategy
		}
		if value := s.inputs["form:"+field.key]; value != nil {
			*value = field.value
		}
	}
}

func (s *app) toggleTaskService(service application.Service) {
	if s.taskUnit.state.taskServices[service.ID] {
		delete(s.taskUnit.state.taskServices, service.ID)
		delete(s.taskUnit.state.taskModules, service.ID)
		return
	}
	s.taskUnit.state.taskServices[service.ID] = true
	selected := make(map[string]bool, len(service.Modules))
	for _, module := range service.Modules {
		selected[module.ID] = true
	}
	s.taskUnit.state.taskModules[service.ID] = selected
}

func (s *app) toggleTaskModule(serviceID, moduleID string, moduleCount int) {
	modules := s.taskUnit.state.taskModules[serviceID]
	if modules == nil {
		modules = make(map[string]bool)
		s.taskUnit.state.taskModules[serviceID] = modules
	}
	modules[moduleID] = !modules[moduleID]
	selected := 0
	for _, value := range modules {
		if value {
			selected++
		}
	}
	if selected == 0 || moduleCount == 0 {
		delete(s.taskUnit.state.taskServices, serviceID)
		delete(s.taskUnit.state.taskModules, serviceID)
	}
}
