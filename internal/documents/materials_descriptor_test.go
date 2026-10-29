package documents

import (
	"path/filepath"
	"testing"
)

func TestAnnualMaterialsDescriptionMatchesPermissionScope(t *testing.T) {
	owned := filepath.Join(t.TempDir(), "2026", "123-需求", "资料")
	description := DescribeMaterials(owned)
	if description.Kind != MaterialsAnnual || description.ReadRoot != filepath.Dir(owned) || description.OwnedRoot != owned || !description.CanWrite || !description.CanSync {
		t.Fatalf("wrong annual description: %+v", description)
	}
	for _, path := range []string{"资料/new.md", "temp/scratch.txt"} {
		if !description.Writable(path, false) || !description.Readable(path) {
			t.Fatal("owned area denied", path)
		}
	}
	for _, path := range []string{"同事/new.md", "temp/.gitignore", "../outside.md", "资料/.silverwing-private"} {
		if description.Writable(path, false) {
			t.Fatal("unauthorized area writable", path)
		}
	}
	if !description.Readable("同事/new.md") || description.Readable(".git/config") {
		t.Fatal("read capabilities differ from materials scope")
	}
	legacy := DescribeMaterials(filepath.Join(t.TempDir(), "123-旧目录", "资料"))
	if legacy.Kind != MaterialsNone || legacy.CanWrite || legacy.CanSync {
		t.Fatal("legacy materials acquired capabilities", legacy)
	}
}
