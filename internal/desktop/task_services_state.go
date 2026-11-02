package desktop

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type taskServiceRow struct {
	serviceID, name, genbuName, environment             string
	pods                                                []production.RuntimePod
	loaded, loading                                     bool
	error, fetchedAt                                    string
	namespace, configText, configError, configFetchedAt string
	configLoaded, configLoading                         bool
	configView                                          nativeCodePreviewState
}

func (row *taskServiceRow) key() string { return row.serviceID + ":" + row.environment }

type taskServicesView struct {
	identity                           taskFilesIdentity
	queryIdentity, groupID, membership string
	active                             bool
	epoch                              uint64
	rows                               []*taskServiceRow
	hasServices                        bool
	environment                        string
	expanded, detail                   string
	vertical, horizontal               ui.ScrollState
	canvas                             ui.Handle
	layout                             taskServicesLayout
}

// Bounds are committed after a native frame. A host layout change needs one
// follow-up frame before pointer input uses the new table column positions.
type taskServicesLayout struct {
	windowWidth, windowHeight, paneWidth float32
	navigationCollapsed                  bool
}

type taskServicesRequest struct {
	cancel      context.CancelFunc
	ctx         context.Context
	view        *taskServicesView
	epoch       uint64
	environment string
}

type taskServicesController struct {
	views   map[string]*taskServicesView
	current *taskServicesView
	jobs    map[string]*taskServicesRequest
	wg      sync.WaitGroup
	stopped bool
}

func (s *app) taskServicesPageActive() bool {
	return (s.page == "tasks" || s.page == "archive") && s.tab == taskServicesTab && !s.workspaceReviewPageVisible()
}

func (s *app) taskServicesProjection() (*taskServicesView, string) {
	id := s.taskFilesIdentity()
	if id.id == "" {
		return nil, ""
	}
	cfg, err := s.readSnapshotAppConfig()
	if err != nil {
		return nil, ""
	}
	t := s.taskUnit.currentTask()
	groupID := t.groupID
	if groupID == "" {
		groupID = domain.DefaultGroupID
	}
	v := &taskServicesView{identity: id, groupID: groupID, queryIdentity: application.TaskServiceQueryIdentity(cfg), environment: "uat"}
	v.hasServices = len(t.workspaces) > 0 || len(t.serviceIDs) > 0
	seen := map[string]bool{}
	appendService := func(serviceID, name string) {
		if serviceID == "" {
			group, _ := cfg.Group(groupID)
			matches := 0
			for _, candidate := range group.Services {
				if candidate.DisplayName == name {
					serviceID = candidate.ID
					matches++
				}
			}
			if matches != 1 {
				serviceID = "unmapped:" + name
			}
		}
		if seen[serviceID] {
			return
		}
		seen[serviceID] = true
		service, lookupErr := cfg.GroupService(groupID, serviceID)
		if lookupErr != nil || !service.GenbuProbeEnabled {
			return
		}
		name = service.DisplayName
		genbu := service.GenbuServiceName
		if name == "" {
			name = serviceID
		}
		for _, env := range []string{"uat", "prod"} {
			v.rows = append(v.rows, &taskServiceRow{serviceID: serviceID, name: name, genbuName: genbu, environment: env})
		}
	}
	// Snapshot.Services contains display names. Workspace.ServiceID is the
	// stable membership identity and retains the manifest's module order.
	if len(t.workspaces) > 0 {
		for _, workspace := range t.workspaces {
			appendService(workspace.ServiceID, workspace.Name)
		}
	} else {
		for _, serviceID := range t.serviceIDs {
			appendService(serviceID, "")
		}
	}
	parts := []string{id.documentID("services"), v.queryIdentity, groupID, fmt.Sprint(v.hasServices)}
	for _, row := range v.rows {
		parts = append(parts, row.key(), row.name, row.genbuName)
	}
	v.membership = strings.Join(parts, "\x00")
	return v, v.membership
}

func (s *app) taskServicesCurrent(v *taskServicesView) bool {
	if v == nil || !v.active || s.taskServicesUnit.stopped || s.taskServicesUnit.current != v || !s.taskServicesPageActive() {
		return false
	}
	_, key := s.taskServicesProjection()
	return key != "" && key == v.membership
}

