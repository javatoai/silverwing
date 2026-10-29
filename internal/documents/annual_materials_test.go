package documents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestAnnualRequirementReuseAndDuplicates(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "2024", "123-原名")
	for _, path := range []string{original, filepath.Join(root, "2025", "1234-不同需求"), filepath.Join(root, "123-旧平铺")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	path, err := findAnnualRequirementDirectory(root, "123", "改名", 2026)
	if err != nil || path != original {
		t.Fatalf("reuse %s %v", path, err)
	}
	path, err = findAnnualRequirementDirectory(root, "99", "新需求", 2026)
	if err != nil || path != filepath.Join(root, "2026", "99-新需求") {
		t.Fatal(path, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "2025", "123-重复"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = findAnnualRequirementDirectory(root, "123", "改名", 2026); err == nil {
		t.Fatal("duplicate silently reused")
	}
}

func TestAnnualPlanRaceReusesWinnerAndCapturedYear(t *testing.T) {
	root := t.TempDir()
	sub := "资料"
	config := domain.AppConfig{RequirementMaterialsRoot: &root, RequirementMaterialsSubdirectory: &sub}
	service := NewPreparationService(storage.NewPaths(t.TempDir()), nil)
	ctx := WithMaterialsCreationTime(context.Background(), time.Date(2024, 12, 31, 20, 0, 0, 0, time.Local))
	link := "https://project.feishu.cn/obt/userstory/detail/123"
	first, err := service.Plan(ctx, config, link, "需求", "第一个名称")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Plan(ctx, config, link, "需求", "第二个名称")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.RequirementDirectory, filepath.Join("2024", "123-")) {
		t.Fatal(first)
	}
	a, err := service.Materialize(ctx, config, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Materialize(ctx, config, second)
	if err != nil {
		t.Fatal(err)
	}
	if a.Plan.RequirementDirectory != b.Plan.RequirementDirectory || !b.Plan.ReusedExistingDirectory {
		t.Fatal(a, b)
	}
	if _, err := os.Stat(second.RequirementDirectory); !os.IsNotExist(err) {
		t.Fatal("racing plan created duplicate", err)
	}
}

func TestAnnualScopeTempAndPermissions(t *testing.T) {
	owned := filepath.Join(t.TempDir(), "2026", "123-需求", "自定义资料")
	scope, ok := AnnualScope(owned)
	if !ok {
		t.Fatal("scope")
	}
	if err := scope.EnsureLocal(); err != nil {
		t.Fatal(err)
	}
	ignore := filepath.Join(scope.Temporary, ".gitignore")
	before, _ := os.Stat(ignore)
	if err := scope.EnsureLocal(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(ignore)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("no-op rewrote ignore")
	}
	for _, path := range []string{"自定义资料/a.bin", "自定义资料/nested/anything.xyz", "temp/scratch.sql"} {
		if !scope.Writable(path, false) {
			t.Fatal("denied", path)
		}
	}
	for _, path := range []string{"测试/a.md", "outside.txt", "自定义资料", "temp", "temp/.gitignore", "../其他/资料/a", "自定义资料/.git/config", "自定义资料/.silverwing-private"} {
		if scope.Writable(path, false) {
			t.Fatal("allowed", path)
		}
	}
	if !scope.Writable(owned, true) {
		t.Fatal("cannot create in owned root")
	}
	peer := filepath.Join(scope.Root, "测试")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	text, err := scope.DirectoryInstructions(false)
	if err != nil || !strings.Contains(text, peer) || !strings.Contains(text, "只读") || !strings.Contains(text, scope.Temporary) {
		t.Fatal(text, err)
	}
}

func TestAnnualPreviewDoesNotCreateAndEnsureReuses(t *testing.T) {
	root := t.TempDir()
	sub := "文档"
	existing := filepath.Join(root, "2023", "123-老名字")
	if err := os.MkdirAll(existing, 0755); err != nil {
		t.Fatal(err)
	}
	config := domain.AppConfig{RequirementMaterialsRoot: &root, RequirementMaterialsSubdirectory: &sub}
	service := NewPreparationService(storage.NewPaths(t.TempDir()), nil)
	link := "https://project.feishu.cn/obt/userstory/detail/123"
	preview, err := service.Preview(context.Background(), config, link, "新名字")
	if err != nil || preview.Status != domain.MaterialsReady || *preview.WriteRoot != filepath.Join(existing, sub) {
		t.Fatal(preview, err)
	}
	if _, err := os.Stat(filepath.Join(existing, "temp")); !os.IsNotExist(err) {
		t.Fatal("preview wrote temp")
	}
	ready, err := service.Ensure(context.Background(), config, link, "新名字")
	if err != nil || ready.Status != domain.MaterialsReady || *ready.WriteRoot != *preview.WriteRoot {
		t.Fatal(ready, err)
	}
	if _, err := os.Stat(filepath.Join(existing, "temp", ".gitignore")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "Index.md"), filepath.Join(existing, "README.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("generated unwanted index", path)
		}
	}
}

func TestAnnualCandidateIdentityConflictAndFlatIgnored(t *testing.T) {
	root := t.TempDir()
	sub := "研发"
	flat := filepath.Join(root, "123-旧名字", sub)
	if err := os.MkdirAll(flat, 0755); err != nil {
		t.Fatal(err)
	}
	path, err := findAnnualRequirementDirectory(root, "123", "新名字", 2026)
	if err != nil || path == filepath.Dir(flat) {
		t.Fatal(path, err)
	}
	owned := filepath.Join(root, "2025", "123-名字", sub)
	if err := os.MkdirAll(owned, 0755); err != nil {
		t.Fatal(err)
	}
	files := &preparationFileStore{}
	manifest := RequirementDocumentationManifest{FormatVersion: documentationFormatVersion, Identity: RequirementIdentity{Space: "other", Kind: "userstory", WorkItemID: "123"}, DirectoryName: "123-名字"}
	if err := files.write(filepath.Join(owned, requirementManifestName), files.encodeRequirement(manifest)); err != nil {
		t.Fatal(err)
	}
	want := RequirementIdentity{Space: "obt", Kind: "userstory", WorkItemID: "123"}
	if err := validateExistingRequirementDirectory(filepath.Dir(owned), sub, want, files); err == nil {
		t.Fatal("identity conflict accepted")
	}
	if _, err := requireMaterialsSubdirectory(&[]string{"TEMP"}[0]); err == nil {
		t.Fatal("temp alias accepted")
	}
}
