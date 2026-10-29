package architecture

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fixtureModule = "fixture.test/project"

func TestRepositoryDependencyDirections(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestDependencyRulesRejectViolatingImports(t *testing.T) {
	tests := []struct {
		name, file, dependency, declaration, rule string
	}{
		{"domain editor", "domain/model.go", fixtureModule + "/internal/editor", "", "domain-direction"},
		{"domain process", "domain/model.go", fixtureModule + "/internal/process", "", "domain-direction"},
		{"domain application subpackage", "domain/nested/model.go", fixtureModule + "/internal/application/operations", "", "domain-direction"},
		{"domain storage", "domain/model.go", fixtureModule + "/internal/storage", "", "domain-direction"},
		{"domain framework", "domain/model.go", "github.com/egoist/mygo/ui", "", "domain-direction"},
		{"markdown business", "markdowntext/parse.go", fixtureModule + "/internal/domain", "", "pure-package"},
		{"filelanguage framework", "filelanguage/language.go", "github.com/egoist/mygo/ui/editor", "", "pure-package"},
		{"asyncjob process", "asyncjob/jobs.go", fixtureModule + "/internal/process", "", "pure-package"},
		{"profile adapter", "productprofile/current.go", fixtureModule + "/internal/platform", "", "pure-package"},
		{"pure external", "asyncjob/jobs.go", "example.test/framework", "", "pure-package"},
		{"nonstandard unqualified path", "asyncjob/jobs.go", "framework/runtime", "", "pure-package"},
		{"goldmark outside markdown", "productprofile/current.go", "github.com/yuin/goldmark/ast", "", "pure-package"},
		{"application desktop", "application/actions.go", fixtureModule + "/internal/desktop", "", "application-ui"},
		{"application framework", "application/actions.go", "github.com/egoist/mygo/ui", "", "application-ui"},
		{"documents adapter", "documents/preparation.go", fixtureModule + "/internal/requirementprovider", "", "documents-provider"},
		{"documents nested adapter", "documents/preparation.go", fixtureModule + "/internal/requirementprovider/meegle", "", "documents-provider"},
		{"documents integration", "documents/preparation.go", fixtureModule + "/internal/integrations", "", "documents-provider"},
		{"documents remote runner", "documents/preparation.go", fixtureModule + "/internal/commandruntime", "", "documents-provider"},
		{"documents process", "documents/preparation.go", fixtureModule + "/internal/process", "", "documents-provider"},
		{"shared editor", "desktop/component.go", fixtureModule + "/internal/workspace", "func TextEditor() {}", "shared-editor-language"},
		{"shared language", "desktop/syntax.go", fixtureModule + "/internal/workspace", "func workspaceFilePreviewLanguage() {}", "shared-editor-language"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := fixture(t, map[string]string{test.file: "package fixture\n\nimport alias " + strconv.Quote(test.dependency) + "\n" + test.declaration + "\n"})
			violations, err := Check(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 1 {
				t.Fatalf("expected one %s violation, got %v", test.rule, violations)
			}
			got := violations[0]
			if got.Rule != test.rule || got.Import != test.dependency || got.File != "internal/"+test.file || got.Line != 3 {
				t.Fatalf("wrong dependency location or rule: %+v", got)
			}
		})
	}
}

func TestDependencyRulesAllowLegitimateConsumers(t *testing.T) {
	root := fixture(t, map[string]string{
		"domain/model.go":               sourceImport(fixtureModule + "/internal/markdowntext"),
		"markdowntext/parse.go":         sourceImport("github.com/yuin/goldmark/ast"),
		"filelanguage/language.go":      sourceImport("path/filepath"),
		"asyncjob/jobs.go":              sourceImport("context"),
		"productprofile/current.go":     sourceImport("strings"),
		"documents/fulltext.go":         sourceImport(fixtureModule + "/internal/media"),
		"documents/files.go":            sourceImport(fixtureModule + "/internal/storage"),
		"documents/clipboard.go":        sourceImport(fixtureModule + "/internal/executable"),
		"application/actions.go":        sourceImport(fixtureModule + "/internal/workspace"),
		"desktop/materials_host.go":     sourceImport(fixtureModule + "/internal/workspace"),
		"desktop/syntax.go":             sourceImport(fixtureModule+"/internal/filelanguage") + "\nfunc textEditorLanguage() {}\n",
		"requirementprovider/meegle.go": sourceImport(fixtureModule + "/internal/process"),
		// Prefixes must match complete path components.
		"application/similar.go": sourceImport("github.com/egoist/mygo-extra"),
	})
	violations, err := Check(root)
	if err != nil || len(violations) != 0 {
		t.Fatalf("legitimate dependencies rejected: %v, %v", violations, err)
	}
}

func TestChecksAllOperatingSystemsAndBuildTags(t *testing.T) {
	root := fixture(t, map[string]string{
		"domain/model_windows.go": "//go:build windows\n\n" + sourceImport(fixtureModule+"/internal/desktop"),
		"domain/model_darwin.go":  "//go:build darwin\n\n" + sourceImport(fixtureModule+"/internal/desktop"),
		"domain/model_future.go":  "//go:build an_unset_feature\n\n" + sourceImport(fixtureModule+"/internal/desktop"),
	})
	violations, err := Check(root)
	if err != nil || len(violations) != 3 {
		t.Fatalf("platform-independent scan got %v, %v", violations, err)
	}
}

func TestSkipsTestsFixturesAndSeparateModules(t *testing.T) {
	root := fixture(t, map[string]string{
		"domain/model.go":               "package domain\n// import \"" + fixtureModule + "/internal/editor\"\n",
		"domain/model_test.go":          "this test is intentionally not valid Go",
		"domain/testdata/bad.go":        "this fixture is intentionally not valid Go",
		"domain/vendor/other/bad.go":    "this vendored file is intentionally not valid Go",
		"domain/.hidden/bad.go":         "this hidden file is intentionally not valid Go",
		"separate/go.mod":               "module separate.test/module\n",
		"separate/internal/domain/a.go": sourceImport(fixtureModule + "/internal/editor"),
		"separate/internal/domain/b.go": "this separate module is intentionally not valid Go",
	})
	violations, err := Check(root)
	if err != nil || len(violations) != 0 {
		t.Fatalf("ignored sources affected scan: %v, %v", violations, err)
	}
}

func TestDocumentsRejectsDirectRemoteCLI(t *testing.T) {
	for _, invocation := range []string{
		`runner.Command("meegle", "work-item", "get")`,
		`runner.CommandContext(nil, "C:\\tools\\meegle.exe", "work-item", "get")`,
		`runner.LookPath("lark")`,
		`runner.CommandContext(nil, resolve("genbu"), "status")`,
	} {
		t.Run(invocation, func(t *testing.T) {
			root := fixture(t, map[string]string{"documents/provider.go": "package documents\nimport runner \"os/exec\"\nfunc run() { _ = " + invocation + " }\n"})
			violations, err := Check(root)
			if err != nil || len(violations) != 1 || violations[0].Rule != "documents-remote-cli" {
				t.Fatalf("remote CLI not identified: %v, %v", violations, err)
			}
		})
	}
}

func TestDocumentsNativeCommandsRemainAllowed(t *testing.T) {
	root := fixture(t, map[string]string{"documents/clipboard_unix.go": "package documents\nimport \"os/exec\"\nfunc run() { _ = exec.CommandContext(nil, \"osascript\", \"-e\", \"clipboard\") }\n"})
	violations, err := Check(root)
	if err != nil || len(violations) != 0 {
		t.Fatalf("native clipboard command rejected: %v, %v", violations, err)
	}
}

func TestDocumentsRemoteCLIHandlesRawAndDotImports(t *testing.T) {
	root := fixture(t, map[string]string{"documents/provider.go": "package documents\nimport . `os/exec`\nfunc run() { _ = Command(\"meegle\", \"work-item\") }\n"})
	violations, err := Check(root)
	if err != nil || len(violations) != 1 || violations[0].Rule != "documents-remote-cli" {
		t.Fatalf("dot/raw import bypassed the remote CLI rule: %v, %v", violations, err)
	}
}

func TestMalformedSourceReturnsAnError(t *testing.T) {
	root := fixture(t, map[string]string{"domain/bad.go": "package"})
	if _, err := Check(root); err == nil || !strings.Contains(err.Error(), "internal/domain/bad.go") {
		t.Fatalf("expected located source parse error, got %v", err)
	}
}

func fixture(t *testing.T, sources map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "go.mod"), "module "+fixtureModule+"\n")
	for name, source := range sources {
		writeFixture(t, filepath.Join(root, "internal", filepath.FromSlash(name)), source)
	}
	return root
}

func writeFixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sourceImport(dependency string) string {
	return "package fixture\nimport " + strconv.Quote(dependency) + "\n"
}
