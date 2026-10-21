package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo"
)

type task struct {
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
}

type app struct {
	win                        *mygo.Window
	backend                    application.Backend
	systemOpenJobs             *systemOpenJobs
	desktopPreferences         *desktopPreferencesState
	readingSession             *desktopReadingSession
	overflowMenus              *overflowMenuState
	workspaceReviewFocusReturn *workspaceReviewFocusReturn

	page                              string
	taskNavigation                    *taskNavigationState
	taskContextScope                  *taskMenuScope
	taskContextUpdate                 func(func()) // Headless scheduler seam; production captures Window.Update.
	taskIndexes                       map[bool]*taskIndexBrowsingState
	taskListFocused                   bool
	dark                              bool
	selected                          int
	tab                               int
	file                              int
	source                            bool
	json                              string
	configJSON                        string
	configReads                       desktopConfigReadCache
	autoSync                          bool
	notifications                     bool
	tasks                             []task
	listWidth                         float32
	notes                             [3]string
	notePaths                         [3]string
	noteHashes                        [3]string
	noteTab                           int
	noteEditing                       bool
	navigationCollapsed               bool
	settingsTab                       string
	settingsPage                      string
	settingsLoaded                    string
	settingsOutput                    string
	confirmationDialog                *confirmationDialogState
	settingsConfigOpen                bool
	settingsConfigText                string
	settingsConfigPath                string
	settingsBackups                   []map[string]any
	settingsConfigRecovery            *settingsConfigRecoveryState
	settingsTemplates                 []map[string]any
	settingsTemplatesLoaded           bool
	settingsMarketplaces              []map[string]any
	settingsAgentScope                string
	settingsAgentGroupID              string
	settingsAgentContent              string
	settingsAgentPath                 string
	settingsAgentLoaded               bool
	settingsAgentContext              string
	settingsSkillsCatalog             string
	settingsExtensions                []map[string]any
	settingsMCPServers                []map[string]any
	settingsMCPPath                   string
	settingsMCPWarnings               []string
	settingsMCPTaskScope              bool
	settingsSkillSources              []map[string]any
	settingsLocalSkills               []map[string]any
	settingsSkillRoot                 string
	settingsSkillPreviewOpen          bool
	settingsSkillPreviewName          string
	settingsSkillPreviewText          string
	settingsSkillFiles                []map[string]any
	settingsSkillFilesDirectory       string
	settingsSkillFilesTruncated       bool
	settingsBranchChoices             []string
	settingsExtensionRequests         *settingsExtensionRequestState
	settingsLogEntries                []map[string]any
	settingsMCPConnections            map[string]any
	settingsMCPConnectionError        string
	settingsGitEnvironment            map[string]any
	settingsDevelopmentToolsSnapshot  map[string]any
	startupDevelopmentTools           *startupDevelopmentToolsState
	settingsCLIStatus                 map[string]map[string]any
	cliJobs                           *cliJobState
	codexAI                           *codexAIState
	meegleProjectCatalog              *meegleProjectCatalogState
	requirementReaders                map[string]*requirementReaderState
	requirementReaderGeneration       uint64
	requirementContentJobs            *requirementContentJobState
	requirementRefreshJobs            *requirementRefreshState
	materialsFileManagement           *materialsFileManagementState
	materialsFileJobs                 *materialsFileJobState
	materialsLiveRefreshLifecycle     *materialsLiveRefreshLifecycle
	skillsBrowser                     *skillsBrowserState
	settingsCLISessions               map[string]map[string]any
	settingsLarkDomains               map[string]bool
	settingsLarkLoginDialog           bool
	settingsAuthPollTimers            map[string]*time.Timer
	settingsTerminalResolution        map[string]any
	settingsGroupID                   string
	settingsPickerBusy                bool
	settingsBoolInputs                map[string]*bool
	migrationPlan                     any
	migrationPreview                  string
	migrationReady                    bool
	taskRootTargetFocused             bool
	toolTab                           string
	tools                             *toolPageState
	toolJobs                          *developerToolJobsState
	repositoryTab                     string
	workspaceGit                      *workspaceGitState
	workspaceRemoval                  *workspaceRemovalState
	workspaceRepair                   *workspaceRepairState
	workspaceHistory                  *workspaceHistoryState
	workspaceReviewSessions           map[string]*workspaceReviewSession
	workspaceHistoryJobs              *workspaceHistoryJobState
	workspaceMerge                    *workspaceMergeUIState
	workspaceMergeBatch               *workspaceMergeBatchUIState
	workspaceCommand                  *workspaceCommandUIState
	workspaceCommandJobs              *workspaceCommandJobsState
	materialsFeishuSyncJobs           *materialsFeishuSyncJobsState
	requirementListJobs               *requirementListJobState
	requirementIdentityJobs           *requirementIdentityJobState
	requirementNaming                 *requirementNamingPreparationState
	requirementDocumentPreparation    *requirementDocumentPreparationState
	taskBranchInfo                    *taskBranchInfoState
	tagGenbuAutoProbe                 *tagGenbuAutoProbeState
	tagJobs                           *tagDesktopJobs
	production                        *productionUIState
	tagHistory                        *tagHistoryViewState
	tagBuild                          *tagBuildViewState
	tagTarget                         *tagTargetViewState
	serviceGroupID                    string
	serviceReorderMode                bool
	repositoryBatchOpen               bool
	repositoryBatchGroupID            string
	repositoryBatchPaths              []string
	repositoryBatchPickerBusy         bool
	repositoryBatchGeneration         uint64
	repositoryBatchResultOpen         bool
	repositoryBatchResult             application.RepositoryBatchResult
	serviceEditor                     *serviceEditorState
	serviceRepositoryChoices          *serviceRepositoryChoiceJobs
	pendingTaskRootMigration          *markdownTaskRootMigrationLease
	selectedRequirement               int
	selectedRequirementKey            string
	requirementsRequested             bool
	requirementsLoading               bool
	requirementsLoaded                bool
	requirementsFetchedAt             int64
	requirementsLoadError             string
	requirementsFailures              []string
	requirementsCompletedQueries      int
	requirementsSprints               []requirementSprint
	requirementsSelectedSprintKey     string
	requirementsFocusedDetail         bool
	taskRequirementMetadata           map[string]taskRequirementMetadataState
	taskRequirementMetadataCancels    map[string]context.CancelFunc
	taskRequirementMetadataGeneration map[string]uint64
	inputs                            map[string]*string
	toolEnabled                       [4]bool
	requirementWidth                  float32

	snapshot               application.Snapshot
	requirements           []application.Requirement
	documents              []application.Document
	documentContent        string
	documentPath           string
	documentLinkFragment   string
	documentKind           string
	documentReadOnly       bool
	documentHash           string
	documentReadPending    bool
	documentDirty          bool
	documentEditing        bool
	documentTabs           []string
	documentDrafts         map[string]string
	documentDirtyMap       map[string]bool
	markdownAutosave       *editor.Autosave
	materialTextEditors    map[string]*materialTextEditorState
	materialTextActive     *materialTextEditorState
	documentClose          *documentCloseState
	documentSaveMu         sync.Mutex
	documentSaveRefs       map[string]documentSaveRef
	backendGate            chan struct{}
	markdown               *markdownEditorState
	nativeMedia            *nativeMediaState
	markdownBitmapViewer   *markdownBitmapViewerState
	taskDeleteRiskJobs     *taskDeleteRiskJobState
	documentSearch         string
	documentFindOpen       bool
	documentFind           *documentFindState
	materialsPicker        *materialsPickerState
	documentsCollapsed     bool
	documentsRequested     bool
	documentsLoaded        bool
	importPreview          bool
	importPath             string
	selectedService        string
	selectedModule         string
	taskGroupID            string
	taskServices           map[string]bool
	taskModules            map[string]map[string]bool
	taskCreation           *taskCreationState
	taskCreationPreflights *taskCreationPreflightJobState
	taskDeleteDialog       *taskDeleteDialogState
	taskNotesUI            *taskNotesUIState
	workspaceAppend        *taskAppendUIState
	workspaceAddServiceID  string
	workspaceAddModuleID   string
	lastResult             string
	taskCreationWarning    string
	lastMessage            string
	lastError              string
	operationNotifications operationNotificationState
	taskRootChange         *taskRootChangeRequest
	pageLocalRead          *pageLocalReadState
	settingsAutosave       *settingsAutosaveState
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
		backend: backend, page: opts.Page, dark: opts.Dark, tab: opts.Tab,
		desktopPreferences: &desktopPreferencesState{enabled: opts.CapturePath == ""},
		listWidth:          240, requirementWidth: 320, settingsTab: "基础", settingsPage: "basic", settingsAgentScope: "global", toolTab: "JSON",
		inputs: make(map[string]*string), toolEnabled: [4]bool{true, true, true, true},
		settingsBoolInputs: make(map[string]*bool),
		documentDrafts:     make(map[string]string), documentDirtyMap: make(map[string]bool),
		documentSaveRefs: make(map[string]documentSaveRef), backendGate: make(chan struct{}, 1),
		taskServices: make(map[string]bool), taskModules: make(map[string]map[string]bool),
		taskRequirementMetadata: make(map[string]taskRequirementMetadataState), taskRequirementMetadataCancels: make(map[string]context.CancelFunc), taskRequirementMetadataGeneration: make(map[string]uint64),
		navigationCollapsed: true,
		json:                "", loading: true,
	}
	s.initMarkdownAutosave()
	return s
}

