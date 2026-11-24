package genbuapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	beastBaseURL         = "https://beast.snowballtech.com"
	genbuBaseURL         = "https://genbu.snowballtech.com"
	systemCode           = "ANDCN"
	loginPath            = "/beast/api/login"
	dragonUserPath       = "/genbu/admin/api/dragonUser"
	applicationsPath     = "/genbu/basic/api/applications"
	versionPath          = "/genbu/version/query"
	baseInfoPath         = "/genbu/version/assign/baseInfo"
	buildLogPath         = "/genbu/version/build/log"
	appEnvNamespacePath  = "/genbu/basic/api/appEnvNamespace"
	configMapListPath    = "/genbu/basic/api/listNamespacedConfigMap"
	podGroupPath         = "/genbu/basic/api/podGroup"
	podPath              = "/genbu/basic/api/pod"
	genbuTimeLayout      = "2006-01-02 15:04:05"
	requestTimeout       = 15 * time.Second
	responseErrorLimit   = 500
	maxResponseBytes     = 32 << 20
	buildStepNumber      = 1
	uatStepNumber        = 7
	productionStepNumber = 9
)

var (
	jwtPattern    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	secretPattern = regexp.MustCompile(`(?i)("?(?:password|x-token|token|access_token)"?\s*[:=]\s*")[^"\s]+`)

	ErrServiceNotFound = errors.New("Genbu 服务未找到")
	ErrTagNotFound     = errors.New("Genbu Tag 未找到")
)

type authenticationError struct{ message string }

// AuthenticationError identifies a Genbu credential or token rejection.
type AuthenticationError = authenticationError

func (err *authenticationError) Error() string { return err.message }

func isAuthenticationError(err error) bool {
	var target *authenticationError
	return errors.As(err, &target)
}

// IsAuthenticationError reports whether an error should trigger token refresh.
func IsAuthenticationError(err error) bool { return isAuthenticationError(err) }

// RequestNotSentError marks a transport preflight rejection before dispatch.
type RequestNotSentError interface {
	error
	RequestNotSent()
}

type genbuAPI interface {
	login(username, password string) (string, error)
	queryStatus(service, tag, token string) (genbuStatus, error)
	buildLog(service, tag string, tailLines int, token string) (genbuBuildLog, error)
	queryConfig(service, namespace, environment, file, token string) (configSnapshot, error)
	queryPods(service, environment, token string) (podSnapshot, error)
}

type genbuClient struct {
	httpClient *http.Client
	beastURL   string
	genbuURL   string
	ctx        context.Context
}

func newGenbuClient() *genbuClient {
	return &genbuClient{
		httpClient: &http.Client{Timeout: requestTimeout},
		beastURL:   beastBaseURL,
		genbuURL:   genbuBaseURL,
	}
}

func (client *genbuClient) login(username, password string) (string, error) {
	response, headers, err := client.requestJSON(
		http.MethodPost,
		client.beastURL+loginPath,
		map[string]any{"username": username, "password": password},
		"",
		"",
	)
	if err != nil {
		var rejected RequestNotSentError
		if errors.As(err, &rejected) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		message := redactValues(err.Error(), password)
		if isAuthenticationError(err) {
			return "", &authenticationError{message: message}
		}
		return "", fmt.Errorf("%s", message)
	}
	beastToken := extractToken(response)
	if beastToken == "" {
		beastToken = headers.Get("X-Token")
	}
	if beastToken == "" {
		return "", fmt.Errorf("登录成功响应中未找到 token；请确认登录接口响应格式")
	}
	return client.exchangeGenbuToken(beastToken)
}

func (client *genbuClient) exchangeGenbuToken(beastToken string) (string, error) {
	query := url.Values{"token": []string{beastToken}}
	response, _, err := client.requestJSON(
		http.MethodGet,
		client.genbuURL+dragonUserPath+"?"+query.Encode(),
		nil,
		"",
		"",
	)
	if err != nil {
		var rejected RequestNotSentError
		if errors.As(err, &rejected) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		message := redactValues(err.Error(), beastToken, url.QueryEscape(beastToken))
		if isAuthenticationError(err) {
			return "", &authenticationError{message: message}
		}
		return "", fmt.Errorf("Genbu token 兑换失败：%s", message)
	}
	genbuToken := extractToken(response)
	if genbuToken == "" {
		return "", fmt.Errorf("Genbu token 兑换响应中未找到 token")
	}
	return genbuToken, nil
}

