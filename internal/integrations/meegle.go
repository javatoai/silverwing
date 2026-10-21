package integrations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type meegleProject struct {
	Name       string `json:"name"`
	ProjectKey string `json:"projectKey"`
	SimpleName string `json:"simpleName"`
}
type sprintItem struct {
	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
}

func (s sprintItem) Key() string { return s.ProjectKey + ":" + s.ID }

type participatedResult struct {
	Items             []domain.ParticipatedWorkItem `json:"items"`
	Failures          []string                      `json:"failures"`
	CompletedQueries  int                           `json:"completedQueries"`
	Sprints           []sprintItem                  `json:"sprints"`
	SelectedSprintKey *string                       `json:"selectedSprintKey,omitempty"`
	FetchedAt         int64                         `json:"fetchedAt,omitempty"`
}
type workItemType struct {
	MQLName        string
	Path           string
	Label          string
	DeveloperRoles []string
	QCRoles        []string
	ProductRoles   []string
}

var workItemTypes = []workItemType{
	{MQLName: "User Story", Path: "userstory", Label: "需求", DeveloperRoles: []string{"Dev Owner"}, QCRoles: []string{"QC Owner"}, ProductRoles: []string{"产品经理"}},
	{MQLName: "Tech Improvement", Path: "technical", Label: "技术改进", DeveloperRoles: []string{"Dev Owner"}, QCRoles: []string{"QC Owner"}},
	{MQLName: "Bug", Path: "bug", Label: "缺陷", DeveloperRoles: []string{"Dev Owner"}, QCRoles: []string{"QC Owner"}},
	{MQLName: "Task", Path: "othertask", Label: "任务", DeveloperRoles: []string{"iOS工程师", "Android工程师", "服务端工程师", "Web端工程师", "嵌入式工程师", "SE工程师"}, QCRoles: []string{"测试工程师"}},
}

func (s *Service) meegleStatus(ctx context.Context, config domain.AppConfig) (any, error) {
	version, err := s.runMeegle(ctx, config, []string{"--version"}, 10*time.Second, "读取 Meegle CLI 版本")
	if err != nil || !version.Successful() {
		var commandFailure *commandError
		if process.IsCleanupFailure(err) || errors.As(err, &commandFailure) {
			return nil, err
		}
		return map[string]any{"installed": false, "authenticated": false, "authenticationState": "NOT_INSTALLED"}, nil
	}
	versionText := strings.TrimSpace(version.Stdout)
	if versionText == "" {
		versionText = strings.TrimSpace(version.Stderr)
	}
	status, statusErr := s.runMeegle(ctx, config, []string{"auth", "status", "--format", "json"}, 15*time.Second, "读取 Meegle 登录状态")
	if statusErr != nil {
		var commandFailure *commandError
		if process.IsCleanupFailure(statusErr) || !errors.As(statusErr, &commandFailure) {
			return nil, statusErr
		}
		return map[string]any{"installed": true, "version": versionText, "authenticated": false, "authenticationError": s.safeDiagnostic(config, statusErr.Error())}, nil
	}
	value, err := decodeJSON(status.Stdout)
	if err != nil {
		return nil, fmt.Errorf("Meegle 登录状态 JSON 解析失败：%w", err)
	}
	root := object(value)
	authenticated, ok := root["authenticated"].(bool)
	if !ok {
		return nil, errors.New("Meegle 登录状态缺少有效的 authenticated 字段")
	}
	result := map[string]any{"installed": true, "version": versionText, "authenticated": authenticated}
	if v := text(root["host"]); v != "" {
		result["host"] = v
	}
	if v, ok := integer(root["expires_in_minutes"]); ok {
		result["expiresInMinutes"] = v
	}
	return result, nil
}

func (s *Service) meegleProjectsForIdentity(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	expected, provided, err := expectedRequirementIdentity(payload)
	if err != nil {
		return nil, err
	}
	if !provided {
		return nil, errors.New("请先确认当前 Meegle 账户后读取项目目录")
	}
	return executeRequirementListWithIdentity(ctx, expected, func(ctx context.Context, token string) error {
		return s.verifyExpectedRequirementIdentity(ctx, config, token)
	}, func(ctx context.Context) (any, error) {
		return s.meegleProjects(ctx, config)
	})
}

func (s *Service) meegleProjects(ctx context.Context, config domain.AppConfig) (any, error) {
	result, err := s.runMeegle(ctx, config, []string{"project", "search", "--auto-paginate", "--format", "json"}, 20*time.Second, "读取 Meegle 项目")
	if err != nil {
		return nil, err
	}
	value, err := decodeJSON(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("Meegle 项目 JSON 解析失败：%w", err)
	}
	projects, ok := object(value)["projects"].([]any)
	if !ok {
		return nil, errors.New("Meegle 项目响应缺少有效的 projects 列表")
	}
	out := make([]meegleProject, 0, len(projects))
	for _, item := range projects {
		row := object(item)
		project := meegleProject{Name: text(row["name"]), ProjectKey: text(row["project_key"]), SimpleName: text(row["simple_name"])}
		if project.Name == "" || project.ProjectKey == "" || project.SimpleName == "" {
			return nil, errors.New("Meegle 项目缺少 name、project_key 或 simple_name")
		}
		out = append(out, project)
	}
	return map[string]any{"projects": out}, nil
}

