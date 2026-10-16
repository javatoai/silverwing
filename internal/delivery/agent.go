package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type AgentBranchReuseKey struct {
	RepositoryID     string  `json:"repositoryId"`
	Branch           string  `json:"branch"`
	StateFingerprint *string `json:"stateFingerprint"`
}

type AgentCreateTaskRequest struct {
	FolderName               string                `json:"folderName"`
	FeatureBranch            string                `json:"featureBranch"`
	GroupID                  string                `json:"groupId"`
	ServiceIDs               []string              `json:"serviceIds"`
	RequirementLink          string                `json:"requirementLink"`
	RequirementTitle         *string               `json:"requirementTitle"`
	TaskNotes                string                `json:"taskNotes"`
	HandoffMarkdown          *string               `json:"handoffMarkdown"`
	ConfirmedBranchReuseKeys []AgentBranchReuseKey `json:"confirmedBranchReuseKeys"`
}

type AgentOperationState string

const (
	AgentOperationPlanned AgentOperationState = "PLANNED"
	AgentOperationApplied AgentOperationState = "APPLIED"
	AgentOperationFailed  AgentOperationState = "FAILED"
	AgentOperationExpired AgentOperationState = "EXPIRED"
)

type AgentBranchReuseConflict struct {
	Key                     AgentBranchReuseKey `json:"key"`
	ServiceName             string              `json:"serviceName"`
	ModuleName              string              `json:"moduleName"`
	LocalExists             bool                `json:"localExists"`
	RemoteRefs              []string            `json:"remoteRefs"`
	OccupiedWorktreePaths   []string            `json:"occupiedWorktreePaths"`
	LockedWorktreePaths     []string            `json:"lockedWorktreePaths"`
	RequiresForceAttach     bool                `json:"requiresForceAttach"`
	ReusesRemoteCloneTarget bool                `json:"reusesRemoteCloneTarget"`
}

type RequirementIdentity struct {
	Space      string `json:"space"`
	Kind       string `json:"kind"`
	WorkItemID string `json:"workItemId"`
}

// DocumentationPlan intentionally mirrors RequirementDocumentationPlan's
// persisted JSON. PreparationAPI adapts the documents package without making
// delivery depend on its storage implementation.
type DocumentationPlan struct {
	FormatVersion           int                 `json:"formatVersion"`
	Identity                RequirementIdentity `json:"identity"`
	RequirementTitle        string              `json:"requirementTitle"`
	RequirementDirectory    string              `json:"requirementDirectory"`
	DocumentationDirectory  string              `json:"documentationDirectory"`
	DirectoryFolderName     string              `json:"directoryFolderName"`
	ReusedExistingDirectory bool                `json:"reusedExistingDirectory"`
}

type AgentOperationRecord struct {
	OperationID          string                     `json:"operationId"`
	State                AgentOperationState        `json:"state"`
	Nonce                string                     `json:"nonce"`
	CreatedAt            string                     `json:"createdAt"`
	ExpiresAt            string                     `json:"expiresAt"`
	Fingerprint          string                     `json:"fingerprint"`
	Request              AgentCreateTaskRequest     `json:"request"`
	Documentation        DocumentationPlan          `json:"documentation"`
	TaskDirectory        string                     `json:"taskDirectory"`
	BranchReuseConflicts []AgentBranchReuseConflict `json:"branchReuseConflicts"`
	ConfirmedAt          *string                    `json:"confirmedAt"`
	HandoffPath          *string                    `json:"handoffPath"`
	Message              *string                    `json:"message"`
}

type AgentInspection struct {
	TaskRoot                         *string `json:"taskRoot"`
	RequirementMaterialsRoot         *string `json:"requirementMaterialsRoot"`
	RequirementMaterialsSubdirectory *string `json:"requirementMaterialsSubdirectory"`
	CanPlan                          bool    `json:"canPlan"`
	ConfigurationMessage             *string `json:"configurationMessage"`
}

type AgentMaterialization struct {
	Plan                        DocumentationPlan       `json:"plan"`
	AgentContext                domain.AgentTaskContext `json:"agentContext"`
	CreatedRequirementDirectory bool                    `json:"createdRequirementDirectory"`
}

type PreparationAPI struct {
	Plan        func(context.Context, domain.AppConfig, string, string, string) (json.RawMessage, error)
	Materialize func(context.Context, domain.AppConfig, json.RawMessage) (json.RawMessage, error)
}

func (s *Service) SetPreparationAPI(api PreparationAPI) { s.preparation = api }