func (client *genbuClient) queryStatus(service, tag, token string) (genbuStatus, error) {
	resolved, err := client.resolveServiceContext(service, token)
	if err != nil {
		return genbuStatus{}, err
	}
	version, err := client.findVersion(resolved.Name, tag, token, resolved.AppID)
	if err != nil {
		return genbuStatus{}, err
	}
	steps, err := client.queryBaseInfo(version.VersionID, token, resolved.AppID)
	if err != nil {
		return genbuStatus{}, err
	}
	buildStep := latestStep(steps, buildStepNumber)
	uatStep := latestStep(steps, uatStepNumber)
	productionStep := latestStep(steps, productionStepNumber)
	return genbuStatus{
		Service:          resolved.Name,
		AppID:            resolved.AppID,
		Tag:              tag,
		VersionID:        version.VersionID,
		VersionName:      version.VersionName,
		BuildStatus:      buildStep.status(),
		UATStatus:        uatStep.status(),
		ProductionStatus: productionStep.status(),
		BuildStep:        buildStep,
		UATStep:          uatStep,
		ProductionStep:   productionStep,
	}, nil
}

func (client *genbuClient) buildLog(service, tag string, tailLines int, token string) (genbuBuildLog, error) {
	resolved, err := client.resolveServiceContext(service, token)
	if err != nil {
		return genbuBuildLog{}, err
	}
	version, err := client.findVersion(resolved.Name, tag, token, resolved.AppID)
	if err != nil {
		return genbuBuildLog{}, err
	}
	content, err := client.queryBuildLog(version.VersionID, tailLines, token, resolved.AppID)
	if err != nil {
		return genbuBuildLog{}, err
	}
	return genbuBuildLog{Service: resolved.Name, Tag: version.VersionName, Content: content}, nil
}

func (client *genbuClient) queryConfig(service, namespace, environment, file, token string) (configSnapshot, error) {
	resolved, err := client.resolveServiceContext(service, token)
	if err != nil {
		return configSnapshot{}, err
	}
	selectedNamespace, err := client.resolveConfigNamespace(resolved, namespace, environment, token)
	if err != nil {
		return configSnapshot{}, err
	}

	query := url.Values{
		"is_all":    []string{"true"},
		"env":       []string{environment},
		"namespace": []string{selectedNamespace},
		"keyword":   []string{""},
		"current":   []string{"1"},
		"size":      []string{"10"},
	}
	response, _, err := client.requestJSON(
		http.MethodGet,
		client.genbuURL+configMapListPath+"?"+query.Encode(),
		nil,
		token,
		resolved.AppID,
	)
	if err != nil {
		return configSnapshot{}, err
	}
	latest, updateTime, err := selectLatestConfigMap(response)
	if err != nil {
		return configSnapshot{}, err
	}
	for key, expected := range map[string]string{"env": environment, "namespace": selectedNamespace, "app_id": resolved.AppID} {
		if value, present := latest[key]; present && (value == nil || fmt.Sprint(value) != expected) {
			return configSnapshot{}, fmt.Errorf("最新 ConfigMap 与查询环境、命名空间或应用不符：%w", ErrConfigTarget)
		}
	}
	files, err := readConfigBodies(latest, file)
	if err != nil {
		return configSnapshot{}, err
	}
	return configSnapshot{
		Service:     resolved.Name,
		Environment: environment,
		Namespace:   selectedNamespace,
		UpdateTime:  updateTime,
		Files:       files,
	}, nil
}

