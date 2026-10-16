//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestMarketplaceRemovalPreservesConcurrentSettings(t *testing.T) {
	root := t.TempDir()
	entered, release := filepath.Join(root, "entered"), filepath.Join(root, "release")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	script := filepath.Join(root, "owned-codex.ps1")
	body := fmt.Sprintf("[IO.File]::WriteAllText(%s,'ready'); while (-not [IO.File]::Exists(%s)) { Start-Sleep -Milliseconds 10 }; Write-Output '{}'", quote(entered), quote(release))
	if err := os.WriteFile(script, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(root, "isolated-home"))
	store := storage.NewConfigStore(paths)
	cfg := domain.DefaultAppConfig()
	cfg.CodexExecutablePath = &script
	cfg.CodexPluginMarketplaceSources = []domain.CodexPluginMarketplaceSource{{ID: "example", Name: "Example", RepositoryURL: "https://example.invalid/plugins.git", MarketplaceDirectory: "."}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	state := emptyExtensionState()
	state.Marketplaces["example"] = MarketplaceSnapshot{SourceID: "example", MarketplaceName: stringPtr("example")}
	if err := service.saveExtensionState(state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := service.Execute(ctx, "codex.extensions/marketplaces/remove-source", map[string]any{"sourceId": "example", "confirmed": true})
		done <- err
	}()
	for !exists(entered) {
		select {
		case err := <-done:
			t.Fatalf("fixture CLI ended before the concurrent update: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := store.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		current.Theme = domain.ThemeDark
		return current, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, []byte("continue"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	actual, err := store.Load()
	if err != nil || actual.Theme != domain.ThemeDark || len(actual.CodexPluginMarketplaceSources) != 0 {
		t.Fatalf("marketplace removal overwrote concurrent configuration: theme=%s sources=%d err=%v", actual.Theme, len(actual.CodexPluginMarketplaceSources), err)
	}
}
