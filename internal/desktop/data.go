package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
)

type task struct {
	materials                                   application.MaterialsPermissions
	id, title, project, status, updated, branch string
	createdAt                                   string
	requirementURL, requirementTitle, groupID   string
	requirementID, materialsStatus              string
	materialsFailureReason                      string
	path, materialsPath                         string
	archived                                    bool
	services                                    []string
	serviceIDs                                  []string
	workspaces                                  []application.Workspace
	workspaceToolLaunches                       []domain.WorkspaceToolLaunch
}

type formField struct {
	key, label, value  string
	placeholder, error string
	multiline          bool
	secret             bool
	options            []string
}

type actionForm struct {
	title, kind, taskID, serviceID, moduleID, path string
	mode                                           string
	text                                           string
	fields                                         []formField
	taskScope                                      *taskMenuScope
}

type app struct {
	mermaidInitializer     *mermaidInitializer
	taskUnit               taskController
	materialsUnit          materialsController
	requirementUnit        requirementController
	settingsUnit           settingsController
	toolsUnit              toolsController
	workspaceUnit          workspaceController
	releaseUnit            releaseController
	win                    *mygo.Window
	backend                application.Backend
	systemOpenJobs         *systemOpenJobs
	desktopPreferences     *desktopPreferencesState
	readingSession         *desktopReadingSession
	overflowMenus          *overflowMenuState
	page                   string
	dark                   bool
	tab                    int
	file                   int
	source                 bool
	listWidth              float32
	navigationCollapsed    bool
	confirmationDialog     *confirmationDialogState
	inputs                 map[string]*string
	snapshot               application.Snapshot
	backendGate            chan struct{}
	lastResult             string
	lastMessage            string
	lastError              string
	operationNotifications operationNotificationState
	loading                bool
	busy                   bool
	operation              string
	operationKind          string
	operationCancelling    bool
	cancelOperation        context.CancelFunc
	dialogTitle            string
	dialogOpen             bool
	form                   actionForm
}

const taskTagTab = 4

var tabs = []string{"任务详情", "任务资料", "需求详情", "需求说明", "Tag"}

func newApp(backend application.Backend, opts Options) *app {
	s := &app{
		taskUnit: taskController{state: taskState{taskServices: make(map[string]bool),
			taskModules: make(map[string]map[string]bool)}},
		materialsUnit: materialsController{state: materialsState{documentDrafts: make(map[string]string),
			documentDirtyMap: make(map[string]bool),
			documentSaveRefs: make(map[string]documentSaveRef)}},
		requirementUnit: requirementController{state: requirementState{requirementWidth: 320,
			taskRequirementMetadata:           make(map[string]taskRequirementMetadataState),
			taskRequirementMetadataCancels:    make(map[string]context.CancelFunc),
			taskRequirementMetadataGeneration: make(map[string]uint64)}},
		settingsUnit: settingsController{state: settingsState{settingsTab: "基础",
			settingsPage:       "basic",
			settingsAgentScope: "global",
			settingsBoolInputs: make(map[string]*bool)}},
		toolsUnit: toolsController{state: toolsState{toolTab: "JSON",
			toolEnabled: [4]bool{true, true, true, true},
			json:        ""}},
		backend:             backend,
		page:                opts.Page,
		dark:                opts.Dark,
		tab:                 opts.Tab,
		desktopPreferences:  &desktopPreferencesState{enabled: opts.CapturePath == ""},
		listWidth:           240,
		inputs:              make(map[string]*string),
		backendGate:         make(chan struct{}, 1),
		navigationCollapsed: true,
		loading:             true,
	}
	s.initMarkdownAutosave()
	return s
}

