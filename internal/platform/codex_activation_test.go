package platform

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/storage"
)

func TestCodexActivationKeepsWorkspaceBindingAndReportsFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "DPF 文件自动解析")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"new", "bound", "force-new", "rejected", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var called string
			service := NewServiceWithCodexURIOpener(storage.NewPaths(t.TempDir()), nil, nil, func(ctx context.Context, uri string) error {
				called = uri
				if mode == "rejected" {
					return errors.New("Windows rejected activation")
				}
				return ctx.Err()
			})
			thread := "11111111-2222-3333-4444-555555555555"
			if mode == "bound" || mode == "force-new" {
				if err := service.setCodexBoundThread(path, &thread); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			data, err := service.Execute(ctx, "codex.ai/open", map[string]any{"workspacePath": path, "forceNew": mode == "force-new"})
			if mode == "rejected" || mode == "canceled" {
				if err == nil {
					t.Fatal("activation failure was reported as opened", data)
				}
				if mode == "canceled" && called != "" {
					t.Fatal("canceled request activated Codex")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			uri, err := url.Parse(called)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "bound" {
				if uri.Path != "/"+thread {
					t.Fatal(called)
				}
			} else if uri.Path != "/new" || uri.Query().Get("path") != path {
				t.Fatal(called)
			}
		})
	}
}
