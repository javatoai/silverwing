// Package filelanguage classifies file syntax without workspace or UI dependencies.
package filelanguage

import (
	"path/filepath"
	"strings"
)

type Language string

const (
	Java       Language = "JAVA"
	Kotlin     Language = "KOTLIN"
	Gradle     Language = "GRADLE"
	Properties Language = "PROPERTIES"
	HTML       Language = "HTML"
	CSS        Language = "CSS"
	JavaScript Language = "JAVASCRIPT"
	TypeScript Language = "TYPESCRIPT"
	JSON       Language = "JSON"
	YAML       Language = "YAML"
	SQL        Language = "SQL"
	Python     Language = "PYTHON"
	Shell      Language = "SHELL"
	PowerShell Language = "POWERSHELL"
	Batch      Language = "BATCH"
	Markdown   Language = "MARKDOWN"
	PlainText  Language = "PLAIN_TEXT"
)

func FromPath(path string) Language {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, ".gradle"), strings.HasSuffix(p, ".gradle.kts"):
		return Gradle
	case strings.HasSuffix(p, ".java"):
		return Java
	case strings.HasSuffix(p, ".kt"), strings.HasSuffix(p, ".kts"):
		return Kotlin
	case strings.HasSuffix(p, ".properties"), strings.HasSuffix(p, ".ini"), strings.HasSuffix(p, ".conf"):
		return Properties
	case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, ".htm"), strings.HasSuffix(p, ".xml"), strings.HasSuffix(p, ".svg"):
		return HTML
	case strings.HasSuffix(p, ".css"), strings.HasSuffix(p, ".scss"), strings.HasSuffix(p, ".sass"), strings.HasSuffix(p, ".less"):
		return CSS
	case strings.HasSuffix(p, ".js"), strings.HasSuffix(p, ".jsx"):
		return JavaScript
	case strings.HasSuffix(p, ".ts"), strings.HasSuffix(p, ".tsx"):
		return TypeScript
	case strings.HasSuffix(p, ".json"):
		return JSON
	case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
		return YAML
	case strings.HasSuffix(p, ".sql"):
		return SQL
	case strings.HasSuffix(p, ".py"), strings.HasSuffix(p, ".pyw"), strings.HasSuffix(p, ".pyi"):
		return Python
	case strings.HasSuffix(p, ".ps1"), strings.HasSuffix(p, ".psm1"), strings.HasSuffix(p, ".psd1"):
		return PowerShell
	case strings.HasSuffix(p, ".bat"), strings.HasSuffix(p, ".cmd"):
		return Batch
	case strings.HasSuffix(p, ".sh"), strings.HasSuffix(p, ".bash"), strings.HasSuffix(p, ".zsh"):
		return Shell
	case strings.HasSuffix(p, ".md"), strings.HasSuffix(p, ".markdown"):
		return Markdown
	default:
		return PlainText
	}
}

// SourceEditorOverride preserves the shared editor's explicit tokenizer choices.
// Other filenames are resolved by the native editor before any typed fallback.
func SourceEditorOverride(filename string) Language {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".gradle":
		return Gradle
	case ".properties":
		return Properties
	case ".ps1", ".psm1", ".psd1":
		return PowerShell
	case ".bat", ".cmd":
		return Batch
	default:
		return ""
	}
}
