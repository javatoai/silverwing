package application

import (
	"context"
	"errors"
)

const (
	RequirementIdentityAuthenticated = "authenticated"
	RequirementIdentityLoggedOut     = "logged-out"
	RequirementIdentityUnavailable   = "unavailable"
)

type RequirementIdentitySnapshot struct {
	State    string `json:"state"`
	Identity string `json:"identity"`
}

// RequirementIdentityBackend is a local-only, read-only identity probe used by
// the desktop to bind a requirements-list request to the active Meegle account.
type RequirementIdentityBackend interface {
	RequirementIdentity(context.Context) (RequirementIdentitySnapshot, error)
}

type RequirementIdentityChangedError struct {
	Snapshot RequirementIdentitySnapshot
}

func (e *RequirementIdentityChangedError) Error() string {
	if e != nil && e.Snapshot.State == RequirementIdentityLoggedOut {
		return "Meegle 当前已退出登录，请重新确认身份后读取需求"
	}
	return "Meegle 登录身份已变化，请重新读取需求"
}

func (a *App) RequirementIdentity(ctx context.Context) (RequirementIdentitySnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RequirementIdentitySnapshot{}, err
	}
	if a == nil || a.Config == nil {
		return RequirementIdentitySnapshot{}, errors.New("应用配置存储不可用")
	}
	config, err := a.Config.Load()
	if err != nil {
		return RequirementIdentitySnapshot{}, err
	}
	if a.Integrations == nil {
		return RequirementIdentitySnapshot{State: RequirementIdentityUnavailable}, nil
	}
	state, identity, err := a.Integrations.RequirementIdentity(ctx, config)
	if err != nil {
		return RequirementIdentitySnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return RequirementIdentitySnapshot{}, err
	}
	return RequirementIdentitySnapshot{State: state, Identity: identity}, nil
}

var _ RequirementIdentityBackend = (*App)(nil)
