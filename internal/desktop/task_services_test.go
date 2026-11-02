package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
)

func taskServicesDesktopFixture(t *testing.T) *app {
	t.Helper()
	cfg := domain.DefaultAppConfig()
	root := "D:/isolated-services"
	cfg.TaskRoot = &root
	cfg.Groups[0].Services = []domain.GroupServiceConfig{{ID: "s1", DisplayName: "A service", GenbuServiceName: "alias-one", GenbuProbeEnabled: true}, {ID: "s2", DisplayName: "Second service", GenbuServiceName: "alias-two", GenbuProbeEnabled: true}}
	cfg.Repositories = []domain.RepositoryConfig{}
	for i := range cfg.Groups[0].Services {
		service := &cfg.Groups[0].Services[i]
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
	cfg.GenbuExecutablePath = stringPointerForServiceTest("other.exe")
	data, _ := json.Marshal(cfg)
	s.snapshot.ConfigJSON = string(data)
	_, changed := s.taskServicesProjection()
	if old == application.TaskServiceQueryIdentity(cfg) || key == changed {
		t.Fatal("CLI change did not invalidate service ownership")
	}
	s.taskUnit.state.tasks[0].serviceIDs = []string{"s2"}
	s.taskUnit.state.tasks[0].workspaces = nil
	v, _ = s.taskServicesProjection()
	if len(v.rows) != 2 {
		t.Fatal("removed service retained", v.rows)
	}
}

func stringPointerForServiceTest(text string) *string { return &text }

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

func TestTaskServicesNamespaceDraftDoesNotAffectPodsAndIsNormalizedForConfig(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v := s.ensureTaskServicesCurrent()
	if v == nil {
		t.Fatal("missing service view")
	}
	row := v.rows[0]
	row.namespace = "bad_namespace"
	if got := s.taskServiceAction(v, row, "task.services.pods").Payload["namespace"]; got != "" {
		t.Fatal("Pod query inherited configuration draft", got)
	}
	row.namespace = " fp "
	s.requestTaskServiceConfig(v, row)
	if row.namespace != "fp" {
		t.Fatal("config namespace was not normalized", row.namespace)
	}
	row.pods = []production.RuntimePod{{Namespace: "fp"}, {Namespace: "secondary"}}
	row.namespace = "  "
	s.requestTaskServiceConfig(v, row)
	if row.namespace != "" || !strings.Contains(row.configError, "请选择或输入") {
		t.Fatal("whitespace bypassed namespace choice", row)
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
	if got := taskServiceNamespaces(pods); len(got) != 2 || got[0] != "fp" || got[1] != "side" {
		t.Fatal(got)
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
	v.expanded = v.rows[0].key()
	v.detail = "config"
	v.vertical.Y = 120
	v.horizontal.X = 80
	cancelled := false
	s.taskServicesUnit.jobs = map[string]*taskServicesRequest{"old-env": {view: v, epoch: v.epoch, environment: "uat", cancel: func() { cancelled = true }}}
	before := v.epoch
	if !s.selectTaskServicesEnvironment(v, "prod") || !cancelled || v.epoch <= before || v.expanded != "" || v.detail != "" || v.vertical.Y != 0 || v.horizontal.X != 0 || len(s.taskServicesUnit.jobs) != 0 {
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
	v.expanded = v.rows[1].key()
	if current := s.ensureTaskServicesCurrent(); current != v || v.environment != "uat" || v.expanded != "" {
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
	row.configText = "safe: <redacted>"
	row.configLoaded = true
	cancelled := false
	s.taskServicesUnit.jobs = map[string]*taskServicesRequest{"test": {view: v, cancel: func() { cancelled = true }}}
	s.leaveTaskServices()
	if !cancelled || v.active || len(s.taskServicesUnit.jobs) != 0 || row.configText != "" || row.configLoaded || !row.loaded || len(row.pods) != 1 {
		t.Fatal("leave lost reusable Pods or retained config/request", row)
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
