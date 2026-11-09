//go:build !windows

package codexprotocol

import (
	"context"
	"errors"
	"silverwing-go/internal/process"
)

func Open(context.Context, string, string, interface {
	Run(context.Context, process.Spec) (process.Result, error)
}) error {
	return errors.New("Codex 协议修复仅适用于 Windows")
}