func (s *app) ensureTaskServicesCurrent() *taskServicesView {
	if !s.taskServicesPageActive() || s.taskServicesUnit.stopped {
		return nil
	}
	fresh, key := s.taskServicesProjection()
	if fresh == nil {
		return nil
	}
	u := &s.taskServicesUnit
	if u.current != nil && u.current.membership != key {
		s.leaveTaskServices()
	}
	if u.views == nil {
		u.views = map[string]*taskServicesView{}
	}
	v := u.views[key]
	if v == nil {
		v = fresh
		u.views[key] = v
	}
	u.current = v
	if !v.active {
		v.active = true
		v.epoch++
		v.environment = "uat"
		v.expanded, v.detail = "", ""
		v.vertical, v.horizontal = ui.ScrollState{}, ui.ScrollState{}
		s.refreshTaskServicesEnvironment(v)
	}
	return v
}

func (v *taskServicesView) visibleRows() []*taskServiceRow {
	rows := make([]*taskServiceRow, 0, len(v.rows)/2)
	for _, row := range v.rows {
		if row.environment == v.environment {
			rows = append(rows, row)
		}
	}
	return rows
}

func (s *app) cancelTaskServicesRequests(v *taskServicesView) {
	v.epoch++
	for key, request := range s.taskServicesUnit.jobs {
		if request.view == v {
			request.cancel()
			delete(s.taskServicesUnit.jobs, key)
		}
	}
	for _, row := range v.rows {
		row.loading = false
		row.configLoading = false
	}
}

func (s *app) selectTaskServicesEnvironment(v *taskServicesView, environment string) bool {
	if (environment != "uat" && environment != "prod") || v == nil || v.environment == environment || !s.taskServicesCurrent(v) {
		return false
	}
	s.cancelTaskServicesRequests(v)
	v.environment = environment
	v.expanded, v.detail = "", ""
	v.vertical, v.horizontal = ui.ScrollState{}, ui.ScrollState{}
	s.refreshTaskServicesEnvironment(v)
	return true
}

func (s *app) refreshTaskServicesEnvironment(v *taskServicesView) {
	if !s.taskServicesCurrent(v) {
		return
	}
	for _, row := range v.visibleRows() {
		s.requestTaskServicePods(v, row)
	}
}

func (s *app) leaveTaskServices() {
	u := &s.taskServicesUnit
	v := u.current
	if v == nil || !v.active {
		return
	}
	v.active = false
	s.cancelTaskServicesRequests(v)
	for _, row := range v.rows {
		row.loading = false
		row.configLoading = false
		row.configText = ""
		row.configLoaded = false
		row.configView = nativeCodePreviewState{}
		row.configError = ""
	}
}

func (s *app) stopTaskServices() {
	u := &s.taskServicesUnit
	s.leaveTaskServices()
	u.stopped = true
	for key, request := range u.jobs {
		request.cancel()
		delete(u.jobs, key)
	}
}

