package native

import (
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/appupdate"
)

// DownloadKind is read-only and remains available before signing is configured.
// Unknown registration never falls back to a different installer type.
func DownloadKind(executable string) appupdate.Kind {
	if runtime.GOOS == "darwin" {
		if strings.HasSuffix(filepath.Dir(filepath.Dir(filepath.Dir(executable))), ".app") {
			return appupdate.MacApp
		}
		return "manual"
	}
	if runtime.GOOS != "windows" {
		return "manual"
	}
	installation, err := detectWindows(appupdate.Installation{Root: filepath.Dir(executable), Executable: executable, Identity: Identity(), Kind: appupdate.Portable})
	if err != nil {
		return "manual"
	}
	return installation.Kind
}
