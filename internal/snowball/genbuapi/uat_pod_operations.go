package genbuapi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
)

const (
	uatPodEnvironment = "UAT"
	uatRedeployPath   = "/genbu/release/operate"
)

var (
	ErrUATPodPreviewStale   = errors.New("UAT 操作目标或内容已变化，请重新预检")
	ErrUATPodSubmitUnknown  = errors.New("Genbu UAT 操作结果不明，请核对 Pod 和发布记录")
	ErrUATPodSubmitRejected = errors.New("Genbu 拒绝 UAT 操作")
)

// UATPodOperationRequest selects a service and, for a release, an exact Tag.
// Action is "release" or "redeploy".
type UATPodOperationRequest struct {
	Action  string
	Service string
	Tag     string
}

// UATPodOperationResult records the target and the outcome of a UAT operation.
type UATPodOperationResult struct {
	Action       string `json:"action"`
	Status       string `json:"status"`
	Service      string `json:"service"`
	AppID        string `json:"app_id"`
	GroupID      int    `json:"group_id"`
	Group        string `json:"group"`
	Environment  string `json:"environment"`
	Namespace    string `json:"namespace"`
	Tag          string `json:"tag,omitempty"`
	PodCount     int    `json:"pod_count"`
	PreviewID    string `json:"preview_id,omitempty"`
	EnableStatus string `json:"enable_status,omitempty"`
}

type preparedUATPodOperation struct {
	result      UATPodOperationResult
	method      string
	path        string
	payload     map[string]any
	matchLabels string
	pods        []uatPodIdentity
	version     genbuVersion
}

type uatPodIdentity struct {
	ID          string
	Name        string
	IP          string
	AppVersion  string
	CreatedTime string
}

// uatPodRequestNotSent marks a failure that prevented release or restart dispatch.
type uatPodRequestNotSent struct{ cause error }

func (err *uatPodRequestNotSent) Error() string { return err.cause.Error() }
func (err *uatPodRequestNotSent) Unwrap() error { return err.cause }
func (*uatPodRequestNotSent) RequestNotSent()   {}

// PreviewUATPodOperation validates the target and fingerprints the request.
func (client *Client) PreviewUATPodOperation(ctx context.Context, request UATPodOperationRequest) (UATPodOperationResult, error) {
	return client.runUATPodOperation(ctx, request, "", nil)
}

// SubmitUATPodOperation rechecks the preview, then submits the approved operation.
// beforeWrite may record or reject it before the first mutation.
func (client *Client) SubmitUATPodOperation(ctx context.Context, request UATPodOperationRequest, expect string, beforeWrite func(context.Context, UATPodOperationResult) error) (UATPodOperationResult, error) {
	if len(expect) != hex.EncodedLen(sha256.Size) {
		return UATPodOperationResult{}, ErrUATPodPreviewStale
	}
	if _, err := hex.DecodeString(expect); err != nil {
		return UATPodOperationResult{}, ErrUATPodPreviewStale
	}
	return client.runUATPodOperation(ctx, request, expect, beforeWrite)
}

