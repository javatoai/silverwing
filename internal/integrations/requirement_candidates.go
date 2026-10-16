package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type requirementCandidateJSONField struct {
	name  string
	value any
}

type requirementCandidateJSONObject []requirementCandidateJSONField

func (object requirementCandidateJSONObject) get(name string) (any, bool) {
	for index := len(object) - 1; index >= 0; index-- {
		if object[index].name == name {
			return object[index].value, true
		}
	}
	return nil, false
}

// RequirementCandidatesResult is the uncached picker result. It intentionally
// stays separate from the single-Sprint participated-list contract.
type RequirementCandidatesResult struct {
	Candidates       []RequirementCandidate        `json:"candidates"`
	Failures         []RequirementCandidateFailure `json:"failures"`
	CompletedQueries int                           `json:"completedQueries"`
}

type RequirementCandidate struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	SourceID string `json:"sourceId"`
}

type RequirementCandidateFailure struct {
	Stage        string `json:"stage"`
	ProjectKey   string `json:"projectKey,omitempty"`
	SprintID     string `json:"sprintId,omitempty"`
	WorkItemType string `json:"workItemType,omitempty"`
	Message      string `json:"message"`
}

type requirementCandidateWorkItemType struct {
	mqlName string
	path    string
}

var requirementCandidateWorkItemTypes = []requirementCandidateWorkItemType{
	{mqlName: "User Story", path: "userstory"},
	{mqlName: "Tech Improvement", path: "technical"},
	{mqlName: "Bug", path: "bug"},
	{mqlName: "Task", path: "othertask"},
}

type requirementCandidateQueryResult struct {
	itemIDs []string
	failure *RequirementCandidateFailure
}

type requirementCandidateSprintQuery struct {
	project domain.MeegleProjectConfig
	sprint  string
	typ     requirementCandidateWorkItemType
}

type requirementCandidateLink struct {
	url        string
	projectKey string
}

