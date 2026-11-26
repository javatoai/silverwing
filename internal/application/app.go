package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/dragon"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/platform"
	"silverwing-go/internal/process"
	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type App struct {
	directoryRecycler            documents.DirectoryRecycler
	materialsGitGate             sync.RWMutex
	Paths                        storage.Paths
	Config                       *storage.ConfigStore
	Manifests                    *storage.ManifestStore
	Runner                       *process.Runner
	Documents                    *documents.Service
	Integrations                 *integrations.Service
	Platform                     *platform.Service
	diffSessionsMu               sync.Mutex
	diffSessions                 *workspace.WorkspaceGitDiffSessionManager
	startupMigrationDiagnostics  []string
	startupTaskRootError         *storage.DefaultTaskRootInitializationError
	tagJobsMu                    sync.Mutex
	tagJobs                      *tagJobManager
	genbuConfigPreviews          genbuConfigPreviewStore
	genbuConfigSources           genbuConfigSourceStore
	dragonQAClient               dragonQAClient
	dragonQAComments             dragonQACommentClient
	taskRequirementCommentsMu    sync.Mutex
	taskRequirementComments      *taskRequirementCommentSync
	taskRequirementCommentClient taskRequirementCommentClient
	optionalCLIs                 optionalCLIRegistry
	optionalCLIRequire           func(context.Context, string) error
	connections                  platformClients
}

func New(home string) (*App, error) {
	return newProfileApp(home)
}

func newProfileApp(home string) (*App, error) {
	paths, _, err := applicationPaths(home)
	if err != nil {
		return nil, err
	}
	paths.ProfileID, err = storage.LoadOrCreateProfileID(paths.Home)
	if err != nil {
		return nil, err
	}
	app := composeApp(paths, storage.NewProfileManifestStore(paths))
	app.Documents = documents.NewProfileService(paths)
	config, err := app.Config.Load()
	if err != nil {
		return nil, err
	}
	app.rebindProfileTaskDocuments(config)
	return app, nil
}

func (a *App) rebindProfileTaskDocuments(config domain.AppConfig) {
	if config.TaskRoot == nil {
		return
	}
	entries, err := a.Manifests.Scan(*config.TaskRoot)
	if err != nil {
		a.startupMigrationDiagnostics = append(a.startupMigrationDiagnostics, "读取任务说明索引失败："+err.Error())
		return
	}
	documents := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	locks := workspace.NewTaskOperationLocks(a.Paths)
	for _, entry := range entries.Current {
		err := locks.WithTask(entry.Path, func() error {
			manifest, err := a.Manifests.Load(entry.Path)
			if err != nil {
				return err
			}
			return documents.RebindProfileTaskDocuments(entry.Path, manifest, config)
		})
		if err != nil && !errors.Is(err, storage.ErrLockBusy) {
			a.startupMigrationDiagnostics = append(a.startupMigrationDiagnostics, fmt.Sprintf("刷新 %s 的任务说明失败：%v", entry.Manifest.FolderName, err))
		}
	}
}

func newApp(home string) (*App, error) {
	paths, userHome, err := applicationPaths(home)
	if err != nil {
		return nil, err
	}
	paths.Tasks = productprofile.DefaultTaskRoot(userHome, paths.Home)
	return composeApp(paths, storage.NewManifestStore()), nil
}

func applicationPaths(home string) (storage.Paths, string, error) {
	if strings.TrimSpace(home) == "" {
		return storage.Paths{}, "", errors.New("请指定应用数据目录 --conf-dir")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return storage.Paths{}, "", err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return storage.Paths{}, "", fmt.Errorf("无法获取用户家目录：%w", err)
	}
	return storage.NewPaths(absolute), userHome, nil
}

func composeApp(paths storage.Paths, manifests *storage.ManifestStore) *App {
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: manifests, Runner: process.NewRunner(), Documents: documents.NewService(paths.Cache), diffSessions: workspace.NewWorkspaceGitDiffSessionManager()}
	app.Integrations = integrations.NewService(paths, app.Config, app.Runner)
	app.Platform = platform.NewService(paths, app.Config, app.Runner)
	app.dragonQAClient = dragon.Native{Client: app.embeddedDragon, Identity: func(ctx context.Context, _ domain.AppConfig) (string, error) {
		return app.embeddedAccountIdentity(ctx, "dragon")
	}}
	app.dragonQAComments = app.Integrations
	app.taskRequirementCommentClient = app.Integrations
	return app
}

func (a *App) Snapshot(ctx context.Context) (Snapshot, error) {
	return a.SnapshotFor(ctx, SnapshotRequest{Tasks: true, TagHistory: true, Skills: true})
}

// SnapshotRequest makes each navigation read explicit. A partial snapshot is
// a projection only; callers merge its selected collections and retain the
// others. It never authorizes an operation or caches external state.
type SnapshotRequest struct {
	Tasks, TagHistory, Skills bool
}

