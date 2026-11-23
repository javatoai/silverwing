package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type taskServiceRow struct {
	serviceID, name, genbuName, environment string
	pods                                    []production.RuntimePod
	loaded, loading                         bool
	error, fetchedAt                        string
}

func (row *taskServiceRow) key() string { return row.serviceID + ":" + row.environment }

type taskServiceDetailKind uint8

const (
	taskServicePodsDetail taskServiceDetailKind = iota
	taskServiceConfigDetail
	taskServiceConfigEditDetail
)

type taskServiceDialog struct {
	open                 bool
	kind                 taskServiceDetailKind
	row                  *taskServiceRow
	vertical, horizontal ui.ScrollState
	config               taskServiceConfigView
	edit                 taskServiceConfigEditView
}

type taskServiceConfigView struct {
	text, error, namespace string
	namespaceOptions       []string
	loaded, loading        bool
	preview                nativeCodePreviewState
}

type taskServicesView struct {
	identity                  taskFilesIdentity
	catalog                   *catalogRuntimeScope
	queryIdentity, membership string
	active                    bool
	epoch                     uint64
	rows                      []*taskServiceRow
	hasServices               bool
	environment               string
	dialog                    *taskServiceDialog
	vertical                  ui.ScrollState
	canvas                    ui.Handle
	layout                    taskServicesLayout
}

func (v *taskServicesView) documentID(key string) string {
	if v.catalog != nil {
		return strings.Join([]string{"service-catalog", v.catalog.home, v.catalog.profile, key}, "\x00")
	}
	return v.identity.documentID(key)
}

func (v *taskServicesView) dialogScope(row *taskServiceRow) string {
	environment := strings.ToUpper(row.environment)
	if v.catalog != nil {
		return environment + " · " + row.name
	}
	return row.name + " · " + environment
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
	configSubmissions  map[string]bool
	views              map[string]*taskServicesView
	current, catalog   *taskServicesView
	catalogEnvironment string
	jobs               map[string]*taskServicesRequest
	wg                 sync.WaitGroup
	stopped            bool
}

func (s *app) taskServicesPageActive() bool {
	return (s.page == "tasks" || s.page == "archive") && s.tab == taskServicesTab && s.taskContentTabAvailable(taskServicesTab) && !s.workspaceReviewPageVisible()
}

