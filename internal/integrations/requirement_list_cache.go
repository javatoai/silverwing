package integrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

const (
	participatedListCacheVersion = 1
	maxParticipatedListCache     = 16 << 20
	participatedListCacheTTL     = 14 * 24 * time.Hour
	participatedListCacheKind    = "participated-list-all-pages-v1"
)

type participatedListCacheEnvelope struct {
	Version   int                `json:"version"`
	Key       string             `json:"key"`
	FetchedAt int64              `json:"fetchedAt"`
	Value     participatedResult `json:"value"`
}

type participatedListCacheKeys struct {
	Identity        string
	ProjectsJSON    string
	RequestedSprint *string
	DefaultProject  *string
}

func (s *Service) loadParticipatedList(ctx context.Context, key string, force bool, load func(context.Context) (any, error)) (any, bool, error) {
	startedAt := requirementUnixMillis()
	if !force {
		cached, _, cacheErr := s.readRequirementListCache(key)
		if cacheErr == nil && cached != nil {
			return cached.Value, true, nil
		}
	}
	if load == nil {
		return nil, false, errors.New("需求列表读取未配置")
	}
	result, err := load(ctx)
	if err != nil {
		return nil, false, err
	}
	if typed, ok := result.(participatedResult); ok {
		typed.FetchedAt = startedAt
		return typed, false, nil
	}
	return result, false, nil
}

