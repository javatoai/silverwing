package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func (s *TaskRootMigrationService) journalPath() string {
	return taskRootMigrationJournalPath(s.Paths)
}

func taskRootMigrationJournalPath(paths storage.Paths) string {
	return filepath.Join(paths.Home, "migrations", "task-root.json")
}

// Presence includes malformed journals: only absence confirms no recovery is
// pending. This read never starts recovery or creates application directories.
func TaskRootMigrationPending(ctx context.Context, paths storage.Paths) (*bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, err := os.Lstat(taskRootMigrationJournalPath(paths))
	pending := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &pending, nil
}
func (s *TaskRootMigrationService) writeJournal(j migrationJournal) error {
	if err := os.MkdirAll(filepath.Dir(s.journalPath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicDocument(s.journalPath(), string(data))
}
func (s *TaskRootMigrationService) readJournal() (migrationJournal, error) {
	data, err := os.ReadFile(s.journalPath())
	if err != nil {
		return migrationJournal{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var j migrationJournal
	if err := decoder.Decode(&j); err != nil {
		return migrationJournal{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return migrationJournal{}, fmt.Errorf("任务根目录迁移日志包含多个 JSON 值")
		}
		return migrationJournal{}, err
	}
	return j, nil
}

type migrationJournal struct {
	ID              string                        `json:"id"`
	Version         int                           `json:"version"`
	SourceRoot      string                        `json:"sourceRoot"`
	TargetRoot      string                        `json:"targetRoot"`
	Mode            TaskRootMigrationMode         `json:"mode"`
	Phase           string                        `json:"phase"`
	ConfigRoots     *TaskRootMigrationRootsChange `json:"configRoots"`
	RepositoryLocks []string                      `json:"repositoryLocks"`
	Tasks           []migrationJournalTask        `json:"tasks"`
}
type migrationJournalTask struct {
	SourceDirectory  string              `json:"sourceDirectory"`
	TargetDirectory  string              `json:"targetDirectory"`
	OriginalManifest domain.TaskManifest `json:"originalManifest"`
}

func validateMigrationJournal(j migrationJournal) error {
	if j.Version != 2 || !validMigrationID(j.ID) {
		return fmt.Errorf("任务根目录迁移日志版本或 ID 无效")
	}
	if j.Phase != "PREPARED" && j.Phase != "CONFIG_UPDATED" {
		return fmt.Errorf("任务根目录迁移日志阶段无效")
	}
	if j.Mode != TaskRootDirectSwitch && j.Mode != TaskRootSameFileStore && j.Mode != TaskRootCrossFileStore {
		return fmt.Errorf("任务根目录迁移日志模式无效")
	}
	if strings.TrimSpace(j.SourceRoot) == "" || strings.TrimSpace(j.TargetRoot) == "" {
		return fmt.Errorf("任务根目录迁移日志中的根目录为空")
	}
	src, dst := mustAbs(j.SourceRoot), mustAbs(j.TargetRoot)
	if src == dst || pathWithin(src, dst) || pathWithin(dst, src) {
		return fmt.Errorf("任务根目录迁移日志中的根目录不安全")
	}
	for _, t := range j.Tasks {
		s, d := mustAbs(t.SourceDirectory), mustAbs(t.TargetDirectory)
		if filepath.Dir(s) != src || filepath.Dir(d) != dst || filepath.Base(s) != t.OriginalManifest.TaskDirectoryName || filepath.Base(d) != t.OriginalManifest.TaskDirectoryName {
			return fmt.Errorf("任务根目录迁移日志包含越界任务路径")
		}

		if _, err := migrationWorkspaceIdentities(t); err != nil {
			return err
		}
	}
	if j.ConfigRoots == nil || j.ConfigRoots.Before.TaskRoot == nil || j.ConfigRoots.After.TaskRoot == nil ||
		!sameMigrationRoot(j.ConfigRoots.Before.TaskRoot, &src) || !sameMigrationRoot(j.ConfigRoots.After.TaskRoot, &dst) {
		return fmt.Errorf("任务根目录迁移日志的配置根路径状态无效")
	}
	if j.Mode != TaskRootDirectSwitch && sameMigrationRoot(j.ConfigRoots.Before.MaterialsRoot, &src) {
		return fmt.Errorf("任务根目录迁移日志的资料根与源任务根相同，目录已保留")
	}
	expectedMaterials, err := rebaseMigrationMaterialsRoot(j.ConfigRoots.Before.MaterialsRoot, journalMigrationPathMappings(j))
	if err != nil || !sameMigrationRoot(expectedMaterials, j.ConfigRoots.After.MaterialsRoot) {
		return fmt.Errorf("任务根目录迁移日志的资料根映射不一致")
	}
	return nil
}
func migrationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validMigrationID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
