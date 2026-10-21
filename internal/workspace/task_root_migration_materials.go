package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

// TaskRootMigrationPathMapping describes a task directory actually transferred
// by this migration. A task-root prefix alone does not prove that data moved.
type TaskRootMigrationPathMapping struct {
	SourceDirectory string `json:"sourceDirectory"`
	TargetDirectory string `json:"targetDirectory"`
}

type TaskRootMigrationConfigRoots struct {
	TaskRoot      *string `json:"taskRoot"`
	MaterialsRoot *string `json:"materialsRoot"`
}

type TaskRootMigrationRootsChange struct {
	Before TaskRootMigrationConfigRoots `json:"before"`
	After  TaskRootMigrationConfigRoots `json:"after"`
}

type TaskRootMigrationTaskMaterials struct {
	TaskDirectoryName            string  `json:"taskDirectoryName"`
	WriteRootBefore              *string `json:"writeRootBefore"`
	WriteRootAfter               *string `json:"writeRootAfter"`
	DocumentationDirectoryBefore *string `json:"documentationDirectoryBefore"`
	DocumentationDirectoryAfter  *string `json:"documentationDirectoryAfter"`
}

// RebaseTaskRootMigrationPath is also usable by document state owners. It does
// not read or write files, and leaves every path outside a transferred task
// unchanged. DIRECT_SWITCH supplies no mappings and moves no materials.
func RebaseTaskRootMigrationPath(path string, mappings []TaskRootMigrationPathMapping) (string, bool, error) {
	if path == "" || len(mappings) == 0 {
		return path, false, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	for _, mapping := range mappings {
		if !filepath.IsAbs(mapping.SourceDirectory) || !filepath.IsAbs(mapping.TargetDirectory) {
			return "", false, fmt.Errorf("任务迁移路径映射必须是绝对路径")
		}
		if !pathWithin(mapping.SourceDirectory, absolute) {
			continue
		}
		relative, err := filepath.Rel(mapping.SourceDirectory, absolute)
		if err != nil {
			return "", false, err
		}
		return filepath.Clean(filepath.Join(mapping.TargetDirectory, relative)), true, nil
	}
	return path, false, nil
}

func cloneMigrationPath(path *string) *string {
	if path == nil {
		return nil
	}
	copy := *path
	return &copy
}

func rebaseMigrationPath(path *string, mappings []TaskRootMigrationPathMapping) (*string, error) {
	if path == nil {
		return nil, nil
	}
	rebased, _, err := RebaseTaskRootMigrationPath(*path, mappings)
	if err != nil {
		return nil, err
	}
	return &rebased, nil
}

func rebaseMigrationMaterialsRoot(path *string, mappings []TaskRootMigrationPathMapping) (*string, error) {
	if path == nil {
		return nil, nil
	}
	rebased, changed, err := RebaseTaskRootMigrationPath(strings.TrimSpace(*path), mappings)
	if err != nil {
		return nil, err
	}
	if !changed {
		return cloneMigrationPath(path), nil
	}
	return &rebased, nil
}

func cloneMigrationManifest(original domain.TaskManifest) domain.TaskManifest {
	copy := original
	if original.Services != nil {
		copy.Services = append([]domain.ServiceWorkspace{}, original.Services...)
	}
	copy.RequirementMaterials.WriteRoot = cloneMigrationPath(original.RequirementMaterials.WriteRoot)
	if original.AgentContext != nil {
		context := *original.AgentContext
		copy.AgentContext = &context
	}
	return copy
}

// Normal transfer and committed recovery use this same transformation, with
// mappings for all tasks: another task may share materials stored in this one.
// Copy fields before replacing them so the original journal snapshot is intact.
func migratedTaskManifest(original domain.TaskManifest, mappings []TaskRootMigrationPathMapping) (domain.TaskManifest, error) {
	updated := cloneMigrationManifest(original)
	var err error
	updated.RequirementMaterials.WriteRoot, err = rebaseMigrationPath(original.RequirementMaterials.WriteRoot, mappings)
	if err != nil {
		return domain.TaskManifest{}, err
	}
	if updated.AgentContext != nil {
		updated.AgentContext.DocumentationDirectory, _, err = RebaseTaskRootMigrationPath(original.AgentContext.DocumentationDirectory, mappings)
		if err != nil {
			return domain.TaskManifest{}, err
		}
	}
	for i, workspace := range original.Services {
		updated.Services[i].WorktreePath, _, err = RebaseTaskRootMigrationPath(workspace.WorktreePath, mappings)
		if err != nil {
			return domain.TaskManifest{}, err
		}
		if workspace.Strategy == domain.WorkspaceIndependentClone {
			updated.Services[i].RepositoryPath = updated.Services[i].WorktreePath
		}
	}
	return updated, nil
}

func planMigrationPathMappings(tasks []migrationTask) []TaskRootMigrationPathMapping {
	mappings := make([]TaskRootMigrationPathMapping, 0, len(tasks))
	for _, task := range tasks {
		mappings = append(mappings, TaskRootMigrationPathMapping{SourceDirectory: task.Source, TargetDirectory: task.Target})
	}
	return mappings
}

func journalMigrationPathMappings(journal migrationJournal) []TaskRootMigrationPathMapping {
	mappings := make([]TaskRootMigrationPathMapping, 0, len(journal.Tasks))
	for _, task := range journal.Tasks {
		mappings = append(mappings, TaskRootMigrationPathMapping{SourceDirectory: task.SourceDirectory, TargetDirectory: task.TargetDirectory})
	}
	return mappings
}

func migrationDraftRelocations(journal migrationJournal, reverse bool) ([]documents.MarkdownDraftTaskRelocation, error) {
	mappings := journalMigrationPathMappings(journal)
	changes := make([]documents.MarkdownDraftTaskRelocation, 0, len(journal.Tasks))
	for _, task := range journal.Tasks {
		root := task.OriginalManifest.RequirementMaterials.WriteRoot
		if root == nil || *root == "" {
			continue
		}
		// Draft identities already canonicalize absolute paths. The task ID is
		// the directory basename used by Snapshot and desktop document actions.
		previousRoot, err := filepath.Abs(*root)
		if err != nil {
			return nil, err
		}
		nextRoot, _, err := RebaseTaskRootMigrationPath(previousRoot, mappings)
		if err != nil {
			return nil, err
		}
		change := documents.MarkdownDraftTaskRelocation{
			TaskID: filepath.Base(task.SourceDirectory), PreviousTaskPath: task.SourceDirectory, NextTaskPath: task.TargetDirectory,
			PreviousMaterialsRoot: previousRoot, NextMaterialsRoot: nextRoot,
		}
		if reverse {
			change.PreviousTaskPath, change.NextTaskPath = change.NextTaskPath, change.PreviousTaskPath
			change.PreviousMaterialsRoot, change.NextMaterialsRoot = change.NextMaterialsRoot, change.PreviousMaterialsRoot
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (s *TaskRootMigrationService) relocateMigrationDrafts(ctx context.Context, journal migrationJournal, reverse bool) error {
	changes, err := migrationDraftRelocations(journal, reverse)
	if err != nil {
		return fmt.Errorf("任务迁移的 Markdown 草稿身份无效，目录与日志已保留：%w", err)
	}
	if len(changes) == 0 {
		return nil
	}
	if s.Drafts == nil {
		return fmt.Errorf("任务迁移缺少 Markdown 草稿服务，目录与日志已保留")
	}
	if err := s.Drafts.RelocateMarkdownDrafts(ctx, changes); err != nil {
		return fmt.Errorf("任务迁移的 Markdown 草稿迁移失败，目录与日志已保留：%w", err)
	}
	return nil
}

// Compensation and startup recovery must not inherit a canceled migration
// context. A failed cache batch prevents any directory cleanup or rollback.
func (s *TaskRootMigrationService) recoverMigrationDrafts(journal migrationJournal, reverse bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.relocateMigrationDrafts(ctx, journal, reverse)
}

func migrationConfigRoots(config domain.AppConfig) TaskRootMigrationConfigRoots {
	return TaskRootMigrationConfigRoots{TaskRoot: cloneMigrationPath(config.TaskRoot), MaterialsRoot: cloneMigrationPath(config.RequirementMaterialsRoot)}
}

func sameMigrationRoot(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if strings.TrimSpace(*a) == "" || strings.TrimSpace(*b) == "" {
		return *a == *b
	}
	return samePath(mustAbs(strings.TrimSpace(*a)), mustAbs(strings.TrimSpace(*b)))
}

func sameMigrationConfigRoots(a, b TaskRootMigrationConfigRoots) bool {
	return sameMigrationRoot(a.TaskRoot, b.TaskRoot) && sameMigrationRoot(a.MaterialsRoot, b.MaterialsRoot)
}

func migrationConfigCommitted(config domain.AppConfig, journal migrationJournal) (bool, error) {
	if journal.ConfigRoots == nil {
		return false, fmt.Errorf("任务迁移日志缺少配置根路径状态，目录与日志已保留")
	}
	current := migrationConfigRoots(config)
	if sameMigrationConfigRoots(current, journal.ConfigRoots.After) {
		return true, nil
	}
	if journal.Phase == "PREPARED" && sameMigrationConfigRoots(current, journal.ConfigRoots.Before) {
		return false, nil
	}
	return false, fmt.Errorf("任务根与资料根配置不符合迁移日志的完整前后状态，目录与日志已保留；请核对两根配置后恢复")
}

func (preview *TaskRootMigrationPreview) prepareMaterials(config domain.AppConfig, tasks []migrationTask) error {
	before := migrationConfigRoots(config)
	preview.Roots = TaskRootMigrationRootsChange{Before: before, After: migrationConfigRoots(config)}
	preview.Roots.After.TaskRoot = cloneMigrationPath(&preview.TargetRoot)
	preview.PathMappings = planMigrationPathMappings(tasks)
	preview.Materials = make([]TaskRootMigrationTaskMaterials, 0, len(tasks))
	var err error
	preview.Roots.After.MaterialsRoot, err = rebaseMigrationMaterialsRoot(before.MaterialsRoot, preview.PathMappings)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		updated, err := migratedTaskManifest(task.Manifest, preview.PathMappings)
		if err != nil {
			return err
		}
		materials := TaskRootMigrationTaskMaterials{TaskDirectoryName: task.Manifest.TaskDirectoryName,
			WriteRootBefore: cloneMigrationPath(task.Manifest.RequirementMaterials.WriteRoot), WriteRootAfter: cloneMigrationPath(updated.RequirementMaterials.WriteRoot)}
		if task.Manifest.AgentContext != nil {
			materials.DocumentationDirectoryBefore = cloneMigrationPath(&task.Manifest.AgentContext.DocumentationDirectory)
			materials.DocumentationDirectoryAfter = cloneMigrationPath(&updated.AgentContext.DocumentationDirectory)
		}
		preview.Materials = append(preview.Materials, materials)
	}
	return nil
}