func (s *Service) executeAgent(ctx context.Context, kind string, payload map[string]any) (any, error) {
	switch kind {
	case "agent.inspect":
		return s.AgentInspect()
	case "agent.plan":
		var request AgentCreateTaskRequest
		data, err := json.Marshal(payloadValue(payload, "request"))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || string(data) == "null" {
			data, err = json.Marshal(payload)
		}
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&request); err != nil {
			return nil, err
		}
		return s.AgentPlan(ctx, request)
	case "agent.apply":
		return s.AgentApply(ctx, stringValue(payload, "operationId"), stringValue(payload, "nonce"))
	case "agent.status":
		return s.AgentStatus(stringValue(payload, "operationId"))
	default:
		return nil, fmt.Errorf("Agent 操作不支持：%s", kind)
	}
}

func (s *Service) AgentInspect() (AgentInspection, error) {
	config, err := s.config.Load()
	if err != nil {
		return AgentInspection{}, err
	}
	missing := make([]string, 0, 3)
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		missing = append(missing, "任务根目录")
	}
	if config.RequirementMaterialsRoot == nil || strings.TrimSpace(*config.RequirementMaterialsRoot) == "" {
		missing = append(missing, "任务资料根目录")
	}
	if config.RequirementMaterialsSubdirectory == nil || strings.TrimSpace(*config.RequirementMaterialsSubdirectory) == "" {
		missing = append(missing, "任务资料子目录")
	}
	result := AgentInspection{
		TaskRoot: config.TaskRoot, RequirementMaterialsRoot: config.RequirementMaterialsRoot,
		RequirementMaterialsSubdirectory: config.RequirementMaterialsSubdirectory, CanPlan: len(missing) == 0,
	}
	if len(missing) != 0 {
		result.ConfigurationMessage = stringPointer(strings.Join(missing, "、") + "尚未配置")
	}
	return result, nil
}

func (s *Service) AgentPlan(ctx context.Context, request AgentCreateTaskRequest) (AgentOperationRecord, error) {
	if err := validateAgentRequest(request); err != nil {
		return AgentOperationRecord{}, err
	}
	if request.TaskNotes == "" {
		request.TaskNotes = ""
	}
	request.HandoffMarkdown = stringPointer(safeHandoffMarkdown(request.HandoffMarkdown))
	if request.ConfirmedBranchReuseKeys == nil {
		request.ConfirmedBranchReuseKeys = []AgentBranchReuseKey{}
	}
	config, err := s.config.Load()
	if err != nil {
		return AgentOperationRecord{}, err
	}
	preflight, err := s.agentPreflight(ctx, config, request)
	if err != nil {
		return AgentOperationRecord{}, err
	}
	now := time.Now().UTC()
	message := "计划已生成；向用户展示该计划后，使用 operationId 与 nonce 执行 apply。"
	if len(preflight.conflicts) != 0 {
		message = "检测到分支复用；只有用户明确确认输出中的完整 key 后，才能在新计划请求中提供 confirmedBranchReuseKeys。"
	}
	record := AgentOperationRecord{
		OperationID: newUUID(), State: AgentOperationPlanned, Nonce: newUUID(),
		CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano),
		Fingerprint: preflight.fingerprint, Request: request, Documentation: preflight.documentation,
		TaskDirectory: preflight.taskDirectory, BranchReuseConflicts: preflight.conflicts, Message: &message,
	}
	if err := saveAgentOperation(s.paths, record); err != nil {
		return AgentOperationRecord{}, err
	}
	return record, nil
}

func validateAgentRequest(request AgentCreateTaskRequest) error {
	if strings.TrimSpace(request.FolderName) == "" {
		return errors.New("任务目录名称不能为空")
	}
	if strings.TrimSpace(request.FeatureBranch) == "" {
		return errors.New("任务分支不能为空")
	}
	if strings.TrimSpace(request.GroupID) == "" {
		return errors.New("任务组不能为空")
	}
	if len(request.ServiceIDs) == 0 {
		return errors.New("至少选择一个服务")
	}
	if strings.TrimSpace(request.RequirementLink) == "" {
		return errors.New("Agent CLI 任务必须提供需求链接")
	}
	return nil
}

type agentPreflightResult struct {
	documentation     DocumentationPlan
	taskDirectory     string
	fingerprint       string
	conflicts         []AgentBranchReuseConflict
	documentationJSON json.RawMessage
}