func (s *app) closeTaskServices(ctx context.Context) error {
	s.stopTaskServices()
	done := make(chan struct{})
	go func() { s.taskServicesUnit.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *app) taskServiceAction(v *taskServicesView, row *taskServiceRow, kind string) application.Action {
	id := v.identity
	namespace := ""
	if kind == "task.services.config" {
		namespace = row.namespace
	}
	return application.Action{Kind: kind, TaskID: id.id, ServiceID: row.serviceID, Payload: map[string]any{
		"environment": row.environment, "namespace": namespace,
		"taskServicesContext": application.TaskServicesContext{TaskFilesContext: application.TaskFilesContext{Home: id.home, TaskPath: id.taskPath, TaskCreatedAt: id.createdAt, TaskRoot: id.taskRoot, Archived: id.archived}, GroupID: v.groupID, GenbuServiceName: row.genbuName, QueryIdentity: v.queryIdentity},
	}}
}

func (s *app) runTaskServiceJob(v *taskServicesView, key string, action application.Action, publish func(application.ActionResult, error)) {
	environment, _ := action.Payload["environment"].(string)
	if !s.taskServicesCurrent(v) || environment != v.environment {
		return
	}
	u := &s.taskServicesUnit
	if old := u.jobs[key]; old != nil {
		old.cancel()
	}
	if u.jobs == nil {
		u.jobs = map[string]*taskServicesRequest{}
	}
	if s.win == nil || s.win.IsDestroyed() || s.backend == nil {
		publish(application.ActionResult{}, fmt.Errorf("Genbu 查询窗口或后端不可用"))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskServicesRequest{cancel: cancel, ctx: ctx, view: v, epoch: v.epoch, environment: environment}
	u.jobs[key] = request
	win, backend := s.win, s.backend
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		defer cancel()
		result, err := backend.Dispatch(ctx, action)
		if ctx.Err() != nil || win.IsDestroyed() {
			return
		}
		done := make(chan struct{})
		win.Update(func() {
			defer close(done)
			if ctx.Err() != nil || u.jobs[key] != request || !s.taskServicesCurrent(v) || v.epoch != request.epoch || v.environment != request.environment {
				return
			}
			delete(u.jobs, key)
			publish(result, err)
		})
		select {
		case <-done:
		case <-ctx.Done():
		}
	}()
}

func (s *app) requestTaskServicePods(v *taskServicesView, row *taskServiceRow) {
	if !s.taskServicesCurrent(v) || row.environment != v.environment {
		return
	}
	if row.genbuName == "" {
		row.error = "未配置 Genbu 服务映射，请在服务配置中补充"
		return
	}
	row.loading = true
	row.error = ""
	action := s.taskServiceAction(v, row, "task.services.pods")
	s.runTaskServiceJob(v, "pods:"+row.key(), action, func(result application.ActionResult, err error) {
		row.loading = false
		if err == nil {
			data, e := decodeTaskFilesResult[application.TaskServicePodsResult](result)
			err = e
			if err == nil && (data.ServiceID != row.serviceID || data.Environment != row.environment || data.GenbuServiceName != row.genbuName) {
				err = fmt.Errorf("服务 Pod 返回的身份或环境不匹配")
			}
			if err == nil {
				row.pods = data.Pods
				row.loaded = true
				row.fetchedAt = data.FetchedAt
				return
			}
		}
		row.error = err.Error()
	})
}

func (s *app) requestTaskServiceConfig(v *taskServicesView, row *taskServiceRow) {
	if !s.taskServicesCurrent(v) || row.environment != v.environment {
		return
	}
	row.namespace = strings.TrimSpace(row.namespace)
	if row.genbuName == "" {
		row.configError = "请先配置 Genbu 服务名"
		return
	}
	ns := taskServiceNamespaces(row.pods)
	if row.namespace == "" && len(ns) == 1 {
		row.namespace = ns[0]
	}
	if row.namespace == "" && len(ns) > 1 {
		row.configError = "请选择或输入 namespace 后读取配置"
		return
	}
	row.configLoading = true
	row.configError = ""
	requested := row.namespace
	action := s.taskServiceAction(v, row, "task.services.config")
	s.runTaskServiceJob(v, "config:"+row.key(), action, func(result application.ActionResult, err error) {
		row.configLoading = false
		if row.namespace != requested {
			return
		}
		if err == nil {
			data, e := decodeTaskFilesResult[application.TaskServiceConfigResult](result)
			err = e
			if err == nil && (data.ServiceID != row.serviceID || data.Environment != row.environment || data.GenbuServiceName != row.genbuName || data.Namespace != requested) {
				err = fmt.Errorf("配置返回的服务、环境或 namespace 不匹配")
			}
			if err == nil {
				row.configText = data.Text
				row.configLoaded = true
				row.configFetchedAt = data.FetchedAt
				row.configView = nativeCodePreviewState{}
				return
			}
		}
		row.configError = err.Error()
	})
}

func (s *app) changeTaskServiceNamespace(v *taskServicesView, row *taskServiceRow) {
	if old := s.taskServicesUnit.jobs["config:"+row.key()]; old != nil {
		old.cancel()
		delete(s.taskServicesUnit.jobs, "config:"+row.key())
	}
	row.configText = ""
	row.configLoaded = false
	row.configLoading = false
	row.configError = ""
	row.configView = nativeCodePreviewState{}
}

func taskServiceNamespaces(pods []production.RuntimePod) []string {
	seen := map[string]bool{}
	values := []string{}
	for _, pod := range pods {
		if pod.Namespace != "" && !seen[pod.Namespace] {
			seen[pod.Namespace] = true
			values = append(values, pod.Namespace)
		}
	}
	sort.Strings(values)
	return values
}

type taskServiceVersionCount struct {
	name  string
	count int
}

func taskServiceVersions(pods []production.RuntimePod) []taskServiceVersionCount {
	counts := map[string]int{}
	order := []string{}
	for _, pod := range pods {
		v := pod.Version
		if v == "" {
			v = "未知版本"
		}
		if counts[v] == 0 {
			order = append(order, v)
		}
		counts[v]++
	}
	values := []taskServiceVersionCount{}
	for _, version := range order {
		values = append(values, taskServiceVersionCount{version, counts[version]})
	}
	return values
}

func taskServiceFirstCreationTime(pods []production.RuntimePod) string {
	if len(pods) == 0 || strings.TrimSpace(pods[0].CreatedTime) == "" {
		return "—"
	}
	return pods[0].CreatedTime
}
