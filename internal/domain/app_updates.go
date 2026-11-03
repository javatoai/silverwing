package domain

import (
	"net/url"
	"strconv"
	"strings"
)

// UpdateCheckResult describes a published release, never an installed update.
type UpdateCheckResult struct {
	CurrentVersion   string         `json:"currentVersion"`
	ReleasesURL      string         `json:"releasesUrl"`
	Repository       string         `json:"repository"`
	TagPrefix        string         `json:"tagPrefix"`
	Platform         string         `json:"platform"`
	CheckedAt        string         `json:"checkedAt"`
	Release          *UpdateRelease `json:"release,omitempty"`
	InstallationKind string         `json:"installationKind,omitempty"`
	CanAutoUpdate    bool           `json:"canAutoUpdate,omitempty"`
	AutoUpdateReason string         `json:"autoUpdateReason,omitempty"`
}

type UpdateRelease struct {
	Version        string       `json:"version"`
	Tag            string       `json:"tag"`
	URL            string       `json:"url"`
	PublishedAt    string       `json:"publishedAt,omitempty"`
	Package        *UpdateAsset `json:"package,omitempty"`
	UpdateManifest *UpdateAsset `json:"updateManifest,omitempty"`
}

type UpdateAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// CompareReleaseVersions accepts stable, numeric major.minor.patch versions.
// Prerelease and unrecognized versions must not produce update notifications.
func CompareReleaseVersions(left, right string) (int, bool) {
	a, ok := releaseVersion(left)
	if !ok {
		return 0, false
	}
	b, ok := releaseVersion(right)
	if !ok {
		return 0, false
	}
	for i := range a {
		if a[i] < b[i] {
			return -1, true
		}
		if a[i] > b[i] {
			return 1, true
		}
	}
	return 0, true
}

func releaseVersion(value string) ([3]uint64, bool) {
	var numbers [3]uint64
	parts := strings.Split(value, ".")
	if len(parts) != len(numbers) {
		return numbers, false
	}
	for i, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return numbers, false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return numbers, false
			}
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return numbers, false
		}
		numbers[i] = number
	}
	return numbers, true
}

func (result UpdateCheckResult) Available(currentVersion, repository, tagPrefix string) bool {
	if !result.Valid(repository, tagPrefix) || result.Release == nil {
		return false
	}
	comparison, valid := CompareReleaseVersions(result.Release.Version, currentVersion)
	return valid && comparison > 0
}

// Valid also guards persisted receipts before offering an external link.
func (result UpdateCheckResult) Valid(repository, tagPrefix string) bool {
	if result.Repository != repository || result.TagPrefix != tagPrefix {
		return false
	}
	if result.Release == nil {
		return true
	}
	release := result.Release
	if _, valid := CompareReleaseVersions(release.Version, release.Version); !valid || release.Tag != tagPrefix+release.Version {
		return false
	}
	base := "https://github.com/" + repository + "/releases/"
	if release.URL != base+"tag/"+url.PathEscape(release.Tag) {
		return false
	}
	for _, asset := range []*UpdateAsset{release.Package, release.UpdateManifest} {
		if asset != nil {
			if asset.Size <= 0 || asset.Name == "" || strings.ContainsAny(asset.Name, "/\\") || asset.URL != base+"download/"+url.PathEscape(release.Tag)+"/"+url.PathEscape(asset.Name) {
				return false
			}
		}
	}
	return true
}
