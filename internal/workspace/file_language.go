package workspace

import (
	"strings"
)

const MaxWorkspaceFilePreviewBytes = 512 * 1024

type WorkspaceFileLanguage string

const (
	FileLanguageJava       WorkspaceFileLanguage = "JAVA"
	FileLanguageKotlin     WorkspaceFileLanguage = "KOTLIN"
	FileLanguageGradle     WorkspaceFileLanguage = "GRADLE"
	FileLanguageProperties WorkspaceFileLanguage = "PROPERTIES"
	FileLanguageHTML       WorkspaceFileLanguage = "HTML"
	FileLanguageCSS        WorkspaceFileLanguage = "CSS"
	FileLanguageJavaScript WorkspaceFileLanguage = "JAVASCRIPT"
	FileLanguageTypeScript WorkspaceFileLanguage = "TYPESCRIPT"
	FileLanguageJSON       WorkspaceFileLanguage = "JSON"
	FileLanguageYAML       WorkspaceFileLanguage = "YAML"
	FileLanguageSQL        WorkspaceFileLanguage = "SQL"
	FileLanguagePython     WorkspaceFileLanguage = "PYTHON"
	FileLanguageShell      WorkspaceFileLanguage = "SHELL"
	FileLanguagePowerShell WorkspaceFileLanguage = "POWERSHELL"
	FileLanguageBatch      WorkspaceFileLanguage = "BATCH"
	FileLanguageMarkdown   WorkspaceFileLanguage = "MARKDOWN"
	FileLanguagePlainText  WorkspaceFileLanguage = "PLAIN_TEXT"
)

func WorkspaceFileLanguageFromPath(path string) WorkspaceFileLanguage {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, ".gradle"), strings.HasSuffix(p, ".gradle.kts"):
		return FileLanguageGradle
	case strings.HasSuffix(p, ".java"):
		return FileLanguageJava
	case strings.HasSuffix(p, ".kt"), strings.HasSuffix(p, ".kts"):
		return FileLanguageKotlin
	case strings.HasSuffix(p, ".properties"), strings.HasSuffix(p, ".ini"), strings.HasSuffix(p, ".conf"):
		return FileLanguageProperties
	case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, ".htm"), strings.HasSuffix(p, ".xml"), strings.HasSuffix(p, ".svg"):
		return FileLanguageHTML
	case strings.HasSuffix(p, ".css"), strings.HasSuffix(p, ".scss"), strings.HasSuffix(p, ".sass"), strings.HasSuffix(p, ".less"):
		return FileLanguageCSS
	case strings.HasSuffix(p, ".js"), strings.HasSuffix(p, ".jsx"):
		return FileLanguageJavaScript
	case strings.HasSuffix(p, ".ts"), strings.HasSuffix(p, ".tsx"):
		return FileLanguageTypeScript
	case strings.HasSuffix(p, ".json"):
		return FileLanguageJSON
	case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
		return FileLanguageYAML
	case strings.HasSuffix(p, ".sql"):
		return FileLanguageSQL
	case strings.HasSuffix(p, ".py"), strings.HasSuffix(p, ".pyw"), strings.HasSuffix(p, ".pyi"):
		return FileLanguagePython
	case strings.HasSuffix(p, ".ps1"), strings.HasSuffix(p, ".psm1"), strings.HasSuffix(p, ".psd1"):
		return FileLanguagePowerShell
	case strings.HasSuffix(p, ".bat"), strings.HasSuffix(p, ".cmd"):
		return FileLanguageBatch
	case strings.HasSuffix(p, ".sh"), strings.HasSuffix(p, ".bash"), strings.HasSuffix(p, ".zsh"):
		return FileLanguageShell
	case strings.HasSuffix(p, ".md"), strings.HasSuffix(p, ".markdown"):
		return FileLanguageMarkdown
	default:
		return FileLanguagePlainText
	}
}
