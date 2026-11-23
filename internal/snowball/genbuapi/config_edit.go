package genbuapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	ErrConfigPreviewStale   = errors.New("配置或编辑内容在预览后发生变化，请重新预览")
	ErrConfigTarget         = errors.New("无法定位 Genbu 配置目标")
	ErrConfigPropertyEdit   = errors.New("无法应用所选配置键")
	ErrConfigSubmitUnknown  = errors.New("配置审批提交结果不明，请查询审批记录")
	ErrConfigSubmitRejected = errors.New("Genbu 拒绝提交配置审批")
)

// PropertyEdit specifies a set, add, or unset operation on a Properties key.
type PropertyEdit struct {
	Operation string
	Key       string
	Value     string
}

// ConfigEditRequest identifies a file in the application's latest ConfigMap.
// The namespace is resolved from the application and environment.
// A non-nil Replacement supplies the complete new body, including an empty body.
type ConfigEditRequest struct {
	Environment string
	Service     string
	File        string
	Edits       []PropertyEdit
	Replacement *string
}

// ConfigEditChange identifies a changed key without returning its value.
type ConfigEditChange struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
}

// ConfigEditResult describes a preview or an approval submission.
type ConfigEditResult struct {
	Service     string             `json:"service"`
	Environment string             `json:"environment"`
	Namespace   string             `json:"namespace"`
	Name        string             `json:"name"`
	File        string             `json:"file"`
	UpdateTime  string             `json:"update_time"`
	SnapshotID  string             `json:"snapshot_id"`
	Status      string             `json:"status"`
	PreviewID   string             `json:"preview_id,omitempty"`
	ChangeID    string             `json:"-"`
	Changes     []ConfigEditChange `json:"changes,omitempty"`
	BeforeBytes *int               `json:"before_bytes,omitempty"`
	AfterBytes  *int               `json:"after_bytes,omitempty"`
	ApprovalID  int64              `json:"approve_id,omitempty"`
}

// PreviewConfigEdit reads the latest ConfigMap and describes the proposed edit.
func (client *Client) PreviewConfigEdit(ctx context.Context, request ConfigEditRequest) (ConfigEditResult, error) {
	return client.editConfig(ctx, request, "")
}

// SubmitConfigEdit rechecks the preview against the current ConfigMap, then
// sends one approval request when the preview still matches.
func (client *Client) SubmitConfigEdit(ctx context.Context, request ConfigEditRequest, expect string) (ConfigEditResult, error) {
	if len(expect) != hex.EncodedLen(sha256.Size) {
		return ConfigEditResult{}, ErrConfigPreviewStale
	}
	if _, err := hex.DecodeString(expect); err != nil {
		return ConfigEditResult{}, ErrConfigPreviewStale
	}
	return client.editConfig(ctx, request, expect)
}

func (client *Client) editConfig(ctx context.Context, request ConfigEditRequest, expect string) (ConfigEditResult, error) {
	environment, err := normalizeEnvironmentName(request.Environment)
	if err != nil {
		return ConfigEditResult{}, err
	}
	request.Environment = environment
	request.Service = strings.TrimSpace(request.Service)
	if request.Service == "" || strings.TrimSpace(request.File) == "" {
		return ConfigEditResult{}, fmt.Errorf("必须指定服务和目标文件名")
	}
	if (request.Replacement == nil) == (len(request.Edits) == 0) {
		return ConfigEditResult{}, fmt.Errorf("请选择按键编辑或完整文件替换")
	}
	if request.Replacement != nil {
		if !utf8.ValidString(*request.Replacement) {
			return ConfigEditResult{}, fmt.Errorf("替换文件必须是有效的 UTF-8 文本")
		}
	} else if !strings.EqualFold(filepath.Ext(request.File), ".properties") {
		return ConfigEditResult{}, fmt.Errorf("按键编辑仅支持 .properties 文件")
	}

	var result ConfigEditResult
	err = client.requestToken(ctx, func(worker *genbuClient, token string) error {
		var err error
		result, err = worker.editConfig(request, expect, token)
		return err
	})
	return result, err
}