func (s *app) taskServicesProjection() (*taskServicesView, string) {
	id := s.taskFilesIdentity()
	if id.id == "" || id.archived != (s.page == "archive") {
		return nil, ""
	}
	cfg, err := s.readSnapshotAppConfig()
	if err != nil {
		return nil, ""
	}
	t := s.taskUnit.currentTask()
	v := &taskServicesView{identity: id, queryIdentity: application.TaskServiceQueryIdentity(cfg), environment: "uat"}
	v.hasServices = len(t.workspaces) > 0 || len(t.serviceIDs) > 0
	seen := map[string]bool{}
	appendService := func(serviceID, name string) {
		if serviceID == "" {
			matches := 0
			for _, candidate := range cfg.ServiceCatalog() {
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
		service, lookupErr := cfg.Service(serviceID)
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
	parts := []string{id.documentID("services"), v.queryIdentity, fmt.Sprint(v.hasServices)}
	for _, row := range v.rows {
		parts = append(parts, row.key(), row.name, row.genbuName)
	}
	v.membership = strings.Join(parts, "\x00")
	return v, v.membership
}

func (s *app) taskServicesCurrent(v *taskServicesView) bool {
	if v == nil || !v.active || s.taskServicesUnit.stopped {
		return false
	}
	if v.catalog != nil {
		return s.catalogRuntimeCurrent(v)
	}
	if s.taskServicesUnit.current != v || !s.taskServicesPageActive() {
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
		s.leaveTaskServices()
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
		v.vertical = ui.ScrollState{}
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
	s.closeTaskServiceDialog(v, v.dialog)
	if v.catalog == nil || (s.genbuApprovals != nil && s.genbuApprovals.source.view == v) {
		s.closeGenbuReadDialogs()
	}
	v.epoch++
	for key, request := range s.taskServicesUnit.jobs {
		if request.view == v {
			request.cancel()
			delete(s.taskServicesUnit.jobs, key)
		}
	}
	for _, row := range v.rows {
		row.loading = false
	}
}

func (s *app) selectTaskServicesEnvironment(v *taskServicesView, environment string) bool {
	if (environment != "uat" && environment != "prod") || v == nil || v.environment == environment || !s.taskServicesCurrent(v) {
		return false
	}
	s.cancelTaskServicesRequests(v)
	v.environment = environment
	v.vertical = ui.ScrollState{}
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
}

func (s *app) taskServiceDialogCurrent(v *taskServicesView, dialog *taskServiceDialog) bool {
	return dialog != nil && dialog.open && v != nil && v.dialog == dialog &&
		dialog.row.environment == v.environment && s.taskServicesCurrent(v)
}

func (s *app) openTaskServiceDialog(v *taskServicesView, row *taskServiceRow, kind taskServiceDetailKind) {
	if !s.taskServicesCurrent(v) || row == nil || row.environment != v.environment || !slices.Contains(v.rows, row) {
		return
	}
	if kind == taskServiceConfigEditDetail && (v.identity.archived || row.genbuName == "") {
		return
	}
	if v.dialog != nil && v.dialog.open && v.dialog.row == row && v.dialog.kind == kind {
		return
	}
	s.closeTaskServiceDialog(v, v.dialog)
	s.closeGenbuReadDialogs()
	dialog := &taskServiceDialog{open: true, kind: kind, row: row}
	v.dialog = dialog
	switch kind {
	case taskServicePodsDetail:
		if v.catalog != nil {
			s.requestTaskServicePods(v, row)
		}
	case taskServiceConfigDetail:
		s.requestTaskServiceConfig(v, dialog)
	case taskServiceConfigEditDetail:
		s.requestTaskServiceConfigFiles(v, dialog)
	}
}

func (s *app) closeTaskServiceDialog(v *taskServicesView, dialog *taskServiceDialog) {
	if v == nil || dialog == nil || v.dialog != dialog {
		return
	}
	v.dialog = nil
	dialog.open = false
	switch dialog.kind {
	case taskServicePodsDetail:
		if v.catalog != nil {
			s.cancelTaskServiceJob(v, "pods:"+dialog.row.key())
			dialog.row.loading = false
		}
	case taskServiceConfigDetail:
		s.cancelTaskServiceJob(v, "config:"+dialog.row.key())
		dialog.config = taskServiceConfigView{}
	case taskServiceConfigEditDetail:
		s.resetTaskServiceConfigEdit(v, dialog)
	}
}

func (s *app) stopTaskServices() {
	u := &s.taskServicesUnit
	s.leaveTaskServices()
	s.leaveCatalogRuntime()
	s.closeGenbuReadDialogs()
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
	if v.catalog != nil {
		return application.Action{Kind: "service.runtime." + strings.TrimPrefix(kind, "task.services."), ServiceID: row.serviceID, Payload: map[string]any{
			"environment":           row.environment,
			"serviceRuntimeContext": application.ServiceRuntimeContext{Home: v.catalog.home, GenbuServiceName: row.genbuName, QueryIdentity: v.queryIdentity},
		}}
	}
	id := v.identity
	return application.Action{Kind: kind, TaskID: id.id, ServiceID: row.serviceID, Payload: map[string]any{
		"environment":         row.environment,
		"taskServicesContext": application.TaskServicesContext{TaskFilesContext: application.TaskFilesContext{Home: id.home, TaskPath: id.taskPath, TaskCreatedAt: id.createdAt, TaskRoot: id.taskRoot, Archived: id.archived}, GenbuServiceName: row.genbuName, QueryIdentity: v.queryIdentity},
	}}
}

func (s *app) runTaskServiceJob(v *taskServicesView, key string, action application.Action, publish func(application.ActionResult, error)) {
	s.runTaskServiceJobWithDrop(v, key, action, publish, nil)
}

func (s *app) runTaskServiceJobWithDrop(v *taskServicesView, key string, action application.Action, publish func(application.ActionResult, error), drop func(application.ActionResult)) {
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
		var complete sync.Once
		discard := func() {
			if drop != nil {
				complete.Do(func() { drop(result) })
			}
		}
		if ctx.Err() != nil || win.IsDestroyed() {
			discard()
			return
		}
		done := make(chan struct{})
		win.Update(func() {
			defer close(done)
			if ctx.Err() != nil || u.jobs[key] != request || !s.taskServicesCurrent(v) || v.epoch != request.epoch || v.environment != request.environment {
				discard()
				return
			}
			delete(u.jobs, key)
			complete.Do(func() { publish(result, err) })
		})
		select {
		case <-done:
		case <-ctx.Done():
			discard()
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

func (s *app) requestTaskServiceConfig(v *taskServicesView, dialog *taskServiceDialog) {
	if !s.taskServiceDialogCurrent(v, dialog) || dialog.kind != taskServiceConfigDetail {
		return
	}
	row, config := dialog.row, &dialog.config
	if row.genbuName == "" {
		config.error = "请先配置 Genbu 服务名"
		return
	}
	config.loading = true
	config.error = ""
	requested := config.namespace
	action := s.taskServiceAction(v, row, "task.services.config")
	if requested != "" {
		action.Payload["namespace"] = requested
	}
	s.runTaskServiceJob(v, "config:"+row.key(), action, func(result application.ActionResult, err error) {
		s.finishTaskServiceConfig(v, dialog, requested, result, err)
	})
}

func (s *app) finishTaskServiceConfig(v *taskServicesView, dialog *taskServiceDialog, requested string, result application.ActionResult, err error) {
	if !s.taskServiceDialogCurrent(v, dialog) || dialog.config.namespace != requested {
		return
	}
	row, config := dialog.row, &dialog.config
	config.loading = false
	config.error = ""
	var ambiguity *production.ServiceConfigNamespaceError
	if requested == "" && errors.As(err, &ambiguity) {
		config.text, config.loaded, config.preview = "", false, nativeCodePreviewState{}
		config.namespaceOptions = slices.Clone(ambiguity.Candidates)
		return
	}
	if err == nil {
		data, decodeErr := decodeTaskFilesResult[application.TaskServiceConfigResult](result)
		err = decodeErr
		if err == nil && (data.ServiceID != row.serviceID || data.Environment != row.environment || data.GenbuServiceName != row.genbuName || data.Namespace != requested) {
			err = fmt.Errorf("配置返回的服务、环境或 namespace 不匹配")
		}
		if err == nil {
			config.text = data.Text
			config.loaded = true
			return
		}
	}
	config.error = err.Error()
}

func (s *app) selectTaskServiceConfigNamespace(v *taskServicesView, dialog *taskServiceDialog, namespace string) {
	if !s.taskServiceDialogCurrent(v, dialog) {
		return
	}
	config := &dialog.config
	if config.namespace == namespace || !slices.Contains(config.namespaceOptions, namespace) {
		return
	}
	config.namespace = namespace
	config.text, config.loaded, config.preview = "", false, nativeCodePreviewState{}
	s.requestTaskServiceConfig(v, dialog)
}

func (s *app) cancelTaskServiceJob(v *taskServicesView, key string) {
	if job := s.taskServicesUnit.jobs[key]; job != nil && job.view == v {
		job.cancel()
		delete(s.taskServicesUnit.jobs, key)
	}
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
