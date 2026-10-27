package platform

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

func TestGitEnvironmentDiagnosticRoutesAreRetired(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	for _, kind := range []string{"git.environment", "git.environment/snapshot"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := service.Execute(context.Background(), kind, nil); err == nil || !strings.Contains(err.Error(), "未知平台操作") {
				t.Fatalf("Execute(%q) error = %v, want retired route rejection", kind, err)
			}
		})
	}
}
