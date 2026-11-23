package desktop

import (
	"context"
	"strings"

	"silverwing-go/internal/application"
)

type catalogRuntimeScope struct {
	home, profile string
	connection    context.Context
}

func (s *app) catalogRuntimePageActive() bool {
	return s.page == "repositories" || s.page == "services"
}

func (s *app) catalogRuntimeEnvironment() string {
	return firstNonEmpty(s.taskServicesUnit.catalogEnvironment, "uat")
}

func (s *app) selectCatalogRuntimeEnvironment(environment string) {
	if !s.catalogRuntimePageActive() || (environment != "uat" && environment != "prod") || environment == s.catalogRuntimeEnvironment() {
		return
	}
	s.leaveCatalogRuntime()
	s.taskServicesUnit.catalogEnvironment = environment
}

func (s *app) observeCatalogRuntime() {
	if !s.catalogRuntimePageActive() {
		s.leaveCatalogRuntime()
		s.taskServicesUnit.catalogEnvironment = ""
	} else if view := s.taskServicesUnit.catalog; view != nil && !s.taskServicesCurrent(view) {
		s.leaveCatalogRuntime()
	}
}

func (s *app) leaveCatalogRuntime() {
	v := s.taskServicesUnit.catalog
	if v == nil {
		return
	}
	s.taskServicesUnit.catalog = nil
	v.active = false
	s.cancelTaskServicesRequests(v)
}

func (s *app) catalogRuntimeTarget(serviceID string) (taskServiceRow, string, bool) {
	if !s.catalogRuntimePageActive() || s.taskServicesUnit.stopped || s.workspaceUnit.state.serviceReorderMode ||
		s.snapshot.Home == "" || !s.integrationReady("genbu") {
		return taskServiceRow{}, "", false
	}
	cfg, err := s.readSnapshotAppConfig()
	if err != nil {
		return taskServiceRow{}, "", false
	}
	service, err := cfg.Service(serviceID)
	if err != nil || !service.Enabled || !service.GenbuProbeEnabled || strings.TrimSpace(service.GenbuServiceName) == "" {
		return taskServiceRow{}, "", false
	}
	return taskServiceRow{
		serviceID: service.ID, name: firstNonEmpty(service.DisplayName, service.ID),
		genbuName: service.GenbuServiceName, environment: s.catalogRuntimeEnvironment(),
	}, application.TaskServiceQueryIdentity(cfg), true
}

func (s *app) catalogRuntimeCurrent(v *taskServicesView) bool {
	if s.taskServicesUnit.catalog != v || v.catalog.home != s.snapshot.Home || v.catalog.profile != s.snapshot.ProfileID ||
		v.catalog.connection.Err() != nil {
		return false
	}
	row := v.rows[0]
	target, queryIdentity, ok := s.catalogRuntimeTarget(row.serviceID)
	return ok && target.name == row.name && target.genbuName == row.genbuName &&
		target.environment == v.environment && queryIdentity == v.queryIdentity
}

func (s *app) openCatalogRuntime(serviceID string) (*taskServicesView, *taskServiceRow) {
	row, queryIdentity, ok := s.catalogRuntimeTarget(serviceID)
	if !ok {
		return nil, nil
	}
	s.leaveTaskServices()
	s.leaveCatalogRuntime()
	v := &taskServicesView{
		catalog: &catalogRuntimeScope{home: s.snapshot.Home, profile: s.snapshot.ProfileID, connection: s.integrationContext("genbu")},
		active:  true, queryIdentity: queryIdentity, rows: []*taskServiceRow{&row}, environment: row.environment,
	}
	s.taskServicesUnit.catalog = v
	return v, &row
}

func (s *app) openCatalogRuntimeDialog(serviceID string, kind taskServiceDetailKind) {
	v, row := s.openCatalogRuntime(serviceID)
	if v != nil {
		s.openTaskServiceDialog(v, row, kind)
	}
}

func (s *app) openCatalogRuntimeApprovals(serviceID string) {
	v, row := s.openCatalogRuntime(serviceID)
	if v != nil {
		s.openTaskServiceApprovals(v, row, 0)
	}
}
