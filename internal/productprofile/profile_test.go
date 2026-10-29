package productprofile

import (
	"path/filepath"
	"testing"
)

func TestCurrentProfileIdentity(t *testing.T) {
	type identity struct {
		name, cliName, executable, packageDirectory, tagPrefix, releasesURL string
		isolated, latest                                                    bool
	}
	expected := map[string]identity{
		"":        {"Silverwing", "SilverWing", "silverwing", "Silverwing", "v", "https://github.com/javatoai/silverwing/releases", false, true},
		"qa":      {"silverwing-qa", "silverwing-qa", "silverwing-qa", "silverwing-qa", "qa-v", "https://github.com/javatoai/silverwing/releases?q=qa-v&expanded=true", true, false},
		"product": {"silverwing-product", "silverwing-product", "silverwing-product", "silverwing-product", "product-v", "https://github.com/javatoai/silverwing/releases?q=product-v&expanded=true", true, false},
	}
	want, ok := expected[Edition]
	if !ok {
		t.Fatalf("unexpected edition %q", Edition)
	}
	got := identity{Name, CLIName, ExecutableName, PackageDirectoryName, ReleaseTagPrefix, ReleasesURL(), TaskRootInHome, ReleaseMakeLatest}
	if got != want {
		t.Fatalf("build identity changed: got %+v, want %+v", got, want)
	}
	if AutomaticUpdates {
		t.Fatal("this build must not start automatic updates")
	}
}

func TestDefaultPathsPreserveExplicitApplicationHome(t *testing.T) {
	userHome := filepath.Join("users", "用户")
	appHome := filepath.Join("custom", "应用资料")
	if got, want := DefaultHome(userHome), filepath.Join(userHome, ExecutableName); got != want {
		t.Fatalf("default application home = %q, want %q", got, want)
	}
	wantTasks := filepath.Join(userHome, "tasks")
	wantMaterials := ""
	if Edition != "" {
		wantTasks = filepath.Join(appHome, "tasks")
		wantMaterials = filepath.Join(appHome, "materials")
	}
	if got := DefaultTaskRoot(userHome, appHome); got != wantTasks {
		t.Fatalf("default task root = %q, want %q", got, wantTasks)
	}
	if got := DefaultMaterialsRoot(appHome); got != wantMaterials {
		t.Fatalf("default materials root = %q, want %q", got, wantMaterials)
	}
}

func TestEditionMarkersRejectOtherBuilds(t *testing.T) {
	for _, candidate := range []string{"", "qa", "product", "QA", " product", "developer"} {
		want := Edition != "" && candidate == Edition
		if got := AcceptsEdition(candidate); got != want {
			t.Errorf("AcceptsEdition(%q) = %v, want %v", candidate, got, want)
		}
	}
}

func TestLegacyIdentityProjectionConstants(t *testing.T) {
	// The existing exported name remains usable in a constant expression.
	const naming = TaskNamingLabel
	want, ok := map[string]struct{ naming, edition, migration string }{
		"":        {"需求命名器", "研发版", "迁移日志不是研发版格式"},
		"qa":      {"测试任务命名器", "测试版", "迁移日志不是 QA 格式"},
		"product": {"产品任务命名器", "产品版", "迁移日志不是产品版格式"},
	}[Edition]
	if !ok {
		t.Fatalf("unexpected edition %q", Edition)
	}
	if naming != want.naming || ActionEditionLabel != want.edition || MigrationEditionError() != want.migration {
		t.Fatalf("legacy wording changed: %q, %q, %q", naming, ActionEditionLabel, MigrationEditionError())
	}
}