func (s *Service) agentPreflight(ctx context.Context, config domain.AppConfig, request AgentCreateTaskRequest) (agentPreflightResult, error) {
	if s.preparation.Plan == nil {
		return agentPreflightResult{}, errors.New("需求资料准备服务尚未连接")
	}
	if s.tasks == nil {
		return agentPreflightResult{}, errors.New("任务工作区服务尚未连接")
	}
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		return agentPreflightResult{}, errors.New("尚未配置任务根目录")
	}
	taskRoot, err := filepath.Abs(*config.TaskRoot)
	if err != nil {
		return agentPreflightResult{}, err
	}
	folder, err := workspace.RequireValidTaskDirectoryName(request.FolderName)
	if err != nil {
		return agentPreflightResult{}, err
	}
	taskDirectory := filepath.Clean(filepath.Join(taskRoot, folder))
	if !samePath(filepath.Dir(taskDirectory), taskRoot) {
		return agentPreflightResult{}, errors.New("任务目录必须是任务根目录的直接子目录")
	}
	requestedTitle := value(request.RequirementTitle)
	docBytes, err := s.preparation.Plan(ctx, config, request.RequirementLink, requestedTitle, folder)
	if err != nil {
		return agentPreflightResult{}, err
	}
	var documentation DocumentationPlan
	if err := json.Unmarshal(docBytes, &documentation); err != nil {
		return agentPreflightResult{}, fmt.Errorf("读取需求资料计划：%w", err)
	}
	domainRequest := workspace.CreateTaskRequest{
		FolderName: folder, FeatureBranch: request.FeatureBranch, GroupID: request.GroupID,
		ServiceIDs: append([]string(nil), request.ServiceIDs...), RequirementLink: request.RequirementLink,
		TaskNotes: request.TaskNotes, ConfirmedBranchReuseKeys: toWorkspaceReuseKeys(request.ConfirmedBranchReuseKeys),
	}
	conflicts, err := s.tasks.InspectCreateBranchReuse(ctx, config, domainRequest)
	if err != nil {
		return agentPreflightResult{}, err
	}
	allConflicts := make([]AgentBranchReuseConflict, 0, len(conflicts))
	unconfirmed := make([]AgentBranchReuseConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		converted := toAgentConflict(conflict)
		allConflicts = append(allConflicts, converted)
		if !requestConfirms(request.ConfirmedBranchReuseKeys, converted.Key) {
			unconfirmed = append(unconfirmed, converted)
		}
	}
	if _, err := os.Lstat(taskDirectory); err == nil {
		return agentPreflightResult{}, fmt.Errorf("目标任务目录已存在：%s", taskDirectory)
	} else if !os.IsNotExist(err) {
		return agentPreflightResult{}, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return agentPreflightResult{}, err
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return agentPreflightResult{}, err
	}
	conflictsJSON, err := json.Marshal(allConflicts)
	if err != nil {
		return agentPreflightResult{}, err
	}
	identity := strings.Join([]string{string(configJSON), string(requestJSON), taskDirectory, "taskDirectoryExists=false", string(conflictsJSON)}, "\n")
	digest := sha256.Sum256([]byte(identity))
	return agentPreflightResult{
		documentation: documentation, documentationJSON: docBytes, taskDirectory: taskDirectory,
		fingerprint: fmt.Sprintf("%x", digest[:]), conflicts: unconfirmed,
	}, nil
}

func toWorkspaceReuseKeys(keys []AgentBranchReuseKey) []workspace.BranchReuseKey {
	result := make([]workspace.BranchReuseKey, 0, len(keys))
	for _, key := range keys {
		result = append(result, workspace.BranchReuseKey{RepositoryID: key.RepositoryID, Branch: key.Branch, StateFingerprint: value(key.StateFingerprint)})
	}
	return result
}

func toAgentConflict(conflict workspace.BranchReuseConflict) AgentBranchReuseConflict {
	return AgentBranchReuseConflict{
		Key:         AgentBranchReuseKey{RepositoryID: conflict.Key.RepositoryID, Branch: conflict.Key.Branch, StateFingerprint: nonEmptyPointer(conflict.Key.StateFingerprint)},
		ServiceName: conflict.ServiceName, ModuleName: conflict.ModuleName, LocalExists: conflict.LocalExists,
		RemoteRefs: append([]string{}, conflict.RemoteRefs...), OccupiedWorktreePaths: append([]string{}, conflict.OccupiedWorktreePaths...),
		LockedWorktreePaths: append([]string{}, conflict.LockedWorktreePaths...), RequiresForceAttach: conflict.RequiresForceAttach,
		ReusesRemoteCloneTarget: conflict.ReusesRemoteCloneTarget,
	}
}

