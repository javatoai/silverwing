package desktop

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
)

func taskServicesDesktopFixture(t *testing.T) *app {
	t.Helper()
	cfg := domain.DefaultAppConfig()
	genbuEnabled := true
	cfg.OptionalCLIs.Genbu = &genbuEnabled
	root := "D:/isolated-services"
	cfg.TaskRoot = &root
	cfg.Services = []domain.GroupServiceConfig{{ID: "s1", DisplayName: "A service", GenbuServiceName: "alias-one", GenbuProbeEnabled: true}, {ID: "s2", DisplayName: "Second service", GenbuServiceName: "alias-two", GenbuProbeEnabled: true}}
	cfg.Repositories = []domain.RepositoryConfig{}
	for i := range cfg.Services {
		service := &cfg.Services[i]
		service.RepositoryID = "repo-" + service.ID
		cfg.Repositories = append(cfg.Repositories, domain.RepositoryConfig{ID: service.RepositoryID, Name: service.ID, RootPath: root + "/" + service.ID, GitCommonDirectory: root + "/" + service.ID + "/.git"})
		service.MasterBranch = "origin/main"
		service.TestTagBaselineRef = "origin/release/test"
		service.DevelopmentTool = domain.DevelopmentToolVisualStudioCode
		service.Modules = []domain.ServiceModuleConfig{{ID: "main", Name: "main", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}}
	}
	store := storage.NewConfigStore(storage.NewPaths(t.TempDir()))
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var err error
	cfg, err = store.LoadForPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := &app{page: "tasks", tab: taskServicesTab, snapshot: application.Snapshot{Home: "D:/isolated-home", ConfigJSON: string(data)}}
	s.settingsUnit.state.configJSON = string(data)
	seedPlatformConnectionsForDesktopTest(s, true, false)
	s.taskUnit.state.tasks = []task{{id: "first", title: "First task", groupID: domain.DefaultGroupID, path: root + "/first", createdAt: "2026-11-02", serviceIDs: []string{"s2", "s1", "s2"}, workspaces: []application.Workspace{{ServiceID: "s2", Name: "Second service"}, {ServiceID: "s1", Name: "A service", ModuleID: "module-one"}, {ServiceID: "s1", Name: "A service", ModuleID: "module-two"}}}}
	return s
}

func TestTaskServicesProjectionOrderDedupAndQueryIdentity(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, key := s.taskServicesProjection()
	if v == nil {
		t.Fatal("valid fixture projection returned nil")
	}
	if len(v.rows) != 4 || v.rows[0].serviceID != "s2" || v.rows[1].environment != "prod" || v.rows[2].serviceID != "s1" || v.rows[2].environment != "uat" {
		t.Fatal(v.rows)
	}
	var cfg domain.AppConfig
	json.Unmarshal([]byte(s.snapshot.ConfigJSON), &cfg)
	old := application.TaskServiceQueryIdentity(cfg)
	disabled := false
	cfg.OptionalCLIs.Genbu = &disabled
	data, _ := json.Marshal(cfg)
	s.snapshot.ConfigJSON = string(data)
	_, changed := s.taskServicesProjection()
	if old == application.TaskServiceQueryIdentity(cfg) || key == changed {
		t.Fatal("Genbu switch change did not invalidate service ownership")
	}
	s.taskUnit.state.tasks[0].serviceIDs = []string{"s2"}
	s.taskUnit.state.tasks[0].workspaces = nil
	v, _ = s.taskServicesProjection()
	if len(v.rows) != 2 {
		t.Fatal("removed service retained", v.rows)
	}
}

