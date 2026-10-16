package application

import "context"

func (a *App) InvalidateRequirementDocuments() {
	a.Integrations.InvalidateRequirementDocuments()
}

func (a *App) CancelRequirementDocuments(ctx context.Context) error {
	return a.Integrations.CancelRequirementDocuments(ctx)
}

func (a *App) CloseRequirementDocuments(ctx context.Context) error {
	return a.Integrations.CloseRequirementDocuments(ctx)
}
