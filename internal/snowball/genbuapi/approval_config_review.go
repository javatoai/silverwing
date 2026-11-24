package genbuapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	approvalConfigDetailPath = "/genbu/basic/api/configMapDetail"
	approvalPermissionsPath  = "/genbu/admin/api/user/operation"
)

var (
	ErrApprovalReviewStale    = errors.New("审批申请或当前配置已变化，请重新查看差异")
	ErrApprovalOutcomeUnknown = errors.New("审批结果待核实，请刷新审批记录")
	ErrApprovalRejected       = errors.New("Genbu 拒绝同意审批")
)

type ApprovalConfigReview struct {
	Namespace  string                     `json:"namespace"`
	ConfigMap  string                     `json:"config_map"`
	Revision   string                     `json:"revision"`
	Files      []ApprovalConfigFileChange `json:"files"`
	CanApprove bool                       `json:"can_approve"`
}

type ApprovalConfigFileChange struct {
	Name         string `json:"name"`
	Before       string `json:"before"`
	After        string `json:"after"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
}

type ApprovalConfigApproveRequest struct {
	ID       int64
	Service  string
	Revision string
}

type ConfigApprovalReadRequest struct {
	ID          int64
	Service     string
	Environment string
	IncludeDiff bool
}

// ReadConfigApproval reads an exact ConfigMap approval in UAT or PRD. It
// does not check approval permissions or prepare a write.
func (client *Client) ReadConfigApproval(ctx context.Context, request ConfigApprovalReadRequest) (ApprovalDetailResult, error) {
	environment, err := normalizeEnvironmentName(request.Environment)
	if err != nil {
		return ApprovalDetailResult{}, err
	}
	if request.ID < 1 || strings.TrimSpace(request.Service) == "" {
		return ApprovalDetailResult{}, errors.New("请选择有效的 Genbu 服务和审批编号")
	}
	var result ApprovalDetailResult
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		row, err := worker.findApproval(request.ID, token, environment)
		if err != nil {
			return err
		}
		secrets := []string{client.ConfigSnapshot().Password, token}
		if request.IncludeDiff {
			result, _, err = worker.configApprovalComparison(row, request.Service, environment, token, secrets)
		} else {
			result, err = configApprovalSummary(row, request.Service, environment, secrets)
		}
		if err == nil {
			content, _ := row["content"].(map[string]any)
			if valueString(content, "env") == environment {
				addApprovalFields(result.Details, content, map[string]string{"name": "ConfigMap", "namespace": "命名空间"}, secrets)
				if names, namesErr := configFileNames(content); namesErr == nil {
					for _, name := range names {
						result.FileNames = append(result.FileNames, redactApprovalText(name, secrets...))
					}
				}
			}
		}
		return err
	})
	return result, err
}

// ApproveUATConfigChange rechecks the proposal, its baseline and the current
// user's permission before writing one decision. The receipt is read back.
func (client *Client) ApproveUATConfigChange(ctx context.Context, request ApprovalConfigApproveRequest) (ApprovalDetailResult, error) {
	if request.ID < 1 || strings.TrimSpace(request.Service) == "" || len(request.Revision) != hex.EncodedLen(sha256.Size) {
		return ApprovalDetailResult{}, ErrApprovalReviewStale
	}
	if _, err := hex.DecodeString(request.Revision); err != nil {
		return ApprovalDetailResult{}, ErrApprovalReviewStale
	}
	var reviewed ApprovalDetailResult
	err := client.requestToken(ctx, func(worker *genbuClient, token string) error {
		row, err := worker.findApproval(request.ID, token, "UAT")
		if err != nil {
			return err
		}
		secrets := []string{client.ConfigSnapshot().Password, token}
		detail, appID, err := worker.configApprovalReview(row, request.Service, token, secrets)
		if err != nil {
			return err
		}
		if detail.Review == nil || detail.Review.Revision != request.Revision {
			return ErrApprovalReviewStale
		}
		if !detail.Review.CanApprove {
			return ErrApprovalRejected
		}
		reviewed = detail
		body, err := json.Marshal(struct {
			ID     int64   `json:"id"`
			Status int     `json:"status"`
			Reason *string `json:"reason"`
		}{request.ID, 1, approvalReason(row)})
		if err != nil {
			return err
		}
		// This transport prevents body replay, follows no redirects, and retains
		// admission failures. Its write errors cannot cause a token refresh retry.
		err = worker.writeUATMutation(http.MethodPut, approvalPath, body, appID, token)
		switch {
		case errors.Is(err, ErrUATPodSubmitUnknown):
			return ErrApprovalOutcomeUnknown
		case errors.Is(err, ErrUATPodSubmitRejected):
			return ErrApprovalRejected
		default:
			return err
		}
	})
	if err != nil && !errors.Is(err, ErrApprovalOutcomeUnknown) {
		return ApprovalDetailResult{}, err
	}
	reviewed.Review.CanApprove = false
	var receipt ApprovalDetailResult
	readErr := client.requestToken(ctx, func(worker *genbuClient, token string) error {
		row, err := worker.findApproval(request.ID, token, "UAT")
		if err != nil {
			return err
		}
		receipt, err = configApprovalSummary(row, request.Service, "UAT", []string{client.ConfigSnapshot().Password, token})
		return err
	})
	if readErr == nil {
		receipt.Review = reviewed.Review
		if receipt.Status == approvalStatuses[1] {
			return receipt, nil
		}
		return receipt, ErrApprovalOutcomeUnknown
	}
	return reviewed, ErrApprovalOutcomeUnknown
}

func approvalReason(row map[string]any) *string {
	reason, ok := row["reason"].(string)
	if !ok {
		return nil
	}
	return &reason
}

func configApprovalSummary(row map[string]any, service, environment string, secrets []string) (ApprovalDetailResult, error) {
	item, err := approvalListItem(row, secrets...)
	if err != nil {
		return ApprovalDetailResult{}, err
	}
	service = strings.TrimSpace(service)
	if service == "" || item.Environment != environment || valueString(row, "approve_type") != "7" ||
		valueString(row, "app_name") != resolveService(service).Name {
		return ApprovalDetailResult{}, errors.New("审批记录与指定环境、服务或配置变更类型不匹配")
	}
	return ApprovalDetailResult{ApprovalItem: item, Details: approvalDetailMetadata(row, secrets)}, nil
}

func (client *genbuClient) configApprovalReview(row map[string]any, service, token string, secrets []string) (ApprovalDetailResult, string, error) {
	result, appID, err := client.configApprovalComparison(row, service, "UAT", token, secrets)
	if err != nil || result.Review == nil || result.Status != approvalStatuses[0] || len(result.Review.Files) == 0 {
		return result, appID, err
	}
	allowed, err := client.canApproveConfig(token)
	if IsAuthenticationError(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ApprovalDetailResult{}, "", err
	}
	result.Review.CanApprove = allowed
	if err != nil {
		result.ReviewError = "无法核对当前账号的审批权限"
	} else if !allowed {
		result.ReviewError = "当前账号没有 UAT 配置审批权限"
	}
	return result, appID, nil
}

func (client *genbuClient) configApprovalComparison(row map[string]any, service, environment, token string, secrets []string) (ApprovalDetailResult, string, error) {
	result, err := configApprovalSummary(row, service, environment, secrets)
	if err != nil {
		return ApprovalDetailResult{}, "", err
	}
	resolved, err := client.resolveServiceContext(strings.TrimSpace(service), token)
	if err != nil {
		return ApprovalDetailResult{}, "", configEditReadError(err, "无法核对审批对应的 Genbu 服务")
	}
	appID := resolved.AppID
	content, _ := row["content"].(map[string]any)
	proposalAppID := valueString(content, "app_id")
	proposalEnv, name, namespace := valueString(content, "env"), valueString(content, "name"), valueString(content, "namespace")
	proposed, err := approvalConfigFiles(content)
	rowAppID := valueString(row, "app_id")
	if err != nil || appID == "" || proposalAppID != appID || proposalEnv != environment ||
		(rowAppID != "" && rowAppID != appID) || !approvalConfigObjectName(name) || !approvalConfigObjectName(namespace) {
		result.ReviewError = "审批记录缺少与指定环境和服务匹配的 ConfigMap 目标或配置文件"
		return result, "", nil
	}
	current, err := client.approvalConfigBaseline(name, namespace, environment, appID, token)
	if err != nil {
		if IsAuthenticationError(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ApprovalDetailResult{}, "", err
		}
		result.ReviewError = "无法读取本审批对应的当前配置"
		return result, "", nil
	}
	original, err := approvalConfigFiles(current)
	if err != nil {
		result.ReviewError = "当前配置的文件数据不完整"
		return result, "", nil
	}
	revision, err := approvalConfigRevision(result.ID, appID, row["reason"], content, current)
	if err != nil {
		return ApprovalDetailResult{}, "", err
	}
	result.Review = &ApprovalConfigReview{
		Namespace: RedactExactSecrets(namespace, secrets...), ConfigMap: RedactExactSecrets(name, secrets...), Revision: revision,
		Files: approvalConfigChanges(original, proposed, secrets),
	}
	return result, appID, nil
}

func approvalConfigObjectName(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '.' {
			return false
		}
	}
	return true
}

func (client *genbuClient) approvalConfigBaseline(name, namespace, environment, appID, token string) (map[string]any, error) {
	query := url.Values{"env": {environment}, "name": {name}, "namespace": {namespace}, "app_id": {appID}}
	response, _, err := client.requestJSON(http.MethodGet, client.genbuURL+approvalConfigDetailPath+"?"+query.Encode(), nil, token, appID)
	if err != nil {
		return nil, configEditReadError(err, "Genbu 无法读取当前配置")
	}
	envelope, _ := response.(map[string]any)
	current, valid := envelope["response"].(map[string]any)
	if !valid {
		return nil, errors.New("Genbu 没有返回当前配置")
	}
	for field, expected := range map[string]string{"name": name, "namespace": namespace, "env": environment, "app_id": appID} {
		if value, present := current[field]; present && (value == nil || valueString(current, field) != expected) {
			return nil, errors.New("Genbu 返回了不匹配的当前配置")
		}
	}
	return current, nil
}

func (client *genbuClient) canApproveConfig(token string) (bool, error) {
	response, _, err := client.requestJSON(http.MethodGet, client.genbuURL+approvalPermissionsPath, nil, token, "")
	if err != nil {
		return false, configEditReadError(err, "Genbu 无法读取用户审批权限")
	}
	envelope, _ := response.(map[string]any)
	permissions, ok := envelope["response"].([]any)
	if !ok {
		return false, errors.New("Genbu 没有返回有效的操作权限")
	}
	for _, entry := range permissions {
		permission, _ := entry.(map[string]any)
		if valueString(permission, "operation_key") == "edit_config_map" && valueString(permission, "env") == "UAT" {
			return true, nil
		}
	}
	return false, nil
}

func approvalConfigFiles(config map[string]any) (map[string]string, error) {
	files, err := configFileRecords(config)
	if err != nil {
		return nil, err
	}
	content := make(map[string]string, len(files))
	for _, record := range files {
		name, nameOK := record["name"].(string)
		body, bodyOK := record["content"].(string)
		_, duplicate := content[name]
		if !nameOK || strings.TrimSpace(name) == "" || !bodyOK || duplicate {
			return nil, errors.New("Genbu ConfigMap 包含无效或重复的文件")
		}
		content[name] = body
	}
	return content, nil
}

func approvalConfigRevision(id int64, appID string, reason any, proposal, current map[string]any) (string, error) {
	data, err := json.Marshal(struct {
		ID       int64
		AppID    string
		Reason   any
		Proposal map[string]any
		Current  map[string]any
	}{id, appID, reason, proposal, current})
	if err != nil {
		return "", errors.New("无法生成审批变更版本")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func approvalConfigChanges(original, proposed map[string]string, secrets []string) []ApprovalConfigFileChange {
	names := make([]string, 0, len(original)+len(proposed))
	for name := range original {
		names = append(names, name)
	}
	for name := range proposed {
		if _, exists := original[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	changes := make([]ApprovalConfigFileChange, 0, len(names))
	for _, name := range names {
		before, beforeExists := original[name]
		after, afterExists := proposed[name]
		if beforeExists == afterExists && before == after {
			continue
		}
		change := ApprovalConfigFileChange{
			Name: redactApprovalText(name, secrets...), BeforeExists: beforeExists, AfterExists: afterExists,
		}
		if beforeExists {
			change.Before = redactApprovalConfigFile(name, before, secrets)
		}
		if afterExists {
			change.After = redactApprovalConfigFile(name, after, secrets)
		}
		changes = append(changes, change)
	}
	return changes
}