func (s *Service) listRequirements(ctx context.Context, config domain.AppConfig, payload map[string]any, force bool) (any, error) {
	expected, provided, err := expectedRequirementIdentity(payload)
	if err != nil {
		return nil, err
	}
	if !provided {
		return s.listRequirementsQuery(ctx, config, payload)
	}
	keys, err := participatedListCacheKeysFor(config, expected, payload)
	if err != nil {
		return nil, err
	}
	force = force || boolValue(payload, "force")
	requestedKey, err := keys.requestedKey()
	if err != nil {
		return nil, err
	}
	if !force {
		cached, hit, cacheErr := s.readConfirmedRequirementListCache(ctx, expected, requestedKey)
		if cacheErr != nil {
			return nil, cacheErr
		}
		if hit {
			if typed, ok := cached.(participatedResult); ok {
				return s.overlayRequirementList(s.requirementOverlayIdentity(keys.Identity), typed), nil
			}
			return cached, nil
		}
	}
	cacheHit := false
	result, err := executeRequirementListWithIdentity(
		ctx,
		expected,
		func(ctx context.Context, token string) error {
			return s.verifyExpectedRequirementIdentity(ctx, config, token)
		},
		func(ctx context.Context) (any, error) {
			value, hit, loadErr := s.loadParticipatedList(ctx, requestedKey, force, func(ctx context.Context) (any, error) {
				return s.listRequirementsQuery(ctx, config, payload)
			})
			cacheHit = hit
			return value, loadErr
		},
	)
	if err != nil {
		return nil, err
	}
	if !cacheHit {
		if typed, ok := result.(participatedResult); ok && requirementListResultCacheable(typed) {
			s.persistParticipatedListCache(keys, typed, force)
		}
	}
	if typed, ok := result.(participatedResult); ok {
		return s.overlayRequirementList(s.requirementOverlayIdentity(keys.Identity), typed), nil
	}
	return result, nil
}

func (s *Service) listRequirementsQuery(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	projects := append([]domain.MeegleProjectConfig(nil), config.MeegleProjects...)
	if len(projects) == 0 {
		return participatedResult{Items: []domain.ParticipatedWorkItem{}, Failures: []string{}}, nil
	}
	statusValue, statusErr := s.meegleStatus(ctx, config)
	status := object(statusValue)
	if statusErr != nil {
		if process.IsCleanupFailure(statusErr) {
			return nil, statusErr
		}
		return participatedResult{Items: []domain.ParticipatedWorkItem{}, Failures: []string{statusErr.Error()}}, nil
	}
	if installed, _ := status["installed"].(bool); !installed {
		return participatedResult{Items: []domain.ParticipatedWorkItem{}, Failures: []string{"Meegle CLI 未安装或无法启动，请在设置中检查命令路径"}}, nil
	}
	if authenticated, _ := status["authenticated"].(bool); !authenticated {
		reason := "Meegle CLI 未登录，请先在设置中登录"
		if msg := text(status["authenticationError"]); msg != "" {
			reason = "Meegle CLI 登录状态检查失败：" + msg
		}
		return participatedResult{Items: []domain.ParticipatedWorkItem{}, Failures: []string{reason}}, nil
	}
	// Project-key duplicates are removed before the sprint catalog is read.
	unique := make([]domain.MeegleProjectConfig, 0, len(projects))
	seenProjects := map[string]bool{}
	for _, project := range projects {
		if !seenProjects[project.ProjectKey] {
			unique = append(unique, project)
			seenProjects[project.ProjectKey] = true
		}
	}

	sprints := make([]sprintItem, 0)
	failures := make([]string, 0)
	completedSprints := 0
	for _, project := range unique {
		items, err := s.querySprints(ctx, config, project)
		if err != nil {
			if process.IsCleanupFailure(err) {
				return nil, err
			}
			failures = append(failures, project.SimpleName+" · Sprint："+err.Error())
			continue
		}
		sprints = append(sprints, items...)
		completedSprints++
	}
	sort.SliceStable(sprints, func(i, j int) bool { return sprints[i].Status != "进行中" && sprints[j].Status == "进行中" })
	var sprintCatalog []sprintItem
	if len(sprints) > 0 || completedSprints > 0 {
		sprintCatalog = sprints
	}
	selectedKey := stringValue(payload, "sprintKey", "selectedSprintKey")
	var selected *sprintItem
	if selectedKey != "" {
		for i := range sprints {
			if sprints[i].Key() == selectedKey {
				selected = &sprints[i]
				break
			}
		}
	} else {
		// Auto-selection is safe only after every configured project returned its
		// sprint catalog. Explicit selection above still works with partial results.
		if completedSprints == len(unique) {
			var ongoing []sprintItem
			for i := range sprints {
				if sprints[i].Status == "进行中" {
					ongoing = append(ongoing, sprints[i])
				}
			}
			if len(ongoing) == 1 {
				selected = &ongoing[0]
			}
			if selected == nil && config.MeegleDefaultSprintProjectKey != nil {
				for i := range ongoing {
					if ongoing[i].ProjectKey == *config.MeegleDefaultSprintProjectKey {
						selected = &ongoing[i]
						break
					}
				}
			}
		}
	}
	if selected == nil {
		result := participatedResult{Items: []domain.ParticipatedWorkItem{}, Failures: failures, Sprints: sprintCatalog}
		if selectedKey != "" && sprintCatalog != nil {
			result.Failures = append(result.Failures, "所选 Sprint 已不可选，请重新选择")
		}
		return result, nil
	}
	project, ok := projectByKey(unique, selected.ProjectKey)
	if !ok {
		return nil, errors.New("Sprint 所属 Meegle 项目配置已变化")
	}
	items, queryFailures, completed, err := s.queryParticipatedItems(ctx, config, project, *selected)
	if err != nil {
		return nil, err
	}
	failures = append(failures, queryFailures...)
	return participatedResult{Items: items, Failures: failures, CompletedQueries: completed, Sprints: sprintCatalog, SelectedSprintKey: nonblank(selected.Key())}, nil
}

