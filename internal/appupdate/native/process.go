package native

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"silverwing-go/internal/appupdate"
)

type Runner struct{}

func (Runner) Run(_ context.Context, executable string, args []string) (int, error) {
	// Installation is not interrupted by killing an installer halfway through.
	cmd := exec.Command(executable, args...)
	hide(cmd)
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), nil
	}
	return 0, err
}

func LaunchHelper(helper, plan string) error {
	cmd := exec.Command(helper, "--job", plan)
	cmd.Dir = filepath.Dir(plan)
	hide(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

type Started struct{ done <-chan error }

func Start(installation appupdate.Installation, health, token string) (*Started, error) {
	return start(installation, health, token, "")
}

func StartWithOutcome(installation appupdate.Installation, path string) (*Started, error) {
	return start(installation, "", "", path)
}

func start(installation appupdate.Installation, health, token, outcome string) (*Started, error) {
	args := []string{"--home", installation.Home}
	if health != "" {
		args = append(args, "--update-health", health, "--update-token", token)
	}
	if outcome != "" {
		args = append(args, "--update-result", outcome)
	}
	cmd := exec.Command(installation.Executable, args...)
	cmd.Dir = installation.Home
	hide(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	return &Started{done: done}, nil
}

// WaitHealthy never terminates the new application. If it is still alive after
// a timeout, the caller retains the recovery package instead of replacing it.
func (started *Started) WaitHealthy(ctx context.Context, path, token string) (alive bool, err error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if content, e := os.ReadFile(path); e == nil && string(content) == token {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-started.done:
			if content, e := os.ReadFile(path); e == nil && string(content) == token {
				return false, nil
			}
			return false, errors.New("新版本在启动就绪前退出")
		case <-ticker.C:
		}
	}
}
