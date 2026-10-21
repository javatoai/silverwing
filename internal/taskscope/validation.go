// Package taskscope carries an ephemeral task mutation check across internal
// package boundaries without adding fields to persisted or action contracts.
package taskscope

import (
	"context"

	"silverwing-go/internal/domain"
)

type validationKey struct{}

type validation func(context.Context, domain.AppConfig, string, domain.TaskManifest) error

func WithValidation(ctx context.Context, validate func(context.Context, domain.AppConfig, string, domain.TaskManifest) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if validate == nil {
		return ctx
	}
	return context.WithValue(ctx, validationKey{}, validation(validate))
}

// Validate runs the one-shot check, when present, while the caller owns the
// task mutation lock. Context cancellation is honored around that check.
func Validate(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	validate, ok := ctx.Value(validationKey{}).(validation)
	if !ok || validate == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validate(ctx, config, taskPath, manifest); err != nil {
		return err
	}
	return ctx.Err()
}