func (s *app) applySnapshot(snapshot application.Snapshot) {
	previousTasks, previousConfigJSON := s.tasks, s.configJSON
	previousTask := s.currentTask()
	s.invalidateCLIConfiguration(snapshot)
	if s.desktopPreferences != nil && s.desktopPreferences.enabled {
		s.restoreDesktopPreferences(snapshot.Home)
	} else {
		s.loadNavigationPreference(snapshot.Home)
	}
	nextTasks := projectTasks(snapshot)
	nextIndex := s.taskLifecycleSnapshotIndex(nextTasks)
	selectionChanged := s.prepareTaskLifecycleSelection(previousTask, taskLifecycleSelectionAt(nextTasks, nextIndex))
	s.snapshot = snapshot
	s.tasks, s.selected = nextTasks, nextIndex
	if snapshot.Error != "" {
		s.lastError = snapshot.Error
	}
	s.syncSettingsConfigInputs(s.configJSON, snapshot.ConfigJSON)
	s.configJSON = snapshot.ConfigJSON
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
	if s.page == "production" && (s.production == nil || s.production.loadedHome != snapshot.Home || s.production.configScope != productionConfigScope(s.configJSON)) {
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
	services := make(map[string]string, len(snapshot.Services))
	for _, service := range snapshot.Services {
		services[service.ID] = service.Name
	}
	projected := make([]task, 0, len(snapshot.Tasks))
	for _, source := range snapshot.Tasks {
		v := task{
			id: source.ID, title: source.Title, project: source.Project, status: source.Status,
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
				name = services[workspace.ServiceID]
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
			name := services[serviceID]
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

func (s *app) currentTask() task {
	if s.selected < 0 || s.selected >= len(s.tasks) {
		return task{}
	}
	return s.tasks[s.selected]
}

func (t task) materialsReady() bool {
	return strings.EqualFold(strings.TrimSpace(t.materialsStatus), "READY") && strings.TrimSpace(t.materialsPath) != ""
}

func (t task) materialsFailed() bool {
	return strings.EqualFold(strings.TrimSpace(t.materialsStatus), "FAILED")
}

func (s *app) activeTasks(archived bool) []int {
	indices := make([]int, 0, len(s.tasks))
	for index, item := range s.tasks {
		if item.archived == archived {
			indices = append(indices, index)
		}
	}
	return indices
}

func (s *app) workspace(taskID, serviceName string) application.Workspace {
	for _, item := range s.tasks {
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
	s.taskGroupID = ""
	s.taskServices = make(map[string]bool)
	s.taskModules = make(map[string]map[string]bool)
	if len(s.snapshot.Groups) > 0 {
		s.taskGroupID = s.snapshot.Groups[0].ID
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
	s.workspaceAddServiceID = ""
	s.workspaceAddModuleID = ""
}

func decodeData[T any](data any) ([]T, error) {
	if data == nil {
		return nil, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var values []T
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func normalizeActionPath(path string) string { return strings.TrimSpace(path) }