func TestTaskServicesConsumesSnapshotWorkspaceIDsNotDisplayNames(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	s.snapshot.Tasks = []application.Task{{ID: "snapshot-task", Title: "Actual Snapshot shape", GroupID: domain.DefaultGroupID, Path: "D:/isolated-services/snapshot-task", CreatedAt: "2026-11-02", Services: []string{"A service", "A service", "Second service"}, Workspaces: []application.Workspace{{ServiceID: "s1", Name: "A service", ModuleID: "one"}, {ServiceID: "s1", Name: "A service", ModuleID: "two"}, {ServiceID: "s2", Name: "Second service", ModuleID: "main"}}}}
	s.taskUnit.state.tasks = projectTasks(s.snapshot)
	v, _ := s.taskServicesProjection()
	if v == nil || len(v.rows) != 4 || v.rows[0].serviceID != "s1" || v.rows[2].serviceID != "s2" {
		t.Fatal("display names became extra service IDs", v)
	}
	for _, row := range v.rows {
		if row.genbuName == "" {
			t.Fatal("configured service lost mapping", row)
		}
	}
}

func TestTaskServicesSummaryAllVersionsReadyAndRawCreationTimes(t *testing.T) {
	pods := []production.RuntimePod{{Namespace: "fp", Version: "long-v2", Ready: true, CreatedTime: "2026-11-02 12:34:56"}, {Namespace: "fp", Version: "v1", Ready: true, CreatedTime: "2026-11-01 10:11:12"}, {Namespace: "side", Version: "long-v2", Ready: false, CreatedTime: ""}}
	versions := taskServiceVersions(pods)
	if len(versions) != 2 || versions[0] != (taskServiceVersionCount{"long-v2", 2}) || versions[1] != (taskServiceVersionCount{"v1", 1}) {
		t.Fatal(versions)
	}
	if got := taskServiceFirstCreationTime(pods); got != "2026-11-02 12:34:56" {
		t.Fatal("summary must use first returned Pod, not earliest time", got)
	}
	if taskServiceFirstCreationTime(nil) != "—" || taskServiceFirstCreationTime([]production.RuntimePod{{}, {CreatedTime: "later-known-time"}}) != "—" {
		t.Fatal("empty Pods manufactured a timestamp")
	}
}

func TestTaskServicesEnvironmentSwitchCancelsClosesAndRetainsIndependentCache(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v := s.ensureTaskServicesCurrent()
	if v == nil {
		t.Fatal("missing view")
	}
	for _, row := range v.rows {
		row.loaded = true
		row.pods = []production.RuntimePod{{Name: row.environment + "-cached"}}
	}
	s.openTaskServiceDialog(v, v.rows[0], taskServiceConfigDetail)
	dialog := v.dialog
	dialog.config.text = "memory-only data"
	v.vertical.Y = 120
	cancelled := false
	s.taskServicesUnit.jobs = map[string]*taskServicesRequest{"old-env": {view: v, epoch: v.epoch, environment: "uat", cancel: func() { cancelled = true }}}
	before := v.epoch
	if !s.selectTaskServicesEnvironment(v, "prod") || !cancelled || v.epoch <= before || v.dialog != nil || dialog.open || dialog.config.text != "" || v.vertical.Y != 0 || len(s.taskServicesUnit.jobs) != 0 {
		t.Fatal("environment switch retained old request, detail or position", v)
	}
	for _, row := range v.visibleRows() {
		if row.environment != "prod" || !row.loaded || row.pods[0].Name != "prod-cached" {
			t.Fatal("wrong environment cache", row)
		}
	}
	if s.selectTaskServicesEnvironment(v, "prod") || s.selectTaskServicesEnvironment(v, "invalid") {
		t.Fatal("same/invalid environment started another load")
	}
}

