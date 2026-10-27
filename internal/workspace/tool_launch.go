package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/taskscope"
)

type TaskWorkspaceToolDescriptor struct {
	ID          string
	DisplayName string
	Description string
}

type TaskWorkspaceToolAvailability struct {
	Available bool
	Reason    string
}

type TaskWorkspaceContext struct {
	TaskName      string
	TaskDirectory string
	AgentsFile    string
	Workspaces    []domain.ServiceWorkspace
}

type TaskWorkspaceToolLauncher interface {
	Descriptor() TaskWorkspaceToolDescriptor
	Availability(context.Context) TaskWorkspaceToolAvailability
	Open(context.Context, TaskWorkspaceContext) error
}

type TaskWorkspaceToolRegistry struct {
	Launchers map[string]TaskWorkspaceToolLauncher
}

func NewTaskWorkspaceToolRegistry(launchers []TaskWorkspaceToolLauncher) (*TaskWorkspaceToolRegistry, error) {
	registry := &TaskWorkspaceToolRegistry{Launchers: map[string]TaskWorkspaceToolLauncher{}}
	for _, launcher := range launchers {
		if launcher == nil {
			return nil, fmt.Errorf("工作区工具启动器不能为空")
		}
		descriptor := launcher.Descriptor()
		if strings.TrimSpace(descriptor.ID) == "" || strings.TrimSpace(descriptor.DisplayName) == "" {
			return nil, fmt.Errorf("工作区工具 ID 和名称不能为空")
		}
		if _, exists := registry.Launchers[descriptor.ID]; exists {
			return nil, fmt.Errorf("工作区工具 ID 重复：%s", descriptor.ID)
		}
		registry.Launchers[descriptor.ID] = launcher
	}
	return registry, nil
}

type WorkspaceToolLaunchService struct {
	Registry  *TaskWorkspaceToolRegistry
	Manifests *storage.ManifestStore
	Locks     *OperationLocks
	Now       func() time.Time
}

func NewWorkspaceToolLaunchService(registry *TaskWorkspaceToolRegistry, manifests *storage.ManifestStore, locks *OperationLocks) *WorkspaceToolLaunchService {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &WorkspaceToolLaunchService{Registry: registry, Manifests: manifests, Locks: locks, Now: time.Now}
}

func (s *WorkspaceToolLaunchService) Descriptors() []TaskWorkspaceToolDescriptor {
	if s.Registry == nil {
		return []TaskWorkspaceToolDescriptor{}
	}
	out := make([]TaskWorkspaceToolDescriptor, 0, len(s.Registry.Launchers))
	for _, launcher := range s.Registry.Launchers {
		out = append(out, launcher.Descriptor())
	}
	return out
}

func (s *WorkspaceToolLaunchService) Availability(ctx context.Context, toolID string) TaskWorkspaceToolAvailability {
	if s.Registry == nil {
		return TaskWorkspaceToolAvailability{Reason: "当前版本未注册工具：" + toolID}
	}
	launcher := s.Registry.Launchers[toolID]
	if launcher == nil {
		return TaskWorkspaceToolAvailability{Reason: "当前版本未注册工具：" + toolID}
	}
	return launcher.Availability(ctx)
}

