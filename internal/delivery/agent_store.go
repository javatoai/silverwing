package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"silverwing-go/internal/storage"
)

func agentOperationPath(paths storage.Paths, operationID string) string {
	return filepath.Join(paths.Home, "agent-operations", operationID+".json")
}

func saveAgentOperation(paths storage.Paths, record AgentOperationRecord) error {
	if !validOperationID(record.OperationID) {
		return fmt.Errorf("操作 ID 不合法：%s", record.OperationID)
	}
	return writeAtomicJSON(agentOperationPath(paths, record.OperationID), record)
}

func loadAgentOperation(paths storage.Paths, operationID string) (AgentOperationRecord, error) {
	if !validOperationID(operationID) {
		return AgentOperationRecord{}, fmt.Errorf("操作 ID 不合法：%s", operationID)
	}
	path := agentOperationPath(paths, operationID)
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentOperationRecord{}, fmt.Errorf("找不到 Agent 操作：%s", operationID)
	}
	var record AgentOperationRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return AgentOperationRecord{}, fmt.Errorf("Agent 操作记录损坏：%s：%w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("Agent 操作记录包含多余 JSON 内容")
		}
		return AgentOperationRecord{}, fmt.Errorf("Agent 操作记录损坏：%s：%w", path, err)
	}
	if record.Request.ConfirmedBranchReuseKeys == nil {
		record.Request.ConfirmedBranchReuseKeys = []AgentBranchReuseKey{}
	}
	if record.BranchReuseConflicts == nil {
		record.BranchReuseConflicts = []AgentBranchReuseConflict{}
	}
	if err := validateAgentOperationRecord(record); err != nil {
		return AgentOperationRecord{}, fmt.Errorf("Agent 操作记录损坏：%s：%w", path, err)
	}
	return record, nil
}

func validateAgentOperationRecord(record AgentOperationRecord) error {
	switch record.State {
	case AgentOperationPlanned, AgentOperationApplied, AgentOperationFailed, AgentOperationExpired:
	default:
		return fmt.Errorf("不支持的 Agent 操作状态：%s", record.State)
	}
	if record.OperationID == "" || record.Nonce == "" || record.CreatedAt == "" || record.ExpiresAt == "" || record.Fingerprint == "" || record.TaskDirectory == "" {
		return fmt.Errorf("Agent 操作记录缺少必需字段")
	}
	if record.Documentation.FormatVersion != 2 || record.Documentation.RequirementTitle == "" || record.Documentation.DirectoryFolderName == "" {
		return fmt.Errorf("Agent 操作记录中的需求资料计划无效")
	}
	request := record.Request
	if request.FolderName == "" || request.FeatureBranch == "" || request.GroupID == "" || len(request.ServiceIDs) == 0 || request.RequirementLink == "" {
		return fmt.Errorf("Agent 操作请求缺少必需字段")
	}
	for _, conflict := range record.BranchReuseConflicts {
		if conflict.RemoteRefs == nil || conflict.OccupiedWorktreePaths == nil || conflict.LockedWorktreePaths == nil {
			return fmt.Errorf("分支复用冲突记录缺少列表字段")
		}
	}
	return nil
}

func withAgentOperationLock(ctx context.Context, paths storage.Paths, operationID string, operation func() error) error {
	if !validOperationID(operationID) {
		return fmt.Errorf("操作 ID 不合法：%s", operationID)
	}
	path := filepath.Join(paths.Locks, "agent-operation-"+operationID+".lock")
	release, err := storage.AcquireLock(path)
	if err != nil {
		return fmt.Errorf("Agent 操作正在被另一个执行器处理：%s: %w", operationID, err)
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}

func payloadValue(payload map[string]any, key string) any {
	if payload == nil {
		return nil
	}
	return payload[key]
}