func (s *app) applySnapshot(snapshot application.Snapshot) {
	previousTasks, previousConfigJSON := s.taskUnit.state.tasks, s.settingsUnit.state.configJSON
	previousTask := s.taskUnit.currentTask()
	s.invalidateCLIConfiguration(snapshot)
	if s.desktopPreferences != nil && s.desktopPreferences.enabled {
		s.restoreDesktopPreferences(snapshot.Home)
	} else {
		s.loadNavigationPreference(snapshot.Home)
	}
	catalog := newDesktopServiceCatalog(snapshot)
	nextTasks := projectTasksWithCatalog(snapshot, catalog)
	nextIndex := s.taskLifecycleSnapshotIndex(nextTasks)
	selectionChanged := s.prepareTaskLifecycleSelection(previousTask, taskLifecycleSelectionAt(nextTasks, nextIndex))
	s.snapshot = snapshot
	s.workspaceUnit.publishServiceCatalog(catalog)
	s.taskUnit.replaceTasks(nextTasks, nextIndex)
	if snapshot.Error != "" {
		s.lastError = snapshot.Error
	}
	s.syncSettingsConfigInputs(s.settingsUnit.state.configJSON, snapshot.ConfigJSON)
	s.settingsUnit.state.configJSON = snapshot.ConfigJSON
	refreshWorkspaceGit := s.reconcileWorkspaceGitDisplayCache()
	s.observeServiceRepositoryChoiceScope()
	s.reconcileTaskRequirementIdentities(previousTasks, previousConfigJSON)
	s.reconcileSidebarNavigation()
	if selectionChanged {
		s.finishTaskLifecycleSelection()
	} else if refreshWorkspaceGit {
		s.requestWorkspaceGitStatus(true)
	}
	s.restoreReadingSessionOnSnapshot()
	s.observePendingTaskRootMigration(snapshot)
	s.restoreActiveTagJobRecords()
	if s.page == "production" && (s.releaseUnit.state.production == nil || s.releaseUnit.state.production.loadedHome != snapshot.Home || s.releaseUnit.state.production.configScope != productionConfigScope(s.settingsUnit.state.configJSON)) {
		s.enterProductionPage()
	}
}

const desktopPreferencesFile = "desktop-preferences.json"

func (s *app) loadNavigationPreference(home string) {
	// Match WindowPreferences' default. A narrow window still collapses the rail
	// even when the user has explicitly expanded it for wider windows.
	s.navigationCollapsed = true
	if strings.TrimSpace(home) == "" {
		return
	}
	content, err := os.ReadFile(filepath.Join(home, desktopPreferencesFile))
	if err != nil {
		return
	}
	var preferences map[string]json.RawMessage
	if err := json.Unmarshal(content, &preferences); err != nil {
		return
	}
	raw, ok := preferences["workspaceNavigationCollapsed"]
	if !ok {
		return
	}
	var collapsed bool
	if json.Unmarshal(raw, &collapsed) == nil {
		s.navigationCollapsed = collapsed
	}
}