func (client *Client) runUATPodOperation(ctx context.Context, request UATPodOperationRequest, expect string, beforeWrite func(context.Context, UATPodOperationResult) error) (UATPodOperationResult, error) {
	request.Service = strings.TrimSpace(request.Service)
	request.Tag = strings.TrimSpace(request.Tag)
	if request.Service == "" {
		return UATPodOperationResult{}, fmt.Errorf("请指定服务")
	}
	switch request.Action {
	case "release":
		if request.Tag == "" {
			return UATPodOperationResult{}, fmt.Errorf("请指定 Tag")
		}
	case "redeploy":
		if request.Tag != "" {
			return UATPodOperationResult{}, fmt.Errorf("重启 UAT Pod 无需指定 Tag")
		}
	default:
		return UATPodOperationResult{}, fmt.Errorf("无法识别 UAT 操作")
	}

	var result UATPodOperationResult
	var submitErr error
	err := client.requestToken(ctx, func(worker *genbuClient, token string) error {
		operation, err := worker.prepareUATPodOperation(request, token)
		if err != nil {
			return err
		}
		body, err := json.Marshal(operation.payload)
		if err != nil {
			return fmt.Errorf("无法生成 Genbu UAT 操作请求")
		}
		previewID, err := operation.previewID(worker.genbuURL, body)
		if err != nil {
			return err
		}
		if expect != "" && expect != previewID {
			return ErrUATPodPreviewStale
		}
		result = operation.result
		result.PreviewID = previewID
		result.Status = "preview"
		if expect == "" {
			return nil
		}
		// Only authenticated reads may run again after refreshing a token.
		submitErr = worker.submitUATPodOperation(request, operation, body, token, &result, beforeWrite)
		var notSent RequestNotSentError
		switch {
		case submitErr == nil:
			result.Status = "submitted"
		case errors.Is(submitErr, ErrUATPodSubmitUnknown):
			result.Status = "unknown"
		case errors.Is(submitErr, ErrUATPodSubmitRejected):
			result.Status = "rejected"
		case request.Action == "release" && errors.As(submitErr, &notSent):
			result.Status = "not-sent"
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, submitErr
}

func (operation preparedUATPodOperation) previewID(endpoint string, body []byte) (string, error) {
	target := operation.result
	// Enabling the selected version does not change the approved release target.
	target.EnableStatus = ""
	content, err := json.Marshal(struct {
		Endpoint    string
		SystemCode  string
		Target      UATPodOperationResult
		Method      string
		Path        string
		Payload     json.RawMessage
		MatchLabels string
		Pods        []uatPodIdentity
	}{
		Endpoint: endpoint, SystemCode: systemCode,
		Target: target, Method: operation.method, Path: operation.path, Payload: body,
		MatchLabels: operation.matchLabels, Pods: operation.pods,
	})
	if err != nil {
		return "", fmt.Errorf("无法记录 Genbu UAT 预检")
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func (client *genbuClient) prepareUATPodOperation(request UATPodOperationRequest, token string) (preparedUATPodOperation, error) {
	service, err := client.resolveServiceContext(request.Service, token)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	if strings.TrimSpace(service.AppID) == "" {
		return preparedUATPodOperation{}, fmt.Errorf("Genbu 服务 %s 缺少应用 ID", service.Name)
	}
	if request.Action == "release" {
		return client.prepareUATRelease(service, request.Tag, token)
	}
	return client.prepareUATRedeploy(service, token)
}

func uatPodOperationTarget(action string, service resolvedService, group podGroup) UATPodOperationResult {
	return UATPodOperationResult{
		Action: action, Service: service.Name, AppID: service.AppID,
		Environment: uatPodEnvironment, GroupID: group.ID, Group: group.Name, Namespace: group.Namespace,
	}
}

func (client *genbuClient) prepareUATRedeploy(service resolvedService, token string) (preparedUATPodOperation, error) {
	groups, err := client.queryPodGroups(uatPodEnvironment, token, service.AppID)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	group, err := selectUniqueUATPodGroup(groups)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	if group.MatchLabels == "" {
		return preparedUATPodOperation{}, fmt.Errorf("UAT Pod Group %s 缺少 Pod 匹配标签", group.Name)
	}
	pods, err := client.queryPodsForGroup(group, uatPodEnvironment, token, service.AppID)
	if err != nil {
		return preparedUATPodOperation{}, err
	}
	if len(pods) == 0 {
		return preparedUATPodOperation{}, fmt.Errorf("UAT Pod Group %s 没有 Pod，无法重启", group.Name)
	}
	identities := make([]uatPodIdentity, 0, len(pods))
	for _, pod := range pods {
		if pod.missingPodID {
			return preparedUATPodOperation{}, fmt.Errorf("UAT Pod Group %s 有 Pod 缺少 ID，无法重启", group.Name)
		}
		if pod.scopeMismatch {
			return preparedUATPodOperation{}, fmt.Errorf("Genbu Pod 与指定服务或 UAT 环境不符")
		}
		identities = append(identities, uatPodIdentity{
			ID: pod.operationID, Name: pod.Name, IP: pod.IP, AppVersion: pod.AppVersion, CreatedTime: pod.CreatedTime,
		})
	}
	slices.SortFunc(identities, func(a, b uatPodIdentity) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID), cmp.Compare(a.IP, b.IP),
			cmp.Compare(a.CreatedTime, b.CreatedTime), cmp.Compare(a.AppVersion, b.AppVersion))
	})
	result := uatPodOperationTarget("redeploy", service, group)
	result.PodCount = len(identities)
	return preparedUATPodOperation{
		result: result, method: http.MethodPatch, path: uatRedeployPath, matchLabels: group.MatchLabels, pods: identities,
		payload: map[string]any{
			"group_id": group.ID, "env": uatPodEnvironment, "deleteDeployment": true,
			"business_id": "", "approve_type": "",
		},
	}, nil
}

func uatPodOperationID(record map[string]any) (id string, missing bool) {
	value, present := record["pod_id"]
	if !present {
		// Older responses omit the field. The preview still binds the Pod's identity fields.
		return "", false
	}
	switch value := value.(type) {
	case string:
		id = strings.TrimSpace(value)
	case json.Number:
		id = string(value)
	}
	return id, id == ""
}

func selectUniqueUATPodGroup(groups []podGroup) (podGroup, error) {
	switch len(groups) {
	case 0:
		return podGroup{}, fmt.Errorf("UAT 未找到 Pod Group")
	case 1:
		group := groups[0]
		if group.scopeMismatch {
			return podGroup{}, fmt.Errorf("Genbu Pod Group 与指定服务或 UAT 环境不符")
		}
		if group.ID <= 0 || group.Name == "" || group.Namespace == "" {
			return podGroup{}, fmt.Errorf("UAT Pod Group 缺少有效的 ID、名称或命名空间")
		}
		return group, nil
	default:
		names := make([]string, 0, len(groups))
		for _, group := range groups {
			names = append(names, group.Namespace+"/"+group.Name)
		}
		sort.Strings(names)
		return podGroup{}, fmt.Errorf("UAT 找到多个 Pod Group（%s），无法自动选择", strings.Join(names, "、"))
	}
}

func matchesPodOperationScope(row map[string]any, environment, appID string) bool {
	for field, want := range map[string]string{"env": environment, "target_system": systemCode, "app_id": appID} {
		if actual, present := firstValue(row, field); present && fmt.Sprint(actual) != want {
			return false
		}
	}
	return true
}

func (client *genbuClient) submitUATPodOperation(request UATPodOperationRequest, operation preparedUATPodOperation, body []byte, token string, result *UATPodOperationResult, beforeWrite func(context.Context, UATPodOperationResult) error) error {
	if err := client.ctx.Err(); err != nil {
		return &uatPodRequestNotSent{cause: err}
	}
	if beforeWrite != nil {
		if err := beforeWrite(client.ctx, *result); err != nil {
			return err
		}
	}
	if result.EnableStatus == "required" {
		if err := client.enableUATRelease(request, operation, token, result); err != nil {
			return err
		}
	}
	return client.writeUATMutation(operation.method, operation.path, body, result.AppID, token)
}

func (client *genbuClient) writeUATMutation(method, path string, body []byte, appID, token string) error {
	if err := client.ctx.Err(); err != nil {
		return &uatPodRequestNotSent{cause: err}
	}
	request, err := http.NewRequestWithContext(client.ctx, method, client.genbuURL+path, bytes.NewReader(body))
	if err != nil {
		return &uatPodRequestNotSent{cause: fmt.Errorf("无法创建 Genbu UAT 操作请求")}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "genbu/0.2.2")
	request.Header.Set("X-Token", token)
	request.Header.Set("App-Id", appID)
	request.Header.Set("System-Code", systemCode)
	// Disable body replay and redirects for this mutation.
	request.GetBody = nil
	httpClient := *client.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := httpClient.Do(request)
	if err != nil {
		var rejected RequestNotSentError
		if errors.As(err, &rejected) {
			return rejected
		}
		return ErrUATPodSubmitUnknown
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
		return fmt.Errorf("%w（HTTP %d）", ErrUATPodSubmitRejected, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUATPodSubmitUnknown
	}
	const maxReceiptBytes = 64 << 10
	content, err := io.ReadAll(io.LimitReader(response.Body, maxReceiptBytes+1))
	if err != nil || len(content) > maxReceiptBytes {
		return ErrUATPodSubmitUnknown
	}
	var receipt struct {
		Code json.Number `json:"code"`
	}
	if err := json.Unmarshal(content, &receipt); err != nil {
		return ErrUATPodSubmitUnknown
	}
	code, err := receipt.Code.Int64()
	if err != nil {
		return ErrUATPodSubmitUnknown
	}
	if code != 20000 {
		return fmt.Errorf("%w（业务码 %d）", ErrUATPodSubmitRejected, code)
	}
	return nil
}