func (client *genbuClient) editConfig(request ConfigEditRequest, expect, token string) (ConfigEditResult, error) {
	target, err := client.resolveLatestConfigTarget(request.Service, request.Environment, token)
	if err != nil {
		return ConfigEditResult{}, err
	}
	request.Service = target.service.Name
	previewID, err := configEditPreviewID(request, target.namespace, target.service.AppID, target.row)
	if err != nil {
		return ConfigEditResult{}, err
	}
	if expect != "" && expect != previewID {
		return ConfigEditResult{}, ErrConfigPreviewStale
	}
	file, err := configEditFile(target.row, request.File)
	if err != nil {
		return ConfigEditResult{}, err
	}
	before, ok := file["content"].(string)
	if !ok {
		return ConfigEditResult{}, fmt.Errorf("目标文件缺少文本正文：%w", ErrConfigTarget)
	}
	snapshotID, err := target.snapshotID()
	if err != nil {
		return ConfigEditResult{}, err
	}
	result := ConfigEditResult{
		Service: request.Service, Environment: request.Environment,
		Namespace: target.namespace, Name: valueString(target.row, "name"), File: request.File,
		UpdateTime: target.updateTime, SnapshotID: snapshotID,
	}
	var after string
	if request.Replacement != nil {
		after = *request.Replacement
		beforeBytes, afterBytes := len(before), len(after)
		result.BeforeBytes, result.AfterBytes = &beforeBytes, &afterBytes
	} else {
		after, result.Changes, err = editConfigProperties(before, request.Edits)
		if err != nil {
			return ConfigEditResult{}, fmt.Errorf("%w：%w", ErrConfigPropertyEdit, err)
		}
	}
	if after == before {
		result.Status = "no-change"
		return result, nil
	}

	result.PreviewID = previewID
	result.ChangeID = configEditChangeID(result, before, after)
	if expect == "" {
		result.Status = "preview"
		return result, nil
	}
	file["content"] = after
	result.ApprovalID, err = client.submitConfigEdit(target.row, token, target.service.AppID)
	if err != nil {
		return ConfigEditResult{}, err
	}
	result.Status = "submitted"
	return result, nil
}

func (client *genbuClient) readConfigEditRow(environment, namespace, token, appID string) (map[string]any, string, error) {
	query := url.Values{
		"is_all": {"true"}, "env": {environment}, "namespace": {namespace},
		"keyword": {""}, "current": {"1"}, "size": {"10"},
	}
	response, _, err := client.requestJSON(
		http.MethodGet, client.genbuURL+configMapListPath+"?"+query.Encode(), nil, token, appID,
	)
	if err != nil {
		return nil, "", configEditReadError(err, "无法读取 Genbu ConfigMap 列表")
	}
	latest, updateTime, err := selectLatestConfigMap(response)
	if err != nil {
		return nil, "", fmt.Errorf("%s：%w", err, ErrConfigTarget)
	}
	if latest == nil {
		return nil, "", fmt.Errorf("未找到可编辑的 ConfigMap：%w", ErrConfigTarget)
	}
	name, _ := latest["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, "", fmt.Errorf("最新 ConfigMap 记录缺少有效名称：%w", ErrConfigTarget)
	}
	env, validEnv := latest["env"].(string)
	rowNamespace, validNamespace := latest["namespace"].(string)
	if !validEnv || !validNamespace {
		return nil, "", fmt.Errorf("最新 ConfigMap 记录缺少有效的环境或命名空间：%w", ErrConfigTarget)
	}
	actualAppID, hasAppID := latest["app_id"]
	if env != environment || rowNamespace != namespace ||
		(hasAppID && fmt.Sprint(actualAppID) != appID) {
		return nil, "", fmt.Errorf("最新 ConfigMap 记录的环境、命名空间或应用与查询条件不符：%w", ErrConfigTarget)
	}
	return latest, updateTime, nil
}

