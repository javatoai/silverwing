package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestResolveDevelopmentToolPath(t *testing.T) {
	root := t.TempDir()
	configured := filepath.Join(root, "Configured WebStorm.app")
	detected := filepath.Join(root, "Detected WebStorm.app")
	emptyBundle := filepath.Join(root, "Empty.app")
	wrongFile := filepath.Join(root, "File.app")
	nonExecutable := filepath.Join(root, "not-executable")
	for _, bundle := range []string{configured, detected} {
		contents := filepath.Join(bundle, "Contents")
		if err := os.MkdirAll(contents, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(contents, "Info.plist"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(emptyBundle, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, regular := range []string{wrongFile, nonExecutable} {
		if err := os.WriteFile(regular, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(root, "Missing.app")
	for _, tc := range []struct {
		name, goos, configured, detected, want string
		wantDetection                          bool
	}{
		{"configured", "darwin", configured, detected, configured, false},
		{"missing", "darwin", missing, detected, detected, true},
		{"empty", "darwin", "", detected, detected, true},
		{"ordinary directory", "darwin", root, detected, detected, true},
		{"empty bundle", "darwin", emptyBundle, detected, detected, true},
		{"file with app suffix", "darwin", wrongFile, detected, detected, true},
		{"non-executable file", "darwin", nonExecutable, detected, detected, true},
		{"undetected", "darwin", missing, "", "", true},
		{"windows", "windows", missing, detected, "", false},
		{"linux", "linux", missing, detected, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const selected = domain.DevelopmentToolWebStorm
			calls := 0
			path, err := resolveDevelopmentToolPath(context.Background(), tc.goos, selected, tc.configured, func(_ context.Context, tool domain.DevelopmentToolType) string {
				calls++
				if tool != selected {
					t.Fatalf("detected a different IDE: %s", tool)
				}
				return tc.detected
			})
			wantCalls := 0
			if tc.wantDetection {
				wantCalls = 1
			}
			if err != nil || path != tc.want || calls != wantCalls {
				t.Fatalf("path = %q, err = %v, detector calls = %d; want %q, no error, %d calls", path, err, calls, tc.want, wantCalls)
			}
		})
	}
}

func TestResolveDevelopmentToolPathCanceledDuringDetection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := resolveDevelopmentToolPath(ctx, "darwin", domain.DevelopmentToolWebStorm, "", func(context.Context, domain.DevelopmentToolType) string {
		cancel()
		return ""
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
