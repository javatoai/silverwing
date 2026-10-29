package application

import (
	"context"

	"silverwing-go/internal/integrations"
)

// WithRequirementReadOperation groups the adjacent reads of one explicit
// refresh; the integration still verifies the account after every remote read.
func WithRequirementReadOperation(ctx context.Context) context.Context {
	return integrations.WithRequirementReadOperation(ctx)
}

// Image reads share bytes only within one cancellable reader batch. Each
// attachment action still performs its own authorization and persistence.
func WithRequirementImageReadOperation(ctx context.Context) context.Context {
	return integrations.WithRequirementImageReadOperation(ctx)
}

func CloseRequirementImageReadOperation(wait, operation context.Context) error {
	return integrations.CloseRequirementImageReadOperation(wait, operation)
}
