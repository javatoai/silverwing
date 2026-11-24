package genbuapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	uatReleaseGroupsPath = "/genbu/release/selectVersion"
	uatDeployInfoPath    = "/genbu/release/DeployInfo"
	uatReleasePath       = "/genbu/release/service"
	uatReleaseEnablePath = baseInfoPath
)

func (client *genbuClient) prepareUATRelease(service resolvedService, tag, token string) (preparedUATPodOperation, error) {
	version, err := client.findVersion(service.Name, tag, token, service.AppID)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	enabled, err := client.readUATReleaseEnabled(version, service.AppID, token, false)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	operation, err := client.prepareUATReleaseTarget(service, version, token)
	if err == nil && !enabled {
		operation.result.EnableStatus = "required"
	}
	return operation, err
}

func (client *genbuClient) prepareUATReleaseTarget(service resolvedService, version genbuVersion, token string) (preparedUATPodOperation, error) {
	group, err := client.queryUATReleaseGroup(service.AppID, token)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	payload, err := client.uatReleasePayload(group, version.VersionID, service.AppID, token)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	result := uatPodOperationTarget("release", service, group)
	result.Tag = version.VersionName
	result.PodCount, _ = valueInt(payload, "pod_num") // The release settings have been validated.
	return preparedUATPodOperation{result: result, method: http.MethodPost, path: uatReleasePath, payload: payload, version: version}, nil
}

func (client *genbuClient) readUATReleaseEnabled(version genbuVersion, appID, token string, requireName bool) (bool, error) {
	steps, err := client.queryBaseInfo(version.VersionID, token, appID)
	if err != nil {
		return false, err
	}
	var buildErr error
	if latestStep(steps, buildStepNumber).status() != pipelineStatusSuccess {
		buildErr = fmt.Errorf("Tag %s 尚未构建成功，无法发版", version.VersionName)
		if !requireName {
			return false, buildErr
		}
	}
	step := latestStep(steps, uatStepNumber)
	if step == nil || (step.StepName != "" && step.StepName != uatPodEnvironment) || (requireName && step.StepName != uatPodEnvironment) {
		return false, fmt.Errorf("无法确认 Tag %s 的 UAT 发布步骤", version.VersionName)
	}
	flag := fmt.Sprint(step.Enabled)
	if flag != "1" && (flag != "0" || step.StepName != uatPodEnvironment) {
		return false, fmt.Errorf("无法确认 Tag %s 的 UAT 发布开关状态", version.VersionName)
	}
	return flag == "1", buildErr
}

func (client *genbuClient) enableUATRelease(request UATPodOperationRequest, operation preparedUATPodOperation, token string, result *UATPodOperationResult) error {
	body, err := json.Marshal(struct {
		VersionID int    `json:"version_id"`
		Env       string `json:"env"`
		Enable    int    `json:"enable"`
	}{VersionID: operation.version.VersionID, Env: uatPodEnvironment, Enable: 1})
	if err != nil {
		return &uatPodRequestNotSent{cause: fmt.Errorf("无法生成开启 UAT 发布的请求")}
	}
	switchErr := client.writeUATMutation(http.MethodPost, uatReleaseEnablePath, body, result.AppID, token)
	if switchErr != nil && !errors.Is(switchErr, ErrUATPodSubmitUnknown) {
		if errors.Is(switchErr, ErrUATPodSubmitRejected) {
			result.EnableStatus = "rejected"
			return &uatPodRequestNotSent{cause: fmt.Errorf("开启 Tag %s 的 UAT 发布失败，未提交发版：%v", result.Tag, switchErr)}
		}
		return &uatPodRequestNotSent{cause: fmt.Errorf("开启 Tag %s 的 UAT 发布失败，未提交发版：%w", result.Tag, switchErr)}
	}

	// Verify the switch on the exact version even if its write receipt was ambiguous.
	enabled, verifyErr := client.readUATReleaseEnabled(operation.version, result.AppID, token, true)
	if !enabled {
		if switchErr == nil && verifyErr == nil {
			result.EnableStatus = "rejected"
			return &uatPodRequestNotSent{cause: fmt.Errorf("开启请求返回成功，但 Tag %s 的 UAT 发布开关仍关闭，未提交发版", result.Tag)}
		}
		result.EnableStatus = "unknown"
		if err := client.ctx.Err(); err != nil {
			return &uatPodRequestNotSent{cause: fmt.Errorf("无法确认 Tag %s 的 UAT 发布开关已开启，未提交发版：%w", result.Tag, err)}
		}
		return &uatPodRequestNotSent{cause: fmt.Errorf("无法确认 Tag %s 的 UAT 发布开关已开启，未提交发版", result.Tag)}
	}
	result.EnableStatus = "enabled"
	if verifyErr != nil {
		return &uatPodRequestNotSent{cause: verifyErr}
	}
	if err := client.recheckEnabledUATRelease(request, *result, operation.version.VersionID, token); err != nil {
		return &uatPodRequestNotSent{cause: err}
	}
	return nil
}