func (client *genbuClient) queryPods(service, environment, token string) (podSnapshot, error) {
	resolved, err := client.resolveServiceContext(service, token)
	if err != nil {
		return podSnapshot{}, err
	}
	groups, err := client.queryPodGroups(environment, token, resolved.AppID)
	if err != nil {
		return podSnapshot{}, err
	}

	pods := make([]genbuPod, 0)
	for _, group := range groups {
		if group.scopeMismatch {
			return podSnapshot{}, fmt.Errorf("Genbu 返回了与查询环境或服务不符的 Pod Group")
		}
		if group.MatchLabels == "" || group.Namespace == "" {
			continue
		}
		items, err := client.queryPodsForGroup(group, environment, token, resolved.AppID)
		if err != nil {
			return podSnapshot{}, err
		}
		for _, pod := range items {
			if pod.scopeMismatch {
				return podSnapshot{}, fmt.Errorf("Genbu 返回了与查询环境或服务不符的 Pod")
			}
		}
		pods = append(pods, items...)
	}
	return podSnapshot{Service: resolved.Name, Environment: environment, Pods: pods}, nil
}

type podGroup struct {
	ID            int
	Name          string
	Namespace     string
	MatchLabels   string
	scopeMismatch bool
}

func (client *genbuClient) queryPodGroups(environment, token, appID string) ([]podGroup, error) {
	query := url.Values{"env": []string{environment}}
	response, _, err := client.requestJSON(http.MethodGet, client.genbuURL+podGroupPath+"?"+query.Encode(), nil, token, appID)
	if err != nil {
		return nil, err
	}
	record, ok := response.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Pod Group 响应格式错误")
	}
	body, ok := record["response"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Pod Group 响应格式错误：response 不是对象")
	}
	rawGroups, ok := body["groups"].([]any)
	if !ok {
		return nil, fmt.Errorf("Pod Group 响应格式错误：groups 不是数组")
	}
	groups := make([]podGroup, 0, len(rawGroups))
	for index, raw := range rawGroups {
		record, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Pod Group 响应格式错误：第 %d 项不是对象", index+1)
		}
		id, err := valueInt(record, "group_id", "groupId", "id")
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("Pod Group 响应格式错误：第 %d 项 group_id 无效", index+1)
		}
		groups = append(groups, podGroup{
			ID:            id,
			Name:          strings.TrimSpace(valueString(record, "pod_group_name", "group_name", "name")),
			Namespace:     strings.TrimSpace(valueString(record, "k8s_namespace", "namespace")),
			MatchLabels:   strings.TrimSpace(valueString(record, "match_labels", "matchLabels")),
			scopeMismatch: !matchesPodOperationScope(record, environment, appID),
		})
	}
	return groups, nil
}

func (client *genbuClient) queryPodsForGroup(group podGroup, environment, token, appID string) ([]genbuPod, error) {
	query := url.Values{
		"group_id":     []string{strconv.Itoa(group.ID)},
		"namespace":    []string{group.Namespace},
		"match_labels": []string{group.MatchLabels},
		"env":          []string{environment},
	}
	response, _, err := client.requestJSON(http.MethodGet, client.genbuURL+podPath+"?"+query.Encode(), nil, token, appID)
	if err != nil {
		return nil, err
	}
	record, ok := response.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Pod 响应格式错误")
	}
	rawPods, ok := record["response"].([]any)
	if !ok {
		return nil, fmt.Errorf("Pod 响应格式错误：response 不是数组")
	}
	pods := make([]genbuPod, 0, len(rawPods))
	for index, raw := range rawPods {
		record, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Pod 响应格式错误：第 %d 项不是对象", index+1)
		}
		name := strings.TrimSpace(valueString(record, "pod_name", "podName", "name"))
		if name == "" {
			return nil, fmt.Errorf("Pod 响应格式错误：第 %d 项缺少 pod_name", index+1)
		}
		restartCount, err := valueInt(record, "restart_count", "restartCount")
		if err != nil || restartCount < 0 {
			return nil, fmt.Errorf("Pod 响应格式错误：第 %d 项 restart_count 无效", index+1)
		}
		reason := strings.TrimSpace(valueString(record, "reason"))
		if waitReason, ok := record["wait_reason"].(map[string]any); ok {
			if candidate := strings.TrimSpace(valueString(waitReason, "reason", "message")); candidate != "" {
				reason = candidate
			}
		}
		ready := false
		if value, ok := record["ready"].(bool); ok {
			ready = value
		}
		operationID, missingPodID := uatPodOperationID(record)
		pods = append(pods, genbuPod{
			GroupName:     group.Name,
			Namespace:     group.Namespace,
			Name:          name,
			IP:            strings.TrimSpace(valueString(record, "pod_ip", "podIP", "ip")),
			AppVersion:    strings.TrimSpace(valueString(record, "app_version", "appVersion", "version")),
			RestartCount:  restartCount,
			Phase:         strings.TrimSpace(valueString(record, "phase", "status")),
			Ready:         ready,
			Reason:        reason,
			CreatedTime:   strings.TrimSpace(valueString(record, "created_time", "createdTime")),
			operationID:   operationID,
			missingPodID:  missingPodID,
			scopeMismatch: !matchesPodOperationScope(record, environment, appID),
		})
	}
	return pods, nil
}

