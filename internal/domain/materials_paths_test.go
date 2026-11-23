package domain

import (
	"path/filepath"
	"testing"
)

func TestMaterialsPathsUseExactConfiguredRoot(t *testing.T) {
	base := t.TempDir()
	for _, root := range []string{
		filepath.Join(base, "snb-work-data"),
		filepath.Join(base, "requirements"),
		filepath.Join(base, "Requirements"),
		filepath.Join(base, "requirements", "requirements"),
	} {
		configured := " " + root + string(filepath.Separator) + " "
		config := AppConfig{RequirementMaterialsRoot: &configured}
		want := MaterialsPaths{Root: root, Requirements: filepath.Join(root, RequirementsDirectory)}
		if paths, err := config.MaterialsPaths(); err != nil || paths != want {
			t.Fatalf("%s: got %+v %v, want %+v", configured, paths, err, want)
		}
	}
}

func TestMaterialsPathsRejectUnconfiguredOrVolumeRoot(t *testing.T) {
	base := t.TempDir()
	for _, configured := range []string{" ", filepath.VolumeName(base) + string(filepath.Separator)} {
		if _, err := ResolveMaterialsPaths(configured); err == nil {
			t.Fatalf("accepted invalid repository root %q", configured)
		}
	}
}