// ReadRequirementCandidates loads every configured project's in-progress
// Sprints, then the four source work-item types for each Sprint. Reads use the
// configured project list as-is and preserve query order when merging results.
func (s *Service) ReadRequirementCandidates(ctx context.Context, payload map[string]any) (RequirementCandidatesResult, error) {
	if s == nil {
		return RequirementCandidatesResult{}, errors.New("需求候选读取服务不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := s.configValue()
	if err != nil {
		return RequirementCandidatesResult{}, err
	}
	expected, provided, err := expectedRequirementIdentity(payload)
	if err != nil {
		return RequirementCandidatesResult{}, err
	}
	read := func(ctx context.Context) (RequirementCandidatesResult, error) {
		return s.readRequirementCandidates(ctx, config)
	}
	if !provided {
		return read(ctx)
	}
	if err := s.verifyExpectedRequirementIdentity(ctx, config, expected); err != nil {
		return RequirementCandidatesResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RequirementCandidatesResult{}, err
	}
	result, readErr := read(ctx)
	if process.IsCleanupFailure(readErr) {
		return RequirementCandidatesResult{}, readErr
	}
	if err := ctx.Err(); err != nil {
		return RequirementCandidatesResult{}, err
	}
	if err := s.verifyExpectedRequirementIdentity(ctx, config, expected); err != nil {
		return RequirementCandidatesResult{}, err
	}
	return result, readErr
}

func (s *Service) readRequirementCandidates(ctx context.Context, config domain.AppConfig) (RequirementCandidatesResult, error) {
	result := RequirementCandidatesResult{
		Candidates: []RequirementCandidate{},
		Failures:   []RequirementCandidateFailure{},
	}
	projects := append([]domain.MeegleProjectConfig(nil), config.MeegleProjects...)
	if len(projects) == 0 {
		return result, nil
	}

	sprintResults, err := orderedRequirementCandidateMap(ctx, projects, 4, func(ctx context.Context, project domain.MeegleProjectConfig) (requirementCandidateQueryResult, error) {
		return s.queryRequirementCandidateIDs(ctx, config, project, "Sprint", "`Status` = '进行中'", "Sprint", "")
	})
	if err != nil {
		return RequirementCandidatesResult{}, err
	}

	queries := make([]requirementCandidateSprintQuery, 0)
	for index, queryResult := range sprintResults {
		project := projects[index]
		if queryResult.failure != nil {
			result.Failures = append(result.Failures, *queryResult.failure)
			continue
		}
		result.CompletedQueries++
		for _, sprintID := range queryResult.itemIDs {
			for _, typ := range requirementCandidateWorkItemTypes {
				queries = append(queries, requirementCandidateSprintQuery{project: project, sprint: sprintID, typ: typ})
			}
		}
	}

	workItemResults, err := orderedRequirementCandidateMap(ctx, queries, 4, func(ctx context.Context, query requirementCandidateSprintQuery) (requirementCandidateQueryResult, error) {
		where := fmt.Sprintf("array_contains(`Sprint`, '<id:%s>') AND array_contains(all_participate_persons(), current_login_user())", query.sprint)
		return s.queryRequirementCandidateIDs(ctx, config, query.project, query.typ.mqlName, where, query.typ.mqlName, query.sprint)
	})
	if err != nil {
		return RequirementCandidatesResult{}, err
	}

	links := make([]requirementCandidateLink, 0)
	seenURLs := make(map[string]bool)
	for index, queryResult := range workItemResults {
		query := queries[index]
		if queryResult.failure != nil {
			result.Failures = append(result.Failures, *queryResult.failure)
			continue
		}
		result.CompletedQueries++
		for _, itemID := range queryResult.itemIDs {
			url := fmt.Sprintf("https://project.feishu.cn/%s/%s/detail/%s", query.project.SimpleName, query.typ.path, itemID)
			if seenURLs[url] {
				continue
			}
			seenURLs[url] = true
			links = append(links, requirementCandidateLink{url: url, projectKey: query.project.ProjectKey})
		}
	}

	titles, err := orderedRequirementCandidateMap(ctx, links, 4, func(ctx context.Context, link requirementCandidateLink) (string, error) {
		title, fetchErr := s.readRequirementCandidateTitle(ctx, config, link)
		if process.IsCleanupFailure(fetchErr) {
			return "", fetchErr
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if fetchErr != nil || strings.TrimSpace(title) == "" {
			return "未读取到需求标题", nil
		}
		return title, nil
	})
	if err != nil {
		return RequirementCandidatesResult{}, err
	}
	for index, link := range links {
		result.Candidates = append(result.Candidates, RequirementCandidate{Title: titles[index], URL: link.url, SourceID: "meegle"})
	}
	return result, nil
}

func (s *Service) queryRequirementCandidateIDs(
	ctx context.Context,
	config domain.AppConfig,
	project domain.MeegleProjectConfig,
	workItemType string,
	where string,
	failureType string,
	sprintID string,
) (requirementCandidateQueryResult, error) {
	mql := fmt.Sprintf("SELECT `Item Id` FROM `%s`.`%s` WHERE %s LIMIT 100", project.ProjectKey, workItemType, where)
	result, err := s.runMeegle(ctx, config, []string{
		"workitem", "query", "--project-key", project.ProjectKey, "--mql", mql, "--auto-paginate", "--format", "json",
	}, 20*time.Second, "Meegle MQL 查询")
	if err != nil {
		if process.IsCleanupFailure(err) {
			return requirementCandidateQueryResult{}, err
		}
		if ctx.Err() != nil {
			return requirementCandidateQueryResult{}, ctx.Err()
		}
		return requirementCandidateQueryResult{failure: &RequirementCandidateFailure{
			Stage: "query", ProjectKey: project.ProjectKey,
			SprintID: sprintID, WorkItemType: failureType, Message: requirementCandidateFailureMessage(err),
		}}, nil
	}
	ids, err := parseRequirementCandidateItemIDs(result.Stdout)
	if err != nil {
		return requirementCandidateQueryResult{failure: &RequirementCandidateFailure{
			Stage: "parse", ProjectKey: project.ProjectKey, SprintID: sprintID,
			WorkItemType: failureType, Message: requirementCandidateFailureMessage(err),
		}}, nil
	}
	return requirementCandidateQueryResult{itemIDs: ids}, nil
}

func parseRequirementCandidateItemIDs(stdout string) ([]string, error) {
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.UseNumber()
	value, err := readRequirementCandidateJSONValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("Meegle MQL 返回不是 JSON：%w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("Meegle MQL 返回包含多段 JSON")
		}
		return nil, fmt.Errorf("Meegle MQL 返回不是 JSON：%w", err)
	}
	ids := []string{}
	collectRequirementCandidateItemIDs(value, &ids)
	return ids, nil
}

func readRequirementCandidateJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := requirementCandidateJSONObject{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("JSON 对象字段名不是字符串")
			}
			value, err := readRequirementCandidateJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object = setRequirementCandidateJSONField(object, key, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("JSON 对象未正确结束")
		}
		return object, nil
	case '[':
		values := []any{}
		for decoder.More() {
			value, err := readRequirementCandidateJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("JSON 数组未正确结束")
		}
		return values, nil
	default:
		return nil, fmt.Errorf("JSON 定界符 %q 无效", delimiter)
	}
}