func (client *genbuClient) resolveConfigNamespace(service resolvedService, namespace, environment, token string) (string, error) {
	if selected := strings.TrimSpace(namespace); selected != "" {
		return selected, nil
	}
	namespaces, err := client.queryNamespaces(environment, token, service.AppID)
	if err != nil {
		return "", err
	}
	return chooseNamespace(service.Name, environment, namespaces)
}

func (client *genbuClient) queryNamespaces(environment, token, appID string) ([]string, error) {
	query := url.Values{"env": []string{environment}}
	response, _, err := client.requestJSON(
		http.MethodGet,
		client.genbuURL+appEnvNamespacePath+"?"+query.Encode(),
		nil,
		token,
		appID,
	)
	if err != nil {
		return nil, err
	}
	record, ok := response.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s namespace 响应格式错误", environment)
	}
	raw, ok := record["response"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s namespace 响应格式错误：response 不是数组", environment)
	}
	seen := make(map[string]struct{}, len(raw))
	namespaces := make([]string, 0, len(raw))
	for _, value := range raw {
		name := ""
		switch typed := value.(type) {
		case string:
			name = typed
		case map[string]any:
			name = valueString(typed, "namespace", "name")
		default:
			return nil, fmt.Errorf("%s namespace 响应格式错误：存在非文本记录", environment)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("%s namespace 响应格式错误：存在空 namespace", environment)
		}
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			namespaces = append(namespaces, name)
		}
	}
	return namespaces, nil
}

type NamespaceAmbiguityError struct {
	Environment string
	Candidates  []string
	description string
}

func (err *NamespaceAmbiguityError) Error() string { return err.description }

func chooseNamespace(service, environment string, namespaces []string) (string, error) {
	if len(namespaces) == 0 {
		return "", fmt.Errorf("服务 %s 暂无 %s namespace：%w", service, environment, ErrConfigTarget)
	}
	prefix := service
	if separator := strings.IndexByte(service, '-'); separator > 0 {
		prefix = service[:separator]
	}
	for _, namespace := range namespaces {
		if namespace == prefix {
			return namespace, nil
		}
	}
	if len(namespaces) == 1 {
		return namespaces[0], nil
	}
	choices := append([]string(nil), namespaces...)
	sort.Strings(choices)
	return "", &NamespaceAmbiguityError{
		Environment: environment,
		Candidates:  choices,
		description: fmt.Sprintf("服务 %s 对应多个 %s namespace（%s）", service, environment, strings.Join(choices, ", ")),
	}
}

func selectLatestConfigMap(response any) (map[string]any, string, error) {
	record, ok := response.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("配置响应格式错误")
	}
	raw, ok := record["response"].([]any)
	if !ok {
		return nil, "", fmt.Errorf("配置响应格式错误：response 不是数组")
	}
	if len(raw) == 0 {
		return nil, "", nil
	}

	var latest map[string]any
	var latestTime time.Time
	latestTimeText := ""
	for index, value := range raw {
		configMap, ok := value.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("配置响应格式错误：第 %d 条 ConfigMap 不是对象", index+1)
		}
		updateTimeText := strings.TrimSpace(valueString(configMap, "update_time"))
		updateTime, err := time.ParseInLocation(genbuTimeLayout, updateTimeText, time.Local)
		if err != nil {
			return nil, "", fmt.Errorf("配置响应格式错误：第 %d 条 ConfigMap 的 update_time 无效", index+1)
		}
		if latest == nil || updateTime.After(latestTime) {
			latest = configMap
			latestTime = updateTime
			latestTimeText = updateTimeText
		}
	}
	return latest, latestTimeText, nil
}

