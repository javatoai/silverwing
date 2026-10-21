package application

import (
	"context"
	"os/exec"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

var desktopLaunchRunner = process.NewRunner()

func detachedToolCommand(ctx context.Context, command string, args []string) (*exec.Cmd, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if system := executable.SystemCommand(command); system != command {
		return process.NewDetachedCommand(system, args...), nil
	}
	spec, err := commandruntime.Prepare(ctx, desktopLaunchRunner, nil, "", "", command)
	if err != nil {
		return nil, err
	}
	return process.NewDetachedCommandWithEnvironment(spec.Executable, args, spec.Env, spec.RemoveEnv), nil
}
