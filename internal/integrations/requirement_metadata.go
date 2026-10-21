package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type RequirementMetadataReadResult struct {
	ID              string                               `json:"id"`
	ProjectKey      string                               `json:"projectKey"`
	Type            string                               `json:"type"`
	Title           *string                              `json:"title"`
	Status          *string                              `json:"status"`
	Participants    domain.RequirementParticipants       `json:"participants"`
	Stale           bool                                 `json:"stale"`
	RefreshError    string                               `json:"refreshError,omitempty"`
	CacheError      string                               `json:"cacheError,omitempty"`
	EstimateError   string                               `json:"estimateError,omitempty"`
	FetchedAt       int64                                `json:"fetchedAt,omitempty"`
	WorkItem        *domain.ParticipatedWorkItem         `json:"workItem,omitempty"`
	SprintEstimates map[string]RequirementSprintEstimate `json:"sprintEstimates,omitempty"`
}

type requirementMetadataResult = RequirementMetadataReadResult

// requirementMetadata reads only the source provider's metadata work-item
// response. It deliberately does not call requirementBody or meta-fields.
func (s *Service) requirementMetadata(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	linkText := stringValue(payload, "url", "requirementLink", "link")
	space, kind, id, builtinProjectKey, err := parseRemoteRequirementURL(linkText)
	if err != nil {
		return nil, err
	}

	configuredProjectKey := ""
	for _, project := range config.MeegleProjects {
		if strings.EqualFold(project.SimpleName, space) {
			configuredProjectKey = project.ProjectKey
			break
		}
	}
	projectKey := strings.TrimSpace(stringValue(payload, "projectKey"))
	switch {
	case configuredProjectKey != "":
		if projectKey != "" && projectKey != configuredProjectKey {
			return nil, errors.New("需求项目 Key 与当前项目配置不一致")
		}
		projectKey = configuredProjectKey
	case builtinProjectKey != "":
		if projectKey != "" && projectKey != builtinProjectKey {
			return nil, errors.New("需求链接的项目 Key 与当前项目配置不一致")
		}
		projectKey = builtinProjectKey
	}
	if !validMeegleProjectKey.MatchString(projectKey) {
		return nil, fmt.Errorf("需求空间 %s 未配置有效的 Meegle project key", space)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expected, _, err := expectedRequirementIdentity(payload)
	if err != nil {
		return nil, err
	}
	force := boolValue(payload, "force")
	session, loggedOut, identityErr := s.requirementIdentityFor(ctx, config, expected, force, s.requirementCacheIdentity)
	if process.IsCleanupFailure(identityErr) {
		return nil, identityErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if identityErr != nil {
		var changed *RequirementIdentityChangedError
		if errors.As(identityErr, &changed) {
			return nil, identityErr
		}
	}
	if err := expectedRequirementDocumentIdentity(payload, session.raw, loggedOut, identityErr); err != nil {
		return nil, err
	}
	if loggedOut {
		return nil, errors.New("Meegle CLI 当前未登录")
	}
	if force && (identityErr != nil || session.raw == "") {
		return nil, errors.New("无法确认当前 Meegle 账户，不能安全刷新需求元数据")
	}
	item := domain.ParticipatedWorkItem{ProjectKey: projectKey, ProjectName: space, Type: kind, ID: id, URL: linkText}
	key := requirementCacheKey("metadata", session.raw, item)
	var saved *requirementMetadataEnvelope
	if session.raw != "" {
		var cacheErr error
		saved, _, cacheErr = s.readRequirementMetadataCache(key)
		if cacheErr != nil {
			saved = nil
		}
		if !force && saved != nil {
			if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
				return nil, err
			}
			saved.Data.FetchedAt = saved.FetchedAt
			return saved.Data, nil
		}
	}
	generation, err := s.beginRequirementRead(key, force)
	if err != nil {
		return nil, err
	}
	defer s.finishRequirementRead(key, generation)
	startedAt := requirementUnixMillis()
	result, fetchErr := s.fetchRequirementMetadata(ctx, config, projectKey, kind, id)
	if process.IsCleanupFailure(fetchErr) {
		return nil, fetchErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if fetchErr != nil {
		if saved == nil {
			return nil, fetchErr
		}
		if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
			return nil, err
		}
		saved.Data.Stale = true
		saved.Data.RefreshError = fetchErr.Error()
		return saved.Data, nil
	}
	if session.raw == "" {
		result.CacheError = "无法确认当前账户，需求元数据尚未持久缓存"
		return result, nil
	}
	result.FetchedAt = startedAt
	if sprintKey := stringValue(payload, "sprintKey"); force && boolValue(payload, "fullRefresh") && sprintKey != "" {
		days, estimateErr := s.refreshRequirementEstimate(ctx, config, item, sprintKey)
		if estimateErr != nil {
			result.EstimateError = estimateErr.Error()
		} else {
			result.SprintEstimates = map[string]RequirementSprintEstimate{sprintKey: {Days: days, FetchedAt: startedAt}}
		}
	}
	if boolValue(payload, "fullRefresh") {
		if err := s.verifyRequirementRefresh(ctx, config, session.raw); err != nil {
			return nil, err
		}
	}
	if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
		return nil, err
	}
	writeErr := s.commitRequirementRead(ctx, config, key, generation, func() error {
		// A refresh in another Sprint may have saved its estimate while this
		// request ran. Merge scoped values without replacing unrelated Sprints.
		if previous, _, readErr := s.readRequirementMetadataCache(key); readErr == nil && previous != nil {
			if result.SprintEstimates == nil {
				result.SprintEstimates = make(map[string]RequirementSprintEstimate)
			}
			for sprint, value := range previous.Data.SprintEstimates {
				if own, exists := result.SprintEstimates[sprint]; !exists || own.FetchedAt < value.FetchedAt {
					result.SprintEstimates[sprint] = value
				}
			}
		}
		_, err := s.writeRequirementMetadataCache(key, result, startedAt)
		return err
	})
	if writeErr != nil {
		if errors.Is(writeErr, errRequirementReadSuperseded) || ctx.Err() != nil {
			return nil, writeErr
		}
		result.CacheError = "需求元数据缓存失败：" + writeErr.Error()
	}
	return result, nil
}

