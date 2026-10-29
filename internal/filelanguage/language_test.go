package filelanguage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFromPathPreservesLanguageMapping(t *testing.T) {
	for _, test := range []struct {
		filenames string
		language  Language
	}{
		{"build.gradle build.gradle.kts", Gradle},
		{"Main.java", Java},
		{"main.kt script.kts", Kotlin},
		{"app.properties app.ini app.conf", Properties},
		{"index.html index.htm config.xml icon.svg", HTML},
		{"app.css app.scss app.sass app.less", CSS},
		{"app.js app.jsx", JavaScript},
		{"app.ts app.tsx", TypeScript},
		{"config.json", JSON},
		{"config.yaml config.yml", YAML},
		{"query.sql", SQL},
		{"main.py main.pyw main.pyi", Python},
		{"script.sh script.bash script.zsh", Shell},
		{"script.ps1 module.psm1 manifest.psd1", PowerShell},
		{"script.bat script.cmd", Batch},
		{"README.md README.markdown", Markdown},
		{"README file.unknown", PlainText},
	} {
		for _, filename := range strings.Fields(test.filenames) {
			t.Run(filename, func(t *testing.T) {
				for _, name := range []string{filename, "目录/" + strings.ToUpper(filename), `C:\任务\` + filename} {
					if got := FromPath(name); got != test.language {
						t.Fatalf("FromPath(%q) = %s, want %s", name, got, test.language)
					}
				}
				encoded, err := json.Marshal(test.language)
				if err != nil || string(encoded) != `"`+string(test.language)+`"` {
					t.Fatalf("language payload changed: %s, %v", encoded, err)
				}
			})
		}
	}
}

func TestSourceEditorOverridePreservesNativeFilenamePrecedence(t *testing.T) {
	for filename, want := range map[string]Language{
		"build.gradle": Gradle, "APP.PROPERTIES": Properties,
		"script.ps1": PowerShell, "module.psm1": PowerShell, "manifest.psd1": PowerShell,
		"script.bat": Batch, "script.cmd": Batch,
		"build.gradle.kts": "", "config.ini": "", "app.conf": "", "main.kt": "", "README.md": "", "unknown": "",
	} {
		if got := SourceEditorOverride(filename); got != want {
			t.Errorf("SourceEditorOverride(%q) = %q, want %q", filename, got, want)
		}
	}
}