func projectByKey(projects []domain.MeegleProjectConfig, key string) (domain.MeegleProjectConfig, bool) {
	for _, p := range projects {
		if p.ProjectKey == key {
			return p, true
		}
	}
	return domain.MeegleProjectConfig{}, false
}

func (s *Service) querySprints(ctx context.Context, config domain.AppConfig, project domain.MeegleProjectConfig) ([]sprintItem, error) {
	mql := fmt.Sprintf("SELECT `work_item_id`, `name`, `work_item_status` FROM `%s`.`Sprint` WHERE (`Status` = '进行中' OR `Status` = '未开始')", project.ProjectKey)
	root, err := s.executeMQL(ctx, config, project.ProjectKey, mql, 20*time.Second)
	if err != nil {
		return nil, err
	}
	rows := responseRows(root, map[string]bool{"moql_field_list": true, "item_id": true, "work_item_id": true, "name": true, "work_item_status": true, "status": true})
	if rows == nil {
		return nil, errors.New("Meegle 未返回可识别的 Sprint 列表")
	}
	out := make([]sprintItem, 0, len(rows))
	for _, row := range rows {
		id := firstFieldText(row, "work_item_id", "item_id")
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(id) {
			continue
		}
		title := sprintTitle(selectedField(row, "name"))
		status := workItemStatus(row)
		if status == "" {
			status = text(row["work_item_status"])
			if status == "" {
				status = text(row["status"])
			}
		}
		if title == "" || (status != "进行中" && status != "未开始") {
			continue
		}
		if status == "未开始" && strings.EqualFold(title, "Backlog") {
			continue
		}
		out = append(out, sprintItem{project.ProjectKey, project.SimpleName, id, title, status})
	}
	return dedupeSprints(out), nil
}

func dedupeSprints(items []sprintItem) []sprintItem {
	seen := map[string]bool{}
	out := make([]sprintItem, 0, len(items))
	for _, item := range items {
		if !seen[item.Key()] {
			seen[item.Key()] = true
			out = append(out, item)
		}
	}
	return out
}

