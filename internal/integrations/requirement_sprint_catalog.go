package integrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const requirementSprintCatalogVersion = 1

// A catalog result exists independently of the work items for any one Sprint.
// readStartedAt is completeness evidence produced only by this process's live,
// all-project query; list envelopes can never supply it after deserialization.
type requirementSprintCatalogResult struct {
	Sprints       []sprintItem `json:"sprints"`
	FetchedAt     int64        `json:"fetchedAt"`
	readStartedAt int64
}

type requirementSprintCatalogEnvelope struct {
	Version       int          `json:"version"`
	Key           string       `json:"key"`
	FetchedAt     int64        `json:"fetchedAt"`
	ReadStartedAt int64        `json:"readStartedAt"`
	Sprints       []sprintItem `json:"sprints"`
}

var requirementSprintCatalogReadOrder atomic.Int64

// Millisecond DTO timestamps can tie. Persist a stricter start order so an
// older background read cannot overwrite a newer refresh when it finishes late.
func nextRequirementSprintCatalogRead() int64 {
	for {
		previous := requirementSprintCatalogReadOrder.Load()
		next := time.Now().UnixNano()
		if next <= previous {
			next = previous + 1
		}
		if requirementSprintCatalogReadOrder.CompareAndSwap(previous, next) {
			return next
		}
	}
}

func (keys participatedListCacheKeys) sprintCatalogKey() (string, error) {
	encoded, err := marshalRequirementJSON([]any{
		"participated-sprint-catalog-all-projects-v1", keys.Identity, keys.ProjectsJSON, keys.DefaultProject,
	})
	return string(encoded), err
}

func requirementSprintProjects(config domain.AppConfig) []domain.MeegleProjectConfig {
	unique := make([]domain.MeegleProjectConfig, 0, len(config.MeegleProjects))
	seen := map[string]bool{}
	for _, project := range config.MeegleProjects {
		if !seen[project.ProjectKey] {
			unique = append(unique, project)
			seen[project.ProjectKey] = true
		}
	}
	return unique
}

func selectDefaultRequirementSprint(sprints []sprintItem, defaultProject *string) *sprintItem {
	var ongoing, projectOngoing []*sprintItem
	for i := range sprints {
		if sprints[i].Status != "进行中" {
			continue
		}
		ongoing = append(ongoing, &sprints[i])
		if defaultProject != nil && sprints[i].ProjectKey == *defaultProject {
			projectOngoing = append(projectOngoing, &sprints[i])
		}
	}
	if len(ongoing) == 1 {
		return ongoing[0]
	}
	if len(projectOngoing) == 1 {
		return projectOngoing[0]
	}
	return nil
}

func (s *Service) queryRequirementSprintCatalog(ctx context.Context, config domain.AppConfig, startedAt int64, progress *requirementListReporter) (requirementSprintCatalogResult, []string, error) {
	projects := requirementSprintProjects(config)
	catalog := requirementSprintCatalogResult{Sprints: make([]sprintItem, 0)}
	failures := make([]string, 0)
	for index, project := range projects {
		if err := ctx.Err(); err != nil {
			return catalog, failures, err
		}
		progress.update("sprints", "running", "正在读取 "+project.SimpleName+" 的 Sprint…", index, len(projects))
		items, err := s.querySprints(ctx, config, project)
		if process.IsCleanupFailure(err) {
			return catalog, failures, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return catalog, failures, ctxErr
		}
		if err != nil {
			failures = append(failures, project.SimpleName+" · Sprint："+err.Error())
			continue
		}
		catalog.Sprints = append(catalog.Sprints, items...)
	}
	status, message := "done", "Sprint 读取完成"
	if len(failures) > 0 {
		status, message = "failed", strings.Join(failures, "；")
	} else {
		catalog.readStartedAt = startedAt
		catalog.FetchedAt = startedAt / int64(time.Millisecond)
	}
	progress.update("sprints", status, message, len(projects), len(projects))
	sort.SliceStable(catalog.Sprints, func(i, j int) bool {
		return catalog.Sprints[i].Status != "进行中" && catalog.Sprints[j].Status == "进行中"
	})
	if len(failures) == len(projects) && len(projects) > 0 {
		catalog.Sprints = nil
	}
	return catalog, failures, nil
}

