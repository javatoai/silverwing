package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type requirementNamingPreparationScope struct {
	identity         requirementListIdentity
	profile          requirementIdentityContext
	model, codex     string
	enabled, prewarm bool
}

type requirementNamingPreparationState struct {
	queue        *requirementNamingPreparationQueue
	scope        requirementNamingPreparationScope
	initialized  bool
	failuresOpen bool
}

func (s *app) currentRequirementNamingPreparationScope() requirementNamingPreparationScope {
	return requirementNamingPreparationScope{identity: s.currentRequirementListIdentity(), profile: s.currentRequirementIdentityContext(),
		model: s.configString("aiRequirementNamingModel"), codex: s.configString("codexExecutablePath"),
		enabled: s.configBool("aiRequirementNamingEnabled", true), prewarm: s.configBool("aiRequirementNamingPrewarmEnabled", true)}
}

func (s *app) ensureRequirementNamingPreparation() *requirementNamingPreparationState {
	if s.requirementNaming == nil {
		state := &requirementNamingPreparationState{}
		win := s.win
		state.queue = newRequirementNamingPreparationQueue(func() {
			if win != nil && !win.IsDestroyed() {
				win.Update(func() {
					if s.requirementNaming == state && !win.IsDestroyed() {
						win.Invalidate()
					}
				})
			}
		})
		s.requirementNaming = state
	}
	return s.requirementNaming
}

func (s *app) startRequirementNamingPreparation(items []application.Requirement) {
	state := s.ensureRequirementNamingPreparation()
	state.scope, state.initialized = s.currentRequirementNamingPreparationScope(), true
	backend := s.backend
	expected := ""
	if identity := s.requirementIdentityJobs; identity != nil && identity.confirmed && identity.state == application.RequirementIdentityAuthenticated {
		expected = identity.identity
	}
	state.queue.start(items, state.scope.enabled && state.scope.prewarm, func(ctx context.Context, item application.Requirement) error {
		return executeRequirementNamingPreparation(ctx, backend, item, expected)
	})
}

func (s *app) observeRequirementNamingPreparation() {
	state := s.requirementNaming
	if state == nil || !state.initialized {
		return
	}
	next := s.currentRequirementNamingPreparationScope()
	if state.scope == next {
		return
	}
	identitySame := state.scope.identity == next.identity && state.scope.profile == next.profile
	s.cancelRequirementNamingPreparation()
	if backend, ok := s.backend.(interface{ CancelRequirementNaming() }); ok {
		backend.CancelRequirementNaming()
	}
	state.scope, state.initialized = next, true
	if identitySame && s.requirementsLoaded && !s.requirementsLoading {
		s.startRequirementNamingPreparation(s.requirements)
	}
}

func executeRequirementNamingPreparation(ctx context.Context, backend application.Backend, item application.Requirement, expected string) error {
	if backend == nil {
		return errors.New("需求命名后端不可用")
	}
	provider, hasIdentity := backend.(application.RequirementIdentityBackend)
	if hasIdentity {
		account, err := provider.RequirementIdentity(ctx)
		if err != nil {
			return err
		}
		if account.State != application.RequirementIdentityAuthenticated || account.Identity == "" {
			return errors.New("无法确认当前 Meegle 登录身份，请登录后继续命名预热")
		}
		if expected != "" && expected != account.Identity {
			return &application.RequirementIdentityChangedError{Snapshot: account}
		}
		expected = account.Identity
	}
	payload := map[string]any{"url": item.URL, "projectKey": item.ProjectKey,
		"projectName": item.Project, "type": item.Type, "id": item.ID, "title": item.Title, "background": true}
	if expected != "" {
		payload["expectedIdentity"] = expected
	}
	result, err := backend.Dispatch(ctx, application.Action{Kind: "requirements.ai-name", Payload: payload})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		return err
	}
	var suggestion domain.RequirementAiNamingSuggestion
	if err = json.Unmarshal(encoded, &suggestion); err != nil {
		return fmt.Errorf("需求命名结果无效：%w", err)
	}
	if _, err = domain.ValidateRequirementAiNamingSuggestion(suggestion); err != nil {
		return err
	}
	if hasIdentity {
		account, err := provider.RequirementIdentity(ctx)
		if err != nil {
			return err
		}
		if account.State != application.RequirementIdentityAuthenticated || account.Identity != expected {
			return &application.RequirementIdentityChangedError{Snapshot: account}
		}
	}
	return ctx.Err()
}

func (s *app) prioritizeRequirementNamingPreparation(item application.Requirement) {
	if state := s.requirementNaming; state != nil {
		state.queue.prioritize(item)
	}
}

func (s *app) cancelRequirementNamingPreparation() {
	if state := s.requirementNaming; state != nil {
		state.queue.cancel()
		state.failuresOpen = false
	}
}

func (s *app) closeRequirementNamingPreparation(ctx context.Context) error {
	var err error
	if backend, ok := s.backend.(interface{ CloseRequirementNaming(context.Context) error }); ok {
		err = backend.CloseRequirementNaming(ctx)
	}
	if state := s.requirementNaming; state != nil {
		err = errors.Join(err, state.queue.close(ctx))
	}
	return err
}

func truncateDisplayRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return string(runes)
}