func (s *Service) executeMQL(ctx context.Context, config domain.AppConfig, projectKey, mql string, timeout time.Duration) (map[string]any, error) {
	args := []string{"workitem", "query", "--project-key", projectKey, "--mql", mql, "--auto-paginate", "--format", "json"}
	result, err := s.runMeegle(ctx, config, args, timeout, "Meegle MQL 查询")
	if err != nil {
		return nil, err
	}
	rootAny, err := decodeJSON(result.Stdout)
	if err != nil {
		return nil, errors.New("Meegle MQL 返回不是 JSON")
	}
	root := object(rootAny)
	descriptor := firstDescriptor(root)
	if text(root["session_id"]) == "" && descriptor == nil {
		return root, nil
	}
	if text(root["session_id"]) == "" && object(root["data"]) != nil && len(object(root["data"])) == 0 {
		return root, nil
	}
	session := text(root["session_id"])
	if session == "" {
		return root, nil
	}
	total, ok := integer(object(descriptor)["count"])
	if !ok || total < 0 {
		return nil, errors.New("Meegle MQL 缺少有效总条目数")
	}
	rows := make([]any, 0, minInt(int(total), 10000))
	seenIDs := map[string]bool{}
	page := int64(1)
	for {
		if current := text(root["session_id"]); current != "" && current != session {
			return nil, errors.New("Meegle MQL 分页会话发生变化")
		}
		if d := firstDescriptor(root); d != nil {
			if count, ok := integer(object(d)["count"]); !ok || count != total {
				return nil, errors.New("Meegle MQL 分页总条目数发生变化")
			}
		}
		pageRows := array(object(root["data"])["1"])
		want := minInt(50, int(total-int64(len(rows))))
		if len(pageRows) != want {
			return nil, fmt.Errorf("Meegle MQL 第 %d 页条目数不完整或超出总数", page)
		}
		ids := make([]string, 0, len(pageRows))
		for _, row := range pageRows {
			id := firstFieldText(object(row), "work_item_id", "item_id")
			if !regexp.MustCompile(`^[0-9]+$`).MatchString(id) || seenIDs[id] {
				return nil, fmt.Errorf("Meegle MQL 第 %d 页分页内容重复或缺少 ID", page)
			}
			seenIDs[id] = true
			ids = append(ids, id)
		}
		rows = append(rows, pageRows...)
		if int64(len(rows)) == total {
			return map[string]any{"data": map[string]any{"1": rows}}, nil
		}
		page++
		paramsPath, cleanup, err := s.writeJSONArgument(map[string]any{"group_pagination_list": []any{
			map[string]any{"group_id": "1", "page_num": page},
		}})
		if err != nil {
			return nil, fmt.Errorf("Meegle MQL 第 %d 页参数文件创建失败：%w", page, err)
		}
		result, err = s.runMeegle(ctx, config, []string{"workitem", "query", "--project-key", projectKey, "--session-id", session, "--params", "@" + paramsPath, "--format", "json"}, timeout, fmt.Sprintf("Meegle MQL 第 %d 页", page))
		cleanup()
		if err != nil {
			return nil, err
		}
		rootAny, err = decodeJSON(result.Stdout)
		if err != nil {
			return nil, errors.New("Meegle MQL 分页返回不是 JSON")
		}
		root = object(rootAny)
	}
}

func firstDescriptor(root map[string]any) any {
	values := array(root["list"])
	if len(values) > 0 {
		return values[0]
	}
	return nil
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func responseRows(root map[string]any, markers map[string]bool) []map[string]any {
	if root == nil || (root["data"] == nil && root["list"] == nil) {
		return nil
	}
	rows := []map[string]any{}
	var walk func(any, bool)
	walk = func(value any, rowCandidate bool) {
		switch current := value.(type) {
		case map[string]any:
			if rowCandidate || hasAnyKey(current, markers) {
				rows = append(rows, current)
				return
			}
			for _, child := range current {
				walk(child, false)
			}
		case []any:
			for _, child := range current {
				walk(child, true)
			}
		}
	}
	if root["data"] != nil {
		walk(root["data"], false)
	} else {
		walk(root["list"], false)
	}
	return rows
}
func hasAnyKey(value map[string]any, markers map[string]bool) bool {
	for key := range value {
		if markers[key] {
			return true
		}
	}
	return false
}
func selectedField(row map[string]any, key string) any {
	if value, ok := row[key]; ok {
		return value
	}
	for _, raw := range array(row["moql_field_list"]) {
		field := object(raw)
		if normalizeFieldName(text(field["key"])) == normalizeFieldName(key) || normalizeFieldName(text(field["name"])) == normalizeFieldName(key) {
			return field["value"]
		}
	}
	return nil
}
func firstFieldText(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := text(selectedField(row, key)); value != "" {
			return value
		}
	}
	return ""
}
func sprintTitle(value any) string { return text(value) }
func workItemStatus(row map[string]any) string {
	value := selectedField(row, "work_item_status")
	labels := []string{}
	for _, item := range array(object(value)["key_label_value_list"]) {
		if v := text(object(item)["label"]); v != "" {
			labels = append(labels, v)
		}
	}
	return strings.Join(uniqueStrings(labels), "、")
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) readPages(ctx context.Context, config domain.AppConfig, args []string, timeout time.Duration, operation string) ([]map[string]any, error) {
	rows := []map[string]any{}
	seen := map[string]bool{}
	for page := 1; page <= 1000; page++ {
		pageArgs := append(append([]string(nil), args...), "--page-num", fmt.Sprint(page), "--format", "json")
		result, err := s.runMeegle(ctx, config, pageArgs, timeout, operation)
		if err != nil {
			return nil, err
		}
		value, err := decodeJSON(result.Stdout)
		if err != nil {
			return nil, fmt.Errorf("%s JSON 解析失败", operation)
		}
		root := object(value)
		list, ok := root["list"].([]any)
		if !ok {
			return nil, fmt.Errorf("%s 未返回完整列表", operation)
		}
		pagination := object(root["pagination"])
		pageNumber, ok := integer(pagination["page_num"])
		if !ok || pageNumber != int64(page) {
			return nil, fmt.Errorf("%s 返回的分页页码未正确前进", operation)
		}
		hasMore, ok := boolean(pagination["has_more"])
		if !ok {
			return nil, fmt.Errorf("%s 分页状态无效", operation)
		}
		for _, item := range list {
			row := object(item)
			if row == nil {
				return nil, fmt.Errorf("%s 列表条目格式无效", operation)
			}
			rows = append(rows, row)
		}
		if !hasMore {
			return rows, nil
		}
		fingerprint := compactJSON(list)
		if len(list) == 0 || seen[fingerprint] {
			return nil, fmt.Errorf("%s 分页内容未前进", operation)
		}
		seen[fingerprint] = true
	}
	return nil, fmt.Errorf("%s 分页超过读取上限", operation)
}

