package domain

import "strings"

func IsLegacyCursorToolID(id string) bool {
	return strings.EqualFold(strings.TrimSpace(id), "cursor")
}

func WithoutLegacyCursorTool(ids []string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if !IsLegacyCursorToolID(id) {
			kept = append(kept, id)
		}
	}
	return kept
}

// NormalizeTaskManifest returns a copy whose non-null list fields use the
// serialized empty-list representation expected by Kotlin. Nil pointer fields
// remain nil because they are nullable in the persisted model.
func NormalizeTaskManifest(manifest TaskManifest) TaskManifest {
	manifest.Services = cloneNonNullSlice(manifest.Services)
	for i := range manifest.Services {
		manifest.Services[i].Warnings = cloneNonNullSlice(manifest.Services[i].Warnings)
	}
	manifest.WorkspaceToolLaunches = cloneNonNullSlice(manifest.WorkspaceToolLaunches)
	return manifest
}

func cloneNonNullSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}