func extractLatestConfigContents(response any) ([]string, string, error) {
	latest, latestTimeText, err := selectLatestConfigMap(response)
	if err != nil || latest == nil {
		return nil, "", err
	}
	files, err := readConfigBodies(latest, "")
	if err != nil {
		return nil, "", err
	}
	contents := make([]string, 0, len(files))
	for _, file := range files {
		contents = append(contents, file.Body)
	}
	return contents, latestTimeText, nil
}

func readConfigBodies(row map[string]any, file string) ([]configBody, error) {
	if file == "" && row == nil {
		return nil, nil
	}
	var entries []map[string]any
	if file != "" {
		entry, err := configEditFile(row, file)
		if err != nil {
			return nil, err
		}
		entries = []map[string]any{entry}
	} else {
		var err error
		entries, err = configFileRecords(row)
		if err != nil {
			return nil, err
		}
	}
	files := make([]configBody, 0, len(entries))
	for index, entry := range entries {
		content, ok := entry["content"].(string)
		if !ok {
			return nil, fmt.Errorf("配置响应格式错误：encoded_data 第 %d 项缺少文本 content", index+1)
		}
		name, _ := entry["name"].(string)
		files = append(files, configBody{Name: name, Body: content})
	}
	return files, nil
}

func (client *genbuClient) resolveServiceContext(service, token string) (resolvedService, error) {
	resolved := resolveService(service)
	if resolved.BuiltIn {
		return resolved, nil
	}
	appID, err := client.resolveAppID(resolved.Name, token)
	if err != nil {
		return resolvedService{}, err
	}
	resolved.AppID = appID
	return resolved, nil
}

