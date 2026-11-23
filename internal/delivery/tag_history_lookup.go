package delivery

import (
	"errors"
	"os"
	"path/filepath"

	"silverwing-go/internal/domain"
)

// ReadTagHistoryOperation loads one local history record from the already
// validated task directory, without touching other records or running a probe.
func ReadTagHistoryOperation(taskDirectory, operationID string) (domain.TagOperation, error) {
	if !validOperationID(operationID) {
		return domain.TagOperation{}, errors.New("Tag 操作 ID 不合法")
	}
	directory := filepath.Join(taskDirectory, tagOperationDirectory)
	parent, err := os.Lstat(directory)
	if err != nil || !parent.IsDir() {
		return domain.TagOperation{}, errors.New("找不到当前任务的 Tag 构建记录")
	}
	info, err := os.Lstat(filepath.Join(directory, operationID+".json"))
	if err != nil || !info.Mode().IsRegular() {
		return domain.TagOperation{}, errors.New("找不到当前任务的 Tag 构建记录")
	}
	operation, err := (tagOperationStore{}).load(taskDirectory, operationID)
	if err != nil {
		return domain.TagOperation{}, err
	}
	if operation.OperationID != operationID {
		return domain.TagOperation{}, errors.New("Tag 构建记录身份不匹配")
	}
	return operation, nil
}
