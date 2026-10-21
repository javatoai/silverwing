package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
)

// Network reads never hold the lock. Only a still-current read may commit;
// ordinary cache misses cannot supersede an explicit refresh already running.
type requirementReadOrder struct {
	mu    sync.Mutex
	next  uint64
	reads map[string]requirementReadTicket
}
type requirementReadTicket struct {
	generation uint64
	force      bool
	active     bool
}

var errRequirementReadSuperseded = errors.New("需求读取已被较新的刷新或配置替代")

func (s *Service) beginRequirementRead(key string, force bool) (uint64, error) {
	o := &s.requirementReads
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.reads == nil {
		o.reads = make(map[string]requirementReadTicket)
	}
	if r := o.reads[key]; r.active && r.force && !force {
		return 0, errors.New("需求正在刷新，请稍后重试")
	}
	o.next++
	o.reads[key] = requirementReadTicket{o.next, force, true}
	return o.next, nil
}

func (s *Service) finishRequirementRead(key string, generation uint64) {
	o := &s.requirementReads
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.reads[key]
	if r.generation == generation {
		r.active = false
		o.reads[key] = r
	}
}

func requirementReadConfig(config domain.AppConfig) string {
	data, _ := json.Marshal([]any{config.MeegleExecutablePath, config.MeegleProjects, config.MeegleDefaultSprintProjectKey,
		config.CommandProxyURL, config.CommandProxyNoProxy, config.CommandProxyUsername, config.CommandProxyPassword, config.CommandProxyTargets})
	return string(data)
}

func (s *Service) commitRequirementRead(ctx context.Context, config domain.AppConfig, key string, generation uint64, save func() error) error {
	o := &s.requirementReads
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.reads[key].generation != generation {
		return errRequirementReadSuperseded
	}
	if err := s.verifyRequirementReadConfig(config); err != nil {
		return err
	}
	return save()
}

func (s *Service) verifyRequirementReadConfig(config domain.AppConfig) error {
	current, err := s.config.Load()
	if err != nil {
		return err
	}
	if requirementReadConfig(current) != requirementReadConfig(config) {
		return errRequirementReadSuperseded
	}
	return nil
}

// Explicit refreshes recheck the CLI account after the network work, before
// persisting. A cached session alone cannot detect an external account switch.
func (s *Service) verifyRequirementRefresh(ctx context.Context, config domain.AppConfig, identity string) error {
	if err := s.verifyRequirementReadConfig(config); err != nil {
		return err
	}
	return s.verifyRequirementIdentity(ctx, config, identity)
}

// Optional fields extend the v1 metadata cache. Common fields are shared across
// Sprints; personal estimates are explicitly scoped and keep their own age.
type RequirementSprintEstimate struct {
	Days      *string `json:"days"`
	FetchedAt int64   `json:"fetchedAt"`
}

func (s *Service) refreshRequirementEstimate(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem, sprintKey string) (*string, error) {
	projectKey, sprintID, ok := strings.Cut(sprintKey, ":")
	if !ok || projectKey != item.ProjectKey || !numericIDPattern.MatchString(sprintID) {
		return nil, errors.New("Sprint 与需求项目不匹配")
	}
	var project domain.MeegleProjectConfig
	for _, candidate := range config.MeegleProjects {
		if candidate.ProjectKey == projectKey {
			project = candidate
			break
		}
	}
	if project.ProjectKey == "" {
		return nil, errors.New("Sprint 项目配置已变化")
	}
	user, err := s.runMeegle(ctx, config, []string{"user", "me", "--format", "json"}, 8*time.Second, "读取当前登录人")
	if err != nil {
		return nil, err
	}
	root, err := decodeJSON(user.Stdout)
	if err != nil {
		return nil, err
	}
	userKey := text(at(root, "user_key"))
	if userKey == "" {
		userKey = text(at(root, "user", "user_key"))
	}
	if userKey == "" {
		return nil, errors.New("当前登录人缺少 user_key")
	}
	dateRange, err := s.querySprintDateRange(ctx, config, project, sprintID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.readPages(ctx, config, []string{"workflow", "get-node", "--project-key", item.ProjectKey, "--work-item-id", item.ID, "--node-id-list", "_all", "--field-key-list", "_all", "--need-sub-task", "true"}, 20*time.Second, "读取工作项排期明细")
	if err != nil {
		return nil, err
	}
	value, err := personalEstimate(nodes, userKey, *dateRange)
	if err != nil {
		return nil, err
	}
	formatted := value.String()
	return &formatted, nil
}

func metadataWorkItem(projectKey, kind, id string, title, status *string, roles any) *domain.ParticipatedWorkItem {
	item := &domain.ParticipatedWorkItem{ProjectKey: projectKey, Type: kind, ID: id, Status: status}
	if title != nil {
		item.Title = *title
	}
	if index := workItemTypeIndex(kind); index >= 0 {
		typ := workItemTypes[index]
		item.TypeLabel = typ.Label
		for _, role := range typ.DeveloperRoles {
			item.Developers = append(item.Developers, requirementMetadataMembers(roles, role)...)
		}
		for _, role := range typ.QCRoles {
			item.QCOwners = append(item.QCOwners, requirementMetadataMembers(roles, role)...)
		}
		for _, role := range typ.ProductRoles {
			item.ProductManagers = append(item.ProductManagers, requirementMetadataMembers(roles, role)...)
		}
	}
	return item
}

// Called only from background list reads, never from UI rendering. Comparing
// request start times also protects against an older list completing late.
func (s *Service) overlayRequirementList(identity string, result participatedResult) participatedResult {
	if identity == "" {
		return result
	}
	result.Items = append([]domain.ParticipatedWorkItem(nil), result.Items...)
	for i := range result.Items {
		item := &result.Items[i]
		cached, _, err := s.readRequirementMetadataCache(requirementCacheKey("metadata", identity, *item))
		if err != nil || cached == nil {
			continue
		}
		metadata := cached.Data
		if metadata.WorkItem != nil && cached.FetchedAt >= result.FetchedAt {
			fresh := metadata.WorkItem
			if metadata.Title != nil {
				item.Title = *metadata.Title
			}
			item.Status, item.Developers, item.QCOwners, item.ProductManagers = fresh.Status, fresh.Developers, fresh.QCOwners, fresh.ProductManagers
		}
		if result.SelectedSprintKey != nil {
			if estimate, ok := metadata.SprintEstimates[*result.SelectedSprintKey]; ok && estimate.FetchedAt >= result.FetchedAt {
				item.MySprintEstimateDays = estimate.Days
			}
		}
	}
	return result
}

func (s *Service) requirementOverlayIdentity(token string) string {
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	if s.identityConfirmed && s.confirmedIdentity == token {
		return s.confirmedIdentityRaw
	}
	for _, session := range s.identitySessions {
		if session.token == token && session.state == requirementIdentityAuthenticated {
			return session.raw
		}
	}
	return ""
}
