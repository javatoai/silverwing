// Package appupdate coordinates explicit application updates. It does not know
// about desktop controls, GitHub authentication or platform installer commands.
package appupdate

import "context"

type Kind string

const (
	Portable Kind = "portable"
	Setup    Kind = "setup"
	MSI      Kind = "msi"
	MacApp   Kind = "macos-app"
)

type Identity struct {
	Repository string `json:"repository"`
	TagPrefix  string `json:"tagPrefix"`
	Executable string `json:"executable"`
	Platform   string `json:"platform"`
	Version    string `json:"version"`
}

// Layout is emitted by packaging, with paths relative to the application root.
type Layout struct {
	Schema     int      `json:"schema"`
	Identity   Identity `json:"identity"`
	Executable string   `json:"executable"`
	Helper     string   `json:"helper"`
	Files      []File   `json:"files"`
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type Installation struct {
	Identity    Identity `json:"identity"`
	Kind        Kind     `json:"kind"`
	Root        string   `json:"root"`
	Executable  string   `json:"executable"`
	Home        string   `json:"home"`
	Helper      string   `json:"helper"`
	LayoutPath  string   `json:"layoutPath"`
	Layout      Layout   `json:"layout"`
	ProductCode string   `json:"productCode,omitempty"`
	UpgradeCode string   `json:"upgradeCode,omitempty"`
}

type Metadata struct {
	Schema          int      `json:"schema"`
	Identity        Identity `json:"identity"`
	Kind            Kind     `json:"kind"`
	Payload         string   `json:"payload"`
	Layout          *Layout  `json:"layout,omitempty"`
	ProductCode     string   `json:"productCode,omitempty"`
	UpgradeCode     string   `json:"upgradeCode,omitempty"`
	InstallerSHA256 string   `json:"installerSHA256,omitempty"`
}

type Bundle struct {
	Directory string   `json:"directory"`
	Archive   string   `json:"archive"`
	Size      int64    `json:"size"`
	Signature string   `json:"signature"`
	Metadata  Metadata `json:"metadata"`
}

type Progress func(downloaded, total int64)

type Source interface {
	Prepare(context.Context, Installation, string, string, Progress) (Bundle, error)
}

// Installer runs only in the external helper after the original process exits.
// Each implementation keeps the installation form, including MSI registration.
type Installer interface {
	Apply(context.Context, Plan) error
	Rollback(context.Context, Plan) error
}

type Snapshot struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Error      string `json:"error,omitempty"`
}

type Plan struct {
	Schema           int          `json:"schema"`
	ID               string       `json:"id"`
	Installation     Installation `json:"installation"`
	New              Bundle       `json:"new"`
	Recovery         *Bundle      `json:"recovery,omitempty"`
	Backup           string       `json:"backup"`
	ParentPID        int          `json:"parentPID"`
	LayoutDigest     string       `json:"layoutDigest"`
	ExecutableDigest string       `json:"executableDigest"`
	HealthToken      string       `json:"healthToken"`
}

func Target(platform string, kind Kind) string {
	if kind == MacApp {
		return "darwin-arm64-macos-app"
	}
	return "windows-amd64-" + string(kind)
}

func SameProduct(a, b Identity) bool {
	return a.Repository == b.Repository && a.TagPrefix == b.TagPrefix && a.Executable == b.Executable && a.Platform == b.Platform
}