func TestTaskServicesEachEntryDefaultsUATAndRefreshOnlyCurrentEnvironment(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, key := s.taskServicesProjection()
	s.taskServicesUnit.current = v
	s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
	for _, row := range v.rows {
		row.loaded = true
		row.error = "untouched"
		row.pods = []production.RuntimePod{{Name: "cached"}}
	}
	v.environment = "prod"
	if current := s.ensureTaskServicesCurrent(); current != v || v.environment != "uat" || v.dialog != nil {
		t.Fatal("entry did not default to UAT")
	}
	for _, row := range v.rows {
		if row.environment == "prod" && row.error != "untouched" {
			t.Fatal("entry queried hidden PROD", row)
		}
	}
	if !s.selectTaskServicesEnvironment(v, "prod") {
		t.Fatal("cannot switch PROD")
	}
	for _, row := range v.rows {
		if row.environment == "uat" {
			row.error = "uat-retained"
		}
	}
	s.refreshTaskServicesEnvironment(v)
	if v.environment != "prod" {
		t.Fatal("refresh reset selected environment")
	}
	for _, row := range v.rows {
		if row.environment == "uat" && row.error != "uat-retained" {
			t.Fatal("refresh queried hidden UAT", row)
		}
	}
	s.leaveTaskServices()
	s.ensureTaskServicesCurrent()
	if v.environment != "uat" {
		t.Fatal("return to page retained PROD")
	}
}

func TestTaskServicesLeaveCancelsAndDropsOnlyConfigurationBodies(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, key := s.taskServicesProjection()
	s.taskServicesUnit.current = v
	s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
	v.active = true
	v.epoch = 1
	row := v.rows[0]
	row.loaded = true
	row.pods = []production.RuntimePod{{Name: "kept-pod"}}
	s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
	dialog := v.dialog
	dialog.config.text = "safe: <redacted>"
	dialog.config.loaded = true
	cancelled := false
	s.taskServicesUnit.jobs = map[string]*taskServicesRequest{"test": {view: v, cancel: func() { cancelled = true }}}
	s.leaveTaskServices()
	if !cancelled || v.active || len(s.taskServicesUnit.jobs) != 0 || v.dialog != nil || dialog.config.text != "" || dialog.config.loaded || !row.loaded || len(row.pods) != 1 {
		t.Fatal("leave lost reusable Pods or retained config/request", row)
	}
}

func TestTaskServicesDialogCloseCancelsOnlyItsConfigAndDiscardsNamespace(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v := s.ensureTaskServicesCurrent()
	row := v.rows[0]
	s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
	old := v.dialog
	old.config.namespace = "selected"
	old.config.namespaceOptions = []string{"other", "selected"}
	old.config.loaded = true
	old.config.text = "safe: <redacted>"
	old.config.preview.bind("kept", "config.txt", old.config.text)

	podCanceled, configCanceled := false, false
	s.taskServicesUnit.jobs["pods:"+row.key()] = &taskServicesRequest{view: v, cancel: func() { podCanceled = true }}
	s.taskServicesUnit.jobs["config:"+row.key()] = &taskServicesRequest{view: v, cancel: func() { configCanceled = true }}
	s.closeTaskServiceDialog(v, old)
	if v.dialog != nil || old.open || !reflect.DeepEqual(old.config, taskServiceConfigView{}) {
		t.Fatal("closing must dispose the config preview and namespace choices")
	}
	if !configCanceled || podCanceled || len(s.taskServicesUnit.jobs) != 1 {
		t.Fatal("closing did not cancel only the config read")
	}

	s.openTaskServiceDialog(v, row, taskServicePodsDetail)
	current := v.dialog
	s.closeTaskServiceDialog(v, old)
	s.requestTaskServiceConfig(v, old)
	if current == nil || v.dialog != current || !current.open || !reflect.DeepEqual(old.config, taskServiceConfigView{}) || podCanceled {
		t.Fatal("stale dialog changed the current modal or its Pod request")
	}
	s.leaveTaskServices()
	if !podCanceled {
		t.Fatal("leaving the page retained the Pod request")
	}
}