func (s *Service) fetchRequirementMetadata(ctx context.Context, config domain.AppConfig, projectKey, kind, id string) (RequirementMetadataReadResult, error) {
	result, err := s.runMeegle(ctx, config, []string{
		"workitem", "get", "--project-key", projectKey, "--work-item-id", id, "--format", "json",
	}, 8*time.Second, "读取需求元数据")
	if err != nil {
		if process.IsCleanupFailure(err) {
			return RequirementMetadataReadResult{}, err
		}
		// runCLI formats subprocess failures for display; preserve caller
		// cancellation as a real cancellation at the service boundary.
		if ctx.Err() != nil {
			return RequirementMetadataReadResult{}, ctx.Err()
		}
		return RequirementMetadataReadResult{}, err
	}
	rootValue, err := decodeJSON(result.Stdout)
	if err != nil {
		return RequirementMetadataReadResult{}, errors.New("读取需求元数据返回的 JSON 无法解析")
	}
	root := object(rootValue)
	if root == nil {
		return RequirementMetadataReadResult{}, errors.New("Meegle 返回不是 JSON 对象")
	}

	attributes := object(root["work_item_attribute"])
	title := requirementMetadataTitle(root, attributes)
	status := nonblank(requirementMetadataText(at(attributes, "work_item_status", "name")))
	participants := requirementMetadataParticipants(kind, attributes["role_members"])
	if attributes == nil && title == nil && status == nil && participants.IsEmpty() {
		return RequirementMetadataReadResult{}, errors.New("Meegle 未返回可用需求元数据")
	}
	return RequirementMetadataReadResult{
		ID: id, ProjectKey: projectKey, Type: kind, Title: title, Status: status, Participants: participants,
		WorkItem: metadataWorkItem(projectKey, kind, id, title, status, attributes["role_members"]),
	}, nil
}

// The metadata provider consumes work_item_attribute.role_members, whose shape
// is distinct from the MQL moql_field_list consumed by roleMembers. Keep the
// source provider's exact role names, member order, nullable email, and
// duplicate behavior instead of coercing detail data into a different schema.
func requirementMetadataParticipants(kind string, value any) domain.RequirementParticipants {
	participants := domain.RequirementParticipants{
		QCOwners:        []domain.RequirementPerson{},
		ProductManagers: []domain.RequirementPerson{},
	}
	switch kind {
	case "userstory":
		participants.QCOwners = requirementMetadataMembers(value, "QC Owner")
		participants.ProductManagers = requirementMetadataMembers(value, "产品经理")
	case "technical", "bug":
		participants.QCOwners = requirementMetadataMembers(value, "QC Owner")
	}
	return participants
}

func requirementMetadataMembers(roles any, roleName string) []domain.RequirementPerson {
	people := []domain.RequirementPerson{}
	for _, rawRole := range array(roles) {
		role := object(rawRole)
		name, ok := requirementMetadataPrimitiveContent(role["name"])
		if !ok || name != roleName {
			continue
		}
		for _, rawMember := range array(role["members"]) {
			member := object(rawMember)
			personName := requirementMetadataText(member["name"])
			if personName == "" {
				continue
			}
			people = append(people, domain.RequirementPerson{Name: personName, Email: nonblank(requirementMetadataText(member["email"]))})
		}
	}
	return people
}

func requirementMetadataTitle(root, attributes map[string]any) *string {
	for _, value := range []any{root["name"], root["work_item_name"], attributes["name"], attributes["work_item_name"]} {
		if title := requirementMetadataText(value); title != "" {
			return &title
		}
	}
	return nil
}

// Kotlin's metadata provider accepts only JSON primitives for title, status,
// role names, member names, and email. Do not recursively extract nested maps.
func requirementMetadataText(value any) string {
	content, ok := requirementMetadataPrimitiveContent(value)
	if !ok {
		return ""
	}
	return strings.TrimSpace(content)
}

func requirementMetadataPrimitiveContent(value any) (string, bool) {
	switch primitive := value.(type) {
	case string:
		return primitive, true
	case json.Number:
		return primitive.String(), true
	case bool:
		return strconv.FormatBool(primitive), true
	default:
		return "", false
	}
}