func (s *Service) refreshRequirementSprints(ctx context.Context, config domain.AppConfig, payload map[string]any) (any, error) {
	expected, provided, err := expectedRequirementIdentity(payload)
	if err != nil {
		return nil, err
	}
	if !provided {
		return nil, errors.New("请先确认当前 Meegle 账户后读取 Sprint 目录")
	}
	keys, err := participatedListCacheKeysFor(config, expected, nil)
	if err != nil {
		return nil, err
	}
	startedAt := nextRequirementSprintCatalogRead()
	value, generation, err := s.executeRequirementSprintRead(ctx, config, expected, func(ctx context.Context) (any, error) {
		catalog, failures, queryErr := s.queryRequirementSprintCatalog(ctx, config, startedAt, nil)
		if queryErr != nil {
			return nil, queryErr
		}
		if len(failures) > 0 {
			return nil, fmt.Errorf("Sprint 目录读取未完成：%s", strings.Join(failures, "；"))
		}
		return catalog, nil
	})
	if err != nil {
		return nil, err
	}
	catalog := value.(requirementSprintCatalogResult)
	if err := s.persistRequirementSprintCatalog(ctx, config, keys, generation, catalog); err != nil {
		return nil, err
	}
	// A later refresh may already have committed while this request was running.
	key, _ := keys.sprintCatalogKey()
	if saved, readErr := s.readRequirementSprintCatalogCache(key); readErr == nil && saved != nil && saved.ReadStartedAt > catalog.readStartedAt {
		catalog = requirementSprintCatalogResult{Sprints: saved.Sprints, FetchedAt: saved.FetchedAt, readStartedAt: saved.ReadStartedAt}
	}
	if err := s.withRequirementSprintReadIdentity(ctx, config, expected, generation, nil); err != nil {
		return nil, err
	}
	return catalog, nil
}

// Keep the existing live identity checks before and after each query, and bind
// persistence to the same identity epoch, even if an account switches away and
// back to the original opaque token while the query is in flight.
func (s *Service) executeRequirementSprintRead(ctx context.Context, config domain.AppConfig, expected string, read requirementListExecutor) (any, uint64, error) {
	var generation uint64
	value, err := executeRequirementListWithIdentity(ctx, expected, func(ctx context.Context, token string) error {
		if err := s.verifyExpectedRequirementIdentity(ctx, config, token); err != nil {
			return err
		}
		s.identityStateMu.Lock()
		defer s.identityStateMu.Unlock()
		if !s.identityConfirmed || s.identityState != requirementIdentityAuthenticated || s.confirmedIdentity != expected ||
			s.identitySessionGeneration == 0 || generation != 0 && generation != s.identitySessionGeneration {
			return &RequirementIdentityChangedError{State: s.identityState, Identity: s.confirmedIdentity}
		}
		generation = s.identitySessionGeneration
		return nil
	}, read)
	return value, generation, err
}

func (s *Service) withRequirementSprintReadIdentity(ctx context.Context, config domain.AppConfig, expected string, generation uint64, save func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.verifyRequirementReadConfig(config); err != nil {
		return err
	}
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.identityClosed || !s.identityConfirmed || s.identityState != requirementIdentityAuthenticated ||
		s.confirmedIdentity != expected || generation == 0 || s.identitySessionGeneration != generation {
		return &RequirementIdentityChangedError{State: s.identityState, Identity: s.confirmedIdentity}
	}
	if save != nil {
		return save()
	}
	return nil
}

func (s *Service) persistRequirementSprintCatalog(ctx context.Context, config domain.AppConfig, keys participatedListCacheKeys, generation uint64, catalog requirementSprintCatalogResult) error {
	if catalog.readStartedAt <= 0 || catalog.FetchedAt <= 0 || catalog.Sprints == nil {
		return nil
	}
	key, err := keys.sprintCatalogKey()
	if err != nil {
		return err
	}
	return s.withRequirementSprintReadIdentity(ctx, config, keys.Identity, generation, func() error {
		return s.writeRequirementSprintCatalogCache(ctx, key, catalog)
	})
}

