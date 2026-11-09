package codexprotocol

import (
	"context"
	"fmt"
)

type activationRoute func(context.Context, string) error

// Elevated callers must use the interactive desktop, so an existing ordinary
// Codex instance receives the URI at its own integrity level. Never replay a
// failed or uncertain desktop handoff through the elevated caller.
func activateInUserContext(ctx context.Context, uri string, elevation func() (bool, error), direct, desktop activationRoute) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	elevated, err := elevation()
	if err != nil {
		return fmt.Errorf("读取应用运行权限失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if elevated {
		return desktop(ctx, uri)
	}
	return direct(ctx, uri)
}
