// Package buildinfo exposes release metadata injected by the native build
// scripts. The defaults keep local `go run` builds useful.
package buildinfo

import (
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

const ProductName = "silverwing-qa"

// These variables are overridden by release builds with -ldflags -X.
var (
	Version   = domain.CurrentProductVersion
	Commit    = "unknown"
	BuildTime = "unknown"
)

// FullVersion returns the version and, when available, the source revision.
func FullVersion() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	commit := strings.TrimSpace(Commit)
	if commit == "" || commit == "unknown" {
		return version
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return version + " (" + commit + ")"
}

// ResourceRoot returns the packaged resource directory for an executable.
// Portable Windows/Linux layouts put resources beside the executable under
// resources/. macOS applications place the executable in Contents/MacOS and
// resources in Contents/Resources.
func ResourceRoot(executable string) string {
	dir := filepath.Dir(filepath.Clean(executable))
	if strings.EqualFold(filepath.Base(dir), "MacOS") && strings.EqualFold(filepath.Base(filepath.Dir(dir)), "Contents") {
		return filepath.Join(filepath.Dir(dir), "Resources")
	}
	return filepath.Join(dir, "resources")
}
