package executable

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexReplacementSkipsFailedDesktopExecutable(t *testing.T) {
	localAppData := t.TempDir()
	t.Setenv("LOCALAPPDATA", localAppData)
	root := filepath.Join(localAppData, "OpenAI", "Codex", "bin")
	failed := filepath.Join(root, "codex.exe")
	replacement := filepath.Join(root, "alternate", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(replacement), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{failed, replacement} {
		if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	earlier := time.Now().Add(-time.Hour)
	if err := os.Chtimes(replacement, earlier, earlier); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := NewResolver(&loginPathRunner{})
	if got := r.Resolve(ctx, ToolCodex, ""); got.Command != failed || got.Source != SourceDesktopApp {
		t.Fatalf("ordinary discovery should prefer the newest Codex: %#v", got)
	}
	if got := r.ResolveCodexReplacement(ctx, failed); got.Command != replacement || got.Source != SourceDesktopApp {
		t.Fatalf("recovery did not skip the failed executable: %#v", got)
	}
	if got := r.Resolve(ctx, ToolCodex, ""); got.Command != failed || got.Source != SourceDesktopApp {
		t.Fatalf("recovery changed ordinary discovery: %#v", got)
	}
}
