package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

func (s tagOperationStore) clear(ctx context.Context, taskDirectory string) (int, error) {
	return s.removeHistory(ctx, taskDirectory, nil, true)
}

func (s tagOperationStore) deleteSelected(ctx context.Context, taskDirectory string, operationIDs []string) (int, error) {
	selected := map[string]bool{}
	for _, id := range operationIDs {
		if strings.TrimSpace(id) != "" {
			selected[id] = true
		}
	}
	if len(selected) == 0 {
		return 0, nil
	}
	return s.removeHistory(ctx, taskDirectory, selected, false)
}

func (s tagOperationStore) removeHistory(ctx context.Context, taskDirectory string, selected map[string]bool, clear bool) (int, error) {
	directory := filepath.Join(taskDirectory, tagOperationDirectory)
	if err := requireLocalHistoryStorage(taskDirectory); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !clear && !selected[id] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		// Selection is compared with direct file names, never used as a path.
		err = withTagRecordLock(ctx, taskDirectory, id, func() error {
			if err := requireLocalHistoryStorage(taskDirectory); err != nil {
				return err
			}
			err := os.Remove(filepath.Join(directory, entry.Name()))
			if err == nil {
				deleted++
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		})
		if err != nil {
			return deleted, err
		}
	}
	history := filepath.Join(taskDirectory, "tag-build-history.jsonl")
	if _, err := os.Lstat(history); errors.Is(err, os.ErrNotExist) {
		return deleted, nil
	} else if err != nil {
		return deleted, err
	}
	err = withWaitingLock(ctx, filepath.Join(directory, ".history.lock"), func() error {
		if err := requireLocalHistoryStorage(taskDirectory); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if clear {
			err := os.Remove(history)
			if err == nil {
				deleted++
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		original, err := os.ReadFile(history)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rewritten := tagHistoryWithoutSelected(original, selected)
		if bytes.Equal(original, rewritten) {
			return nil
		}
		return writeAtomicHistoryBytes(history, rewritten)
	})
	return deleted, err
}

// Preserve every unselected or malformed line, including its newline bytes.
func tagHistoryWithoutSelected(original []byte, selected map[string]bool) []byte {
	retained := make([][]byte, 0)
	for _, line := range bytes.Split(original, []byte{'\n'}) {
		var fields map[string]json.RawMessage
		remove := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\r'}), &fields) == nil
		for _, name := range []string{"operationId", "timestamp", "folderName", "serviceName", "sourceBranch", "state"} {
			var text string
			value, present := fields[name]
			if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
				remove = false
				break
			}
		}
		var id, state string
		_ = json.Unmarshal(fields["operationId"], &id)
		_ = json.Unmarshal(fields["state"], &state)
		var entry domain.TagBuildHistoryEntry
		if json.Unmarshal(line, &entry) != nil || !validHistoryState(state) {
			remove = false
		}
		if mode, exists := fields["tagMode"]; exists && (bytes.Equal(bytes.TrimSpace(mode), []byte("null")) || entry.TagMode != domain.TagBuildCurrentBranch && entry.TagMode != domain.TagBuildMergeToTargetBranch) {
			remove = false
		}
		if !remove || !selected[id] {
			retained = append(retained, line)
		}
	}
	return bytes.Join(retained, []byte{'\n'})
}

func requireLocalHistoryStorage(taskDirectory string) error {
	task, err := os.Lstat(taskDirectory)
	if err != nil {
		return err
	}
	if !task.IsDir() || task.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Tag历史任务目录不是独立本地目录：%s", taskDirectory)
	}
	directory := filepath.Join(taskDirectory, tagOperationDirectory)
	info, err := os.Lstat(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Tag构建记录目录不是独立本地目录：%s", directory)
		}
		taskReal, taskErr := filepath.EvalSymlinks(taskDirectory)
		directoryReal, directoryErr := filepath.EvalSymlinks(directory)
		if taskErr != nil || directoryErr != nil {
			return errors.Join(taskErr, directoryErr)
		}
		if !samePath(filepath.Dir(directoryReal), taskReal) {
			return fmt.Errorf("Tag构建记录目录不在原任务内：%s", directory)
		}
	}
	history := filepath.Join(taskDirectory, "tag-build-history.jsonl")
	if info, err := os.Lstat(history); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Tag历史汇总不是普通文件：%s", history)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validHistoryState(state string) bool {
	switch domain.TagOperationState(state) {
	case domain.TagOperationCreated, domain.TagOperationPreflightPassed, domain.TagOperationSourceBranchPushed,
		domain.TagOperationTargetBranchPushed, domain.TagOperationLocalTagCreated, domain.TagOperationTagPushed,
		domain.TagOperationSuccess, domain.TagOperationConflict, domain.TagOperationPartial, domain.TagOperationFailed:
		return true
	}
	return false
}

func writeAtomicHistoryBytes(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Tag历史汇总不是普通文件：%s", path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tag-history-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
