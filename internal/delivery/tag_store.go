package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const tagOperationDirectory = "tag-operations"

type tagOperationStore struct{}

// Local phase transitions own the Git fields. A concurrent accepted probe owns
// GenbuStatus; retain it when advancing the same tag instead of writing an old
// in-memory status over the freshly persisted query.
func (s tagOperationStore) saveTransition(ctx context.Context, directory string, operation domain.TagOperation) (domain.TagOperation, error) {
	err := withTagRecordLock(ctx, directory, operation.OperationID, func() error {
		if current, err := s.load(directory, operation.OperationID); err == nil && value(operation.Tag) != "" && value(current.Tag) == value(operation.Tag) {
			operation.GenbuStatus = current.GenbuStatus
		}
		return writeAtomicJSON(filepath.Join(directory, tagOperationDirectory, operation.OperationID+".json"), operation)
	})
	return operation, err
}

func (tagOperationStore) save(ctx context.Context, taskDirectory string, operation domain.TagOperation) error {
	if !validOperationID(operation.OperationID) {
		return fmt.Errorf("Tag 操作 ID 不合法：%s", operation.OperationID)
	}
	directory := filepath.Join(taskDirectory, tagOperationDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return withTagRecordLock(ctx, taskDirectory, operation.OperationID, func() error {
		return writeAtomicJSON(filepath.Join(directory, operation.OperationID+".json"), operation)
	})
}

func (tagOperationStore) load(taskDirectory, operationID string) (domain.TagOperation, error) {
	if !validOperationID(operationID) {
		return domain.TagOperation{}, fmt.Errorf("Tag 操作 ID 不合法：%s", operationID)
	}
	path := filepath.Join(taskDirectory, tagOperationDirectory, operationID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.TagOperation{}, fmt.Errorf("找不到 Tag 构建记录：%s", operationID)
	}
	operation, err := decodeTagOperation(data)
	if err != nil {
		return domain.TagOperation{}, fmt.Errorf("Tag 构建记录损坏：%s：%w", path, err)
	}
	return normalizeTagOperation(operation), nil
}

func (tagOperationStore) list(taskDirectory string) ([]domain.TagOperation, error) {
	directory := filepath.Join(taskDirectory, tagOperationDirectory)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.TagOperation{}, nil
	}
	if err != nil {
		return nil, err
	}
	operations := make([]domain.TagOperation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		operation, decodeErr := decodeTagOperation(data)
		if decodeErr != nil {
			return nil, fmt.Errorf("Tag 构建记录损坏：%s：%w", entry.Name(), decodeErr)
		}
		operations = append(operations, normalizeTagOperation(operation))
	}
	sort.SliceStable(operations, func(i, j int) bool {
		if operations[i].UpdatedAt != operations[j].UpdatedAt {
			return operations[i].UpdatedAt > operations[j].UpdatedAt
		}
		return operations[i].OperationID > operations[j].OperationID
	})
	return operations, nil
}