func (s *Service) queryParticipatedItems(ctx context.Context, config domain.AppConfig, project domain.MeegleProjectConfig, sprint sprintItem) ([]domain.ParticipatedWorkItem, []string, int, error) {
	userValue, err := s.runMeegle(ctx, config, []string{"user", "me", "--format", "json"}, 20*time.Second, "读取当前 Meegle 用户")
	if process.IsCleanupFailure(err) {
		return nil, nil, 0, err
	}
	userKey := ""
	userErr := err
	if err == nil {
		userRoot, decodeErr := decodeJSON(userValue.Stdout)
		if decodeErr != nil {
			userErr = errors.New("当前登录人 JSON 无法解析")
		} else {
			userKey = text(at(userRoot, "user_key"))
			if userKey == "" {
				userKey = text(at(userRoot, "user", "user_key"))
			}
			if userKey == "" {
				userErr = errors.New("当前登录人缺少 user_key")
			}
		}
	}
	dateRange, dateErr := s.querySprintDateRange(ctx, config, project, sprint.ID)
	if process.IsCleanupFailure(dateErr) {
		return nil, nil, 0, dateErr
	}
	typeResult := make([][]domain.ParticipatedWorkItem, len(workItemTypes))
	incompleteTypes := make([]bool, len(workItemTypes))
	failures := []string{}
	completed := 0
	for i, typ := range workItemTypes {
		items, err := s.queryWorkItemType(ctx, config, project, sprint, typ)
		typeResult[i] = items
		if err != nil {
			if process.IsCleanupFailure(err) {
				return nil, nil, 0, err
			}
			incompleteTypes[i] = true
			failures = append(failures, project.SimpleName+" · "+typ.Label+"："+err.Error())
			continue
		}
		completed++
	}
	all := aggregateParticipatedTypeResults(typeResult)
	// Schedule enrichment is optional metadata, but incomplete reads remain visible per item.
	for i := range all {
		if userErr != nil {
			all[i].MetadataWarnings = append(all[i].MetadataWarnings, "个人估分："+shortError(userErr))
			continue
		}
		if dateErr != nil {
			all[i].MetadataWarnings = append(all[i].MetadataWarnings, "个人估分："+shortError(dateErr))
			continue
		}
		if typeIndex := workItemTypeIndex(all[i].Type); typeIndex >= 0 && incompleteTypes[typeIndex] {
			all[i].MetadataWarnings = append(all[i].MetadataWarnings, "个人估分：工作项的 Sprint 归属查询不完整")
			continue
		}
		nodes, err := s.readPages(ctx, config, []string{"workflow", "get-node", "--project-key", all[i].ProjectKey, "--work-item-id", all[i].ID, "--node-id-list", "_all", "--field-key-list", "_all", "--need-sub-task", "true"}, 20*time.Second, "读取工作项排期明细")
		if err != nil {
			if process.IsCleanupFailure(err) {
				return nil, nil, 0, err
			}
			all[i].MetadataWarnings = append(all[i].MetadataWarnings, project.SimpleName+" · #"+all[i].ID+" 个人估分："+shortError(err))
			continue
		}
		value, err := personalEstimate(nodes, userKey, *dateRange)
		if err != nil {
			all[i].MetadataWarnings = append(all[i].MetadataWarnings, project.SimpleName+" · #"+all[i].ID+" 个人估分："+shortError(err))
			continue
		}
		formatted := value.String()
		all[i].MySprintEstimateDays = &formatted
	}
	return all, failures, completed, nil
}

func aggregateParticipatedTypeResults(typeResults [][]domain.ParticipatedWorkItem) []domain.ParticipatedWorkItem {
	items := []domain.ParticipatedWorkItem{}
	for _, result := range typeResults {
		items = append(items, result...)
	}
	return dedupeParticipatedItems(items)
}

