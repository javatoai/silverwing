package domain

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