func requestConfirms(requested []AgentBranchReuseKey, key AgentBranchReuseKey) bool {
	for _, candidate := range requested {
		if candidate.RepositoryID == key.RepositoryID && candidate.Branch == key.Branch && sameOptionalString(candidate.StateFingerprint, key.StateFingerprint) {
			return true
		}
	}
	return false
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func safeHandoffMarkdown(supplied *string) string {
	markdown := strings.TrimSpace(value(supplied))
	if markdown == "" {
		return handoffTemplate()
	}
	return workspace.SanitizeHandoffMarkdown(markdown)
}

func (s *Service) AgentApply(ctx context.Context, operationID, nonce string) (AgentOperationRecord, error) {
	if !validOperationID(operationID) {
		return AgentOperationRecord{}, fmt.Errorf("操作 ID 不合法")
	}
	var record AgentOperationRecord
	err := withAgentOperationLock(ctx, s.paths, operationID, func() error {
		current, err := loadAgentOperation(s.paths, operationID)
		if err != nil {
			return err
		}
		if current.Nonce != nonce {
			return errors.New("确认 nonce 不匹配；请使用 plan 返回的原值")
		}
		if current.State == AgentOperationApplied {
			record = current
			return nil
		}
		if current.State != AgentOperationPlanned {
			return fmt.Errorf("操作不处于可执行状态：%s", current.State)
		}
		expires, parseErr := time.Parse(time.RFC3339Nano, current.ExpiresAt)
		if parseErr != nil {
			return fmt.Errorf("Agent 操作过期时间损坏：%w", parseErr)
		}
		if time.Now().After(expires) {
			current.State, current.Message = AgentOperationExpired, stringPointer("计划已过期，请重新生成计划")
			if err := saveAgentOperation(s.paths, current); err != nil {
				return err
			}
			record = current
			return errors.New(*current.Message)
		}
		applied, applyErr := s.applyAgentPlan(ctx, current)
		if applyErr != nil {
			current.State, current.ConfirmedAt, current.Message = AgentOperationFailed, stringPointer(time.Now().UTC().Format(time.RFC3339Nano)), stringPointer(applyErr.Error())
			if saveErr := saveAgentOperation(s.paths, current); saveErr != nil {
				return errors.Join(applyErr, saveErr)
			}
			return applyErr
		}
		if err := saveAgentOperation(s.paths, applied); err != nil {
			return err
		}
		record = applied
		return nil
	})
	if err != nil {
		return record, err
	}
	return record, nil
}

func (s *Service) applyAgentPlan(ctx context.Context, current AgentOperationRecord) (AgentOperationRecord, error) {
	config, err := s.config.Load()
	if err != nil {
		return current, err
	}
	fresh, err := s.agentPreflight(ctx, config, current.Request)
	if err != nil {
		return current, err
	}
	if fresh.fingerprint != current.Fingerprint {
		return current, errors.New("环境指纹已变化（配置、需求资料目录、目标路径或分支状态不同）；请重新生成计划并再次确认")
	}
	if !samePath(fresh.taskDirectory, current.TaskDirectory) {
		return current, errors.New("Agent 操作记录中的任务路径与当前计划不一致；请重新生成计划")
	}
	if len(fresh.conflicts) != 0 {
		return current, errors.New("仍存在未确认的分支复用；请重新生成计划，并将用户确认过的完整 key 写入 confirmedBranchReuseKeys")
	}
	if s.preparation.Materialize == nil {
		return current, errors.New("需求资料准备服务尚未连接")
	}
	materializedBytes, err := s.preparation.Materialize(ctx, config, fresh.documentationJSON)
	if err != nil {
		return current, err
	}
	var materialized AgentMaterialization
	if err := json.Unmarshal(materializedBytes, &materialized); err != nil {
		return current, fmt.Errorf("读取需求资料创建结果：%w", err)
	}
	handoff := value(current.Request.HandoffMarkdown)
	handoffPath := filepath.Join(fresh.taskDirectory, workspace.AgentWorkspaceDir, workspace.HandoffFileName)
	_, err = s.tasks.CreateTask(ctx, config, workspace.CreateTaskRequest{
		FolderName: current.Request.FolderName, FeatureBranch: current.Request.FeatureBranch,
		GroupID: current.Request.GroupID, ServiceIDs: append([]string(nil), current.Request.ServiceIDs...),
		RequirementLink: current.Request.RequirementLink, TaskNotes: current.Request.TaskNotes,
		ConfirmedBranchReuseKeys: toWorkspaceReuseKeys(current.Request.ConfirmedBranchReuseKeys),
		AgentContext:             &materialized.AgentContext, AgentHandoffMarkdown: &handoff,
	})
	if err != nil {
		return current, err
	}
	message := "任务、AGENTS.md、.workspace/HANDOFF.md 与需求过程文档已创建；请重新读取状态后继续。"
	return AgentOperationRecord{
		OperationID: current.OperationID, State: AgentOperationApplied, Nonce: current.Nonce,
		CreatedAt: current.CreatedAt, ExpiresAt: current.ExpiresAt, Fingerprint: current.Fingerprint,
		Request: current.Request, Documentation: materialized.Plan, TaskDirectory: fresh.taskDirectory,
		BranchReuseConflicts: current.BranchReuseConflicts, ConfirmedAt: stringPointer(time.Now().UTC().Format(time.RFC3339Nano)),
		HandoffPath: &handoffPath, Message: &message,
	}, nil
}

func (s *Service) AgentStatus(operationID string) (AgentOperationRecord, error) {
	return loadAgentOperation(s.paths, operationID)
}