func dedupeParticipatedItems(items []domain.ParticipatedWorkItem) []domain.ParticipatedWorkItem {
	seen := make(map[string]struct{}, len(items))
	unique := make([]domain.ParticipatedWorkItem, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item.Key()]; exists {
			continue
		}
		seen[item.Key()] = struct{}{}
		unique = append(unique, item)
	}
	return unique
}

func workItemTypeIndex(path string) int {
	for i, typ := range workItemTypes {
		if typ.Path == path {
			return i
		}
	}
	return -1
}

func shortError(err error) string {
	if err == nil {
		return "读取失败"
	}
	value := strings.TrimSpace(strings.Split(err.Error(), "\n")[0])
	if len(value) > 300 {
		value = value[:300]
	}
	if value == "" {
		return "读取失败"
	}
	return value
}

func (s *Service) queryWorkItemType(ctx context.Context, config domain.AppConfig, project domain.MeegleProjectConfig, sprint sprintItem, typ workItemType) ([]domain.ParticipatedWorkItem, error) {
	roles, err := s.readPages(ctx, config, []string{"workitem", "meta-roles", "--project-key", project.ProjectKey, "--work-item-type", typ.MQLName}, 20*time.Second, "读取工作项人员角色")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, role := range roles {
		id := text(role["role_id"])
		name := text(role["role_name"])
		if id == "" || name == "" {
			return nil, errors.New("人员角色缺少 role_id 或 role_name")
		}
		if strings.IndexFunc(id, func(r rune) bool { return r == ' ' || r < 0x20 }) >= 0 || strings.ContainsAny(name, "`\\\r\n") {
			return nil, errors.New("人员角色包含不支持的 MQL 字符")
		}
		if prior, ok := ids[name]; ok && prior != id {
			return nil, fmt.Errorf("角色名称 %s 对应多个标识", name)
		}
		ids[name] = id
	}
	if len(ids) == 0 {
		return []domain.ParticipatedWorkItem{}, nil
	}
	participation := []string{}
	for _, id := range sortedKeys(ids) {
		participation = append(participation, "array_contains(`__"+id+"`, current_login_user())")
	}
	from := fmt.Sprintf("FROM `%s`.`%s` WHERE array_contains(`Sprint`, '<id:%s>') AND (%s)", project.ProjectKey, typ.MQLName, sprint.ID, strings.Join(participation, " OR "))
	base := []string{"work_item_id", "name", "work_item_status"}
	roleNames := sortedKeys(ids)
	cols := append(append([]string{}, base...), mapStrings(roleNames, func(v string) string { return "__" + v })...)
	mql := fmt.Sprintf("SELECT %s %s", quoteMQLColumns(cols), from)
	root, err := s.executeMQL(ctx, config, project.ProjectKey, mql, 60*time.Second)
	roleWarning := ""
	if process.IsCleanupFailure(err) {
		return nil, err
	}
	if err != nil && strings.Contains(err.Error(), "3003") {
		roleWarning = "人员字段不可用：" + shortError(err)
		mql = fmt.Sprintf("SELECT %s %s", quoteMQLColumns(base), from)
		root, err = s.executeMQL(ctx, config, project.ProjectKey, mql, 60*time.Second)
	}
	if err != nil {
		return nil, err
	}
	rows := responseRows(root, map[string]bool{"moql_field_list": true, "work_item_id": true})
	if rows == nil {
		return nil, errors.New("Meegle 未返回可识别的工作项列表")
	}
	out := []domain.ParticipatedWorkItem{}
	failures := []string{}
	for _, row := range rows {
		id := firstFieldText(row, "work_item_id")
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(id) {
			failures = append(failures, "返回的工作项缺少有效 ID")
			continue
		}
		title := firstFieldText(row, "name")
		if title == "" {
			failures = append(failures, "工作项 "+id+" 缺少标题")
			title = "未命名工作项 #" + id
		}
		item := domain.ParticipatedWorkItem{ProjectKey: project.ProjectKey, ProjectName: project.SimpleName, Type: typ.Path, TypeLabel: typ.Label, ID: id, Title: title, URL: fmt.Sprintf("https://project.feishu.cn/%s/%s/detail/%s", project.SimpleName, typ.Path, id), Developers: []domain.RequirementPerson{}, QCOwners: []domain.RequirementPerson{}, ProductManagers: []domain.RequirementPerson{}, MetadataWarnings: []string{}, Status: nonblank(workItemStatus(row))}
		for _, field := range []struct {
			roles []string
			dst   *[]domain.RequirementPerson
		}{{typ.DeveloperRoles, &item.Developers}, {typ.QCRoles, &item.QCOwners}, {typ.ProductRoles, &item.ProductManagers}} {
			people, peopleErr := roleMembers(row, field.roles)
			if peopleErr != nil {
				item.MetadataWarnings = append(item.MetadataWarnings, project.SimpleName+" · #"+id+" 人员："+shortError(peopleErr))
			} else {
				*field.dst = people
			}
		}
		if roleWarning != "" {
			item.MetadataWarnings = append(item.MetadataWarnings, project.SimpleName+" · "+typ.Label+" "+roleWarning)
		}
		out = append(out, item)
	}
	if len(failures) > 0 {
		return out, fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	return out, nil
}

func quoteMQLColumns(columns []string) string {
	out := make([]string, len(columns))
	for i, v := range columns {
		out[i] = "`" + v + "`"
	}
	return strings.Join(out, ", ")
}
func sortedKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for k := range values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func mapStrings(values []string, convert func(string) string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = convert(v)
	}
	return out
}

