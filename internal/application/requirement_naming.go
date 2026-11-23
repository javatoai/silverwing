package application

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
)

// RequirementNamingCacheBackend is an optional local-only cache read. Desktop
// callers may use it after confirming the account identity to avoid probing
// Meegle or invoking AI for an existing scoped suggestion.
type RequirementNamingCacheBackend interface {
	CachedRequirementAINaming(context.Context, string, string) (domain.RequirementAiNamingSuggestion, bool, error)
}

func (a *App) CachedRequirementAINaming(ctx context.Context, link, expectedIdentity string) (domain.RequirementAiNamingSuggestion, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.RequirementAiNamingSuggestion{}, false, err
	}
	if a == nil || a.Config == nil {
		return domain.RequirementAiNamingSuggestion{}, false, errors.New("应用配置存储不可用")
	}
	if a.Integrations == nil {
		return domain.RequirementAiNamingSuggestion{}, false, nil
	}
	config, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return domain.RequirementAiNamingSuggestion{}, false, err
	}
	suggestion, found, err := a.Integrations.ReadCachedRequirementAINaming(ctx, config, link, expectedIdentity)
	if err != nil {
		var changed *integrations.RequirementIdentityChangedError
		if errors.As(err, &changed) {
			return domain.RequirementAiNamingSuggestion{}, false, &RequirementIdentityChangedError{Snapshot: RequirementIdentitySnapshot{State: changed.State, Identity: changed.Identity}}
		}
	}
	return suggestion, found, err
}

// Shared naming requests outlive an individual foreground/prewarm subscriber.
// Their lifetime still belongs to this application instance.
func (a *App) CancelRequirementNaming() { a.Integrations.CancelRequirementNaming() }

func (a *App) CloseRequirementNaming(ctx context.Context) error {
	return a.Integrations.CloseRequirementNaming(ctx)
}