func configEditReadError(err error, message string) error {
	var rejected RequestNotSentError
	if errors.As(err, &rejected) {
		return rejected
	}
	if IsAuthenticationError(err) {
		return &authenticationError{message: "Genbu 认证失败"}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%s：%w", message, ErrConfigTarget)
}

func configEditFile(row map[string]any, name string) (map[string]any, error) {
	files, err := configFileRecords(row)
	if err != nil {
		return nil, err
	}
	var file map[string]any
	for _, value := range files {
		fileName, _ := value["name"].(string)
		if fileName != name {
			continue
		}
		if file != nil {
			return nil, fmt.Errorf("ConfigMap 内存在多个同名文件，无法唯一定位：%w", ErrConfigTarget)
		}
		file = value
	}
	if file == nil {
		return nil, fmt.Errorf("最新 ConfigMap 中未找到指定的配置文件：%w", ErrConfigTarget)
	}
	return file, nil
}

func configFileRecords(row map[string]any) ([]map[string]any, error) {
	entries, ok := row["encoded_data"].([]any)
	if !ok {
		return nil, fmt.Errorf("Genbu ConfigMap 缺少配置文件数组：%w", ErrConfigTarget)
	}
	files := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		file, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Genbu ConfigMap 配置文件记录格式错误：%w", ErrConfigTarget)
		}
		files = append(files, file)
	}
	return files, nil
}

func configEditPreviewID(request ConfigEditRequest, namespace, appID string, row map[string]any) (string, error) {
	// Bind the approval to the same canonical request and complete snapshot.
	payload, err := json.Marshal(struct {
		Request   ConfigEditRequest
		Namespace string
		AppID     string
		Row       map[string]any
	}{request, namespace, appID, row})
	if err != nil {
		return "", fmt.Errorf("无法生成配置预览 ID")
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// Unlike the preview, the change is independent of unrelated ConfigMap metadata
// and whether the caller supplied property operations or the full file.
func configEditChangeID(result ConfigEditResult, before, after string) string {
	hash := sha256.New()
	for _, field := range [...]string{result.Service, result.Environment, result.Namespace, result.Name, result.File, before, after} {
		_, _ = fmt.Fprintf(hash, "%d:", len(field))
		_, _ = io.WriteString(hash, field)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (client *genbuClient) submitConfigEdit(row map[string]any, token, appID string) (int64, error) {
	payload, err := json.Marshal(row)
	if err != nil {
		return 0, fmt.Errorf("无法生成配置审批请求")
	}
	request, err := http.NewRequestWithContext(client.ctx, http.MethodPost, client.genbuURL+configMapListPath, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("无法创建配置审批请求")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "genbu/0.2.2")
	request.Header.Set("X-Token", token)
	request.Header.Set("App-Id", appID)
	request.Header.Set("System-Code", systemCode)
	// A replayable body can be retried by net/http on a reused connection.
	request.GetBody = nil
	httpClient := *client.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	if err := client.ctx.Err(); err != nil {
		return 0, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		var rejected RequestNotSentError
		if errors.As(err, &rejected) {
			return 0, rejected
		}
		return 0, ErrConfigSubmitUnknown
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
		// Keep this error separate from authentication errors, which can cause retries.
		return 0, fmt.Errorf("%w（HTTP %d）", ErrConfigSubmitRejected, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, ErrConfigSubmitUnknown
	}
	const maxApprovalResponseBytes = 64 << 10
	content, err := io.ReadAll(io.LimitReader(response.Body, maxApprovalResponseBytes+1))
	if err != nil || len(content) > maxApprovalResponseBytes {
		return 0, ErrConfigSubmitUnknown
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(content, &envelope); err != nil {
		return 0, ErrConfigSubmitUnknown
	}
	code, err := configEditResponseNumber(envelope["code"])
	if err != nil {
		return 0, ErrConfigSubmitUnknown
	}
	if code != 20000 {
		return 0, fmt.Errorf("Genbu 提交配置审批失败（业务码 %d）：%w", code, ErrConfigSubmitUnknown)
	}
	if id, err := configEditResponseNumber(envelope["approve_id"]); err == nil && id > 0 {
		return id, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope["response"], &data); err == nil {
		if id, err := configEditResponseNumber(data["approve_id"]); err == nil && id > 0 {
			return id, nil
		}
	}
	return 0, nil
}

func configEditResponseNumber(raw json.RawMessage) (int64, error) {
	var quoted string
	if err := json.Unmarshal(raw, &quoted); err == nil {
		return strconv.ParseInt(quoted, 10, 64)
	}
	return strconv.ParseInt(string(raw), 10, 64)
}