func TestTaskServicesArchiveConfigUsesActiveScopeAndDiscardsLateResult(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	s.page, s.taskUnit.state.tasks[0].archived = "archive", true
	v := s.ensureTaskServicesCurrent()
	if !s.selectTaskServicesEnvironment(v, "prod") {
		t.Fatal("cannot select archived PROD scope")
	}
	row := v.rows[1]
	row.pods = []production.RuntimePod{{Name: "existing-pod", Namespace: "fp"}, {Name: "other-pod", Namespace: "ops"}}
	calls := make(chan application.Action, 2)
	s.win = &mygo.Window{}
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, action application.Action) (application.ActionResult, error) {
		calls <- action
		<-ctx.Done()
		return application.ActionResult{Data: application.TaskServiceConfigResult{
			ServiceID: row.serviceID, GenbuServiceName: row.genbuName,
			Environment: "prod", Text: "old: <redacted>",
		}}, nil
	}}
	t.Cleanup(s.stopTaskServices)

	s.openTaskServiceDialog(v, row, taskServicePodsDetail)
	if v.dialog == nil || len(s.taskServicesUnit.jobs) != 0 {
		t.Fatal("opening Pod details started a read")
	}
	s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
	old := v.dialog
	select {
	case action := <-calls:
		scope, ok := action.Payload["taskServicesContext"].(application.TaskServicesContext)
		if action.Kind != "task.services.config" || action.TaskID != v.identity.id || action.ServiceID != row.serviceID ||
			action.Payload["environment"] != "prod" || action.Payload["namespace"] != nil || !ok ||
			!scope.Archived || scope.GenbuServiceName != row.genbuName {
			t.Fatalf("wrong archive configuration request: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("no configuration read")
	}
	request := s.taskServicesUnit.jobs["config:"+row.key()]
	s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
	if request == nil || s.taskServicesUnit.jobs["config:"+row.key()] != request || v.dialog != old {
		t.Fatal("reopening an already active dialog started another read")
	}
	s.closeTaskServiceDialog(v, old)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.closeTaskServices(ctx); err != nil {
		t.Fatal(err)
	}
	if request.ctx.Err() != context.Canceled || v.dialog != nil || !reflect.DeepEqual(old.config, taskServiceConfigView{}) {
		t.Fatal("the dismissed modal retained its config request or content")
	}
}

func TestTaskServicesDialogInvalidatesWithTaskAndPageScope(t *testing.T) {
	for name, change := range map[string]func(*app){
		"another task": func(s *app) { s.taskUnit.state.tasks[0].id = "other-task" },
		"removed task": func(s *app) { s.taskUnit.state.tasks = nil },
		"archive page": func(s *app) { s.page = "archive" },
		"other tab":    func(s *app) { s.tab = taskFilesTab },
		"other page":   func(s *app) { s.page = "services" },
	} {
		t.Run(name, func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v := s.ensureTaskServicesCurrent()
			s.openTaskServiceDialog(v, v.rows[0], taskServicePodsDetail)
			dialog := v.dialog
			change(s)
			tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServiceDialog(c, setTheme(c, false)) }, 1100, 700)
			if tester.HasText("Pod 明细") || v.dialog != nil || dialog.open {
				t.Fatal("stale modal remained on screen")
			}
		})
	}
}

func TestTaskServicesEntryDoesNotStartMaterialsOrGit(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	s.tab = 0
	s.materialsUnit.state.documentPath = "draft.md"
	s.materialsUnit.state.documentContent = "unsaved"
	s.materialsUnit.state.documentDirty = true
	if !s.selectTaskContentTab(taskServicesTab) || s.materialsUnit.state.documentsRequested || s.workspaceUnit.state.workspaceGit != nil || s.materialsUnit.state.documentContent != "unsaved" || !s.materialsUnit.state.documentDirty {
		t.Fatal("service entry changed materials/Git")
	}
}

func TestTaskList108MinimumAndCompactThreshold(t *testing.T) {
	if taskListPaneMinWidth != 108 || clampTaskListPaneWidth(80, 1000) != 108 || clampTaskListPaneWidth(240, 1200) != 240 {
		t.Fatal("list width clamp changed default or minimum")
	}
	s := &app{listWidth: 108}
	if !s.taskIndexCompact() {
		t.Fatal("108 is not compact")
	}
	s.taskListPaneLayout().width = 159
	if !s.taskIndexCompact() {
		t.Fatal("159 is not compact")
	}
	s.taskListPaneLayout().width = 160
	if s.taskIndexCompact() {
		t.Fatal("160 still hides status label")
	}
}
