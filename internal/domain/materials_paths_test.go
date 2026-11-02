package domain

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMaterialsPathsNormalizeBothPickerChoices(t *testing.T) {
	root := filepath.Join(t.TempDir(), "snb-work-data")
	choices := []string{root, filepath.Join(root, "requirements"), filepath.Join(root, "requirements") + string(filepath.Separator)}
	if runtime.GOOS == "windows" {
		choices = append(choices, filepath.Join(root, "Requirements"))
	}
	for _, selected := range choices {
		paths, err := ResolveMaterialsPaths(selected)
		if err != nil || paths.Root != root || !equalMaterialTestPath(paths.Requirements, filepath.Join(root, "requirements")) {
			t.Fatalf("%s: %+v %v", selected, paths, err)
		}
		again, err := ResolveMaterialsPaths(paths.Root)
		if err != nil || !equalMaterialTestPath(again.Requirements, paths.Requirements) {
			t.Fatalf("normalization is not stable: %+v %v", again, err)
		}
	}
	if _, err := ResolveMaterialsPaths(filepath.Join(root, "requirements", "requirements")); err == nil {
		t.Fatal("ambiguous repeated collection accepted")
	}
}

func equalMaterialTestPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return a == b
}
