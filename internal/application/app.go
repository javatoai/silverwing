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
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/platform"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type App struct {
	Paths                       storage.Paths
	Config                      *storage.ConfigStore
	Manifests                   *storage.ManifestStore
	Runner                      *process.Runner
	Documents                   *documents.Service
	Integrations                *integrations.Service
	Platform                    *platform.Service
	diffSessionsMu              sync.Mutex
	diffSessions                *workspace.WorkspaceGitDiffSessionManager
	startupMigrationDiagnostics []string
	startupTaskRootError        *storage.DefaultTaskRootInitializationError
	tagJobsMu                   sync.Mutex
	tagJobs                     *tagJobManager
}

func New(home string) (*App, error) {
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("请指定应用数据目录 --home")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("无法获取用户家目录：%w", err)
	}
	paths := storage.NewPaths(absolute)
	paths.Tasks = filepath.Join(userHome, "tasks")
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Runner: process.NewRunner(), Documents: documents.NewService(paths.Cache), diffSessions: workspace.NewWorkspaceGitDiffSessionManager()}
	app.Integrations = integrations.NewService(paths, app.Config, app.Runner)
	app.Platform = platform.NewService(paths, app.Config, app.Runner)
	// Configuration stays in the chosen application home; new task roots default
	// to the user's tasks directory. Existing configured roots are retained.
	// Unsupported configuration is presented as a startup error in Snapshot.
	// Recover a valid interrupted task-root migration once before Snapshot can
	// scan task manifests. Recovery diagnostics are surfaced by Snapshot.
	if config, loadErr := app.Config.Load(); loadErr == nil {
		app.startupMigrationDiagnostics = app.recoverStartupTaskRootMigration(config)
	} else {
		// Default initialization may persist an editable configuration with no
		// task root. Retain the failure that the next successful Load cannot see.
		errors.As(loadErr, &app.startupTaskRootError)
	}
	return app, nil
}