func roleMembers(row map[string]any, roles []string) ([]domain.RequirementPerson, error) {
	fields := array(row["moql_field_list"])
	dedup := map[string]domain.RequirementPerson{}
	for _, name := range roles {
		var value any
		for _, raw := range fields {
			field := object(raw)
			if text(field["name"]) == "__"+name {
				value = field["value"]
				break
			}
		}
		if value == nil {
			continue
		}
		users, ok := object(value)["user_value_list"].([]any)
		if !ok && value != nil {
			return nil, fmt.Errorf("角色 %s 未返回人员列表", name)
		}
		for _, raw := range users {
			user := object(raw)
			display := ""
			for _, key := range []string{"name_cn", "name_en", "email", "user_key"} {
				if display = text(user[key]); display != "" {
					break
				}
			}
			if display == "" {
				return nil, fmt.Errorf("角色成员缺少可展示身份")
			}
			email := nonblank(text(user["email"]))
			identity := text(user["user_key"])
			if identity == "" {
				identity = text(user["email"])
			}
			if identity == "" {
				identity = display
			}
			dedup[identity] = domain.RequirementPerson{Name: display, Email: email}
		}
	}
	out := make([]domain.RequirementPerson, 0, len(dedup))
	for _, person := range dedup {
		out = append(out, person)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) querySprintDateRange(ctx context.Context, config domain.AppConfig, project domain.MeegleProjectConfig, id string) (*sprintRange, error) {
	fields, err := s.readPages(ctx, config, []string{"workitem", "meta-fields", "--project-key", project.ProjectKey, "--work-item-type", "Sprint"}, 20*time.Second, "读取 Sprint 排期字段")
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, field := range fields {
		if text(field["field_name"]) == "Duration" && text(field["field_type"]) == "schedule" {
			keys = append(keys, text(field["field_key"]))
		}
	}
	if len(keys) != 1 || keys[0] == "" {
		return nil, errors.New("Sprint 的 Duration 排期字段缺失或不唯一")
	}
	result, err := s.runMeegle(ctx, config, []string{"workitem", "get", "--project-key", project.ProjectKey, "--work-item-id", id, "--fields", keys[0], "--format", "json"}, 20*time.Second, "读取 Sprint 排期")
	if err != nil {
		return nil, err
	}
	root, err := decodeJSON(result.Stdout)
	if err != nil {
		return nil, errors.New("Sprint 排期 JSON 无法解析")
	}
	var target any
	for _, field := range array(at(root, "work_item_fields")) {
		if text(object(field)["key"]) == keys[0] {
			target = object(field)["value"]
		}
	}
	start, ok1 := integer(at(target, "start_time", "timestamp"))
	end, ok2 := integer(at(target, "end_time", "timestamp"))
	if !ok1 || !ok2 || start > end {
		return nil, errors.New("Sprint 排期时间无效")
	}
	return &sprintRange{start, end}, nil
}

type sprintRange struct{ Start, End int64 }

func parseRemoteRequirementURL(raw string) (string, string, string, string, error) {
	link := domain.ParseFeishuWorkItemLink(raw)
	if link == nil {
		return "", "", "", "", errors.New("仅支持飞书工作项链接")
	}
	return link.Space, link.Kind, link.WorkItemID, valuePointer(link.ProjectKey), nil
}
func valuePointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) readRequirement(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	item, err := s.requirementTarget(ctx, config, payload)
	if err != nil {
		return nil, err
	}
	force := boolValue(payload, "force")
	expected, _, err := expectedRequirementIdentity(payload)
	if err != nil {
		return nil, err
	}
	session, loggedOut, identityErr := s.requirementIdentityFor(ctx, config, expected, force, s.requirementCacheIdentity)
	identity := session.raw
	if process.IsCleanupFailure(identityErr) {
		return nil, identityErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var changed *RequirementIdentityChangedError
	if errors.As(identityErr, &changed) {
		return nil, identityErr
	}
	if err := expectedRequirementDocumentIdentity(payload, identity, loggedOut, identityErr); err != nil {
		return nil, err
	}
	if loggedOut {
		return nil, errors.New("Meegle CLI 当前未登录")
	}
	if force && (identityErr != nil || identity == "") {
		return nil, errors.New("无法确认当前 Meegle 账户，不能安全刷新正文缓存")
	}
	key := requirementCacheKey("body", identity, item)
	return s.requirementDocumentCoordinator().read(ctx, key, force, func(shared context.Context, request *requirementBodyRequest) (RequirementReadResult, error) {
		return s.fetchRequirementDocument(shared, config, item, identity, session.generation, key, request, boolValue(payload, "fullRefresh"))
	})
}

func requirementReadFromDocument(document *requirementDocument, source string, stale bool, refreshError, cacheError string) RequirementReadResult {
	result := RequirementReadResult{ID: document.Index.ID, ProjectKey: document.Index.Project, Type: document.Index.Type,
		Title: document.Index.Title, Body: document.Body, URL: document.Index.URL, FetchedAt: document.Index.FetchedAt,
		Source: source, Stale: stale, RefreshError: refreshError, CacheError: cacheError,
		MarkdownPath: document.MarkdownPath, Generation: document.Index.Generation, Resources: append([]RequirementResource{}, document.Resources...),
		Complete: document.Complete && cacheError == ""}
	if len(document.Resources) > 0 {
		result.AssetsDirectory = document.AssetsPath
	}
	return result
}

func requirementReadFromRemote(item domain.ParticipatedWorkItem, body string, fetchedAt int64, cacheError string) RequirementReadResult {
	resources := make([]RequirementResource, 0)
	for _, imageURL := range requirementDocumentImageURLs(body) {
		resources = append(resources, RequirementResource{URL: imageURL})
	}
	return RequirementReadResult{ID: item.ID, ProjectKey: item.ProjectKey, Type: item.Type, Title: item.Title, Body: body,
		URL: item.URL, FetchedAt: fetchedAt, Source: "REMOTE", CacheError: cacheError, Complete: false, Resources: resources}
}

func (s *Service) readWorkItem(ctx context.Context, config domain.AppConfig, projectKey, id string) (map[string]any, error) {
	return s.readWorkItemWithField(ctx, config, projectKey, id, "")
}

func (s *Service) readWorkItemWithField(ctx context.Context, config domain.AppConfig, projectKey, id, fieldKey string) (map[string]any, error) {
	args := []string{"workitem", "get", "--project-key", projectKey, "--work-item-id", id}
	if fieldKey != "" {
		args = append(args, "--fields", fieldKey)
	}
	args = append(args, "--format", "json")
	result, err := s.runMeegle(ctx, config, args, 12*time.Second, "读取需求")
	if err != nil {
		return nil, err
	}
	value, err := decodeJSON(result.Stdout)
	if err != nil {
		return nil, errors.New("读取需求返回的 JSON 无法解析")
	}
	root := object(value)
	if root == nil {
		return nil, errors.New("Meegle 返回不是 JSON 对象")
	}
	return root, nil
}
func requirementTitle(root map[string]any) string {
	for _, v := range []any{root["name"], root["work_item_name"], at(root, "work_item_attribute", "name"), at(root, "work_item_attribute", "work_item_name")} {
		if v := text(v); v != "" {
			return v
		}
	}
	return ""
}
func (s *Service) requirementBody(ctx context.Context, config domain.AppConfig, summary map[string]any, projectKey, kind, id string) (string, error) {
	if body := bodyFromWorkItem(summary); body != "" {
		return body, nil
	}
	workType := text(at(summary, "work_item_attribute", "work_item_type", "key"))
	if workType == "" {
		workType = text(at(summary, "work_item_attribute", "work_item_type", "name"))
	}
	if workType == "" {
		workType = kind
	}
	for _, query := range []string{"需求描述", "描述", "内容", "详情"} {
		fields, err := s.readPages(ctx, config, []string{"workitem", "meta-fields", "--project-key", projectKey, "--work-item-type", workType, "--page-num", "1", "--field-query", query}, 20*time.Second, "查询需求正文配置")
		if err != nil {
			return "", err
		}
		var field map[string]any
		for _, f := range fields {
			if oneOfEqual(text(f["field_name"]), "需求描述", "描述", "需求内容", "内容", "详情", "Description", "Content", "Detail") {
				field = f
				break
			}
		}
		if field == nil {
			continue
		}
		key := text(field["field_key"])
		if key == "" {
			continue
		}
		detail, err := s.readWorkItemWithField(ctx, config, projectKey, id, key)
		if err != nil {
			return "", err
		}
		if body := bodyFromFieldKey(detail, key); body != "" {
			return body, nil
		}
	}
	return "", nil
}
func oneOfEqual(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
