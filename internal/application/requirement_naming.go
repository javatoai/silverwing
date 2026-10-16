package application

import "context"

// Shared naming requests outlive an individual foreground/prewarm subscriber.
// Their lifetime still belongs to this application instance.
func (a *App) CancelRequirementNaming() { a.Integrations.CancelRequirementNaming() }

func (a *App) CloseRequirementNaming(ctx context.Context) error {
	return a.Integrations.CloseRequirementNaming(ctx)
}