func (a *App) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	config, err := a.Config.Load()
	if err != nil {
		s := Snapshot{Home: a.Paths.Home, Error: err.Error(), Tasks: []Task{}, Repositories: []Repository{}, Services: []Service{}, Skills: []Skill{}, TagHistory: []TagRecord{}}
		a.appendStartupMigrationDiagnostics(&s)
		return s, nil
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Home: a.Paths.Home, Theme: string(config.Theme), ConfigJSON: string(encoded), Tasks: []Task{}, Repositories: []Repository{}, Services: []Service{}, Groups: []Group{}, Skills: []Skill{}, TagHistory: []TagRecord{}}
	s.TaskRootMigrationPending, err = a.TaskRootMigrationPending(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		appendSnapshotError(&s, "读取任务根迁移恢复状态失败："+err.Error())
	}
	a.appendStartupMigrationDiagnostics(&s)
	if a.startupTaskRootError != nil && config.TaskRoot == nil {
		appendSnapshotError(&s, a.startupTaskRootError.Error())
	}
	if data, skillErr := a.Platform.Execute(ctx, "skills.catalog", nil); skillErr == nil {
		if rows, e := localSkillRows(data); e == nil {
			s.Skills = rows
		} else {
			appendSnapshotError(&s, e.Error())
		}
	} else {
		appendSnapshotError(&s, skillErr.Error())
	}
	for _, repository := range config.Repositories {
		s.Repositories = append(s.Repositories, Repository{ID: repository.ID, Name: repository.Name, RootPath: repository.RootPath, OriginURL: value(repository.OriginURL), CurrentBranch: value(repository.CurrentBranch), DefaultBranch: value(repository.DefaultRemoteBranch)})
	}
	groupNames := map[string]string{}
	for _, group := range config.Groups {
		g := Group{ID: group.ID, Name: group.Name, ServiceIDs: []string{}}
		groupNames[group.ID] = group.Name
		for _, service := range group.Services {
			g.ServiceIDs = append(g.ServiceIDs, service.ID)
			v := Service{ID: service.ID, Name: service.DisplayName, RepositoryID: service.RepositoryID, MasterBranch: service.MasterBranch, GroupID: group.ID, Modules: []Module{}}
			for _, module := range service.Modules {
				v.Modules = append(v.Modules, Module{ID: module.ID, Name: module.Name, MasterBranch: value(module.MasterBranch), Strategy: string(module.Strategy), TagTargetRef: value(module.TagTargetRef), TagEnabled: module.TagEnabled})
			}
			if len(service.Modules) > 0 {
				v.Strategy = string(service.Modules[0].Strategy)
			}
			s.Services = append(s.Services, v)
		}
		s.Groups = append(s.Groups, g)
	}
	if config.TaskRoot != nil {
		scan, scanErr := a.Manifests.Scan(*config.TaskRoot)
		if scanErr != nil {
			appendSnapshotError(&s, scanErr.Error())
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
			m := entry.Manifest
			t := Task{ID: filepath.Base(entry.Path), Title: m.FolderName, Project: groupNames[m.GroupID], Status: "进行中", Updated: m.UpdatedAt, CreatedAt: m.CreatedAt, Branch: m.FeatureBranch, Path: entry.Path, MaterialsPath: value(m.RequirementMaterials.WriteRoot), RequirementURL: m.RequirementLink, GroupID: m.GroupID, Archived: m.LifecycleStatus == domain.TaskArchived, Services: []string{}, Workspaces: []Workspace{}}
			t.RequirementID = value(m.RequirementID)
			t.MaterialsStatus = string(m.RequirementMaterials.Status)
			t.MaterialsFailureReason = value(m.RequirementMaterials.FailureReason)
			t.WorkspaceToolLaunches = append([]domain.WorkspaceToolLaunch{}, m.WorkspaceToolLaunches...)
			if t.Archived {
				t.Status = "已归档"
			}
			for _, w := range m.Services {
				t.Services = append(t.Services, w.ServiceName)
				t.Workspaces = append(t.Workspaces, Workspace{ID: w.GroupServiceID + ":" + w.ModuleID, ServiceID: w.GroupServiceID, ModuleID: w.ModuleID, Name: w.ServiceName, Path: w.WorktreePath, Branch: w.Branch, PushRemote: w.PushRemote, BaseRef: value(w.BaseRef), Strategy: string(w.Strategy), State: string(w.Health), Warnings: append([]string{}, w.Warnings...), Record: w})
			}
			s.Tasks = append(s.Tasks, t)
			reports, historyErr := delivery.History(ctx, t.ID)
			if historyErr == nil {
				s.TagHistory = append(s.TagHistory, tagRecords(t.ID, reports)...)
			} else {
				appendSnapshotError(&s, historyErr.Error())
			}
		}
		sort.SliceStable(s.Tasks, func(i, j int) bool { return s.Tasks[i].Updated > s.Tasks[j].Updated })
		sortTagHistoryRecords(s.TagHistory)
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
	entries, err := a.Manifests.List(*config.TaskRoot)
	if err != nil {
		return domain.TaskManifest{}, "", err
	}
	for _, entry := range entries {
		if action.TaskID == filepath.Base(entry.Path) {
			if entry.Manifest.TaskDirectoryName != action.TaskID {
				return domain.TaskManifest{}, "", errors.New("任务清单的目录标识与实际路径不一致，请修复后重试")
			}
			return entry.Manifest, entry.Path, nil
		}
	}
	return domain.TaskManifest{}, "", fmt.Errorf("未找到任务：%s", action.TaskID)
}
func (a *App) materialRoot(action Action) (string, error) {
	manifest, _, err := a.task(action)
	if err != nil {
		return "", err
	}
	if manifest.RequirementMaterials.WriteRoot == nil || *manifest.RequirementMaterials.WriteRoot == "" {
		return "", errors.New("当前任务尚未配置资料目录")
	}
	root := *manifest.RequirementMaterials.WriteRoot
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
	return workspace.TaskRootMigrationPending(ctx, a.Paths)
}
