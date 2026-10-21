package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"silverwing-go/internal/domain"
)

type requirementNamingKey struct{ identity, model, context string }

type requirementNamingRequest struct {
	force     bool
	forbidden string
	cancel    context.CancelFunc
	done      chan struct{}
	result    domain.RequirementAiNamingSuggestion
	err       error
}

// A foreground form and the list prewarmer subscribe to the same request.
// Cancelling a subscriber leaves the shared generation available to others.
type requirementNamingCoordinator struct {
	mu         sync.Mutex
	requests   map[requirementNamingKey]*requirementNamingRequest
	slots      chan struct{}
	background chan struct{}
	closed     bool
}

func (s *Service) requirementNamingCoordinator() *requirementNamingCoordinator {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.naming == nil {
		s.naming = &requirementNamingCoordinator{requests: make(map[requirementNamingKey]*requirementNamingRequest),
			slots: make(chan struct{}, 2), background: make(chan struct{}, 1)}
	}
	return s.naming
}

func (s *Service) requirementNamingContext(config domain.AppConfig, account string) string {
	encoded, _ := json.Marshal([]any{s.paths.Home, config.MeegleExecutablePath, config.MeegleProjects,
		config.CodexExecutablePath, config.AIRequirementNamingModel, config.AIRequirementNamingEnabled,
		config.CommandProxyURL, config.CommandProxyTargets, config.CommandProxyNoProxy,
		config.CommandProxyUsername, config.CommandProxyPassword, account})
	return materialsHash(string(encoded))
}

func (c *requirementNamingCoordinator) suggest(ctx context.Context, key requirementNamingKey, force bool, forbidden []string,
	background bool, cached func() *domain.RequirementAiNamingSuggestion,
	generate func(context.Context) (domain.RequirementAiNamingSuggestion, error)) (domain.RequirementAiNamingSuggestion, error) {
	forbidden = append([]string(nil), forbidden...)
	sort.Strings(forbidden)
	encoded, _ := json.Marshal(forbidden)
	forbiddenKey := string(encoded)
	for {
		if err := ctx.Err(); err != nil {
			return domain.RequirementAiNamingSuggestion{}, err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return domain.RequirementAiNamingSuggestion{}, errors.New("需求命名服务正在关闭")
		}
		request := c.requests[key]
		if request == nil && !force {
			if value := cached(); value != nil {
				c.mu.Unlock()
				return *value, nil
			}
		}
		if request == nil {
			shared, cancel := context.WithCancel(context.Background())
			request = &requirementNamingRequest{force: force, forbidden: forbiddenKey, cancel: cancel, done: make(chan struct{})}
			c.requests[key] = request
			go c.run(shared, key, request, background, cached, generate)
		}
		compatible := !force || request.force && request.forbidden == forbiddenKey
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return domain.RequirementAiNamingSuggestion{}, ctx.Err()
		case <-request.done:
			if err := ctx.Err(); err != nil {
				return domain.RequirementAiNamingSuggestion{}, err
			}
			if compatible {
				return request.result, request.err
			}
			// Refreshes with different collision histories wait, then request their
			// own result rather than consuming another caller's suggestion.
		}
	}
}

func requirementNamingPermit(ctx context.Context, slots chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-slots
			return err
		}
		return nil
	}
}

func (c *requirementNamingCoordinator) run(ctx context.Context, key requirementNamingKey, request *requirementNamingRequest,
	background bool, cached func() *domain.RequirementAiNamingSuggestion,
	generate func(context.Context) (domain.RequirementAiNamingSuggestion, error)) {
	defer func() {
		request.cancel()
		c.mu.Lock()
		if c.requests[key] == request {
			delete(c.requests, key)
		}
		close(request.done)
		c.mu.Unlock()
	}()
	if background {
		if request.err = requirementNamingPermit(ctx, c.background); request.err != nil {
			return
		}
		defer func() { <-c.background }()
	}
	if request.err = requirementNamingPermit(ctx, c.slots); request.err != nil {
		return
	}
	defer func() { <-c.slots }()
	if !request.force {
		if value := cached(); value != nil {
			request.result = *value
			return
		}
	}
	request.result, request.err = generate(ctx)
	if ctx.Err() != nil {
		request.err = ctx.Err()
	}
}

func (s *Service) generateRequirementNaming(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem,
	identity, model string, forbidden []string, force bool, account string) (domain.RequirementAiNamingSuggestion, error) {
	if account != "" {
		if err := s.verifyRequirementIdentity(ctx, config, account); err != nil {
			return domain.RequirementAiNamingSuggestion{}, err
		}
	}
	// Naming and document preparation consume the same body cache and refresh
	// route. A cached body therefore does not require another work-item fetch.
	content, err := s.readRequirement(ctx, config, map[string]any{"url": item.URL, "projectKey": item.ProjectKey,
		"projectName": item.ProjectName, "type": item.Type, "id": item.ID, "title": item.Title, "force": false})
	if err != nil {
		return domain.RequirementAiNamingSuggestion{}, err
	}
	data := content.(RequirementReadResult)
	title := data.Title
	if title == "" {
		title = item.Title
	}
	if title == "" {
		return domain.RequirementAiNamingSuggestion{}, errors.New("需求缺少标题")
	}
	value, err := s.suggestRequirementName(ctx, config, requirementAIContext{title: title,
		body: truncateRunes(normalizeBody(data.Body), 50)}, forbidden, model)
	if err != nil {
		return domain.RequirementAiNamingSuggestion{}, err
	}
	suggestion := value.(domain.RequirementAiNamingSuggestion)
	if account != "" {
		if err := s.verifyRequirementIdentity(ctx, config, account); err != nil {
			return domain.RequirementAiNamingSuggestion{}, err
		}
	}
	current, loadErr := s.config.Load()
	if ctx.Err() == nil && loadErr == nil && s.requirementNamingContext(current, "") == s.requirementNamingContext(config, "") {
		// Fresh suggestions, including explicit regeneration, replace the old
		// cache entry just as the source's shared coordinator does.
		s.writeRequirementAINamingCache(identity, model, suggestion)
	}
	return suggestion, nil
}

// CancelRequirementNaming retires shared requests when the desktop's account
// or configuration scope changes. New requests remain available afterwards.
func (s *Service) CancelRequirementNaming() {
	c := s.requirementNamingCoordinator()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, request := range c.requests {
		request.cancel()
	}
}

func (s *Service) CloseRequirementNaming(ctx context.Context) error {
	c := s.requirementNamingCoordinator()
	c.mu.Lock()
	c.closed = true
	requests := make([]*requirementNamingRequest, 0, len(c.requests))
	for _, request := range c.requests {
		request.cancel()
		requests = append(requests, request)
	}
	c.mu.Unlock()
	for _, request := range requests {
		select {
		case <-request.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