func setRequirementCandidateJSONField(object requirementCandidateJSONObject, name string, value any) requirementCandidateJSONObject {
	for index := range object {
		if object[index].name == name {
			object[index].value = value
			return object
		}
	}
	return append(object, requirementCandidateJSONField{name: name, value: value})
}

func collectRequirementCandidateItemIDs(value any, results *[]string) {
	switch current := value.(type) {
	case requirementCandidateJSONObject:
		if typedKey, ok := current.get("key"); ok && requirementCandidateJSONPrimitive(typedKey) {
			fieldName := normalizeFieldName(requirementCandidateJSONContent(typedKey))
			if fieldName == "itemid" || fieldName == "workitemid" {
				if fieldValue, exists := current.get("value"); exists {
					if id, ok := requirementCandidateItemIDValue(fieldValue); ok {
						*results = append(*results, id)
					}
				}
				return
			}
		}
		for _, field := range current {
			if normalizeFieldName(field.name) == "itemid" || normalizeFieldName(field.name) == "workitemid" {
				if id, ok := requirementCandidateItemIDValue(field.value); ok {
					*results = append(*results, id)
				}
			} else {
				collectRequirementCandidateItemIDs(field.value, results)
			}
		}
	case []any:
		for _, item := range current {
			collectRequirementCandidateItemIDs(item, results)
		}
	}
}

func requirementCandidateJSONPrimitive(value any) bool {
	switch value.(type) {
	case string, json.Number, bool, nil:
		return true
	default:
		return false
	}
}

func requirementCandidateJSONContent(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case json.Number:
		return current.String()
	case bool:
		return fmt.Sprint(current)
	case nil:
		return "null"
	default:
		return ""
	}
}

func requirementCandidateItemIDValue(value any) (string, bool) {
	switch current := value.(type) {
	case string:
		return current, strings.TrimSpace(current) != ""
	case json.Number:
		id := current.String()
		return id, strings.TrimSpace(id) != ""
	case bool, nil:
		return requirementCandidateJSONContent(current), true
	case requirementCandidateJSONObject:
		for _, key := range []string{"long_value", "string_value", "text_value"} {
			if nested, exists := current.get(key); exists {
				if id, ok := requirementCandidateItemIDValue(nested); ok {
					return id, true
				}
			}
		}
	}
	return "", false
}

func (s *Service) readRequirementCandidateTitle(ctx context.Context, config domain.AppConfig, link requirementCandidateLink) (string, error) {
	_, _, _, _, err := parseRemoteRequirementURL(link.url)
	if err != nil {
		return "", nil
	}
	value, err := s.requirementMetadata(ctx, config, map[string]any{"url": link.url, "projectKey": link.projectKey})
	if err != nil {
		if process.IsCleanupFailure(err) {
			return "", err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", nil
	}
	metadata, ok := value.(requirementMetadataResult)
	if !ok || metadata.Title == nil {
		return "", nil
	}
	return *metadata.Title, nil
}

func requirementCandidateFailureMessage(err error) string {
	var commandFailure *commandError
	message := err.Error()
	if errors.As(err, &commandFailure) && commandFailure.Message != "" {
		message = commandFailure.Message
	}
	runes := []rune(message)
	if len(runes) > 300 {
		return string(runes[:300])
	}
	return message
}

type requirementCandidateIndexedJob[T any] struct {
	index int
	value T
}

func orderedRequirementCandidateMap[T any, R any](ctx context.Context, values []T, maxConcurrent int, run func(context.Context, T) (R, error)) ([]R, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]R, len(values))
	if len(values) == 0 {
		return results, nil
	}
	workers := maxConcurrent
	if workers < 1 {
		workers = 1
	}
	if workers > len(values) {
		workers = len(values)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan requirementCandidateIndexedJob[T], len(values))
	for index, value := range values {
		jobs <- requirementCandidateIndexedJob[T]{index: index, value: value}
	}
	close(jobs)
	var failureMu sync.Mutex
	var failure error
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for job := range jobs {
				if workerCtx.Err() != nil {
					return
				}
				value, err := run(workerCtx, job.value)
				if err != nil {
					cancel()
					failureMu.Lock()
					// Worker cancellation must not replace the cleanup failure that caused it.
					if failure == nil || process.IsCleanupFailure(err) && !process.IsCleanupFailure(failure) {
						failure = err
					}
					failureMu.Unlock()
					return
				}
				results[job.index] = value
			}
		}()
	}
	wait.Wait()
	if failure != nil {
		return nil, failure
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
