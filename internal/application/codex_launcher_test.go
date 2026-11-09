package application

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/platform"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestTaskCreationCodexUsesPlatformActivation(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	taskPath := filepath.Join(t.TempDir(), "DPF 文件自动解析")
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rejected := range []bool{false, true} {
		var uri string
		service := platform.NewServiceWithCodexURIOpener(paths, nil, nil, func(_ context.Context, value string) error {
			uri = value
			if rejected {
				return errors.New("activation rejected")
			}
			return nil
		})
		app := &App{Paths: paths, Manifests: storage.NewManifestStore(), Runner: process.NewRunner(), Platform: service}
		launcher := app.taskService(domain.DefaultAppConfig()).WorkspaceTools.Registry.Launchers["codex"]
		err := launcher.Open(context.Background(), workspace.TaskWorkspaceContext{TaskDirectory: taskPath})
		if (err != nil) != rejected {
			t.Fatal("activation error was lost", err)
		}
		parsed, parseErr := url.Parse(uri)
		if parseErr != nil || parsed.Path != "/new" || parsed.Query().Get("path") != taskPath {
			t.Fatal(uri, parseErr)
		}
	}
}