func (s *Service) readConfirmedRequirementListCache(ctx context.Context, expected, key string) (any, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	confirmed, err := s.confirmedRequirementIdentity(expected)
	if err != nil || !confirmed {
		return nil, false, err
	}
	cached, _, cacheErr := s.readRequirementListCache(key)
	if cacheErr != nil || cached == nil {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, err := s.confirmedRequirementIdentity(expected); err != nil {
		return nil, false, err
	}
	return cached.Value, true, nil
}

func participatedListCacheKeysFor(config domain.AppConfig, identity string, payload map[string]any) (participatedListCacheKeys, error) {
	projectsJSON, err := marshalRequirementJSON(config.MeegleProjects)
	if err != nil {
		return participatedListCacheKeys{}, err
	}
	keys := participatedListCacheKeys{
		Identity:       identity,
		ProjectsJSON:   string(projectsJSON),
		DefaultProject: config.MeegleDefaultSprintProjectKey,
	}
	if sprint := stringValue(payload, "sprintKey", "selectedSprintKey"); sprint != "" {
		keys.RequestedSprint = &sprint
	}
	return keys, nil
}

func (keys participatedListCacheKeys) key(sprint *string) (string, error) {
	encoded, err := marshalRequirementJSON([]any{
		participatedListCacheKind,
		keys.Identity,
		keys.ProjectsJSON,
		sprint,
		keys.DefaultProject,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (keys participatedListCacheKeys) requestedKey() (string, error) {
	return keys.key(keys.RequestedSprint)
}

func (s *Service) readRequirementListCache(key string) (*participatedListCacheEnvelope, string, error) {
	if s == nil || strings.TrimSpace(s.paths.Cache) == "" {
		return nil, "", errors.New("需求列表缓存目录不可用")
	}
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	path := filepath.Join(s.paths.Cache, "requirements", requirementCacheDigest(key)+".json")
	if err := requirementSafePath(s.paths.Cache, path); err != nil {
		return nil, path, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(path, info) || info.Size() > maxParticipatedListCache {
		if info.Mode().IsRegular() && !requirementCacheReparsePoint(path, info) {
			discardRequirementListCache(s.paths.Cache, path)
		}
		return nil, path, errors.New("需求列表缓存不是有效普通文件或超过大小限制")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		discardRequirementListCache(s.paths.Cache, path)
		return nil, path, err
	}
	var saved participatedListCacheEnvelope
	if err := strictRequirementJSON(data, &saved); err != nil {
		discardRequirementListCache(s.paths.Cache, path)
		return nil, path, fmt.Errorf("需求列表缓存无法读取：%w", err)
	}
	now := requirementUnixMillis()
	if saved.Version != participatedListCacheVersion || saved.Key != requirementCacheDigest(key) ||
		!participatedListCacheFresh(saved.FetchedAt, now) || saved.Value.Items == nil {
		discardRequirementListCache(s.paths.Cache, path)
		return nil, path, errors.New("需求列表缓存格式不匹配或已过期")
	}
	normalizeParticipatedResult(&saved.Value)
	saved.Value.FetchedAt = saved.FetchedAt
	return &saved, path, nil
}

func (s *Service) writeRequirementListCache(key string, result participatedResult) error {
	if s == nil || strings.TrimSpace(s.paths.Cache) == "" {
		return errors.New("需求列表缓存目录不可用")
	}
	if result.FetchedAt <= 0 {
		return errors.New("需求列表缓存缺少有效读取时间")
	}
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	normalizeParticipatedResult(&result)
	fetchedAt := result.FetchedAt
	result.FetchedAt = 0
	envelope := participatedListCacheEnvelope{
		Version:   participatedListCacheVersion,
		Key:       requirementCacheDigest(key),
		FetchedAt: fetchedAt,
		Value:     result,
	}
	data, err := marshalRequirementJSON(envelope)
	if err != nil {
		return err
	}
	if len(data) > maxParticipatedListCache {
		return errors.New("需求列表缓存超过大小限制")
	}
	path := filepath.Join(s.paths.Cache, "requirements", requirementCacheDigest(key)+".json")
	return atomicRequirementWrite(s.paths.Cache, path, data)
}

func (s *Service) persistParticipatedListCache(keys participatedListCacheKeys, result participatedResult, force bool) {
	if !requirementListResultCacheable(result) || result.FetchedAt <= 0 {
		return
	}
	requestedKey, err := keys.requestedKey()
	if err != nil {
		return
	}
	_ = s.writeRequirementListCache(requestedKey, result)

	if keys.RequestedSprint == nil && result.SelectedSprintKey != nil {
		resolvedKey, keyErr := keys.key(result.SelectedSprintKey)
		if keyErr == nil {
			_ = s.writeRequirementListCache(resolvedKey, result)
		}
		return
	}
	if !force || keys.RequestedSprint == nil || result.SelectedSprintKey == nil {
		return
	}
	defaultKey, err := keys.key(nil)
	if err != nil {
		return
	}
	previous, _, err := s.readRequirementListCache(defaultKey)
	if err == nil && previous != nil && previous.Value.SelectedSprintKey != nil &&
		*previous.Value.SelectedSprintKey == *result.SelectedSprintKey {
		_ = s.writeRequirementListCache(defaultKey, result)
	}
}

func requirementListResultCacheable(result participatedResult) bool {
	if len(result.Failures) > 0 {
		return false
	}
	for _, item := range result.Items {
		if len(item.MetadataWarnings) > 0 {
			return false
		}
	}
	return true
}

func participatedListCacheFresh(fetchedAt, now int64) bool {
	if fetchedAt < 0 || fetchedAt > now {
		return false
	}
	return now-fetchedAt < participatedListCacheTTL.Milliseconds()
}

func normalizeParticipatedResult(result *participatedResult) {
	if result.Items == nil {
		result.Items = []domain.ParticipatedWorkItem{}
	}
	if result.Failures == nil {
		result.Failures = []string{}
	}
	for index := range result.Items {
		item := &result.Items[index]
		if item.Developers == nil {
			item.Developers = []domain.RequirementPerson{}
		}
		if item.QCOwners == nil {
			item.QCOwners = []domain.RequirementPerson{}
		}
		if item.ProductManagers == nil {
			item.ProductManagers = []domain.RequirementPerson{}
		}
		if item.MetadataWarnings == nil {
			item.MetadataWarnings = []string{}
		}
	}
}

func discardRequirementListCache(root, path string) {
	if requirementSafePath(root, path) != nil {
		return
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() && !requirementCacheReparsePoint(path, info) {
		_ = os.Remove(path)
	}
}
