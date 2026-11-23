//go:build darwin

package profilelaunch

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func shortcutExtension() string { return ".app" }

func writeNativeShortcut(path string, spec Shortcut) error {
	if !filepath.IsAbs(spec.Executable) {
		return errors.New("shortcut target executable must be an absolute path")
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("shortcut staging path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect shortcut staging path: %w", err)
	}

	contents := filepath.Join(path, "Contents")
	macOS := filepath.Join(contents, "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		return fmt.Errorf("create Finder application bundle: %w", err)
	}
	launcher := filepath.Join(macOS, "launcher")
	if err := os.WriteFile(launcher, macOSLauncher(spec), 0o755); err != nil {
		return fmt.Errorf("write Finder application launcher: %w", err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), macOSInfoPlist(spec), 0o644); err != nil {
		return fmt.Errorf("write Finder application metadata: %w", err)
	}
	return nil
}

func macOSLauncher(spec Shortcut) []byte {
	var launcher bytes.Buffer
	launcher.WriteString("#!/bin/sh\nTARGET=")
	launcher.WriteString(quotePOSIXShellArgument(spec.Executable))
	launcher.WriteString("\nif [ ! -x \"$TARGET\" ]; then\n")
	launcher.WriteString("  /usr/bin/osascript -e 'display alert \"SilverWing 启动失败\" message \"找不到原始程序，请重新创建此快捷方式。\"' >/dev/null 2>&1\n")
	launcher.WriteString("  exit 127\nfi\nexec \"$TARGET\"")
	for _, argument := range spec.args() {
		launcher.WriteByte(' ')
		launcher.WriteString(quotePOSIXShellArgument(argument))
	}
	launcher.WriteByte('\n')
	return launcher.Bytes()
}

func macOSInfoPlist(spec Shortcut) []byte {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = "SilverWing"
	}
	identifierHash := sha256.Sum256([]byte(spec.ProfileID))
	identifier := fmt.Sprintf("com.silverwing.profile.%x", identifierHash[:8])
	var plist bytes.Buffer
	plist.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	plist.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	plist.WriteString("<plist version=\"1.0\"><dict>\n")
	writePlistString(&plist, "CFBundleExecutable", "launcher")
	writePlistString(&plist, "CFBundleIdentifier", identifier)
	writePlistString(&plist, "CFBundleName", name)
	writePlistString(&plist, "CFBundleDisplayName", name)
	writePlistString(&plist, "CFBundlePackageType", "APPL")
	plist.WriteString("</dict></plist>\n")
	return plist.Bytes()
}

func writePlistString(plist *bytes.Buffer, key, value string) {
	plist.WriteString("<key>")
	plist.WriteString(escapeXMLText(key))
	plist.WriteString("</key><string>")
	plist.WriteString(escapeXMLText(value))
	plist.WriteString("</string>\n")
}

func escapeXMLText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "\"", "&quot;")
	return strings.ReplaceAll(value, "'", "&apos;")
}

func quotePOSIXShellArgument(argument string) string {
	return "'" + strings.ReplaceAll(argument, "'", "'\"'\"'") + "'"
}