func (a *App) SnapshotFor(ctx context.Context, request SnapshotRequest) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	config, err := a.Config.Load()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Snapshot{}, ctxErr
		}
		s := Snapshot{Home: a.Paths.Home, ProfileID: a.Paths.ProfileID, Error: err.Error(), Tasks: []Task{}, Repositories: []Repository{}, Services: []Service{}, Skills: []Skill{}, TagHistory: []TagRecord{}}
		a.appendStartupMigrationDiagnostics(&s)
		return s, nil
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Home: a.Paths.Home, ProfileID: a.Paths.ProfileID, Theme: string(config.Theme), ConfigJSON: string(encoded), Tasks: []Task{}, Repositories: []Repository{}, Services: []Service{}, Groups: []Group{}, Skills: []Skill{}, TagHistory: []TagRecord{}}
	s.TaskRootMigrationPending, err = a.TaskRootMigrationPending(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		appendSnapshotError(&s, "读取任务根迁移恢复状态失败："+err.Error())
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	a.appendStartupMigrationDiagnostics(&s)
	if a.startupTaskRootError != nil && config.TaskRoot == nil {
		appendSnapshotError(&s, a.startupTaskRootError.Error())
	}
	if request.Skills {
		if data, skillErr := a.Platform.Execute(ctx, "skills.catalog", nil); skillErr == nil {
			if rows, e := localSkillRows(data); e == nil {
				s.Skills = rows
			} else {
				appendSnapshotError(&s, e.Error())
			}
		} else {
			appendSnapshotError(&s, skillErr.Error())
		}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	for _, repository := range config.Repositories {
		s.Repositories = append(s.Repositories, Repository{ID: repository.ID, Name: repository.Name, RootPath: repository.RootPath, OriginURL: value(repository.OriginURL), CurrentBranch: value(repository.CurrentBranch), DefaultBranch: value(repository.DefaultRemoteBranch)})
	}
	for _, service := range config.ServiceCatalog() {
		v := Service{ID: service.ID, Name: service.DisplayName, RepositoryID: service.RepositoryID, MasterBranch: service.MasterBranch, Modules: []Module{}}
		for _, module := range service.Modules {
			v.Modules = append(v.Modules, Module{ID: module.ID, Name: module.Name, MasterBranch: value(module.MasterBranch), Strategy: string(module.Strategy), TagTargetRef: value(module.TagTargetRef), TagEnabled: module.TagEnabled})
		}
		if len(service.Modules) > 0 {
			v.Strategy = string(service.Modules[0].Strategy)
		}
		s.Services = append(s.Services, v)
	}
	if config.TaskRoot != nil && (request.Tasks || request.TagHistory) {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if a.Paths.ProfileID != "" {
			info, err := os.Stat(*config.TaskRoot)
			if err != nil || !info.IsDir() {
				appendSnapshotError(&s, "无法访问任务目录："+*config.TaskRoot)
				return s, ctx.Err()
			}
		}
		scan, scanErr := a.Manifests.Scan(*config.TaskRoot)
		if scanErr != nil {
			appendSnapshotError(&s, scanErr.Error())
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		issues := map[string]string{}
		for path, reason := range scan.Failures {
			issues[path] = reason
		}
		for path, reason := range scan.UnsupportedReasons {
			issues[path] = reason
		}
		issuePaths := make([]string, 0, len(issues))
		for path := range issues {
			issuePaths = append(issuePaths, path)
		}
		sort.Strings(issuePaths)
		for _, path := range issuePaths {
			appendSnapshotError(&s, path+"："+issues[path])
		}
		delivery := a.delivery(config)
		// Snapshot only reads Tag history. Retention runs when the owning task
		// explicitly reads its Tag history, so refreshing task A never trims B.
		for _, entry := range scan.Current {
			if err := ctx.Err(); err != nil {
				return Snapshot{}, err
			}
			m := documents.ResolveBoundTaskMaterials(entry.Manifest, config)
			if a.Paths.ProfileID != "" {
				if err := workspace.NewTaskOperationLocks(a.Paths).ValidateWorkspaceDirectory(config, entry.Path, m); err != nil {
					appendSnapshotError(&s, err.Error())
					continue
				}
			}
			t := Task{ID: filepath.Base(entry.Path), Title: m.FolderName, Status: "进行中", Updated: m.UpdatedAt, CreatedAt: m.CreatedAt, Branch: m.FeatureBranch, Path: entry.Path, MaterialsPath: value(m.RequirementMaterials.WriteRoot), RequirementURL: m.RequirementLink, Archived: m.LifecycleStatus == domain.TaskArchived, Services: []string{}, Workspaces: []Workspace{}}
			t.Materials = taskMaterialsPermissions(m, config)
			t.MaterialsPath = t.Materials.ReadRoot
			t.RequirementID = value(m.RequirementID)
			t.MaterialsStatus = string(m.RequirementMaterials.Status)
			t.MaterialsFailureReason = value(m.RequirementMaterials.FailureReason)
			if err := validAnnualTaskMaterials(m, config); err != nil {
				t.MaterialsStatus = string(domain.MaterialsFailed)
				t.MaterialsFailureReason = err.Error()
				t.MaterialsPath = ""
			}
			t.WorkspaceToolLaunches = append([]domain.WorkspaceToolLaunch{}, m.WorkspaceToolLaunches...)
			if t.Archived {
				t.Status = "已归档"
			}
			for _, w := range m.Services {
				t.Services = append(t.Services, w.ServiceName)
				t.Workspaces = append(t.Workspaces, Workspace{ID: w.GroupServiceID + ":" + w.ModuleID, ServiceID: w.GroupServiceID, ModuleID: w.ModuleID, Name: w.ServiceName, Path: w.WorktreePath, Branch: w.Branch, PushRemote: w.PushRemote, BaseRef: value(w.BaseRef), Strategy: string(w.Strategy), State: string(w.Health), Warnings: append([]string{}, w.Warnings...), Record: w})
			}
			if request.Tasks {
				s.Tasks = append(s.Tasks, t)
			}
			if request.TagHistory {
				reports, historyErr := delivery.History(ctx, t.ID)
				if historyErr == nil {
					s.TagHistory = append(s.TagHistory, tagRecords(t.ID, reports)...)
				} else {
					if err := ctx.Err(); err != nil {
						return Snapshot{}, err
					}
					appendSnapshotError(&s, historyErr.Error())
				}
			}
			if err := ctx.Err(); err != nil {
				return Snapshot{}, err
			}
		}
		sort.SliceStable(s.Tasks, func(i, j int) bool { return s.Tasks[i].Updated > s.Tasks[j].Updated })
		sortTagHistoryRecords(s.TagHistory)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

func (a *App) appendStartupMigrationDiagnostics(snapshot *Snapshot) {
	for _, diagnostic := range a.startupMigrationDiagnostics {
		appendSnapshotError(snapshot, diagnostic)
	}
}

func appendSnapshotError(snapshot *Snapshot, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	if snapshot.Error != "" {
		snapshot.Error += "\n"
	}
	snapshot.Error += message
}

func value(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func textPayload(action Action, key string) string {
	if action.Payload == nil {
		return ""
	}
	v, _ := action.Payload[key].(string)
	return v
}
func boolPayload(action Action, key string) bool {
	if action.Payload == nil {
		return false
	}
	v, _ := action.Payload[key].(bool)
	return v
}
func actionPath(action Action) string {
	if action.Path != "" {
		return action.Path
	}
	return textPayload(action, "path")
}
func actionText(action Action) string {
	if action.Text != "" {
		return action.Text
	}
	return textPayload(action, "text")
}

func (a *App) task(action Action) (domain.TaskManifest, string, error) {
	config, err := a.Config.Load()
	if err != nil {
		return domain.TaskManifest{}, "", err
	}
	return a.taskInConfig(action, config)
}

func (a *App) taskInConfig(action Action, config domain.AppConfig) (domain.TaskManifest, string, error) {
	if config.TaskRoot == nil {
		return domain.TaskManifest{}, "", errors.New("任务根目录未配置")
	}
	entry, err := a.Manifests.LoadTask(*config.TaskRoot, action.TaskID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.TaskManifest{}, "", fmt.Errorf("未找到任务：%s", action.TaskID)
		}
		return domain.TaskManifest{}, "", err
	}
	if a.Paths.ProfileID != "" {
		if err := workspace.NewTaskOperationLocks(a.Paths).ValidateWorkspaceDirectory(config, entry.Path, entry.Manifest); err != nil {
			return domain.TaskManifest{}, "", err
		}
	}
	return entry.Manifest, entry.Path, nil
}
func (a *App) materialRoot(action Action) (string, error) {
	config, err := a.Config.Load()
	if err != nil {
		return "", err
	}
	manifest, _, err := a.taskInConfig(action, config)
	if err != nil {
		return "", err
	}
	if manifest.RequirementMaterials.WriteRoot == nil || *manifest.RequirementMaterials.WriteRoot == "" {
		return "", errors.New("当前任务尚未配置资料目录")
	}
	manifest = documents.ResolveBoundTaskMaterials(manifest, config)
	if err := validAnnualTaskMaterials(manifest, config); err != nil {
		return "", err
	}
	root := visibleMaterialsRoot(*manifest.RequirementMaterials.WriteRoot)
	if _, err = os.Stat(root); err != nil {
		return "", err
	}
	return root, nil
}
func (a *App) git(config domain.AppConfig) *workspace.GitClient {
	return workspace.NewGitClientWithConfig(a.Runner, config)
}

var _ Backend = (*App)(nil)

// The desktop receives this state through Snapshot; isolated adapters can
// forward the same read without loading unrelated platform integrations.
func (a *App) TaskRootMigrationPending(ctx context.Context) (*bool, error) {
	if a.Paths.ProfileID != "" {
		pending := false
		return &pending, nil
	}
	return workspace.TaskRootMigrationPending(ctx, a.Paths)
}