func (s *Service) readRequirementSprintCatalogCache(key string) (*requirementSprintCatalogEnvelope, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	return s.readRequirementSprintCatalogCacheUnlocked(key)
}

func (s *Service) readRequirementSprintCatalogCacheUnlocked(key string) (*requirementSprintCatalogEnvelope, error) {
	if s == nil || strings.TrimSpace(s.paths.Cache) == "" {
		return nil, errors.New("Sprint 目录缓存不可用")
	}
	path := filepath.Join(s.paths.Cache, "requirement-sprint-catalogs", requirementCacheDigest(key)+".json")
	if err := requirementSafePath(s.paths.Cache, path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(path, info) || info.Size() > maxParticipatedListCache {
		return nil, errors.New("Sprint 目录缓存不是有效普通文件或超过大小限制")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var saved requirementSprintCatalogEnvelope
	if err := strictRequirementJSON(data, &saved); err != nil {
		return nil, fmt.Errorf("Sprint 目录缓存无法读取：%w", err)
	}
	if saved.Version != requirementSprintCatalogVersion || saved.Key != requirementCacheDigest(key) || saved.Sprints == nil ||
		saved.ReadStartedAt <= 0 || saved.FetchedAt <= 0 || saved.ReadStartedAt/int64(time.Millisecond) != saved.FetchedAt ||
		!participatedListCacheFresh(saved.FetchedAt, requirementUnixMillis()) {
		return nil, errors.New("Sprint 目录缓存格式不匹配或已过期")
	}
	return &saved, nil
}

func (s *Service) writeRequirementSprintCatalogCache(ctx context.Context, key string, catalog requirementSprintCatalogResult) error {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || strings.TrimSpace(s.paths.Cache) == "" || catalog.Sprints == nil || catalog.FetchedAt <= 0 ||
		catalog.readStartedAt <= 0 || catalog.readStartedAt/int64(time.Millisecond) != catalog.FetchedAt {
		return errors.New("Sprint 目录缓存缺少完整读取结果")
	}
	previous, err := s.readRequirementSprintCatalogCacheUnlocked(key)
	if err == nil && previous != nil && previous.ReadStartedAt >= catalog.readStartedAt {
		return nil
	}
	envelope := requirementSprintCatalogEnvelope{
		Version: requirementSprintCatalogVersion, Key: requirementCacheDigest(key),
		FetchedAt: catalog.FetchedAt, ReadStartedAt: catalog.readStartedAt, Sprints: catalog.Sprints,
	}
	data, err := marshalRequirementJSON(envelope)
	if err != nil {
		return err
	}
	if len(data) > maxParticipatedListCache {
		return errors.New("Sprint 目录缓存超过大小限制")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join(s.paths.Cache, "requirement-sprint-catalogs", requirementCacheDigest(key)+".json")
	return atomicRequirementWrite(s.paths.Cache, path, data)
}

func (s *Service) overlayRequirementSprintCatalog(keys participatedListCacheKeys, result participatedResult) participatedResult {
	key, err := keys.sprintCatalogKey()
	if err != nil {
		return result
	}
	catalog, err := s.readRequirementSprintCatalogCache(key)
	if err != nil || catalog == nil {
		return result
	}
	if !result.FromCache && result.sprintCatalogReadStartedAt > catalog.ReadStartedAt {
		return result
	}
	result.Sprints = catalog.Sprints
	result.SprintsFetchedAt = catalog.FetchedAt
	if !result.FromCache && result.sprintCatalogReadStartedAt == 0 {
		// Old authoritative choices can help a partial live result, but a positive
		// live timestamp must certify this request completed all project queries.
		result.SprintsFetchedAt = 0
	}
	if result.SelectedSprintKey == nil {
		return result
	}
	for _, sprint := range catalog.Sprints {
		if sprint.Key() == *result.SelectedSprintKey {
			return result
		}
	}
	// Never relabel cached work items when their Sprint has disappeared. The
	// desktop can choose a valid Sprint and explicitly read that bound list.
	result.SelectedSprintKey = nil
	result.Items = []domain.ParticipatedWorkItem{}
	result.CompletedQueries = 0
	result.Failures = append(result.Failures, "所选 Sprint 已不可选，请重新选择")
	return result
}