func (client *genbuClient) resolveAppID(service, token string) (string, error) {
	query := url.Values{"production_line": []string{systemCode}}
	response, _, err := client.requestJSON(
		http.MethodPost,
		client.genbuURL+applicationsPath+"?"+query.Encode(),
		nil,
		token,
		"",
	)
	if err != nil {
		return "", err
	}
	matches := map[string]struct{}{}
	walkJSON(response, func(record map[string]any) {
		name, nameFound := firstValue(record, "app_name", "appName", "service_name", "serviceName")
		id, idFound := firstValue(record, "app_id", "appId")
		if nameFound && idFound && fmt.Sprint(name) == service {
			matches[fmt.Sprint(id)] = struct{}{}
		}
	})
	if len(matches) == 0 {
		return "", fmt.Errorf("未在产线 %s 中找到服务 %s：%w", systemCode, service, ErrServiceNotFound)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for id := range matches {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return "", fmt.Errorf("服务 %s 对应多个 app-id（%s），无法唯一确认服务", service, strings.Join(ids, ", "))
	}
	for id := range matches {
		return id, nil
	}
	panic("unreachable")
}

func (client *genbuClient) findVersion(service, tag, token, appID string) (genbuVersion, error) {
	response, err := client.queryVersions(token, appID, 1, 100, map[string]any{"key_word": tag})
	if err != nil {
		return genbuVersion{}, err
	}
	versions := extractVersions(response)
	var exact []genbuVersion
	for _, version := range versions {
		if version.VersionName == tag && (version.AppName == "" || version.AppName == service) {
			exact = append(exact, version)
		}
	}
	if len(exact) == 0 {
		availableSet := map[string]struct{}{}
		for _, version := range versions {
			availableSet[version.VersionName] = struct{}{}
		}
		available := make([]string, 0, len(availableSet))
		for name := range availableSet {
			available = append(available, name)
		}
		sort.Strings(available)
		if len(available) > 10 {
			available = available[:10]
		}
		suffix := ""
		if len(available) > 0 {
			suffix = "；接口返回的版本包括：" + strings.Join(available, ", ")
		}
		return genbuVersion{}, fmt.Errorf("未找到服务 %s 的 Tag %s%s：%w", service, tag, suffix, ErrTagNotFound)
	}
	best := exact[0]
	for _, candidate := range exact[1:] {
		if candidate.VersionID > best.VersionID {
			best = candidate
		}
	}
	return best, nil
}

func (client *genbuClient) queryBaseInfo(versionID int, token, appID string) ([]pipelineStep, error) {
	query := url.Values{"version_id": []string{strconv.Itoa(versionID)}}
	response, _, err := client.requestJSON(
		http.MethodGet,
		client.genbuURL+baseInfoPath+"?"+query.Encode(),
		nil,
		token,
		appID,
	)
	if err != nil {
		return nil, err
	}
	steps := extractSteps(response)
	if len(steps) == 0 {
		return nil, fmt.Errorf("version_id=%d 的响应中未找到流水线步骤", versionID)
	}
	return steps, nil
}

func (client *genbuClient) queryBuildLog(versionID, tailLines int, token, appID string) (string, error) {
	query := url.Values{
		"version_id": []string{strconv.Itoa(versionID)},
		"tail_lines": []string{strconv.Itoa(tailLines)},
	}
	response, _, err := client.requestJSON(
		http.MethodGet,
		client.genbuURL+buildLogPath+"?"+query.Encode(),
		nil,
		token,
		appID,
	)
	if err != nil {
		return "", err
	}
	record, ok := response.(map[string]any)
	if !ok {
		return "", fmt.Errorf("version_id=%d 的构建日志响应格式错误", versionID)
	}
	content, ok := record["response"].(string)
	if !ok {
		return "", fmt.Errorf("version_id=%d 的响应中未找到构建日志", versionID)
	}
	return content, nil
}

func (client *genbuClient) requestJSON(method, endpoint string, payload any, token, appID string) (any, http.Header, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot encode Genbu request")
		}
		body = bytes.NewReader(encoded)
	}
	ctx := client.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create Genbu request")
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("User-Agent", "genbu/0.2.2")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.HasPrefix(endpoint, client.genbuURL) {
		request.Header.Set("System-Code", systemCode)
		if appID != "" {
			request.Header.Set("App-Id", appID)
		}
	}
	if token != "" {
		request.Header.Set("X-Token", token)
	}

	httpClient := *client.httpClient
	// X-Token is a custom header and net/http otherwise copies it across redirects.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(request)
	if err != nil {
		var rejected RequestNotSentError
		if errors.As(err, &rejected) {
			return nil, nil, rejected
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, context.DeadlineExceeded
		}
		return nil, nil, fmt.Errorf("%s %s 连接失败：%s", method, endpoint, redactValues(err.Error(), token))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := ""
		if content, err := io.ReadAll(io.LimitReader(response.Body, responseErrorLimit+1)); err == nil && len(content) <= responseErrorLimit {
			if text := strings.TrimSpace(redactValues(string(content), token)); text != "" {
				detail = "：" + text
			}
		}
		message := fmt.Sprintf("%s %s 请求失败，HTTP %d%s", method, endpoint, response.StatusCode, detail)
		if response.StatusCode == http.StatusUnauthorized {
			return nil, nil, &authenticationError{message: message}
		}
		return nil, nil, fmt.Errorf("%s", message)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("%s %s 响应读取失败", method, endpoint)
	}
	if len(content) > maxResponseBytes {
		return nil, nil, fmt.Errorf("%s %s 响应超过允许的大小", method, endpoint)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, nil, fmt.Errorf("%s %s 返回空响应", method, endpoint)
	}
	if !utf8.Valid(content) {
		return nil, nil, fmt.Errorf("%s %s 返回的内容不是有效的 UTF-8", method, endpoint)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil || !errors.Is(decoder.Decode(new(json.RawMessage)), io.EOF) {
		return nil, nil, fmt.Errorf("%s %s 返回的不是 JSON", method, endpoint)
	}
	if err := validateAPIResponse(decoded, method, endpoint); err != nil {
		message := redactValues(err.Error(), token)
		if IsAuthenticationError(err) {
			return nil, nil, &authenticationError{message: message}
		}
		return nil, nil, errors.New(message)
	}
	return decoded, response.Header, nil
}