func (s *WorkspaceToolLaunchService) Launch(ctx context.Context, taskPath string, manifest domain.TaskManifest, toolIDs []string) (domain.TaskManifest, error) {
	selected := uniqueStringsNonEmpty(toolIDs)
	if len(selected) == 0 {
		return manifest, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.NormalizeTaskManifest(manifest), err
	}
	// A failed launch must not erase the already-created task from its caller.
	updated := domain.NormalizeTaskManifest(manifest)
	err := s.Locks.WithTask(taskPath, func() error {
		current, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if !sameTaskManifest(current, manifest) {
			return fmt.Errorf("任务清单已发生变化，请重新尝试启动")
		}
		if err := taskscope.Validate(ctx, domain.AppConfig{}, taskPath, current); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		updated = current
		pending := current
		now := s.now()
		for _, id := range selected {
			pending = replaceWorkspaceToolLaunch(pending, domain.WorkspaceToolLaunch{ToolID: id, Status: domain.WorkspaceToolPending, UpdatedAt: now})
		}
		pending.UpdatedAt = now
		if err := s.Manifests.Save(taskPath, pending); err != nil {
			return err
		}
		updated = pending
		for _, id := range selected {
			status := domain.WorkspaceToolLaunch{ToolID: id, Status: domain.WorkspaceToolFailed, UpdatedAt: s.now()}
			launcher := TaskWorkspaceToolLauncher(nil)
			if s.Registry != nil {
				launcher = s.Registry.Launchers[id]
			}
			switch {
			case ctx.Err() != nil:
				status.Message = ptr(ctx.Err().Error())
			case launcher == nil:
				status.Message = ptr("当前版本未注册工具：" + id)
			default:
				availability := launcher.Availability(ctx)
				if ctx.Err() != nil {
					status.Message = ptr(ctx.Err().Error())
				} else if !availability.Available {
					status.Message = ptr(availability.Reason)
				} else {
					if err := taskscope.Validate(ctx, domain.AppConfig{}, taskPath, updated); err != nil {
						// Do not leave a retryable canceled launch looking pending, and
						// never write this result over a replacement task incarnation.
						if currentErr := s.requireCurrentManifest(taskPath, updated); currentErr != nil {
							return errors.Join(err, currentErr)
						}
						status.Message = ptr(err.Error())
						updated = replaceWorkspaceToolLaunch(updated, status)
						updated.UpdatedAt = status.UpdatedAt
						return errors.Join(err, s.Manifests.Save(taskPath, updated))
					}
					if err := s.requireCurrentManifest(taskPath, updated); err != nil {
						return err
					}
					ctxData := TaskWorkspaceContext{TaskName: current.FolderName, TaskDirectory: mustAbs(taskPath), AgentsFile: filepath.Join(mustAbs(taskPath), "AGENTS.md"), Workspaces: append([]domain.ServiceWorkspace(nil), current.Services...)}
					if runErr := launcher.Open(ctx, ctxData); runErr != nil {
						status.Message = ptr(runErr.Error())
					} else {
						status.Status = domain.WorkspaceToolOpened
					}
				}
			}
			if err := s.requireCurrentManifest(taskPath, updated); err != nil {
				return err
			}
			updated = replaceWorkspaceToolLaunch(updated, status)
			updated.UpdatedAt = status.UpdatedAt
			if err := s.Manifests.Save(taskPath, updated); err != nil {
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (s *WorkspaceToolLaunchService) requireCurrentManifest(path string, expected domain.TaskManifest) error {
	current, err := s.Manifests.Load(path)
	if err != nil {
		return err
	}
	if !sameTaskManifest(current, expected) {
		return fmt.Errorf("任务清单已发生变化，请重新尝试启动")
	}
	return nil
}

func (s *WorkspaceToolLaunchService) Retry(ctx context.Context, taskPath string, manifest domain.TaskManifest, toolID string) (domain.TaskManifest, error) {
	if strings.TrimSpace(toolID) == "" {
		return domain.TaskManifest{}, fmt.Errorf("工作区工具 ID 不能为空")
	}
	return s.Launch(ctx, taskPath, manifest, []string{toolID})
}

func (s *WorkspaceToolLaunchService) now() string {
	if s.Now != nil {
		return s.Now().UTC().Format(time.RFC3339Nano)
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func replaceWorkspaceToolLaunch(manifest domain.TaskManifest, value domain.WorkspaceToolLaunch) domain.TaskManifest {
	launches := append([]domain.WorkspaceToolLaunch(nil), manifest.WorkspaceToolLaunches...)
	for i, current := range launches {
		if current.ToolID == value.ToolID {
			launches[i] = value
			manifest.WorkspaceToolLaunches = launches
			return manifest
		}
	}
	manifest.WorkspaceToolLaunches = append(launches, value)
	return manifest
}
func uniqueStringsNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
func sameTaskManifest(left, right domain.TaskManifest) bool {
	return reflect.DeepEqual(domain.NormalizeTaskManifest(left), domain.NormalizeTaskManifest(right))
}