func (s *app) saveNavigationPreference() error {
	if s.desktopPreferences != nil && s.desktopPreferences.enabled {
		return s.persistDesktopNavigation()
	}
	if strings.TrimSpace(s.snapshot.Home) == "" {
		return fmt.Errorf("工作空间路径尚未加载，无法保存导航偏好")
	}
	path := filepath.Join(s.snapshot.Home, desktopPreferencesFile)
	preferences := make(map[string]json.RawMessage)
	if content, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(content, &preferences); err != nil {
			return fmt.Errorf("读取桌面偏好失败：%w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取桌面偏好失败：%w", err)
	}
	value, err := json.Marshal(s.navigationCollapsed)
	if err != nil {
		return fmt.Errorf("编码导航偏好失败：%w", err)
	}
	preferences["workspaceNavigationCollapsed"] = value
	content, err := json.MarshalIndent(preferences, "", "  ")
	if err != nil {
		return fmt.Errorf("编码桌面偏好失败：%w", err)
	}
	if err := os.MkdirAll(s.snapshot.Home, 0o700); err != nil {
		return fmt.Errorf("创建桌面偏好目录失败：%w", err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
		return fmt.Errorf("保存导航偏好失败：%w", err)
	}
	return nil
}

func projectTasks(snapshot application.Snapshot) []task {
	return projectTasksWithCatalog(snapshot, newDesktopServiceCatalog(snapshot))
}

func projectTasksWithCatalog(snapshot application.Snapshot, catalog *desktopServiceCatalog) []task {
	projected := make([]task, 0, len(snapshot.Tasks))
	for _, source := range snapshot.Tasks {
		v := task{
			materials: source.Materials,
			id:        source.ID, title: source.Title, project: source.Project, status: source.Status,
			updated: source.Updated, createdAt: source.CreatedAt, branch: source.Branch, requirementURL: source.RequirementURL,
			requirementTitle: source.RequirementTitle, groupID: source.GroupID, path: source.Path,
			requirementID: source.RequirementID, materialsStatus: source.MaterialsStatus,
			materialsFailureReason: source.MaterialsFailureReason,
			materialsPath:          source.MaterialsPath, archived: source.Archived, workspaces: source.Workspaces,
			workspaceToolLaunches: source.WorkspaceToolLaunches,
		}
		if v.materialsStatus == "" {
			if v.materialsPath != "" {
				v.materialsStatus = "READY"
			} else {
				v.materialsStatus = "NOT_REQUESTED"
			}
		}
		seen := make(map[string]bool)
		for _, workspace := range source.Workspaces {
			name := workspace.Name
			if name == "" {
				if service, ok := catalog.service(source.GroupID, workspace.ServiceID); ok {
					name = service.Name
				}
			}
			if name == "" {
				name = workspace.ServiceID
			}
			if name != "" && !seen[name] {
				v.services = append(v.services, name)
				seen[name] = true
			}
			if workspace.ServiceID != "" {
				v.serviceIDs = append(v.serviceIDs, workspace.ServiceID)
			}
		}
		seenServiceIDs := make(map[string]bool)
		for _, serviceID := range source.Services {
			if serviceID != "" && !seenServiceIDs[serviceID] {
				v.serviceIDs = append(v.serviceIDs, serviceID)
				seenServiceIDs[serviceID] = true
			}
			name := ""
			if service, ok := catalog.service(source.GroupID, serviceID); ok {
				name = service.Name
			}
			if name == "" {
				name = serviceID
			}
			if name != "" && !seen[name] {
				v.services = append(v.services, name)
				seen[name] = true
			}
		}
		projected = append(projected, v)
	}
	return projected
}

func (t task) materialsReady() bool {
	return strings.EqualFold(strings.TrimSpace(t.materialsStatus), "READY") && strings.TrimSpace(t.materialsPath) != ""
}

func (t task) materialsFailed() bool {
	return strings.EqualFold(strings.TrimSpace(t.materialsStatus), "FAILED")
}

func (s *app) workspace(taskID, serviceName string) application.Workspace {
	for _, item := range s.taskUnit.state.tasks {
		if item.id != taskID {
			continue
		}
		for _, workspace := range item.workspaces {
			name := workspace.Name
			if name == "" {
				for _, service := range s.snapshot.Services {
					if service.ID == workspace.ServiceID {
						name = service.Name
						break
					}
				}
			}
			if name == serviceName || workspace.ServiceID == serviceName {
				return workspace
			}
		}
	}
	return application.Workspace{}
}

func (s *app) serviceID(taskID, serviceName string) string {
	if workspace := s.workspace(taskID, serviceName); workspace.ServiceID != "" {
		return workspace.ServiceID
	}
	for _, service := range s.snapshot.Services {
		if service.ID == serviceName || service.Name == serviceName {
			return service.ID
		}
	}
	return serviceName
}

func (s *app) input(name, value string) *string {
	if v, ok := s.inputs[name]; ok {
		return v
	}
	initial := value
	v := &initial
	s.inputs[name] = v
	return v
}

func (s *app) openForm(kind, title, taskID, serviceID, moduleID, path string, fields ...formField) {
	if kind == "skills.sources/save" || kind == "codex.extensions/marketplaces/add-source" {
		s.resetSettingsExtensionForm()
	}
	for _, field := range fields {
		delete(s.inputs, "form:"+field.key)
		if field.secret {
			*s.settingsBoolInput("form-secret:"+kind+":"+field.key, false) = false
		}
	}
	s.form = actionForm{kind: kind, title: title, taskID: taskID, serviceID: serviceID, moduleID: moduleID, path: path, fields: fields}
	s.lastError = ""
	s.dialogTitle = title
	s.dialogOpen = true
}

func (s *app) openTaskCreateForm(title, requirementTitle, requirementURL string) {
	fields := []formField{{key: "title", label: "任务名称", value: requirementTitle}, {key: "branch", label: "分支名称"}, {key: "requirementURL", label: "需求链接", value: requirementURL}, {key: "taskNotes", label: "任务说明", multiline: true}}
	s.openForm("task.create", title, "", "", "", "", fields...)
	s.form.mode = "task-create"
	s.taskUnit.state.taskGroupID = ""
	s.taskUnit.state.taskServices = make(map[string]bool)
	s.taskUnit.state.taskModules = make(map[string]map[string]bool)
	if len(s.snapshot.Groups) > 0 {
		s.taskUnit.state.taskGroupID = s.snapshot.Groups[0].ID
	}
	s.initTaskCreation(title, requirementTitle, requirementURL)
}

func (s *app) openWorkspaceAddForm(t task) {
	branch := t.branch
	s.openForm("workspace.add", "添加服务工作区", t.id, "", "", "",
		formField{key: "baseRef", label: "基础分支（留空则继承）"},
		formField{key: "strategy", label: "工作区策略", value: "STANDARD_WORKTREE"},
		formField{key: "branch", label: "任务分支", value: branch})
	s.form.mode = "workspace-add"
	s.workspaceUnit.state.workspaceAddServiceID = ""
	s.workspaceUnit.state.workspaceAddModuleID = ""
}

func decodeData[T any](data any) ([]T, error) {
	if data == nil {
		return nil, nil
	}
	return application.ResultData[[]T](application.ActionResult{Data: data})
}

func normalizeActionPath(path string) string { return strings.TrimSpace(path) }
