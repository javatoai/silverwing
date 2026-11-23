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

func TestTaskCreationUsesConfiguredCodexByDefault(t *testing.T) {
	for _, test := range []struct {
		name   string
		ids    []string
		status domain.WorkspaceToolLaunchStatus
	}{
		{name: "enabled", ids: []string{"codex"}, status: domain.WorkspaceToolOpened},
		{name: "disabled", ids: []string{}},
		{name: "activation-failed", ids: []string{"codex"}, status: domain.WorkspaceToolFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, config, _, _ := taskContextScopeFixture(t)
			config.DefaultWorkspaceToolIDs = test.ids
			if err := app.Config.Save(config); err != nil {
				t.Fatal(err)
			}
			var opened []string
			app.Platform = platform.NewServiceWithCodexURIOpener(app.Paths, app.Config, nil, func(_ context.Context, uri string) error {
				opened = append(opened, uri)
				if test.status == domain.WorkspaceToolFailed {
					return errors.New("activation rejected")
				}
				return nil
			})
			result, err := app.Dispatch(context.Background(), Action{Kind: "task.create", Payload: map[string]any{"folderName": "configured-tools"}})
			if err != nil {
				t.Fatal(err)
			}
			taskPath := filepath.Join(*config.TaskRoot, "configured-tools")
			manifest, err := app.Manifests.Load(taskPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(opened) != len(test.ids) || len(manifest.WorkspaceToolLaunches) != len(test.ids) {
				t.Fatalf("unexpected Codex activations or saved results: %v %+v", opened, manifest.WorkspaceToolLaunches)
			}
			if len(test.ids) != 0 {
				uri, err := url.Parse(opened[0])
				if err != nil || uri.Query().Get("path") != taskPath || manifest.WorkspaceToolLaunches[0].Status != test.status {
					t.Fatalf("wrong activation or saved status: %v %+v %v", opened, manifest.WorkspaceToolLaunches, err)
				}
			}
			if (result.Text != "") != (test.status == domain.WorkspaceToolFailed) {
				t.Fatalf("unexpected launch warning: %q", result.Text)
			}
		})
	}
}