func (client *genbuClient) recheckEnabledUATRelease(request UATPodOperationRequest, result UATPodOperationResult, versionID int, token string) error {
	service, err := client.resolveServiceContext(request.Service, token)
	if err != nil {
		return fmt.Errorf("无法重新核对 Genbu 服务")
	}
	if service.AppID != result.AppID || service.Name != result.Service {
		return ErrUATPodPreviewStale
	}
	version, err := client.findVersion(service.Name, request.Tag, token, service.AppID)
	if err != nil {
		return fmt.Errorf("无法重新核对 Genbu Tag %s", result.Tag)
	}
	if version.VersionID != versionID {
		return ErrUATPodPreviewStale
	}
	current, err := client.prepareUATReleaseTarget(service, version, token)
	if err != nil {
		return fmt.Errorf("无法重新核对 Genbu UAT 发布目标和配置")
	}
	body, err := json.Marshal(current.payload)
	if err != nil {
		return fmt.Errorf("无法重新核对 Genbu UAT 发布参数")
	}
	previewID, err := current.previewID(client.genbuURL, body)
	if err != nil {
		return err
	}
	if previewID != result.PreviewID {
		return ErrUATPodPreviewStale
	}
	return nil
}

func (client *genbuClient) queryUATReleaseGroup(appID, token string) (podGroup, error) {
	query := url.Values{"env": {uatPodEnvironment}}
	response, _, err := client.requestJSON(http.MethodGet, client.genbuURL+uatReleaseGroupsPath+"?"+query.Encode(), nil, token, appID)
	if err != nil {
		return podGroup{}, err
	}
	envelope, ok := response.(map[string]any)
	if !ok {
		return podGroup{}, fmt.Errorf("Genbu UAT 发布 Group 响应格式错误")
	}
	rows, ok := envelope["response"].([]any)
	if !ok && envelope["response"] != nil {
		return podGroup{}, fmt.Errorf("Genbu UAT 发布 Group 列表格式错误")
	}
	groups := make([]podGroup, 0, len(rows))
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok || !matchesPodOperationScope(row, uatPodEnvironment, appID) {
			return podGroup{}, fmt.Errorf("Genbu 发布 Group 与指定服务或 UAT 环境不符")
		}
		id, _ := valueInt(row, "group_id")
		groups = append(groups, podGroup{
			ID:        id,
			Name:      strings.TrimSpace(valueString(row, "pod_group_name")),
			Namespace: strings.TrimSpace(valueString(row, "k8s_namespace")),
		})
	}
	return selectUniqueUATPodGroup(groups)
}

func (client *genbuClient) uatReleasePayload(group podGroup, versionID int, appID, token string) (map[string]any, error) {
	query := url.Values{"group_id": {strconv.Itoa(group.ID)}}
	response, _, err := client.requestJSON(http.MethodGet, client.genbuURL+uatDeployInfoPath+"?"+query.Encode(), nil, token, appID)
	if err != nil {
		if IsAuthenticationError(err) {
			return nil, &authenticationError{message: "Genbu 认证失败"}
		}
		var rejected RequestNotSentError
		if errors.As(err, &rejected) {
			return nil, rejected
		}
		if client.ctx.Err() != nil {
			return nil, client.ctx.Err()
		}
		return nil, fmt.Errorf("无法读取 Genbu UAT Group 的发布配置")
	}
	envelope, _ := response.(map[string]any)
	settings, ok := envelope["response"].(map[string]any)
	if !ok || valueString(settings, "k8s_namespace") != group.Namespace || !matchesPodOperationScope(settings, uatPodEnvironment, appID) {
		return nil, fmt.Errorf("Genbu 发布配置与选定的 UAT Group 不符")
	}
	if id, present := firstValue(settings, "group_id"); present && fmt.Sprint(id) != strconv.Itoa(group.ID) {
		return nil, fmt.Errorf("Genbu 发布配置与选定的 UAT Group 不符")
	}

	// The version-management form loads Group settings over these defaults.
	payload := map[string]any{
		"termination_grace_period_seconds": 30, "launch_timeout": 0, "manual_rollout_flag": 0,
		"slow_launch_flag": 0, "single_batch_pullout": 25, "wait_batch_interval": 0,
		"ready_check": true, "recycle_resource": false, "is_sidecar": false, "auto_rollback": true,
		"rollback_form": map[string]any{"versionId": nil, "logId": nil},
		"port":          nil, "config_version": "", "mount_filebeat": true, "filebeat_config": "",
	}
	for key, value := range settings {
		payload[key] = value
	}
	if err := validateUATReleaseSettings(payload); err != nil {
		return nil, err
	}
	payload["group_id"] = []int{group.ID}
	payload["env"] = uatPodEnvironment
	payload["target_system"] = systemCode
	payload["version_id"] = versionID
	payload["approve_type"] = nil
	delete(payload, "business_id")
	delete(payload, "group_name")
	delete(payload, "new_group_name")
	return payload, nil
}

func validateUATReleaseSettings(payload map[string]any) error {
	if count, err := valueInt(payload, "pod_num"); err != nil || count <= 0 {
		return fmt.Errorf("UAT Group 缺少有效的 Pod 数")
	}
	for _, field := range []string{"live_check_form", "ready_check_form", "startup_check_form"} {
		if probe, ok := payload[field].(map[string]any); !ok || len(probe) == 0 {
			return fmt.Errorf("UAT Group 缺少发布所需的健康检查配置（%s）", field)
		}
	}
	if enabled, _ := payload["mount_filebeat"].(bool); enabled && valueString(payload, "filebeat_config") == "" {
		return fmt.Errorf("UAT Group 已开启 Filebeat，但缺少对应配置")
	}
	if enabled, _ := payload["auto_rollback"].(bool); enabled {
		if id, err := valueInt(payload, "rollback_log_id"); err != nil || id <= 0 {
			return fmt.Errorf("UAT Group 已开启自动回滚，但缺少回滚发布记录")
		}
	}
	return nil
}
