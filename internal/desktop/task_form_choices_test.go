package desktop

import (
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTaskCreationServiceMatchesNameIDAndRepositoryPath(t *testing.T) {
	service := domain.GroupServiceConfig{ID: "service-orders", DisplayName: "订单服务", RepositoryID: "repo-orders"}
	cases := []struct {
		query string
		want  bool
	}{
		{query: "订单", want: true},
		{query: "service-orders", want: true},
		{query: "workspace\\orders", want: true},
		{query: "missing", want: false},
		{query: "  ", want: true},
	}
	for _, test := range cases {
		t.Run(test.query, func(t *testing.T) {
			if got := taskCreationServiceMatches(service, `D:\workspace\orders`, test.query); got != test.want {
				t.Fatalf("taskCreationServiceMatches(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func TestTaskCreationSelectingServiceDefaultsAllConfiguredModules(t *testing.T) {
	service := domain.GroupServiceConfig{
		ID: "service-orders", DisplayName: "订单服务", RepositoryID: "repo-orders",
		Enabled: true,
		Modules: []domain.ServiceModuleConfig{{ID: "api", Name: "API"}, {ID: "worker", Name: "Worker"}},
	}
	state := &taskCreationState{
		groupID:  "group-a",
		config:   domain.AppConfig{Groups: []domain.GroupConfig{{ID: "group-a", Services: []domain.GroupServiceConfig{service}}}},
		draft:    domain.RequirementDraftState{Branch: "feature/example"},
		services: make(map[string]bool), modules: make(map[string]map[string]bool),
		moduleData: make(map[string]map[string]*taskCreationModuleDraft), moduleOrder: make(map[string][]string),
	}
	server := &app{taskCreation: state}
	server.ensureTaskCreationService(service.ID)
	if !state.services[service.ID] || len(state.modules[service.ID]) != len(service.Modules) {
		t.Fatalf("selecting a service should default all configured modules: %+v", state.modules)
	}
	for _, module := range service.Modules {
		if !state.modules[service.ID][module.ID] {
			t.Fatalf("module %q should be selected by default", module.ID)
		}
	}
}

func TestTaskAppendServiceMatchesNameIDAndRepositoryPath(t *testing.T) {
	service := domain.GroupServiceConfig{ID: "service-orders", DisplayName: "订单服务", RepositoryID: "repo-orders"}
	cases := []struct {
		query string
		want  bool
	}{
		{query: "订单", want: true},
		{query: "service-orders", want: true},
		{query: "workspace\\orders", want: true},
		{query: "missing", want: false},
	}
	for _, test := range cases {
		t.Run(test.query, func(t *testing.T) {
			if got := taskAppendServiceMatches(service, `D:\workspace\orders`, test.query); got != test.want {
				t.Fatalf("taskAppendServiceMatches(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func TestTaskAppendSelectingServiceDefaultsAllConfiguredModules(t *testing.T) {
	service := domain.GroupServiceConfig{
		ID: "service-orders", DisplayName: "订单服务", RepositoryID: "repo-orders",
		Modules: []domain.ServiceModuleConfig{{ID: "api", Name: "API"}, {ID: "worker", Name: "Worker"}},
	}
	state := taskAppendNewState(task{}, "services", domain.GroupConfig{})
	state.serviceOrder = []string{service.ID}
	state.services[service.ID] = service
	server := &app{workspaceAppend: state}
	server.toggleTaskAppendService(service.ID)
	if !state.selectedServices[service.ID] || state.activeServiceID != service.ID || len(state.moduleData[service.ID]) != len(service.Modules) {
		t.Fatalf("selecting a service should activate it and default all configured modules: %+v", state)
	}
	for _, module := range service.Modules {
		if state.moduleData[service.ID][module.ID] == nil {
			t.Fatalf("module %q should be initialized", module.ID)
		}
	}
}

func TestTaskAppendPreviewInvalidationReturnsToCheckAction(t *testing.T) {
	state := &taskAppendUIState{
		previewSignature: "old",
		conflicts:        []workspace.BranchReuseConflict{{}},
		sharedAck:        true,
		inlineError:      "选择已变化，请重新预检。",
	}
	server := &app{workspaceAppend: state}
	server.invalidateTaskAppendPreview("服务或模块选择已变化，请重新预检。")
	if state.previewSignature != "" || len(state.conflicts) != 0 || state.sharedAck || state.inlineError != "" {
		t.Fatalf("invalidation should clear stale preview state and hints: %+v", state)
	}
}

func TestTaskCreationPreviewInvalidationReturnsToCheckAction(t *testing.T) {
	state := &taskCreationState{phase: "confirm", preview: &taskCreationPreview{}, inlineError: "内容已变化，请重新预检。"}
	server := &app{taskCreation: state}
	server.invalidateTaskCreationPreview("内容已变化，请重新预检。")
	if state.phase != "editing" || state.preview != nil || state.inlineError != "" {
		t.Fatalf("invalidation should return to editing without a persistent hint: %+v", state)
	}
}
