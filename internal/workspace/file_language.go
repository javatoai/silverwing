package workspace

import "silverwing-go/internal/filelanguage"

// WorkspaceFileLanguage is retained for preview payload and caller compatibility.
type WorkspaceFileLanguage = filelanguage.Language

const (
	FileLanguageJava       = filelanguage.Java
	FileLanguageKotlin     = filelanguage.Kotlin
	FileLanguageGradle     = filelanguage.Gradle
	FileLanguageProperties = filelanguage.Properties
	FileLanguageHTML       = filelanguage.HTML
	FileLanguageCSS        = filelanguage.CSS
	FileLanguageJavaScript = filelanguage.JavaScript
	FileLanguageTypeScript = filelanguage.TypeScript
	FileLanguageJSON       = filelanguage.JSON
	FileLanguageYAML       = filelanguage.YAML
	FileLanguageSQL        = filelanguage.SQL
	FileLanguagePython     = filelanguage.Python
	FileLanguageShell      = filelanguage.Shell
	FileLanguagePowerShell = filelanguage.PowerShell
	FileLanguageBatch      = filelanguage.Batch
	FileLanguageMarkdown   = filelanguage.Markdown
	FileLanguagePlainText  = filelanguage.PlainText
)

func WorkspaceFileLanguageFromPath(path string) WorkspaceFileLanguage {
	return filelanguage.FromPath(path)
}