func validateAPIResponse(response any, method, endpoint string) error {
	record, ok := response.(map[string]any)
	if !ok {
		return nil
	}
	code, found := record["code"]
	if !found {
		return nil
	}
	codeText := fmt.Sprint(code)
	if codeText == "200" || codeText == "20000" || (codeText != "" && strings.Trim(codeText, "0") == "") {
		return nil
	}
	message := "未知错误"
	if value, ok := record["message"]; ok {
		message = fmt.Sprint(value)
	} else if value, ok := record["msg"]; ok {
		message = fmt.Sprint(value)
	}
	errorMessage := fmt.Sprintf("%s %s 业务请求失败，code=%s：%s", method, endpoint, codeText, redact(message))
	if codeText == "401" {
		return &authenticationError{message: errorMessage}
	}
	return fmt.Errorf("%s", errorMessage)
}

func extractToken(response any) string {
	var token string
	walkJSON(response, func(record map[string]any) {
		if token != "" {
			return
		}
		for key, value := range record {
			switch strings.ToLower(key) {
			case "token", "x-token", "x_token", "accesstoken", "access_token":
				candidate, ok := value.(string)
				if ok && strings.TrimSpace(candidate) != "" {
					token = strings.TrimSpace(candidate)
					return
				}
			}
		}
	})
	if token == "" {
		if candidate, ok := response.(string); ok {
			token = strings.TrimSpace(candidate)
		}
	}
	return token
}

func extractVersions(response any) []genbuVersion {
	seen := map[string]struct{}{}
	var versions []genbuVersion
	walkJSON(response, func(record map[string]any) {
		idValue, idFound := firstValue(record, "version_id", "versionId", "id")
		nameValue, nameFound := firstValue(record, "version_name", "versionName", "tag_name", "tagName", "tag", "name")
		if !idFound || !nameFound {
			return
		}
		versionID, err := strconv.Atoi(fmt.Sprint(idValue))
		if err != nil {
			return
		}
		appName := ""
		if appValue, found := firstValue(record, "app_name", "appName", "service_name", "serviceName"); found {
			appName = fmt.Sprint(appValue)
		}
		version := genbuVersion{VersionID: versionID, VersionName: fmt.Sprint(nameValue), AppName: appName}
		identity := fmt.Sprintf("%d\x00%s\x00%s", version.VersionID, version.VersionName, version.AppName)
		if _, exists := seen[identity]; !exists {
			seen[identity] = struct{}{}
			versions = append(versions, version)
		}
	})
	return versions
}

func extractSteps(response any) []pipelineStep {
	seen := map[string]struct{}{}
	var steps []pipelineStep
	walkJSON(response, func(record map[string]any) {
		stepValue, hasStep := record["step"]
		status, hasStatus := record["status"]
		if !hasStep || !hasStatus {
			return
		}
		stepNumber, err := strconv.Atoi(fmt.Sprint(stepValue))
		if err != nil {
			return
		}
		stepName := valueString(record, "step_name", "stepName")
		createdTime := valueString(record, "created_time", "createdTime")
		identity := fmt.Sprintf("%d\x00%s\x00%s", stepNumber, fmt.Sprint(status), createdTime)
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		steps = append(steps, pipelineStep{
			Step:        stepNumber,
			StepName:    stepName,
			Status:      status,
			CreatedTime: createdTime,
			Enabled:     record["enabled"],
		})
	})
	return steps
}

func latestStep(steps []pipelineStep, number int) *pipelineStep {
	var latest *pipelineStep
	for index := range steps {
		step := &steps[index]
		if step.Step == number && (latest == nil || step.CreatedTime > latest.CreatedTime) {
			latest = step
		}
	}
	return latest
}

func walkJSON(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(child, visit)
		}
	}
}

func firstValue(record map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := record[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func valueString(record map[string]any, keys ...string) string {
	if value, ok := firstValue(record, keys...); ok {
		return fmt.Sprint(value)
	}
	return ""
}

func valueInt(record map[string]any, keys ...string) (int, error) {
	value, found := firstValue(record, keys...)
	if !found {
		return 0, errors.New("missing integer")
	}
	return strconv.Atoi(fmt.Sprint(value))
}

func redact(value string) string {
	value = jwtPattern.ReplaceAllString(value, "<redacted-token>")
	return secretPattern.ReplaceAllString(value, `${1}<redacted>`)
}

func redactValues(value string, secrets ...string) string {
	value = redact(value)
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return value
}
