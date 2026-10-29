// Package productprofile exposes this build's fixed application identity.
// current.go is the only edition-specific definition; it is also read by the
// native packaging scripts. There is no runtime edition selection.
package productprofile

import "path/filepath"

const AutomaticUpdates = false

// These projections retain the existing public naming constant and repository URL.
const TaskNamingLabel = TaskKindLabel + "命名器"
const RepositoryURL = "https://github.com/" + ReleaseRepository

// DefaultHome returns the default application data directory.
func DefaultHome(userHome string) string {
	return filepath.Join(userHome, ExecutableName)
}

// DefaultTaskRoot preserves the developer task root and isolates the other builds.
// A caller-specified application home still owns all QA/product defaults.
func DefaultTaskRoot(userHome, appHome string) string {
	if TaskRootInHome {
		return filepath.Join(appHome, "tasks")
	}
	return filepath.Join(userHome, "tasks")
}

// DefaultMaterialsRoot is empty when this build has no configured default.
func DefaultMaterialsRoot(appHome string) string {
	if !TaskRootInHome {
		return ""
	}
	return filepath.Join(appHome, "materials")
}

// ReleasesURL opens only the current build's release channel.
func ReleasesURL() string {
	url := RepositoryURL + "/releases"
	if ReleaseTagPrefix != "v" {
		url += "?q=" + ReleaseTagPrefix + "&expanded=true"
	}
	return url
}

// AcceptsEdition is used only by schemas which persist an edition marker.
// Empty markers are never accepted; developer schemas continue to omit it.
func AcceptsEdition(edition string) bool {
	return Edition != "" && edition == Edition
}

func ConfigurationEditionError() string {
	return "配置不是 " + Name + " 格式，请使用 " + ConfigEditionLabel + "配置或备份"
}

func ManifestEditionError() string { return "任务不是 " + Name + " 格式" }

func ManifestWriteEditionError() string {
	return "不能写入非" + ManifestWriteLabel + " 任务清单"
}

// MigrationFormatLabel includes the spacing used by the existing journal error.
func MigrationEditionError() string {
	return "迁移日志不是" + MigrationFormatLabel + "格式"
}