func (s tagOperationStore) appendHistory(ctx context.Context, taskDirectory string, operation domain.TagOperation) error {
	if err := os.MkdirAll(taskDirectory, 0o755); err != nil {
		return err
	}
	historyPath := filepath.Join(taskDirectory, "tag-build-history.jsonl")
	// Windows byte-range locks also block writes through another file handle.
	// Serialize appends on a separate file instead of locking the JSONL itself.
	lock := filepath.Join(taskDirectory, tagOperationDirectory, ".history.lock")
	return withWaitingLock(ctx, lock, func() error {
		entry := domain.TagBuildHistoryEntry{
			OperationID: operation.OperationID, Timestamp: operation.UpdatedAt,
			FolderName: operation.FolderName, ServiceName: operation.ServiceName,
			SourceBranch: operation.SourceBranch, TargetBranch: operation.TargetBranch,
			TagMode: operation.TagMode, Tag: operation.Tag, State: operation.State,
			Message: operation.Message, BatchID: operation.BatchID,
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		if _, err = file.Write(append(data, '\n')); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		return err
	})
}

func (s tagOperationStore) updateIfUnchanged(ctx context.Context, taskDirectory string, expected domain.TagOperation, update func(domain.TagOperation) domain.TagOperation) (domain.TagOperation, bool, error) {
	path := filepath.Join(taskDirectory, tagOperationDirectory, expected.OperationID+".json")
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return domain.TagOperation{}, false, nil
	}
	var result domain.TagOperation
	changed := false
	err := withTagRecordLock(ctx, taskDirectory, expected.OperationID, func() error {
		current, err := s.load(taskDirectory, expected.OperationID)
		if err != nil {
			return nil // A probe never recreates a deleted or corrupt operation.
		}
		if !sameTagOperation(current, expected) {
			result = current
			return nil
		}
		result = update(current)
		if sameTagOperation(current, result) {
			return nil
		}
		if err := writeAtomicJSON(path, result); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return result, changed, err
}

func withTagRecordLock(ctx context.Context, taskDirectory, operationID string, operation func() error) error {
	directory := filepath.Join(taskDirectory, tagOperationDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	normalized, err := filepath.Abs(filepath.Join(taskDirectory, operationID))
	if err != nil {
		normalized = filepath.Clean(filepath.Join(taskDirectory, operationID))
	}
	existing := normalized
	for {
		if _, statErr := os.Stat(existing); statErr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	identity := normalized
	if real, realErr := filepath.EvalSymlinks(existing); realErr == nil {
		if existing == normalized {
			identity = real
		} else if relative, relErr := filepath.Rel(existing, normalized); relErr == nil {
			identity = filepath.Join(real, relative)
		}
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(identity))))
	path := filepath.Join(directory, "."+fmt.Sprintf("%x", digest[:8])+".lock")
	return withWaitingLock(ctx, path, operation)
}

func withWaitingLock(ctx context.Context, path string, operation func() error) error {
	for {
		release, err := storage.AcquireLock(path)
		if err == nil {
			defer release()
			return operation()
		}
		if !errors.Is(err, storage.ErrLockBusy) {
			return err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func writeAtomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(path), ".silverwing-write-*.tmp")
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

func sameTagOperation(left, right domain.TagOperation) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

func normalizeTagOperation(operation domain.TagOperation) domain.TagOperation {
	if operation.TagMode == "" {
		operation.TagMode = domain.TagBuildMergeToTargetBranch
	}
	if operation.GroupServiceID == "" {
		operation.GroupServiceID = operation.RepositoryID
	}
	if operation.ModuleID == "" {
		operation.ModuleID = "default"
	}
	if operation.GenbuStatus.Build == "" {
		operation.GenbuStatus.Build = domain.GenbuUnknown
	}
	if operation.GenbuStatus.UAT == "" {
		operation.GenbuStatus.UAT = domain.GenbuUnknown
	}
	if operation.GenbuStatus.Production == "" {
		operation.GenbuStatus.Production = domain.GenbuUnknown
	}
	if operation.ConflictFiles == nil {
		operation.ConflictFiles = []string{}
	}
	return operation
}

func decodeTagOperation(data []byte) (domain.TagOperation, error) {
	var operation domain.TagOperation
	if err := json.Unmarshal(data, &operation); err != nil {
		return domain.TagOperation{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return domain.TagOperation{}, err
	}
	for _, field := range []string{"operationId", "folderName", "serviceName", "repositoryId", "sourceBranch", "remote", "state", "createdAt", "updatedAt"} {
		if raw, found := fields[field]; !found || strings.TrimSpace(string(raw)) == "null" {
			return domain.TagOperation{}, fmt.Errorf("缺少必需字段 %s", field)
		}
	}
	if raw, found := fields["tagMode"]; found {
		if strings.TrimSpace(string(raw)) == "null" {
			return domain.TagOperation{}, errors.New("Tag 操作字段 tagMode 不能为空")
		}
	} else {
		operation.TagMode = domain.TagBuildMergeToTargetBranch
	}
	switch operation.TagMode {
	case domain.TagBuildMergeToTargetBranch, domain.TagBuildCurrentBranch:
	default:
		return domain.TagOperation{}, fmt.Errorf("不支持的 Tag 模式：%s", operation.TagMode)
	}
	switch operation.State {
	case domain.TagOperationCreated, domain.TagOperationPreflightPassed, domain.TagOperationSourceBranchPushed,
		domain.TagOperationTargetBranchPushed, domain.TagOperationLocalTagCreated, domain.TagOperationTagPushed,
		domain.TagOperationSuccess, domain.TagOperationConflict, domain.TagOperationFailed, domain.TagOperationPartial:
	default:
		return domain.TagOperation{}, fmt.Errorf("不支持的 Tag 操作状态：%s", operation.State)
	}
	if raw, found := fields["conflictFiles"]; found && strings.TrimSpace(string(raw)) == "null" {
		return domain.TagOperation{}, errors.New("Tag 操作字段 conflictFiles 不能为空")
	}
	for _, field := range []string{"groupServiceId", "moduleId"} {
		if raw, found := fields[field]; found && strings.TrimSpace(string(raw)) == "null" {
			return domain.TagOperation{}, fmt.Errorf("Tag 操作字段 %s 不能为空", field)
		}
	}
	if raw, found := fields["genbuStatus"]; found {
		if strings.TrimSpace(string(raw)) == "null" {
			return domain.TagOperation{}, errors.New("Tag 操作字段 genbuStatus 不能为空")
		}
		if err := validateGenbuStatus(raw); err != nil {
			return domain.TagOperation{}, err
		}
	}
	return normalizeTagOperation(operation), nil
}

func validateGenbuStatus(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, field := range []string{"build", "uat", "production"} {
		raw, found := fields[field]
		if !found {
			continue
		}
		if strings.TrimSpace(string(raw)) == "null" {
			return fmt.Errorf("Genbu 状态字段 %s 不能为空", field)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("读取 Genbu 状态字段 %s：%w", field, err)
		}
		switch domain.GenbuStageStatus(value) {
		case domain.GenbuInitial, domain.GenbuBuilding, domain.GenbuSuccess, domain.GenbuFailed, domain.GenbuUnknown:
		default:
			return fmt.Errorf("不支持的 Genbu 状态：%s", value)
		}
	}
	for _, field := range []string{"notFound", "stoppedByNewerRelease"} {
		if raw, found := fields[field]; found && strings.TrimSpace(string(raw)) == "null" {
			return fmt.Errorf("Genbu 状态字段 %s 不能为空", field)
		}
	}
	return nil
}

func validOperationID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
